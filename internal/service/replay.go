package service

import (
	"context"
	"reflect"
	"sort"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
	"solo-0002-version-compatibility/internal/semver"
)

// MissingRange marks the event segment that is no longer retained.
type MissingRange struct {
	From uint64 `json:"from"`
	To   uint64 `json:"to"`
}

// ReplayedState is the reconstructed catalog, environment and plan state.
type ReplayedState struct {
	Revision        uint64                      `json:"revision"`
	CatalogRevision uint64                      `json:"catalog_revision"`
	Components      []domain.Component          `json:"components"`
	Releases        map[string][]domain.Release `json:"releases"`
	Environments    []domain.Environment        `json:"environments"`
	Plans           []domain.Plan               `json:"plans"`
}

// KindComparison summarizes how one entity kind differs from the current state.
type KindComparison struct {
	Added   int `json:"added"`
	Changed int `json:"changed"`
	Removed int `json:"removed"`
}

func (c KindComparison) empty() bool {
	return c.Added == 0 && c.Changed == 0 && c.Removed == 0
}

// ReplayComparison relates the reconstructed state to the current state.
type ReplayComparison struct {
	MatchesCurrent bool           `json:"matches_current"`
	EventsSince    uint64         `json:"events_since"`
	Components     KindComparison `json:"components"`
	Releases       KindComparison `json:"releases"`
	Environments   KindComparison `json:"environments"`
	Plans          KindComparison `json:"plans"`
}

// ReplayReport is the audit result for one event sequence.
type ReplayReport struct {
	Sequence       uint64            `json:"sequence"`
	LatestSequence uint64            `json:"latest_sequence"`
	Complete       bool              `json:"complete"`
	Missing        *MissingRange     `json:"missing,omitempty"`
	ReplayedEvents uint64            `json:"replayed_events"`
	State          *ReplayedState    `json:"state,omitempty"`
	Comparison     *ReplayComparison `json:"comparison,omitempty"`
}

// ReplayEvents rebuilds the state at an event sequence purely from the
// recorded event payloads. It never re-solves dependencies and never reads
// live state for the reconstruction itself; the current state only feeds the
// comparison section. When the retained log does not reach back far enough,
// the report names the missing segment and omits the state instead of
// presenting a partial snapshot that would look complete.
func (s *Service) ReplayEvents(ctx context.Context, sequence uint64) (ReplayReport, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return ReplayReport{}, err
	}
	if sequence > state.Revision {
		return ReplayReport{}, domain.Invalid("sequence %d exceeds the latest recorded event %d", sequence, state.Revision)
	}
	report := ReplayReport{Sequence: sequence, LatestSequence: state.Revision, Complete: true}
	// Replay folds events 1..sequence over the empty state, so any retained
	// gap at or below the target makes the reconstruction incomplete.
	var gapTo uint64
	if len(state.Events) > 0 {
		gapTo = state.Events[0].Sequence - 1
	}
	for _, event := range state.Events {
		if event.Sequence > sequence {
			break
		}
		if event.Payload == nil && event.Sequence > gapTo {
			gapTo = event.Sequence
		}
	}
	if sequence > 0 && gapTo > 0 {
		report.Complete = false
		report.Missing = &MissingRange{From: 1, To: gapTo}
		return report, nil
	}
	components := make(map[string]domain.Component)
	releases := make(map[string]map[string]domain.Release)
	environments := make(map[string]domain.Environment)
	plans := make(map[string]domain.Plan)
	var catalogRevision uint64
	for _, event := range state.Events {
		if event.Sequence > sequence {
			break
		}
		// The gap scan above guarantees a payload for every event up to the
		// target sequence.
		payload := event.Payload
		if payload.Component != nil {
			component := *payload.Component
			components[component.ID] = component
			if releases[component.ID] == nil {
				releases[component.ID] = make(map[string]domain.Release)
			}
		}
		if payload.Release != nil {
			release := *payload.Release
			if releases[release.ComponentID] == nil {
				releases[release.ComponentID] = make(map[string]domain.Release)
			}
			releases[release.ComponentID][release.Version] = release
		}
		if payload.Environment != nil {
			environments[payload.Environment.ID] = *payload.Environment
		}
		if payload.Plan != nil {
			plans[payload.Plan.ID] = *payload.Plan
		}
		if event.Kind == "component" || event.Kind == "release" {
			catalogRevision++
		}
	}
	report.ReplayedEvents = sequence
	report.State = replayView(sequence, catalogRevision, components, releases, environments, plans)
	report.Comparison = compareReplayed(sequence, state, components, releases, environments, plans)
	return report, nil
}

