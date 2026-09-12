package repository

import (
	"encoding/json"
	"fmt"
	"time"

	"solo-0002-version-compatibility/internal/domain"
)

// SchemaVersion is the persisted layout this build reads and writes.
// Schema 2 adds environment revision provenance.
const SchemaVersion = 2

type Event struct {
	Sequence uint64    `json:"sequence"`
	Kind     string    `json:"kind"`
	EntityID string    `json:"entity_id"`
	Action   string    `json:"action"`
	At       time.Time `json:"at"`
}

type State struct {
	Schema       int                            `json:"schema"`
	Revision     uint64                         `json:"revision"`
	Catalog      domain.Catalog                 `json:"catalog"`
	Environments map[string]domain.Environment  `json:"environments"`
	Plans        map[string]domain.Plan         `json:"plans"`
	Provenance   map[string][]domain.Provenance `json:"provenance"`
	Events       []Event                        `json:"events"`
}

func NewState() *State {
	return &State{
		Schema: SchemaVersion,
		Catalog: domain.Catalog{
			Components: make(map[string]domain.Component),
			Releases:   make(map[string]map[string]domain.Release),
		},
		Environments: make(map[string]domain.Environment),
		Plans:        make(map[string]domain.Plan),
		Provenance:   make(map[string][]domain.Provenance),
		Events:       make([]Event, 0),
	}
}

// migrateState upgrades persisted layouts to the current schema. Schema 1
// predates provenance tracking, so its environments can only establish a
// baseline origin from their current state; earlier history stays unknown.
func migrateState(s *State) error {
	switch s.Schema {
	case 1:
		s.Schema = SchemaVersion
		s.establishBaselines()
	case SchemaVersion:
	default:
		return fmt.Errorf("unsupported schema %d", s.Schema)
	}
	return nil
}

// establishBaselines initializes the provenance collection and records a
// baseline origin for every environment that has no provenance yet.
func (s *State) establishBaselines() {
	if s.Provenance == nil {
		s.Provenance = make(map[string][]domain.Provenance)
	}
	for id, env := range s.Environments {
		if len(s.Provenance[id]) > 0 {
			continue
		}
		s.Provenance[id] = []domain.Provenance{{
			EnvironmentID:   id,
			Revision:        env.Revision,
			Kind:            domain.OriginBaseline,
			CatalogRevision: s.Catalog.Revision,
			Roots:           domain.CopyStrings(env.Roots),
			Resolved:        domain.CopyStrings(env.Resolved),
			At:              env.UpdatedAt,
		}}
	}
}

// AppendProvenance records the origin of an environment revision. It must run
// inside the same mutation that produces the revision so both commit or fail
// together.
func (s *State) AppendProvenance(record domain.Provenance) {
	if s.Provenance == nil {
		s.Provenance = make(map[string][]domain.Provenance)
	}
	s.Provenance[record.EnvironmentID] = append(s.Provenance[record.EnvironmentID], record)
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
