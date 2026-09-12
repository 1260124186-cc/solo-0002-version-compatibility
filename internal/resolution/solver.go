package resolution

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"solo-0002-version-compatibility/internal/domain"
	"solo-0002-version-compatibility/internal/semver"
)

type Solver struct {
	MaxSteps  int
	MaxNodes  int
	ResumeKey []byte
}

// evidence is one requirement observed to fail (or be exhausted) during the
// deterministic search. It survives continuation so the final no_solution
// evidence is identical to a one-shot search.
type evidence struct {
	From       string `json:"f"`
	Target     string `json:"t"`
	Constraint string `json:"c"`
}

// frame mirrors one level of the recursive solve.
//
// The recursive implementation is:
//
//	solve(selected):
//	  needs := requirements(selected)
//	  pick the first unresolved component (or detect a clash / completion)
//	  for each matching candidate, in deterministic order:
//	    next := copy(selected); next[target] = choice
//	    if result := solve(next); result != nil { return result }
//	  explain(target); return nil
//
// A frame stores that loop position. Base is this call's immutable "selected"
// argument; the frame does not mutate it while trying candidates. Target is the
// component chosen on entry; Cursor is the candidate index last tried.
//
// The top frame is "active". Two phases, distinguished by Enter:
//   - Enter=true: run the entry check (one step): compute needs over Base,
//     detect completion/clash, otherwise pick Target and try its first
//     candidate, then push an Enter=true child whose Base is Base+choice.
//   - Enter=false: a child subtree just returned nil. Try the next candidate
//     for the same Target (Cursor advances) with no new step, exactly like the
//     recursive for-loop resuming after solve(next) == nil.
//
// Precharged marks an entering frame whose entry step was already charged by
// the page that paused on the expansion boundary; resume runs its entry logic
// without charging twice.
type frame struct {
	Target     string               `json:"t"`
	Cursor     int                  `json:"c"`
	Enter      bool                 `json:"e"`
	Precharged bool                 `json:"p,omitempty"`
	Base       map[string]candidate `json:"-"`
}

// continuationToken is the self-contained, signed resume state. Only target and
// cursor are persisted; each frame's Base selection view is deterministically
// replayed from the catalog during restore.
type continuationToken struct {
	Version    int               `json:"v"`
	Revision   uint64            `json:"rev"`
	Print      string            `json:"fp"`
	Roots      map[string]string `json:"roots"`
	Frames     []frame           `json:"frames"`
	Steps      int               `json:"steps"`
	Expansions int               `json:"exp"`
	Conflicts  []evidence        `json:"conflicts"`
	MAC        string            `json:"mac"`
}

type search struct {
	ctx         context.Context
	catalog     map[string][]candidate
	roots       map[string]semver.Constraint
	frames      []frame
	steps       int
	expansions  int
	limit       int
	globalSteps int
	maxNodes    int
	pause       bool
	conflicts   []evidence
}

// Resolve keeps the non-resumable entry point used by environment and plan
// workflows. The configured step limit remains a hard error for these callers.
func (s Solver) Resolve(ctx context.Context, catalog domain.Catalog, roots map[string]string) (domain.Resolution, error) {
	return s.ResolveRequest(ctx, catalog, domain.ResolutionInput{Roots: roots})
}

