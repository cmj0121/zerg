package build

import "testing"

// TestRangeMembershipRuns covers `v in lo..hi` half-open membership (GRAMMAR group 4):
// the lower bound is inclusive and the upper bound is exclusive, so 10 is not in 0..10.
func TestRangeMembershipRuns(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	src := "fn main() {\n" +
		"\tprint 5 in 0..10\n" +
		"\tprint 10 in 0..10\n" +
		"\tprint 0 in 0..10\n" +
		"}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if got := compileAndRun(t, cc, code); got != "true\nfalse\ntrue\n" {
		t.Fatalf("range membership = %q, want %q", got, "true\nfalse\ntrue\n")
	}
}

// TestRangeInclusiveMembershipRuns covers the inclusive form `v in lo..=hi`: the upper
// bound is included, so 10 is in 0..=10.
func TestRangeInclusiveMembershipRuns(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	src := "fn main() {\n\tprint 10 in 0..=10\n\tprint 11 in 0..=10\n}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if got := compileAndRun(t, cc, code); got != "true\nfalse\n" {
		t.Fatalf("inclusive range membership = %q, want %q", got, "true\nfalse\n")
	}
}

// TestByteRangeMembershipRuns covers a byte range membership `b in '0'..'9'` — the shape
// a lexer leans on heavily (a digit test).
func TestByteRangeMembershipRuns(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	src := "fn digit(b: byte) -> bool {\n\treturn b in b'0'..=b'9'\n}\n" +
		"fn main() {\n\tprint digit(b'5')\n\tprint digit(b'a')\n}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if got := compileAndRun(t, cc, code); got != "true\nfalse\n" {
		t.Fatalf("byte range membership = %q, want %q", got, "true\nfalse\n")
	}
}

// TestRangeForInStillIterates guards that giving a range a real type did not regress the
// for-in iterate form `for x in 0..N`, which is lowered inline (not through the range value).
func TestRangeForInStillIterates(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	src := "fn main() {\n\tmut s := 0\n\tfor x in 0..5 {\n\t\ts = s + x\n\t}\n\tprint s\n}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if got := compileAndRun(t, cc, code); got != "10\n" {
		t.Fatalf("for-in over range = %q, want %q", got, "10\n")
	}
}
