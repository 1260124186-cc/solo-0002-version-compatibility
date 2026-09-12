package domain

import "time"

const (
	Draft     = "draft"
	Ready     = "ready"
	Applied   = "applied"
	Cancelled = "cancelled"
)

type Change struct {
	ComponentID string `json:"component_id"`
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
	Kind        string `json:"kind"`
}

// Correction appends an immutable clarification to a cancelled plan.
type Correction struct {
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

type Plan struct {
	ID              string            `json:"id"`
	EnvironmentID   string            `json:"environment_id"`
	BaseRevision    uint64            `json:"base_revision"`
	Revision        uint64            `json:"revision"`
	CatalogRevision uint64            `json:"catalog_revision"`
	Roots           map[string]string `json:"roots"`
	Resolved        map[string]string `json:"resolved"`
	Changes         []Change          `json:"changes"`
	State           string            `json:"state"`
	Reason          string            `json:"reason"`
	CancelReason    string            `json:"cancel_reason,omitempty"`
	Corrections     []Correction      `json:"corrections,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type PlanInput struct {
	EnvironmentID string            `json:"environment_id"`
	BaseRevision  uint64            `json:"base_revision"`
	Roots         map[string]string `json:"roots"`
	Reason        string            `json:"reason"`
}

type RevisionInput struct {
	Revision uint64 `json:"revision"`
}

// ReasonInput is the body for cancel and correct actions.
type ReasonInput struct {
	Revision uint64 `json:"revision"`
	Reason   string `json:"reason"`
}

func (p Plan) CanValidate() error {
	if p.State != Draft && p.State != Ready {
		return Conflict("cannot validate a %s plan", p.State)
	}
	return nil
}

func (p Plan) CanCancel() error {
	if p.State != Draft && p.State != Ready {
		return Conflict("cannot cancel a %s plan", p.State)
	}
	return nil
}

// CheckNotCancelled reports the same conflict for every action on a
// cancelled plan, regardless of the revision the caller supplies.
func (p Plan) CheckNotCancelled(action string) error {
	if p.State == Cancelled {
		return Conflict("cannot %s a cancelled plan", action)
	}
	return nil
}

func (p Plan) CheckRevision(revision uint64) error {
	if revision == 0 {
		return Invalid("revision must be positive")
	}
	if revision != p.Revision {
		return Conflict("plan revision is %d, received %d", p.Revision, revision)
	}
	return nil
}
