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

type Request struct {
	Roots     map[string]string
	Overrides map[string]string
}

type search struct {
	ctx       context.Context
	catalog   map[string][]candidate
	roots     map[string]semver.Constraint
	overrides map[string]semver.Constraint
	steps     int
	maxSteps  int
	maxNodes  int
	conflicts []string
}

func (s Solver) Resolve(ctx context.Context, catalog domain.Catalog, roots map[string]string) (domain.Resolution, error) {
	return s.ResolveRequest(ctx, catalog, Request{Roots: roots})
}

func (s Solver) ResolveRequest(ctx context.Context, catalog domain.Catalog, request Request) (domain.Resolution, error) {
	roots := request.Roots
	overrides := request.Overrides
	if err := domain.ValidateRequirements(roots, false); err != nil {
		return domain.Resolution{}, err
	}
	if err := domain.ValidateOverrides(overrides, roots); err != nil {
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
	work := search{
		ctx:       ctx,
		catalog:   compiled,
		roots:     make(map[string]semver.Constraint),
		overrides: make(map[string]semver.Constraint),
		maxSteps:  s.MaxSteps,
		maxNodes:  s.MaxNodes,
	}
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
	for _, id := range domain.SortedKeys(overrides) {
		if _, exists := catalog.Components[id]; !exists {
			return domain.Resolution{}, domain.Missing("overridden component", id)
		}
		version, err := semver.Parse(overrides[id])
		if err != nil {
			return domain.Resolution{}, domain.Invalid("override for %s: %s", id, err)
		}
		work.overrides[id] = semver.Constraint{
			Raw:        version.String(),
			Predicates: []semver.Predicate{{Operator: "=", Version: version}},
		}
	}
	selected, err := work.solve(make(map[string]candidate))
	if err != nil {
		return domain.Resolution{}, err
	}
	if selected == nil {
		return domain.Resolution{}, &domain.Fault{Code: "no_solution", Detail: "no compatible set satisfies the requested constraints and overrides", Conflicts: work.conflicts}
	}
	for _, id := range domain.SortedKeys(overrides) {
		if chosen, exists := selected[id]; !exists {
			if len(work.conflicts) < 8 {
				work.conflicts = append(work.conflicts, fmt.Sprintf("environment override requires %s %s, but no dependency path reaches that component", id, work.overrides[id].Raw))
			}
			return domain.Resolution{}, &domain.Fault{Code: "no_solution", Detail: "no compatible set satisfies the requested constraints and overrides", Conflicts: work.conflicts}
		} else if chosen.version.Compare(work.overrides[id].Predicates[0].Version) != 0 {
			if len(work.conflicts) < 8 {
				work.conflicts = append(work.conflicts, fmt.Sprintf("environment override requires %s %s", id, work.overrides[id].Raw))
			}
			return domain.Resolution{}, &domain.Fault{Code: "no_solution", Detail: "no compatible set satisfies the requested constraints and overrides", Conflicts: work.conflicts}
		}
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
	for _, id := range domain.SortedKeys(s.overrides) {
		if _, required := needs[id]; required {
			needs[id] = append(needs[id], requirement{from: "environment override", constraint: s.overrides[id], override: true})
		}
	}
	return needs
}

func (s *search) explain(id string, needs []requirement) {
	if len(s.conflicts) >= 8 {
		return
	}
	override := findOverride(needs)
	if override != nil {
		overrideVersion := override.constraint.Predicates[0].Version
		reported := false
		for _, need := range needs {
			if need.override || need.constraint.Matches(overrideVersion) {
				continue
			}
			s.addConflict(fmt.Sprintf("%s requires %s %s, but environment override requires %s %s", need.from, id, need.constraint.Raw, id, override.constraint.Raw))
			reported = true
			if len(s.conflicts) >= 8 {
				return
			}
		}
		if !reported {
			s.addConflict(fmt.Sprintf("environment override requires %s %s, but that release is not available", id, override.constraint.Raw))
		}
		return
	}
	for _, need := range needs {
		s.addConflict(fmt.Sprintf("%s requires %s %s", need.from, id, need.constraint.Raw))
		if len(s.conflicts) >= 8 {
			return
		}
	}
}

func findOverride(needs []requirement) *requirement {
	for i := range needs {
		if needs[i].override {
			return &needs[i]
		}
	}
	return nil
}

func (s *search) addConflict(item string) {
	for _, existing := range s.conflicts {
		if existing == item {
			return
		}
	}
	s.conflicts = append(s.conflicts, item)
}
