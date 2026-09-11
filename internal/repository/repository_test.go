package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"solo-0002-version-compatibility/internal/domain"
)

func testComponent(id string) domain.Component {
	return domain.Component{ID: id, Name: id, CreatedAt: time.Unix(1, 0).UTC()}
}

func openTempRepository(t *testing.T) *Repository {
	t.Helper()
	dir := t.TempDir()
	repo, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(%q) returned error: %v", dir, err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func addComponent(state *State, id string, at time.Time) {
	state.Catalog.Components[id] = testComponent(id)
	state.Catalog.Releases[id] = make(map[string]domain.Release)
	state.Catalog.Revision++
	state.Record("component", id, "created", at)
}

func dataDirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read data directory: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func TestMutationErrorLeavesStateAndDiskUntouched(t *testing.T) {
	dir := t.TempDir()
	repo, err := Open(dir)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	ctx := context.Background()

	// Establish a committed baseline (revision 1, one event).
	if err := repo.Update(ctx, func(state *State) error {
		addComponent(state, "comp-a", time.Unix(1, 0).UTC())
		return nil
	}); err != nil {
		t.Fatalf("baseline commit failed: %v", err)
	}
	baseline, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read baseline state: %v", err)
	}

	// A mutator that dirties the candidate and then fails must not commit.
	sentinel := errors.New("mutator rejected")
	err = repo.Update(ctx, func(state *State) error {
		addComponent(state, "comp-b", time.Unix(2, 0).UTC())
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error to surface, got %v", err)
	}

	// In-memory state keeps the baseline revision, catalog and event log.
	snapshot, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot after failed update: %v", err)
	}
	if snapshot.Revision != 1 {
		t.Errorf("revision changed after failed mutation: %d", snapshot.Revision)
	}
	if _, exists := snapshot.Catalog.Components["comp-b"]; exists {
		t.Errorf("rejected component leaked into memory state")
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].EntityID != "comp-a" {
		t.Errorf("event log altered by failed mutation: %+v", snapshot.Events)
	}

	// Disk state must be byte-identical to the committed baseline.
	after, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read state after failed update: %v", err)
	}
	if string(after) != string(baseline) {
		t.Errorf("state.json changed despite failed mutation")
	}
	if names := dataDirEntries(t, dir); len(names) != 2 ||
		names[0] != ".compatibility.lock" || names[1] != "state.json" {
		t.Errorf("failed write left unexpected directory entries: %v", names)
	}
}

func TestWriteFailureKeepsMemoryAndEventsUnchanged(t *testing.T) {
	dir := t.TempDir()
	repo, err := Open(dir)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	ctx := context.Background()

	if err := repo.Update(ctx, func(state *State) error {
		addComponent(state, "comp-a", time.Unix(1, 0).UTC())
		return nil
	}); err != nil {
		t.Fatalf("baseline commit failed: %v", err)
	}

	// Replace state.json with a directory of the same name: renaming a file
	// onto a non-empty directory fails, simulating a commit-time write failure.
	statePath := filepath.Join(dir, "state.json")
	if err := os.Remove(statePath); err != nil {
		t.Fatalf("remove baseline state file: %v", err)
	}
	if err := os.Mkdir(statePath, 0700); err != nil {
		t.Fatalf("create blocking directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(statePath, "blocker"), []byte("x"), 0600); err != nil {
		t.Fatalf("plant blocker file: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(statePath) })

	err = repo.Update(ctx, func(state *State) error {
		addComponent(state, "comp-b", time.Unix(2, 0).UTC())
		return nil
	})
	if err == nil {
		t.Fatal("expected persistence error when rename target is a non-empty directory")
	}

	// The candidate must never have replaced the live memory state.
	snapshot, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot after write failure: %v", err)
	}
	if snapshot.Revision != 1 {
		t.Errorf("memory revision advanced after a failed write: %d", snapshot.Revision)
	}
	if _, exists := snapshot.Catalog.Components["comp-b"]; exists {
		t.Errorf("uncommitted component visible in memory after write failure")
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].EntityID != "comp-a" {
		t.Errorf("event log changed after a failed write: %+v", snapshot.Events)
	}

	// No temp file may survive a failed write.
	tmpEntries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read directory after failed write: %v", err)
	}
	for _, entry := range tmpEntries {
		if strings.HasPrefix(entry.Name(), ".compatibility-") && strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("temporary write file was not cleaned up: %s", entry.Name())
		}
	}
}

func TestLockPreventsSecondProcessOnSameDirectory(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open returned error: %v", err)
	}
	defer first.Close()

	second, err := Open(dir)
	if err == nil {
		_ = second.Close()
		t.Fatal("second Open on a locked data directory must fail")
	}
}

func TestSnapshotIsolation(t *testing.T) {
	repo := openTempRepository(t)
	ctx := context.Background()
	if err := repo.Update(ctx, func(state *State) error {
		addComponent(state, "comp-a", time.Unix(1, 0).UTC())
		return nil
	}); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	snapshot, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	snapshot.Catalog.Components["tampered"] = testComponent("tampered")
	snapshot.Catalog.Releases["tampered"] = make(map[string]domain.Release)
	snapshot.Events = append(snapshot.Events, Event{Sequence: 99})
	snapshot.Revision = 99

	fresh, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatalf("second snapshot failed: %v", err)
	}
	if _, exists := fresh.Catalog.Components["tampered"]; exists {
		t.Fatal("mutating a snapshot leaked into repository state")
	}
	if fresh.Revision != 1 || len(fresh.Events) != 1 {
		t.Fatalf("repository state altered through a snapshot copy: %+v", fresh)
	}
}
