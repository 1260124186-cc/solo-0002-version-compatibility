package semver

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, raw string) Version {
	t.Helper()
	v, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%q) returned unexpected error: %v", raw, err)
	}
	return v
}

func TestParseVersionBoundaries(t *testing.T) {
	valid := []string{
		"0.0.0",
		"1.2.3",
		"10.20.30",
		"4294967295.4294967295.4294967295",
	}
	for _, raw := range valid {
		if _, err := Parse(raw); err != nil {
			t.Errorf("Parse(%q) expected success, got %v", raw, err)
		}
	}

	invalid := []string{
		"",
		"1",
		"1.2",
		"1.2.3.4",
		".1.2",
		"1..2",
		"1.2.",
		"v1.2.3",
		"1.2.3-alpha",
		"1.2.3-rc.1",
		"1.2.3+build",
		"01.2.3",
		"1.02.3",
		"1.2.03",
		"00.0.0",
		"-1.0.0",
		"1.0.0 ",
		" 1.0.0",
		"1.x.0",
		"1.*.0",
		"*.0.0",
		"4294967296.0.0",
		"0.4294967296.0",
		"0.0.4294967296",
		"9999999999.0.0",
	}
	for _, raw := range invalid {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", raw)
		}
	}
}

func TestParseVersionNumericCeiling(t *testing.T) {
	max := mustParse(t, "4294967295.4294967295.4294967295")
	if max.Major != 4294967295 || max.Minor != 4294967295 || max.Patch != 4294967295 {
		t.Fatalf("ceiling version parsed as %v", max)
	}
	below := mustParse(t, "4294967295.4294967295.4294967294")
	if max.Compare(below) <= 0 {
		t.Fatalf("expected ceiling version to compare greater, got %d", max.Compare(below))
	}
	if below.Compare(max) >= 0 {
		t.Fatalf("expected lower version to compare smaller, got %d", below.Compare(max))
	}
	if max.Compare(max) != 0 {
		t.Fatalf("version must compare equal to itself")
	}

	// Longest legal raw string is exactly 32 bytes (10+1+10+1+10).
	longest := "4294967295.4294967295.4294967295"
	if len(longest) != 32 {
		t.Fatalf("test fixture length changed: %d", len(longest))
	}
	if _, err := Parse(longest); err != nil {
		t.Fatalf("32-byte ceiling version must be accepted: %v", err)
	}
}

