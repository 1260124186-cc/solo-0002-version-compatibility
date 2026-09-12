package domain

import "time"

const (
	Draft     = "draft"
	Ready     = "ready"
	Applied   = "applied"
	Cancelled = "cancelled"
)

// Applicability reason codes reported on plan reads.
const (
	ReasonStateNotReady      = "state_not_ready"
	ReasonCatalogChanged     = "catalog_changed"
	ReasonEnvironmentChanged = "environment_changed"
)

// Applicability describes whether a plan can be applied right now, listing
// every reason that currently blocks application.
type Applicability struct {
	Applicable bool     `json:"applicable"`
	Reasons    []string `json:"reasons"`
}

// PlanApplicability evaluates a plan against the environment and catalog
// revision read in the same snapshot. It is a pure read and never modifies
// the plan.
func PlanApplicability(plan Plan, env Environment, catalogRevision uint64) Applicability {
	reasons := make([]string, 0, 3)
	if plan.State != Ready {
		reasons = append(reasons, ReasonStateNotReady)
	}
	// CatalogRevision is zero until the plan is first validated, so a draft
	// has no catalog snapshot to compare against.
	if plan.CatalogRevision > 0 && plan.CatalogRevision != catalogRevision {
		reasons = append(reasons, ReasonCatalogChanged)
	}
	if env.Revision != plan.BaseRevision {
		reasons = append(reasons, ReasonEnvironmentChanged)
	}
	return Applicability{Applicable: len(reasons) == 0, Reasons: reasons}
}

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

func (p Plan) CheckRevision(revision uint64) error {
	if revision == 0 {
		return Invalid("revision must be positive")
	}
	if revision != p.Revision {
		return Conflict("plan revision is %d, received %d", p.Revision, revision)
	}
	return nil
}
