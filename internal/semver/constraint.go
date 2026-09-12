package semver

import (
	"fmt"
	"strings"
)

type Predicate struct {
	Operator string
	Version  Version
}

// Constraint is a union (OR) of intersection branches (AND of predicates).
// An empty branch (nil predicates) matches every stable version; it is the
// parsed form of "*". Legacy intersection constraints without "||" produce a
// single branch, preserving their original meaning byte for byte.
type Constraint struct {
	Raw      string
	Branches [][]Predicate
}

const (
	maxConstraintLength   = 256
	maxConstraintBranches = 8
	maxBranchTokens       = 8
)

func ParseConstraint(raw string) (Constraint, error) {
	c := Constraint{Raw: raw}
	if len(raw) == 0 || len(raw) > maxConstraintLength || strings.TrimSpace(raw) != raw {
		return c, fmt.Errorf("constraint must contain 1–%d characters without outer spaces", maxConstraintLength)
	}
	branches := strings.Split(raw, "||")
	if len(branches) > maxConstraintBranches {
		return c, fmt.Errorf("invalid constraint %q: more than %d union branches", raw, maxConstraintBranches)
	}
	for _, branch := range branches {
		tokens := strings.Fields(branch)
		if len(tokens) == 0 {
			return c, fmt.Errorf("invalid constraint %q: empty union branch", raw)
		}
		if len(tokens) > maxBranchTokens {
			return c, fmt.Errorf("invalid constraint %q: too many predicates in one branch", raw)
		}
		if len(tokens) == 1 && tokens[0] == "*" {
			c.Branches = append(c.Branches, nil)
			continue
		}
		predicates := make([]Predicate, 0, len(tokens))
		for _, token := range tokens {
			expanded, err := parsePredicate(raw, token)
			if err != nil {
				return c, err
			}
			predicates = append(predicates, expanded...)
		}
		c.Branches = append(c.Branches, predicates)
	}
	return c, nil
}

// parsePredicate expands "^" and "~" into their lower and upper bounds,
// keeping the other operators as a single predicate.
func parsePredicate(raw, token string) ([]Predicate, error) {
	op, rest := "=", token
	for _, prefix := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(token, prefix) {
			op, rest = prefix, strings.TrimPrefix(token, prefix)
			break
		}
	}
	v, err := Parse(rest)
	if err != nil {
		return nil, fmt.Errorf("invalid constraint %q: %w", raw, err)
	}
	if op == "^" || op == "~" {
		upper := Version{Major: v.Major, Minor: v.Minor + 1}
		if op == "^" {
			switch {
			case v.Major > 0:
				upper = Version{Major: v.Major + 1}
			case v.Minor > 0:
				upper = Version{Minor: v.Minor + 1}
			default:
				upper = Version{Patch: v.Patch + 1}
			}
		}
		return []Predicate{{">=", v}, {"<", upper}}, nil
	}
	return []Predicate{{op, v}}, nil
}

func (c Constraint) Matches(v Version) bool {
	for _, branch := range c.Branches {
		if branchMatches(v, branch) {
			return true
		}
	}
	return false
}

func branchMatches(v Version, predicates []Predicate) bool {
	for _, p := range predicates {
		n := v.Compare(p.Version)
		switch p.Operator {
		case "=":
			if n != 0 {
				return false
			}
		case ">=":
			if n < 0 {
				return false
			}
		case "<=":
			if n > 0 {
				return false
			}
		case ">":
			if n <= 0 {
				return false
			}
		case "<":
			if n >= 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