// replayView renders the folded maps in a deterministic order so repeated
// reports for the same sequence are identical.
func replayView(revision, catalogRevision uint64, components map[string]domain.Component, releases map[string]map[string]domain.Release, environments map[string]domain.Environment, plans map[string]domain.Plan) *ReplayedState {
	view := &ReplayedState{
		Revision:        revision,
		CatalogRevision: catalogRevision,
		Components:      make([]domain.Component, 0, len(components)),
		Releases:        make(map[string][]domain.Release, len(releases)),
		Environments:    make([]domain.Environment, 0, len(environments)),
		Plans:           make([]domain.Plan, 0, len(plans)),
	}
	for _, id := range domain.SortedKeys(components) {
		view.Components = append(view.Components, components[id])
	}
	for _, id := range domain.SortedKeys(releases) {
		versions := make([]domain.Release, 0, len(releases[id]))
		for _, release := range releases[id] {
			versions = append(versions, release)
		}
		sort.Slice(versions, func(i, j int) bool {
			a, _ := semver.Parse(versions[i].Version)
			b, _ := semver.Parse(versions[j].Version)
			return a.Compare(b) > 0
		})
		view.Releases[id] = versions
	}
	for _, id := range domain.SortedKeys(environments) {
		view.Environments = append(view.Environments, environments[id])
	}
	for _, plan := range plans {
		view.Plans = append(view.Plans, plan)
	}
	sort.Slice(view.Plans, func(i, j int) bool {
		if view.Plans[i].CreatedAt.Equal(view.Plans[j].CreatedAt) {
			return view.Plans[i].ID < view.Plans[j].ID
		}
		return view.Plans[i].CreatedAt.Before(view.Plans[j].CreatedAt)
	})
	return view
}

func compareReplayed(sequence uint64, state *repository.State, components map[string]domain.Component, releases map[string]map[string]domain.Release, environments map[string]domain.Environment, plans map[string]domain.Plan) *ReplayComparison {
	comparison := &ReplayComparison{
		EventsSince:  state.Revision - sequence,
		Components:   diffEntities(components, state.Catalog.Components),
		Releases:     diffEntities(flattenReleases(releases), flattenReleases(state.Catalog.Releases)),
		Environments: diffEntities(environments, state.Environments),
		Plans:        diffEntities(plans, state.Plans),
	}
	comparison.MatchesCurrent = comparison.Components.empty() && comparison.Releases.empty() && comparison.Environments.empty() && comparison.Plans.empty()
	return comparison
}

func diffEntities[T any](replayed, current map[string]T) KindComparison {
	result := KindComparison{}
	for id, before := range replayed {
		after, exists := current[id]
		if !exists {
			result.Removed++
			continue
		}
		if !reflect.DeepEqual(before, after) {
			result.Changed++
		}
	}
	for id := range current {
		if _, exists := replayed[id]; !exists {
			result.Added++
		}
	}
	return result
}

func flattenReleases(releases map[string]map[string]domain.Release) map[string]domain.Release {
	flat := make(map[string]domain.Release)
	for id, versions := range releases {
		for version, release := range versions {
			flat[id+"@"+version] = release
		}
	}
	return flat
}
