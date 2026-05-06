package syntax

import (
	"strings"
	"testing"
)

// v0.9 Unit 1 — `never` bottom type typeck tests. Coverage:
//
//   - never recognised at type position (`-> never`, `x: never`).
//   - bottom-type subtyping: a -> never call typechecks at any expected slot.
//   - fn-decl `-> never` requires every code path to diverge.
//   - reservation: user struct/enum/spec named `never` rejects.

// --- type-position recognition --------------------------------------------

func TestV09NeverReturnTypeResolves(t *testing.T) {
	src := "fn diverge() -> never { for { } }\n"
	prog := checkSrc(t, src)
	fn, ok := firstStmt(t, prog).(*FnDecl)
	if !ok {
		t.Fatalf("first stmt is %T, want *FnDecl", firstStmt(t, prog))
	}
	if fn.Return == nil || fn.Return.Resolved == nil {
		t.Fatalf("return type unresolved")
	}
	if fn.Return.Resolved.Kind != TypeNever {
		t.Errorf("return kind = %v, want TypeNever", fn.Return.Resolved.Kind)
	}
	if fn.Return.Resolved != TNever() {
		t.Errorf("return type is not the canonical tNever singleton")
	}
}

func TestV09NeverPrintsAsNever(t *testing.T) {
	if got := TNever().String(); got != "never" {
		t.Errorf("TNever().String() = %q, want %q", got, "never")
	}
}

// --- subtyping rule -------------------------------------------------------

func TestV09NeverCallFlowsIntoIntSlot(t *testing.T) {
	// A fn `-> never` returns a value of type `never`; assigning it to a
	// slot annotated `int` must typecheck because never <: int.
	src := "fn diverge() -> never { for { } }\n" +
		"fn use_it() -> int { return diverge() }\n"
	checkSrc(t, src)
}

func TestV09NeverCallFlowsIntoStrSlot(t *testing.T) {
	src := "fn diverge() -> never { for { } }\n" +
		"fn use_it() -> str { return diverge() }\n"
	checkSrc(t, src)
}

func TestV09NeverFlowsIntoLetAnnotated(t *testing.T) {
	src := "fn diverge() -> never { for { } }\n" +
		"fn use_it() { x: int = diverge() }\n"
	checkSrc(t, src)
}

// The reverse direction is rejected: a non-never value cannot flow into a
// `never` slot. let-binding a `never` slot from an int RHS rejects.
func TestV09NeverSlotRejectsNonNeverRHS(t *testing.T) {
	src := "fn use_it() { x: never = 1 }\n"
	checkErr(t, src, "cannot assign")
}

// --- fn-decl `-> never` divergence walker ---------------------------------

func TestV09NeverFnInfiniteLoopAccepted(t *testing.T) {
	checkSrc(t, "fn diverge() -> never { for { } }\n")
}

func TestV09NeverFnTailCallToNeverAccepted(t *testing.T) {
	src := "fn d1() -> never { for { } }\n" +
		"fn d2() -> never { d1() }\n"
	checkSrc(t, src)
}

func TestV09NeverFnIfElseAllDivergeAccepted(t *testing.T) {
	src := "fn diverge() -> never { for { } }\n" +
		"fn d2(x: int) -> never {\n" +
		"  if x > 0 { diverge() } else { diverge() }\n" +
		"}\n"
	checkSrc(t, src)
}

func TestV09NeverFnFallthroughRejected(t *testing.T) {
	// Body finishes without diverging — control would fall off the end,
	// which a `-> never` fn forbids.
	src := "fn bad() -> never { x := 1 }\n"
	checkErr(t, src, "non-diverging path")
}

func TestV09NeverFnIfWithoutElseRejected(t *testing.T) {
	src := "fn diverge() -> never { for { } }\n" +
		"fn bad(x: int) -> never { if x > 0 { diverge() } }\n"
	checkErr(t, src, "non-diverging path")
}

func TestV09NeverFnForLoopWithBreakRejected(t *testing.T) {
	src := "fn bad() -> never { for { break } }\n"
	checkErr(t, src, "non-diverging path")
}

// --- reservation diagnostic -----------------------------------------------

func TestV09NeverReservedAsStruct(t *testing.T) {
	got := checkErr(t, "struct never { x: int }\n", "is reserved")
	if !strings.Contains(got, "never") {
		t.Errorf("error %q does not mention never", got)
	}
}

func TestV09NeverReservedAsEnum(t *testing.T) {
	checkErr(t, "enum never { A, B }\n", "is reserved")
}

func TestV09NeverReservedAsSpec(t *testing.T) {
	checkErr(t, "spec never { fn m() }\n", "is reserved")
}

// --- diverging-arm extension (match) --------------------------------------

func TestV09NeverMatchArmDivergeAccepted(t *testing.T) {
	// A match arm whose body tail-calls a -> never fn participates in the
	// "diverging arm" treatment that match exhaustiveness / branch-merge
	// already applies to bare `return`. The borrow checker no longer
	// requires the moved binding's end-state to agree across the diverged
	// arm.
	src := "fn diverge() -> never { for { } }\n" +
		"fn use_it(n: int) {\n" +
		"  match n {\n" +
		"    1 => { diverge() }\n" +
		"    _ => { print 0 }\n" +
		"  }\n" +
		"}\n"
	checkSrc(t, src)
}
