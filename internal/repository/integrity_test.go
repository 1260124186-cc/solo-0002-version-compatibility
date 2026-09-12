package repository

import (
	"strings"
	"testing"
	"time"

	"solo-0002-version-compatibility/internal/domain"
)

func events(revision uint64) []Event {
	items := make([]Event, 0, revision)
	at := time.Now().UTC()
	for sequence := uint64(1); sequence <= revision; sequence++ {
		items = append(items, Event{Sequence: sequence, Kind: "test", EntityID: "test", Action: "test", At: at})
	}
	return items
}

func componentRecord(id, family, visibility string, consumers ...string) domain.Component {
	return domain.Component{ID: id, Name: id, Family: family, Visibility: visibility, AllowedConsumers: consumers, CreatedAt: time.Now().UTC()}
}

func releaseRecord(id, version string, requires map[string]string) domain.Release {
	return domain.Release{ComponentID: id, Version: version, Requires: requires, State: domain.Available, CreatedAt: time.Now().UTC()}
}

func ptrUint64(value uint64) *uint64 { return &value }

// A schema 1 state written before visibility existed must boot unchanged:
// components default to public and historical records skip boundary checks.
func TestValidateStateMigratesSchemaOne(t *testing.T) {
	now := time.Now().UTC()
	state := &State{
		Schema:   1,
		Revision: 3,
		Catalog: domain.Catalog{
			Revision:   2,
			Components: map[string]domain.Component{"legacy-core": {ID: "legacy-core", Name: "core", CreatedAt: now}},
			Releases: map[string]map[string]domain.Release{
				"legacy-core": {"1.0.0": releaseRecord("legacy-core", "1.0.0", nil)},
			},
		},
		Environments: map[string]domain.Environment{
			"prod": {ID: "prod", Name: "prod", Roots: map[string]string{"legacy-core": "*"}, Resolved: map[string]string{"legacy-core": "1.0.0"}, Revision: 1, CreatedAt: now, UpdatedAt: now},
		},
		Plans: map[string]domain.Plan{
			"plan-1": {ID: "plan-1", EnvironmentID: "prod", BaseRevision: 1, Revision: 2, CatalogRevision: 2, Roots: map[string]string{"legacy-core": "*"}, Resolved: map[string]string{"legacy-core": "1.0.0"}, State: domain.Applied, CreatedAt: now, UpdatedAt: now},
		},
		Events: events(3),
	}
	if err := validateState(state); err != nil {
		t.Fatalf("legacy state failed to boot: %v", err)
	}
	if state.Schema != 2 {
		t.Fatalf("schema was not migrated to 2, got %d", state.Schema)
	}
	if domain.EffectiveVisibility(state.Catalog.Components["legacy-core"]) != domain.Public {
		t.Fatalf("legacy component must default to public")
	}
}

// Applied plans and environments resolved before the rule stay readable even if
// a later policy would now reject the same direct root edge.
func TestValidateStateKeepsHistoricalAppliedRecordsReadable(t *testing.T) {
	now := time.Now().UTC()
	state := &State{
		Schema:   2,
		Revision: 4,
		Catalog: domain.Catalog{
			Revision:           3,
			VisibilityRevision: 1,
			Components: map[string]domain.Component{
				"core-engine": componentRecord("core-engine", "core", domain.Internal),
			},
			Releases: map[string]map[string]domain.Release{
				"core-engine": {"1.0.0": releaseRecord("core-engine", "1.0.0", nil)},
			},
		},
		Environments: map[string]domain.Environment{
			// No visibility revision marks this environment as pre-rule.
			"prod": {ID: "prod", Name: "prod", Roots: map[string]string{"core-engine": "*"}, Resolved: map[string]string{"core-engine": "1.0.0"}, Revision: 1, CatalogRevision: 3, CreatedAt: now, UpdatedAt: now},
		},
		Plans: map[string]domain.Plan{
			"plan-1": {ID: "plan-1", EnvironmentID: "prod", BaseRevision: 1, Revision: 2, CatalogRevision: 3, Roots: map[string]string{"core-engine": "*"}, Resolved: map[string]string{"core-engine": "1.0.0"}, State: domain.Applied, CreatedAt: now, UpdatedAt: now},
		},
		Events: events(4),
	}
	if err := validateState(state); err != nil {
		t.Fatalf("historical applied records must remain readable: %v", err)
	}
}

