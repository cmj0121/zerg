package build

import (
	"strings"
	"testing"
)

// --- SLICE 4: pattern family A2/A3/A4 + exhaustiveness + mono ------------------

// TestTuplePatternBindRuns covers A2: a tuple pattern `(x, y)` destructures the
// subject's carrier fields (.f0/.f1) and binds each element, so `x + y` sees 3 and 4.
// A tuple of all bindings is also irrefutable, so this lone arm is exhaustive (no `_`
// needed) — see the exhaustiveness fix below.
func TestTuplePatternBindRuns(t *testing.T) {
	got := runProgram(t, "fn f() -> int {\n\treturn match (3, 4) {\n\t\t(x, y) => x + y\n\t}\n}\n"+
		"fn main() { print f() }\n")
	if got != "7\n" {
		t.Fatalf("tuple pattern match = %q, want %q", got, "7\n")
	}
}

// TestStructPatternBindRuns covers A3: a struct pattern `P { x, y }` locates each
// field at place.zg_<field> and binds the shorthand names.
func TestStructPatternBindRuns(t *testing.T) {
	got := runProgram(t, "struct P { pub x: int; pub y: int }\n"+
		"fn f(p: P) -> int {\n\treturn match p {\n\t\tP { x, y } => x + y\n\t}\n}\n"+
		"fn main() { print f(P(x: 3, y: 8)) }\n")
	if got != "11\n" {
		t.Fatalf("struct pattern match = %q, want %q", got, "11\n")
	}
}

// TestRangeArmRuns covers A4: a range arm tests containment — `0..10` is a half-open
// `>= 0 && < 10`, `10..=100` is inclusive `>= 10 && <= 100` — and binds nothing. The
// three inputs land in distinct arms.
func TestRangeArmRuns(t *testing.T) {
	got := runProgram(t, "fn g(n: int) -> int {\n"+
		"\treturn match n {\n\t\t0..10 => 1\n\t\t10..=100 => 2\n\t\t_ => 3\n\t}\n}\n"+
		"fn main() {\n\tprint g(5)\n\tprint g(50)\n\tprint g(500)\n}\n")
	if got != "1\n2\n3\n" {
		t.Fatalf("range arm match = %q, want %q", got, "1\n2\n3\n")
	}
}

// TestAsPatternBindRuns covers the as-pattern: `0 as z` matches the literal 0 and also
// binds the whole matched value to z, so f(0) yields z+100 while f(7) falls to `other`.
func TestAsPatternBindRuns(t *testing.T) {
	got := runProgram(t, "fn f(n: int) -> int {\n"+
		"\treturn match n {\n\t\t0 as z => z + 100\n\t\tother => other\n\t}\n}\n"+
		"fn main() {\n\tprint f(0)\n\tprint f(7)\n}\n")
	if got != "100\n7\n" {
		t.Fatalf("as-pattern match = %q, want %q", got, "100\n7\n")
	}
}

// TestSingleTupleArmExhaustive covers the exhaustiveness fix for A2/A3: a single tuple
// arm whose elements are all bindings is irrefutable, so the match is exhaustive with
// NO `_` arm. Before the fix this was falsely rejected as non-exhaustive. (Because the
// arm is now a catch-all, a redundant trailing `_` would correctly become an
// unreachable-arm error — the two behaviours are the same isCatchAll fact.)
func TestSingleTupleArmExhaustive(t *testing.T) {
	code, _, diags := Compile("fn f() -> int {\n\treturn match (1, 2) {\n\t\t(a, b) => a + b\n\t}\n}\n" +
		"fn main() { print f() }\n")
	if len(diags) != 0 || code == "" {
		t.Fatalf("a lone irrefutable tuple arm should be exhaustive, got diags=%v", diags)
	}
}

// TestListPatternGate covers the A8 cut for list patterns: a general list[T] pattern
// has no runtime, so it must fail cleanly rather than mis-index.
func TestListPatternGate(t *testing.T) {
	src := "fn f(xs: list[int]) -> int {\n\treturn match xs {\n\t\t[a, b] => a + b\n\t\t_ => 0\n\t}\n}\n" +
		"fn main() { print 0 }\n"
	code, _, diags := Compile(src)
	if len(diags) == 0 || code != "" {
		t.Fatalf("a list pattern should be gated, got code=%q diags=%v", code, diags)
	}
	if !strings.Contains(diags[0].Msg, "list pattern is not yet supported") {
		t.Fatalf("expected the list-pattern gate diagnostic, got: %v", diags)
	}
}
