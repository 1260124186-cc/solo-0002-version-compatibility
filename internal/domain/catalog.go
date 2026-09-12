package domain

import "time"

type Component struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Revision tracks metadata-only edits (name/description). It is independent
	// of Catalog.Revision, so renaming a component never invalidates resolution
	// results or ready plans.
	Revision  uint64     `json:"revision"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type Release struct {
	ComponentID string            `json:"component_id"`
	Version     string            `json:"version"`
	Requires    map[string]string `json:"requires"`
	State       string            `json:"state"`
	CreatedAt   time.Time         `json:"created_at"`
	WithdrawnAt *time.Time        `json:"withdrawn_at,omitempty"`
}

type ComponentInput struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ComponentUpdateInput carries descriptive metadata plus the component
// revision the caller based its view on. The identifier is deliberately
// absent: it is immutable once a component exists.
type ComponentUpdateInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Revision    uint64 `json:"revision"`
}

type ReleaseInput struct {
	Version  string            `json:"version"`
	Requires map[string]string `json:"requires"`
}

type Catalog struct {
	Revision   uint64                        `json:"revision"`
	Components map[string]Component          `json:"components"`
	Releases   map[string]map[string]Release `json:"releases"`
}

const (
	Available       = "available"
	Withdrawn       = "withdrawn"
	MaxComponents   = 500
	MaxReleases     = 200
	MaxDependencies = 32
)
