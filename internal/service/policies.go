package service

import (
	"context"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/policy"
	"solo-0002-version-compatibility/internal/repository"
)

func (s *Service) CreatePolicy(ctx context.Context, input domain.PolicySetInput) (domain.PolicySet, error) {
	if err := domain.ValidatePolicySet(input.ID, input.Name, input.Rules); err != nil {
		return domain.PolicySet{}, err
	}
	at := now()
	set := domain.PolicySet{ID: input.ID, Name: input.Name, Rules: domain.CopyRules(input.Rules), Revision: 1, CreatedAt: at, UpdatedAt: at}
	err := s.repo.Update(ctx, func(state *repository.State) error {
		if _, exists := state.Policies[input.ID]; exists {
			return domain.Conflict("policy %s already exists", input.ID)
		}
		if len(state.Policies) >= domain.MaxPolicySets {
			return domain.Limit("policy capacity reached")
		}
		state.Policies[input.ID] = set
		state.Record("policy", input.ID, "created", at)
		return nil
	})
	return set, err
}

func (s *Service) ListPolicies(ctx context.Context) ([]domain.PolicySet, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]domain.PolicySet, 0, len(state.Policies))
	for _, id := range domain.SortedKeys(state.Policies) {
		items = append(items, state.Policies[id])
	}
	return items, nil
}

func (s *Service) Policy(ctx context.Context, id string) (domain.PolicySet, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return domain.PolicySet{}, err
	}
	set, exists := state.Policies[id]
	if !exists {
		return set, domain.Missing("policy", id)
	}
	return set, nil
}

func (s *Service) UpdatePolicy(ctx context.Context, id string, input domain.PolicySetUpdateInput) (domain.PolicySet, error) {
	if err := domain.ValidateText(input.Name, "name", 1, 120); err != nil {
		return domain.PolicySet{}, err
	}
	if err := domain.ValidatePolicyRules(input.Rules); err != nil {
		return domain.PolicySet{}, err
	}
	if input.Revision == 0 {
		return domain.PolicySet{}, domain.Invalid("revision must be positive")
	}
	var result domain.PolicySet
	err := s.repo.Update(ctx, func(state *repository.State) error {
		set, exists := state.Policies[id]
		if !exists {
			return domain.Missing("policy", id)
		}
		if set.Revision != input.Revision {
			return domain.Conflict("policy revision is %d, received %d", set.Revision, input.Revision)
		}
		set.Name = input.Name
		set.Rules = domain.CopyRules(input.Rules)
		set.Revision++
		set.UpdatedAt = now()
		state.Policies[id] = set
		state.Record("policy", id, "updated", set.UpdatedAt)
		result = set
		return nil
	})
	return result, err
}

// DeletePolicy removes an unbound policy set; validated plans keep their
// historical findings and are unaffected.
func (s *Service) DeletePolicy(ctx context.Context, id string, revision uint64) (domain.PolicySet, error) {
	var removed domain.PolicySet
	if revision == 0 {
		return removed, domain.Invalid("revision must be positive")
	}
	err := s.repo.Update(ctx, func(state *repository.State) error {
		set, exists := state.Policies[id]
		if !exists {
			return domain.Missing("policy", id)
		}
		if set.Revision != revision {
			return domain.Conflict("policy revision is %d, received %d", set.Revision, revision)
		}
		for _, envID := range domain.SortedKeys(state.Environments) {
			if state.Environments[envID].PolicyID == id {
				return domain.Conflict("policy is bound to environment %s", envID)
			}
		}
		delete(state.Policies, id)
		state.Record("policy", id, "deleted", now())
		removed = set
		return nil
	})
	return removed, err
}

// BindPolicy attaches a policy set to an environment, or detaches it when the
// input is empty. The environment revision is left untouched; plans validated
// under a different binding are rejected at apply time instead.
func (s *Service) BindPolicy(ctx context.Context, environmentID string, input domain.BindPolicyInput) (domain.Environment, error) {
	if input.PolicyID != "" {
		if err := domain.ValidateID(input.PolicyID); err != nil {
			return domain.Environment{}, err
		}
	}
	var result domain.Environment
	err := s.repo.Update(ctx, func(state *repository.State) error {
		env, exists := state.Environments[environmentID]
		if !exists {
			return domain.Missing("environment", environmentID)
		}
		action := "policy-unbound"
		if input.PolicyID != "" {
			if _, exists := state.Policies[input.PolicyID]; !exists {
				return domain.Missing("policy", input.PolicyID)
			}
			action = "policy-bound"
		}
		env.PolicyID = input.PolicyID
		env.UpdatedAt = now()
		state.Environments[environmentID] = env
		state.Record("environment", environmentID, action, env.UpdatedAt)
		result = env
		return nil
	})
	return result, err
}

// evaluatePolicy runs the policy set bound to the environment against the
// planned changes. The returned identifiers snapshot the policy state used
// for the evaluation; both are empty when no policy is bound.
func evaluatePolicy(state *repository.State, env domain.Environment, changes []domain.Change) ([]domain.PolicyFinding, string, uint64, error) {
	if env.PolicyID == "" {
		return nil, "", 0, nil
	}
	set, exists := state.Policies[env.PolicyID]
	if !exists {
		return nil, "", 0, domain.Conflict("policy %s bound to environment %s no longer exists", env.PolicyID, env.ID)
	}
	return policy.Evaluate(set, changes), set.ID, set.Revision, nil
}

// checkPolicyBinding detects binding or policy changes since evaluation.
func checkPolicyBinding(state *repository.State, env domain.Environment, policyID string, revision uint64) error {
	if env.PolicyID != policyID {
		return domain.Conflict("policy binding changed during computation; retry against the current binding")
	}
	if policyID == "" {
		return nil
	}
	set, exists := state.Policies[policyID]
	if !exists || set.Revision != revision {
		return domain.Conflict("policy changed during computation; retry against the current policy")
	}
	return nil
}

// checkPolicyCurrent blocks applying a plan whose validation predates the
// current policy binding or policy revision.
func checkPolicyCurrent(state *repository.State, env domain.Environment, plan domain.Plan) error {
	if plan.PolicyID != env.PolicyID {
		return domain.Conflict("policy binding changed after validation; validate the plan again")
	}
	if env.PolicyID == "" {
		return nil
	}
	set, exists := state.Policies[env.PolicyID]
	if !exists {
		return domain.Conflict("policy %s bound to environment %s no longer exists", env.PolicyID, env.ID)
	}
	if set.Revision != plan.PolicyRevision {
		return domain.Conflict("policy %s changed after validation; validate the plan again", env.PolicyID)
	}
	return nil
}
