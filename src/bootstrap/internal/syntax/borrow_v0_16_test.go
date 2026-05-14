package syntax

import "testing"

// ---------------------------------------------------------------------------
// v0.16 borrow-check tests for string interpolation.
//
// Each var piece is a primitive read (typeck restricts pieces to int / float /
// bool / str / byte / rune). Primitives copy on read, so no move-consume
// semantics apply; the borrow walker just descends into each Var piece's
// Ident so use-after-move on the underlying binding still surfaces.
// ---------------------------------------------------------------------------

func TestBorrowInterpReadsAreNonMoving(t *testing.T) {
	// `n` is read twice inside one interpolated string. Primitives copy, so
	// the second read must NOT see a moved state.
	checkSrc(t, "n := 1\nprint \"{n} and {n}\"\n")
}

func TestBorrowInterpReadAfterStrAssignAccepts(t *testing.T) {
	// `s` is reassigned; the new value is a fresh string. The read inside
	// the interp must succeed (no stale-state misfire).
	checkSrc(t,
		"mut s: str = \"a\"\ns = \"b\"\nprint \"now {s}\"\n")
}
