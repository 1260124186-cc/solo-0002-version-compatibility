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
	Roots             map[string]string `json:"roots"`
	StepBudget        *int              `json:"step_budget,omitempty"`
	ContinuationToken string            `json:"continuation_token,omitempty"`
}

type ResolutionConfirmed struct {
	Roots     map[string]string `json:"roots"`
	Steps     int               `json:"steps"`
	Conflicts []string          `json:"conflicts"`
}

type ResolutionProvisional struct {
	Selected map[string]string `json:"selected"`
	Edges    []Edge            `json:"edges"`
}

type Edge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Constraint string `json:"constraint"`
}

type Resolution struct {
	CatalogRevision   uint64                 `json:"catalog_revision"`
	Status            string                 `json:"status"`
	Complete          bool                   `json:"complete"`
	Resolved          map[string]string      `json:"resolved"`
	Edges             []Edge                 `json:"edges"`
	Steps             int                    `json:"steps"`
	Confirmed         ResolutionConfirmed    `json:"confirmed"`
	Provisional       *ResolutionProvisional `json:"provisional,omitempty"`
	ContinuationToken string                 `json:"continuation_token,omitempty"`
}

const (
	ResolutionComplete        = "complete"
	ResolutionBudgetExhausted = "budget_exhausted"
)
