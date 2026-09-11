package resolution

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"solo-0002-version-compatibility/internal/domain"
)

type catalogBuilder struct {
	catalog domain.Catalog
}

func newTestCatalog() *catalogBuilder {
	return &catalogBuilder{catalog: domain.Catalog{
		Components: make(map[string]domain.Component),
		Releases:   make(map[string]map[string]domain.Release),
	}}
}

func (b *catalogBuilder) component(id string) *catalogBuilder {
	if _, exists := b.catalog.Components[id]; !exists {
		b.catalog.Components[id] = domain.Component{ID: id, Name: id}
		b.catalog.Releases[id] = make(map[string]domain.Release)
		b.catalog.Revision++
	}
	return b
}

func (b *catalogBuilder) release(id, version string, requires map[string]string, state ...string) *catalogBuilder {
	b.component(id)
	for dep := range requires {
		b.component(dep)
	}
	s := domain.Available
	if len(state) > 0 {
		s = state[0]
	}
	b.catalog.Releases[id][version] = domain.Release{
		ComponentID: id,
		Version:     version,
		Requires:    requires,
		State:       s,
	}
	b.catalog.Revision++
	return b
}

func (b *catalogBuilder) build() domain.Catalog {
	return b.catalog
}

func faultCode(t *testing.T, err error) string {
	t.Helper()
	var fault *domain.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("expected *domain.Fault, got %T: %v", err, err)
	}
	return fault.Code
}

func resolve(t *testing.T, solver Solver, catalog domain.Catalog, roots map[string]string) domain.Resolution {
	t.Helper()
	result, err := solver.Resolve(context.Background(), catalog, roots)
	if err != nil {
		t.Fatalf("Resolve(%v) returned unexpected error: %v", roots, err)
	}
	return result
}

func TestTransitiveResolution(t *testing.T) {
	catalog := newTestCatalog().
		release("lib-a", "1.0.0", nil).
		release("lib-a", "1.5.0", nil).
		release("lib-b", "1.0.0", map[string]string{"lib-a": "^1.0.0"}).
		release("lib-b", "2.0.0", map[string]string{"lib-a": "^1.5.0"}).
		release("app", "1.0.0", map[string]string{"lib-b": "^2.0.0"}).
		build()

	result := resolve(t, Solver{MaxSteps: 50000}, catalog, map[string]string{"app": "*"})

	if got := result.Resolved; got["app"] != "1.0.0" || got["lib-b"] != "2.0.0" || got["lib-a"] != "1.5.0" {
		t.Fatalf("transitive selection wrong: %v", got)
	}
	wantEdges := map[[2]string]bool{
		{"app", "lib-b"}:   true,
		{"lib-b", "lib-a"}: true,
	}
	if len(result.Edges) != len(wantEdges) {
		t.Fatalf("expected %d dependency edges, got %d: %v", len(wantEdges), len(result.Edges), result.Edges)
	}
	for _, edge := range result.Edges {
		if !wantEdges[[2]string{edge.From, edge.To}] {
			t.Errorf("unexpected edge %s -> %s (%s)", edge.From, edge.To, edge.Constraint)
		}
	}
	if result.CatalogRevision != catalog.Revision {
		t.Errorf("catalog_revision = %d, want %d", result.CatalogRevision, catalog.Revision)
	}
	if result.Steps <= 0 {
		t.Errorf("steps must be positive, got %d", result.Steps)
	}
}

func TestBacktrackingPicksCompatibleOlderRelease(t *testing.T) {
	catalog := newTestCatalog().
		release("core", "1.0.0", nil).
		release("core", "2.0.0", nil).
		release("engine", "1.0.0", map[string]string{"core": "^1.0.0"}).
		release("engine", "2.0.0", map[string]string{"core": "^2.0.0"}).
		build()

	// The newest engine pulls core 2.x, but the root pins core 1.x: the
	// solver must backtrack to engine 1.0.0 instead of failing.
	result := resolve(t, Solver{MaxSteps: 50000}, catalog,
		map[string]string{"engine": "*", "core": "1.0.0"})

	if result.Resolved["engine"] != "1.0.0" || result.Resolved["core"] != "1.0.0" {
		t.Fatalf("backtracking result wrong: %v", result.Resolved)
	}
}

func TestCompatibleCycleResolves(t *testing.T) {
	catalog := newTestCatalog().
		release("cyc-alpha", "1.0.0", map[string]string{"cyc-beta": "~1.0.0"}).
		release("cyc-beta", "1.0.0", map[string]string{"cyc-alpha": ">=1.0.0 <2.0.0"}).
		build()

	result := resolve(t, Solver{MaxSteps: 50000}, catalog, map[string]string{"cyc-alpha": "*"})
	if len(result.Resolved) != 2 {
		t.Fatalf("expected both cycle members resolved, got %v", result.Resolved)
	}
	if result.Resolved["cyc-alpha"] != "1.0.0" || result.Resolved["cyc-beta"] != "1.0.0" {
		t.Fatalf("cycle versions wrong: %v", result.Resolved)
	}
	if len(result.Edges) != 2 {
		t.Fatalf("expected both cycle edges, got %v", result.Edges)
	}
}

