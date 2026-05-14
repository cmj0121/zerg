package syntax

import "testing"

// ---------------------------------------------------------------------------
// v0.19 borrow-check tests for self-rehydrating multi-assign on composites.
//
// The v0.15 form `a, b = b, a + b` was admitted for primitive ints because
// reading a primitive is a copy. For composite types (lists, structs like
// math.BigInt), the same form was rejected by the loop-body move guard —
// the bare-ident element `b` looked like a move into the tuple temp.
//
// v0.19 special-cases the RHS tuple-literal walk in checkMultiAssign: when
// an element is a bare ident naming one of this statement's own targets,
// it is treated as a read (not a move). The LHS write that follows rebinds
// the slot, so the post-statement state matches the pre-statement state
// even inside a loop body.
// ---------------------------------------------------------------------------

// --- positive ---------------------------------------------------------------

// Composite swap inside a loop body — the case the v0.19 rule is for.
// Without the rule, the bare-ident `b` would consume the loop-outer
// binding and the second iteration would observe a moved value.
func TestBorrowMultiAssignCompositeSwapInLoop(t *testing.T) {
	src := `mut a := [1, 2]
mut b := [3, 4]
mut i := 0
for i < 3 {
a, b = b, a
i = i + 1
}
print a[0]
print b[0]
`
	checkSrc(t, src)
}

// Three-way rotation over composites inside a loop. Each RHS element is a
// bare ident naming one of the three targets, so all three go through the
// rehydrate read path.
func TestBorrowMultiAssignCompositeRotateInLoop(t *testing.T) {
	src := `mut a := [1]
mut b := [2]
mut c := [3]
mut i := 0
for i < 2 {
a, b, c = c, a, b
i = i + 1
}
`
	checkSrc(t, src)
}

// Composite multi-assign at top level (no loop) — the rehydrate rule is
// independent of loop depth, so the bare-ident swap admits cleanly.
func TestBorrowMultiAssignCompositeSwapTopLevel(t *testing.T) {
	src := `mut a := [1, 2]
mut b := [3, 4]
a, b = b, a
print a[0]
print b[0]
`
	checkSrc(t, src)
}

// Mixed shape: one element is a self-target bare ident (rehydrate), the
// other is a fresh literal (no move). Both arms must admit.
func TestBorrowMultiAssignMixedRehydrateAndLiteral(t *testing.T) {
	src := `mut a := [1, 2]
mut b := [3, 4]
mut i := 0
for i < 2 {
a, b = b, [9]
i = i + 1
}
`
	checkSrc(t, src)
}

// --- negative ---------------------------------------------------------------

// A non-target composite ident in the RHS is still consumed (moved). The
// rehydrate rule only fires for bare idents that name one of THIS
// statement's targets — unrelated bindings must still surface the move.
func TestBorrowMultiAssignNonTargetIdentStillMoves(t *testing.T) {
	src := `mut a := [1, 2]
mut b := [3, 4]
c := [5, 6]
a, b = c, [9]
print c[0]
`
	borrowErrSrc(t, src, `use of moved value: "c"`)
}
