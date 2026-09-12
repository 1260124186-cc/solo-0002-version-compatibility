package service

import (
	"context"
	"time"

	"solo-0002-version-compatibility/internal/repository"
)

type EventPage struct {
	Items     []repository.Event `json:"items"`
	NextAfter uint64             `json:"next_after"`
	Latest    uint64             `json:"latest"`
	Truncated bool               `json:"truncated"`
}

// EventFilter narrows an event page. Empty fields are unconstrained. When set,
// Start and End are both inclusive. The Has* flags distinguish a zero time
// (which is a valid RFC 3339 instant) from an absent bound.
type EventFilter struct {
	EntityID string
	Kind     string
	Action   string
	Start    time.Time
	End      time.Time
	HasStart bool
	HasEnd   bool
}

func (f EventFilter) matches(event repository.Event) bool {
	if f.EntityID != "" && event.EntityID != f.EntityID {
		return false
	}
	if f.Kind != "" && event.Kind != f.Kind {
		return false
	}
	if f.Action != "" && event.Action != f.Action {
		return false
	}
	// Inclusive on both ends: events exactly at Start or End are included.
	if f.HasStart && event.At.Before(f.Start) {
		return false
	}
	if f.HasEnd && event.At.After(f.End) {
		return false
	}
	return true
}

func (s *Service) Events(ctx context.Context, after uint64, limit int, filter EventFilter) (EventPage, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return EventPage{}, err
	}
	page := EventPage{Items: make([]repository.Event, 0), NextAfter: after, Latest: state.Revision}
	if len(state.Events) > 0 {
		first := state.Events[0].Sequence
		page.Truncated = first > 1 && after < first-1
	}
	for _, event := range state.Events {
		if event.Sequence <= after {
			continue
		}
		// The cursor advances over every examined event, including non-matching
		// ones, so pages with few matches cannot stall, repeat, or skip events.
		page.NextAfter = event.Sequence
		if filter.matches(event) {
			page.Items = append(page.Items, event)
			if len(page.Items) == limit {
				break
			}
		}
	}
	return page, nil
}
