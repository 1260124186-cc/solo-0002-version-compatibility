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
	if candidate.Revision != r.state.Revision+1 {
		return fmt.Errorf("mutation must record exactly one event")
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
