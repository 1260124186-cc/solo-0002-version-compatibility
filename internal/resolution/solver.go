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

type frame struct {
	Target string `json:"t"`
	Cursor int    `json:"c"`
}

type evidence struct {
	From       string `json:"f"`
	Target     string `json:"t"`
	Constraint string `json:"c"`
}

type continuationToken struct {
	Version   int               `json:"v"`
	Revision  uint64            `json:"rev"`
	Print     string            `json:"fp"`
	Roots     map[string]string `json:"roots"`
	Frames    []frame           `json:"frames"`
	Steps     int               `json:"steps"`
	Conflicts []evidence        `json:"conflicts"`
	MAC       string            `json:"mac"`
}

type search struct {
	ctx       context.Context
	catalog   map[string][]candidate
	roots     map[string]semver.Constraint
	selected  map[string]candidate
	frames    []frame
	steps     int
	limit     int
	maxNodes  int
	pause     bool
	conflicts []evidence
}

// Resolve keeps the non-resumable entry point used by environment and plan
// workflows. A configured step limit remains a hard error for these callers.
func (s Solver) Resolve(ctx context.Context, catalog domain.Catalog, roots map[string]string) (domain.Resolution, error) {
	return s.ResolveRequest(ctx, catalog, domain.ResolutionInput{Roots: roots})
}

// ResolveRequest runs a bounded search. When StepBudget is supplied, exhausting
// that request budget returns a complete domain.Resolution with Status set to
// budget_exhausted and a self-contained continuation token.
func (s Solver) ResolveRequest(ctx context.Context, catalog domain.Catalog, input domain.ResolutionInput) (domain.Resolution, error) {
	maxSteps := s.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 50000
	}
	maxNodes := s.MaxNodes
	if maxNodes <= 0 {
		maxNodes = 128
	}

	requested := maxSteps
	budget := maxSteps + 1
	pauseOnExhaustion := false
	if input.StepBudget != nil {
		if *input.StepBudget < 1 || *input.StepBudget > maxSteps {
			return domain.Resolution{}, domain.Invalid("step_budget must be between 1 and %d", maxSteps)
		}
		requested = *input.StepBudget
		budget = requested
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
		if token.Steps >= maxSteps {
			return domain.Resolution{}, domain.Limit("dependency search exhausted its step budget")
		}
		remaining := maxSteps - token.Steps
		if input.StepBudget != nil {
			pauseOnExhaustion = true
			if requested > remaining {
				requested = remaining
			}
			budget = token.Steps + requested
		} else {
			// Finish within the global cap; exhausting it stays a hard error,
			// exactly like a non-resumable resolve.
			budget = token.Steps + remaining
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
		ctx:       ctx,
		catalog:   compiled,
		roots:     make(map[string]semver.Constraint),
		selected:  make(map[string]candidate),
		frames:    []frame{{Cursor: -1}},
		limit:     budget,
		maxNodes:  maxNodes,
		pause:     pauseOnExhaustion,
		conflicts: make([]evidence, 0),
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
		if err := work.restore(token, compiled); err != nil {
			return domain.Resolution{}, err
		}
	}

	outcome, err := work.run()
	if err != nil {
		return domain.Resolution{}, err
	}
	confirmed := domain.ResolutionConfirmed{Roots: domain.CopyStrings(roots), Steps: work.steps, Conflicts: formatEvidence(work.conflicts)}
	if outcome == "no_solution" {
		return domain.Resolution{}, &domain.Fault{Code: "no_solution", Detail: "no compatible set satisfies the requested constraints", Conflicts: formatEvidence(work.conflicts)}
	}
	if outcome == "paused" {
		token = continuationToken{
			Version:   1,
			Revision:  catalog.Revision,
			Print:     fingerprint(catalog),
			Roots:     domain.CopyStrings(roots),
			Frames:    append([]frame(nil), work.frames...),
			Steps:     work.steps,
			Conflicts: append([]evidence(nil), work.conflicts...),
			MAC:       "",
		}
		encoded, err := encodeContinuation(token, s.ResumeKey)
		if err != nil {
			return domain.Resolution{}, err
		}
		selected, edges := selections(work.selected)
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

	selected, edges := selections(work.selected)
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
		top := &s.frames[len(s.frames)-1]
		if top.Target != "" {
			// A child call finished; continue this call's deterministic
			// candidate loop. No step is charged, exactly like the recursive
			// implementation resuming after a nil branch.
			if !s.advance(top) {
				s.explain(top.Target, s.requirements(s.selected)[top.Target])
				s.popCall()
				if len(s.frames) == 0 {
					return "no_solution", nil
				}
			}
			continue
		}

		// Entering a search call: the only place steps and node limits count.
		if s.steps >= s.limit {
			if s.pause {
				return "paused", nil
			}
			return "", domain.Limit("dependency search exhausted its step budget")
		}
		s.steps++

		needs := s.requirements(s.selected)
		if len(needs) > s.maxNodes {
			return "", domain.Limit("dependency graph exceeds component limit")
		}
		unresolved := ""
		for _, id := range domain.SortedKeys(needs) {
			if chosen, ok := s.selected[id]; ok {
				if !matchesAll(chosen.version, needs[id]) {
					s.explain(id, needs[id])
					s.popCall()
					if len(s.frames) == 0 {
						return "no_solution", nil
					}
					unresolved = "**blocked**"
					break
				}
			} else if unresolved == "" {
				unresolved = id
			}
		}
		if unresolved == "**blocked**" {
			continue
		}
		if unresolved == "" {
			return "complete", nil
		}
		top.Target = unresolved
		if !s.advance(top) {
			s.explain(unresolved, needs[unresolved])
			s.popCall()
			if len(s.frames) == 0 {
				return "no_solution", nil
			}
		}
	}
}

