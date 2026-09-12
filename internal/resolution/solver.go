package resolution

import (
	"context"
	"fmt"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/semver"
)

type Solver struct {
	MaxSteps int
	MaxNodes int
}

type search struct {
	ctx       context.Context
	catalog   map[string][]candidate
	roots     map[string]semver.Constraint
	steps     int
	maxSteps  int
	maxNodes  int
	conflicts []string
}

func (s Solver) Resolve(ctx context.Context, catalog domain.Catalog, roots map[string]string) (domain.Resolution, error) {
	return s.resolve(ctx, catalog, roots, nil)
}

// ResolvePreferring tries the installed version of each component first
// whenever it still satisfies every constraint, and falls back to other
// versions when keeping it leads to a dependency conflict. The selection
// order stays deterministic; it is a preference, not a search for the
// globally smallest set of changes.
func (s Solver) ResolvePreferring(ctx context.Context, catalog domain.Catalog, roots, installed map[string]string) (domain.Resolution, error) {
	return s.resolve(ctx, catalog, roots, installed)
}

func (s Solver) resolve(ctx context.Context, catalog domain.Catalog, roots, installed map[string]string) (domain.Resolution, error) {
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
	preferInstalled(compiled, installed)
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
	selected, err := work.solve(make(map[string]candidate))
	if err != nil {
		return domain.Resolution{}, err
	}
	if selected == nil {
		return domain.Resolution{}, &domain.Fault{Code: "no_solution", Detail: "no compatible set satisfies the requested constraints", Conflicts: work.conflicts}
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

func (s *search) solve(selected map[string]candidate) (map[string]candidate, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	s.steps++
	if s.steps > s.maxSteps {
		return nil, domain.Limit("dependency search exhausted its step budget")
	}
	needs := s.requirements(selected)
	if len(needs) > s.maxNodes {
		return nil, domain.Limit("dependency graph exceeds component limit")
	}
	unresolved := ""
	for _, id := range domain.SortedKeys(needs) {
		if chosen, ok := selected[id]; ok {
			if !matchesAll(chosen.version, needs[id]) {
				s.explain(id, needs[id])
				return nil, nil
			}
		} else if unresolved == "" {
			unresolved = id
		}
	}
	if unresolved == "" {
		return selected, nil
	}
	for _, choice := range s.catalog[unresolved] {
		if !matchesAll(choice.version, needs[unresolved]) {
			continue
		}
		next := make(map[string]candidate, len(selected)+1)
		for id, value := range selected {
			next[id] = value
		}
		next[unresolved] = choice
		result, err := s.solve(next)
		if err != nil {
			return nil, err
		}
		if result != nil {
			return result, nil
		}
	}
	s.explain(unresolved, needs[unresolved])
	return nil, nil
}

func (s *search) requirements(selected map[string]candidate) map[string][]requirement {
	needs := make(map[string][]requirement)
	for _, id := range domain.SortedKeys(s.roots) {
		needs[id] = append(needs[id], requirement{from: "root", constraint: s.roots[id]})
	}
	for _, id := range domain.SortedKeys(selected) {
		chosen := selected[id]
		for _, dep := range domain.SortedKeys(chosen.dependencies) {
			needs[dep] = append(needs[dep], requirement{from: id + "@" + chosen.release.Version, constraint: chosen.dependencies[dep]})
		}
	}
	return needs
}

func (s *search) explain(id string, needs []requirement) {
	if len(s.conflicts) >= 8 {
		return
	}
	for _, need := range needs {
		item := fmt.Sprintf("%s requires %s %s", need.from, id, need.constraint.Raw)
		found := false
		for _, existing := range s.conflicts {
			if existing == item {
				found = true
				break
			}
		}
		if !found {
			s.conflicts = append(s.conflicts, item)
		}
		if len(s.conflicts) >= 8 {
			return
		}
	}
}
