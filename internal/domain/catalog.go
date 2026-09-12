package domain

import "time"

type Component struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type Release struct {
	ComponentID                 string                       `json:"component_id"`
	Version                     string                       `json:"version"`
	Requires                    map[string]string            `json:"requires"`
	State                       string                       `json:"state"`
	CreatedAt                   time.Time                    `json:"created_at"`
	WithdrawnAt                 *time.Time                   `json:"withdrawn_at,omitempty"`
	WithdrawalReason            string                       `json:"withdrawal_reason,omitempty"`
	WithdrawalEventSequence     uint64                       `json:"withdrawal_event_sequence,omitempty"`
	WithdrawalReasonCorrections []WithdrawalReasonCorrection `json:"withdrawal_reason_corrections,omitempty"`
}

type WithdrawalReasonCorrection struct {
	Reason           string    `json:"reason"`
	At               time.Time `json:"at"`
	EventSequence    uint64    `json:"event_sequence"`
	CorrectsSequence uint64    `json:"corrects_sequence"`
}

type ComponentInput struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ReleaseInput struct {
	Version  string            `json:"version"`
	Requires map[string]string `json:"requires"`
}

type WithdrawReleaseInput struct {
	Reason string `json:"reason"`
}

type CorrectWithdrawalReasonInput struct {
	Reason string `json:"reason"`
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
