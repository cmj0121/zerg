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

// TestForInListRuns: a for-in over a list value walks it in index order (the list is
// materialized once and dropped after the loop).
func TestForInListRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\tfor x in [1, 2, 3] { print x }\n}\n")
	if got != "1\n2\n3\n" {
		t.Fatalf("for-in over a list = %q, want %q", got, "1\n2\n3\n")
	}
}

// TestListTypeInParamRuns: a `list[int]` param type is a real container (zrt_list), so
// a function taking one by value compiles and runs.
func TestListTypeInParamRuns(t *testing.T) {
	got := runProgramRT(t, "fn f(x: list[int]) -> int {\n\treturn x[1]\n}\nfn main() { print f([4, 5, 6]) }\n")
	if got != "5\n" {
		t.Fatalf("list param = %q, want %q", got, "5\n")
	}
}

// TestTupleWithListTypeDiagnostic: a `(list[int], int)` tuple param copies as a whole,
// but the tuple copy/drop path does not yet handle a non-POD element, so it gates
// cleanly (a list element of a struct or a nested list is supported; a tuple is not).
func TestTupleWithListTypeDiagnostic(t *testing.T) {
	code, _, diags := Compile("fn f(p: (list[int], int)) -> int {\n\treturn p.1\n}\nfn main() { print 0 }\n")
	if len(diags) == 0 || code != "" {
		t.Fatalf("a tuple-with-list type must gate; diags=%v code=%q", diags, code)
	}
	if !strings.Contains(diags[0].Msg, "is not supported") {
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
