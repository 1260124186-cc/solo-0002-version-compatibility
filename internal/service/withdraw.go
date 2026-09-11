package service

import (
	"context"
	"sort"
	"time"

	"solo-0002-version-compatibility/internal/domain"
)

// ImpactNode is one hop on a dependency path from a root component down to the
// inspected release. Constraint is the requirement the predecessor imposed;
// root nodes carry an empty constraint.
type ImpactNode struct {
	ComponentID string `json:"component_id"`
	Version     string `json:"version"`
	Constraint  string `json:"constraint,omitempty"`
}

// ImpactEnvironment describes one environment whose resolved set uses the
// inspected release, together with the root dependencies that reach it.
type ImpactEnvironment struct {
	EnvironmentID string            `json:"environment_id"`
	Revision      uint64            `json:"environment_revision"`
	Roots         map[string]string `json:"roots"`
	Paths         [][]ImpactNode    `json:"paths"`
}

// ImpactReadyPlan describes a ready (not yet applied) plan whose resolution
// contains the inspected release. Applying such a plan is already impossible
// after the catalog changes; it must be revalidated first.
type ImpactReadyPlan struct {
	PlanID          string            `json:"plan_id"`
	EnvironmentID   string            `json:"environment_id"`
	Revision        uint64            `json:"plan_revision"`
	CatalogRevision uint64            `json:"catalog_revision"`
	Roots           map[string]string `json:"roots"`
	Paths           [][]ImpactNode    `json:"paths"`
}

// WithdrawImpact is the read-only analysis returned before a withdrawal. It is
// bound to CatalogRevision: once the catalog revision changes the report is
// stale and must be regenerated.
type WithdrawImpact struct {
	ComponentID     string              `json:"component_id"`
	Version         string              `json:"version"`
	ReleaseState    string              `json:"release_state"`
	CatalogRevision uint64              `json:"catalog_revision"`
	GeneratedAt     time.Time           `json:"generated_at"`
	Withdrawable    bool                `json:"withdrawable"`
	Environments    []ImpactEnvironment `json:"environments"`
	ReadyPlans      []ImpactReadyPlan   `json:"ready_plans"`
}

// WithdrawPrecheck performs impact analysis only. It never mutates the catalog,
// environments, plans or the event stream.
func (s *Service) WithdrawPrecheck(ctx context.Context, componentID, version string) (WithdrawImpact, error) {
	state, err := s.repo.Snapshot(ctx)
	if err != nil {
		return WithdrawImpact{}, err
	}
	release, exists := state.Catalog.Releases[componentID][version]
	if !exists {
		return WithdrawImpact{}, domain.Missing("release", componentID+"@"+version)
	}
	report := WithdrawImpact{
		ComponentID:     componentID,
		Version:         version,
		ReleaseState:    release.State,
		CatalogRevision: state.Catalog.Revision,
		GeneratedAt:     now(),
		Environments:    make([]ImpactEnvironment, 0),
		ReadyPlans:      make([]ImpactReadyPlan, 0),
	}
	for _, envID := range domain.SortedKeys(state.Environments) {
		env := state.Environments[envID]
		if env.Resolved[componentID] != version {
			continue
		}
		paths := impactPaths(state.Catalog, env.Resolved, env.Roots, componentID)
		report.Environments = append(report.Environments, ImpactEnvironment{
			EnvironmentID: envID,
			Revision:      env.Revision,
			Roots:         impactRoots(env.Roots, paths),
			Paths:         paths,
		})
	}
	planIDs := make([]string, 0, len(state.Plans))
	for id := range state.Plans {
		planIDs = append(planIDs, id)
	}
	sort.Slice(planIDs, func(i, j int) bool {
		a, b := state.Plans[planIDs[i]], state.Plans[planIDs[j]]
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	for _, id := range planIDs {
		plan := state.Plans[id]
		if plan.State != domain.Ready || plan.Resolved[componentID] != version {
			continue
		}
		paths := impactPaths(state.Catalog, plan.Resolved, plan.Roots, componentID)
		report.ReadyPlans = append(report.ReadyPlans, ImpactReadyPlan{
			PlanID:          plan.ID,
			EnvironmentID:   plan.EnvironmentID,
			Revision:        plan.Revision,
			CatalogRevision: plan.CatalogRevision,
			Roots:           impactRoots(plan.Roots, paths),
			Paths:           paths,
		})
	}
	// Current withdrawal rule: only environments using the release block it;
	// ready plans are invalidated by the catalog revision bump instead.
	report.Withdrawable = release.State == domain.Available && len(report.Environments) == 0
	return report, nil
}

type impactHop struct {
	from       string
	constraint string
}

// impactRoots keeps only root dependencies from which the target component is
// reachable, according to the computed paths. Roots unrelated to the target
// must never appear in the precheck report.
func impactRoots(allRoots map[string]string, paths [][]ImpactNode) map[string]string {
	roots := make(map[string]string, len(paths))
	for _, path := range paths {
		if len(path) == 0 {
			continue
		}
		root := path[0].ComponentID
		if constraint, exists := allRoots[root]; exists {
			roots[root] = constraint
		}
	}
	return roots
}

// impactPaths returns one shortest dependency path per root from which the
// target component is reachable inside the resolved set. Traversal is bounded
// by the resolved map and lexicographically ordered, so dependency cycles
// terminate and equal-length ties are deterministic.
func impactPaths(catalog domain.Catalog, resolved, roots map[string]string, target string) [][]ImpactNode {
	adjacency := make(map[string][]impactHop, len(resolved))
	for _, componentID := range domain.SortedKeys(resolved) {
		release, exists := catalog.Releases[componentID][resolved[componentID]]
		if !exists {
			continue
		}
		for _, dep := range domain.SortedKeys(release.Requires) {
			if _, inResolved := resolved[dep]; !inResolved {
				continue
			}
			adjacency[componentID] = append(adjacency[componentID], impactHop{from: dep, constraint: release.Requires[dep]})
		}
	}
	paths := make([][]ImpactNode, 0)
	for _, root := range domain.SortedKeys(roots) {
		if _, exists := resolved[root]; !exists {
			continue
		}
		previous := map[string]impactHop{root: {}}
		found := root == target
		queue := []string{root}
		for len(queue) > 0 && !found {
			current := queue[0]
			queue = queue[1:]
			for _, hop := range adjacency[current] {
				if _, visited := previous[hop.from]; visited {
					continue
				}
				previous[hop.from] = impactHop{from: current, constraint: hop.constraint}
				if hop.from == target {
					found = true
					break
				}
				queue = append(queue, hop.from)
			}
		}
		if !found {
			continue
		}
		chain := []string{target}
		for chain[len(chain)-1] != root {
			chain = append(chain, previous[chain[len(chain)-1]].from)
		}
		path := make([]ImpactNode, 0, len(chain))
		for i := len(chain) - 1; i >= 0; i-- {
			componentID := chain[i]
			node := ImpactNode{ComponentID: componentID, Version: resolved[componentID]}
			if componentID != root {
				node.Constraint = previous[componentID].constraint
			}
			path = append(path, node)
		}
		paths = append(paths, path)
	}
	return paths
}
