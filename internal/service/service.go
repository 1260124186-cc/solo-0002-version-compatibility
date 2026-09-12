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

func freshID(prefix string) (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(data[:]), nil
}

func freshPlanID() (string, error) { return freshID("plan") }

func freshChangeSetID() (string, error) { return freshID("set") }

func now() time.Time { return time.Now().UTC() }

func (s *Service) Resolve(ctx context.Context, input domain.ResolutionInput) (domain.Resolution, error) {
	snapshot, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Resolution{}, err
	}
	return s.solver.Resolve(ctx, snapshot.Catalog, input.Roots)
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
