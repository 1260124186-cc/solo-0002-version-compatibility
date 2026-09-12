package domain

import (
	"time"

	"solo-0002-version-compatibility/internal/semver"
)

const (
	DriftMissingComponent = "missing"
	DriftExtraComponent   = "extra"
	DriftVersionMismatch  = "version_mismatch"

	MaxDriftChecks        = 2000
	MaxReportedComponents = 128
	// Missing, extra and version-mismatch findings use disjoint component IDs.
	// The expected and installed sets can each hold MaxReportedComponents and
	// be disjoint, so the findings can number twice that.
	MaxDriftComponentFindings = MaxReportedComponents * 2
)

// DriftComponentFinding describes one difference between the expected
// resolved set and the installed set reported by the caller.
type DriftComponentFinding struct {
	ComponentID string `json:"component_id"`
	Expected    string `json:"expected,omitempty"`
	Actual      string `json:"actual,omitempty"`
	Status      string `json:"status"`
}

// DriftViolation is a dependency constraint proven unsatisfiable inside the
// installed set using registered releases only.
type DriftViolation struct {
	ComponentID string `json:"component_id"`
	Version     string `json:"version"`
	Requires    string `json:"requires"`
	Constraint  string `json:"constraint"`
	Actual      string `json:"actual,omitempty"`
	Reason      string `json:"reason"`
}

// DriftEdge identifies one dependency edge that participates in a finding.
type DriftEdge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Constraint string `json:"constraint"`
}

// DriftUnverifiable marks an installed release that is not registered in the
// catalog; compatibility for it (or for an edge pointing at it) cannot be
// verified, so it must never be treated as compatible.
type DriftUnverifiable struct {
	ComponentID string     `json:"component_id"`
	Version     string     `json:"version"`
	Reason      string     `json:"reason"`
	Edge        *DriftEdge `json:"edge,omitempty"`
}

// DriftCheck is an immutable verification record. It captures the exact
// environment and catalog revisions it was based on; later revisions do not
// alter it.
type DriftCheck struct {
	ID                  string                  `json:"id"`
	EnvironmentID       string                  `json:"environment_id"`
	EnvironmentRevision uint64                  `json:"environment_revision"`
	CatalogRevision     uint64                  `json:"catalog_revision"`
	Installed           map[string]string       `json:"installed"`
	Missing             []DriftComponentFinding `json:"missing"`
	Extra               []DriftComponentFinding `json:"extra"`
	VersionMismatches   []DriftComponentFinding `json:"version_mismatches"`
	Violations          []DriftViolation        `json:"violations"`
	Unverifiable        []DriftUnverifiable     `json:"unverifiable"`
	Conformant          bool                    `json:"conformant"`
	CreatedAt           time.Time               `json:"created_at"`
}

type DriftCheckInput struct {
	EnvironmentRevision uint64            `json:"environment_revision"`
	Installed           map[string]string `json:"installed"`
}

func ValidateInstalled(installed map[string]string) error {
	if len(installed) == 0 {
		return Invalid("installed must contain at least one component")
	}
	if len(installed) > MaxReportedComponents {
		return Invalid("installed must contain at most %d components", MaxReportedComponents)
	}
	for _, id := range SortedKeys(installed) {
		if err := ValidateID(id); err != nil {
			return err
		}
		if _, err := semver.Parse(installed[id]); err != nil {
			return Invalid("installed version for %s: %s", id, err)
		}
	}
	return nil
}

func (c DriftCheck) HasFindings() bool {
	return len(c.Missing) > 0 || len(c.Extra) > 0 || len(c.VersionMismatches) > 0 ||
		len(c.Violations) > 0 || len(c.Unverifiable) > 0
}
