package service

import (
	"context"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
)

type AppliedResult struct {
	Plan        domain.Plan        `json:"plan"`
	Environment domain.Environment `json:"environment"`
}

func (s *Service) ApplyPlan(ctx context.Context, id string, revision uint64) (AppliedResult, error) {
	var result AppliedResult
	err := s.repo.Update(ctx, func(state *repository.State) error {
		plan, exists := state.Plans[id]
		if !exists {
			return domain.Missing("plan", id)
		}
		if err := plan.CheckNotCancelled("apply"); err != nil {
			return err
		}
		if err := plan.CheckRevision(revision); err != nil {
			return err
		}
		if plan.State != domain.Ready {
			return domain.Conflict("only a ready plan can be applied")
		}
		if err := checkCatalog(plan.CatalogRevision, state); err != nil {
			return err
		}
		env := state.Environments[plan.EnvironmentID]
		if err := checkEnvironment(env, plan.BaseRevision); err != nil {
			return err
		}
		if err := repository.ValidateSelection(state.Catalog, plan.Roots, plan.Resolved, true); err != nil {
			return err
		}
		at := now()
		env.Roots = domain.CopyStrings(plan.Roots)
		env.Resolved = domain.CopyStrings(plan.Resolved)
		env.Revision++
		env.UpdatedAt = at
		plan.State = domain.Applied
		plan.Revision++
		plan.UpdatedAt = at
		state.Environments[env.ID] = env
		state.Plans[id] = plan
		state.Record("plan", id, "applied", "", at)
		result = AppliedResult{Plan: plan, Environment: env}
		return nil
	})
	return result, err
}

func (s *Service) CancelPlan(ctx context.Context, id string, input domain.ReasonInput) (domain.Plan, error) {
	var result domain.Plan
	err := s.repo.Update(ctx, func(state *repository.State) error {
		plan, exists := state.Plans[id]
		if !exists {
			return domain.Missing("plan", id)
		}
		// A cancelled plan is terminal: report the conflict before looking
		// at the reason, so repeated cancels always return 409.
		if err := plan.CheckNotCancelled("cancel"); err != nil {
			return err
		}
		if err := domain.ValidateReason(input.Reason); err != nil {
			return err
		}
		if err := plan.CheckRevision(input.Revision); err != nil {
			return err
		}
		if err := plan.CanCancel(); err != nil {
			return err
		}
		plan.State = domain.Cancelled
		plan.CancelReason = input.Reason
		plan.Revision++
		plan.UpdatedAt = now()
		state.Plans[id] = plan
		state.Record("plan", id, "cancelled", input.Reason, plan.UpdatedAt)
		result = plan
		return nil
	})
	return result, err
}

// CorrectPlan appends an immutable correction to the recorded cancel reason.
func (s *Service) CorrectPlan(ctx context.Context, id string, input domain.ReasonInput) (domain.Plan, error) {
	if err := domain.ValidateReason(input.Reason); err != nil {
		return domain.Plan{}, err
	}
	var result domain.Plan
	err := s.repo.Update(ctx, func(state *repository.State) error {
		plan, exists := state.Plans[id]
		if !exists {
			return domain.Missing("plan", id)
		}
		if plan.State != domain.Cancelled {
			return domain.Conflict("only a cancelled plan can be corrected")
		}
		if err := plan.CheckRevision(input.Revision); err != nil {
			return err
		}
		at := now()
		plan.Corrections = append(plan.Corrections, domain.Correction{Reason: input.Reason, At: at})
		plan.Revision++
		plan.UpdatedAt = at
		state.Plans[id] = plan
		state.Record("plan", id, "corrected", input.Reason, at)
		result = plan
		return nil
	})
	return result, err
}
