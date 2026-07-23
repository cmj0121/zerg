package build

import (
	"strings"
	"testing"
)

// This file covers the Group-A round-2 silent-miscompile fixes. Each item either now
// RUNS correctly (a compile+execute oracle) or FAILS LOUD with a clean diagnostic —
// never silently. Diagnostic tests assert no C is emitted for a rejected program.

// --- A1: `for x in <iterable>` iterative loop -----------------------------------

// TestForInRangeRuns: an exclusive range `0..3` yields 0,1,2 through a real counted
// loop over an int64_t loop var the body reads (was a `for(;;)` infinite loop).
func TestForInRangeRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\tfor x in 0..3 { print x }\n}\n")
	if got != "0\n1\n2\n" {
		t.Fatalf("for x in 0..3 = %q, want %q", got, "0\n1\n2\n")
	}
}

// TestForInInclusiveRangeRuns: an inclusive range `1..=3` includes the upper bound.
func TestForInInclusiveRangeRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\tfor x in 1..=3 { print x }\n}\n")
	if got != "1\n2\n3\n" {
		t.Fatalf("for x in 1..=3 = %q, want %q", got, "1\n2\n3\n")
	}
}

// TestForInFormerlyHangingTerminates: the exact re-audit repro — `for x in 0..3 { print
// 7 } print 99`. It used to emit `for(;;)` and hang printing 7 with 99 unreachable; it
// must now terminate, print 7 three times, then reach 99.
func TestForInFormerlyHangingTerminates(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\tfor x in 0..3 { print 7 }\n\tprint 99\n}\n")
	if got != "7\n7\n7\n99\n" {
		t.Fatalf("previously-hanging loop = %q, want %q", got, "7\n7\n7\n99\n")
	}
}

// TestForInArrayRuns: a fixed array `[int; 3]` iterates its elements, copying each into
// a real loop-var local the body reads.
func TestForInArrayRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\txs: [int; 3] = [10, 20, 30]\n\tfor x in xs { print x }\n}\n")
	if got != "10\n20\n30\n" {
		t.Fatalf("for x in xs = %q, want %q", got, "10\n20\n30\n")
	}
}

// TestForInListDiagnostic: a for-in over an unsupported iterable (a list value) is
// rejected with a clean diagnostic rather than miscompiled or hung.
func TestForInListDiagnostic(t *testing.T) {
	// An inferred list literal reaches the for-in element-typing check directly (no
	// explicit `list[T]` annotation, which A4 would gate first), so the for-in gate is
	// what fires.
	code, _, diags := Compile("fn main() {\n\tfor x in [1, 2, 3] { print x }\n}\n")
	if len(diags) == 0 || code != "" {
		t.Fatalf("a for-in over a list must gate; diags=%v code=%q", diags, code)
	}
	if !strings.Contains(diags[0].Msg, "for-in over") || !strings.Contains(diags[0].Msg, "not yet supported") {
		t.Fatalf("unexpected diagnostic: %v", diags[0].Msg)
	}
}

// --- A2: variant pattern payload sub-pattern (literal / nested) ------------------

// TestVariantPayloadLiteralPatternRuns: a literal payload sub-pattern `Leaf(0)` must
// contribute its own test, so `f(Leaf(9))` reaches the binding arm (=> 9) instead of
// falling into `Leaf(0) => -1` (the dropped-payload-test silent miscompile).
func TestVariantPayloadLiteralPatternRuns(t *testing.T) {
	got := runProgramRT(t, "enum Tree { Leaf(int); Node(int, int) }\n"+
		"fn f(t: Tree) -> int {\n\treturn match t {\n"+
		"\t\tLeaf(0) => -1\n\t\tLeaf(v) => v\n\t\tNode(a, b) => a + b\n\t}\n}\n"+
		"fn main() {\n\tprint f(Leaf(9))\n\tprint f(Leaf(0))\n\tprint f(Node(2, 3))\n}\n")
	if got != "9\n-1\n5\n" {
		t.Fatalf("variant payload-literal dispatch = %q, want %q", got, "9\n-1\n5\n")
	}
}

// TestVariantNestedPatternRuns: a nested payload sub-pattern `Wrap(A(v))` must recurse
// into the inner variant, binding v (previously an undeclared-name cc leak / dropped
// test).
func TestVariantNestedPatternRuns(t *testing.T) {
	got := runProgramRT(t, "enum Inner { A(int); B }\n"+
		"enum Outer { Wrap(Inner); Nil }\n"+
		"fn f(o: Outer) -> int {\n\treturn match o {\n"+
		"\t\tWrap(A(v)) => v\n\t\tWrap(B) => 0\n\t\tNil => -1\n\t}\n}\n"+
		"fn main() {\n\tprint f(Wrap(A(7)))\n\tprint f(Wrap(B))\n\tprint f(Nil)\n}\n")
	if got != "7\n0\n-1\n" {
		t.Fatalf("nested variant dispatch = %q, want %q", got, "7\n0\n-1\n")
	}
}

// --- A3: NUL in a string literal ------------------------------------------------

