package repository

import (
	"time"

	"solo-0002-version-compatibility/internal/domain"
)

// migrate upgrades a schema 1 state in place. Existing environments receive a
// baseline snapshot captured from their current state; earlier revisions are
// left absent rather than fabricated. The caller persists the result.
func migrate(state *State) {
	state.Snapshots = make(map[string]map[uint64]domain.EnvironmentSnapshot, len(state.Environments))
	at := time.Now().UTC()
	for id, env := range state.Environments {
		snapshot := domain.CaptureSnapshot(env, state.Catalog, at)
		snapshot.Origin = domain.SnapshotBaseline
		state.Snapshots[id] = map[uint64]domain.EnvironmentSnapshot{env.Revision: snapshot}
	}
	state.Schema = 2
}
