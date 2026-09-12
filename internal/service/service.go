package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
	"solo-0002-version-compatibility/internal/resolution"
)

type Service struct {
	repo   *repository.Repository
	solver resolution.Solver
}

func New(repo *repository.Repository, maxSteps int) *Service {
	return &Service{repo: repo, solver: resolution.Solver{MaxSteps: maxSteps, MaxNodes: 128}}
}

func freshID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return "plan-" + hex.EncodeToString(data[:]), nil
}

func now() time.Time { return time.Now().UTC() }

func (s *Service) Resolve(ctx context.Context, input domain.ResolutionInput) (domain.Resolution, error) {
	if (input.EnvironmentID == "") != (input.EnvironmentRevision == 0) {
		return domain.Resolution{}, domain.Invalid("environment_id and environment_revision must be provided together")
	}
	snapshot, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Resolution{}, err
	}
	if input.EnvironmentID == "" {
		return s.solver.Resolve(ctx, snapshot.Catalog, input.Roots)
	}
	if err := domain.ValidateID(input.EnvironmentID); err != nil {
		return domain.Resolution{}, err
	}
	env, exists := snapshot.Environments[input.EnvironmentID]
	if !exists {
		return domain.Resolution{}, domain.Missing("environment", input.EnvironmentID)
	}
	if env.Revision != input.EnvironmentRevision {
		return domain.Resolution{}, domain.Conflict("environment revision is %d, expected %d", env.Revision, input.EnvironmentRevision)
	}
	result, err := s.solver.ResolvePreferring(ctx, snapshot.Catalog, input.Roots, env.Resolved)
	if err != nil {
		return domain.Resolution{}, err
	}
	// Resolving only reads the environment; the diff is reported, never applied.
	result.EnvironmentRevision = env.Revision
	changes := resolution.Diff(env.Resolved, result.Resolved)
	result.Changes = &changes
	return result, nil
}

func checkCatalog(revision uint64, state *repository.State) error {
	if state.Catalog.Revision != revision {
		return domain.Conflict("component versions changed during computation; retry against the current catalog")
	}
	return nil
}

func checkEnvironment(env domain.Environment, revision uint64) error {
	if env.Revision != revision {
		return domain.Conflict("environment revision is %d, expected %d; create a new plan", env.Revision, revision)
	}
	return nil
}
