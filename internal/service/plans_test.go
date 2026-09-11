package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/repository"
)

type testHarness struct {
	t    *testing.T
	svc  *Service
	repo *repository.Repository
	dir  string
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	dir := t.TempDir()
	repo, err := repository.Open(dir)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return &testHarness{t: t, svc: New(repo, 50000), repo: repo, dir: dir}
}

func (h *testHarness) component(id string) {
	h.t.Helper()
	if _, err := h.svc.CreateComponent(context.Background(), domain.ComponentInput{ID: id, Name: id}); err != nil {
		h.t.Fatalf("create component %s: %v", id, err)
	}
}

func (h *testHarness) release(id, version string, requires map[string]string) {
	h.t.Helper()
	if _, err := h.svc.AddRelease(context.Background(), id, domain.ReleaseInput{Version: version, Requires: requires}); err != nil {
		h.t.Fatalf("add release %s@%s: %v", id, version, err)
	}
}

func (h *testHarness) environment(id string, roots map[string]string) domain.Environment {
	h.t.Helper()
	env, err := h.svc.CreateEnvironment(context.Background(), domain.EnvironmentInput{ID: id, Name: id, Roots: roots})
	if err != nil {
		h.t.Fatalf("create environment %s: %v", id, err)
	}
	return env
}

func (h *testHarness) plan(envID string, base uint64, roots map[string]string) domain.Plan {
	h.t.Helper()
	plan, err := h.svc.CreatePlan(context.Background(), domain.PlanInput{
		EnvironmentID: envID, BaseRevision: base, Roots: roots, Reason: "boundary test",
	})
	if err != nil {
		h.t.Fatalf("create plan: %v", err)
	}
	return plan
}

func (h *testHarness) validate(planID string, revision uint64) domain.Plan {
	h.t.Helper()
	plan, err := h.svc.ValidatePlan(context.Background(), planID, revision)
	if err != nil {
		h.t.Fatalf("validate plan %s at revision %d: %v", planID, revision, err)
	}
	return plan
}

func (h *testHarness) apply(planID string, revision uint64) AppliedResult {
	h.t.Helper()
	result, err := h.svc.ApplyPlan(context.Background(), planID, revision)
	if err != nil {
		h.t.Fatalf("apply plan %s at revision %d: %v", planID, revision, err)
	}
	return result
}

func (h *testHarness) eventActions() []string {
	h.t.Helper()
	page, err := h.svc.Events(context.Background(), 0, 200, "")
	if err != nil {
		h.t.Fatalf("list events: %v", err)
	}
	actions := make([]string, 0, len(page.Items))
	for _, event := range page.Items {
		actions = append(actions, event.EntityID+":"+event.Action)
	}
	return actions
}

func expectFault(t *testing.T, err error, code string) {
	t.Helper()
	var fault *domain.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("expected domain fault %q, got %T: %v", code, err, err)
	}
	if fault.Code != code {
		t.Fatalf("expected fault code %q, got %q: %s", code, fault.Code, fault.Detail)
	}
}

func (h *testHarness) setupUpgradableStack() (string, map[string]string, map[string]string) {
	h.component("core")
	h.component("engine")
	h.release("core", "1.0.0", nil)
	h.release("core", "2.0.0", nil)
	h.release("engine", "1.0.0", map[string]string{"core": "^1.0.0"})
	h.release("engine", "2.0.0", map[string]string{"core": "^2.0.0"})
	oldRoots := map[string]string{"engine": "1.0.0"}
	newRoots := map[string]string{"engine": "2.0.0"}
	return "env-a", oldRoots, newRoots
}

