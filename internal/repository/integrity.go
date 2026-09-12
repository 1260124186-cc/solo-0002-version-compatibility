package repository

import (
	"fmt"
	"strings"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/locking"
	"solo-0002-version-compatibility/internal/semver"
)

// MaxLockfiles bounds the immutable evidence-record collection.
const MaxLockfiles = 2000

func validateState(s *State) error {
	if s.Schema != 1 || s.Catalog.Components == nil || s.Catalog.Releases == nil || s.Environments == nil || s.Plans == nil {
		return fmt.Errorf("unsupported schema or missing collections")
	}
	// State files written before lockfiles existed simply decode to nil; treat
	// them as the empty immutable collection.
	if s.Lockfiles == nil {
		s.Lockfiles = make(map[string]domain.Lockfile)
	}
	if s.Catalog.Revision > s.Revision || len(s.Catalog.Components) > domain.MaxComponents {
		return fmt.Errorf("invalid catalog revision or component count")
	}
	if len(s.Environments) > 200 || len(s.Plans) > 5000 || len(s.Lockfiles) > MaxLockfiles || len(s.Events) > 10000 {
		return fmt.Errorf("persisted collection exceeds capacity")
	}
	for id, component := range s.Catalog.Components {
		if s.Catalog.Releases[id] == nil {
			return fmt.Errorf("component is missing its release collection")
		}
		if id != component.ID {
			return fmt.Errorf("component key mismatch")
		}
		if err := domain.ValidateComponent(domain.ComponentInput{ID: id, Name: component.Name, Description: component.Description}); err != nil {
			return err
		}
	}
	for id, releases := range s.Catalog.Releases {
		if _, ok := s.Catalog.Components[id]; !ok {
			return fmt.Errorf("orphan release collection")
		}
		if len(releases) > domain.MaxReleases {
			return fmt.Errorf("too many releases")
		}
		for version, release := range releases {
			if release.ComponentID != id || release.Version != version {
				return fmt.Errorf("release key mismatch")
			}
			if err := domain.ValidateRelease(domain.ReleaseInput{Version: version, Requires: release.Requires}, id); err != nil {
				return err
			}
			if release.State != domain.Available && release.State != domain.Withdrawn {
				return fmt.Errorf("invalid release state")
			}
			if (release.State == domain.Withdrawn) != (release.WithdrawnAt != nil) {
				return fmt.Errorf("invalid withdrawal timestamp")
			}
			for dep := range release.Requires {
				if _, ok := s.Catalog.Components[dep]; !ok {
					return fmt.Errorf("unknown dependency %s", dep)
				}
			}
		}
	}
	for id, env := range s.Environments {
		if id != env.ID || env.Revision == 0 {
			return fmt.Errorf("invalid environment identity or revision")
		}
		if err := domain.ValidateID(id); err != nil {
			return err
		}
		if err := domain.ValidateText(env.Name, "name", 1, 120); err != nil {
			return err
		}
		if err := ValidateSelection(s.Catalog, env.Roots, env.Resolved, true); err != nil {
			return err
		}
	}
	for id, plan := range s.Plans {
		if id != plan.ID || plan.Revision == 0 {
			return fmt.Errorf("invalid plan identity or revision")
		}
		env, ok := s.Environments[plan.EnvironmentID]
		if !ok || plan.BaseRevision == 0 || plan.BaseRevision > env.Revision || plan.CatalogRevision > s.Catalog.Revision {
			return fmt.Errorf("plan references an invalid revision")
		}
		if err := domain.ValidateRequirements(plan.Roots, false); err != nil {
			return err
		}
		switch plan.State {
		case domain.Draft, domain.Cancelled:
		case domain.Ready, domain.Applied:
			if err := ValidateSelection(s.Catalog, plan.Roots, plan.Resolved, false); err != nil {
				return err
			}
		default:
			return fmt.Errorf("invalid plan state")
		}
	}
	var previous uint64
	for i, event := range s.Events {
		if event.Sequence == 0 || event.Sequence > s.Revision || (i > 0 && event.Sequence != previous+1) {
			return fmt.Errorf("invalid event sequence")
		}
		previous = event.Sequence
	}
	if previous != s.Revision {
		return fmt.Errorf("event tail does not match state revision")
	}
	for id, lock := range s.Lockfiles {
		if err := ValidateLockfile(s, id, lock); err != nil {
			return err
		}
	}
	return nil
}

