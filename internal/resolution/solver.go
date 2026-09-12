package resolution

import (
	"context"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/semver"
)

type Solver struct {
	MaxSteps int
	MaxNodes int
}

type search struct {
	ctx      context.Context
	catalog  map[string][]candidate
	roots    map[string]semver.Constraint
	steps    int
	maxSteps int
	maxNodes int
}

func (s Solver) Resolve(ctx context.Context, catalog domain.Catalog, roots map[string]string) (domain.Resolution, error) {
	if err := domain.ValidateRequirements(roots, false); err != nil {
		return domain.Resolution{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Resolution{}, err
	}
	compiled, err := compile(ctx, catalog)
	if err != nil {
		return domain.Resolution{}, err
	}
	if s.MaxSteps <= 0 {
		s.MaxSteps = 50000
	}
	if s.MaxNodes <= 0 {
		s.MaxNodes = 128
	}
	work := search{ctx: ctx, catalog: compiled, roots: make(map[string]semver.Constraint), maxSteps: s.MaxSteps, maxNodes: s.MaxNodes}
	for _, id := range domain.SortedKeys(roots) {
		if _, exists := catalog.Components[id]; !exists {
			return domain.Resolution{}, domain.Missing("component", id)
		}
		constraint, err := semver.ParseConstraint(roots[id])
		if err != nil {
			return domain.Resolution{}, err
		}
		work.roots[id] = constraint
	}
	selected, failure, err := work.solve(make(map[string]candidate))
	if err != nil {
		return domain.Resolution{}, err
	}
	if selected == nil {
		if failure == nil {
			failure = newConflictSet()
		}
		legacy, report := render(failure.minimize(compiled))
		return domain.Resolution{}, &domain.Fault{Code: "no_solution", Detail: "no compatible set satisfies the requested constraints", Conflicts: legacy, Conflict: report}
	}
	result := domain.Resolution{CatalogRevision: catalog.Revision, Resolved: make(map[string]string), Edges: make([]domain.Edge, 0), Steps: work.steps}
	for _, id := range domain.SortedKeys(selected) {
		chosen := selected[id]
		result.Resolved[id] = chosen.release.Version
		for _, dep := range domain.SortedKeys(chosen.dependencies) {
			result.Edges = append(result.Edges, domain.Edge{From: id, To: dep, Constraint: chosen.dependencies[dep].Raw})
		}
	}
	return result, nil
}

// solve searches for a compatible selection. When it fails it also returns
// the set of requirements responsible for the failure: a set that no
// assignment extending the current one can satisfy, so the top-level failure
// describes an actual contradiction rather than every constraint seen.
func (s *search) solve(selected map[string]candidate) (map[string]candidate, *conflictSet, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, nil, err
	}
	s.steps++
	if s.steps > s.maxSteps {
		return nil, nil, domain.Limit("dependency search exhausted its step budget")
	}
	needs := s.requirements(selected)
	if len(needs) > s.maxNodes {
		return nil, nil, domain.Limit("dependency graph exceeds component limit")
	}
	unresolved := ""
	for _, id := range domain.SortedKeys(needs) {
		if chosen, ok := selected[id]; ok {
			if !matchesAll(chosen.version, needs[id]) {
				return nil, newConflictSet().addAll(needs[id]), nil
			}
		} else if unresolved == "" {
			unresolved = id
		}
	}
	if unresolved == "" {
		return selected, nil, nil
	}
	var failure *conflictSet
	for _, choice := range s.catalog[unresolved] {
		if !matchesAll(choice.version, needs[unresolved]) {
			continue
		}
		next := make(map[string]candidate, len(selected)+1)
		for id, value := range selected {
			next[id] = value
		}
		next[unresolved] = choice
		result, sub, err := s.solve(next)
		if err != nil {
			return nil, nil, err
		}
		if result != nil {
			return result, nil, nil
		}
		if failure == nil {
			failure = newConflictSet().addAll(needs[unresolved])
		}
		failure.merge(sub)
	}
	if failure == nil {
		failure = newConflictSet().addAll(needs[unresolved])
	}
	return nil, failure, nil
}

func (s *search) requirements(selected map[string]candidate) map[string][]requirement {
	needs := make(map[string][]requirement)
	for _, id := range domain.SortedKeys(s.roots) {
		needs[id] = append(needs[id], requirement{component: id, from: "root", constraint: s.roots[id]})
	}
	for _, id := range domain.SortedKeys(selected) {
		chosen := selected[id]
		for _, dep := range domain.SortedKeys(chosen.dependencies) {
			needs[dep] = append(needs[dep], requirement{component: dep, from: id, version: chosen.release.Version, constraint: chosen.dependencies[dep]})
		}
	}
	return needs
}
