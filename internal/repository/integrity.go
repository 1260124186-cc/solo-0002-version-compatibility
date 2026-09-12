package repository

import (
	"fmt"

	"solo-0002-version-compatibility/internal/domain"
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
	withdrawalEvents := make(map[uint64]Event)
	correctionEvents := make(map[uint64]Event)
	var firstEventSequence uint64
	for _, event := range s.Events {
		if firstEventSequence == 0 {
			firstEventSequence = event.Sequence
		}
		switch {
		case event.Kind == "release" && event.Action == "withdrawn":
			withdrawalEvents[event.Sequence] = event
		case event.Kind == "release" && event.Action == "withdrawal_reason_corrected":
			correctionEvents[event.Sequence] = event
		}
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
			if release.WithdrawnAt != nil && release.WithdrawnAt.IsZero() {
				return fmt.Errorf("invalid withdrawal timestamp")
			}
			if release.State == domain.Withdrawn {
				if release.WithdrawalEventSequence == 0 {
					return fmt.Errorf("withdrawn release is missing its withdrawal event")
				}
				if release.WithdrawalEventSequence > s.Revision {
					return fmt.Errorf("withdrawn release references a future event")
				}
				if firstEventSequence != 0 && release.WithdrawalEventSequence >= firstEventSequence {
					if _, exists := withdrawalEvents[release.WithdrawalEventSequence]; !exists {
						return fmt.Errorf("missing withdrawal event for retained sequence")
					}
				}
				if err := domain.ValidateWithdrawalReason(release.WithdrawalReason); err != nil {
					return err
				}
				seenCorrections := make(map[uint64]bool)
				previousCorrectionSequence := release.WithdrawalEventSequence
				for _, correction := range release.WithdrawalReasonCorrections {
					if err := domain.ValidateWithdrawalReason(correction.Reason); err != nil {
						return err
					}
					if correction.CorrectsSequence != release.WithdrawalEventSequence || correction.EventSequence <= previousCorrectionSequence {
						return fmt.Errorf("withdrawal correction points to an invalid withdrawal")
					}
					previousCorrectionSequence = correction.EventSequence
					if correction.EventSequence > s.Revision {
						return fmt.Errorf("withdrawal correction references a future event")
					}
					if firstEventSequence != 0 && correction.EventSequence >= firstEventSequence {
						if _, exists := correctionEvents[correction.EventSequence]; !exists {
							return fmt.Errorf("missing withdrawal reason correction event")
						}
					}
					if correction.At.IsZero() {
						return fmt.Errorf("withdrawal correction is missing its timestamp")
					}
					if seenCorrections[correction.EventSequence] {
						return fmt.Errorf("duplicate withdrawal reason correction")
					}
					seenCorrections[correction.EventSequence] = true
					correctionEvent, exists := correctionEvents[correction.EventSequence]
					if exists && (correctionEvent.EntityID != release.ComponentID+"@"+release.Version ||
						correctionEvent.CorrectsEventSequence != release.WithdrawalEventSequence ||
						correctionEvent.Reason != correction.Reason ||
						!correctionEvent.At.Equal(correction.At)) {
						return fmt.Errorf("withdrawal reason correction does not match its event")
					}
				}
			} else if release.WithdrawalEventSequence != 0 || release.WithdrawalReason != "" || len(release.WithdrawalReasonCorrections) != 0 {
				return fmt.Errorf("available release has withdrawal metadata")
			}
			for dep := range release.Requires {
				if _, ok := s.Catalog.Components[dep]; !ok {
					return fmt.Errorf("unknown dependency %s", dep)
				}
			}
		}
	}
	referencedWithdrawals := make(map[uint64]bool)
	referencedCorrections := make(map[uint64]bool)
	for _, releases := range s.Catalog.Releases {
		for _, release := range releases {
			if release.State != domain.Withdrawn {
				continue
			}
			entityID := release.ComponentID + "@" + release.Version
			if referencedWithdrawals[release.WithdrawalEventSequence] {
				return fmt.Errorf("withdrawal event referenced by multiple releases")
			}
			referencedWithdrawals[release.WithdrawalEventSequence] = true
			event, exists := withdrawalEvents[release.WithdrawalEventSequence]
			if exists {
				if event.EntityID != entityID || event.Reason != release.WithdrawalReason || !event.At.Equal(*release.WithdrawnAt) {
					return fmt.Errorf("withdrawal event does not match %s", entityID)
				}
				if event.CorrectsEventSequence != 0 {
					return fmt.Errorf("withdrawal event cannot correct another event")
				}
			}
			for _, correction := range release.WithdrawalReasonCorrections {
				if referencedCorrections[correction.EventSequence] {
					return fmt.Errorf("correction event referenced by multiple releases")
				}
				referencedCorrections[correction.EventSequence] = true
			}
		}
	}
	for sequence := range withdrawalEvents {
		if !referencedWithdrawals[sequence] {
			return fmt.Errorf("withdrawal event is not attached to a withdrawn release")
		}
	}
	for sequence := range correctionEvents {
		if !referencedCorrections[sequence] {
			return fmt.Errorf("withdrawal correction event is not attached to a release")
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
		if event.Kind == "release" && event.Action == "withdrawn" {
			if err := domain.ValidateWithdrawalReason(event.Reason); err != nil || event.CorrectsEventSequence != 0 {
				return fmt.Errorf("invalid withdrawal event payload")
			}
		} else if event.Kind == "release" && event.Action == "withdrawal_reason_corrected" {
			if err := domain.ValidateWithdrawalReason(event.Reason); err != nil {
				return err
			}
			if event.CorrectsEventSequence == 0 || event.CorrectsEventSequence >= event.Sequence {
				return fmt.Errorf("withdrawal correction points to an invalid event")
			}
			if firstEventSequence != 0 && event.CorrectsEventSequence >= firstEventSequence {
				original, exists := withdrawalEvents[event.CorrectsEventSequence]
				if !exists {
					return fmt.Errorf("withdrawal correction points to a non-withdrawal event")
				}
				if original.EntityID != event.EntityID {
					return fmt.Errorf("withdrawal correction changes release identity")
				}
			}
		} else if event.Reason != "" || event.CorrectsEventSequence != 0 {
			return fmt.Errorf("event contains unsupported withdrawal metadata")
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