// ResolveRequest runs a bounded search. When StepBudget is supplied, exhausting
// that request's expansion budget returns a 200 paused resolution carrying a
// self-contained continuation token instead of a generic over-limit error.
func (s Solver) ResolveRequest(ctx context.Context, catalog domain.Catalog, input domain.ResolutionInput) (domain.Resolution, error) {
	maxSteps := s.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 50000
	}
	maxNodes := s.MaxNodes
	if maxNodes <= 0 {
		maxNodes = 128
	}

	// Budgets bound non-terminal work (expansions). The terminal observation
	// of a satisfied selection is always admitted and reported: a chain of N
	// components performs N expansions and reports N+1 steps whether it runs
	// once or is continued. limit is the absolute cumulative expansion count
	// at which a paged request pauses.
	limit := maxSteps
	pauseOnExhaustion := false
	if input.StepBudget != nil {
		if *input.StepBudget < 1 || *input.StepBudget > maxSteps {
			return domain.Resolution{}, domain.Invalid("step_budget must be between 1 and %d", maxSteps)
		}
		limit = *input.StepBudget
		pauseOnExhaustion = true
	}

	var token continuationToken
	if input.ContinuationToken != "" {
		decoded, err := decodeContinuation(input.ContinuationToken, s.ResumeKey)
		if err != nil {
			return domain.Resolution{}, err
		}
		token = decoded
		if token.Revision != catalog.Revision || token.Print != fingerprint(catalog) {
			return domain.Resolution{}, domain.Conflict("continuation token is stale because the component catalog changed; start a new resolution")
		}
		// Page limit is absolute cumulative expansions: already-spent
		// expansions plus the requested page, clamped to the global cap.
		if input.StepBudget != nil {
			pauseOnExhaustion = true
			limit = token.Expansions + *input.StepBudget
			if limit > maxSteps {
				limit = maxSteps
			}
		}
	} else if err := domain.ValidateRequirements(input.Roots, false); err != nil {
		return domain.Resolution{}, err
	}

	if err := ctx.Err(); err != nil {
		return domain.Resolution{}, err
	}
	compiled, err := compile(ctx, catalog)
	if err != nil {
		return domain.Resolution{}, err
	}

	roots := input.Roots
	if token.Roots != nil {
		roots = token.Roots
		if input.Roots != nil && !sameRoots(input.Roots, token.Roots) {
			return domain.Resolution{}, domain.Invalid("roots do not match the continuation token")
		}
	}
	if err := domain.ValidateRequirements(roots, false); err != nil {
		return domain.Resolution{}, err
	}

	work := search{
		ctx:         ctx,
		catalog:     compiled,
		roots:       make(map[string]semver.Constraint),
		frames:      []frame{{Cursor: -1, Enter: true, Base: make(map[string]candidate)}},
		limit:       limit,
		globalSteps: maxSteps,
		maxNodes:    maxNodes,
		pause:       pauseOnExhaustion,
		conflicts:   make([]evidence, 0),
	}
	for _, id := range domain.SortedKeys(roots) {
		if _, exists := catalog.Components[id]; !exists {
			return domain.Resolution{}, domain.Missing("component", id)
		}
		constraint, err := semver.ParseConstraint(roots[id])
		if err != nil {
			return domain.Resolution{}, err
		}
		work.roots[id] = constraint
	}

	if token.Frames != nil {
		if err := work.restore(token); err != nil {
			return domain.Resolution{}, err
		}
	}

	outcome, err := work.run()
	if err != nil {
		return domain.Resolution{}, err
	}
	finalSelected := work.topBase()
	confirmed := domain.ResolutionConfirmed{Roots: domain.CopyStrings(roots), Steps: work.steps, Conflicts: formatEvidence(work.conflicts)}
	if outcome == "no_solution" {
		return domain.Resolution{}, &domain.Fault{Code: "no_solution", Detail: "no compatible set satisfies the requested constraints", Conflicts: formatEvidence(work.conflicts)}
	}
	if outcome == "paused" {
		token = continuationToken{
			Version:    1,
			Revision:   catalog.Revision,
			Print:      fingerprint(catalog),
			Roots:      domain.CopyStrings(roots),
			Frames:     append([]frame(nil), work.frames...),
			Steps:      work.steps,
			Expansions: work.expansions,
			Conflicts:  append([]evidence(nil), work.conflicts...),
			MAC:        "",
		}
		encoded, err := encodeContinuation(token, s.ResumeKey)
		if err != nil {
			return domain.Resolution{}, err
		}
		selected, edges := selections(finalSelected)
		return domain.Resolution{
			CatalogRevision:   catalog.Revision,
			Status:            domain.ResolutionBudgetExhausted,
			Complete:          false,
			Resolved:          map[string]string{},
			Edges:             []domain.Edge{},
			Steps:             work.steps,
			Confirmed:         confirmed,
			Provisional:       &domain.ResolutionProvisional{Selected: selected, Edges: edges},
			ContinuationToken: encoded,
		}, nil
	}

	selected, edges := selections(finalSelected)
	return domain.Resolution{
		CatalogRevision: catalog.Revision,
		Status:          domain.ResolutionComplete,
		Complete:        true,
		Resolved:        selected,
		Edges:           edges,
		Steps:           work.steps,
		Confirmed:       confirmed,
	}, nil
}

