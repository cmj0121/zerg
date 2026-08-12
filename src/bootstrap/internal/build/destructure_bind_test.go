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
	src := "struct Div {\n\tpub q: int\n\tpub r: int\n}\n" +
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

// TestTupleDestructureStrBalanced (Bug 2) covers a destructuring bind of a non-POD tuple:
// the RHS temp holding the two managed-str leaves is scheduled for teardown, so each
// field is released once at scope exit rather than leaking. Compiled under ASan+UBSan
// with the counting allocator (runProgramRTBalanced), a pass asserts a zero alloc/free
// balance in addition to the exact stdout.
func TestTupleDestructureStrBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"fn main() -> Result[nil] {\n"+
			"\t(a, b) := (\"x\" + \"y\", \"z\" + \"w\")\n"+
			"\tprint a\n"+
			"\tprint b\n"+
			"\treturn nil\n}\n")
	if want := "xy\nzw\n"; got != want {
		t.Fatalf("tuple destructure str: got %q, want %q", got, want)
	}
}

// TestStructDestructureStrBalanced (Bug 2) covers a destructuring bind of a non-POD
// struct: the RHS temp registers its generated drop-env thunk, releasing both str fields
// once at scope exit.
func TestStructDestructureStrBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"struct P { pub a: str; pub b: str }\n"+
			"fn main() -> Result[nil] {\n"+
			"\tP{a, b} := P(\"x\" + \"y\", \"z\" + \"w\")\n"+
			"\tprint a\n"+
			"\tprint b\n"+
			"\treturn nil\n}\n")
	if want := "xy\nzw\n"; got != want {
		t.Fatalf("struct destructure str: got %q, want %q", got, want)
	}
}

// TestTupleDestructureRefBalanced (Bug 2) covers a destructuring bind of a tuple of Refs:
// each bare-Ref leaf place is registered with the zg_ref_drop slot guard, so both boxes
// are released once at scope exit.
func TestTupleDestructureRefBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"fn main() -> Result[nil] {\n"+
			"\t(a, b) := (Ref(1), Ref(2))\n"+
			"\tprint deref(a)\n"+
			"\tprint deref(b)\n"+
			"\treturn nil\n}\n")
	if want := "1\n2\n"; got != want {
		t.Fatalf("tuple destructure ref: got %q, want %q", got, want)
	}
}

// TestTupleDestructureStrLeafUsedBalanced (Bug 2) covers a destructured leaf that is
// itself later copied out: the copy retains independently and the temp's own field drop
// still fires exactly once — no double-free.
func TestTupleDestructureStrLeafUsedBalanced(t *testing.T) {
	got := runProgramRTBalanced(t,
		"fn main() -> Result[nil] {\n"+
			"\t(a, b) := (\"x\" + \"y\", \"z\" + \"w\")\n"+
			"\tc := a\n"+
			"\tprint c\n"+
			"\tprint b\n"+
			"\treturn nil\n}\n")
	if want := "xy\nzw\n"; got != want {
		t.Fatalf("tuple destructure leaf used: got %q, want %q", got, want)
	}
}