func TestPlanStaleRevisionAndRepeatedApplyRejected(t *testing.T) {
	h := newTestHarness(t)
	envID, oldRoots, newRoots := h.setupUpgradableStack()
	h.environment(envID, oldRoots)

	// Plan creation requires the current environment revision.
	if _, err := h.svc.CreatePlan(context.Background(), domain.PlanInput{
		EnvironmentID: envID, BaseRevision: 99, Roots: newRoots, Reason: "stale",
	}); err == nil {
		t.Fatal("plan creation against an old environment revision must fail")
	}

	plan := h.plan(envID, 1, newRoots)
	if plan.State != domain.Draft || plan.Revision != 1 {
		t.Fatalf("new plan must be draft revision 1, got %s rev %d", plan.State, plan.Revision)
	}

	// A zero revision is an input error, not a state conflict.
	if _, err := h.svc.ValidatePlan(context.Background(), plan.ID, 0); err == nil {
		t.Fatal("validation with revision 0 must fail")
	}
	// A draft plan cannot be applied directly.
	if _, err := h.svc.ApplyPlan(context.Background(), plan.ID, 1); err == nil {
		t.Fatal("applying a draft plan must fail")
	}
	// Neither failed transition changed the plan.
	if got, _ := h.svc.Plan(context.Background(), plan.ID); got.State != domain.Draft || got.Revision != 1 {
		t.Fatalf("failed transitions mutated the draft: state=%s rev=%d", got.State, got.Revision)
	}

	ready := h.validate(plan.ID, 1)
	if ready.State != domain.Ready || ready.Revision != 2 || len(ready.Changes) != 2 {
		t.Fatalf("validation did not produce the expected ready plan: %+v", ready)
	}

	// Old plan revision is rejected, even though the plan content would apply.
	if _, err := h.svc.ApplyPlan(context.Background(), plan.ID, 1); err == nil {
		t.Fatal("apply with the stale plan revision must fail")
	}
	if env, _ := h.svc.Environment(context.Background(), envID); env.Revision != 1 {
		t.Fatalf("stale apply must not move the environment, revision=%d", env.Revision)
	}
	if got, _ := h.svc.Plan(context.Background(), plan.ID); got.State != domain.Ready || got.Revision != 2 {
		t.Fatalf("rejected apply mutated the plan: state=%s rev=%d", got.State, got.Revision)
	}

	applied := h.apply(plan.ID, 2)
	if applied.Environment.Revision != 2 || applied.Environment.Resolved["core"] != "2.0.0" {
		t.Fatalf("apply result wrong: %+v", applied.Environment)
	}
	if applied.Plan.State != domain.Applied || applied.Plan.Revision != 3 {
		t.Fatalf("applied plan wrong: %+v", applied.Plan)
	}

	// A plan can only be applied once: repeating with the fresh revision fails.
	if _, err := h.svc.ApplyPlan(context.Background(), plan.ID, 3); err == nil {
		t.Fatal("repeated apply must fail")
	}
	// Reusing the old revision fails too, and the applied state is immutable.
	if _, err := h.svc.ApplyPlan(context.Background(), plan.ID, 2); err == nil {
		t.Fatal("repeated apply with an old revision must fail")
	}
	got, _ := h.svc.Plan(context.Background(), plan.ID)
	if got.State != domain.Applied || got.Revision != 3 {
		t.Fatalf("rejected repeat apply mutated the plan: state=%s rev=%d", got.State, got.Revision)
	}
	if env, _ := h.svc.Environment(context.Background(), envID); env.Revision != 2 {
		t.Fatalf("rejected repeat apply mutated the environment revision: %d", env.Revision)
	}

	// Exactly one apply event exists for this plan.
	var applies int
	for _, action := range h.eventActions() {
		if action == plan.ID+":applied" {
			applies++
		}
	}
	if applies != 1 {
		t.Fatalf("expected exactly one applied event, got %d (%v)", applies, h.eventActions())
	}
}

