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
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type PlanInput struct {
	EnvironmentID string            `json:"environment_id"`
	BaseRevision  uint64            `json:"base_revision"`
	Roots         map[string]string `json:"roots"`
	Reason        string            `json:"reason"`
}

type PlanEditInput struct {
	Revision uint64            `json:"revision"`
	Roots    map[string]string `json:"roots"`
	Reason   string            `json:"reason"`
}

type RevisionInput struct {
	Revision uint64 `json:"revision"`
}

func (p Plan) CanEdit() error {
	if p.State != Draft {
		return Conflict("cannot edit a %s plan", p.State)
	}
	return nil
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

func (p Plan) CheckRevision(revision uint64) error {
	if revision == 0 {
		return Invalid("revision must be positive")
	}
	if revision != p.Revision {
		return Conflict("plan revision is %d, received %d", p.Revision, revision)
	}
	return nil
}
