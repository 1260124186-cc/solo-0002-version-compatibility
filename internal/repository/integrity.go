package repository

import (
	"fmt"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/resolution"
	"solo-0002-version-compatibility/internal/semver"
)

func validateState(s *State) error {
	if s.Schema != 1 || s.Catalog.Components == nil || s.Catalog.Releases == nil || s.Environments == nil || s.Plans == nil {
		return fmt.Errorf("unsupported schema or missing collections")
	}
	if s.Catalog.Revision > s.Revision || len(s.Catalog.Components) > domain.MaxComponents {
		return fmt.Errorf("invalid catalog revision or component count")
	}
	if len(s.Environments) > 200 || len(s.Plans) > 5000 || len(s.Events) > 10000 {
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
		// A live environment was created under the proof rules, so its proof
		// must re-derive structurally. It may be bound to an older catalog
		// revision (structural mode), but a broken chain is corruption.
		if err := resolution.VerifyProof(s.Catalog, env.Roots, env.Resolved, env.Proof, false); err != nil {
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
			if plan.Proof == nil || plan.Proof.CatalogRevision != plan.CatalogRevision {
				return fmt.Errorf("plan proof is missing or bound to a different catalog revision")
			}
			// Ready plans may be bound to a catalog revision that has since
			// moved (their proof then advertises stale); applied plans stay
			// valid historical records. In both cases the proof structure
			// itself must remain intact.
			if err := resolution.VerifyProof(s.Catalog, plan.Roots, plan.Resolved, plan.Proof, false); err != nil {
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
	return nil
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
