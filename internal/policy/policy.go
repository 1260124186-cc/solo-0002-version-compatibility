// Package policy evaluates upgrade policy rules against plan changes.
package policy

import (
	"fmt"
	"strings"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/semver"
)

// Evaluate checks every rule of the set against the planned changes and
// returns one finding per rule, in rule order.
func Evaluate(set domain.PolicySet, changes []domain.Change) []domain.PolicyFinding {
	findings := make([]domain.PolicyFinding, 0, len(set.Rules))
	for _, rule := range set.Rules {
		findings = append(findings, check(rule, changes))
	}
	return findings
}

// Passed reports whether every finding allows the change set.
func Passed(findings []domain.PolicyFinding) bool {
	for _, finding := range findings {
		if !finding.Passed {
			return false
		}
	}
	return true
}

func check(rule domain.PolicyRule, changes []domain.Change) domain.PolicyFinding {
	finding := domain.PolicyFinding{Rule: rule, Passed: true, Detail: "rule satisfied"}
	switch rule.Kind {
	case domain.RuleNoDowngrade:
		var offenders []string
		for _, change := range changes {
			if change.Kind == domain.ChangeDowngrade {
				offenders = append(offenders, describe(change))
			}
		}
		if len(offenders) > 0 {
			finding.Passed = false
			finding.Detail = "downgrades are not allowed: " + strings.Join(offenders, ", ")
		}
	case domain.RuleMajorChange:
		var offenders []string
		for _, change := range changes {
			if change.From == "" || change.To == "" {
				continue
			}
			from, errFrom := semver.Parse(change.From)
			to, errTo := semver.Parse(change.To)
			if errFrom != nil || errTo != nil {
				continue
			}
			if delta := majorDelta(from.Major, to.Major); delta > *rule.MaxMajorDelta {
				offenders = append(offenders, describe(change))
			}
		}
		if len(offenders) > 0 {
			finding.Passed = false
			finding.Detail = fmt.Sprintf("major version change exceeds %d: %s", *rule.MaxMajorDelta, strings.Join(offenders, ", "))
		}
	case domain.RuleProtectComponents:
		protected := make(map[string]bool, len(rule.Components))
		for _, id := range rule.Components {
			protected[id] = true
		}
		var offenders []string
		for _, change := range changes {
			if change.Kind == domain.ChangeRemove && protected[change.ComponentID] {
				offenders = append(offenders, change.ComponentID)
			}
		}
		if len(offenders) > 0 {
			finding.Passed = false
			finding.Detail = "protected components cannot be removed: " + strings.Join(offenders, ", ")
		}
	default:
		finding.Passed = false
		finding.Detail = "unknown rule kind"
	}
	return finding
}

func describe(change domain.Change) string {
	return fmt.Sprintf("%s %s→%s", change.ComponentID, change.From, change.To)
}

func majorDelta(a, b uint64) uint64 {
	if a > b {
		return a - b
	}
	return b - a
}
