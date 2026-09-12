package resolution

import (
	"fmt"
	"sort"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/semver"
)

// maxCollectedCauses bounds the raw conflict gathered during search; the
// minimized report is further limited to maxEvidence entries.
const maxCollectedCauses = 64

// maxEvidence bounds the number of causes reported to callers.
const maxEvidence = 8

// conflictSet accumulates the requirements responsible for a failed search
// branch. Every requirement names its target component and its source (a
// root requirement or a specific parent release), so a reported set pinpoints
// which conditions contradict each other.
type conflictSet struct {
	causes []requirement
	seen   map[string]bool
}

func newConflictSet() *conflictSet {
	return &conflictSet{seen: make(map[string]bool)}
}

func requirementKey(need requirement) string {
	return need.from + "\x00" + need.version + "\x00" + need.component + "\x00" + need.constraint.Raw
}

func (c *conflictSet) addAll(needs []requirement) *conflictSet {
	for _, need := range needs {
		if len(c.causes) >= maxCollectedCauses {
			return c
		}
		key := requirementKey(need)
		if !c.seen[key] {
			c.seen[key] = true
			c.causes = append(c.causes, need)
		}
	}
	return c
}

func (c *conflictSet) merge(other *conflictSet) {
	if other != nil {
		c.addAll(other.causes)
	}
}

// minimize reduces the collected causes to a smaller set that is still
// unsatisfiable on its own: removing any remaining cause makes the set
// satisfiable again. Parent-release conditions are considered for removal
// before root conditions so root requirements stay visible when possible.
// If the collected set is not jointly unsatisfiable (only possible when
// collection hit maxCollectedCauses), it is returned as gathered.
func (c *conflictSet) minimize(catalog map[string][]candidate) []requirement {
	if len(c.causes) == 0 || !unsatisfiable(c.causes, catalog) {
		return c.causes
	}
	kept := make([]requirement, len(c.causes))
	copy(kept, c.causes)
	for _, candidate := range deletionOrder(kept) {
		if len(kept) <= 1 {
			break
		}
		key := requirementKey(candidate)
		rest := make([]requirement, 0, len(kept)-1)
		for _, current := range kept {
			if requirementKey(current) != key {
				rest = append(rest, current)
			}
		}
		if len(rest) < len(kept) && unsatisfiable(rest, catalog) {
			kept = rest
		}
	}
	return kept
}

func deletionOrder(causes []requirement) []requirement {
	ordered := make([]requirement, len(causes))
	copy(ordered, causes)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if (a.from == "root") != (b.from == "root") {
			return a.from != "root"
		}
		return requirementKey(a) < requirementKey(b)
	})
	return ordered
}

// unsatisfiable reports whether the causes cannot hold simultaneously: every
// cause demands its target component to match the given constraint, and every
// parent-release source additionally pins that parent to the named version.
// A set is unsatisfiable when some component has no available catalog version
// matching all conditions placed on it.
func unsatisfiable(causes []requirement, catalog map[string][]candidate) bool {
	needs := make(map[string][]semver.Constraint)
	for _, cause := range causes {
		needs[cause.component] = append(needs[cause.component], cause.constraint)
		if cause.from != "root" {
			if pin, err := semver.Parse(cause.version); err == nil {
				needs[cause.from] = append(needs[cause.from], exact(pin))
			}
		}
	}
	for id, constraints := range needs {
		if !anyMatches(catalog[id], constraints) {
			return true
		}
	}
	return false
}

func exact(version semver.Version) semver.Constraint {
	return semver.Constraint{Alternatives: [][]semver.Predicate{{{Operator: "=", Version: version}}}}
}

func anyMatches(candidates []candidate, constraints []semver.Constraint) bool {
	for _, cand := range candidates {
		ok := true
		for _, constraint := range constraints {
			if !constraint.Matches(cand.version) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// render converts the minimized causes into the legacy flat evidence strings
// and the structured conflict report, both bounded to maxEvidence entries.
func render(causes []requirement) ([]string, *domain.ConflictReport) {
	ordered := make([]requirement, len(causes))
	copy(ordered, causes)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.component != b.component {
			return a.component < b.component
		}
		if (a.from == "root") != (b.from == "root") {
			return a.from == "root"
		}
		if a.from != b.from {
			return a.from < b.from
		}
		if a.version != b.version {
			av, aErr := semver.Parse(a.version)
			bv, bErr := semver.Parse(b.version)
			if aErr == nil && bErr == nil {
				if compared := av.Compare(bv); compared != 0 {
					return compared < 0
				}
			}
			return a.version < b.version
		}
		return a.constraint.Raw < b.constraint.Raw
	})
	if len(ordered) > maxEvidence {
		ordered = ordered[:maxEvidence]
	}
	components := make(map[string]bool)
	legacy := make([]string, 0, len(ordered))
	report := &domain.ConflictReport{Causes: make([]domain.ConflictCause, 0, len(ordered))}
	for _, cause := range ordered {
		components[cause.component] = true
		if cause.from == "root" {
			legacy = append(legacy, fmt.Sprintf("root requires %s %s", cause.component, cause.constraint.Raw))
			report.Causes = append(report.Causes, domain.ConflictCause{Component: cause.component, Constraint: cause.constraint.Raw, Source: "root"})
		} else {
			components[cause.from] = true
			legacy = append(legacy, fmt.Sprintf("%s@%s requires %s %s", cause.from, cause.version, cause.component, cause.constraint.Raw))
			report.Causes = append(report.Causes, domain.ConflictCause{Component: cause.component, Constraint: cause.constraint.Raw, Source: "parent", SourceComponent: cause.from, SourceVersion: cause.version})
		}
	}
	report.Components = domain.SortedKeys(components)
	return legacy, report
}
