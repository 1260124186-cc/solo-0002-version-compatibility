package repository_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"solo-0002-version-compatibility/internal/repository"
	"solo-0002-version-compatibility/internal/service"
)

// trimmedRepository writes a state whose event history has already been
// trimmed to the latest 10000 records: revision 10003, sequences 4..10003.
func trimmedRepository(t *testing.T) *repository.Repository {
	t.Helper()
	directory := t.TempDir()
	state := repository.NewState()
	state.Revision = 10003
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	state.Events = make([]repository.Event, 0, 10000)
	for seq := uint64(4); seq <= 10003; seq++ {
		kind, action := "component", "created"
		if seq%2 == 0 {
			kind, action = "release", "added"
		}
		state.Events = append(state.Events, repository.Event{
			Sequence: seq,
			Kind:     kind,
			EntityID: "entity",
			Action:   action,
			At:       base.Add(time.Duration(seq) * time.Second),
		})
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "state.json"), data, 0600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	repo, err := repository.Open(directory)
	if err != nil {
		t.Fatalf("open trimmed repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

// TestEventsAfterTrimming checks truncation reporting and sparse filtered
// paging once events 1..3 have been pruned by the 10000-record retention rule.
func TestEventsAfterTrimming(t *testing.T) {
	svc := service.New(trimmedRepository(t), 50000)
	ctx := context.Background()

	page, err := svc.Events(ctx, 0, 1, service.EventFilter{})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if !page.Truncated {
		t.Fatalf("cursor before the retained window must report truncated")
	}
	if page.Latest != 10003 {
		t.Fatalf("latest = %d, want 10003", page.Latest)
	}
	if page, err = svc.Events(ctx, 2, 1, service.EventFilter{}); err != nil || !page.Truncated {
		t.Fatalf("after=2 must still be truncated, got %+v, err=%v", page, err)
	}
	// after = first-1 sits exactly at the boundary: nothing readable is missing.
	if page, err = svc.Events(ctx, 3, 1, service.EventFilter{}); err != nil || page.Truncated {
		t.Fatalf("after=3 must not be truncated, got %+v, err=%v", page, err)
	}
	// Filters must not suppress the truncation signal.
	page, err = svc.Events(ctx, 0, 1, service.EventFilter{Action: "created"})
	if err != nil {
		t.Fatalf("filtered events: %v", err)
	}
	if !page.Truncated {
		t.Fatalf("truncated must be reported even with an action filter")
	}

	// Page through a sparse filter (odd sequences 5,7,...,10003) with limit 7.
	filter := service.EventFilter{Action: "created"}
	seen := make(map[uint64]bool)
	var previous uint64
	cursor := uint64(3)
	for {
		page, err := svc.Events(ctx, cursor, 7, filter)
		if err != nil {
			t.Fatalf("filtered events after %d: %v", cursor, err)
		}
		if page.Truncated {
			t.Fatalf("paging inside the retained window must not be truncated")
		}
		if page.NextAfter < cursor {
			t.Fatalf("cursor moved backwards: %d -> %d", cursor, page.NextAfter)
		}
		for _, event := range page.Items {
			if event.Action != "created" || event.Sequence%2 != 1 {
				t.Fatalf("filter leaked event %+v", event)
			}
			if event.Sequence <= previous || seen[event.Sequence] {
				t.Fatalf("duplicate or out-of-order event %d", event.Sequence)
			}
			seen[event.Sequence] = true
			previous = event.Sequence
		}
		if page.NextAfter == cursor {
			if len(page.Items) != 0 {
				t.Fatalf("cursor stalled while items were returned")
			}
			break
		}
		cursor = page.NextAfter
	}
	if cursor != 10003 {
		t.Fatalf("filtered paging ended at %d, want latest 10003", cursor)
	}
	if len(seen) != 5000 || !seen[5] || !seen[10003] {
		t.Fatalf("collected %d events spanning [%v,%v], want 5000 spanning [5,10003]", len(seen), seen[5], seen[10003])
	}

	// Inclusive time bounds combined with cursor and entity filters.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	window := service.EventFilter{
		EntityID: "entity",
		Start:    base.Add(10 * time.Second),
		HasStart: true,
		End:      base.Add(12 * time.Second),
		HasEnd:   true,
	}
	page, err = svc.Events(ctx, 3, 50, window)
	if err != nil {
		t.Fatalf("time-window events: %v", err)
	}
	var got []uint64
	for _, event := range page.Items {
		got = append(got, event.Sequence)
	}
	want := []uint64{10, 11, 12}
	if len(got) != len(want) {
		t.Fatalf("inclusive window = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("inclusive window = %v, want %v", got, want)
		}
	}
}
