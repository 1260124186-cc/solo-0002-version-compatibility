package semver

import (
	"fmt"
	"strings"
)

type Predicate struct {
	Operator string
	Version  Version
}

// Constraint is a union of intersection groups: a version matches when it
// satisfies every predicate of at least one alternative.
type Constraint struct {
	Raw          string
	Alternatives [][]Predicate
}

const (
	maxRawConstraint = 256
	maxAlternatives  = 8
	maxPredicates    = 8
)

func ParseConstraint(raw string) (Constraint, error) {
	c := Constraint{Raw: raw}
	if len(raw) == 0 || len(raw) > maxRawConstraint || strings.TrimSpace(raw) != raw {
		return c, fmt.Errorf("constraint must contain 1–256 characters without outer spaces")
	}
	groups := strings.Split(raw, "||")
	if len(groups) > maxAlternatives {
		return c, fmt.Errorf("constraint contains too many alternatives")
	}
	for _, group := range groups {
		predicates, err := parseIntersection(strings.TrimSpace(group))
		if err != nil {
			return c, err
		}
		c.Alternatives = append(c.Alternatives, predicates)
	}
	return c, nil
}

func parseIntersection(raw string) ([]Predicate, error) {
	if raw == "" {
		return nil, fmt.Errorf("constraint contains an empty alternative")
	}
	if raw == "*" {
		return nil, nil
	}
	tokens := strings.Fields(raw)
	if len(tokens) > maxPredicates {
		return nil, fmt.Errorf("constraint contains too many predicates")
	}
	var predicates []Predicate
	for _, token := range tokens {
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
			predicates = append(predicates, Predicate{">=", v}, Predicate{"<", upper})
		} else {
			predicates = append(predicates, Predicate{op, v})
		}
	}
	return predicates, nil
}

func (c Constraint) Matches(v Version) bool {
	for _, alternative := range c.Alternatives {
		if matchesAll(alternative, v) {
			return true
		}
	}
	return false
}

func matchesAll(predicates []Predicate, v Version) bool {
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
