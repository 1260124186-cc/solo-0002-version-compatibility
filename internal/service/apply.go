package service

import (
	"context"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
	"solo-0002-version-compatibility/internal/resolution"
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
		if err := plan.CheckRevision(revision); err != nil {
			return err
		}
		if plan.State != domain.Ready {
			return domain.Conflict("only a ready plan can be applied")
		}
		env := state.Environments[plan.EnvironmentID]
		if err := checkEnvironment(env, plan.BaseRevision); err != nil {
			return err
		}
		// The stored proof is the single source of truth for applicability:
		// it must still be bound to the current catalog, every release it
		// names must still be available, and the whole selection chain must
		// re-derive from the catalog. A catalog change fails here and the
		// plan has to be revalidated.
		if err := resolution.VerifyProof(state.Catalog, plan.Roots, plan.Resolved, plan.Proof, true); err != nil {
			return err
		}
		at := now()
		env.Roots = domain.CopyStrings(plan.Roots)
		env.Resolved = domain.CopyStrings(plan.Resolved)
		env.Proof = plan.Proof
		env.Revision++
		env.UpdatedAt = at
		env.ProofStatus = domain.ProofCurrent
		plan.State = domain.Applied
		plan.Revision++
		plan.UpdatedAt = at
		plan.ProofStatus = domain.ProofCurrent
		state.Environments[env.ID] = env
		state.Plans[id] = plan
		state.Record("plan", id, "applied", at)
		result = AppliedResult{Plan: plan, Environment: env}
		return nil
	})
	return result, err
}

func (s *Service) CancelPlan(ctx context.Context, id string, revision uint64) (domain.Plan, error) {
	var result domain.Plan
	err := s.repo.Update(ctx, func(state *repository.State) error {
		plan, exists := state.Plans[id]
		if !exists {
			return domain.Missing("plan", id)
		}
		if err := plan.CheckRevision(revision); err != nil {
			return err
		}
		if err := plan.CanCancel(); err != nil {
			return err
		}
		plan.State = domain.Cancelled
		plan.Revision++
		plan.UpdatedAt = now()
		plan.ProofStatus = domain.ProofStatusFor(state.Catalog.Revision, plan.Proof)
		state.Plans[id] = plan
		state.Record("plan", id, "cancelled", plan.UpdatedAt)
		result = plan
		return nil
	})
	return result, err
}
