package service

import (
	"context"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
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
	env := domain.Environment{ID: input.ID, Name: input.Name, Roots: domain.CopyStrings(input.Roots), Resolved: resolved.Resolved, Revision: 1, CreatedAt: at, UpdatedAt: at}
	err = s.repo.Update(ctx, func(current *repository.State) error {
		if err := checkCatalog(resolved.CatalogRevision, current); err != nil {
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

// DeriveEnvironment copies the source's exact roots and resolved set without
// re-resolving, so newer catalog versions are never picked. The source
// revision check and the copy happen inside one serialized update, which
// keeps a concurrent source change from leaking into a mixed result.
func (s *Service) DeriveEnvironment(ctx context.Context, sourceID string, input domain.DeriveInput) (domain.Environment, error) {
	if err := domain.ValidateID(input.ID); err != nil {
		return domain.Environment{}, err
	}
	if err := domain.ValidateText(input.Name, "name", 1, 120); err != nil {
		return domain.Environment{}, err
	}
	if input.SourceRevision == 0 {
		return domain.Environment{}, domain.Invalid("source_revision must be positive")
	}
	at := now()
	var env domain.Environment
	err := s.repo.Update(ctx, func(state *repository.State) error {
		source, exists := state.Environments[sourceID]
		if !exists {
			return domain.Missing("environment", sourceID)
		}
		if source.Revision != input.SourceRevision {
			return domain.Conflict("source environment revision is %d, expected %d; re-read the source and retry", source.Revision, input.SourceRevision)
		}
		if _, exists := state.Environments[input.ID]; exists {
			return domain.Conflict("environment already exists")
		}
		if len(state.Environments) >= 200 {
			return domain.Limit("environment capacity reached")
		}
		env = domain.Environment{
			ID:          input.ID,
			Name:        input.Name,
			Roots:       domain.CopyStrings(source.Roots),
			Resolved:    domain.CopyStrings(source.Resolved),
			Revision:    1,
			DerivedFrom: &domain.Derivation{SourceID: sourceID, SourceRevision: source.Revision},
			CreatedAt:   at,
			UpdatedAt:   at,
		}
		state.Environments[input.ID] = env
		state.Record("environment", input.ID, "derived", at)
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
		items = append(items, state.Environments[id])
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
	return env, nil
}
