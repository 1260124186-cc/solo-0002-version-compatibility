package resolution

import (
	"context"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/semver"
)

// DriftReport is the classification of an installed set against the resolved
// set expected by one environment revision.
type DriftReport struct {
	Missing           []domain.DriftComponentFinding
	Extra             []domain.DriftComponentFinding
	VersionMismatches []domain.DriftComponentFinding
	Violations        []domain.DriftViolation
	Unverifiable      []domain.DriftUnverifiable
}

// EvaluateDrift never solves or mutates anything. A version that is not
// registered in the catalog is reported as unverifiable rather than assumed
// compatible; dependency edges touching such a release cannot be proven
// either and are recorded with the same status.
func EvaluateDrift(ctx context.Context, catalog domain.Catalog, expected, installed map[string]string) (DriftReport, error) {
	if err := ctx.Err(); err != nil {
		return DriftReport{}, err
	}
	report := DriftReport{
		Missing:           make([]domain.DriftComponentFinding, 0),
		Extra:             make([]domain.DriftComponentFinding, 0),
		VersionMismatches: make([]domain.DriftComponentFinding, 0),
		Violations:        make([]domain.DriftViolation, 0),
		Unverifiable:      make([]domain.DriftUnverifiable, 0),
	}
	// Register of installed releases that have no catalog entry; edges
	// touching them cannot be proven either way.
	unknown := make(map[string]bool)
	for _, id := range domain.SortedKeys(installed) {
		actual := installed[id]
		want, expected := expected[id]
		_, registered := catalog.Releases[id][actual]
		if !registered {
			unknown[id] = true
			report.Unverifiable = append(report.Unverifiable, domain.DriftUnverifiable{
				ComponentID: id,
				Version:     actual,
				Reason:      "installed release is not registered in the component catalog; compatibility cannot be verified",
			})
		}
		switch {
		case !expected:
			report.Extra = append(report.Extra, domain.DriftComponentFinding{
				ComponentID: id,
				Actual:      actual,
				Status:      domain.DriftExtraComponent,
			})
		case actual != want:
			report.VersionMismatches = append(report.VersionMismatches, domain.DriftComponentFinding{
				ComponentID: id,
				Expected:    want,
				Actual:      actual,
				Status:      domain.DriftVersionMismatch,
			})
		}
	}
	for _, id := range domain.SortedKeys(expected) {
		if _, present := installed[id]; !present {
			report.Missing = append(report.Missing, domain.DriftComponentFinding{
				ComponentID: id,
				Expected:    expected[id],
				Status:      domain.DriftMissingComponent,
			})
		}
	}
	// Only registered releases expose verifiable dependency constraints.
	for _, id := range domain.SortedKeys(installed) {
		if unknown[id] {
			continue
		}
		version := installed[id]
		release := catalog.Releases[id][version]
		for _, dep := range domain.SortedKeys(release.Requires) {
			if err := ctx.Err(); err != nil {
				return DriftReport{}, err
			}
			raw := release.Requires[dep]
			constraint, err := semver.ParseConstraint(raw)
			if err != nil {
				// Catalog constraints are validated on write; keep this defensive.
				return DriftReport{}, err
			}
			actual, present := installed[dep]
			switch {
			case !present:
				report.Violations = append(report.Violations, domain.DriftViolation{
					ComponentID: id,
					Version:     version,
					Requires:    dep,
					Constraint:  raw,
					Reason:      "required component is absent from the installed set",
				})
			case unknown[dep]:
				report.Unverifiable = append(report.Unverifiable, domain.DriftUnverifiable{
					ComponentID: dep,
					Version:     actual,
					Reason:      "required dependency uses an unregistered release; constraint cannot be verified",
					Edge:        &domain.DriftEdge{From: id, To: dep, Constraint: raw},
				})
			default:
				actualVersion, err := semver.Parse(actual)
				if err != nil {
					return DriftReport{}, err
				}
				if !constraint.Matches(actualVersion) {
					report.Violations = append(report.Violations, domain.DriftViolation{
						ComponentID: id,
						Version:     version,
						Requires:    dep,
						Constraint:  raw,
						Actual:      actual,
						Reason:      "installed dependency does not satisfy the constraint",
					})
				}
			}
		}
	}
	return report, nil
}
