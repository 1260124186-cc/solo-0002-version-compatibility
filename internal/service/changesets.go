package service

import (
	"context"
	"sort"
	"time"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
)

type AppliedChangeSetResult struct {
	ChangeSet    domain.ChangeSet        `json:"change_set"`
	Applications []AppliedChangeSetEntry `json:"applications"`
	Environments []domain.Environment    `json:"environments"`
}

type AppliedChangeSetEntry struct {
	Plan        domain.Plan        `json:"plan"`
	Environment domain.Environment `json:"environment"`
}

// invalidateReferencingChangeSets marks every live (draft or ready) change set
// that references planID as invalidated. The set identified by skipID (for
// example the set currently applying that very plan) is left untouched.
// Terminal sets are always left alone.
func invalidateReferencingChangeSets(state *repository.State, planID, skipID, reason string, at time.Time) {
	setIDs := make([]string, 0)
	for id, set := range state.ChangeSets {
		if id == skipID || (set.State != domain.Draft && set.State != domain.Ready) {
			continue
		}
		for _, entry := range set.Entries {
			if entry.PlanID == planID {
				setIDs = append(setIDs, id)
				break
			}
		}
	}
	sort.Strings(setIDs)
	for _, id := range setIDs {
		set := state.ChangeSets[id]
		set.State = domain.Invalidated
		set.InvalidatedReason = reason
		set.Revision++
		set.UpdatedAt = at
		state.ChangeSets[id] = set
		state.Record("change_set", id, "invalidated", at)
	}
}

func (s *Service) CreateChangeSet(ctx context.Context, input domain.ChangeSetInput) (domain.ChangeSet, error) {
	if err := domain.ValidateText(input.Reason, "reason", 1, 1000); err != nil {
		return domain.ChangeSet{}, err
	}
	if len(input.PlanIDs) < domain.MinChangeSetPlans || len(input.PlanIDs) > domain.MaxChangeSetPlans {
		return domain.ChangeSet{}, domain.Invalid("change set must reference %d–%d plans", domain.MinChangeSetPlans, domain.MaxChangeSetPlans)
	}
	id, err := freshChangeSetID()
	if err != nil {
		return domain.ChangeSet{}, err
	}
	at := now()
	set := domain.ChangeSet{ID: id, Revision: 1, State: domain.Draft, Reason: input.Reason, Entries: make([]domain.ChangeSetEntry, 0, len(input.PlanIDs)), CreatedAt: at, UpdatedAt: at}
	err = s.repo.Update(ctx, func(state *repository.State) error {
		seenPlans := make(map[string]bool, len(input.PlanIDs))
		seenEnvs := make(map[string]bool, len(input.PlanIDs))
		entries := make([]domain.ChangeSetEntry, 0, len(input.PlanIDs))
		for _, planID := range input.PlanIDs {
			if err := domain.ValidateID(planID); err != nil {
				return err
			}
			if seenPlans[planID] {
				return domain.Invalid("plan %s is listed more than once", planID)
			}
			seenPlans[planID] = true
			plan, exists := state.Plans[planID]
			if !exists {
				return domain.Missing("plan", planID)
			}
			// A set can only bundle plans that are still usable; applied and
			// cancelled plans cannot be re-applied as part of a group.
			if plan.State != domain.Draft && plan.State != domain.Ready {
				return domain.Conflict("plan %s is %s and cannot join a change set", planID, plan.State)
			}
			if seenEnvs[plan.EnvironmentID] {
				return domain.Invalid("environment %s appears in more than one referenced plan", plan.EnvironmentID)
			}
			seenEnvs[plan.EnvironmentID] = true
			entries = append(entries, domain.ChangeSetEntry{PlanID: planID, EnvironmentID: plan.EnvironmentID})
		}
		if len(state.ChangeSets) >= domain.MaxChangeSets {
			return domain.Limit("change set capacity reached")
		}
		// Entries are ordered by environment so the set is deterministic.
		sort.Slice(entries, func(i, j int) bool { return entries[i].EnvironmentID < entries[j].EnvironmentID })
		set.Entries = entries
		state.ChangeSets[id] = set
		state.Record("change_set", id, "created", at)
		return nil
	})
	return set, err
}

func (s *Service) ChangeSet(ctx context.Context, id string) (domain.ChangeSet, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	set, exists := state.ChangeSets[id]
	if !exists {
		return set, domain.Missing("change set", id)
	}
	return set, nil
}

