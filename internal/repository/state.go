package repository

import (
	"encoding/json"
	"fmt"
	"time"

	"solo-0002-version-compatibility/internal/domain"
)

// EventPayload carries the post-image of every entity a mutation touched.
// Replay folds these images instead of re-running business logic, so a
// reconstruction depends only on the recorded events.
type EventPayload struct {
	Component   *domain.Component   `json:"component,omitempty"`
	Release     *domain.Release     `json:"release,omitempty"`
	Environment *domain.Environment `json:"environment,omitempty"`
	Plan        *domain.Plan        `json:"plan,omitempty"`
}

type Event struct {
	Sequence uint64        `json:"sequence"`
	Kind     string        `json:"kind"`
	EntityID string        `json:"entity_id"`
	Action   string        `json:"action"`
	At       time.Time     `json:"at"`
	Payload  *EventPayload `json:"payload,omitempty"`
}

type State struct {
	Schema       int                           `json:"schema"`
	Revision     uint64                        `json:"revision"`
	Catalog      domain.Catalog                `json:"catalog"`
	Environments map[string]domain.Environment `json:"environments"`
	Plans        map[string]domain.Plan        `json:"plans"`
	Events       []Event                       `json:"events"`
}

func NewState() *State {
	return &State{
		Schema: 1,
		Catalog: domain.Catalog{
			Components: make(map[string]domain.Component),
			Releases:   make(map[string]map[string]domain.Release),
		},
		Environments: make(map[string]domain.Environment),
		Plans:        make(map[string]domain.Plan),
		Events:       make([]Event, 0),
	}
}

// Clone keeps callers from sharing mutable maps across a commit boundary.
func (s *State) Clone() (*State, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var result State
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Record appends the event that describes a mutation. The payload is
// deep-copied so the historical record cannot drift with live state.
func (s *State) Record(kind, id, action string, at time.Time, payload EventPayload) error {
	if payload == (EventPayload{}) {
		return fmt.Errorf("event must record the affected entity")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	stored := EventPayload{}
	if err := json.Unmarshal(data, &stored); err != nil {
		return err
	}
	s.Revision++
	s.Events = append(s.Events, Event{
		Sequence: s.Revision,
		Kind:     kind,
		EntityID: id,
		Action:   action,
		At:       at,
		Payload:  &stored,
	})
	if len(s.Events) > 10000 {
		s.Events = s.Events[len(s.Events)-10000:]
	}
	return nil
}
