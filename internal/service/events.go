package service

import (
	"context"

	"solo-0002-version-compatibility/internal/repository"
)

type EventPage struct {
	Items     []repository.Event `json:"items"`
	NextAfter uint64             `json:"next_after"`
	Latest    uint64             `json:"latest"`
	Truncated bool               `json:"truncated"`
}

func (s *Service) Events(ctx context.Context, after uint64, limit int, entityID string) (EventPage, error) {
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
		page.NextAfter = event.Sequence
		if entityID == "" || event.EntityID == entityID {
			event.Payload = nil // payloads serve replay; the change feed stays slim
			page.Items = append(page.Items, event)
		}
		if len(page.Items) == limit {
			break
		}
	}
	return page, nil
}
