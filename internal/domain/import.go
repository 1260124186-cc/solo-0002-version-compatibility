package domain

import "time"

const (
	ImportPending   = "pending"
	ImportCompleted = "completed"
	ImportFailed    = "failed"
)

const (
	ImportCreateComponent = "create_component_and_release"
	ImportAddRelease      = "add_release"
	ImportAlreadyPresent  = "already_present"
)

const (
	MaxImportEntries = 200
	MaxImports       = 500
)

// ImportEntryInput is one raw manifest line supplied by the caller.
type ImportEntryInput struct {
	ComponentID string            `json:"component_id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	Requires    map[string]string `json:"requires"`
}

type ImportInput struct {
	Entries []ImportEntryInput `json:"entries"`
}

// ImportEntryError is always attached to the entry it belongs to; Field uses
// dotted names such as "version" or "requires.other-component".
type ImportEntryError struct {
	Field  string `json:"field,omitempty"`
	Detail string `json:"detail"`
}

// ImportEntryResult is the preview row for a single original entry. Index keeps
// its position in the submitted manifest even when other entries are invalid.
type ImportEntryResult struct {
	Index       int                `json:"index"`
	ComponentID string             `json:"component_id"`
	Name        string             `json:"name,omitempty"`
	Description string             `json:"description,omitempty"`
	Version     string             `json:"version"`
	Requires    map[string]string  `json:"requires"`
	Action      string             `json:"action,omitempty"`
	Errors      []ImportEntryError `json:"errors"`
}

type ImportSummary struct {
	Entries          int `json:"entries"`
	CreateComponents int `json:"create_components"`
	AddReleases      int `json:"add_releases"`
	AlreadyPresent   int `json:"already_present"`
	Errors           int `json:"errors"`
}

type Import struct {
	ID                string              `json:"id"`
	Fingerprint       string              `json:"fingerprint"`
	State             string              `json:"state"`
	CatalogRevision   uint64              `json:"catalog_revision"`
	Summary           ImportSummary       `json:"summary"`
	Entries           []ImportEntryResult `json:"entries"`
	CreatedComponents []string            `json:"created_components,omitempty"`
	AddedReleases     []string            `json:"added_releases,omitempty"`
	CreatedAt         time.Time           `json:"created_at"`
	CompletedAt       *time.Time          `json:"completed_at,omitempty"`
	FailedAt          *time.Time          `json:"failed_at,omitempty"`
}

// Inputs reconstructs the original manifest from a persisted preview so a
// confirmation can be replayed against the current catalog.
func (i Import) Inputs() []ImportEntryInput {
	entries := make([]ImportEntryInput, len(i.Entries))
	for n, item := range i.Entries {
		entries[n] = ImportEntryInput{
			ComponentID: item.ComponentID,
			Name:        item.Name,
			Description: item.Description,
			Version:     item.Version,
			Requires:    CopyStrings(item.Requires),
		}
	}
	return entries
}
