package resolution

import (
	"fmt"
	"sort"
	"strings"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/semver"
)

// releaseSource names the constraint origin in the same format the solver uses
// for conflict evidence: "root" or "<component>@<version>".
const rootSource = "root"

func releaseSource(id, version string) string { return id + "@" + version }

// buildProof derives the auditable selection chain from the final chosen set
// and the decision order in which releases were committed. A constraint from a
// release chosen earlier (or from the roots) introduced the component; a
// constraint from a release chosen later could not influence the pick but is
// still satisfied by it, and is therefore listed as verified_against.
func buildProof(revision uint64, roots map[string]string, selected map[string]candidate, order []string) *domain.Proof {
	position := make(map[string]int, len(order))
	for i, id := range order {
		position[id] = i
	}
	proof := &domain.Proof{
		CatalogRevision: revision,
		Roots:           domain.CopyStrings(roots),
		Selection:       make([]domain.SelectionStep, 0, len(order)),
		Cycles:          make([]domain.DependencyCycle, 0),
	}
	for _, id := range order {
		chosen := selected[id]
		introduced := make([]domain.ConstraintOrigin, 0)
		verified := make([]domain.ConstraintOrigin, 0)
		if raw, isRoot := roots[id]; isRoot {
			introduced = append(introduced, domain.ConstraintOrigin{Source: rootSource, Constraint: raw})
		}
		for _, parent := range domain.SortedKeys(selected) {
			if constraint, ok := selected[parent].dependencies[id]; ok {
				origin := domain.ConstraintOrigin{Source: releaseSource(parent, selected[parent].release.Version), Constraint: constraint.Raw}
				if position[parent] < position[id] {
					introduced = append(introduced, origin)
				} else {
					verified = append(verified, origin)
				}
			}
		}
		sortOrigins(introduced)
		sortOrigins(verified)
		proof.Selection = append(proof.Selection, domain.SelectionStep{
			Component:       id,
			Version:         chosen.release.Version,
			Order:           position[id],
			IntroducedBy:    introduced,
			VerifiedAgainst: verified,
		})
	}
	graph := make(map[string]map[string]string, len(selected))
	for id := range selected {
		graph[id] = make(map[string]string)
	}
	for id, chosen := range selected {
		for dep, constraint := range chosen.dependencies {
			if _, ok := selected[dep]; ok {
				graph[id][dep] = constraint.Raw
			}
		}
	}
	proof.Cycles = detectCycles(graph)
	return proof
}

func sortOrigins(origins []domain.ConstraintOrigin) {
	sort.Slice(origins, func(i, j int) bool {
		if origins[i].Source != origins[j].Source {
			return origins[i].Source < origins[j].Source
		}
		return origins[i].Constraint < origins[j].Constraint
	})
}

// maxReportedCycles and maxCycleSearchSteps bound the closed cycle
// representation: enumerating every elementary circuit is exponential in the
// worst case, so the deterministic walk stops at either limit. The very same
// function runs while building and while verifying a proof, so identical
// graphs always produce an identical (possibly truncated) closure and a stored
// proof remains exactly comparable.
const (
	maxReportedCycles   = 64
	maxCycleSearchSteps = 20000
)

// detectCycles enumerates elementary dependency cycles in the chosen graph.
// Each elementary circuit is rooted exactly once at its smallest node: a walk
// may only pass nodes greater than the root and never repeats a node. A cycle
// is stored as its node list plus the closing edge back to the first node, so
// the representation is finite instead of expanding the loop. Results are
// deterministically ordered and bounded.
func detectCycles(graph map[string]map[string]string) []domain.DependencyCycle {
	nodes := make([]string, 0, len(graph))
	for id := range graph {
		nodes = append(nodes, id)
	}
	sort.Strings(nodes)

	cycles := make([]domain.DependencyCycle, 0)
	steps := 0
	exhausted := false
	for _, root := range nodes {
		if len(cycles) >= maxReportedCycles || exhausted {
			break
		}
		visited := map[string]bool{root: true}
		var walk func(string, []string)
		walk = func(id string, path []string) {
			if exhausted || len(cycles) >= maxReportedCycles {
				return
			}
			neighbors := make([]string, 0, len(graph[id]))
			for next := range graph[id] {
				neighbors = append(neighbors, next)
			}
			sort.Strings(neighbors)
			for _, next := range neighbors {
				steps++
				if steps > maxCycleSearchSteps {
					exhausted = true
					return
				}
				if len(cycles) >= maxReportedCycles {
					return
				}
				switch {
				case next == root:
					ring := append([]string(nil), path...)
					edges := make([]domain.Edge, 0, len(ring))
					for i := range ring {
						from, to := ring[i], ring[(i+1)%len(ring)]
						edges = append(edges, domain.Edge{From: from, To: to, Constraint: graph[from][to]})
					}
					cycles = append(cycles, domain.DependencyCycle{Nodes: ring, Edges: edges})
				case next > root && !visited[next]:
					visited[next] = true
					walk(next, append(path, next))
					visited[next] = false
				}
				if exhausted {
					return
				}
			}
		}
		walk(root, []string{root})
	}
	sort.Slice(cycles, func(i, j int) bool {
		return canonicalCycle(cycles[i].Nodes) < canonicalCycle(cycles[j].Nodes)
	})
	return cycles
}

