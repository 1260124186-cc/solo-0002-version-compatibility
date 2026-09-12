package domain

import "time"

type Environment struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Roots    map[string]string `json:"roots"`
	Resolved map[string]string `json:"resolved"`
	Revision uint64            `json:"revision"`
	// NameRevision counts renames only; Revision counts plan applications.
	// Renaming must not move Revision, so plans based on it stay valid.
	NameRevision uint64    `json:"name_revision"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type EnvironmentInput struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Roots map[string]string `json:"roots"`
}

// RenameInput carries the current name revision as an optimistic lock:
// the first committer advances it and later submitters conflict.
type RenameInput struct {
	Name     string `json:"name"`
	Revision uint64 `json:"revision"`
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
