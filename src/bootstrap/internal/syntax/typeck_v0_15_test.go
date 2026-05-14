package syntax

import "testing"

// ---------------------------------------------------------------------------
// v0.15 typeck — bare-comma tuple parallel reassignment.
//
//	IDENT (',' IDENT)+ '=' expr (',' expr)*
//
// Rules verified here:
//   * Every target must resolve to a mut binding (let / const / undefined
//     reject with focused diagnostics).
//   * RHS must type as a tuple of arity matching the LHS count.
//   * Per-slot types must be compatible with the targets' declared types.
//
// The parser already rejects duplicate / non-ident LHS shapes at parse time
// — see parser_v0_15_test.go.
// ---------------------------------------------------------------------------

// --- positive ---------------------------------------------------------------

func TestCheckMultiAssignPair(t *testing.T) {
	checkSrc(t, "mut a := 0\nmut b := 1\na, b = b, a + b\nprint a\nprint b\n")
}

func TestCheckMultiAssignThreeWay(t *testing.T) {
	checkSrc(t, "mut a := 1\nmut b := 2\nmut c := 3\na, b, c = c, a, b\n")
}

func TestCheckMultiAssignTupleCallRHS(t *testing.T) {
	src := `fn divmod(a: int, b: int) -> tuple[int, int] { return (a // b, a % b) }
mut q := 0
mut r := 0
q, r = divmod(10, 3)
print q
`
	checkSrc(t, src)
}

// --- negative ---------------------------------------------------------------

func TestCheckMultiAssignRejectsLet(t *testing.T) {
	// `a` is bound via `:=` (immutable). The first slot rejects with the
	// let-specific diagnostic.
	checkErr(t,
		"a := 0\nmut b := 1\na, b = b, a + b\n",
		`cannot assign to "a" in multi-assign (immutable binding`)
}

func TestCheckMultiAssignRejectsConst(t *testing.T) {
	checkErr(t,
		"const a := 0\nmut b := 1\na, b = b, a + b\n",
		`cannot assign to "a" in multi-assign (declared with const)`)
}

func TestCheckMultiAssignRejectsUndefined(t *testing.T) {
	checkErr(t,
		"mut b := 1\na, b = b, b + 1\n",
		`undefined name "a"`)
}

func TestCheckMultiAssignRejectsArityMismatch(t *testing.T) {
	// RHS is a 3-tuple, LHS is 2 slots.
	checkErr(t,
		"mut a := 0\nmut b := 0\na, b = 1, 2, 3\n",
		"multi-assign expects 2 value(s), rhs has 3")
}

func TestCheckMultiAssignRejectsPerSlotTypeMismatch(t *testing.T) {
	// Slot 1 is `str`, RHS slot 1 is `int`.
	checkErr(t,
		`mut a: int = 0
mut b: str = "x"
a, b = 1, 2
`,
		`cannot assign int to "b" (declared str)`)
}
