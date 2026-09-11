package service

import (
	"context"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
	"solo-0002-version-compatibility/internal/resolution"
)

func (s *Service) CreateEnvironment(ctx context.Context, input domain.EnvironmentInput) (domain.Environment, error) {
	if err := domain.ValidateID(input.ID); err != nil {
		return domain.Environment{}, err
	}
	if err := domain.ValidateText(input.Name, "name", 1, 120); err != nil {
		return domain.Environment{}, err
	}
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Environment{}, err
	}
	if _, exists := state.Environments[input.ID]; exists {
		return domain.Environment{}, domain.Conflict("environment already exists")
	}
	resolved, err := s.solver.Resolve(ctx, state.Catalog, input.Roots)
	if err != nil {
		return domain.Environment{}, err
	}
	at := now()
	env := domain.Environment{ID: input.ID, Name: input.Name, Roots: domain.CopyStrings(input.Roots), Resolved: resolved.Resolved, Proof: resolved.Proof, Revision: 1, ProofStatus: domain.ProofCurrent, CreatedAt: at, UpdatedAt: at}
	err = s.repo.Update(ctx, func(current *repository.State) error {
		// Re-audit the proof inside the commit: it only stands if the catalog
		// did not move between the snapshot and the commit.
		if err := resolution.VerifyProof(current.Catalog, input.Roots, resolved.Resolved, resolved.Proof, true); err != nil {
			return err
		}
		if _, exists := current.Environments[input.ID]; exists {
			return domain.Conflict("environment already exists")
		}
		if len(current.Environments) >= 200 {
			return domain.Limit("environment capacity reached")
		}
		current.Environments[input.ID] = env
		current.Record("environment", input.ID, "created", at)
		return nil
	})
	return env, err
}

func (s *Service) ListEnvironments(ctx context.Context) ([]domain.Environment, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]domain.Environment, 0, len(state.Environments))
	for _, id := range domain.SortedKeys(state.Environments) {
		env := state.Environments[id]
		env.ProofStatus = domain.ProofStatusFor(state.Catalog.Revision, env.Proof)
		items = append(items, env)
	}
	return items, nil
}

func (s *Service) Environment(ctx context.Context, id string) (domain.Environment, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Environment{}, err
	}
	env, exists := state.Environments[id]
	if !exists {
		return env, domain.Missing("environment", id)
	}
	env.ProofStatus = domain.ProofStatusFor(state.Catalog.Revision, env.Proof)
	return env, nil
}
