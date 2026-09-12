package service

import (
	"context"
	"sort"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
	"solo-0002-version-compatibility/internal/resolution"
)

// CheckEnvironmentDrift verifies a caller-reported installed set against one
// environment revision. The check is read-only with respect to environments
// and plans: it only appends its own immutable record.
func (s *Service) CheckEnvironmentDrift(ctx context.Context, environmentID string, input domain.DriftCheckInput) (domain.DriftCheck, error) {
	if input.EnvironmentRevision == 0 {
		return domain.DriftCheck{}, domain.Invalid("environment_revision must be positive")
	}
	if err := domain.ValidateInstalled(input.Installed); err != nil {
		return domain.DriftCheck{}, err
	}
	id, err := freshID("drift")
	if err != nil {
		return domain.DriftCheck{}, err
	}
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.DriftCheck{}, err
	}
	env, exists := state.Environments[environmentID]
	if !exists {
		return domain.DriftCheck{}, domain.Missing("environment", environmentID)
	}
	if err := checkDriftEnvironmentRevision(env, input.EnvironmentRevision); err != nil {
		return domain.DriftCheck{}, err
	}
	report, err := resolution.EvaluateDrift(ctx, state.Catalog, env.Resolved, input.Installed)
	if err != nil {
		return domain.DriftCheck{}, err
	}
	at := now()
	check := domain.DriftCheck{
		ID:                  id,
		EnvironmentID:       environmentID,
		EnvironmentRevision: env.Revision,
		CatalogRevision:     state.Catalog.Revision,
		Installed:           domain.CopyStrings(input.Installed),
		Missing:             report.Missing,
		Extra:               report.Extra,
		VersionMismatches:   report.VersionMismatches,
		Violations:          report.Violations,
		Unverifiable:        report.Unverifiable,
		CreatedAt:           at,
	}
	check.Conformant = !check.HasFindings()
	var stored domain.DriftCheck
	err = s.repo.Update(ctx, func(current *repository.State) error {
		latest, exists := current.Environments[environmentID]
		if !exists {
			return domain.Missing("environment", environmentID)
		}
		if err := checkDriftEnvironmentRevision(latest, input.EnvironmentRevision); err != nil {
			return err
		}
		if err := checkCatalog(state.Catalog.Revision, current); err != nil {
			return err
		}
		if len(current.DriftChecks) >= domain.MaxDriftChecks {
			return domain.Limit("drift check record capacity reached")
		}
		stored = check
		current.DriftChecks[id] = stored
		current.Record("drift_check", id, "checked", at)
		return nil
	})
	return stored, err
}

func checkDriftEnvironmentRevision(env domain.Environment, revision uint64) error {
	if env.Revision != revision {
		return domain.Conflict("environment revision is %d, drift check requested %d; refetch the environment and resubmit", env.Revision, revision)
	}
	return nil
}

func (s *Service) DriftCheck(ctx context.Context, id string) (domain.DriftCheck, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.DriftCheck{}, err
	}
	check, exists := state.DriftChecks[id]
	if !exists {
		return check, domain.Missing("drift check", id)
	}
	return check, nil
}

func (s *Service) ListDriftChecks(ctx context.Context, environmentID string) ([]domain.DriftCheck, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if environmentID != "" {
		if _, exists := state.Environments[environmentID]; !exists {
			return nil, domain.Missing("environment", environmentID)
		}
	}
	items := make([]domain.DriftCheck, 0)
	for _, check := range state.DriftChecks {
		if environmentID == "" || check.EnvironmentID == environmentID {
			items = append(items, check)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}
