package repository

import (
	"fmt"
	"sort"
	"strings"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/semver"
)

func validateState(s *State) error {
	if s.Schema != 1 || s.Catalog.Components == nil || s.Catalog.Releases == nil || s.Environments == nil || s.Plans == nil || s.Imports == nil {
		return fmt.Errorf("unsupported schema or missing collections")
	}
	if s.Catalog.Revision > s.Revision || len(s.Catalog.Components) > domain.MaxComponents {
		return fmt.Errorf("invalid catalog revision or component count")
	}
	if len(s.Environments) > 200 || len(s.Plans) > 5000 || len(s.Events) > 10000 {
		return fmt.Errorf("persisted collection exceeds capacity")
	}
	for id, component := range s.Catalog.Components {
		if s.Catalog.Releases[id] == nil {
			return fmt.Errorf("component is missing its release collection")
		}
		if id != component.ID {
			return fmt.Errorf("component key mismatch")
		}
		if err := domain.ValidateComponent(domain.ComponentInput{ID: id, Name: component.Name, Description: component.Description}); err != nil {
			return err
		}
	}
	for id, releases := range s.Catalog.Releases {
		if _, ok := s.Catalog.Components[id]; !ok {
			return fmt.Errorf("orphan release collection")
		}
		if len(releases) > domain.MaxReleases {
			return fmt.Errorf("too many releases")
		}
		for version, release := range releases {
			if release.ComponentID != id || release.Version != version {
				return fmt.Errorf("release key mismatch")
			}
			if err := domain.ValidateRelease(domain.ReleaseInput{Version: version, Requires: release.Requires}, id); err != nil {
				return err
			}
			if release.State != domain.Available && release.State != domain.Withdrawn {
				return fmt.Errorf("invalid release state")
			}
			if (release.State == domain.Withdrawn) != (release.WithdrawnAt != nil) {
				return fmt.Errorf("invalid withdrawal timestamp")
			}
			for dep := range release.Requires {
				if _, ok := s.Catalog.Components[dep]; !ok {
					return fmt.Errorf("unknown dependency %s", dep)
				}
			}
		}
	}
	for id, env := range s.Environments {
		if id != env.ID || env.Revision == 0 {
			return fmt.Errorf("invalid environment identity or revision")
		}
		if err := domain.ValidateID(id); err != nil {
			return err
		}
		if err := domain.ValidateText(env.Name, "name", 1, 120); err != nil {
			return err
		}
		if err := ValidateSelection(s.Catalog, env.Roots, env.Resolved, true); err != nil {
			return err
		}
	}
	for id, plan := range s.Plans {
		if id != plan.ID || plan.Revision == 0 {
			return fmt.Errorf("invalid plan identity or revision")
		}
		env, ok := s.Environments[plan.EnvironmentID]
		if !ok || plan.BaseRevision == 0 || plan.BaseRevision > env.Revision || plan.CatalogRevision > s.Catalog.Revision {
			return fmt.Errorf("plan references an invalid revision")
		}
		if err := domain.ValidateRequirements(plan.Roots, false); err != nil {
			return err
		}
		switch plan.State {
		case domain.Draft, domain.Cancelled:
		case domain.Ready, domain.Applied:
			if err := ValidateSelection(s.Catalog, plan.Roots, plan.Resolved, false); err != nil {
				return err
			}
		default:
			return fmt.Errorf("invalid plan state")
		}
	}
	if len(s.Imports) > domain.MaxImports {
		return fmt.Errorf("import collection exceeds capacity")
	}
	for id, importRecord := range s.Imports {
		if id != importRecord.ID {
			return fmt.Errorf("import key mismatch")
		}
		if err := validateImport(s.Catalog, importRecord); err != nil {
			return err
		}
	}
	var previous uint64
	for i, event := range s.Events {
		if event.Sequence == 0 || event.Sequence > s.Revision || (i > 0 && event.Sequence != previous+1) {
			return fmt.Errorf("invalid event sequence")
		}
		previous = event.Sequence
	}
	if previous != s.Revision {
		return fmt.Errorf("event tail does not match state revision")
	}
	return nil
}

// ValidateSelection also verifies that the set contains no unreachable entries.
func ValidateSelection(c domain.Catalog, roots, selected map[string]string, available bool) error {
	if err := domain.ValidateRequirements(roots, false); err != nil {
		return err
	}
	seen := make(map[string]bool)
	var visit func(string, string) error
	visit = func(id, raw string) error {
		version, exists := selected[id]
		if !exists {
			return fmt.Errorf("selection omits %s", id)
		}
		release, exists := c.Releases[id][version]
		if !exists || (available && release.State != domain.Available) {
			return fmt.Errorf("selection references unavailable %s@%s", id, version)
		}
		constraint, err := semver.ParseConstraint(raw)
		if err != nil {
			return err
		}
		v, err := semver.Parse(version)
		if err != nil {
			return err
		}
		if !constraint.Matches(v) {
			return fmt.Errorf("%s@%s violates %s", id, version, raw)
		}
		if seen[id] {
			return nil
		}
		seen[id] = true
		for _, dep := range domain.SortedKeys(release.Requires) {
			if err := visit(dep, release.Requires[dep]); err != nil {
				return err
			}
		}
		return nil
	}
	for _, id := range domain.SortedKeys(roots) {
		if err := visit(id, roots[id]); err != nil {
			return err
		}
	}
	if len(seen) != len(selected) {
		return fmt.Errorf("selection contains unreachable components")
	}
	return nil
}