// ValidateLockfile checks an immutable lock record: identity, digest, internal
// self-consistency and bounded historical references. Later catalog drift
// (withdrawal or revision advance) is not corruption and is not checked here.
func ValidateLockfile(s *State, id string, lock domain.Lockfile) error {
	if id != lock.ID {
		return fmt.Errorf("lockfile key mismatch")
	}
	if lock.Format != domain.LockfileFormat {
		return fmt.Errorf("unsupported lockfile format")
	}
	if err := locking.Verify(lock); err != nil {
		return fmt.Errorf("lockfile %s failed integrity: %w", id, err)
	}
	if err := domain.ValidateID(lock.EnvironmentID); err != nil {
		return err
	}
	env, exists := s.Environments[lock.EnvironmentID]
	if !exists {
		return fmt.Errorf("lockfile references a missing environment")
	}
	if lock.EnvironmentRevision == 0 || lock.EnvironmentRevision > env.Revision {
		return fmt.Errorf("lockfile references an invalid environment revision")
	}
	if lock.CatalogRevision > s.Catalog.Revision {
		return fmt.Errorf("lockfile references a future catalog revision")
	}
	if len(lock.Releases) != len(lock.Resolved) || len(lock.Reasons) != len(lock.Resolved) {
		return fmt.Errorf("lockfile release coverage mismatch")
	}
	lockedByID := make(map[string]domain.LockedRelease, len(lock.Releases))
	seen := make(map[string]bool, len(lock.Releases))
	for _, release := range lock.Releases {
		selected, ok := lock.Resolved[release.ComponentID]
		if !ok || selected != release.Version {
			return fmt.Errorf("lockfile release does not match its resolved set")
		}
		if seen[release.ComponentID] {
			return fmt.Errorf("lockfile lists a component twice")
		}
		seen[release.ComponentID] = true
		if _, err := semver.Parse(release.Version); err != nil {
			return fmt.Errorf("lockfile holds an invalid version")
		}
		if release.State != domain.Available && release.State != domain.Withdrawn {
			return fmt.Errorf("lockfile holds an invalid release state")
		}
		if err := domain.ValidateRequirements(release.Requires, true); err != nil {
			return err
		}
		if _, self := release.Requires[release.ComponentID]; self {
			return fmt.Errorf("lockfile release depends on itself")
		}
		lockedByID[release.ComponentID] = release
	}
	for componentID, version := range lock.Resolved {
		release, ok := lockedByID[componentID]
		if !ok {
			return fmt.Errorf("lockfile is missing a captured release definition")
		}
		reason, ok := lock.Reasons[componentID]
		if !ok || len(reason.Sources) == 0 {
			return fmt.Errorf("lockfile is missing its selection rationale")
		}
		for _, source := range reason.Sources {
			if source.From != "root" {
				parent, parentVersion, found := strings.Cut(source.From, "@")
				if !found {
					return fmt.Errorf("lockfile rationale has an invalid source")
				}
				parentRelease, parentKnown := lockedByID[parent]
				if !parentKnown || parentRelease.Version != parentVersion {
					return fmt.Errorf("lockfile rationale references an unlocked parent")
				}
				if parentRelease.Requires[componentID] != source.Constraint {
					return fmt.Errorf("lockfile rationale constraint disagrees with the captured definition")
				}
			} else if lock.Roots[componentID] != source.Constraint {
				return fmt.Errorf("lockfile rationale disagrees with its root constraint")
			}
			if _, err := semver.ParseConstraint(source.Constraint); err != nil {
				return fmt.Errorf("lockfile rationale holds an invalid constraint")
			}
		}
		// The rationale must cite the root whenever the component is a root.
		_, isRoot := lock.Roots[componentID]
		citesRoot := false
		for _, source := range reason.Sources {
			if source.From == "root" {
				citesRoot = true
			}
		}
		if isRoot != citesRoot {
			return fmt.Errorf("lockfile root rationale mismatch")
		}
		// The captured definition must reproduce the catalog row that existed at
		// lock time when that exact version is still present. Later withdrawal
		// only flips state and is allowed.
		current, present := s.Catalog.Releases[componentID][version]
		if present && !sameRequires(current.Requires, release.Requires) {
			return fmt.Errorf("catalog release definition no longer matches the locked record")
		}
	}
	if err := domain.ValidateRequirements(lock.Roots, false); err != nil {
		return err
	}
	for componentID := range lock.Roots {
		if _, ok := lock.Resolved[componentID]; !ok {
			return fmt.Errorf("lockfile omits a root component")
		}
	}
	return nil
}

func sameRequires(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for dep, constraint := range a {
		if b[dep] != constraint {
			return false
		}
	}
	return true
}

// ValidateSelection also verifies that the set contains no unreachable entries.
func ValidateSelection(c domain.Catalog, roots, selected map[string]string, available bool) error {
	if err := domain.ValidateRequirements(roots, false); err != nil {
		return err
	}
	seen := make(map[string]bool)
	var visit func(string, string) error
	visit = func(id, raw string) error {
		version, exists := selected[id]
		if !exists {
			return fmt.Errorf("selection omits %s", id)
		}
		release, exists := c.Releases[id][version]
		if !exists || (available && release.State != domain.Available) {
			return fmt.Errorf("selection references unavailable %s@%s", id, version)
		}
		constraint, err := semver.ParseConstraint(raw)
		if err != nil {
			return err
		}
		v, err := semver.Parse(version)
		if err != nil {
			return err
		}
		if !constraint.Matches(v) {
			return fmt.Errorf("%s@%s violates %s", id, version, raw)
		}
		if seen[id] {
			return nil
		}
		seen[id] = true
		for _, dep := range domain.SortedKeys(release.Requires) {
			if err := visit(dep, release.Requires[dep]); err != nil {
				return err
			}
		}
		return nil
	}
	for _, id := range domain.SortedKeys(roots) {
		if err := visit(id, roots[id]); err != nil {
			return err
		}
	}
	if len(seen) != len(selected) {
		return fmt.Errorf("selection contains unreachable components")
	}
	return nil
}
