package domain

import (
	"fmt"
	"time"

	"solo-0002-version-compatibility/internal/semver"
)

const (
	SnapshotBaseline = "baseline"
	SnapshotCreated  = "created"
	SnapshotApplied  = "applied"
)

// SnapshotRelease records the dependency definitions of one resolved release
// at capture time, so a snapshot stays self-contained evidence.
type SnapshotRelease struct {
	Version  string            `json:"version"`
	Requires map[string]string `json:"requires"`
}

// EnvironmentSnapshot is an immutable record of an environment revision.
type EnvironmentSnapshot struct {
	EnvironmentID   string                     `json:"environment_id"`
	Revision        uint64                     `json:"revision"`
	Origin          string                     `json:"origin"`
	PlanID          string                     `json:"plan_id,omitempty"`
	Roots           map[string]string          `json:"roots"`
	Resolved        map[string]string          `json:"resolved"`
	Releases        map[string]SnapshotRelease `json:"releases"`
	CatalogRevision uint64                     `json:"catalog_revision"`
	CapturedAt      time.Time                  `json:"captured_at"`
}

type SnapshotVerification struct {
	EnvironmentID   string    `json:"environment_id"`
	Revision        uint64    `json:"revision"`
	CatalogRevision uint64    `json:"catalog_revision"`
	Holds           bool      `json:"holds"`
	Issues          []string  `json:"issues"`
	CheckedAt       time.Time `json:"checked_at"`
}

type VerifySnapshotInput struct {
	CatalogRevision uint64 `json:"catalog_revision"`
}

// CaptureSnapshot freezes the environment state together with the dependency
// definitions of every resolved release in the given catalog.
func CaptureSnapshot(env Environment, catalog Catalog, at time.Time) EnvironmentSnapshot {
	releases := make(map[string]SnapshotRelease, len(env.Resolved))
	for id, version := range env.Resolved {
		release := catalog.Releases[id][version]
		releases[id] = SnapshotRelease{Version: version, Requires: CopyStrings(release.Requires)}
	}
	return EnvironmentSnapshot{
		EnvironmentID:   env.ID,
		Revision:        env.Revision,
		Roots:           CopyStrings(env.Roots),
		Resolved:        CopyStrings(env.Resolved),
		Releases:        releases,
		CatalogRevision: catalog.Revision,
		CapturedAt:      at,
	}
}

// ValidateSnapshot checks the internal consistency of a persisted snapshot.
func ValidateSnapshot(snapshot EnvironmentSnapshot) error {
	if issues := snapshotInternalIssues(snapshot); len(issues) > 0 {
		return fmt.Errorf("invalid snapshot: %s", issues[0])
	}
	return nil
}

// VerifySnapshot reports whether the snapshot still holds against the catalog,
// meaning the recorded set could still be deployed exactly as captured.
func VerifySnapshot(snapshot EnvironmentSnapshot, catalog Catalog, at time.Time) SnapshotVerification {
	issues := snapshotInternalIssues(snapshot)
	for _, id := range SortedKeys(snapshot.Resolved) {
		version := snapshot.Resolved[id]
		if _, exists := catalog.Components[id]; !exists {
			issues = append(issues, fmt.Sprintf("component %s no longer exists in the catalog", id))
			continue
		}
		release, exists := catalog.Releases[id][version]
		if !exists {
			issues = append(issues, fmt.Sprintf("release %s@%s is no longer in the catalog", id, version))
			continue
		}
		if release.State != Available {
			issues = append(issues, fmt.Sprintf("release %s@%s is %s", id, version, release.State))
		}
		if !requirementsEqual(release.Requires, snapshot.Releases[id].Requires) {
			issues = append(issues, fmt.Sprintf("dependency definitions of %s@%s differ from the snapshot", id, version))
		}
	}
	return SnapshotVerification{
		EnvironmentID:   snapshot.EnvironmentID,
		Revision:        snapshot.Revision,
		CatalogRevision: catalog.Revision,
		Holds:           len(issues) == 0,
		Issues:          issues,
		CheckedAt:       at,
	}
}

// snapshotInternalIssues validates roots, resolved versions and the recorded
// dependency closure against the snapshot itself, in deterministic order.
func snapshotInternalIssues(snapshot EnvironmentSnapshot) []string {
	issues := make([]string, 0)
	if err := ValidateRequirements(snapshot.Roots, false); err != nil {
		issues = append(issues, fmt.Sprintf("roots are invalid: %s", err))
	}
	if len(snapshot.Resolved) == 0 {
		issues = append(issues, "resolved set is empty")
	}
	for _, id := range SortedKeys(snapshot.Resolved) {
		version := snapshot.Resolved[id]
		if _, err := semver.Parse(version); err != nil {
			issues = append(issues, fmt.Sprintf("resolved version of %s is invalid: %s", id, err))
		}
		recorded, exists := snapshot.Releases[id]
		if !exists {
			issues = append(issues, fmt.Sprintf("dependency definitions for %s are missing", id))
			continue
		}
		if recorded.Version != version {
			issues = append(issues, fmt.Sprintf("dependency definitions for %s record version %s, resolved %s", id, recorded.Version, version))
		}
	}
	if len(snapshot.Releases) != len(snapshot.Resolved) {
		issues = append(issues, "dependency definitions cover components outside the resolved set")
	}
	for _, id := range SortedKeys(snapshot.Roots) {
		version, exists := snapshot.Resolved[id]
		if !exists {
			issues = append(issues, fmt.Sprintf("root %s is not resolved", id))
			continue
		}
		if !constraintMatches(snapshot.Roots[id], version) {
			issues = append(issues, fmt.Sprintf("resolved %s@%s violates root constraint %s", id, version, snapshot.Roots[id]))
		}
	}
	for _, id := range SortedKeys(snapshot.Releases) {
		for _, dep := range SortedKeys(snapshot.Releases[id].Requires) {
			raw := snapshot.Releases[id].Requires[dep]
			version, exists := snapshot.Resolved[dep]
			if !exists {
				issues = append(issues, fmt.Sprintf("dependency %s of %s is not resolved", dep, id))
				continue
			}
			if !constraintMatches(raw, version) {
				issues = append(issues, fmt.Sprintf("resolved %s@%s violates constraint %s from %s", dep, version, raw, id))
			}
		}
	}
	return issues
}

func constraintMatches(raw, version string) bool {
	constraint, err := semver.ParseConstraint(raw)
	if err != nil {
		return false
	}
	parsed, err := semver.Parse(version)
	if err != nil {
		return false
	}
	return constraint.Matches(parsed)
}

func requirementsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for id, constraint := range a {
		if b[id] != constraint {
			return false
		}
	}
	return true
}
