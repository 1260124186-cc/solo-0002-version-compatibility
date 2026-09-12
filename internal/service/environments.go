package service

import (
	"context"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
)

func (s *Service) CreateEnvironment(ctx context.Context, input domain.EnvironmentInput) (domain.Environment, error) {
	if err := domain.ValidateID(input.ID); err != nil {
		return domain.Environment{}, err
	}
	if err := domain.ValidateText(input.Name, "name", 1, 120); err != nil {
		return domain.Environment{}, err
	}
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Environment{}, err
	}
	if _, exists := state.Environments[input.ID]; exists {
		return domain.Environment{}, domain.Conflict("environment already exists")
	}
	resolved, err := s.solver.Resolve(ctx, state.Catalog, input.Roots)
	if err != nil {
		return domain.Environment{}, err
	}
	at := now()
	env := domain.Environment{ID: input.ID, Name: input.Name, Roots: domain.CopyStrings(input.Roots), Resolved: resolved.Resolved, Revision: 1, CreatedAt: at, UpdatedAt: at}
	err = s.repo.Update(ctx, func(current *repository.State) error {
		if err := checkCatalog(resolved.CatalogRevision, current); err != nil {
			return err
		}
		if _, exists := current.Environments[input.ID]; exists {
			return domain.Conflict("environment already exists")
		}
		if len(current.Environments) >= 200 {
			return domain.Limit("environment capacity reached")
		}
		current.Environments[input.ID] = env
		current.RootTimelines[input.ID] = domain.RootTimeline{
			EnvironmentID: input.ID,
			Entries: []domain.RootTimelineEntry{{
				Sequence:      1,
				Type:          domain.RootTimelineCreated,
				At:            at,
				EventSequence: current.Revision + 1,
				AfterRevision: env.Revision,
				After:         domain.CopyStrings(env.Roots),
				RootChanges:   domain.RootDiff(nil, env.Roots),
			}},
		}
		current.Record("environment", input.ID, "created", at)
		return nil
	})
	return env, err
}

func (s *Service) ListEnvironments(ctx context.Context) ([]domain.Environment, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]domain.Environment, 0, len(state.Environments))
	for _, id := range domain.SortedKeys(state.Environments) {
		items = append(items, state.Environments[id])
	}
	return items, nil
}

func (s *Service) Environment(ctx context.Context, id string) (domain.Environment, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.Environment{}, err
	}
	env, exists := state.Environments[id]
	if !exists {
		return env, domain.Missing("environment", id)
	}
	return env, nil
}

func (s *Service) RootTimeline(ctx context.Context, id, planID string) (domain.RootTimeline, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.RootTimeline{}, err
	}
	timeline, exists := state.RootTimelines[id]
	if !exists {
		return timeline, domain.Missing("environment", id)
	}
	if planID != "" {
		plan, exists := state.Plans[planID]
		if !exists || plan.EnvironmentID != id {
			return timeline, domain.Missing("plan", planID)
		}
		entries := make([]domain.RootTimelineEntry, 0)
		for _, entry := range timeline.Entries {
			if entry.PlanID == planID {
				entries = append(entries, entry)
			}
		}
		timeline.Entries = entries
	}
	return timeline, nil
}
