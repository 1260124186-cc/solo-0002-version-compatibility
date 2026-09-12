package domain

import "time"

type LifecycleTransition struct {
	From   string    `json:"from"`
	To     string    `json:"to"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

type Component struct {
	ID           string                `json:"id"`
	Name         string                `json:"name"`
	Description  string                `json:"description"`
	State        string                `json:"state"`
	CreatedAt    time.Time             `json:"created_at"`
	DeprecatedAt *time.Time            `json:"deprecated_at,omitempty"`
	RetiredAt    *time.Time            `json:"retired_at,omitempty"`
	Lifecycle    []LifecycleTransition `json:"lifecycle"`
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

type LifecycleInput struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
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
	Active          = "active"
	Deprecated      = "deprecated"
	Retired         = "retired"
	MaxComponents   = 500
	MaxReleases     = 200
	MaxLifecycle    = 100
	MaxDependencies = 32
)
