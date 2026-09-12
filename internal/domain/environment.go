package domain

import "time"

type Environment struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Roots       map[string]string `json:"roots"`
	Resolved    map[string]string `json:"resolved"`
	Revision    uint64            `json:"revision"`
	DerivedFrom *Derivation       `json:"derived_from,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// Derivation records the exact source state a derived environment was copied
// from, so the origin of its initial selection stays traceable.
type Derivation struct {
	SourceID       string `json:"source_id"`
	SourceRevision uint64 `json:"source_revision"`
}

type EnvironmentInput struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Roots map[string]string `json:"roots"`
}

type DeriveInput struct {
	SourceRevision uint64 `json:"source_revision"`
	ID             string `json:"id"`
	Name           string `json:"name"`
}

type ResolutionInput struct {
	Roots map[string]string `json:"roots"`
}

type Edge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Constraint string `json:"constraint"`
}

type Resolution struct {
	CatalogRevision uint64            `json:"catalog_revision"`
	Resolved        map[string]string `json:"resolved"`
	Edges           []Edge            `json:"edges"`
	Steps           int               `json:"steps"`
}
