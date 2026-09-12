package service

import (
	"context"
	"sort"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/policy"
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
	result, err := s.solver.Resolve(ctx, state.Catalog, plan.Roots)
	if err != nil {
		return plan, err
	}
	changes := resolution.Diff(env.Resolved, result.Resolved)
	findings, policyID, policyRevision, err := evaluatePolicy(state, env, changes)
	if err != nil {
		return plan, err
	}
	// A plan that violates any bound rule must not become applicable.
	next := domain.Ready
	if !policy.Passed(findings) {
		next = domain.Draft
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
		if err := checkPolicyBinding(current, current.Environments[plan.EnvironmentID], policyID, policyRevision); err != nil {
			return err
		}
		latest.State = next
		latest.Resolved = result.Resolved
		latest.Changes = changes
		latest.CatalogRevision = result.CatalogRevision
		latest.PolicyID = policyID
		latest.PolicyRevision = policyRevision
		latest.PolicyFindings = findings
		latest.Revision++
		latest.UpdatedAt = now()
		current.Plans[id] = latest
		current.Record("plan", id, "validated", latest.UpdatedAt)
		updated = latest
		return nil
	})
	return updated, err
}
