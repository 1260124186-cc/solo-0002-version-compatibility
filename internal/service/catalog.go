package service

import (
	"context"
	"sort"
	"strings"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
	"solo-0002-version-compatibility/internal/semver"
)

func (s *Service) CreateComponent(ctx context.Context, input domain.ComponentInput) (domain.Component, error) {
	if err := domain.ValidateComponent(input); err != nil {
		return domain.Component{}, err
	}
	component := domain.Component{ID: input.ID, Name: input.Name, Description: input.Description, CreatedAt: now()}
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

// RenameComponent moves a component to an unoccupied identifier and rewrites
// every stored reference in the same atomic commit: dependency constraints,
// environment and plan roots and selections, plan changes, and past events.
// The catalog revision advances so in-flight resolutions computed against the
// old identifier fail their commit-time revision check, and ready plans must
// be revalidated before they can be applied. Environment and plan revisions
// stay unchanged because the selected versions themselves do not change.
func (s *Service) RenameComponent(ctx context.Context, id string, input domain.RenameInput) (domain.Component, error) {
	if err := domain.ValidateID(input.NewID); err != nil {
		return domain.Component{}, err
	}
	if input.NewID == id {
		return domain.Component{}, domain.Invalid("new identifier must differ from the current identifier")
	}
	var renamed domain.Component
	err := s.repo.Update(ctx, func(state *repository.State) error {
		component, exists := state.Catalog.Components[id]
		if !exists {
			return domain.Missing("component", id)
		}
		if _, occupied := state.Catalog.Components[input.NewID]; occupied {
			return domain.Conflict("component %s already exists", input.NewID)
		}
		component.ID = input.NewID
		delete(state.Catalog.Components, id)
		state.Catalog.Components[input.NewID] = component
		releases := state.Catalog.Releases[id]
		delete(state.Catalog.Releases, id)
		state.Catalog.Releases[input.NewID] = releases
		for version, release := range releases {
			release.ComponentID = input.NewID
			releases[version] = release
		}
		for _, collection := range state.Catalog.Releases {
			for version, release := range collection {
				if _, refers := release.Requires[id]; refers {
					renameReference(release.Requires, id, input.NewID)
					collection[version] = release
				}
			}
		}
		for envID, env := range state.Environments {
			renameReference(env.Roots, id, input.NewID)
			renameReference(env.Resolved, id, input.NewID)
			state.Environments[envID] = env
		}
		for planID, plan := range state.Plans {
			renameReference(plan.Roots, id, input.NewID)
			renameReference(plan.Resolved, id, input.NewID)
			for i := range plan.Changes {
				if plan.Changes[i].ComponentID == id {
					plan.Changes[i].ComponentID = input.NewID
				}
			}
			state.Plans[planID] = plan
		}
		for i, event := range state.Events {
			if event.Kind == "component" && event.EntityID == id {
				state.Events[i].EntityID = input.NewID
			} else if event.Kind == "release" && strings.HasPrefix(event.EntityID, id+"@") {
				state.Events[i].EntityID = input.NewID + event.EntityID[len(id):]
			}
		}
		state.Catalog.Revision++
		state.Record("component", input.NewID, "renamed", now(), id)
		renamed = component
		return nil
	})
	return renamed, err
}

// renameReference retires oldID from a constraint or selection map. The new
// identifier can never collide with an existing key: stored references only
// point at existing components and the rename target was verified unoccupied.
func renameReference(values map[string]string, oldID, newID string) {
	if value, refers := values[oldID]; refers {
		delete(values, oldID)
		values[newID] = value
	}
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
