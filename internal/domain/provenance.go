package domain

import "time"

// Provenance kinds explain how an environment reached a revision.
const (
	// OriginCreated marks the initial resolution recorded at environment creation.
	OriginCreated = "created"
	// OriginApplied marks a revision produced by successfully applying a plan.
	OriginApplied = "plan_applied"
	// OriginBaseline marks the starting point established from the current
	// state of an environment that predates provenance tracking; anything
	// earlier is unknown and must not be reconstructed.
	OriginBaseline = "baseline"
)

// Provenance traces one environment revision back to its origin: the plan
// that produced it (if any), the environment revision it was applied from,
// the catalog revision it was resolved against, and the root requirements
// and resolved selection observed at that moment.
type Provenance struct {
	EnvironmentID   string            `json:"environment_id"`
	Revision        uint64            `json:"revision"`
	Kind            string            `json:"kind"`
	PlanID          string            `json:"plan_id,omitempty"`
	BaseRevision    uint64            `json:"base_revision,omitempty"`
	CatalogRevision uint64            `json:"catalog_revision"`
	Roots           map[string]string `json:"roots"`
	Resolved        map[string]string `json:"resolved"`
	At              time.Time         `json:"at"`
}