func TestNoSolutionReturnsConflictEvidence(t *testing.T) {
	catalog := newTestCatalog().
		release("core", "1.0.0", nil).
		release("core", "2.0.0", nil).
		release("engine", "1.0.0", map[string]string{"core": "^1.0.0"}).
		release("engine", "2.0.0", map[string]string{"core": "^2.0.0"}).
		build()

	_, err := Solver{MaxSteps: 50000}.Resolve(context.Background(), catalog,
		map[string]string{"engine": "2.0.0", "core": "^1.0.0"})
	var fault *domain.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("expected *domain.Fault, got %T: %v", err, err)
	}
	if fault.Code != "no_solution" {
		t.Fatalf("expected no_solution, got %s: %s", fault.Code, fault.Detail)
	}
	if len(fault.Conflicts) == 0 {
		t.Fatalf("conflict evidence must accompany an unsatisfiable request")
	}
	if len(fault.Conflicts) > 8 {
		t.Fatalf("conflict evidence is capped at 8 entries, got %d", len(fault.Conflicts))
	}
	for _, item := range fault.Conflicts {
		if item == "" {
			t.Errorf("conflict evidence entry must not be empty")
		}
	}
}

func TestStepBudgetExhausted(t *testing.T) {
	catalog := newTestCatalog().release("solo", "1.0.0", nil).build()

	// Even a single selection needs more than one recursive search step.
	_, err := Solver{MaxSteps: 1, MaxNodes: 128}.Resolve(context.Background(), catalog,
		map[string]string{"solo": "*"})
	if code := faultCode(t, err); code != "limit_exceeded" {
		t.Fatalf("expected limit_exceeded after budget exhaustion, got %s", code)
	}

	// A three-node chain cannot be completed within a two-step budget either,
	// proving the counter spans recursive choices rather than requests.
	chain := newTestCatalog().
		release("c0", "1.0.0", nil).
		release("c1", "1.0.0", map[string]string{"c0": "*"}).
		release("c2", "1.0.0", map[string]string{"c1": "*"}).
		build()
	_, err = Solver{MaxSteps: 2, MaxNodes: 128}.Resolve(context.Background(), chain,
		map[string]string{"c2": "*"})
	if code := faultCode(t, err); code != "limit_exceeded" {
		t.Fatalf("expected limit_exceeded on a chain exceeding budget, got %s", code)
	}
}

func TestComponentNodeBudgetExceeded(t *testing.T) {
	// Default component cap is 128; a chain of 129 distinct components fails.
	builder := newTestCatalog()
	builder.release("n000", "1.0.0", nil)
	for i := 1; i < 129; i++ {
		builder.release(fmt.Sprintf("n%03d", i), "1.0.0",
			map[string]string{fmt.Sprintf("n%03d", i-1): "*"})
	}
	catalog := builder.build()

	_, err := Solver{MaxSteps: 50000}.Resolve(context.Background(), catalog,
		map[string]string{"n128": "*"})
	if code := faultCode(t, err); code != "limit_exceeded" {
		t.Fatalf("expected limit_exceeded past the 128 component limit, got %s: %v", code, err)
	}

	// An explicit small cap fails earlier, with step budget still ample.
	small := newTestCatalog().
		release("c0", "1.0.0", nil).
		release("c1", "1.0.0", map[string]string{"c0": "*"}).
		release("c2", "1.0.0", map[string]string{"c1": "*"}).
		build()
	_, err = Solver{MaxSteps: 50000, MaxNodes: 2}.Resolve(context.Background(), small,
		map[string]string{"c2": "*"})
	if code := faultCode(t, err); code != "limit_exceeded" {
		t.Fatalf("expected limit_exceeded past the explicit 2 component limit, got %s", code)
	}
}

func TestResolutionSkipsWithdrawnReleases(t *testing.T) {
	catalog := newTestCatalog().
		release("core", "1.0.0", nil).
		release("core", "2.0.0", nil, domain.Withdrawn).
		build()

	result := resolve(t, Solver{MaxSteps: 50000}, catalog, map[string]string{"core": "*"})
	if result.Resolved["core"] != "1.0.0" {
		t.Fatalf("withdrawn release must not be selected, got %v", result.Resolved)
	}
}

func TestMissingComponentRoot(t *testing.T) {
	catalog := newTestCatalog().component("known").build()
	_, err := Solver{MaxSteps: 50000}.Resolve(context.Background(), catalog,
		map[string]string{"unknown": "*"})
	if code := faultCode(t, err); code != "not_found" {
		t.Fatalf("expected not_found for missing root component, got %s", code)
	}
}

func TestResolutionIsDeterministic(t *testing.T) {
	catalog := newTestCatalog().
		release("core", "1.0.0", nil).
		release("core", "2.0.0", nil).
		release("engine", "1.0.0", map[string]string{"core": "^1.0.0"}).
		release("engine", "2.0.0", map[string]string{"core": "^2.0.0"}).
		release("app", "1.0.0", map[string]string{"engine": "*"}).
		build()
	roots := map[string]string{"app": "*"}

	first := resolve(t, Solver{MaxSteps: 50000}, catalog, roots)
	second := resolve(t, Solver{MaxSteps: 50000}, catalog, roots)

	if fmt.Sprint(first.Resolved) != fmt.Sprint(second.Resolved) {
		t.Fatalf("resolved sets differ across runs: %v vs %v", first.Resolved, second.Resolved)
	}
	if fmt.Sprint(first.Edges) != fmt.Sprint(second.Edges) {
		t.Fatalf("dependency edges differ across runs: %v vs %v", first.Edges, second.Edges)
	}
}
