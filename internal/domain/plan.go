package domain

import "time"

const (
	Draft     = "draft"
	Ready     = "ready"
	Applied   = "applied"
	Cancelled = "cancelled"
	Expired   = "expired"
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

type RevisionInput struct {
	Revision uint64 `json:"revision"`
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

func (p Plan) CanExpire() error {
	if p.State != Draft && p.State != Ready {
		return Conflict("cannot expire a %s plan", p.State)
	}
	return nil
}

// Stale reports whether a draft or ready plan has been superseded: the
// environment moved past the plan's BaseRevision, or the catalog moved past
// the plan's CatalogRevision. Terminal states are never stale.
func (p Plan) Stale(environmentRevision, catalogRevision uint64) bool {
	if p.State != Draft && p.State != Ready {
		return false
	}
	return p.BaseRevision < environmentRevision || p.CatalogRevision < catalogRevision
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
