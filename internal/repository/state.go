package repository

import (
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
