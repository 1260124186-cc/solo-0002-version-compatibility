package service

import (
	"context"
	"sort"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
	"solo-0002-version-compatibility/internal/resolution"
)

func (s *Service) CreatePlan(ctx context.Context, input domain.PlanInput) (domain.Plan, error) {
	if err := domain.ValidateRequirements(input.Roots, false); err != nil {
		return domain.Plan{}, err
	}
	if err := domain.ValidateText(input.Reason, "reason", 1, 1000); err != nil {
		return domain.Plan{}, err
	}
	if input.BaseRevision == 0 {
		return domain.Plan{}, domain.Invalid("base_revision must be positive")
	}
	id, err := freshID()
	if err != nil {
		return domain.Plan{}, err
	}
	at := now()
	plan := domain.Plan{ID: id, EnvironmentID: input.EnvironmentID, BaseRevision: input.BaseRevision, Revision: 1, Roots: domain.CopyStrings(input.Roots), Resolved: make(map[string]string), Changes: make([]domain.Change, 0), State: domain.Draft, Reason: input.Reason, CreatedAt: at, UpdatedAt: at}
	err = s.repo.Update(ctx, func(state *repository.State) error {
		env, exists := state.Environments[input.EnvironmentID]
		if !exists {
			return domain.Missing("environment", input.EnvironmentID)
		}
		if err := checkEnvironment(env, input.BaseRevision); err != nil {
			return err
		}
		if len(state.Plans) >= 5000 {
			return domain.Limit("plan capacity reached")
		}
		for _, component := range domain.SortedKeys(input.Roots) {
			if _, exists := state.Catalog.Components[component]; !exists {
				return domain.Missing("component", component)
			}
		}
		state.Plans[id] = plan
		state.Record("plan", id, "created", at)
		return nil
	})
	return plan, err
}

func (s *Service) Plan(ctx context.Context, id string) (domain.Plan, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Plan{}, err
	}
	plan, exists := state.Plans[id]
	if !exists {
		return plan, domain.Missing("plan", id)
	}
	return plan, nil
}

func (s *Service) ListPlans(ctx context.Context, environmentID, phase string) ([]domain.Plan, error) {
	if phase != "" && phase != domain.Draft && phase != domain.Ready && phase != domain.Applied && phase != domain.Cancelled {
		return nil, domain.Invalid("unknown plan state")
	}
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if environmentID != "" {
		if _, exists := state.Environments[environmentID]; !exists {
			return nil, domain.Missing("environment", environmentID)
		}
	}
	items := make([]domain.Plan, 0)
	for _, plan := range state.Plans {
		if environmentID != "" && plan.EnvironmentID != environmentID {
			continue
		}
		if phase != "" && plan.State != phase {
			continue
		}
		items = append(items, plan)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

// solveChanges is the single computation shared by plan preview and plan
// validation, so a preview shows exactly what validating the same roots
// against the same catalog and environment revisions would produce.
func (s *Service) solveChanges(ctx context.Context, catalog domain.Catalog, env domain.Environment, roots map[string]string) (domain.Resolution, []domain.Change, error) {
	result, err := s.solver.Resolve(ctx, catalog, roots)
	if err != nil {
		return domain.Resolution{}, nil, err
	}
	return result, resolution.Diff(env.Resolved, result.Resolved), nil
}

// PreviewPlan solves the requested roots against the current catalog and the
// environment at the given revision. It only reads a state snapshot: no plan
// is created, no event is recorded and nothing is persisted.
func (s *Service) PreviewPlan(ctx context.Context, input domain.PlanPreviewInput) (domain.PlanPreview, error) {
	if err := domain.ValidateRequirements(input.Roots, false); err != nil {
		return domain.PlanPreview{}, err
	}
	if input.BaseRevision == 0 {
		return domain.PlanPreview{}, domain.Invalid("base_revision must be positive")
	}
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.PlanPreview{}, err
	}
	env, exists := state.Environments[input.EnvironmentID]
	if !exists {
		return domain.PlanPreview{}, domain.Missing("environment", input.EnvironmentID)
	}
	if err := checkEnvironment(env, input.BaseRevision); err != nil {
		return domain.PlanPreview{}, err
	}
	result, changes, err := s.solveChanges(ctx, state.Catalog, env, input.Roots)
	if err != nil {
		return domain.PlanPreview{}, err
	}
	return domain.PlanPreview{EnvironmentID: env.ID, BaseRevision: env.Revision, CatalogRevision: result.CatalogRevision, Roots: domain.CopyStrings(input.Roots), Resolved: result.Resolved, Changes: changes}, nil
}

func (s *Service) ValidatePlan(ctx context.Context, id string, revision uint64) (domain.Plan, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Plan{}, err
	}
	plan, exists := state.Plans[id]
	if !exists {
		return plan, domain.Missing("plan", id)
	}
	if err := plan.CheckRevision(revision); err != nil {
		return plan, err
	}
	if err := plan.CanValidate(); err != nil {
		return plan, err
	}
	env := state.Environments[plan.EnvironmentID]
	if err := checkEnvironment(env, plan.BaseRevision); err != nil {
		return plan, err
	}
	result, changes, err := s.solveChanges(ctx, state.Catalog, env, plan.Roots)
	if err != nil {
		return plan, err
	}
	var updated domain.Plan
	err = s.repo.Update(ctx, func(current *repository.State) error {
		latest := current.Plans[id]
		if err := latest.CheckRevision(revision); err != nil {
			return err
		}
		if err := latest.CanValidate(); err != nil {
			return err
		}
		if err := checkCatalog(result.CatalogRevision, current); err != nil {
			return err
		}
		if err := checkEnvironment(current.Environments[plan.EnvironmentID], plan.BaseRevision); err != nil {
			return err
		}
		latest.State = domain.Ready
		latest.Resolved = result.Resolved
		latest.Changes = changes
		latest.CatalogRevision = result.CatalogRevision
		latest.Revision++
		latest.UpdatedAt = now()
		current.Plans[id] = latest
		current.Record("plan", id, "validated", latest.UpdatedAt)
		updated = latest
		return nil
	})
	return updated, err
}
