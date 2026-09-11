package domain

import "time"

const (
	ProfileActive       = "active"
	ProfileInactive     = "inactive"
	MaxProfiles         = 200
	MaxProfileRevisions = 200
)

// ProfileSnapshot captures the editable content of a profile at one revision.
// Snapshots are immutable once written, so environments keep pointing at the
// exact constraint set they were generated from.
type ProfileSnapshot struct {
	Revision    uint64            `json:"revision"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Roots       map[string]string `json:"roots"`
	CreatedAt   time.Time         `json:"created_at"`
}

// Profile is a named, reusable set of root dependency constraints. Saving a
// profile never solves the catalog; feasibility is only checked when an
// environment is generated from a revision.
type Profile struct {
	ID          string                     `json:"id"`
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	Roots       map[string]string          `json:"roots"`
	Revision    uint64                     `json:"revision"`
	State       string                     `json:"state"`
	CreatedAt   time.Time                  `json:"created_at"`
	UpdatedAt   time.Time                  `json:"updated_at"`
	InactiveAt  *time.Time                 `json:"inactive_at,omitempty"`
	Revisions   map[uint64]ProfileSnapshot `json:"revisions"`
}

type ProfileInput struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Roots       map[string]string `json:"roots"`
}

// ProfileUpdateInput replaces the full editable content and requires the
// revision the client based its edit on.
type ProfileUpdateInput struct {
	Revision    uint64            `json:"revision"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Roots       map[string]string `json:"roots"`
}

// EnvironmentFromProfileInput carries only the new environment identity; the
// root constraints come from the referenced profile revision.
type EnvironmentFromProfileInput struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ProfileRevision uint64 `json:"profile_revision"`
}

func ValidateProfile(input ProfileInput) error {
	if err := ValidateID(input.ID); err != nil {
		return err
	}
	if err := ValidateText(input.Name, "name", 1, 120); err != nil {
		return err
	}
	if err := ValidateText(input.Description, "description", 0, 2000); err != nil {
		return err
	}
	return ValidateRequirements(input.Roots, false)
}

func ValidateProfileUpdate(input ProfileUpdateInput) error {
	if input.Revision == 0 {
		return Invalid("revision must be positive")
	}
	if err := ValidateText(input.Name, "name", 1, 120); err != nil {
		return err
	}
	if err := ValidateText(input.Description, "description", 0, 2000); err != nil {
		return err
	}
	return ValidateRequirements(input.Roots, false)
}

func (p Profile) CheckRevision(revision uint64) error {
	if revision == 0 {
		return Invalid("revision must be positive")
	}
	if revision != p.Revision {
		return Conflict("profile revision is %d, received %d", p.Revision, revision)
	}
	return nil
}

func (p Profile) RequireActive() error {
	if p.State != ProfileActive {
		return Conflict("profile %q is deactivated and cannot generate environments", p.ID)
	}
	return nil
}

func (p Profile) Snapshot(at time.Time) ProfileSnapshot {
	return ProfileSnapshot{
		Revision:    p.Revision,
		Name:        p.Name,
		Description: p.Description,
		Roots:       CopyStrings(p.Roots),
		CreatedAt:   at,
	}
}
