package run

import "testing"

// ---------------------------------------------------------------------------
// v0.15 interpreter tests — tuple parallel reassignment.
//
// The "reads OLD / writes NEW" semantics is the load-bearing property; if
// the interpreter wrote slots eagerly during RHS eval the Fibonacci loop
// would diverge and the rotation cases would all collapse to a single
// pre-write value.
// ---------------------------------------------------------------------------

func TestRunMultiAssignFibTen(t *testing.T) {
	// After 10 iterations a holds fib(10) = 55 and b holds fib(11) = 89.
	src := `mut a := 0
mut b := 1
mut i := 0
for i < 10 {
	a, b = b, a + b
	i = i + 1
}
print a
print b
`
	expectOK(t, src, "55\n89\n")
}

func TestRunMultiAssignThreeWayRotation(t *testing.T) {
	src := `mut a := 1
mut b := 2
mut c := 3
a, b, c = c, a, b
print a
print b
print c
`
	expectOK(t, src, "3\n1\n2\n")
}

func TestRunMultiAssignTupleCallRHS(t *testing.T) {
	src := `fn divmod(a: int, b: int) -> tuple[int, int] { return (a // b, a % b) }
mut q := 0
mut r := 0
q, r = divmod(10, 3)
print q
print r
`
	expectOK(t, src, "3\n1\n")
}

// TestRunMultiAssignSwapPrimitive — the simplest case: a swap must read both
// values before writing either. If the implementation accidentally writes
// slot 0 before evaluating slot 1, b ends up with the new a's value.
func TestRunMultiAssignSwapPrimitive(t *testing.T) {
	src := `mut a := 11
mut b := 22
a, b = b, a
print a
print b
`
	expectOK(t, src, "22\n11\n")
}
