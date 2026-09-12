package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Repository struct {
	mu     sync.RWMutex
	state  *State
	path   string
	lock   *os.File
	closed bool
}

func Open(directory string) (*Repository, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	lock, err := acquireLock(filepath.Join(directory, ".compatibility.lock"))
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "state.json")
	state, err := readState(path)
	if err != nil {
		releaseLock(lock)
		return nil, err
	}
	return &Repository{state: state, path: path, lock: lock}, nil
}

func (r *Repository) Snapshot(ctx context.Context) (*State, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return nil, fmt.Errorf("repository is closed")
	}
	return r.state.Clone()
}

// Update serializes writers and makes failure atomic for memory and disk.
// The mutation function must not retain references after returning.
func (r *Repository) Update(ctx context.Context, mutate func(*State) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("repository is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	candidate, err := r.state.Clone()
	if err != nil {
		return err
	}
	if err := mutate(candidate); err != nil {
		return err
	}
	// A commit may record several events atomically (for example a change-set
	// application updates multiple plans and environments together), but every
	// revision step must be backed by exactly one event. The tail check stays
	// valid even when the bounded event history was trimmed during the commit.
	if candidate.Revision <= r.state.Revision {
		return fmt.Errorf("mutation must advance the state revision")
	}
	delta := int(candidate.Revision - r.state.Revision)
	tail := delta
	if tail > len(candidate.Events) {
		tail = len(candidate.Events)
	}
	start := candidate.Revision - uint64(tail) + 1
	for i, event := range candidate.Events[len(candidate.Events)-tail:] {
		if event.Sequence != start+uint64(i) {
			return fmt.Errorf("mutation must record one event per revision step")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeState(r.path, candidate); err != nil {
		return err
	}
	r.state = candidate
	return nil
}

func (r *Repository) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return releaseLock(r.lock)
}
