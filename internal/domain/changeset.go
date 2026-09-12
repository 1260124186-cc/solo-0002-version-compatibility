package domain

import "time"

// Invalidated joins draft/ready/applied/cancelled as a change-set-specific
// terminal state. A set becomes invalidated when a referenced plan changes
// outside of the set's own application.
const Invalidated = "invalidated"

const (
	MaxChangeSets     = 1000
	MinChangeSetPlans = 2
	MaxChangeSetPlans = 200
)

// ChangeSetEntry records the revision basis (plan, environment, catalog) that
// the set validated against. The group application rejects the whole batch if
// any of these revisions drifts beforehand.
type ChangeSetEntry struct {
	PlanID              string `json:"plan_id"`
	EnvironmentID       string `json:"environment_id"`
	PlanRevision        uint64 `json:"plan_revision"`
	EnvironmentRevision uint64 `json:"environment_revision"`
	CatalogRevision     uint64 `json:"catalog_revision"`
}

type ChangeSet struct {
	ID                string           `json:"id"`
	Revision          uint64           `json:"revision"`
	State             string           `json:"state"`
	Reason            string           `json:"reason"`
	Entries           []ChangeSetEntry `json:"entries"`
	InvalidatedReason string           `json:"invalidated_reason,omitempty"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
}

type ChangeSetInput struct {
	PlanIDs []string `json:"plan_ids"`
	Reason  string   `json:"reason"`
}

func (c ChangeSet) CheckRevision(revision uint64) error {
	if revision == 0 {
		return Invalid("revision must be positive")
	}
	if revision != c.Revision {
		return Conflict("change set revision is %d, received %d", c.Revision, revision)
	}
	return nil
}

// CanValidate allows fresh draft sets and re-validation of still-live ready
// sets. Invalidated sets are terminal and must be rebuilt.
func (c ChangeSet) CanValidate() error {
	if c.State != Draft && c.State != Ready {
		return Conflict("cannot validate a %s change set", c.State)
	}
	return nil
}

func (c ChangeSet) CanApply() error {
	if c.State != Ready {
		return Conflict("only a ready change set can be applied")
	}
	return nil
}

func (c ChangeSet) CanCancel() error {
	if c.State != Draft && c.State != Ready {
		return Conflict("cannot cancel a %s change set", c.State)
	}
	return nil
}
