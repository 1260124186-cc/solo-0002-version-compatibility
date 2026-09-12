package service

import (
	"context"
	"errors"
	"testing"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
)

func newTestService(t *testing.T) (*Service, *repository.Repository) {
	t.Helper()
	repo, err := repository.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return New(repo, 50000), repo
}

func mustComponent(t *testing.T, s *Service, id, family, visibility string, consumers ...string) {
	t.Helper()
	_, err := s.CreateComponent(context.Background(), domain.ComponentInput{ID: id, Name: id, Family: family, Visibility: visibility, AllowedConsumers: consumers})
	if err != nil {
		t.Fatalf("create component %s: %v", id, err)
	}
}

func reqInput(requires map[string]string) map[string]domain.RequirementInput {
	result := make(map[string]domain.RequirementInput, len(requires))
	for id, constraint := range requires {
		result[id] = domain.RequirementInput{Constraint: constraint, Visibility: domain.Public}
	}
	return result
}

func mustRelease(t *testing.T, s *Service, id, version string, requires map[string]string) {
	t.Helper()
	if _, err := s.AddRelease(context.Background(), id, domain.ReleaseInput{Version: version, Requires: reqInput(requires)}); err != nil {
		t.Fatalf("add release %s@%s: %v", id, version, err)
	}
}

func expectCode(t *testing.T, err error, code string) {
	t.Helper()
	var fault *domain.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("expected fault %s, got %v", code, err)
	}
	if fault.Code != code {
		t.Fatalf("expected fault %s, got %s (%s)", code, fault.Code, fault.Detail)
	}
}

func TestAddReleaseEnforcesVisibility(t *testing.T) {
	s, _ := newTestService(t)
	mustComponent(t, s, "core-engine", "core", domain.Internal)
	mustComponent(t, s, "core-facade", "core", domain.Public)
	mustComponent(t, s, "trusted-app", "", domain.Public)
	mustComponent(t, s, "stranger-app", "", domain.Public)
	mustRelease(t, s, "core-engine", "1.0.0", nil)

	// Same family may reach the internal component.
	mustRelease(t, s, "core-facade", "1.0.0", map[string]string{"core-engine": "^1.0.0"})

	// The trusted app is not on the allow list yet.
	_, err := s.AddRelease(context.Background(), "trusted-app", domain.ReleaseInput{Version: "1.0.0", Requires: reqInput(map[string]string{"core-engine": "*"})})
	expectCode(t, err, "visibility_denied")

	// Cross-family access is rejected at registration.
	_, err = s.AddRelease(context.Background(), "stranger-app", domain.ReleaseInput{Version: "1.0.0", Requires: reqInput(map[string]string{"core-engine": "*"})})
	expectCode(t, err, "visibility_denied")
}

func TestInternalComponentRequiresFamilyAndUnknownGrant(t *testing.T) {
	s, _ := newTestService(t)
	_, err := s.CreateComponent(context.Background(), domain.ComponentInput{ID: "bad-core", Name: "bad", Visibility: domain.Internal})
	expectCode(t, err, "invalid_input")
	_, err = s.CreateComponent(context.Background(), domain.ComponentInput{ID: "bad-core", Name: "bad", Family: "core", Visibility: domain.Internal, AllowedConsumers: []string{"ghost-app"}})
	expectCode(t, err, "not_found")
}

func TestEndToEndFamilyEntryPointAndRootRejection(t *testing.T) {
	s, _ := newTestService(t)
	mustComponent(t, s, "core-engine", "core", domain.Internal)
	mustComponent(t, s, "core-facade", "core", domain.Public)
	mustRelease(t, s, "core-engine", "1.0.0", nil)
	mustRelease(t, s, "core-facade", "1.0.0", map[string]string{"core-engine": "^1.0.0"})

	// Internal component is reachable through the public family entry point.
	result, err := s.Resolve(context.Background(), domain.ResolutionInput{Roots: map[string]string{"core-facade": "*"}})
	if err != nil {
		t.Fatalf("resolve entry point: %v", err)
	}
	if result.Resolved["core-engine"] != "1.0.0" {
		t.Fatalf("internal component not reached transitively: %v", result.Resolved)
	}

	// A root request cannot name the internal component directly.
	_, err = s.Resolve(context.Background(), domain.ResolutionInput{Roots: map[string]string{"core-engine": "*"}})
	expectCode(t, err, "visibility_denied")

	// The same rule applies to initial environment creation.
	_, err = s.CreateEnvironment(context.Background(), domain.EnvironmentInput{ID: "staging", Name: "staging", Roots: map[string]string{"core-engine": "*"}})
	expectCode(t, err, "visibility_denied")
}

