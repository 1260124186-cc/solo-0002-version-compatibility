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
		if reasons := releaseUsage(state, id, version); len(reasons) > 0 {
			return domain.ConflictWith(reasons, "release is %s", reasons[0])
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

type WithdrawnBatch struct {
	ComponentID     string           `json:"component_id"`
	Releases        []domain.Release `json:"releases"`
	CatalogRevision uint64           `json:"catalog_revision"`
}

// WithdrawReleases withdraws several releases of one component atomically:
// every version is checked on its own, any blocker rejects the whole batch,
// and an accepted batch commits one catalog revision with one event per version.
func (s *Service) WithdrawReleases(ctx context.Context, id string, input domain.BatchWithdrawInput) (WithdrawnBatch, error) {
	if err := domain.ValidateVersionList(input.Versions); err != nil {
		return WithdrawnBatch{}, err
	}
	versions := make([]string, len(input.Versions))
	copy(versions, input.Versions)
	sort.Slice(versions, func(i, j int) bool {
		a, _ := semver.Parse(versions[i])
		b, _ := semver.Parse(versions[j])
		return a.Compare(b) > 0
	})
	var result WithdrawnBatch
	err := s.repo.Update(ctx, func(state *repository.State) error {
		if _, exists := state.Catalog.Components[id]; !exists {
			return domain.Missing("component", id)
		}
		releases := state.Catalog.Releases[id]
		for _, version := range versions {
			if _, exists := releases[version]; !exists {
				return domain.Missing("release", id+"@"+version)
			}
		}
		blocked := 0
		conflicts := make([]string, 0)
		for _, version := range versions {
			release := releases[version]
			if release.State != domain.Available {
				blocked++
				conflicts = append(conflicts, id+"@"+version+": already withdrawn")
				continue
			}
			if reasons := releaseUsage(state, id, version); len(reasons) > 0 {
				blocked++
				conflicts = append(conflicts, id+"@"+version+": "+strings.Join(reasons, "; "))
			}
		}
		if blocked > 0 {
			return domain.ConflictWith(conflicts, "%d of %d releases cannot be withdrawn; the batch was rejected", blocked, len(versions))
		}
		at := now()
		withdrawn := make([]domain.Release, 0, len(versions))
		for _, version := range versions {
			release := releases[version]
			release.State = domain.Withdrawn
			release.WithdrawnAt = &at
			releases[version] = release
			state.Record("release", id+"@"+version, "withdrawn", at)
			withdrawn = append(withdrawn, release)
		}
		state.Catalog.Revision++
		result = WithdrawnBatch{ComponentID: id, Releases: withdrawn, CatalogRevision: state.Catalog.Revision}
		return nil
	})
	return result, err
}

// releaseUsage lists why a release cannot be withdrawn: every environment
// whose resolved set selects it and every ready plan that would apply it.
func releaseUsage(state *repository.State, id, version string) []string {
	var reasons []string
	for _, envID := range domain.SortedKeys(state.Environments) {
		if state.Environments[envID].Resolved[id] == version {
			reasons = append(reasons, "used by environment "+envID)
		}
	}
	for _, planID := range domain.SortedKeys(state.Plans) {
		plan := state.Plans[planID]
		if plan.State == domain.Ready && plan.Resolved[id] == version {
			reasons = append(reasons, "used by ready plan "+planID)
		}
	}
	return reasons
}
