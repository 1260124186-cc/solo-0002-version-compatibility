package domain

import "time"

// Rule kinds supported by upgrade policy sets.
const (
	RuleNoDowngrade       = "no_downgrade"
	RuleMajorChange       = "major_change"
	RuleProtectComponents = "protect_components"
)

const (
	MaxPolicySets          = 200
	MaxPolicyRules         = 32
	MaxProtectedComponents = 500
)

// maxMajorDeltaBound matches the uint32 range of semver numeric parts.
const maxMajorDeltaBound = 4294967295

type PolicyRule struct {
	Kind          string   `json:"kind"`
	MaxMajorDelta *uint64  `json:"max_major_delta,omitempty"`
	Components    []string `json:"components,omitempty"`
}

// PolicyFinding records the judgment of one rule during plan validation.
type PolicyFinding struct {
	Rule   PolicyRule `json:"rule"`
	Passed bool       `json:"passed"`
	Detail string     `json:"detail"`
}

type PolicySet struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Rules     []PolicyRule `json:"rules"`
	Revision  uint64       `json:"revision"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

type PolicySetInput struct {
	ID    string       `json:"id"`
	Name  string       `json:"name"`
	Rules []PolicyRule `json:"rules"`
}

type PolicySetUpdateInput struct {
	Revision uint64       `json:"revision"`
	Name     string       `json:"name"`
	Rules    []PolicyRule `json:"rules"`
}

// BindPolicyInput attaches a policy set to an environment; an empty
// PolicyID removes the current binding.
type BindPolicyInput struct {
	PolicyID string `json:"policy_id"`
}

func ValidatePolicyRule(rule PolicyRule) error {
	switch rule.Kind {
	case RuleNoDowngrade:
		if rule.MaxMajorDelta != nil || len(rule.Components) != 0 {
			return Invalid("no_downgrade does not accept parameters")
		}
	case RuleMajorChange:
		if rule.MaxMajorDelta == nil {
			return Invalid("major_change requires max_major_delta")
		}
		if *rule.MaxMajorDelta > maxMajorDeltaBound {
			return Invalid("max_major_delta exceeds the version range")
		}
		if len(rule.Components) != 0 {
			return Invalid("major_change does not accept components")
		}
	case RuleProtectComponents:
		if rule.MaxMajorDelta != nil {
			return Invalid("protect_components does not accept max_major_delta")
		}
		if len(rule.Components) == 0 || len(rule.Components) > MaxProtectedComponents {
			return Invalid("components must contain 1–%d entries", MaxProtectedComponents)
		}
		seen := make(map[string]bool, len(rule.Components))
		for _, id := range rule.Components {
			if err := ValidateID(id); err != nil {
				return err
			}
			if seen[id] {
				return Invalid("component %q is protected more than once", id)
			}
			seen[id] = true
		}
	default:
		return Invalid("unknown rule kind %q", rule.Kind)
	}
	return nil
}

func ValidatePolicyRules(rules []PolicyRule) error {
	if len(rules) == 0 || len(rules) > MaxPolicyRules {
		return Invalid("rules must contain 1–%d entries", MaxPolicyRules)
	}
	seen := make(map[string]bool, len(rules))
	for _, rule := range rules {
		if seen[rule.Kind] {
			return Invalid("rule kind %q appears more than once", rule.Kind)
		}
		seen[rule.Kind] = true
		if err := ValidatePolicyRule(rule); err != nil {
			return err
		}
	}
	return nil
}

func ValidatePolicySet(id, name string, rules []PolicyRule) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	if err := ValidateText(name, "name", 1, 120); err != nil {
		return err
	}
	return ValidatePolicyRules(rules)
}

func CopyRules(rules []PolicyRule) []PolicyRule {
	result := make([]PolicyRule, len(rules))
	for i, rule := range rules {
		result[i] = rule
		if rule.MaxMajorDelta != nil {
			delta := *rule.MaxMajorDelta
			result[i].MaxMajorDelta = &delta
		}
		if rule.Components != nil {
			result[i].Components = append([]string(nil), rule.Components...)
		}
	}
	return result
}