func TestCompetingPlansOnlyLegalOneCommits(t *testing.T) {
	h := newTestHarness(t)
	envID, oldRoots, newRoots := h.setupUpgradableStack()
	h.environment(envID, oldRoots)

	// Two plans race for the same environment, both based on revision 1.
	planA := h.plan(envID, 1, newRoots)
	planB := h.plan(envID, 1, map[string]string{"engine": "2.0.0"})
	h.validate(planA.ID, 1)
	h.validate(planB.ID, 1)

	// Plan A commits first and moves the environment to revision 2.
	appliedA := h.apply(planA.ID, 2)
	if appliedA.Environment.Revision != 2 {
		t.Fatalf("environment should advance to revision 2, got %d", appliedA.Environment.Revision)
	}

	// Plan B still passes plan-revision checks (it is a ready, unapplied plan),
	// but its captured base revision no longer matches the environment.
	_, err := h.svc.ApplyPlan(context.Background(), planB.ID, 2)
	expectFault(t, err, "conflict")

	gotB, _ := h.svc.Plan(context.Background(), planB.ID)
	if gotB.State != domain.Ready || gotB.Revision != 2 {
		t.Fatalf("rejected competitor must stay ready revision 2, got %s rev %d", gotB.State, gotB.Revision)
	}
	env, _ := h.svc.Environment(context.Background(), envID)
	if env.Revision != 2 || env.Resolved["engine"] != "2.0.0" {
		t.Fatalf("environment must retain plan A's commit: %+v", env)
	}

	var applies int
	for _, event := range h.eventActions() {
		if event == planA.ID+":applied" {
			applies++
		}
		if event == planB.ID+":applied" {
			t.Fatal("rejected competitor must not record an applied event")
		}
	}
	if applies != 1 {
		t.Fatalf("exactly one plan may commit, counted %d applied events", applies)
	}

	// The loser remains usable through its normal lifecycle: it can be cancelled.
	cancelled, err := h.svc.CancelPlan(context.Background(), planB.ID, 2)
	if err != nil {
		t.Fatalf("cancelling the losing plan: %v", err)
	}
	if cancelled.State != domain.Cancelled {
		t.Fatalf("competitor should cancel, got %s", cancelled.State)
	}
}

func TestAppliedPlanSurvivesRestart(t *testing.T) {
	h := newTestHarness(t)
	envID, oldRoots, newRoots := h.setupUpgradableStack()
	h.environment(envID, oldRoots)
	plan := h.plan(envID, 1, newRoots)
	h.validate(plan.ID, 1)
	applied := h.apply(plan.ID, 2)
	eventsBefore := h.eventActions()
	if err := h.repo.Close(); err != nil {
		t.Fatalf("close repository: %v", err)
	}

	// Simulate a process restart against the same data directory.
	reopened, err := repository.Open(h.dir)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	svc := New(reopened, 50000)

	gotPlan, err := svc.Plan(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("load plan after restart: %v", err)
	}
	if gotPlan.State != domain.Applied || gotPlan.Revision != applied.Plan.Revision {
		t.Fatalf("plan not preserved across restart: %+v", gotPlan)
	}
	gotEnv, err := svc.Environment(context.Background(), envID)
	if err != nil {
		t.Fatalf("load environment after restart: %v", err)
	}
	if gotEnv.Revision != 2 || gotEnv.Resolved["core"] != "2.0.0" || gotEnv.Resolved["engine"] != "2.0.0" {
		t.Fatalf("environment not preserved across restart: %+v", gotEnv)
	}
	page, err := svc.Events(context.Background(), 0, 200, "")
	if err != nil {
		t.Fatalf("load events after restart: %v", err)
	}
	var actionsAfter []string
	for _, event := range page.Items {
		actionsAfter = append(actionsAfter, event.EntityID+":"+event.Action)
	}
	if len(actionsAfter) != len(eventsBefore) {
		t.Fatalf("event count changed across restart: %v vs %v", actionsAfter, eventsBefore)
	}
	for i := range eventsBefore {
		if actionsAfter[i] != eventsBefore[i] {
			t.Fatalf("event %d changed across restart: %q vs %q", i, actionsAfter[i], eventsBefore[i])
		}
	}
}

