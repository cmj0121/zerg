package syntax

import "testing"

// ---------------------------------------------------------------------------
// v0.15 borrow-check tests for tuple parallel reassignment.
//
// The RHS is walked for moves / borrows BEFORE any LHS slot is written, so
// `a, b = b, a + b` reads the pre-write values of `a` and `b`. The borrow
// checker only verifies each target's current state is writable; the
// "evaluate-into-temp" sequencing that makes the form correct in practice
// is owned by cgen / run, not by borrow.go.
// ---------------------------------------------------------------------------

// --- positive ---------------------------------------------------------------

// The canonical Fibonacci step. If borrow check misfires (e.g. flips `a` to
// Moved before checking `a + b` on the RHS), Check() will reject.
func TestBorrowMultiAssignFibStep(t *testing.T) {
	checkSrc(t, "mut a := 0\nmut b := 1\na, b = b, a + b\nprint b\n")
}

// Three-way rotation reuses values on the RHS — exercises that multiple
// reads of the same LHS name during RHS eval are accepted (primitive int
// reads are not move-consuming).
func TestBorrowMultiAssignRotate(t *testing.T) {
	checkSrc(t, "mut a := 1\nmut b := 2\nmut c := 3\na, b, c = c, a, b\n")
}

// --- negative ---------------------------------------------------------------

// A composite target whose value has been moved is rejected on use; multi-
// assign must surface the same diagnostic the single-assign path does.
func TestBorrowMultiAssignRejectsMovedTarget(t *testing.T) {
	src := `mut xs := [1, 2]
mut ys := [3, 4]
zs := xs
xs, ys = ys, [9]
`
	borrowErrSrc(t, src, `use of moved value: "xs"`)
}

// A composite target captured by `for x in xs` is BorrowedShared during the
// body; multi-assigning to it must reject with the borrow diagnostic. Mirrors
// the precedent at borrow_test.go:248 for the single-assign / index-assign
// paths, exercising the bsBorrowedShared branch of checkMultiAssign.
func TestBorrowMultiAssignRejectsBorrowedSharedTarget(t *testing.T) {
	src := `mut xs := [1, 2, 3]
mut ys := [9, 9]
for x in xs {
xs, ys = ys, [0]
}
`
	borrowErrSrc(t, src, "borrowed")
}
