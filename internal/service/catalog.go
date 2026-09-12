package service

import (
	"context"
	"errors"
	"sort"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
	"solo-0002-version-compatibility/internal/semver"
)

// errComponentUnchanged reports a metadata edit that changes neither name nor
// description. It aborts the transaction without recording an event, so the
// caller simply receives the current component.
var errComponentUnchanged = errors.New("component metadata unchanged")

func (s *Service) CreateComponent(ctx context.Context, input domain.ComponentInput) (domain.Component, error) {
	if err := domain.ValidateComponent(input); err != nil {
		return domain.Component{}, err
	}
	component := domain.Component{ID: input.ID, Name: input.Name, Description: input.Description, Revision: 1, CreatedAt: now()}
	err := s.repo.Update(ctx, func(state *repository.State) error {
		if _, exists := state.Catalog.Components[input.ID]; exists {
			return domain.Conflict("component %s already exists", input.ID)
		}
		if len(state.Catalog.Components) >= domain.MaxComponents {
			return domain.Limit("component capacity reached")
		}
		state.Catalog.Components[input.ID] = component
		state.Catalog.Releases[input.ID] = make(map[string]domain.Release)
		state.Catalog.Revision++
		state.Record("component", input.ID, "created", component.CreatedAt)
		return nil
	})
	return component, err
}

func (s *Service) ListComponents(ctx context.Context) ([]domain.Component, uint64, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, 0, err
	}
	items := make([]domain.Component, 0, len(state.Catalog.Components))
	for _, id := range domain.SortedKeys(state.Catalog.Components) {
		items = append(items, state.Catalog.Components[id])
	}
	return items, state.Catalog.Revision, nil
}

func (s *Service) Component(ctx context.Context, id string) (domain.Component, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Component{}, err
	}
	component, exists := state.Catalog.Components[id]
	if !exists {
		return component, domain.Missing("component", id)
	}
	return component, nil
}

// UpdateComponent edits the descriptive metadata (name, description) under
// optimistic concurrency on the component's own revision. It deliberately
// does not touch Catalog.Revision: a rename is irrelevant to dependency
// solving, so resolution results and already validated plans stay valid.
func (s *Service) UpdateComponent(ctx context.Context, id string, input domain.ComponentUpdateInput) (domain.Component, error) {
	if err := domain.ValidateComponentUpdate(input); err != nil {
		return domain.Component{}, err
	}
	var updated domain.Component
	err := s.repo.Update(ctx, func(state *repository.State) error {
		component, exists := state.Catalog.Components[id]
		if !exists {
			return domain.Missing("component", id)
		}
		if component.Revision != input.Revision {
			return domain.Conflict("component revision is %d, received %d; refetch the component before editing", component.Revision, input.Revision)
		}
		if component.Name == input.Name && component.Description == input.Description {
			updated = component
			return errComponentUnchanged
		}
		at := now()
		component.Name = input.Name
		component.Description = input.Description
		component.Revision++
		component.UpdatedAt = &at
		state.Catalog.Components[id] = component
		// Catalog.Revision intentionally unchanged: only state metadata moved.
		state.Record("component", id, "updated", at)
		updated = component
		return nil
	})
	if errors.Is(err, errComponentUnchanged) {
		return updated, nil
	}
	return updated, err
}

func (s *Service) AddRelease(ctx context.Context, id string, input domain.ReleaseInput) (domain.Release, error) {
	if err := domain.ValidateRelease(input, id); err != nil {
		return domain.Release{}, err
	}
	release := domain.Release{ComponentID: id, Version: input.Version, Requires: domain.CopyStrings(input.Requires), State: domain.Available, CreatedAt: now()}
	err := s.repo.Update(ctx, func(state *repository.State) error {
		if _, exists := state.Catalog.Components[id]; !exists {
			return domain.Missing("component", id)
		}
		releases := state.Catalog.Releases[id]
		if _, exists := releases[input.Version]; exists {
			return domain.Conflict("release %s@%s already exists", id, input.Version)
		}
		if len(releases) >= domain.MaxReleases {
			return domain.Limit("release capacity reached for this component")
		}
		for _, dep := range domain.SortedKeys(input.Requires) {
			if _, exists := state.Catalog.Components[dep]; !exists {
				return domain.Missing("dependency component", dep)
			}
		}
		releases[input.Version] = release
		state.Catalog.Revision++
		state.Record("release", id+"@"+input.Version, "added", release.CreatedAt)
		return nil
	})
	return release, err
}

func (s *Service) ListReleases(ctx context.Context, id string) ([]domain.Release, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if _, exists := state.Catalog.Components[id]; !exists {
		return nil, domain.Missing("component", id)
	}
	items := make([]domain.Release, 0, len(state.Catalog.Releases[id]))
	for _, release := range state.Catalog.Releases[id] {
		items = append(items, release)
	}
	sort.Slice(items, func(i, j int) bool {
		a, _ := semver.Parse(items[i].Version)
		b, _ := semver.Parse(items[j].Version)
		return a.Compare(b) > 0
	})
	return items, nil
}

func (s *Service) WithdrawRelease(ctx context.Context, id, version string) (domain.Release, error) {
	var result domain.Release
	err := s.repo.Update(ctx, func(state *repository.State) error {
		release, exists := state.Catalog.Releases[id][version]
		if !exists {
			return domain.Missing("release", id+"@"+version)
		}
		if release.State != domain.Available {
			return domain.Conflict("release is already withdrawn")
		}
		for _, envID := range domain.SortedKeys(state.Environments) {
			if state.Environments[envID].Resolved[id] == version {
				return domain.Conflict("release is used by environment %s", envID)
			}
		}
		at := now()
		release.State = domain.Withdrawn
		release.WithdrawnAt = &at
		state.Catalog.Releases[id][version] = release
		state.Catalog.Revision++
		state.Record("release", id+"@"+version, "withdrawn", at)
		result = release
		return nil
	})
	return result, err
}
