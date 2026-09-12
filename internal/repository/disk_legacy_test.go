package repository

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A schema 1 state.json produced by the pre-visibility binary must be accepted
// by the current binary through the real open/read path, not just the
// in-memory validator.
func TestOpenBootsSchemaOneStateFromDisk(t *testing.T) {
	directory := t.TempDir()
	legacy := map[string]any{
		"schema":   1,
		"revision": 1,
		"catalog": map[string]any{
			"revision": 1,
			"components": map[string]any{
				"legacy-core": map[string]any{"id": "legacy-core", "name": "core", "description": "", "created_at": "2024-01-01T00:00:00Z"},
			},
			"releases": map[string]any{
				"legacy-core": map[string]any{
					"1.0.0": map[string]any{
						"component_id": "legacy-core", "version": "1.0.0",
						"requires": map[string]string{}, "state": "available",
						"created_at": "2024-01-01T00:00:00Z",
					},
				},
			},
		},
		"environments": map[string]any{},
		"plans":        map[string]any{},
		"events": []map[string]any{
			{"sequence": 1, "kind": "component", "entity_id": "legacy-core", "action": "created", "at": "2024-01-01T00:00:00Z"},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "state.json"), data, 0600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	repo, err := Open(directory)
	if err != nil {
		t.Fatalf("current binary refused a schema 1 state: %v", err)
	}
	defer repo.Close()

	snapshot, err := repo.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.Schema != 2 {
		t.Fatalf("loaded schema should be migrated in memory to 2, got %d", snapshot.Schema)
	}
	component := snapshot.Catalog.Components["legacy-core"]
	if component.Visibility != "" {
		t.Fatalf("legacy component must default to public, got %q", component.Visibility)
	}
}
