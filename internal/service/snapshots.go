package service

import (
	"context"
	"fmt"

	"solo-0002-version-compatibility/internal/domain"
)

func (s *Service) ListSnapshots(ctx context.Context, environmentID string) ([]domain.EnvironmentSnapshot, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	env, exists := state.Environments[environmentID]
	if !exists {
		return nil, domain.Missing("environment", environmentID)
	}
	revisions := state.Snapshots[environmentID]
	items := make([]domain.EnvironmentSnapshot, 0, len(revisions))
	for revision := uint64(1); revision <= env.Revision; revision++ {
		if snapshot, ok := revisions[revision]; ok {
			items = append(items, snapshot)
		}
	}
	return items, nil
}

func (s *Service) Snapshot(ctx context.Context, environmentID string, revision uint64) (domain.EnvironmentSnapshot, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.EnvironmentSnapshot{}, err
	}
	if _, exists := state.Environments[environmentID]; !exists {
		return domain.EnvironmentSnapshot{}, domain.Missing("environment", environmentID)
	}
	snapshot, exists := state.Snapshots[environmentID][revision]
	if !exists {
		return snapshot, domain.Missing("snapshot", fmt.Sprintf("%s@%d", environmentID, revision))
	}
	return snapshot, nil
}

func (s *Service) VerifySnapshot(ctx context.Context, environmentID string, revision, catalogRevision uint64) (domain.SnapshotVerification, error) {
	if catalogRevision == 0 {
		return domain.SnapshotVerification{}, domain.Invalid("catalog_revision must be positive")
	}
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.SnapshotVerification{}, err
	}
	if _, exists := state.Environments[environmentID]; !exists {
		return domain.SnapshotVerification{}, domain.Missing("environment", environmentID)
	}
	snapshot, exists := state.Snapshots[environmentID][revision]
	if !exists {
		return domain.SnapshotVerification{}, domain.Missing("snapshot", fmt.Sprintf("%s@%d", environmentID, revision))
	}
	if state.Catalog.Revision != catalogRevision {
		return domain.SnapshotVerification{}, domain.Conflict("catalog revision is %d, received %d", state.Catalog.Revision, catalogRevision)
	}
	return domain.VerifySnapshot(snapshot, state.Catalog, now()), nil
}
