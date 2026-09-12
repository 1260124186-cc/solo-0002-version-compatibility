package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/locking"
	"solo-0002-version-compatibility/internal/repository"
)

// LockReport is the read-only verification/export view of a lockfile.
type LockReport struct {
	Lockfile domain.Lockfile `json:"lockfile"`
	// Drift is present only for verification; it describes differences for
	// inspection and never triggers a mutation.
	Drift *domain.LockDrift `json:"drift,omitempty"`
}

func freshLockID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return "lock-" + hex.EncodeToString(data[:]), nil
}

// CreateLockfile freezes the current state of one environment revision into an
// immutable evidence record. Each (environment, revision) may be locked only
// once, so different revisions produce distinct, traceable records.
func (s *Service) CreateLockfile(ctx context.Context, environmentID string) (domain.Lockfile, error) {
	if err := domain.ValidateID(environmentID); err != nil {
		return domain.Lockfile{}, err
	}
	id, err := freshLockID()
	if err != nil {
		return domain.Lockfile{}, err
	}
	at := now()
	var created domain.Lockfile
	err = s.repo.Update(ctx, func(state *repository.State) error {
		env, exists := state.Environments[environmentID]
		if !exists {
			return domain.Missing("environment", environmentID)
		}
		for _, existing := range state.Lockfiles {
			if existing.EnvironmentID == environmentID && existing.EnvironmentRevision == env.Revision {
				return domain.Conflict("environment %s revision %d is already locked by %s; a lockfile cannot be rewritten", environmentID, env.Revision, existing.ID)
			}
		}
		if len(state.Lockfiles) >= repository.MaxLockfiles {
			return domain.Limit("lockfile capacity reached")
		}
		lock, buildErr := locking.Build(state.Catalog, env, id, at)
		if buildErr != nil {
			return buildErr
		}
		state.Lockfiles[id] = lock
		state.Record("lockfile", id, "created", at)
		created = lock
		return nil
	})
	return created, err
}

// Lockfile returns one stored immutable lock record.
func (s *Service) Lockfile(ctx context.Context, id string) (domain.Lockfile, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Lockfile{}, err
	}
	lock, exists := state.Lockfiles[id]
	if !exists {
		return lock, domain.Missing("lockfile", id)
	}
	return lock, nil
}

// ListLockfiles lists lock records, optionally scoped to one environment, in
// deterministic (environment, revision, id) order.
func (s *Service) ListLockfiles(ctx context.Context, environmentID string) ([]domain.Lockfile, error) {
	if environmentID != "" {
		if err := domain.ValidateID(environmentID); err != nil {
			return nil, err
		}
	}
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if environmentID != "" {
		if _, exists := state.Environments[environmentID]; !exists {
			return nil, domain.Missing("environment", environmentID)
		}
	}
	items := make([]domain.Lockfile, 0, len(state.Lockfiles))
	for _, lock := range state.Lockfiles {
		if environmentID == "" || lock.EnvironmentID == environmentID {
			items = append(items, lock)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].EnvironmentID != items[j].EnvironmentID {
			return items[i].EnvironmentID < items[j].EnvironmentID
		}
		if items[i].EnvironmentRevision != items[j].EnvironmentRevision {
			return items[i].EnvironmentRevision < items[j].EnvironmentRevision
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func evaluate(ctx context.Context, repo *repository.Repository, lock domain.Lockfile) (domain.LockDrift, error) {
	if err := locking.Verify(lock); err != nil {
		return domain.LockDrift{}, err
	}
	state, err := repo.Snapshot(ctx)
	if err != nil {
		return domain.LockDrift{}, err
	}
	drift := locking.Evaluate(lock, state.Catalog, lookupEnv(state, lock.EnvironmentID))
	drift.DigestValid = true
	return drift, nil
}

func lookupEnv(state *repository.State, id string) *domain.Environment {
	if env, exists := state.Environments[id]; exists {
		return &env
	}
	return nil
}

// VerifyStoredLockfile checks a stored lock's integrity and reports its drift
// from the live catalog/environment. Drift is informational only.
func (s *Service) VerifyStoredLockfile(ctx context.Context, id string) (LockReport, error) {
	lock, err := s.Lockfile(ctx, id)
	if err != nil {
		return LockReport{}, err
	}
	drift, err := evaluate(ctx, s.repo, lock)
	if err != nil {
		return LockReport{}, err
	}
	return LockReport{Lockfile: lock, Drift: &drift}, nil
}

// VerifyExportedLockfile checks a lockfile document supplied by the caller
// (e.g. one exported elsewhere). Nothing is persisted.
func (s *Service) VerifyExportedLockfile(ctx context.Context, lock domain.Lockfile) (LockReport, error) {
	drift, err := evaluate(ctx, s.repo, lock)
	if err != nil {
		return LockReport{}, err
	}
	return LockReport{Lockfile: lock, Drift: &drift}, nil
}

// ExportLockfile returns a stored lock document exactly as it was created.
func (s *Service) ExportLockfile(ctx context.Context, id string) (domain.Lockfile, error) {
	return s.Lockfile(ctx, id)
}
