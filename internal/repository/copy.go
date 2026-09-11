package repository

import "solo-0002-version-compatibility/internal/domain"

// This file owns the in-memory deep-copy boundary of the state. It is the only
// place allowed to duplicate a committed State: Snapshot hands a copy to
// readers and Update mutates a copy before the disk write commits it.
//
// The copy is constructed field by field on purpose: it is a memory safety
// mechanism and must not depend on encoding/json, which is the persistence
// format owned by disk.go. Adding a nested map, slice or pointer to State or
// any entity requires extending the clone path below and the domain clones.

// Clone returns a fully independent copy of the state. The result shares no
// mutable map, slice or pointer with the source, so mutating a snapshot or a
// commit candidate can never reach the committed state.
func (s *State) Clone() *State {
	if s == nil {
		return nil
	}
	result := &State{
		Schema:   s.Schema,
		Revision: s.Revision,
		Catalog:  s.Catalog.Clone(),
	}
	if s.Environments != nil {
		result.Environments = make(map[string]domain.Environment, len(s.Environments))
		for id, env := range s.Environments {
			result.Environments[id] = env.Clone()
		}
	}
	if s.Plans != nil {
		result.Plans = make(map[string]domain.Plan, len(s.Plans))
		for id, plan := range s.Plans {
			result.Plans[id] = plan.Clone()
		}
	}
	result.Events = cloneEvents(s.Events)
	return result
}

// cloneEvents copies the event backing array. Event currently holds only
// scalar values (including the value-type time.Time); a nested field added to
// Event must be deep-copied here.
func cloneEvents(events []Event) []Event {
	if events == nil {
		return nil
	}
	result := make([]Event, len(events))
	copy(result, events)
	return result
}
