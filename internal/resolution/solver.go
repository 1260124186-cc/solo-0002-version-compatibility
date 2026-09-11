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

func (s Solver) limits() (int, int) {
	maxSteps, maxNodes := s.MaxSteps, s.MaxNodes
	if maxSteps <= 0 {
		maxSteps = 50000
	}
	if maxNodes <= 0 {
		maxNodes = 128
	}
	return maxSteps, maxNodes
}

// Session solves multiple root sets against one compiled catalog view while
// sharing a single step budget across all solves.
type Session struct {
	catalog  map[string][]candidate
	revision uint64
	maxSteps int
	maxNodes int
	steps    int
}

func (s Solver) NewSession(ctx context.Context, catalog domain.Catalog) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	compiled, err := compile(ctx, catalog)
	if err != nil {
		return nil, err
	}
	maxSteps, maxNodes := s.limits()
	return &Session{catalog: compiled, revision: catalog.Revision, maxSteps: maxSteps, maxNodes: maxNodes}, nil
}

// Steps reports the total search steps consumed by the session so far.
func (s *Session) Steps() int { return s.steps }

func (s *Session) Solve(ctx context.Context, roots map[string]string) (domain.Resolution, error) {
	if err := domain.ValidateRequirements(roots, false); err != nil {
		return domain.Resolution{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Resolution{}, err
	}
	work := search{ctx: ctx, catalog: s.catalog, roots: make(map[string]semver.Constraint), maxSteps: s.maxSteps - s.steps, maxNodes: s.maxNodes}
	for _, id := range domain.SortedKeys(roots) {
		if _, exists := s.catalog[id]; !exists {
			return domain.Resolution{}, domain.Missing("component", id)
		}
		constraint, err := semver.ParseConstraint(roots[id])
		if err != nil {
			return domain.Resolution{}, err
		}
		work.roots[id] = constraint
	}
	selected, err := work.solve(make(map[string]candidate))
	s.steps += work.steps
	if err != nil {
		return domain.Resolution{}, err
	}
	if selected == nil {
		return domain.Resolution{}, &domain.Fault{Code: "no_solution", Detail: "no compatible set satisfies the requested constraints", Conflicts: work.conflicts}
	}
	result := domain.Resolution{CatalogRevision: s.revision, Resolved: make(map[string]string), Edges: make([]domain.Edge, 0), Steps: work.steps}
	for _, id := range domain.SortedKeys(selected) {
		chosen := selected[id]
		result.Resolved[id] = chosen.release.Version
		for _, dep := range domain.SortedKeys(chosen.dependencies) {
			result.Edges = append(result.Edges, domain.Edge{From: id, To: dep, Constraint: chosen.dependencies[dep].Raw})
		}
	}
	return result, nil
}

func (s Solver) Resolve(ctx context.Context, catalog domain.Catalog, roots map[string]string) (domain.Resolution, error) {
	session, err := s.NewSession(ctx, catalog)
	if err != nil {
		return domain.Resolution{}, err
	}
	return session.Solve(ctx, roots)
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
