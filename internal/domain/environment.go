package domain

import "time"

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
	// EnvironmentID and EnvironmentRevision form an optional baseline: both
	// must be set together to prefer the versions installed in that
	// environment, or both left out to keep the newest-first behavior.
	EnvironmentID       string `json:"environment_id,omitempty"`
	EnvironmentRevision uint64 `json:"environment_revision,omitempty"`
}

type Edge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Constraint string `json:"constraint"`
}

type Resolution struct {
	CatalogRevision uint64 `json:"catalog_revision"`
	// EnvironmentRevision and Changes are only set when the request named an
	// environment baseline: the revision the resolution was based on and the
	// changes relative to that environment. A non-nil pointer keeps an empty
	// change list visible as [] instead of dropping the field.
	EnvironmentRevision uint64            `json:"environment_revision,omitempty"`
	Resolved            map[string]string `json:"resolved"`
	Edges               []Edge            `json:"edges"`
	Changes             *[]Change         `json:"changes,omitempty"`
	Steps               int               `json:"steps"`
}
