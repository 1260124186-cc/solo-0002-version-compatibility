package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequirementInputAcceptsStringAndObject(t *testing.T) {
	var input ReleaseInput
	body := `{"version":"1.0.0","requires":{"core-engine":"^1.0.0","secret-store":{"constraint":"~2.0.0","visibility":"internal"}}}`
	if err := json.Unmarshal([]byte(body), &input); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if got := input.Requires["core-engine"]; got.Constraint != "^1.0.0" || got.IsInternal() {
		t.Fatalf("string requirement decoded wrong: %+v", got)
	}
	if got := input.Requires["secret-store"]; got.Constraint != "~2.0.0" || !got.IsInternal() {
		t.Fatalf("object requirement decoded wrong: %+v", got)
	}
	if err := ValidateRelease(input, "core-facade"); err != nil {
		t.Fatalf("valid mixed release rejected: %v", err)
	}
	internal := InternalDependencyIDs(input.Requires)
	if len(internal) != 1 || internal[0] != "secret-store" {
		t.Fatalf("expected secret-store as the only internal edge, got %v", internal)
	}
}

func TestRequirementInputRejectsUnknownFieldsAndBadVisibility(t *testing.T) {
	cases := []string{
		`{"version":"1.0.0","requires":{"x":{"constraint":"*","unexpected":1}}}`,
		`{"version":"1.0.0","requires":{"x":{"constraint":"*","visibility":"secret"}}}`,
		`{"version":"1.0.0","requires":{"x":{"visibility":"internal"}}}`,
	}
	for _, body := range cases {
		var input ReleaseInput
		err := json.Unmarshal([]byte(body), &input)
		if err == nil {
			t.Fatalf("expected rejection for %s", body)
		}
	}
}

func TestRequirementInputRoundTrips(t *testing.T) {
	input := map[string]RequirementInput{
		"core-engine":  {Constraint: "^1.0.0", Visibility: Public},
		"secret-store": {Constraint: "~2.0.0", Visibility: Internal},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"core-engine":"^1.0.0"`) {
		t.Fatalf("public edge should encode as a bare string, got %s", data)
	}
	var decoded map[string]RequirementInput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !decoded["secret-store"].IsInternal() || decoded["core-engine"].IsInternal() {
		t.Fatalf("round trip lost edge visibility: %+v", decoded)
	}
}