func TestApplyWriteFailureLeavesMemoryEventsAndLaterCommitsIntact(t *testing.T) {
	h := newTestHarness(t)
	envID, oldRoots, newRoots := h.setupUpgradableStack()
	h.environment(envID, oldRoots)
	plan := h.plan(envID, 1, newRoots)
	h.validate(plan.ID, 1)
	eventsBefore := h.eventActions()

	// Sabotage persistence: a non-empty directory at the state file path makes
	// the atomic rename fail. Memory must not move ahead of disk.
	statePath := filepath.Join(h.dir, "state.json")
	if err := os.Remove(statePath); err != nil {
		t.Fatalf("remove state file: %v", err)
	}
	if err := os.Mkdir(statePath, 0700); err != nil {
		t.Fatalf("create blocking directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(statePath, "blocker"), []byte("x"), 0600); err != nil {
		t.Fatalf("plant blocker: %v", err)
	}
	if _, err := h.svc.ApplyPlan(context.Background(), plan.ID, 2); err == nil {
		t.Fatal("apply must surface the persistence error")
	}
	_ = os.RemoveAll(statePath)

	// Plan, environment and the in-memory event log stay at their pre-write state.
	got, _ := h.svc.Plan(context.Background(), plan.ID)
	if got.State != domain.Ready || got.Revision != 2 {
		t.Fatalf("failed write must not advance the plan: state=%s rev=%d", got.State, got.Revision)
	}
	env, _ := h.svc.Environment(context.Background(), envID)
	if env.Revision != 1 {
		t.Fatalf("failed write must not advance the environment: rev=%d", env.Revision)
	}
	eventsAfter := h.eventActions()
	if len(eventsAfter) != len(eventsBefore) {
		t.Fatalf("failed write altered the event log: %v vs %v", eventsAfter, eventsBefore)
	}
	for i := range eventsBefore {
		if eventsAfter[i] != eventsBefore[i] {
			t.Fatalf("event %d changed after failed write: %q vs %q", i, eventsAfter[i], eventsBefore[i])
		}
	}

	// Memory stayed consistent with the last committed revision, so the retry commits.
	applied := h.apply(plan.ID, 2)
	if applied.Plan.State != domain.Applied || applied.Environment.Revision != 2 {
		t.Fatalf("retry after a failed write did not commit cleanly: %+v", applied)
	}
}

func TestFailedValidationKeepsDraftAndEmitsNoEvent(t *testing.T) {
	h := newTestHarness(t)
	envID, oldRoots, _ := h.setupUpgradableStack()
	h.environment(envID, oldRoots)
	eventsBefore := len(h.eventActions())

	// engine 9.0.0 does not exist: the plan is creatable (constraints parse and
	// the component exists) but validation cannot solve it.
	plan := h.plan(envID, 1, map[string]string{"engine": "9.0.0"})
	if _, err := h.svc.ValidatePlan(context.Background(), plan.ID, 1); err == nil {
		t.Fatal("validation of an unsatisfiable plan must fail")
	}

	got, _ := h.svc.Plan(context.Background(), plan.ID)
	if got.State != domain.Draft || got.Revision != 1 {
		t.Fatalf("failed validation must keep draft revision 1, got %s rev %d", got.State, got.Revision)
	}
	if len(got.Resolved) != 0 || len(got.Changes) != 0 {
		t.Fatalf("failed validation must not attach a resolution: %+v", got)
	}
	if actions := h.eventActions(); len(actions) != eventsBefore+1 {
		t.Fatalf("failed validation must not record an event, got %v", actions)
	}
}

func TestCatalogChangeInvalidatesReadyPlanUntilRevalidated(t *testing.T) {
	h := newTestHarness(t)
	envID, oldRoots, newRoots := h.setupUpgradableStack()
	h.environment(envID, oldRoots)
	plan := h.plan(envID, 1, newRoots)
	ready := h.validate(plan.ID, 1)

	// A catalog write after validation invalidates the ready plan's catalog revision.
	h.release("core", "3.0.0", nil)
	if _, err := h.svc.ApplyPlan(context.Background(), plan.ID, ready.Revision); err == nil {
		t.Fatal("apply against a changed catalog must fail")
	}
	if env, _ := h.svc.Environment(context.Background(), envID); env.Revision != 1 {
		t.Fatalf("stale catalog apply must not move the environment, rev=%d", env.Revision)
	}

	// Revalidation captures the new catalog revision and the plan applies.
	ready = h.validate(plan.ID, ready.Revision)
	applied := h.apply(plan.ID, ready.Revision)
	if applied.Environment.Revision != 2 {
		t.Fatalf("revalidated plan should apply, env rev=%d", applied.Environment.Revision)
	}
}
