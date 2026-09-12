package service

import (
	"context"
	"sort"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
	"solo-0002-version-compatibility/internal/resolution"
)

// PlanView pairs a stored plan with its staleness indicator. The flag is
// derived from current revisions at read time and is never persisted.
type PlanView struct {
	domain.Plan
	Stale bool `json:"stale"`
}

func viewOf(plan domain.Plan, state *repository.State) PlanView {
	env := state.Environments[plan.EnvironmentID]
	return PlanView{Plan: plan, Stale: plan.Stale(env.Revision, state.Catalog.Revision)}
}

func (s *Service) CreatePlan(ctx context.Context, input domain.PlanInput) (PlanView, error) {
	if err := domain.ValidateRequirements(input.Roots, false); err != nil {
		return PlanView{}, err
	}
	if err := domain.ValidateText(input.Reason, "reason", 1, 1000); err != nil {
		return PlanView{}, err
	}
	if input.BaseRevision == 0 {
		return PlanView{}, domain.Invalid("base_revision must be positive")
	}
	id, err := freshID()
	if err != nil {
		return PlanView{}, err
	}
	at := now()
	plan := domain.Plan{ID: id, EnvironmentID: input.EnvironmentID, BaseRevision: input.BaseRevision, Revision: 1, Roots: domain.CopyStrings(input.Roots), Resolved: make(map[string]string), Changes: make([]domain.Change, 0), State: domain.Draft, Reason: input.Reason, CreatedAt: at, UpdatedAt: at}
	var result PlanView
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
		plan.CatalogRevision = state.Catalog.Revision
		state.Plans[id] = plan
		state.Record("plan", id, "created", at)
		result = viewOf(plan, state)
		return nil
	})
	return result, err
}

func (s *Service) Plan(ctx context.Context, id string) (PlanView, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return PlanView{}, err
	}
	plan, exists := state.Plans[id]
	if !exists {
		return PlanView{}, domain.Missing("plan", id)
	}
	return viewOf(plan, state), nil
}

func (s *Service) ListPlans(ctx context.Context, environmentID, phase string, stale *bool) ([]PlanView, error) {
	if phase != "" && phase != domain.Draft && phase != domain.Ready && phase != domain.Applied && phase != domain.Cancelled && phase != domain.Expired {
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
	items := make([]PlanView, 0)
	for _, plan := range state.Plans {
		if environmentID != "" && plan.EnvironmentID != environmentID {
			continue
		}
		if phase != "" && plan.State != phase {
			continue
		}
		view := viewOf(plan, state)
		if stale != nil && view.Stale != *stale {
			continue
		}
		items = append(items, view)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *Service) ValidatePlan(ctx context.Context, id string, revision uint64) (PlanView, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return PlanView{}, err
	}
	plan, exists := state.Plans[id]
	if !exists {
		return PlanView{}, domain.Missing("plan", id)
	}
	if err := plan.CheckRevision(revision); err != nil {
		return PlanView{}, err
	}
	if err := plan.CanValidate(); err != nil {
		return PlanView{}, err
	}
	env := state.Environments[plan.EnvironmentID]
	if err := checkEnvironment(env, plan.BaseRevision); err != nil {
		return PlanView{}, err
	}
	result, err := s.solver.Resolve(ctx, state.Catalog, plan.Roots)
	if err != nil {
		return PlanView{}, err
	}
	changes := resolution.Diff(env.Resolved, result.Resolved)
	var updated PlanView
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
		updated = viewOf(latest, current)
		return nil
	})
	return updated, err
}

// ExpirePlan marks a stale draft or ready plan as expired. Expiry is always
// this explicit call, never a side effect of other operations. The plan
// content is kept as history; only the state, revision and timestamp change,
// and an "expired" event is recorded for traceability.
func (s *Service) ExpirePlan(ctx context.Context, id string, revision uint64) (PlanView, error) {
	var result PlanView
	err := s.repo.Update(ctx, func(state *repository.State) error {
		plan, exists := state.Plans[id]
		if !exists {
			return domain.Missing("plan", id)
		}
		if err := plan.CheckRevision(revision); err != nil {
			return err
		}
		if err := plan.CanExpire(); err != nil {
			return err
		}
		env := state.Environments[plan.EnvironmentID]
		if !plan.Stale(env.Revision, state.Catalog.Revision) {
			return domain.Conflict("plan is still current; only a stale plan can be expired")
		}
		plan.State = domain.Expired
		plan.Revision++
		plan.UpdatedAt = now()
		state.Plans[id] = plan
		state.Record("plan", id, "expired", plan.UpdatedAt)
		result = viewOf(plan, state)
		return nil
	})
	return result, err
}
