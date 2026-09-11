package service

import (
	"context"
	"sort"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
)

// CreateProfile validates and stores a reusable root constraint set. It never
// solves the catalog, so a saved profile can still be unsatisfiable.
func (s *Service) CreateProfile(ctx context.Context, input domain.ProfileInput) (domain.Profile, error) {
	if err := domain.ValidateProfile(input); err != nil {
		return domain.Profile{}, err
	}
	at := now()
	roots := domain.CopyStrings(input.Roots)
	profile := domain.Profile{
		ID:          input.ID,
		Name:        input.Name,
		Description: input.Description,
		Roots:       roots,
		Revision:    1,
		State:       domain.ProfileActive,
		CreatedAt:   at,
		UpdatedAt:   at,
	}
	snapshot := domain.ProfileSnapshot{Revision: 1, Name: input.Name, Description: input.Description, Roots: roots, CreatedAt: at}
	profile.Revisions = map[uint64]domain.ProfileSnapshot{1: snapshot}
	err := s.repo.Update(ctx, func(state *repository.State) error {
		if _, exists := state.Profiles[input.ID]; exists {
			return domain.Conflict("profile already exists")
		}
		if len(state.Profiles) >= domain.MaxProfiles {
			return domain.Limit("profile capacity reached")
		}
		state.Profiles[input.ID] = profile
		state.Record("profile", input.ID, "created", at)
		return nil
	})
	return profile, err
}

func (s *Service) Profile(ctx context.Context, id string) (domain.Profile, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Profile{}, err
	}
	profile, exists := state.Profiles[id]
	if !exists {
		return profile, domain.Missing("profile", id)
	}
	return profile, nil
}

func (s *Service) ListProfiles(ctx context.Context) ([]domain.Profile, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]domain.Profile, 0, len(state.Profiles))
	for _, id := range domain.SortedKeys(state.Profiles) {
		items = append(items, state.Profiles[id])
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

// UpdateProfile replaces the name, description and roots. Constraints are only
// syntax-checked; previously generated environments keep their own snapshot.
func (s *Service) UpdateProfile(ctx context.Context, id string, input domain.ProfileUpdateInput) (domain.Profile, error) {
	if err := domain.ValidateProfileUpdate(input); err != nil {
		return domain.Profile{}, err
	}
	var updated domain.Profile
	err := s.repo.Update(ctx, func(state *repository.State) error {
		profile, exists := state.Profiles[id]
		if !exists {
			return domain.Missing("profile", id)
		}
		if err := profile.CheckRevision(input.Revision); err != nil {
			return err
		}
		if profile.State != domain.ProfileActive {
			return domain.Conflict("cannot edit a deactivated profile")
		}
		if len(profile.Revisions) >= domain.MaxProfileRevisions {
			return domain.Limit("profile revision capacity reached")
		}
		at := now()
		profile.Name = input.Name
		profile.Description = input.Description
		profile.Roots = domain.CopyStrings(input.Roots)
		profile.Revision++
		profile.UpdatedAt = at
		profile.Revisions[profile.Revision] = profile.Snapshot(at)
		state.Profiles[id] = profile
		state.Record("profile", id, "updated", at)
		updated = profile
		return nil
	})
	return updated, err
}

// DeactivateProfile retires a profile. It stays readable and existing
// environments keep working, but it can no longer generate environments.
func (s *Service) DeactivateProfile(ctx context.Context, id string, revision uint64) (domain.Profile, error) {
	var updated domain.Profile
	err := s.repo.Update(ctx, func(state *repository.State) error {
		profile, exists := state.Profiles[id]
		if !exists {
			return domain.Missing("profile", id)
		}
		if err := profile.CheckRevision(revision); err != nil {
			return err
		}
		if profile.State != domain.ProfileActive {
			return domain.Conflict("profile is already deactivated")
		}
		at := now()
		profile.State = domain.ProfileInactive
		profile.InactiveAt = &at
		profile.Revision++
		profile.UpdatedAt = at
		// The new revision records the content as it was at deactivation so
		// the history stays a contiguous sequence.
		profile.Revisions[profile.Revision] = profile.Snapshot(at)
		state.Profiles[id] = profile
		state.Record("profile", id, "deactivated", at)
		updated = profile
		return nil
	})
	return updated, err
}
