package repository

import (
	"fmt"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/semver"
)

// currentSchema is 2: schema 1 records are read additively (all components
// default to public), then rewritten as schema 2 on the first successful write.
const currentSchema = 2

func validateState(s *State) error {
	if s.Schema != 1 && s.Schema != currentSchema {
		return fmt.Errorf("unsupported schema")
	}
	legacy := s.Schema == 1
	s.Schema = currentSchema
	if s.Catalog.Components == nil || s.Catalog.Releases == nil || s.Environments == nil || s.Plans == nil {
		return fmt.Errorf("missing collections")
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
		if legacy {
			// Pre-visibility data carries no policy fields; treat as public.
			component.Family = ""
			component.Visibility = ""
			component.AllowedConsumers = nil
			s.Catalog.Components[id] = component
		}
		if err := domain.ValidateComponentPolicy(component, s.Catalog.Components); err != nil {
			return err
		}
	}
	if s.Catalog.VisibilityRevision > s.Catalog.Revision {
		return fmt.Errorf("invalid visibility revision")
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
				// Visibility is enforced at the decision points (registration,
				// resolution, plan validation and application). Startup stays
				// structural so a later policy tightening cannot make persisted
				// releases or historical environments unbootable.
			}
			declared := make(map[string]bool, len(release.InternalDeps))
			for _, dep := range release.InternalDeps {
				if _, required := release.Requires[dep]; !required {
					return fmt.Errorf("release %s@%s marks %s internal without requiring it", id, version, dep)
				}
				if declared[dep] {
					return fmt.Errorf("release %s@%s marks %s internal more than once", id, version, dep)
				}
				declared[dep] = true
				if target, ok := s.Catalog.Components[dep]; ok && domain.EffectiveVisibility(target) != domain.Internal {
					return fmt.Errorf("release %s@%s marks an edge to public %s as internal", id, version, dep)
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
		if env.CatalogRevision > s.Catalog.Revision {
			return fmt.Errorf("environment references an invalid catalog revision")
		}
		if env.VisibilityRevision != nil && *env.VisibilityRevision > s.Catalog.VisibilityRevision {
			return fmt.Errorf("environment references an invalid visibility revision")
		}
		// Persisted environments are historical facts: startup verifies the
		// selection structurally and never retro-applies the current policy,
		// which could otherwise prevent the service from booting after a
		// tightening. New resolutions and plan applies enforce visibility.
		if err := ValidateLegacySelection(s.Catalog, env.Roots, env.Resolved, true); err != nil {
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
		if plan.VisibilityRevision != nil && *plan.VisibilityRevision > s.Catalog.VisibilityRevision {
			return fmt.Errorf("plan references an invalid visibility revision")
		}
		if err := domain.ValidateRequirements(plan.Roots, false); err != nil {
			return err
		}
		switch plan.State {
		case domain.Draft, domain.Cancelled:
		case domain.Ready, domain.Applied:
			// Historical facts are validated structurally; the apply path
			// independently enforces current visibility and revision checks.
			if err := ValidateLegacySelection(s.Catalog, plan.Roots, plan.Resolved, false); err != nil {
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

// SelectionOptions controls how ValidateSelection interprets a selection.
type SelectionOptions struct {
	RequireAvailable  bool
	EnforceVisibility bool
}

// ValidateSelection also verifies that the set contains no unreachable entries.
// Visibility is checked per direct edge, including cycle back edges; the vertex
// visit set only terminates the walk and must not skip an edge.
func ValidateSelection(c domain.Catalog, roots, selected map[string]string, available bool) error {
	return ValidateSelectionWithOptions(c, roots, selected, SelectionOptions{RequireAvailable: available, EnforceVisibility: true})
}

// ValidateLegacySelection accepts selections recorded before visibility was
// introduced. Structural checks still apply; boundary checks are skipped.
func ValidateLegacySelection(c domain.Catalog, roots, selected map[string]string, available bool) error {
	return ValidateSelectionWithOptions(c, roots, selected, SelectionOptions{RequireAvailable: available, EnforceVisibility: false})
}

func ValidateSelectionWithOptions(c domain.Catalog, roots, selected map[string]string, options SelectionOptions) error {
	if err := domain.ValidateRequirements(roots, false); err != nil {
		return err
	}
	checked := make(map[string]bool)
	checkEdge := func(from, to string) error {
		if !options.EnforceVisibility {
			return nil
		}
		key := from + "\x00" + to
		if checked[key] {
			return nil
		}
		checked[key] = true
		target, exists := c.Components[to]
		if !exists {
			return fmt.Errorf("unknown dependency %s", to)
		}
		var owner domain.Component
		if from != "" {
			owner = c.Components[from]
		}
		if !domain.CanReference(from, owner, target) {
			if from == "" {
				return fmt.Errorf("root directly references internal component %s; use its public entry point", to)
			}
			return fmt.Errorf("%s directly references internal %s without family membership or an allowed-consumer grant", from, to)
		}
		return nil
	}
	seen := make(map[string]bool)
	var visit func(string, string, string) error
	visit = func(parent, id, raw string) error {
		if err := checkEdge(parent, id); err != nil {
			return err
		}
		version, exists := selected[id]
		if !exists {
			return fmt.Errorf("selection omits %s", id)
		}
		release, exists := c.Releases[id][version]
		if !exists || (options.RequireAvailable && release.State != domain.Available) {
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
			if err := visit(id, dep, release.Requires[dep]); err != nil {
				return err
			}
		}
		return nil
	}
	for _, id := range domain.SortedKeys(roots) {
		if err := visit("", id, roots[id]); err != nil {
			return err
		}
	}
	if len(seen) != len(selected) {
		return fmt.Errorf("selection contains unreachable components")
	}
	return nil
}