func (s *search) run() (string, error) {
	for {
		if err := s.ctx.Err(); err != nil {
			return "", err
		}
		current := &s.frames[len(s.frames)-1]

		if !current.Enter {
			// A child subtree returned nil: resume this call's candidate loop
			// for the same target without a new entry step.
			if !s.tryCandidate(current) {
				s.explain(current.Target, s.requirements(current.Base)[current.Target])
				s.failFrame()
				if len(s.frames) == 0 {
					return "no_solution", nil
				}
			}
			continue
		}

		// Entry check for solve(Base): one reported step.
		if !current.Precharged {
			s.steps++
		}
		current.Enter = false
		current.Precharged = false

		needs := s.requirements(current.Base)
		if len(needs) > s.maxNodes {
			return "", domain.Limit("dependency graph exceeds component limit")
		}
		unresolved := ""
		blockedID := ""
		for _, id := range domain.SortedKeys(needs) {
			if chosen, ok := current.Base[id]; ok {
				if !matchesAll(chosen.version, needs[id]) {
					s.explain(id, needs[id])
					blockedID = id
					break
				}
			} else if unresolved == "" {
				unresolved = id
			}
		}

		if blockedID == "" && unresolved == "" {
			// Terminal observation: never expansion-gated, so a solution whose
			// expansions spend the cap exactly finishes identically one-shot
			// or across continuations.
			return "complete", nil
		}

		if blockedID != "" {
			// This branch is infeasible: return nil to the parent without
			// expanding a component.
			s.failFrame()
			if len(s.frames) == 0 {
				return "no_solution", nil
			}
			continue
		}

		// Choosing a component is the only non-terminal, budget-gated work.
		if s.expansions >= s.globalSteps {
			return "", domain.Limit("dependency search exhausted its step budget")
		}
		if s.expansions >= s.limit {
			if s.pause {
				// Hand the entering frame back: its entry step belongs to this
				// page, the expansion to the next page.
				current.Enter = true
				current.Precharged = true
				return "paused", nil
			}
			return "", domain.Limit("dependency search exhausted its step budget")
		}
		s.expansions++

		current.Target = unresolved
		current.Cursor = -1
		if !s.tryCandidate(current) {
			s.explain(unresolved, needs[unresolved])
			s.failFrame()
			if len(s.frames) == 0 {
				return "no_solution", nil
			}
			continue
		}
	}
}

// tryCandidate advances the frame to its next candidate of Target that
// satisfies Target's requirements over the frame's Base. On success it records
// the choice and pushes an entering child whose Base is a copy with that
// choice, exactly the recursion's "next := copy(Base); next[target]=choice;
// solve(next)". It returns false when no candidate is left.
func (s *search) tryCandidate(current *frame) bool {
	candidates := s.catalog[current.Target]
	for current.Cursor++; current.Cursor < len(candidates); current.Cursor++ {
		choice := candidates[current.Cursor]
		if !matchesAll(choice.version, s.requirements(current.Base)[current.Target]) {
			continue
		}
		childBase := cloneCandidates(current.Base)
		childBase[current.Target] = choice
		s.frames = append(s.frames, frame{Cursor: -1, Enter: true, Base: childBase})
		return true
	}
	return false
}

// failFrame removes the active call and marks its parent (if any) as resumed,
// so the parent advances its candidate loop without a fresh entry step.
func (s *search) failFrame() {
	s.frames = s.frames[:len(s.frames)-1]
	if len(s.frames) > 0 {
		s.frames[len(s.frames)-1].Enter = false
		s.frames[len(s.frames)-1].Precharged = false
	}
}

func (s *search) topBase() map[string]candidate {
	if len(s.frames) == 0 {
		return make(map[string]candidate)
	}
	return s.frames[len(s.frames)-1].Base
}

func cloneCandidates(in map[string]candidate) map[string]candidate {
	out := make(map[string]candidate, len(in))
	for id, value := range in {
		out[id] = value
	}
	return out
}

func (s *search) requirements(selected map[string]candidate) map[string][]requirement {
	needs := make(map[string][]requirement)
	for _, id := range domain.SortedKeys(s.roots) {
		needs[id] = append(needs[id], requirement{from: "root", constraint: s.roots[id]})
	}
	for _, id := range domain.SortedKeys(selected) {
		chosen := selected[id]
		for _, dep := range domain.SortedKeys(chosen.dependencies) {
			needs[dep] = append(needs[dep], requirement{from: id + "@" + chosen.release.Version, constraint: chosen.dependencies[dep]})
		}
	}
	return needs
}

func (s *search) explain(id string, needs []requirement) {
	if len(needs) == 0 || len(s.conflicts) >= 8 {
		return
	}
	for _, need := range needs {
		item := evidence{From: need.from, Target: id, Constraint: need.constraint.Raw}
		found := false
		for _, existing := range s.conflicts {
			if existing == item {
				found = true
				break
			}
		}
		if !found {
			s.conflicts = append(s.conflicts, item)
		}
		if len(s.conflicts) >= 8 {
			return
		}
	}
}

func formatEvidence(items []evidence) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, fmt.Sprintf("%s requires %s %s", item.From, item.Target, item.Constraint))
	}
	return result
}

