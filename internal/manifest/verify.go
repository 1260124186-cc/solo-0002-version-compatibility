package manifest

import (
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strings"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/semver"
)

// maxManifestComponents mirrors the solver's component bound; a manifest
// describing a larger set cannot come from this service's resolution.
const maxManifestComponents = 128

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Verify checks a submitted manifest against this instance's catalog and
// reports every inconsistency found. The catalog revision is deliberately
// not compared: instances revise catalogs independently, so a revision
// difference alone says nothing about compatibility.
func Verify(m domain.Manifest, catalog domain.Catalog) (domain.ManifestReport, error) {
	report := domain.ManifestReport{Format: m.Format, Issues: make([]domain.ManifestIssue, 0)}
	if m.Format != domain.ManifestFormat {
		report.Issues = append(report.Issues, domain.ManifestIssue{
			Kind:   domain.IssueUnsupportedFormat,
			Detail: fmt.Sprintf("manifest format %d is not supported by this instance (supports format %d)", m.Format, domain.ManifestFormat),
		})
		return report, nil
	}
	// The digest arbitrates whether the content is intact. A mismatch means
	// the content was altered after generation, so no further finding about
	// its meaning would be trustworthy; corruption is reported on its own.
	if !digestPattern.MatchString(m.Digest) {
		report.Issues = append(report.Issues, domain.ManifestIssue{
			Kind:   domain.IssueInvalidManifest,
			Detail: `digest must be "sha256:" followed by 64 lowercase hex characters`,
		})
		return report, nil
	}
	computed, err := Digest(m)
	if err != nil {
		return report, err
	}
	report.DigestMatch = computed == m.Digest
	if !report.DigestMatch {
		report.Issues = append(report.Issues, domain.ManifestIssue{
			Kind:   domain.IssueContentCorrupted,
			Detail: "digest does not match the manifest content; the content was altered or corrupted after generation",
		})
		return report, nil
	}
	report.Issues = append(report.Issues, structureIssues(m)...)
	if len(report.Issues) == 0 {
		compareCatalog(&report, m, catalog)
	}
	report.Valid = len(report.Issues) == 0
	return report, nil
}

// structureIssues validates the manifest's internal consistency so the
// catalog comparison can rely on it.
func structureIssues(m domain.Manifest) []domain.ManifestIssue {
	issues := make([]domain.ManifestIssue, 0)
	add := func(detail string) {
		issues = append(issues, domain.ManifestIssue{Kind: domain.IssueInvalidManifest, Detail: detail})
	}
	if err := domain.ValidateRequirements(m.Roots, false); err != nil {
		add(fmt.Sprintf("roots are invalid: %s", err))
	}
	if len(m.Resolved) == 0 || len(m.Resolved) > maxManifestComponents {
		add(fmt.Sprintf("resolved set must contain 1-%d components", maxManifestComponents))
	}
	for _, id := range domain.SortedKeys(m.Resolved) {
		if err := domain.ValidateID(id); err != nil {
			add(fmt.Sprintf("resolved component %q: %s", id, err))
		}
		if _, err := semver.Parse(m.Resolved[id]); err != nil {
			add(fmt.Sprintf("resolved version %q for component %q: %s", m.Resolved[id], id, err))
		}
	}
	for _, id := range domain.SortedKeys(m.Roots) {
		if _, exists := m.Resolved[id]; !exists {
			add(fmt.Sprintf("root component %q is not in the resolved set", id))
		}
	}
	if len(m.Releases) > maxManifestComponents {
		add(fmt.Sprintf("release definitions must contain at most %d entries", maxManifestComponents))
	}
	definitions := make(map[string]string, len(m.Releases))
	for _, release := range m.Releases {
		if err := domain.ValidateID(release.ComponentID); err != nil {
			add(fmt.Sprintf("release definition component %q: %s", release.ComponentID, err))
		}
		if _, err := semver.Parse(release.Version); err != nil {
			add(fmt.Sprintf("release definition version %q for component %q: %s", release.Version, release.ComponentID, err))
		}
		if err := domain.ValidateRequirements(release.Requires, true); err != nil {
			add(fmt.Sprintf("release definition %s@%s requires are invalid: %s", release.ComponentID, release.Version, err))
		}
		if _, duplicate := definitions[release.ComponentID]; duplicate {
			add(fmt.Sprintf("component %q has more than one release definition", release.ComponentID))
		}
		definitions[release.ComponentID] = release.Version
	}
	for _, id := range domain.SortedKeys(m.Resolved) {
		if definitions[id] != m.Resolved[id] {
			add(fmt.Sprintf("resolved component %s@%s has no matching release definition", id, m.Resolved[id]))
		}
	}
	for _, id := range domain.SortedKeys(definitions) {
		if m.Resolved[id] != definitions[id] {
			add(fmt.Sprintf("release definition %s@%s is not part of the resolved set", id, definitions[id]))
		}
	}
	return issues
}

// compareCatalog checks that every referenced release exists on this
// instance and that its dependency definition is identical.
func compareCatalog(report *domain.ManifestReport, m domain.Manifest, catalog domain.Catalog) {
	releases := make([]domain.ManifestRelease, len(m.Releases))
	copy(releases, m.Releases)
	sort.Slice(releases, func(i, j int) bool {
		if releases[i].ComponentID != releases[j].ComponentID {
			return releases[i].ComponentID < releases[j].ComponentID
		}
		return releases[i].Version < releases[j].Version
	})
	for _, definition := range releases {
		report.ReleasesChecked++
		id := definition.ComponentID
		if _, exists := catalog.Components[id]; !exists {
			report.Issues = append(report.Issues, domain.ManifestIssue{
				Kind:        domain.IssueMissingRelease,
				ComponentID: id,
				Version:     definition.Version,
				Detail:      fmt.Sprintf("component %q does not exist on this instance", id),
			})
			continue
		}
		release, exists := catalog.Releases[id][definition.Version]
		if !exists {
			report.Issues = append(report.Issues, domain.ManifestIssue{
				Kind:        domain.IssueMissingRelease,
				ComponentID: id,
				Version:     definition.Version,
				Detail:      fmt.Sprintf("release %s@%s does not exist on this instance", id, definition.Version),
			})
			continue
		}
		if !maps.Equal(release.Requires, definition.Requires) {
			report.Issues = append(report.Issues, domain.ManifestIssue{
				Kind:        domain.IssueRequiresMismatch,
				ComponentID: id,
				Version:     definition.Version,
				Detail: fmt.Sprintf("release %s@%s dependency definitions differ: manifest requires %s, this instance requires %s",
					id, definition.Version, formatRequires(definition.Requires), formatRequires(release.Requires)),
			})
		}
	}
}

func formatRequires(requires map[string]string) string {
	if len(requires) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(requires))
	for _, id := range domain.SortedKeys(requires) {
		parts = append(parts, fmt.Sprintf("%s %q", id, requires[id]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
