package domain

import "time"

type Environment struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Roots           map[string]string `json:"roots"`
	Resolved        map[string]string `json:"resolved"`
	Revision        uint64            `json:"revision"`
	ProfileID       string            `json:"profile_id,omitempty"`
	ProfileRevision uint64            `json:"profile_revision,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type EnvironmentInput struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Roots map[string]string `json:"roots"`
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
