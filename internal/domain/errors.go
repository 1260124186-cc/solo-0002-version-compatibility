package domain

import "fmt"

type Fault struct {
	Code      string   `json:"code"`
	Detail    string   `json:"detail"`
	Conflicts []string `json:"conflicts,omitempty"`
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

func ConflictWith(reasons []string, format string, args ...any) error {
	return &Fault{Code: "conflict", Detail: fmt.Sprintf(format, args...), Conflicts: reasons}
}

func Limit(detail string) error {
	return &Fault{Code: "limit_exceeded", Detail: detail}
}
