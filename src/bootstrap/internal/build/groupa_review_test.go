package build

import (
	"strings"
	"testing"
)

// This file covers the three review (agent-ellis) findings on the Group-A fixes:
//   FIX 1 (FORK-A5) a non-constant trailing default GATES in sema instead of being
//         pasted verbatim at the call/construct site (a silent wrong value);
//   FIX 2 an or-pattern lowers to a real disjunction (never a constant-true test),
//         with an anti-silence backstop over unhandled pattern nodes;
//   FIX 3 a provided spec method calling another provided method dispatches through
//         the erased-receiver instance (no null callee reaching cc).

// --- FIX 1: FORK-A5 default-value gate -----------------------------------------

// TestDefaultReferencingParamGates: a parameter default that names another parameter
// is not a self-contained constant. Emit would paste `a` at the call site, where it
// wrongly resolves to a same-named global — a silent wrong value. It must gate cleanly
// in sema (no C emitted).
func TestDefaultReferencingParamGates(t *testing.T) {
	code, _, diags := Compile("fn f(a: int, b: int = a) -> int {\n\treturn a + b\n}\n" +
		"fn main() {\n\tprint f(1)\n}\n")
	if len(diags) == 0 || code != "" {
		t.Fatalf("a default referencing a parameter must gate; diags=%v code=%q", diags, code)
	}
	if !strings.Contains(diags[0].Msg, "must be a constant expression") {
		t.Fatalf("unexpected diagnostic: %v", diags[0].Msg)
	}
}

// TestFieldDefaultReferencingFieldGates: a struct field default that names another
// field is likewise not a self-contained constant and must gate cleanly.
func TestFieldDefaultReferencingFieldGates(t *testing.T) {
	code, _, diags := Compile("struct S {\n\tx: int\n\ty: int = x\n}\n" +
		"fn main() {\n\tprint S(x: 5).y\n}\n")
	if len(diags) == 0 || code != "" {
		t.Fatalf("a field default referencing a field must gate; diags=%v code=%q", diags, code)
	}
	if !strings.Contains(diags[0].Msg, "must be a constant expression") {
		t.Fatalf("unexpected diagnostic: %v", diags[0].Msg)
	}
}

// TestDefaultCallGates: a parameter default that is a function call (W2, the generic
// case) is not a self-contained constant — the callee need not be enqueued for
// monomorphization and would reach cc as bad C. It must gate cleanly.
func TestDefaultCallGates(t *testing.T) {
	code, _, diags := Compile("fn id(n: int) -> int {\n\treturn n\n}\n" +
		"fn f(a: int, b: int = id(5)) -> int {\n\treturn a + b\n}\n" +
		"fn main() {\n\tprint f(1)\n}\n")
	if len(diags) == 0 || code != "" {
		t.Fatalf("a call default must gate; diags=%v code=%q", diags, code)
	}
	if !strings.Contains(diags[0].Msg, "must be a constant expression") {
		t.Fatalf("unexpected diagnostic: %v", diags[0].Msg)
	}
}

// TestConstantParamDefaultRuns: a constant literal default still backfills — the
// working FORK-A5 case must stay green.
func TestConstantParamDefaultRuns(t *testing.T) {
	got := runProgram(t, "fn f(a: int, b: int = 10) -> int {\n\treturn a + b\n}\n"+
		"fn main() {\n\tprint f(1)\n}\n")
	if got != "11\n" {
		t.Fatalf("f(1) with default b=10 = %q, want %q", got, "11\n")
	}
}

// TestConstantFieldDefaultRuns: a constant literal field default still backfills at
// the construct site.
func TestConstantFieldDefaultRuns(t *testing.T) {
	got := runProgram(t, "struct S {\n\tx: int\n\ty: int = 100\n}\n"+
		"fn main() {\n\tprint S(x: 5).y\n}\n")
	if got != "100\n" {
		t.Fatalf("S(x: 5).y with default y=100 = %q, want %q", got, "100\n")
	}
}

// --- FIX 2: or-pattern lowering + pattern backstop -----------------------------

// TestVariantOrPatternRuns: a variant or-pattern 'Red | Green => 1' must lower to a
// real disjunction, so code(Blue) reaches the 'Blue => 2' arm instead of falling
// through a constant-true test on the first arm (the silent miscompile).
func TestVariantOrPatternRuns(t *testing.T) {
	got := runProgram(t, "enum Color {\n\tRed\n\tGreen\n\tBlue\n}\n"+
		"fn code(c: Color) -> int {\n\treturn match c {\n\t\tRed | Green => 1\n\t\tBlue => 2\n\t}\n}\n"+
		"fn main() {\n\tprint code(Blue)\n\tprint code(Red)\n\tprint code(Green)\n}\n")
	if got != "2\n1\n1\n" {
		t.Fatalf("variant or-pattern dispatch = %q, want %q", got, "2\n1\n1\n")
	}
}

// TestLiteralOrPatternRuns: a literal or-pattern '1 | 2 => "lo"' lowers to a
// disjunction too, so only 1 and 2 take the "lo" arm.
func TestLiteralOrPatternRuns(t *testing.T) {
	got := runProgram(t, "fn cls(n: int) -> str {\n\treturn match n {\n\t\t1 | 2 => \"lo\"\n\t\t_ => \"hi\"\n\t}\n}\n"+
		"fn main() {\n\tprint cls(1)\n\tprint cls(2)\n\tprint cls(9)\n}\n")
	if got != "lo\nlo\nhi\n" {
		t.Fatalf("literal or-pattern dispatch = %q, want %q", got, "lo\nlo\nhi\n")
	}
}

// --- FIX 3: A10 provided method calling another provided method ----------------
