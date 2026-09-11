package domain

// ConstraintOrigin identifies one constraint that participated in a release
// choice. Source is "root" for a root requirement, otherwise the
// "<component>@<version>" release whose dependency declared the constraint.
type ConstraintOrigin struct {
	Source     string `json:"source"`
	Constraint string `json:"constraint"`
}

// SelectionStep is one auditable link of the resolution proof. It states which
// release was chosen, at which decision position, which constraints introduced
// the component and were already in force at that point, and which constraints
// came from releases chosen later but are still satisfied by this choice.
type SelectionStep struct {
	Component       string             `json:"component"`
	Version         string             `json:"version"`
	Order           int                `json:"order"`
	IntroducedBy    []ConstraintOrigin `json:"introduced_by"`
	VerifiedAgainst []ConstraintOrigin `json:"verified_against"`
}

// DependencyCycle closes a dependency chain inside the chosen set. Nodes lists
// each component once following the dependency direction; Edges contains the
// closing edge back to the first node, so the structure stays finite instead
// of expanding the cycle indefinitely.
type DependencyCycle struct {
	Nodes []string `json:"nodes"`
	Edges []Edge   `json:"edges"`
}

// Proof is the complete, auditable selection chain of one resolution, bound to
// the exact catalog revision against which it was computed. A proof computed
// against an older revision must be reported as stale and never treated as a
// current conclusion.
type Proof struct {
	CatalogRevision uint64            `json:"catalog_revision"`
	Roots           map[string]string `json:"roots"`
	Selection       []SelectionStep   `json:"selection"`
	Cycles          []DependencyCycle `json:"cycles"`
}

const (
	// ProofCurrent means the proof is bound to the catalog revision in force.
	ProofCurrent = "current"
	// ProofStale means the catalog moved on; the proof remains a historical
	// record but must not be acted upon without a fresh resolution.
	ProofStale = "stale"
	// ProofAbsent means the record predates proofs or is not yet resolved.
	ProofAbsent = "absent"
)

// ProofStatusFor derives the advertised status of a stored proof. The status is
// intentionally not persisted: it is recomputed against the live catalog every
// time an environment or plan is read.
func ProofStatusFor(catalogRevision uint64, proof *Proof) string {
	switch {
	case proof == nil:
		return ProofAbsent
	case proof.CatalogRevision == catalogRevision:
		return ProofCurrent
	default:
		return ProofStale
	}
}
