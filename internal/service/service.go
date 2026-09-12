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

func rejectNewRetiredComponents(catalog domain.Catalog, selected map[string]string, existing map[string]string, allowExisting bool) error {
	for _, id := range domain.SortedKeys(selected) {
		if allowExisting {
			if _, alreadyUsed := existing[id]; alreadyUsed {
				continue
			}
		}
		if component, exists := catalog.Components[id]; exists && component.State == domain.Retired {
			if allowExisting {
				return domain.Conflict("retired component %s cannot be added to an existing environment", id)
			}
			return domain.Conflict("retired component %s cannot be referenced by a new environment", id)
		}
	}
	return nil
}