// restore replays each frame's Base selection view from the signed target and
// cursor and validates every cursor against the deterministically derived
// requirement stack, so a tampered token cannot resume an arbitrary branch.
func (s *search) restore(token continuationToken) error {
	malformed := domain.Invalid("malformed continuation token")
	if token.Version != 1 || token.Steps < 0 || token.Expansions < 0 || len(token.Frames) == 0 {
		return malformed
	}
	if len(token.Conflicts) > 8 {
		return malformed
	}
	if len(token.Frames) > s.maxNodes+1 {
		return domain.Limit("dependency graph exceeds component limit")
	}
	if token.Steps < token.Expansions || token.Expansions > s.globalSteps {
		return malformed
	}

	last := token.Frames[len(token.Frames)-1]
	if last.Target != "" || last.Cursor != -1 || !last.Enter {
		return malformed
	}

	replayed := make([]frame, 0, len(token.Frames))
	base := make(map[string]candidate)
	seen := make(map[string]bool)
	for index, raw := range token.Frames {
		if index == len(token.Frames)-1 {
			if raw.Precharged && token.Steps <= token.Expansions {
				return malformed
			}
			replayed = append(replayed, frame{Cursor: -1, Enter: true, Precharged: raw.Precharged, Base: cloneCandidates(base)})
			break
		}
		if raw.Enter || raw.Precharged || raw.Target == "" || raw.Cursor < 0 || seen[raw.Target] {
			return malformed
		}
		candidates := s.catalog[raw.Target]
		if raw.Cursor >= len(candidates) {
			return malformed
		}
		// The cursor may point at any matching candidate: after a child branch
		// fails, a resumed frame advances past earlier candidates, so the
		// recorded choice is not necessarily the first one that matches its
		// target's own requirements. Only verify that it is a feasible choice
		// over the replayed parent Base (cursors at skipped, higher candidates
		// cannot be forged because the token is HMAC-signed).
		choice := candidates[raw.Cursor]
		if !matchesAll(choice.version, s.requirements(base)[raw.Target]) {
			return malformed
		}
		base[raw.Target] = choice
		seen[raw.Target] = true
		replayed = append(replayed, frame{
			Target: raw.Target,
			Cursor: raw.Cursor,
			Enter:  false,
			Base:   cloneCandidates(base),
		})
	}

	for _, item := range token.Conflicts {
		if item.From == "" || item.Target == "" || item.Constraint == "" {
			return malformed
		}
		if item.From != "root" {
			if _, _, ok := strings.Cut(item.From, "@"); !ok {
				return malformed
			}
		}
	}
	s.frames = replayed
	s.steps = token.Steps
	s.expansions = token.Expansions
	s.conflicts = append(s.conflicts, token.Conflicts...)
	return nil
}

func selections(selected map[string]candidate) (map[string]string, []domain.Edge) {
	versions := make(map[string]string, len(selected))
	edges := make([]domain.Edge, 0)
	for _, id := range domain.SortedKeys(selected) {
		chosen := selected[id]
		versions[id] = chosen.release.Version
	}
	for _, id := range domain.SortedKeys(selected) {
		chosen := selected[id]
		for _, dep := range domain.SortedKeys(chosen.dependencies) {
			edges = append(edges, domain.Edge{From: id, To: dep, Constraint: chosen.dependencies[dep].Raw})
		}
	}
	return versions, edges
}

func sameRoots(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for id, value := range a {
		if b[id] != value {
			return false
		}
	}
	return true
}

type continuationEnvelope struct {
	Payload string `json:"p"`
	MAC     string `json:"m"`
}

func encodeContinuation(token continuationToken, key []byte) (string, error) {
	raw, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	envelope, err := json.Marshal(continuationEnvelope{Payload: payload, MAC: tokenMAC(payload, key)})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(envelope), nil
}

func decodeContinuation(value string, key []byte) (continuationToken, error) {
	var token continuationToken
	malformed := domain.Invalid("malformed continuation token")
	envelopeData, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(envelopeData) > 16<<10 {
		return token, malformed
	}
	var envelope continuationEnvelope
	if err := json.Unmarshal(envelopeData, &envelope); err != nil || envelope.Payload == "" || envelope.MAC == "" {
		return token, malformed
	}
	if !hmac.Equal([]byte(envelope.MAC), []byte(tokenMAC(envelope.Payload, key))) {
		return token, malformed
	}
	raw, err := base64.RawURLEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return token, malformed
	}
	if err := json.Unmarshal(raw, &token); err != nil {
		return token, malformed
	}
	if token.MAC != "" || token.Print == "" || len(token.Roots) == 0 || len(token.Frames) == 0 {
		return token, malformed
	}
	return token, nil
}

func tokenMAC(payload string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
