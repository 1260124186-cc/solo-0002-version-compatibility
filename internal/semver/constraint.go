package semver

import (
	"fmt"
	"strings"
)

type Predicate struct {
	Operator string
	Version  Version
}

type Constraint struct {
	Raw        string
	Predicates []Predicate
}

func ParseConstraint(raw string) (Constraint, error) {
	c := Constraint{Raw: raw}
	if len(raw) == 0 || len(raw) > 256 || strings.TrimSpace(raw) != raw {
		return c, fmt.Errorf("constraint must contain 1–256 characters without outer spaces")
	}
	if raw == "*" {
		return c, nil
	}
	tokens := strings.Fields(raw)
	if len(tokens) > 8 {
		return c, fmt.Errorf("constraint contains too many predicates")
	}
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
			return c, fmt.Errorf("invalid constraint %q: %w", raw, err)
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
			c.Predicates = append(c.Predicates, Predicate{">=", v}, Predicate{"<", upper})
		} else {
			c.Predicates = append(c.Predicates, Predicate{op, v})
		}
	}
	return c, nil
}

func (c Constraint) Matches(v Version) bool {
	for _, p := range c.Predicates {
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