// Current records carry a visibility revision and must satisfy the boundary.
func TestValidateStateRejectsBoundaryCrossingCurrentEnvironment(t *testing.T) {
	now := time.Now().UTC()
	state := &State{
		Schema:   2,
		Revision: 3,
		Catalog: domain.Catalog{
			Revision:           2,
			VisibilityRevision: 1,
			Components: map[string]domain.Component{
				"core-engine": componentRecord("core-engine", "core", domain.Internal),
			},
			Releases: map[string]map[string]domain.Release{
				"core-engine": {"1.0.0": releaseRecord("core-engine", "1.0.0", nil)},
			},
		},
		Environments: map[string]domain.Environment{
			"prod": {ID: "prod", Name: "prod", Roots: map[string]string{"core-engine": "*"}, Resolved: map[string]string{"core-engine": "1.0.0"}, Revision: 1, CatalogRevision: 2, VisibilityRevision: ptrUint64(1), CreatedAt: now, UpdatedAt: now},
		},
		Plans:  map[string]domain.Plan{},
		Events: events(3),
	}
	// Startup never retro-applies the current visibility policy to a persisted
	// environment, so a tightening must not prevent the service from booting.
	if err := validateState(state); err != nil {
		t.Fatalf("persisted environment must stay structurally readable after tightening: %v", err)
	}
}

// A cycle back edge is an edge like any other and must be checked even though
// its target vertex was already visited.
func TestValidateSelectionRejectsCrossFamilyCycleBackEdge(t *testing.T) {
	catalog := domain.Catalog{
		Revision:           1,
		VisibilityRevision: 1,
		Components: map[string]domain.Component{
			"core-facade":    componentRecord("core-facade", "core", domain.Public),
			"core-engine":    componentRecord("core-engine", "core", domain.Internal),
			"partner-bridge": componentRecord("partner-bridge", "partner", domain.Public),
		},
		Releases: map[string]map[string]domain.Release{
			"core-facade":    {"1.0.0": releaseRecord("core-facade", "1.0.0", map[string]string{"core-engine": "^1.0.0"})},
			"core-engine":    {"1.0.0": releaseRecord("core-engine", "1.0.0", map[string]string{"partner-bridge": "^1.0.0"})},
			"partner-bridge": {"1.0.0": releaseRecord("partner-bridge", "1.0.0", map[string]string{"core-engine": "^1.0.0"})},
		},
	}
	roots := map[string]string{"core-facade": "*"}
	selected := map[string]string{"core-facade": "1.0.0", "core-engine": "1.0.0", "partner-bridge": "1.0.0"}
	err := ValidateSelection(catalog, roots, selected, true)
	if err == nil || !strings.Contains(err.Error(), "partner-bridge") {
		t.Fatalf("expected the partner-bridge cycle back edge to be rejected, got %v", err)
	}

	// With an explicit grant the same cycle is legal.
	granted := catalog
	granted.Components["core-engine"] = componentRecord("core-engine", "core", domain.Internal, "partner-bridge")
	if err := ValidateSelection(granted, roots, selected, true); err != nil {
		t.Fatalf("granted cycle edge was rejected: %v", err)
	}
}

// A release edge recorded while it was legal must not block startup after the
// target policy is tightened; visibility is enforced at decision time instead.
func TestValidateStateKeepsCrossBoundaryRegistrationReadable(t *testing.T) {
	state := &State{
		Schema:   2,
		Revision: 3,
		Catalog: domain.Catalog{
			Revision:           2,
			VisibilityRevision: 1,
			Components: map[string]domain.Component{
				"core-engine":  componentRecord("core-engine", "core", domain.Internal),
				"stranger-app": componentRecord("stranger-app", "", domain.Public),
			},
			Releases: map[string]map[string]domain.Release{
				"core-engine":  {"1.0.0": releaseRecord("core-engine", "1.0.0", nil)},
				"stranger-app": {"1.0.0": releaseRecord("stranger-app", "1.0.0", map[string]string{"core-engine": "*"})},
			},
		},
		Environments: map[string]domain.Environment{},
		Plans:        map[string]domain.Plan{},
		Events:       events(3),
	}
	if err := validateState(state); err != nil {
		t.Fatalf("previously legal registration must stay readable after tightening: %v", err)
	}
}
