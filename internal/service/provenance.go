package service

import (
	"context"
	"fmt"

	"solo-0002-version-compatibility/internal/domain"
)

// ListProvenance returns the origin records of an environment, newest first.
func (s *Service) ListProvenance(ctx context.Context, environmentID string) ([]domain.Provenance, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if _, exists := state.Environments[environmentID]; !exists {
		return nil, domain.Missing("environment", environmentID)
	}
	records := state.Provenance[environmentID]
	items := make([]domain.Provenance, 0, len(records))
	for i := len(records) - 1; i >= 0; i-- {
		items = append(items, records[i])
	}
	return items, nil
}

// Provenance finds the origin record explaining one environment revision.
func (s *Service) Provenance(ctx context.Context, environmentID string, revision uint64) (domain.Provenance, error) {
	if revision == 0 {
		return domain.Provenance{}, domain.Invalid("revision must be positive")
	}
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Provenance{}, err
	}
	if _, exists := state.Environments[environmentID]; !exists {
		return domain.Provenance{}, domain.Missing("environment", environmentID)
	}
	for _, record := range state.Provenance[environmentID] {
		if record.Revision == revision {
			return record, nil
		}
	}
	return domain.Provenance{}, &domain.Fault{Code: "not_found", Detail: fmt.Sprintf("no provenance for environment %q revision %d", environmentID, revision)}
}