func (s *Service) ListChangeSets(ctx context.Context, phase string) ([]domain.ChangeSet, error) {
	valid := map[string]bool{domain.Draft: true, domain.Ready: true, domain.Applied: true, domain.Cancelled: true, domain.Invalidated: true}
	if phase != "" && !valid[phase] {
		return nil, domain.Invalid("unknown change set state")
	}
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]domain.ChangeSet, 0)
	for _, set := range state.ChangeSets {
		if phase != "" && set.State != phase {
			continue
		}
		items = append(items, set)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

// ValidateChangeSet confirms that every referenced plan is still applicable and
// records the plan/environment/catalog revision basis for the whole group.
func (s *Service) ValidateChangeSet(ctx context.Context, id string, revision uint64) (domain.ChangeSet, error) {
	var updated domain.ChangeSet
	err := s.repo.Update(ctx, func(state *repository.State) error {
		set, exists := state.ChangeSets[id]
		if !exists {
			return domain.Missing("change set", id)
		}
		if err := set.CheckRevision(revision); err != nil {
			return err
		}
		if err := set.CanValidate(); err != nil {
			return err
		}
		at := now()
		entries := make([]domain.ChangeSetEntry, 0, len(set.Entries))
		for _, entry := range set.Entries {
			plan, exists := state.Plans[entry.PlanID]
			if !exists {
				return domain.Conflict("plan %s no longer exists", entry.PlanID)
			}
			if plan.State != domain.Ready {
				return domain.Conflict("plan %s is %s; validate each plan before validating the change set", entry.PlanID, plan.State)
			}
			env, exists := state.Environments[entry.EnvironmentID]
			if !exists {
				return domain.Conflict("environment %s no longer exists", entry.EnvironmentID)
			}
			if err := checkEnvironment(env, plan.BaseRevision); err != nil {
				return err
			}
			if err := checkCatalog(plan.CatalogRevision, state); err != nil {
				return err
			}
			if err := repository.ValidateSelection(state.Catalog, plan.Roots, plan.Resolved, true); err != nil {
				return err
			}
			entries = append(entries, domain.ChangeSetEntry{
				PlanID:              plan.ID,
				EnvironmentID:       env.ID,
				PlanRevision:        plan.Revision,
				EnvironmentRevision: env.Revision,
				CatalogRevision:     plan.CatalogRevision,
			})
		}
		set.Entries = entries
		set.State = domain.Ready
		set.InvalidatedReason = ""
		set.Revision++
		set.UpdatedAt = at
		state.ChangeSets[id] = set
		state.Record("change_set", id, "validated", at)
		updated = set
		return nil
	})
	return updated, err
}

// ApplyChangeSet commits all referenced plans in one atomic transaction. If any
// recorded basis has expired the whole group is rejected and nothing changes.
func (s *Service) ApplyChangeSet(ctx context.Context, id string, revision uint64) (AppliedChangeSetResult, error) {
	var result AppliedChangeSetResult
	err := s.repo.Update(ctx, func(state *repository.State) error {
		set, exists := state.ChangeSets[id]
		if !exists {
			return domain.Missing("change set", id)
		}
		if err := set.CheckRevision(revision); err != nil {
			return err
		}
		if err := set.CanApply(); err != nil {
			return err
		}
		at := now()
		entries := make([]AppliedChangeSetEntry, 0, len(set.Entries))
		envs := make([]domain.Environment, 0, len(set.Entries))
		// Re-check the whole group before mutating anything so a single stale
		// basis rejects the entire operation.
		for _, entry := range set.Entries {
			plan, exists := state.Plans[entry.PlanID]
			if !exists {
				return domain.Conflict("plan %s no longer exists", entry.PlanID)
			}
			if plan.State != domain.Ready {
				return domain.Conflict("plan %s is %s; the change set must be re-validated", entry.PlanID, plan.State)
			}
			if plan.Revision != entry.PlanRevision {
				return domain.Conflict("plan %s revision changed since the change set was validated", entry.PlanID)
			}
			if err := checkCatalog(entry.CatalogRevision, state); err != nil {
				return err
			}
			env, exists := state.Environments[entry.EnvironmentID]
			if !exists {
				return domain.Conflict("environment %s no longer exists", entry.EnvironmentID)
			}
			if env.Revision != entry.EnvironmentRevision {
				return domain.Conflict("environment %s revision changed since the change set was validated", entry.EnvironmentID)
			}
			if err := checkEnvironment(env, plan.BaseRevision); err != nil {
				return err
			}
			if err := repository.ValidateSelection(state.Catalog, plan.Roots, plan.Resolved, true); err != nil {
				return err
			}
		}
		// All bases are fresh: commit plans and environments together.
		for _, entry := range set.Entries {
			plan := state.Plans[entry.PlanID]
			env := state.Environments[entry.EnvironmentID]
			env.Roots = domain.CopyStrings(plan.Roots)
			env.Resolved = domain.CopyStrings(plan.Resolved)
			env.Revision++
			env.UpdatedAt = at
			plan.State = domain.Applied
			plan.Revision++
			plan.UpdatedAt = at
			state.Environments[env.ID] = env
			state.Plans[plan.ID] = plan
			state.Record("plan", plan.ID, "applied", at)
			entries = append(entries, AppliedChangeSetEntry{Plan: plan, Environment: env})
			envs = append(envs, env)
			// Other live sets that share these plans can no longer apply them.
			invalidateReferencingChangeSets(state, plan.ID, id, "plan "+plan.ID+" was applied by change set "+id, at)
		}
		set.State = domain.Applied
		set.InvalidatedReason = ""
		set.Revision++
		set.UpdatedAt = at
		state.ChangeSets[id] = set
		state.Record("change_set", id, "applied", at)
		result = AppliedChangeSetResult{ChangeSet: set, Applications: entries, Environments: envs}
		return nil
	})
	return result, err
}

// CancelChangeSet cancels the group only. The referenced independent plans are
// left in their current state and stay usable through the single-plan entry.
func (s *Service) CancelChangeSet(ctx context.Context, id string, revision uint64) (domain.ChangeSet, error) {
	var result domain.ChangeSet
	err := s.repo.Update(ctx, func(state *repository.State) error {
		set, exists := state.ChangeSets[id]
		if !exists {
			return domain.Missing("change set", id)
		}
		if err := set.CheckRevision(revision); err != nil {
			return err
		}
		if err := set.CanCancel(); err != nil {
			return err
		}
		at := now()
		set.State = domain.Cancelled
		set.Revision++
		set.UpdatedAt = at
		state.ChangeSets[id] = set
		state.Record("change_set", id, "cancelled", at)
		result = set
		return nil
	})
	return result, err
}