func canonicalCycle(nodes []string) string {
	if len(nodes) == 0 {
		return ""
	}
	smallest := 0
	for i := 1; i < len(nodes); i++ {
		if nodes[i] < nodes[smallest] {
			smallest = i
		}
	}
	rotated := make([]string, 0, len(nodes))
	rotated = append(rotated, nodes[smallest:]...)
	rotated = append(rotated, nodes[:smallest]...)
	return strings.Join(rotated, "|")
}

// VerifyProof re-derives every claim of a stored proof from the catalog.
//
// In strict mode the proof must be bound to the catalog revision in force and
// every selected release must still be available; this guards fresh decision
// points (environment creation, plan validation and application). In
// structural mode the revision only has to be one the catalog has moved past
// and withdrawn releases are tolerated, so historical proofs on applied plans
// remain auditable, e.g. during startup integrity validation. Either way a
// structurally inconsistent proof is rejected rather than trusted.
func VerifyProof(catalog domain.Catalog, roots, resolved map[string]string, proof *domain.Proof, strict bool) error {
	if proof == nil {
		return domain.InvalidProof("resolution proof is missing")
	}
	if strict {
		if proof.CatalogRevision != catalog.Revision {
			return domain.StaleProof("proof is bound to catalog revision %d, current revision is %d", proof.CatalogRevision, catalog.Revision)
		}
	} else if proof.CatalogRevision > catalog.Revision {
		return domain.InvalidProof("proof references future catalog revision %d (current %d)", proof.CatalogRevision, catalog.Revision)
	}
	if !sameStringMap(proof.Roots, roots) {
		return domain.InvalidProof("proof roots do not match the requested requirements")
	}
	if len(proof.Selection) != len(resolved) {
		return domain.InvalidProof("proof covers %d selections but the set contains %d", len(proof.Selection), len(resolved))
	}

	position := make(map[string]int, len(resolved))
	chosen := make(map[string]domain.Release, len(resolved))
	for i, step := range proof.Selection {
		if step.Order != i {
			return domain.InvalidProof("proof selection order is not a contiguous chain starting at 0")
		}
		if _, duplicated := position[step.Component]; duplicated {
			return domain.InvalidProof("proof lists %s more than once", step.Component)
		}
		version, ok := resolved[step.Component]
		if !ok {
			return domain.InvalidProof("proof selects %s which is not part of the set", step.Component)
		}
		if version != step.Version {
			return domain.InvalidProof("proof version %s for %s does not match selected %s", step.Version, step.Component, version)
		}
		release, exists := catalog.Releases[step.Component][version]
		if !exists {
			return domain.InvalidProof("proof selects %s@%s which no longer exists in the catalog", step.Component, version)
		}
		if strict && release.State != domain.Available {
			return domain.StaleProof("proof selects withdrawn release %s@%s", step.Component, version)
		}
		if _, err := semver.Parse(version); err != nil {
			return domain.InvalidProof("proof carries invalid version %s: %s", version, err)
		}
		position[step.Component] = i
		chosen[step.Component] = release
	}
	for id := range resolved {
		if _, covered := position[id]; !covered {
			return domain.InvalidProof("selection of %s has no proof step", id)
		}
	}

	// Re-derive the introduced/verified origin split from the decision order
	// and the catalog releases, then check every claimed constraint matches.
	for id := range resolved {
		pos := position[id]
		wantIntroduced := make(map[string]bool)
		wantVerified := make(map[string]bool)
		if raw, isRoot := roots[id]; isRoot {
			wantIntroduced[rootSource+"\x00"+raw] = true
		}
		for parent := range resolved {
			if raw, dep := chosen[parent].Requires[id]; dep {
				key := releaseSource(parent, resolved[parent]) + "\x00" + raw
				if position[parent] < pos {
					wantIntroduced[key] = true
				} else {
					wantVerified[key] = true
				}
			}
		}
		var step domain.SelectionStep
		for _, candidate := range proof.Selection {
			if candidate.Component == id {
				step = candidate
				break
			}
		}
		if err := checkOrigins(id, step.Version, step.IntroducedBy, wantIntroduced, "introduced_by"); err != nil {
			return err
		}
		if err := checkOrigins(id, step.Version, step.VerifiedAgainst, wantVerified, "verified_against"); err != nil {
			return err
		}
	}

	// Every dependency edge of the chosen set must exist in the set and hold.
	for id, release := range chosen {
		for dep, raw := range release.Requires {
			depVersion, ok := resolved[dep]
			if !ok {
				return domain.InvalidProof("selected %s@%s requires %s which is absent from the set", id, resolved[id], dep)
			}
			constraint, err := semver.ParseConstraint(raw)
			if err != nil {
				return domain.InvalidProof("catalog constraint of %s@%s on %s is invalid: %s", id, resolved[id], dep, err)
			}
			parsed, err := semver.Parse(depVersion)
			if err != nil {
				return domain.InvalidProof("selected version %s for %s is invalid: %s", depVersion, dep, err)
			}
			if !constraint.Matches(parsed) {
				return domain.InvalidProof("selected %s@%s violates %s %s required by %s@%s", dep, depVersion, dep, raw, id, resolved[id])
			}
		}
	}

	// No extraneous component: every selection must be reachable from a root.
	reachable := make(map[string]bool)
	var visit func(string)
	visit = func(id string) {
		if reachable[id] {
			return
		}
		reachable[id] = true
		for dep := range chosen[id].Requires {
			if _, ok := resolved[dep]; ok {
				visit(dep)
			}
		}
	}
	for id := range roots {
		if _, ok := resolved[id]; !ok {
			return domain.InvalidProof("root component %s is missing from the selection", id)
		}
		visit(id)
	}
	if len(reachable) != len(resolved) {
		return domain.InvalidProof("proof set contains components unreachable from the roots")
	}

	// The cycle closure must be exactly what the chosen graph produces.
	graph := make(map[string]map[string]string, len(resolved))
	for id, release := range chosen {
		graph[id] = make(map[string]string)
		for dep, raw := range release.Requires {
			if _, ok := resolved[dep]; ok {
				graph[id][dep] = raw
			}
		}
	}
	if !sameCycles(detectCycles(graph), proof.Cycles) {
		return domain.InvalidProof("proof cycle closure does not match the dependency graph")
	}
	return nil
}

