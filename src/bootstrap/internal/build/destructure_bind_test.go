package build

import "testing"

// TestTupleBindRuns covers the destructuring bind '(a, b) := e' (GRAMMAR bind-target):
// the tuple RHS is destructured component-wise into the bound names.
func TestTupleBindRuns(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	src := "fn main() {\n\t(a, b) := (1, 2)\n\tprint a\n\tprint b\n}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if got := compileAndRun(t, cc, code); got != "1\n2\n" {
		t.Fatalf("tuple bind = %q, want %q", got, "1\n2\n")
	}
}

// TestNestedTupleBindRuns covers a nested tuple bind target '(a, (b, c)) := e'.
func TestNestedTupleBindRuns(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	src := "fn main() {\n\t(a, (b, c)) := (1, (2, 3))\n\tprint a\n\tprint b\n\tprint c\n}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if got := compileAndRun(t, cc, code); got != "1\n2\n3\n" {
		t.Fatalf("nested tuple bind = %q, want %q", got, "1\n2\n3\n")
	}
}

// TestStructBindRuns covers the destructuring bind 'P{q, r} := e': the struct RHS's
// fields are bound by name.
func TestStructBindRuns(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	src := "struct Div {\n\tq: int\n\tr: int\n}\n" +
		"fn divmod(a: int, b: int) -> Div {\n\treturn Div(q: a / b, r: a % b)\n}\n" +
		"fn main() {\n\tDiv{q, r} := divmod(7, 2)\n\tprint q\n\tprint r\n}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if got := compileAndRun(t, cc, code); got != "3\n1\n" {
		t.Fatalf("struct bind = %q, want %q", got, "3\n1\n")
	}
}