func TestCompareOrdersBySegment(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.10.0", "1.9.0", 1},
		{"2.0.0", "1.99.99", 1},
		{"0.0.10", "0.0.9", 1},
		{"1.0.0", "1.0.1", -1},
		{"1.2.3", "1.2.3", 0},
	}
	for _, tc := range cases {
		got := mustParse(t, tc.a).Compare(mustParse(t, tc.b))
		if got != tc.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func mustConstraint(t *testing.T, raw string) Constraint {
	t.Helper()
	c, err := ParseConstraint(raw)
	if err != nil {
		t.Fatalf("ParseConstraint(%q) returned unexpected error: %v", raw, err)
	}
	return c
}

func TestCaretAndTildeUpperBounds(t *testing.T) {
	cases := []struct {
		raw      string
		matching []string
		excluded []string
	}{
		// ^1.2.3 := >=1.2.3 <2.0.0
		{"^1.2.3", []string{"1.2.3", "1.9.9", "1.2.4"}, []string{"1.2.2", "2.0.0", "0.9.9"}},
		// ^0.2.3 := >=0.2.3 <0.3.0 (zero major locks the minor)
		{"^0.2.3", []string{"0.2.3", "0.2.9", "0.2.10"}, []string{"0.2.2", "0.3.0", "0.1.9", "0.0.9"}},
		// ^0.0.3 := >=0.0.3 <0.0.4 (zero major and minor locks the patch)
		{"^0.0.3", []string{"0.0.3"}, []string{"0.0.2", "0.0.4", "0.1.0"}},
		// ^0.0.0 can only ever select exactly 0.0.0
		{"^0.0.0", []string{"0.0.0"}, []string{"0.0.1", "0.1.0", "1.0.0"}},
		// ~1.2.3 := >=1.2.3 <1.3.0
		{"~1.2.3", []string{"1.2.3", "1.2.9"}, []string{"1.2.2", "1.3.0", "2.0.0"}},
		// ~0.2.3 stays inside the 0.2 minor
		{"~0.2.3", []string{"0.2.3", "0.2.99"}, []string{"0.2.2", "0.3.0"}},
		// ~0.0.3 stays inside the 0.0 minor
		{"~0.0.3", []string{"0.0.3", "0.0.9"}, []string{"0.0.2", "0.1.0"}},
	}
	for _, tc := range cases {
		c := mustConstraint(t, tc.raw)
		for _, raw := range tc.matching {
			if !c.Matches(mustParse(t, raw)) {
				t.Errorf("constraint %s should match %s", tc.raw, raw)
			}
		}
		for _, raw := range tc.excluded {
			if c.Matches(mustParse(t, raw)) {
				t.Errorf("constraint %s must not match %s", tc.raw, raw)
			}
		}
	}
}

func TestComparatorIntersections(t *testing.T) {
	cases := []struct {
		raw      string
		matching []string
		excluded []string
	}{
		{"*", []string{"0.0.0", "4294967295.0.0"}, nil},
		{"1.5.0", []string{"1.5.0"}, []string{"1.4.9", "1.5.1"}},
		{"=1.5.0", []string{"1.5.0"}, []string{"1.4.9", "1.5.1"}},
		{">=1.0.0 <2.0.0", []string{"1.0.0", "1.9.9"}, []string{"0.9.9", "2.0.0"}},
		{">1.0.0 <=2.0.0", []string{"1.0.1", "2.0.0"}, []string{"1.0.0", "2.0.1"}},
		{">=1.0.0 >=1.5.0 <2.0.0", []string{"1.5.0", "1.9.9"}, []string{"1.4.9", "2.0.0"}},
		{"=1.5.0 >=1.0.0", []string{"1.5.0"}, []string{"1.4.9", "1.5.1"}},
		{"<2.0.0 >1.0.0", []string{"1.2.3", "1.9.9"}, []string{"1.0.0", "2.0.0"}},
		// Contradictory intersections match nothing.
		{">=2.0.0 <2.0.0", nil, []string{"1.9.9", "2.0.0", "2.0.1"}},
		{">1.0.0 <1.0.0", nil, []string{"1.0.0", "1.0.1", "0.9.9"}},
		{">1.0.0 <=1.0.0", nil, []string{"1.0.0", "1.0.1"}},
	}
	for _, tc := range cases {
		c := mustConstraint(t, tc.raw)
		for _, raw := range tc.matching {
			if !c.Matches(mustParse(t, raw)) {
				t.Errorf("constraint %q should match %s", tc.raw, raw)
			}
		}
		for _, raw := range tc.excluded {
			if c.Matches(mustParse(t, raw)) {
				t.Errorf("constraint %q must not match %s", tc.raw, raw)
			}
		}
	}
}

func TestIllegalConstraints(t *testing.T) {
	invalid := []string{
		"",
		" ",
		"\t",
		" *",
		"* ",
		" 1.2.3",
		"1.2.3 ",
		"\t1.0.0",
		"1.2",
		"1.2.3.4",
		"v1.2.3",
		"1.2.3-alpha.1",
		"1.2.3+build.7",
		"1.x.0",
		"1.*.0",
		">= 1.0.0",
		"^ 1.0.0",
		"||",
		"1.0.0 || 2.0.0",
		"^",
		"~",
		">",
		">=",
		"<=",
		"==",
		"!=1.0.0",
		"**",
		"^01.0.0",
		"1.0.0,2.0.0",
		strings.Repeat(">=1.0.0 ", 8) + ">=1.0.0", // 9 predicates, cap is 8
		strings.Repeat("a", 257),                  // over the 256 character limit
	}
	for _, raw := range invalid {
		if _, err := ParseConstraint(raw); err == nil {
			t.Errorf("ParseConstraint(%q) expected error, got nil", raw)
		}
	}
}

func TestRangesAtNumericCeilingDoNotOverflow(t *testing.T) {
	ceiling := mustParse(t, "4294967295.4294967295.4294967295")

	caret := mustConstraint(t, "^4294967295.0.0")
	if !caret.Matches(ceiling) {
		t.Errorf("ceiling major release must satisfy its own caret range")
	}
	if caret.Matches(mustParse(t, "4294967294.0.0")) {
		t.Errorf("caret at major ceiling must not include the previous major")
	}

	tinyPatch := mustConstraint(t, "^0.0.4294967295")
	if !tinyPatch.Matches(mustParse(t, "0.0.4294967295")) {
		t.Errorf("0.0.4294967295 must satisfy ^0.0.4294967295")
	}
	if tinyPatch.Matches(mustParse(t, "0.0.4294967294")) {
		t.Errorf("^0.0.4294967295 must not include the previous patch")
	}

	tilde := mustConstraint(t, "~4294967295.4294967295.0")
	if !tilde.Matches(mustParse(t, "4294967295.4294967295.0")) {
		t.Errorf("tilde at ceiling minor must include its anchor version")
	}
}
