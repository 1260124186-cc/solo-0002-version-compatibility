package repository

import (
	"encoding/json"
	"time"

	"solo-0002-version-compatibility/internal/domain"
)

type Event struct {
	Sequence uint64    `json:"sequence"`
	Kind     string    `json:"kind"`
	EntityID string    `json:"entity_id"`
	Action   string    `json:"action"`
	At       time.Time `json:"at"`
}

type State struct {
	Schema       int                           `json:"schema"`
	Revision     uint64                        `json:"revision"`
	Catalog      domain.Catalog                `json:"catalog"`
	Environments map[string]domain.Environment `json:"environments"`
	Plans        map[string]domain.Plan        `json:"plans"`
	DriftChecks  map[string]domain.DriftCheck  `json:"drift_checks"`
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
		DriftChecks:  make(map[string]domain.DriftCheck),
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

func (s *State) Record(kind, id, action string, at time.Time) {
	s.Revision++
	s.Events = append(s.Events, Event{
		Sequence: s.Revision,
		Kind:     kind,
		EntityID: id,
		Action:   action,
		At:       at,
	})
	if len(s.Events) > 10000 {
		s.Events = s.Events[len(s.Events)-10000:]
	}
}
