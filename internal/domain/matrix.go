package domain

import (
	"time"

	"solo-0002-version-compatibility/internal/semver"
)

const (
	MaxMatrixVersions = 12
	MaxMatrices       = 500
)

type MatrixAxis struct {
	Component string   `json:"component"`
	Versions  []string `json:"versions"`
}

type MatrixInput struct {
	First  MatrixAxis        `json:"first"`
	Second MatrixAxis        `json:"second"`
	Roots  map[string]string `json:"roots"`
	Save   bool              `json:"save"`
}

type MatrixCell struct {
	FirstVersion  string            `json:"first_version"`
	SecondVersion string            `json:"second_version"`
	Compatible    bool              `json:"compatible"`
	Resolved      map[string]string `json:"resolved,omitempty"`
	Detail        string            `json:"detail,omitempty"`
	Conflicts     []string          `json:"conflicts,omitempty"`
}

type Matrix struct {
	ID              string            `json:"id,omitempty"`
	CatalogRevision uint64            `json:"catalog_revision"`
	First           MatrixAxis        `json:"first"`
	Second          MatrixAxis        `json:"second"`
	Roots           map[string]string `json:"roots"`
	Cells           []MatrixCell      `json:"cells"`
	Compatible      int               `json:"compatible"`
	Incompatible    int               `json:"incompatible"`
	Steps           int               `json:"steps"`
	Saved           bool              `json:"saved"`
	CreatedAt       time.Time         `json:"created_at"`
}

func ValidateMatrixInput(input MatrixInput) error {
	if err := ValidateID(input.First.Component); err != nil {
		return err
	}
	if err := ValidateID(input.Second.Component); err != nil {
		return err
	}
	if input.First.Component == input.Second.Component {
		return Invalid("matrix axes must reference two different components")
	}
	if err := validateMatrixVersions(input.First.Versions); err != nil {
		return err
	}
	if err := validateMatrixVersions(input.Second.Versions); err != nil {
		return err
	}
	if err := ValidateRequirements(input.Roots, true); err != nil {
		return err
	}
	if len(input.Roots) > MaxDependencies-2 {
		return Invalid("roots must contain at most %d entries so the two axis pins fit the dependency limit", MaxDependencies-2)
	}
	if _, exists := input.Roots[input.First.Component]; exists {
		return Invalid("roots must not include axis component %s", input.First.Component)
	}
	if _, exists := input.Roots[input.Second.Component]; exists {
		return Invalid("roots must not include axis component %s", input.Second.Component)
	}
	return nil
}

func validateMatrixVersions(versions []string) error {
	if len(versions) == 0 || len(versions) > MaxMatrixVersions {
		return Invalid("each axis must list 1–%d candidate versions", MaxMatrixVersions)
	}
	seen := make(map[string]bool, len(versions))
	for _, raw := range versions {
		if _, err := semver.Parse(raw); err != nil {
			return Invalid("%s", err)
		}
		if seen[raw] {
			return Invalid("duplicate candidate version %s", raw)
		}
		seen[raw] = true
	}
	return nil
}
