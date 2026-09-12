package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

type Component struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	Family           string    `json:"family,omitempty"`
	Visibility       string    `json:"visibility,omitempty"`
	AllowedConsumers []string  `json:"allowed_consumers,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type Release struct {
	ComponentID  string            `json:"component_id"`
	Version      string            `json:"version"`
	Requires     map[string]string `json:"requires"`
	InternalDeps []string          `json:"internal_dependencies,omitempty"`
	State        string            `json:"state"`
	CreatedAt    time.Time         `json:"created_at"`
	WithdrawnAt  *time.Time        `json:"withdrawn_at,omitempty"`
}

// RequirementInput accepts either a bare constraint string ("^1.0.0") or an
// object {"constraint":"^1.0.0","visibility":"internal"}. An edge declared
// internal asserts that the target belongs to the owner's family (or is on the
// target's allow list); the declaration never grants access by itself.
type RequirementInput struct {
	Constraint string
	Visibility string
}

func (r *RequirementInput) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		r.Constraint = raw
		r.Visibility = Public
		return nil
	}
	var structured struct {
		Constraint string `json:"constraint"`
		Visibility string `json:"visibility"`
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&structured); err != nil {
		return err
	}
	if structured.Constraint == "" {
		return fmt.Errorf("requirement constraint is required")
	}
	visibility := structured.Visibility
	if visibility == "" {
		visibility = Public
	}
	if visibility != Public && visibility != Internal {
		return fmt.Errorf("requirement visibility must be %q or %q", Public, Internal)
	}
	r.Constraint = structured.Constraint
	r.Visibility = visibility
	return nil
}

func (r RequirementInput) MarshalJSON() ([]byte, error) {
	if r.Visibility == Internal {
		return json.Marshal(struct {
			Constraint string `json:"constraint"`
			Visibility string `json:"visibility"`
		}{Constraint: r.Constraint, Visibility: Internal})
	}
	return json.Marshal(r.Constraint)
}

// IsInternal reports whether the dependency edge was declared internal.
func (r RequirementInput) IsInternal() bool { return r.Visibility == Internal }

type ComponentInput struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Family           string   `json:"family"`
	Visibility       string   `json:"visibility"`
	AllowedConsumers []string `json:"allowed_consumers"`
}

type ReleaseInput struct {
	Version  string                      `json:"version"`
	Requires map[string]RequirementInput `json:"requires"`
}

type Catalog struct {
	Revision           uint64                        `json:"revision"`
	VisibilityRevision uint64                        `json:"visibility_revision,omitempty"`
	Components         map[string]Component          `json:"components"`
	Releases           map[string]map[string]Release `json:"releases"`
}

const (
	Available       = "available"
	Withdrawn       = "withdrawn"
	Public          = "public"
	Internal        = "internal"
	MaxComponents   = 500
	MaxReleases     = 200
	MaxDependencies = 32
	MaxConsumers    = 64
)

// EffectiveVisibility treats components stored before the visibility rule as public.
func EffectiveVisibility(component Component) string {
	if component.Visibility == Internal {
		return Internal
	}
	return Public
}

// CanReference reports whether a direct dependency edge from -> target is
// allowed. Root requests have no family and are never on an allow list, so
// they may only reference public components. Internal targets are reachable
// through a public entry point but cannot be referenced directly from outside
// their family unless the target explicitly allows the consuming component.
func CanReference(from string, fromComponent, target Component) bool {
	if EffectiveVisibility(target) == Public {
		return true
	}
	if from == "" {
		return false
	}
	if fromComponent.Family != "" && fromComponent.Family == target.Family {
		return true
	}
	for _, consumer := range target.AllowedConsumers {
		if consumer == from {
			return true
		}
	}
	return false
}

// VisibilityDenied marks a direct dependency that crosses a visibility boundary.
func VisibilityDenied(format string, args ...any) error {
	return &Fault{Code: "visibility_denied", Detail: fmt.Sprintf(format, args...)}
}
