package domain

import "fmt"

type Fault struct {
	Code      string          `json:"code"`
	Detail    string          `json:"detail"`
	Conflicts []string        `json:"conflicts,omitempty"`
	Conflict  *ConflictReport `json:"conflict,omitempty"`
}

// ConflictCause attributes one conflicting condition to its source: either a
// root requirement or the dependency of a specific parent release.
type ConflictCause struct {
	Component       string `json:"component"`
	Constraint      string `json:"constraint"`
	Source          string `json:"source"`
	SourceComponent string `json:"source_component,omitempty"`
	SourceVersion   string `json:"source_version,omitempty"`
}

// ConflictReport is a bounded, near-minimal set of mutually incompatible
// conditions. Components lists every component named by the causes.
type ConflictReport struct {
	Components []string        `json:"components"`
	Causes     []ConflictCause `json:"causes"`
}

func (f *Fault) Error() string {
	return f.Code + ": " + f.Detail
}

func Invalid(format string, args ...any) error {
	return &Fault{Code: "invalid_input", Detail: fmt.Sprintf(format, args...)}
}

func Missing(kind, id string) error {
	return &Fault{Code: "not_found", Detail: fmt.Sprintf("%s %q does not exist", kind, id)}
}

func Conflict(format string, args ...any) error {
	return &Fault{Code: "conflict", Detail: fmt.Sprintf(format, args...)}
}

func Limit(detail string) error {
	return &Fault{Code: "limit_exceeded", Detail: detail}
}