// advance moves the call to its next matching candidate. The call's previous
// choice, if any, belonged to a branch that has been exhausted.
func (s *search) advance(current *frame) bool {
	delete(s.selected, current.Target)
	for current.Cursor++; current.Cursor < len(s.catalog[current.Target]); current.Cursor++ {
		choice := s.catalog[current.Target][current.Cursor]
		needs := s.requirements(s.selected)[current.Target]
		if !matchesAll(choice.version, needs) {
			continue
		}
		s.selected[current.Target] = choice
		s.frames = append(s.frames, frame{Cursor: -1})
		return true
	}
	return false
}

// popCall removes the active call. A marker frame (a call entered but before a
// successful choice) owns no selection; a choice frame owns its target entry.
func (s *search) popCall() {
	top := s.frames[len(s.frames)-1]
	if top.Target != "" {
		delete(s.selected, top.Target)
	}
	s.frames = s.frames[:len(s.frames)-1]
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
	if len(s.conflicts) >= 8 {
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

func (s *search) restore(token continuationToken, catalog map[string][]candidate) error {
	malformed := domain.Invalid("malformed continuation token")
	if token.Version != 1 || token.Steps < 0 || len(token.Frames) == 0 {
		return malformed
	}
	if len(token.Conflicts) > 8 {
		return malformed
	}
	if len(token.Frames) > s.maxNodes+1 {
		return domain.Limit("dependency graph exceeds component limit")
	}
	first := token.Frames[0]
	last := token.Frames[len(token.Frames)-1]
	if last.Target != "" || last.Cursor != -1 {
		return malformed
	}
	if len(token.Frames) == 1 && first.Target == "" {
		// Pause before any choice was made: only the root marker exists.
		s.frames = append([]frame(nil), token.Frames...)
		s.steps = token.Steps
		s.conflicts = append(s.conflicts, token.Conflicts...)
		return nil
	}
	selected := make(map[string]candidate)
	seen := make(map[string]bool)
	for index := 0; index < len(token.Frames)-1; index++ {
		item := token.Frames[index]
		if item.Target == "" || item.Cursor < 0 || seen[item.Target] {
			return malformed
		}
		candidates := catalog[item.Target]
		if item.Cursor >= len(candidates) {
			return malformed
		}
		// Re-derive the requirement stack from earlier choices and confirm the
		// recorded deterministic cursor still denotes a feasible branch.
		choice := candidates[item.Cursor]
		if !matchesAll(choice.version, s.requirements(selected)[item.Target]) {
			return malformed
		}
		selected[item.Target] = choice
		seen[item.Target] = true
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
	s.selected = selected
	s.frames = append([]frame(nil), token.Frames...)
	s.steps = token.Steps
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
	expectedMAC := tokenMAC(envelope.Payload, key)
	if !hmac.Equal([]byte(envelope.MAC), []byte(expectedMAC)) {
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
