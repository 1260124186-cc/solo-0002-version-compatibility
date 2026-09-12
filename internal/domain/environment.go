package domain

import "time"

const (
	RootTimelineBootstrap = "bootstrap"
	RootTimelineCreated   = "created"
	RootTimelineApplied   = "applied"
)

type Environment struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Roots     map[string]string `json:"roots"`
	Resolved  map[string]string `json:"resolved"`
	Revision  uint64            `json:"revision"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type EnvironmentInput struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Roots map[string]string `json:"roots"`
}

type ResolutionInput struct {
	Roots map[string]string `json:"roots"`
}

type Edge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Constraint string `json:"constraint"`
}

type Resolution struct {
	CatalogRevision uint64            `json:"catalog_revision"`
	Resolved        map[string]string `json:"resolved"`
	Edges           []Edge            `json:"edges"`
	Steps           int               `json:"steps"`
}

type RootTimelineEntry struct {
	Sequence       uint64            `json:"sequence"`
	Type           string            `json:"type"`
	At             time.Time         `json:"at"`
	PlanID         string            `json:"plan_id,omitempty"`
	Reason         string            `json:"reason,omitempty"`
	EventSequence  uint64            `json:"event_sequence"`
	BeforeRevision uint64            `json:"before_revision"`
	AfterRevision  uint64            `json:"after_revision"`
	Before         map[string]string `json:"before,omitempty"`
	After          map[string]string `json:"after"`
	RootChanges    []Change          `json:"root_changes"`
}

type RootTimeline struct {
	EnvironmentID string              `json:"environment_id"`
	Entries       []RootTimelineEntry `json:"entries"`
}
