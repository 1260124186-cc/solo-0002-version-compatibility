package repository

import (
	"fmt"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/semver"
)

func validateState(s *State) error {
	if (s.Schema != 1 && s.Schema != CurrentSchema) ||
		s.Catalog.Components == nil || s.Catalog.Releases == nil ||
		s.Environments == nil || s.Plans == nil ||
		(s.Schema == CurrentSchema && s.RootTimelines == nil) {
		return fmt.Errorf("unsupported schema or missing collections")
	}
	if s.Catalog.Revision > s.Revision || len(s.Catalog.Components) > domain.MaxComponents {
		return fmt.Errorf("invalid catalog revision or component count")
	}
	if len(s.Environments) > 200 || len(s.Plans) > 5000 ||
		(s.Schema == CurrentSchema && len(s.RootTimelines) > 200) ||
		len(s.Events) > 10000 {
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
		if s.Schema == CurrentSchema && plan.RootChanges == nil {
			return fmt.Errorf("plan root changes are missing")
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

	if s.Schema == 1 {
		if s.RootTimelines != nil {
			return fmt.Errorf("legacy state cannot contain root timelines")
		}
	} else {
		if err := validateRootTimelines(s); err != nil {
			return err
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

func validateRootTimelines(s *State) error {
	if len(s.RootTimelines) != len(s.Environments) {
		return fmt.Errorf("an environment is missing its root timeline")
	}

	var totalEntries int
	firstEventSequence := uint64(1)
	if len(s.Events) > 0 {
		firstEventSequence = s.Events[0].Sequence
	}

	for id, timeline := range s.RootTimelines {
		if id != timeline.EnvironmentID {
			return fmt.Errorf("root timeline key mismatch")
		}
		env, exists := s.Environments[id]
		if !exists {
			return fmt.Errorf("root timeline references a missing environment")
		}
		if len(timeline.Entries) == 0 {
			return fmt.Errorf("root timeline has no starting point")
		}
		totalEntries += len(timeline.Entries)
		if totalEntries > 100000 {
			return fmt.Errorf("root timeline exceeds capacity")
		}

		var previousAfterRevision uint64
		for i, entry := range timeline.Entries {
			if entry.Sequence != uint64(i+1) || entry.AfterRevision == 0 || entry.AfterRevision > env.Revision {
				return fmt.Errorf("invalid root timeline sequence or revision")
			}
			if i > 0 && entry.BeforeRevision < previousAfterRevision {
				return fmt.Errorf("root timeline revision chain is broken")
			}
			if entry.Type == domain.RootTimelineApplied && entry.AfterRevision != entry.BeforeRevision+1 {
				return fmt.Errorf("applied root timeline entry must span one environment revision")
			}
			if entry.BeforeRevision != 0 && entry.BeforeRevision >= entry.AfterRevision {
				return fmt.Errorf("invalid root timeline revision transition")
			}
			previousAfterRevision = entry.AfterRevision

			if err := domain.ValidateRequirements(entry.After, false); err != nil {
				return err
			}
			if err := validateTimelineEntryShape(s, id, entry, i, firstEventSequence); err != nil {
				return err
			}
		}

		if timeline.Entries[len(timeline.Entries)-1].AfterRevision > env.Revision {
			return fmt.Errorf("root timeline points beyond the current environment revision")
		}
		if !sameRequirements(timeline.Entries[len(timeline.Entries)-1].After, env.Roots) {
			return fmt.Errorf("root timeline does not reach the current root requirements")
		}
	}
	return nil
}

func validateTimelineEntryShape(s *State, environmentID string, entry domain.RootTimelineEntry, index int, firstEventSequence uint64) error {
	switch entry.Type {
	case domain.RootTimelineBootstrap:
		if index != 0 || entry.PlanID != "" || entry.Reason != "" || entry.Before != nil || entry.BeforeRevision != 0 {
			return fmt.Errorf("invalid bootstrap timeline entry")
		}
		if entry.EventSequence != 0 {
			return fmt.Errorf("bootstrap timeline entry cannot fabricate an event")
		}
		if entry.RootChanges == nil || len(entry.RootChanges) != 0 {
			return fmt.Errorf("bootstrap timeline entry must not fabricate root changes")
		}

	case domain.RootTimelineCreated:
		if index != 0 || entry.PlanID != "" || entry.Reason != "" || entry.Before != nil || entry.BeforeRevision != 0 {
			return fmt.Errorf("invalid creation timeline entry")
		}
		if err := validateLinkedTimelineEvent(s, environmentID, entry, "environment", "created", firstEventSequence); err != nil {
			return err
		}
		if err := validateRootChanges(nil, entry.After, entry.RootChanges); err != nil {
			return err
		}

	case domain.RootTimelineApplied:
		if index == 0 || entry.PlanID == "" || entry.Reason == "" || entry.Before == nil {
			return fmt.Errorf("invalid application timeline entry")
		}
		if err := validateLinkedTimelineEvent(s, entry.PlanID, entry, "plan", "applied", firstEventSequence); err != nil {
			return err
		}
		plan, ok := s.Plans[entry.PlanID]
		if !ok {
			return fmt.Errorf("timeline references a missing applied plan")
		}
		if plan.State != domain.Applied || plan.EnvironmentID != environmentID || plan.Reason != entry.Reason {
			return fmt.Errorf("timeline does not match its applied plan")
		}
		if plan.BaseRevision != entry.BeforeRevision {
			return fmt.Errorf("timeline base revision does not match its plan")
		}
		if err := validateRootChanges(entry.Before, entry.After, entry.RootChanges); err != nil {
			return err
		}

	default:
		return fmt.Errorf("invalid root timeline entry type")
	}
	return nil
}

func validateLinkedTimelineEvent(s *State, entityID string, entry domain.RootTimelineEntry, kind, action string, firstEventSequence uint64) error {
	if entry.EventSequence == 0 || entry.EventSequence > s.Revision {
		return fmt.Errorf("root timeline is not linked to an atomic state event")
	}
	// The general event log retains only the latest 10000 entries. The timeline
	// still keeps event_sequence as the durable correlation key after trimming.
	if entry.EventSequence < firstEventSequence {
		return nil
	}
	index := entry.EventSequence - firstEventSequence
	if index >= uint64(len(s.Events)) {
		return fmt.Errorf("root timeline event is out of range")
	}
	event := s.Events[index]
	if event.Sequence != entry.EventSequence ||
		event.Kind != kind || event.EntityID != entityID ||
		event.Action != action || !event.At.Equal(entry.At) {
		return fmt.Errorf("root timeline event does not match its atomic write")
	}
	return nil
}

func sameRequirements(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for id, constraint := range a {
		if b[id] != constraint {
			return false
		}
	}
	return true
}

func validateRootChanges(before, after map[string]string, changes []domain.Change) error {
	if changes == nil {
		return fmt.Errorf("root changes are missing")
	}
	expected := domain.RootDiff(before, after)
	if len(changes) != len(expected) {
		return fmt.Errorf("root change count does not match root requirements")
	}
	for i := range expected {
		if changes[i] != expected[i] {
			return fmt.Errorf("root changes do not match root requirements")
		}
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
