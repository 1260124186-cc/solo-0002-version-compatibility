package service

import (
	"context"
	"strconv"

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

// CreateEnvironmentFromProfile solves the roots stored at the given profile
// revision against the current catalog. The profile can be saved without a
// feasible solution; this call returns no_solution in that case.
func (s *Service) CreateEnvironmentFromProfile(ctx context.Context, profileID string, input domain.EnvironmentFromProfileInput) (domain.Environment, error) {
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
	profile, exists := state.Profiles[profileID]
	if !exists {
		return domain.Environment{}, domain.Missing("profile", profileID)
	}
	if err := profile.RequireActive(); err != nil {
		return domain.Environment{}, err
	}
	revision := input.ProfileRevision
	if revision == 0 {
		revision = profile.Revision
	}
	snapshot, exists := profile.Revisions[revision]
	if !exists {
		return domain.Environment{}, domain.Missing("profile revision", profileID+"@"+strconv.FormatUint(revision, 10))
	}
	resolved, err := s.solver.Resolve(ctx, state.Catalog, snapshot.Roots)
	if err != nil {
		return domain.Environment{}, err
	}
	at := now()
	env := domain.Environment{
		ID:              input.ID,
		Name:            input.Name,
		Roots:           domain.CopyStrings(snapshot.Roots),
		Resolved:        resolved.Resolved,
		Revision:        1,
		ProfileID:       profileID,
		ProfileRevision: revision,
		CreatedAt:       at,
		UpdatedAt:       at,
	}
	err = s.repo.Update(ctx, func(current *repository.State) error {
		if err := checkCatalog(resolved.CatalogRevision, current); err != nil {
			return err
		}
		latest, exists := current.Profiles[profileID]
		if !exists {
			return domain.Missing("profile", profileID)
		}
		// A profile deactivated concurrently with this request must win.
		if err := latest.RequireActive(); err != nil {
			return err
		}
		if _, exists := latest.Revisions[revision]; !exists {
			return domain.Missing("profile revision", profileID+"@"+strconv.FormatUint(revision, 10))
		}
		if _, exists := current.Environments[input.ID]; exists {
			return domain.Conflict("environment already exists")
		}
		if len(current.Environments) >= 200 {
			return domain.Limit("environment capacity reached")
		}
		current.Environments[input.ID] = env
		current.Record("environment", input.ID, "created_from_profile", at)
		return nil
	})
	return env, err
}
