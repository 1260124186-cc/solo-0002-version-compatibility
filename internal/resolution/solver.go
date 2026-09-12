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
	ctx        context.Context
	catalog    map[string][]candidate
	components map[string]domain.Component
	roots      map[string]semver.Constraint
	steps      int
	maxSteps   int
	maxNodes   int
	conflicts  []string
	visibility []string
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
	work := search{ctx: ctx, catalog: compiled, components: catalog.Components, roots: make(map[string]semver.Constraint), maxSteps: s.MaxSteps, maxNodes: s.MaxNodes}
	for _, id := range domain.SortedKeys(roots) {
		component, exists := catalog.Components[id]
		if !exists {
			return domain.Resolution{}, domain.Missing("component", id)
		}
		constraint, err := semver.ParseConstraint(roots[id])
		if err != nil {
			return domain.Resolution{}, err
		}
		if !domain.CanReference("", domain.Component{}, component) {
			return domain.Resolution{}, domain.VisibilityDenied("root requests cannot depend directly on internal component %s; use its public family entry point", id)
		}
		work.roots[id] = constraint
	}
	selected, err := work.solve(make(map[string]candidate))
	if err != nil {
		return domain.Resolution{}, err
	}
	if selected == nil {
		if len(work.visibility) > 0 {
			return domain.Resolution{}, &domain.Fault{Code: "visibility_denied", Detail: "no selectable release respects the internal dependency boundaries", Conflicts: work.visibility}
		}
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
		if len(choice.blocked) > 0 {
			s.explainVisibility(unresolved, choice.release.Version, choice.blocked)
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

func (s *search) explainVisibility(from, version string, blocked map[string]bool) {
	if len(s.visibility) >= 8 {
		return
	}
	for _, dep := range domain.SortedKeys(blocked) {
		target := s.components[dep]
		item := fmt.Sprintf("%s@%s directly depends on internal %s (family %q) without family membership or an allowed-consumer grant", from, version, dep, target.Family)
		found := false
		for _, existing := range s.visibility {
			if existing == item {
				found = true
				break
			}
		}
		if !found {
			s.visibility = append(s.visibility, item)
		}
		if len(s.visibility) >= 8 {
			return
		}
	}
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
