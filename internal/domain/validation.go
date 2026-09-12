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
	return ValidateText(input.Description, "description", 0, 2000)
}

func ValidateComponentUpdate(input ComponentUpdateInput) error {
	if input.Revision == 0 {
		return Invalid("revision must be positive")
	}
	if err := ValidateText(input.Name, "name", 1, 120); err != nil {
		return err
	}
	return ValidateText(input.Description, "description", 0, 2000)
}

func ValidateRelease(input ReleaseInput, componentID string) error {
	if _, err := semver.Parse(input.Version); err != nil {
		return Invalid("%s", err)
	}
	if err := ValidateRequirements(input.Requires, true); err != nil {
		return err
	}
	if _, exists := input.Requires[componentID]; exists {
		return Invalid("a release cannot directly depend on its own component")
	}
	return nil
}
