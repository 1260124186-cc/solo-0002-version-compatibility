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

func Limit(detail string) error {
	return &Fault{Code: "limit_exceeded", Detail: detail}
}

// StaleProof marks a proof bound to a catalog revision that is no longer in
// force. It carries the same revision semantics as any other 409 conflict: the
// caller must resolve again before relying on the conclusion.
func StaleProof(format string, args ...any) error {
	return &Fault{Code: "proof_stale", Detail: fmt.Sprintf(format, args...)}
}

// InvalidProof marks an internal proof whose structure does not re-derive the
// stored selection from the catalog. Proofs are server-generated, so reaching
// this on a live record signals tampering or corruption rather than bad input.
func InvalidProof(format string, args ...any) error {
	return &Fault{Code: "proof_invalid", Detail: fmt.Sprintf(format, args...)}
}
