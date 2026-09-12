package repository

import (
	"fmt"

	"solo-0002-version-compatibility/internal/domain"
)

// validateDriftFindings checks structural invariants of a stored record. It
// deliberately does not recompute findings against the current environment or
// catalog: a record is an immutable statement about the revisions named in it,
// and later revisions must not invalidate the persisted evidence.
func validateDriftFindings(check domain.DriftCheck) error {
	total := len(check.Missing) + len(check.Extra) + len(check.VersionMismatches)
	if total > domain.MaxReportedComponents {
		return fmt.Errorf("drift check reports too many components")
	}
	statuses := map[string]bool{}
	for _, finding := range check.Missing {
		if finding.Status != domain.DriftMissingComponent || finding.ComponentID == "" || finding.Expected == "" || finding.Actual != "" {
			return fmt.Errorf("invalid missing finding")
		}
		statuses[finding.ComponentID] = true
	}
	for _, finding := range check.Extra {
		if finding.Status != domain.DriftExtraComponent || finding.ComponentID == "" || finding.Actual == "" || finding.Expected != "" {
			return fmt.Errorf("invalid extra finding")
		}
		statuses[finding.ComponentID] = true
	}
	for _, finding := range check.VersionMismatches {
		if finding.Status != domain.DriftVersionMismatch || finding.ComponentID == "" || finding.Expected == "" || finding.Actual == "" {
			return fmt.Errorf("invalid version mismatch finding")
		}
		statuses[finding.ComponentID] = true
	}
	if len(statuses) != total {
		return fmt.Errorf("drift component findings overlap")
	}
	if len(check.Installed) > domain.MaxReportedComponents {
		return fmt.Errorf("drift check installed set exceeds capacity")
	}
	for _, violation := range check.Violations {
		if violation.ComponentID == "" || violation.Requires == "" || violation.Constraint == "" || violation.Reason == "" {
			return fmt.Errorf("invalid drift violation")
		}
	}
	for _, item := range check.Unverifiable {
		if item.ComponentID == "" || item.Version == "" || item.Reason == "" {
			return fmt.Errorf("invalid unverifiable entry")
		}
	}
	return nil
}