func checkOrigins(id, version string, got []domain.ConstraintOrigin, want map[string]bool, field string) error {
	seen := make(map[string]bool, len(got))
	for _, origin := range got {
		key := origin.Source + "\x00" + origin.Constraint
		if seen[key] {
			return domain.InvalidProof("duplicate %s origin %s for %s", field, origin.Source, id)
		}
		seen[key] = true
		if !want[key] {
			return domain.InvalidProof("proof %s for %s cites an unexpected origin %s %s", field, id, origin.Source, origin.Constraint)
		}
		constraint, err := semver.ParseConstraint(origin.Constraint)
		if err != nil {
			return domain.InvalidProof("proof %s for %s carries invalid constraint %s: %s", field, id, origin.Constraint, err)
		}
		parsed, err := semver.Parse(version)
		if err != nil {
			return domain.InvalidProof("proof carries invalid version %s for %s", version, id)
		}
		if !constraint.Matches(parsed) {
			return domain.InvalidProof("selected %s@%s does not satisfy %s constraint %s from %s", id, version, id, origin.Constraint, origin.Source)
		}
	}
	if len(seen) != len(want) {
		return domain.InvalidProof("proof %s for %s omits a constraint origin", field, id)
	}
	return nil
}

func sameStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			return false
		}
	}
	return true
}

func sameCycles(a, b []domain.DependencyCycle) bool {
	if len(a) != len(b) {
		return false
	}
	keys := func(cycles []domain.DependencyCycle) map[string]bool {
		out := make(map[string]bool, len(cycles))
		for _, cycle := range cycles {
			out[fmt.Sprintf("%s|edges=%v", canonicalCycle(cycle.Nodes), cycle.Edges)] = true
		}
		return out
	}
	left, right := keys(a), keys(b)
	if len(left) != len(right) {
		return false
	}
	for key := range left {
		if !right[key] {
			return false
		}
	}
	return true
}