// TestNulInStringDiagnostic: a `\0` inside a `"..."` string is rejected (GRAMMAR 156),
// so it can never silently truncate the C string at runtime.
func TestNulInStringDiagnostic(t *testing.T) {
	code, _, diags := Compile("fn main() { print \"a\\0b\" }\n")
	if len(diags) == 0 || code != "" {
		t.Fatalf("a NUL in a string must gate; diags=%v code=%q", diags, code)
	}
	if !strings.Contains(diags[0].Msg, "NUL") {
		t.Fatalf("unexpected diagnostic: %v", diags[0].Msg)
	}
}

// TestNormalStringEscapesRun: a string with ordinary escapes (not NUL) still compiles
// and runs unchanged — the NUL check does not over-reject.
func TestNormalStringEscapesRun(t *testing.T) {
	got := runProgram(t, "fn main() { print \"a\\tb\" }\n")
	if got != "a\tb\n" {
		t.Fatalf("normal escapes = %q, want %q", got, "a\tb\n")
	}
}

// --- A4: composite generic type in a codegen type position ----------------------

// TestListTypeInParamDiagnostic: a `list[int]` param type reaching codegen must gate
// cleanly instead of lowering to a `void` field/param that cc rejects.
func TestListTypeInParamDiagnostic(t *testing.T) {
	code, _, diags := Compile("fn f(x: list[int]) -> int {\n\treturn 0\n}\nfn main() { print 0 }\n")
	if len(diags) == 0 || code != "" {
		t.Fatalf("a list param type must gate; diags=%v code=%q", diags, code)
	}
	if !strings.Contains(diags[0].Msg, "list type is not yet supported") {
		t.Fatalf("unexpected diagnostic: %v", diags[0].Msg)
	}
}

// TestTupleWithListTypeDiagnostic: the exact repro — a `(list[int], int)` tuple param
// (its carrier struct would get a `void f0` field) must gate cleanly.
func TestTupleWithListTypeDiagnostic(t *testing.T) {
	code, _, diags := Compile("fn f(p: (list[int], int)) -> int {\n\treturn p.1\n}\nfn main() { print 0 }\n")
	if len(diags) == 0 || code != "" {
		t.Fatalf("a tuple-with-list type must gate; diags=%v code=%q", diags, code)
	}
	if !strings.Contains(diags[0].Msg, "list type is not yet supported") {
		t.Fatalf("unexpected diagnostic: %v", diags[0].Msg)
	}
}

// --- A5: ptr[T]? --------------------------------------------------------------

// TestPtrOptDiagnostic: `ptr[T]?` is not a real type (GRAMMAR group 12) — a raw pointer
// is already nullable — so it is rejected rather than silently wrapped as a tagged
// optional.
func TestPtrOptDiagnostic(t *testing.T) {
	code, _, diags := Compile("fn main() {\n\tunsafe {\n\t\tx: int = 1\n\t\tp: ptr[int]? = addr(x)\n\t\tprint 0\n\t}\n}\n")
	if len(diags) == 0 || code != "" {
		t.Fatalf("ptr[T]? must gate; diags=%v code=%q", diags, code)
	}
	if !strings.Contains(diags[0].Msg, "may not be optional") {
		t.Fatalf("unexpected diagnostic: %v", diags[0].Msg)
	}
}

// TestPtrTypeStillCompiles: a plain `ptr[int]` binding still type-checks — the optional
// rejection does not touch the ordinary raw-pointer type.
func TestPtrTypeStillCompiles(t *testing.T) {
	_, _, diags := Compile("fn main() {\n\tunsafe {\n\t\tx: int = 1\n\t\tp: ptr[int] = addr(x)\n\t\tprint 0\n\t}\n}\n")
	if len(diags) != 0 {
		t.Fatalf("a plain ptr[int] should compile, got: %v", diags)
	}
}

// --- A6: `with <non-Ref resource>` (GATE) ---------------------------------------

// TestWithPodResourceDiagnostic: a `with` over a POD resource (no automatic cleanup)
// would bind and run its body with NO teardown scheduled — a silent no-op. Until the
// user teardown protocol exists it is rejected loudly.
func TestWithPodResourceDiagnostic(t *testing.T) {
	code, _, diags := Compile("struct Lock { pub n: int }\n" +
		"impl Lock { pub fn teardown() { print 99 } }\n" +
		"fn acquire() -> Lock { return Lock(n: 1) }\n" +
		"fn main() { with acquire() as l { print l.n } }\n")
	if len(diags) == 0 || code != "" {
		t.Fatalf("a with over a POD resource must gate; diags=%v code=%q", diags, code)
	}
	if !strings.Contains(diags[0].Msg, "automatic cleanup") {
		t.Fatalf("unexpected diagnostic: %v", diags[0].Msg)
	}
}

// TestWithRefResourceStillRuns: a `with` over a Ref resource keeps working — its
// zg_ref_drop teardown runs on scope exit — so the A6 gate does not regress the
// supported path.
func TestWithRefResourceStillRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\twith Ref(7) as f {\n\t\tprint deref(f)\n\t}\n}\n")
	if got != "7\n" {
		t.Fatalf("with Ref(7) = %q, want %q", got, "7\n")
	}
}