func TestPlanValidationRejectsInternalRootAndAppliesViaFacade(t *testing.T) {
	s, _ := newTestService(t)
	mustComponent(t, s, "core-engine", "core", domain.Internal)
	mustComponent(t, s, "core-facade", "core", domain.Public)
	mustRelease(t, s, "core-engine", "1.0.0", nil)
	mustRelease(t, s, "core-facade", "1.0.0", map[string]string{"core-engine": "^1.0.0"})

	env, err := s.CreateEnvironment(context.Background(), domain.EnvironmentInput{ID: "staging", Name: "staging", Roots: map[string]string{"core-facade": "1.0.0"}})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	if env.VisibilityRevision == nil {
		t.Fatalf("environment must record the visibility revision")
	}

	// Drafting a plan that names an internal root is allowed; validation rejects it.
	plan, err := s.CreatePlan(context.Background(), domain.PlanInput{EnvironmentID: "staging", BaseRevision: 1, Roots: map[string]string{"core-engine": "*"}, Reason: "bypass facade"})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	_, err = s.ValidatePlan(context.Background(), plan.ID, plan.Revision)
	expectCode(t, err, "visibility_denied")

	// A legal plan validates and applies, and re-checks visibility at apply time.
	plan, err = s.CreatePlan(context.Background(), domain.PlanInput{EnvironmentID: "staging", BaseRevision: 1, Roots: map[string]string{"core-facade": "*"}, Reason: "through facade"})
	if err != nil {
		t.Fatalf("create legal plan: %v", err)
	}
	ready, err := s.ValidatePlan(context.Background(), plan.ID, plan.Revision)
	if err != nil {
		t.Fatalf("validate legal plan: %v", err)
	}
	if ready.VisibilityRevision == nil || ready.Resolved["core-engine"] != "1.0.0" {
		t.Fatalf("ready plan must record policy revision and internal selection: %+v", ready)
	}
	applied, err := s.ApplyPlan(context.Background(), plan.ID, ready.Revision)
	if err != nil {
		t.Fatalf("apply plan: %v", err)
	}
	if applied.Environment.Resolved["core-engine"] != "1.0.0" {
		t.Fatalf("environment did not retain internal selection: %v", applied.Environment.Resolved)
	}
}

func TestAllowedConsumerEndToEndCycle(t *testing.T) {
	s, _ := newTestService(t)
	mustComponent(t, s, "core-facade", "core", domain.Public)
	mustComponent(t, s, "core-engine", "core", domain.Internal)
	mustComponent(t, s, "partner-bridge", "partner", domain.Public)
	mustRelease(t, s, "core-engine", "1.0.0", map[string]string{"partner-bridge": "^1.0.0"})

	// Registering the ungranted cycle back edge is rejected at registration, so
	// no release can ever carry an illegal edge into a resolution.
	_, err := s.AddRelease(context.Background(), "partner-bridge", domain.ReleaseInput{Version: "1.0.0", Requires: reqInput(map[string]string{"core-engine": "^1.0.0"})})
	expectCode(t, err, "visibility_denied")

	// A fresh catalog granting the bridge access resolves the whole cycle. The
	// granted consumer component must exist before the internal component is
	// created with it on its allow list.
	s2, _ := newTestService(t)
	mustComponent(t, s2, "core-facade", "core", domain.Public)
	mustComponent(t, s2, "partner-bridge", "partner", domain.Public)
	mustComponent(t, s2, "core-engine", "core", domain.Internal, "partner-bridge")
	mustRelease(t, s2, "core-engine", "1.0.0", map[string]string{"partner-bridge": "^1.0.0"})
	mustRelease(t, s2, "partner-bridge", "1.0.0", map[string]string{"core-engine": "^1.0.0"})
	mustRelease(t, s2, "core-facade", "1.0.0", map[string]string{"core-engine": "^1.0.0"})
	env, err := s2.CreateEnvironment(context.Background(), domain.EnvironmentInput{ID: "staging", Name: "staging", Roots: map[string]string{"core-facade": "1.0.0"}})
	if err != nil {
		t.Fatalf("granted cross-family cycle should resolve: %v", err)
	}
	if len(env.Resolved) != 3 {
		t.Fatalf("expected the full cycle selected, got %v", env.Resolved)
	}
}

// A grant recorded on the component is immutable at release time; tightening is
// exercised by the repository fixture tests, which also prove the historical
// environment stays readable and new resolutions are rejected at decision time.
