package locking

import (
	"time"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/resolution"
)

// Build creates an unsigned lockfile capturing one environment revision against
// the catalog it was resolved from. The caller computes the digest and persists
// the result; Build itself is a pure, deterministic function.
func Build(catalog domain.Catalog, env domain.Environment, id string, at time.Time) (domain.Lockfile, error) {
	lock := domain.Lockfile{
		Format:              domain.LockfileFormat,
		ID:                  id,
		EnvironmentID:       env.ID,
		EnvironmentRevision: env.Revision,
		CatalogRevision:     catalog.Revision,
		Roots:               domain.CopyStrings(env.Roots),
		Resolved:            domain.CopyStrings(env.Resolved),
		Releases:            make([]domain.LockedRelease, 0, len(env.Resolved)),
		Reasons:             make(map[string]domain.LockReason),
		CreatedAt:           at.UTC(),
	}
	for _, componentID := range domain.SortedKeys(env.Resolved) {
		version := env.Resolved[componentID]
		collection, ok := catalog.Releases[componentID]
		if !ok {
			return domain.Lockfile{}, domain.Missing("component", componentID)
		}
		release, ok := collection[version]
		if !ok {
			return domain.Lockfile{}, domain.Missing("release", componentID+"@"+version)
		}
		lock.Releases = append(lock.Releases, domain.LockedRelease{
			ComponentID: componentID,
			Version:     version,
			Requires:    domain.CopyStrings(release.Requires),
			State:       release.State,
		})
		lock.Reasons[componentID] = domain.LockReason{Sources: collectSources(componentID, env.Roots, env.Resolved, catalog)}
	}
	digest, err := Digest(lock)
	if err != nil {
		return domain.Lockfile{}, err
	}
	lock.Digest = digest
	return lock, nil
}

// collectSources lists every constraint edge that justified selecting
// componentID: the root constraint when it is a root, plus one entry per
// selected parent release that requires it.
func collectSources(componentID string, roots, resolved map[string]string, catalog domain.Catalog) []domain.LockConstraint {
	sources := make([]domain.LockConstraint, 0)
	if rootConstraint, isRoot := roots[componentID]; isRoot {
		sources = append(sources, domain.LockConstraint{From: "root", Constraint: rootConstraint})
	}
	for _, parent := range domain.SortedKeys(resolved) {
		release, ok := catalog.Releases[parent][resolved[parent]]
		if !ok {
			continue
		}
		if constraint, requires := release.Requires[componentID]; requires {
			sources = append(sources, domain.LockConstraint{From: parent + "@" + resolved[parent], Constraint: constraint})
		}
	}
	return sources
}

// Evaluate compares a lockfile with the live catalog and environment. The
// result is purely informational: callers must never apply it back.
func Evaluate(lock domain.Lockfile, catalog domain.Catalog, env *domain.Environment) domain.LockDrift {
	drift := domain.LockDrift{
		CurrentCatalogRevision: catalog.Revision,
		EnvironmentChanges:     make([]domain.Change, 0),
		CatalogChanges:         make([]domain.LockCatalogChange, 0),
		RootChanges:            make([]domain.LockRootChange, 0),
	}
	liveResolved := map[string]string{}
	liveRoots := map[string]string{}
	if env != nil {
		drift.EnvironmentExists = true
		drift.CurrentEnvironmentRevision = env.Revision
		drift.EnvironmentRevisionMatched = env.Revision == lock.EnvironmentRevision
		liveResolved = env.Resolved
		liveRoots = env.Roots
	}
	drift.CatalogRevisionMatched = catalog.Revision == lock.CatalogRevision
	drift.EnvironmentChanges = resolution.Diff(lock.Resolved, liveResolved)
	drift.RootChanges = diffRoots(lock.Roots, liveRoots)
	drift.EnvironmentMatched = env != nil && equalMaps(lock.Roots, liveRoots) && equalMaps(lock.Resolved, liveResolved)
	for _, componentID := range domain.SortedKeys(lock.Resolved) {
		lockedVersion := lock.Resolved[componentID]
		collection, componentExists := catalog.Releases[componentID]
		release, releaseExists := collection[lockedVersion]
		change := domain.LockCatalogChange{ComponentID: componentID, Locked: lockedVersion}
		switch {
		case !componentExists:
			change.Kind = "removed"
			change.Detail = "component no longer exists in the catalog"
		case !releaseExists:
			change.Kind = "removed"
			change.Detail = "locked version no longer exists in the catalog"
		case release.State == domain.Withdrawn:
			change.Kind = "withdrawn"
		case !equalMaps(lockedRequires(lock, componentID, lockedVersion), release.Requires):
			change.Kind = "definition_changed"
			change.Detail = "the dependency definition of the locked version changed"
		default:
			continue
		}
		drift.CatalogChanges = append(drift.CatalogChanges, change)
	}
	drift.CatalogMatched = len(drift.CatalogChanges) == 0
	return drift
}

func lockedRequires(lock domain.Lockfile, componentID, version string) map[string]string {
	for _, release := range lock.Releases {
		if release.ComponentID == componentID && release.Version == version {
			return release.Requires
		}
	}
	return nil
}

func diffRoots(locked, live map[string]string) []domain.LockRootChange {
	ids := make(map[string]bool)
	for id := range locked {
		ids[id] = true
	}
	for id := range live {
		ids[id] = true
	}
	changes := make([]domain.LockRootChange, 0)
	for _, id := range domain.SortedKeys(ids) {
		lockedConstraint, hadLocked := locked[id]
		liveConstraint, hasLive := live[id]
		change := domain.LockRootChange{ComponentID: id, Locked: lockedConstraint, Current: liveConstraint}
		switch {
		case hadLocked && !hasLive:
			change.Kind = "root_removed"
		case !hadLocked && hasLive:
			change.Kind = "root_added"
		case lockedConstraint != liveConstraint:
			change.Kind = "constraint_changed"
		default:
			continue
		}
		changes = append(changes, change)
	}
	return changes
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			return false
		}
	}
	return true
}
