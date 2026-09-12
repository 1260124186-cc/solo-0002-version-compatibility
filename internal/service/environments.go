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
	env := domain.Environment{ID: input.ID, Name: input.Name, Roots: domain.CopyStrings(input.Roots), Resolved: resolved.Resolved, Revision: 1, NameRevision: 1, CreatedAt: at, UpdatedAt: at}
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

// RenameEnvironment changes only the display name. Roots, Resolved and
// Revision are left untouched so plans based on the environment revision
// remain valid; the name revision alone advances and guards concurrency.
func (s *Service) RenameEnvironment(ctx context.Context, id string, input domain.RenameInput) (domain.Environment, error) {
	if err := domain.ValidateText(input.Name, "name", 1, 120); err != nil {
		return domain.Environment{}, err
	}
	if input.Revision == 0 {
		return domain.Environment{}, domain.Invalid("revision must be positive")
	}
	var renamed domain.Environment
	err := s.repo.Update(ctx, func(state *repository.State) error {
		env, exists := state.Environments[id]
		if !exists {
			return domain.Missing("environment", id)
		}
		if env.NameRevision != input.Revision {
			return domain.Conflict("environment name revision is %d, received %d", env.NameRevision, input.Revision)
		}
		at := now()
		env.Name = input.Name
		env.NameRevision++
		env.UpdatedAt = at
		state.Environments[id] = env
		state.Record("environment", id, "renamed", at)
		renamed = env
		return nil
	})
	return renamed, err
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
