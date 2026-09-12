package domain

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"solo-0002-version-compatibility/internal/semver"
)

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,63}$`)
var familyPattern = idPattern

func ValidateID(id string) error {
	if !idPattern.MatchString(id) {
		return Invalid("identifier must be 2–64 lowercase letters, digits or hyphens and start with a letter")
	}
	return nil
}

func ValidateText(value, field string, minimum, maximum int) error {
	if !utf8.ValidString(value) {
		return Invalid("%s must be valid UTF-8", field)
	}
	n := utf8.RuneCountInString(value)
	if n < minimum || n > maximum || strings.TrimSpace(value) != value {
		return Invalid("%s must contain %d–%d characters without outer spaces", field, minimum, maximum)
	}
	for _, c := range value {
		if unicode.IsControl(c) && c != '\n' && c != '\t' {
			return Invalid("%s contains a control character", field)
		}
	}
	return nil
}

func ValidateRequirements(requirements map[string]string, allowEmpty bool) error {
	if len(requirements) > MaxDependencies || (!allowEmpty && len(requirements) == 0) {
		return Invalid("requirements must contain %d–%d entries", boolMinimum(allowEmpty), MaxDependencies)
	}
	for _, id := range SortedKeys(requirements) {
		if err := ValidateID(id); err != nil {
			return err
		}
		if _, err := semver.ParseConstraint(requirements[id]); err != nil {
			return Invalid("requirement for %s: %s", id, err)
		}
	}
	return nil
}

// ValidateRequirementInputs validates dependency edges declared through the
// release API, which accept either a constraint string or an object carrying an
// explicit internal visibility assertion.
func ValidateRequirementInputs(requirements map[string]RequirementInput, allowEmpty bool) error {
	if len(requirements) > MaxDependencies || (!allowEmpty && len(requirements) == 0) {
		return Invalid("requirements must contain %d–%d entries", boolMinimum(allowEmpty), MaxDependencies)
	}
	for _, id := range SortedKeys(requirements) {
		if err := ValidateID(id); err != nil {
			return err
		}
		requirement := requirements[id]
		if _, err := semver.ParseConstraint(requirement.Constraint); err != nil {
			return Invalid("requirement for %s: %s", id, err)
		}
		if requirement.Visibility != "" && requirement.Visibility != Public && requirement.Visibility != Internal {
			return Invalid("requirement for %s has unknown visibility %q", id, requirement.Visibility)
		}
	}
	return nil
}

func boolMinimum(allowEmpty bool) int {
	if allowEmpty {
		return 0
	}
	return 1
}

func SortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func CopyStrings(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for k, v := range values {
		result[k] = v
	}
	return result
}

func ValidateComponent(input ComponentInput) error {
	if err := ValidateID(input.ID); err != nil {
		return err
	}
	if err := ValidateText(input.Name, "name", 1, 120); err != nil {
		return err
	}
	if err := ValidateText(input.Description, "description", 0, 2000); err != nil {
		return err
	}
	visibility := input.Visibility
	if visibility == "" {
		visibility = Public
	}
	if visibility != Public && visibility != Internal {
		return Invalid("visibility must be %q or %q", Public, Internal)
	}
	if input.Family != "" && !familyPattern.MatchString(input.Family) {
		return Invalid("family must be 2–64 lowercase letters, digits or hyphens and start with a letter")
	}
	if visibility == Internal && input.Family == "" {
		return Invalid("an internal component must declare a family")
	}
	if len(input.AllowedConsumers) > MaxConsumers {
		return Invalid("allowed_consumers may contain at most %d entries", MaxConsumers)
	}
	seen := make(map[string]bool, len(input.AllowedConsumers))
	for _, consumer := range input.AllowedConsumers {
		if err := ValidateID(consumer); err != nil {
			return Invalid("allowed consumer: %s", err)
		}
		if consumer == input.ID {
			return Invalid("allowed_consumers cannot contain the component itself")
		}
		if seen[consumer] {
			return Invalid("allowed consumer %q is listed more than once", consumer)
		}
		seen[consumer] = true
	}
	if visibility == Public && len(input.AllowedConsumers) > 0 {
		return Invalid("allowed_consumers require an internal component")
	}
	return nil
}

// ValidateComponentPolicy validates the stored component against the catalog
// so visibility grants cannot point at missing components.
func ValidateComponentPolicy(component Component, components map[string]Component) error {
	if err := ValidateComponent(ComponentInput{ID: component.ID, Name: component.Name, Description: component.Description, Family: component.Family, Visibility: component.Visibility, AllowedConsumers: component.AllowedConsumers}); err != nil {
		return err
	}
	if EffectiveVisibility(component) == Internal {
		for _, consumer := range component.AllowedConsumers {
			if _, exists := components[consumer]; !exists {
				return Invalid("component %s allows unknown consumer %s", component.ID, consumer)
			}
		}
	}
	return nil
}

func ValidateRelease(input ReleaseInput, componentID string) error {
	if _, err := semver.Parse(input.Version); err != nil {
		return Invalid("%s", err)
	}
	if err := ValidateRequirementInputs(input.Requires, true); err != nil {
		return err
	}
	if _, exists := input.Requires[componentID]; exists {
		return Invalid("a release cannot directly depend on its own component")
	}
	return nil
}

// RequirementConstraints projects validated edge inputs onto constraint strings.
func RequirementConstraints(input map[string]RequirementInput) map[string]string {
	result := make(map[string]string, len(input))
	for id, requirement := range input {
		result[id] = requirement.Constraint
	}
	return result
}

// StoredRequirements reconstructs edge inputs from persisted constraints and
// the set of edges declared internal, for validation of on-disk releases.
func StoredRequirements(constraints map[string]string, internal []string) map[string]RequirementInput {
	internalSet := make(map[string]bool, len(internal))
	for _, id := range internal {
		internalSet[id] = true
	}
	result := make(map[string]RequirementInput, len(constraints))
	for id, constraint := range constraints {
		visibility := Public
		if internalSet[id] {
			visibility = Internal
		}
		result[id] = RequirementInput{Constraint: constraint, Visibility: visibility}
	}
	return result
}

// InternalDependencyIDs lists edges explicitly declared internal, sorted.
func InternalDependencyIDs(input map[string]RequirementInput) []string {
	ids := make([]string, 0)
	for id, requirement := range input {
		if requirement.IsInternal() {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
