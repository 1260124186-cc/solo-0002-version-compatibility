package resolution

import (
	"context"
	"errors"
	"testing"

	"solo-0002-version-compatibility/internal/domain"
)

type releaseSpec struct {
	component string
	version   string
	requires  map[string]string
}

func buildCatalog(t *testing.T, components []domain.Component, releases []releaseSpec) domain.Catalog {
	t.Helper()
	c := domain.Catalog{Revision: 1, Components: map[string]domain.Component{}, Releases: map[string]map[string]domain.Release{}}
	for _, component := range components {
		if component.Visibility == "" {
			component.Visibility = domain.Public
		}
		c.Components[component.ID] = component
		c.Releases[component.ID] = map[string]domain.Release{}
	}
	for _, spec := range releases {
		c.Releases[spec.component][spec.version] = domain.Release{
			ComponentID: spec.component,
			Version:     spec.version,
			Requires:    spec.requires,
			State:       domain.Available,
		}
	}
	return c
}

func faultCode(t *testing.T, err error) string {
	t.Helper()
	var fault *domain.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("expected domain fault, got %v", err)
	}
	return fault.Code
}

var solverUnderTest = Solver{MaxSteps: 1000, MaxNodes: 32}

func TestResolveSameFamilyInternalThroughPublicEntry(t *testing.T) {
	catalog := buildCatalog(t,
		[]domain.Component{
			{ID: "core-facade", Family: "core", Visibility: domain.Public},
			{ID: "core-engine", Family: "core", Visibility: domain.Internal},
		},
		[]releaseSpec{
			{component: "core-engine", version: "1.0.0"},
			{component: "core-facade", version: "1.0.0", requires: map[string]string{"core-engine": "^1.0.0"}},
		},
	)
	result, err := solverUnderTest.Resolve(context.Background(), catalog, map[string]string{"core-facade": "*"})
	if err != nil {
		t.Fatalf("same-family resolution failed: %v", err)
	}
	if result.Resolved["core-engine"] != "1.0.0" {
		t.Fatalf("internal component not resolved transitively: %v", result.Resolved)
	}
}

func TestResolveRootCannotReferenceInternal(t *testing.T) {
	catalog := buildCatalog(t,
		[]domain.Component{{ID: "core-engine", Family: "core", Visibility: domain.Internal}},
		[]releaseSpec{{component: "core-engine", version: "1.0.0"}},
	)
	_, err := solverUnderTest.Resolve(context.Background(), catalog, map[string]string{"core-engine": "*"})
	if code := faultCode(t, err); code != "visibility_denied" {
		t.Fatalf("expected visibility_denied for root edge, got %s", code)
	}
}

func TestResolveAllowedConsumerGrant(t *testing.T) {
	components := []domain.Component{
		{ID: "secret-store", Family: "secret-team", Visibility: domain.Internal, AllowedConsumers: []string{"trusted-app"}},
		{ID: "trusted-app"},
		{ID: "stranger-app"},
	}
	releases := []releaseSpec{
		{component: "secret-store", version: "1.0.0"},
		{component: "trusted-app", version: "1.0.0", requires: map[string]string{"secret-store": "*"}},
		{component: "stranger-app", version: "1.0.0", requires: map[string]string{"secret-store": "*"}},
	}
	catalog := buildCatalog(t, components, releases)

	result, err := solverUnderTest.Resolve(context.Background(), catalog, map[string]string{"trusted-app": "*"})
	if err != nil {
		t.Fatalf("explicitly allowed consumer was rejected: %v", err)
	}
	if result.Resolved["secret-store"] != "1.0.0" {
		t.Fatalf("allowed consumer did not resolve internal component: %v", result.Resolved)
	}

	_, err = solverUnderTest.Resolve(context.Background(), catalog, map[string]string{"stranger-app": "*"})
	if code := faultCode(t, err); code != "visibility_denied" {
		t.Fatalf("expected visibility_denied for consumer without grant, got %v (%v)", code, err)
	}
}

func TestResolveCrossFamilyCycleBackEdge(t *testing.T) {
	components := []domain.Component{
		{ID: "core-facade", Family: "core", Visibility: domain.Public},
		{ID: "core-engine", Family: "core", Visibility: domain.Internal},
		{ID: "partner-bridge", Family: "partner", Visibility: domain.Public},
	}
	releases := []releaseSpec{
		{component: "partner-bridge", version: "1.0.0", requires: map[string]string{"core-engine": "*"}},
		{component: "core-engine", version: "1.0.0", requires: map[string]string{"partner-bridge": "^1.0.0"}},
		{component: "core-facade", version: "1.0.0", requires: map[string]string{"core-engine": "^1.0.0"}},
	}
	catalog := buildCatalog(t, components, releases)

	// The cycle back edge partner-bridge -> core-engine has no grant.
	_, err := solverUnderTest.Resolve(context.Background(), catalog, map[string]string{"core-facade": "*"})
	if code := faultCode(t, err); code != "visibility_denied" {
		t.Fatalf("cross-family cycle back edge was not rejected, got code %s err %v", code, err)
	}

	// Grant the bridge access to the internal component; the cycle is legal.
	engine := catalog.Components["core-engine"]
	engine.AllowedConsumers = []string{"partner-bridge"}
	catalog.Components["core-engine"] = engine
	result, err := solverUnderTest.Resolve(context.Background(), catalog, map[string]string{"core-facade": "*"})
	if err != nil {
		t.Fatalf("granted cycle edge was rejected: %v", err)
	}
	if len(result.Resolved) != 3 {
		t.Fatalf("expected all three cycle members resolved, got %v", result.Resolved)
	}
}

func TestResolveBacktracksAwayFromBlockedRelease(t *testing.T) {
	catalog := buildCatalog(t,
		[]domain.Component{
			{ID: "core-engine", Family: "core", Visibility: domain.Internal},
			{ID: "backtrack-app"},
		},
		[]releaseSpec{
			{component: "core-engine", version: "2.0.0"},
			{component: "core-engine", version: "1.0.0"},
			// 2.0.0 crosses the boundary, 1.0.0 routes nowhere illegal.
			{component: "backtrack-app", version: "2.0.0", requires: map[string]string{"core-engine": "*"}},
			{component: "backtrack-app", version: "1.0.0"},
		},
	)
	result, err := solverUnderTest.Resolve(context.Background(), catalog, map[string]string{"backtrack-app": "*"})
	if err != nil {
		t.Fatalf("solver did not backtrack to a legal release: %v", err)
	}
	if result.Resolved["backtrack-app"] != "1.0.0" {
		t.Fatalf("expected the legal 1.0.0 release, got %v", result.Resolved)
	}
	if _, present := result.Resolved["core-engine"]; present {
		t.Fatalf("internal component must not enter a legal set through a blocked branch")
	}
}
