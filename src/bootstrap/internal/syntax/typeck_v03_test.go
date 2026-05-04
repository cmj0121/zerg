package syntax

import (
	"testing"
)

// ---------------------------------------------------------------------------
// v0.3 typeck tests — Unit 1: list-element assignment placeholder.
//
// At Unit 1 the parser admits `xs[i] = v` but typeck stubs it with a
// "v0.3 work in progress" diagnostic so the v0.0 / v0.1 / v0.2 corpora
// are unaffected and any program that exercises the new surface fails
// with a clear message until Unit 3 (borrow checker) lands.
// ---------------------------------------------------------------------------

// TestCheckIndexAssignWorkInProgress confirms typeck rejects `xs[i] = v` with
// the placeholder message — Units 2 and 3 will replace this with real mut /
// borrow / type checking.
func TestCheckIndexAssignWorkInProgress(t *testing.T) {
	src := "mut xs := [1, 2, 3]\nxs[0] = 99\n"
	checkErr(t, src, "v0.3 work in progress")
}

// TestCheckIdentAssignStillWorks is the regression guard — broadening the
// AST shape must not break the v0.1 simple-assignment path. typeck still
// validates the mut / let / type rules for `x = expr` exactly as before.
func TestCheckIdentAssignStillWorks(t *testing.T) {
	checkSrc(t, "mut x := 1\nx = 2\n")
}

// TestCheckIdentAssignLetStillRejected confirms the v0.1 "cannot assign to
// let-bound name" rule still fires after the AssignStmt restructure.
func TestCheckIdentAssignLetStillRejected(t *testing.T) {
	checkErr(t, "let x := 1\nx = 2\n", "declared with let")
}