func validateImport(c domain.Catalog, record domain.Import) error {
	if len(record.Fingerprint) != 64 || strings.TrimLeft(record.Fingerprint, "0123456789abcdef") != "" {
		return fmt.Errorf("import fingerprint is invalid")
	}
	if len(record.Entries) == 0 || len(record.Entries) > domain.MaxImportEntries {
		return fmt.Errorf("import entries out of range")
	}
	if record.Summary.Entries != len(record.Entries) {
		return fmt.Errorf("import summary does not match entries")
	}
	switch record.State {
	case domain.ImportPending:
		if record.CompletedAt != nil || record.FailedAt != nil {
			return fmt.Errorf("pending import carries a terminal timestamp")
		}
	case domain.ImportCompleted:
		if record.CompletedAt == nil || record.FailedAt != nil || record.CatalogRevision > c.Revision {
			return fmt.Errorf("completed import has invalid metadata")
		}
	case domain.ImportFailed:
		if record.FailedAt == nil || record.CompletedAt != nil {
			return fmt.Errorf("failed import has invalid metadata")
		}
	default:
		return fmt.Errorf("invalid import state")
	}
	if record.CreatedAt.IsZero() {
		return fmt.Errorf("import is missing a creation timestamp")
	}
	seen := make(map[string]bool, len(record.Entries))
	errorCount := 0
	createCount, addCount, presentCount := 0, 0, 0
	for n, entry := range record.Entries {
		if entry.Index != n {
			return fmt.Errorf("import entry indexes are not contiguous")
		}
		if entry.Requires == nil {
			return fmt.Errorf("import entry is missing requirements")
		}
		// Failed imports persist the exact invalid manifest lines that caused
		// them, so their syntax is only validated when the row itself is clean.
		if len(entry.Errors) > 0 {
			if record.State != domain.ImportFailed {
				return fmt.Errorf("entry errors only belong to failed imports")
			}
			errorCount += len(entry.Errors)
			continue
		}
		if err := domain.ValidateID(entry.ComponentID); err != nil {
			return err
		}
		if _, err := semver.Parse(entry.Version); err != nil {
			return err
		}
		key := entry.ComponentID + "@" + entry.Version
		if seen[key] {
			return fmt.Errorf("import repeats %s", key)
		}
		seen[key] = true
		if err := domain.ValidateRequirements(entry.Requires, true); err != nil {
			return err
		}
		if _, self := entry.Requires[entry.ComponentID]; self {
			return fmt.Errorf("import entry depends on its own component")
		}
		if entry.Name != "" {
			if err := domain.ValidateText(entry.Name, "name", 1, 120); err != nil {
				return err
			}
		}
		if err := domain.ValidateText(entry.Description, "description", 0, 2000); err != nil {
			return err
		}
		switch entry.Action {
		case domain.ImportCreateComponent:
			createCount++
			if entry.Name == "" {
				return fmt.Errorf("import entry creates a component without a name")
			}
		case domain.ImportAddRelease:
			addCount++
			if entry.Name != "" || entry.Description != "" {
				return fmt.Errorf("import entry carries metadata for an existing component")
			}
		case domain.ImportAlreadyPresent:
			presentCount++
			if entry.Name != "" || entry.Description != "" {
				return fmt.Errorf("import entry carries metadata for an existing component")
			}
		case "":
			return fmt.Errorf("import entry is missing an action")
		default:
			return fmt.Errorf("import entry has an unknown action")
		}
	}
	if record.Summary.Errors != errorCount {
		return fmt.Errorf("import error summary is wrong")
	}
	if record.State != domain.ImportFailed {
		if record.Summary.CreateComponents != createCount || record.Summary.AddReleases != addCount || record.Summary.AlreadyPresent != presentCount {
			return fmt.Errorf("import action summary does not match entries")
		}
	}
	if record.State == domain.ImportPending && errorCount != 0 {
		return fmt.Errorf("pending import carries entry errors")
	}
	if record.State == domain.ImportCompleted {
		if errorCount != 0 {
			return fmt.Errorf("completed import carries entry errors")
		}
		expectedComponents := make([]string, 0, len(record.CreatedComponents))
		expectedReleases := make([]string, 0, len(record.AddedReleases))
		for _, entry := range record.Entries {
			if entry.Action == domain.ImportCreateComponent {
				expectedComponents = append(expectedComponents, entry.ComponentID)
			}
			if entry.Action == domain.ImportCreateComponent || entry.Action == domain.ImportAddRelease {
				expectedReleases = append(expectedReleases, entry.ComponentID+"@"+entry.Version)
			}
			release, ok := c.Releases[entry.ComponentID][entry.Version]
			if !ok {
				return fmt.Errorf("completed import is missing %s@%s", entry.ComponentID, entry.Version)
			}
			if !sameRequirements(release.Requires, entry.Requires) {
				return fmt.Errorf("completed import diverges from catalog requirements")
			}
		}
		sort.Strings(expectedComponents)
		if !equalStrings(expectedComponents, record.CreatedComponents) {
			return fmt.Errorf("completed import component list is wrong")
		}
		if !equalStrings(expectedReleases, record.AddedReleases) {
			return fmt.Errorf("completed import release list is wrong")
		}
	}
	return nil
}

func sameRequirements(a, b map[string]string) bool {
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

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
