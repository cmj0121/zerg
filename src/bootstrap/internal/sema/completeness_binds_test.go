package sema

import "testing"

// TestIfBindHeadTypes covers the `if x := e` binding head (GRAMMAR group 6): an optional
// or a Result operand binds its unwrapped element in the then-block (statement and
// expression forms), and anything else is a clean error.
func TestIfBindHeadTypes(t *testing.T) {
	const g = "fn g() -> int? { return 5 }\n"
	wantOK(t, g+"fn f() {\n\tif x := g() {\n\t\tprint x\n\t}\n}\n")
	wantOK(t, g+"fn f() -> int {\n\treturn if x := g() { x } else { 0 }\n}\n")
	// the else-branch does not see x.
	wantErr(t, g+"fn f() {\n\tif x := g() {\n\t\tprint x\n\t} else {\n\t\tprint x\n\t}\n}\n",
		"undefined name")
	// a Result head binds its Left — the same tag-0 slot an optional carries its value in,
	// and what makes `if v := <-ch { … }` read a channel receive.
	const r = "fn r() -> Result[int] { return 5 }\n"
	wantOK(t, r+"fn f() {\n\tif x := r() {\n\t\tprint x\n\t}\n}\n")
	// a head that is neither is rejected.
	wantErr(t, "fn f() {\n\tif x := 5 {\n\t\tprint x\n\t}\n}\n", "requires an optional or a Result value")
}

// TestDestructuringBindTypes covers the destructuring bind targets '(a, b) := e' and
// 'P{x, y} := e' (GRAMMAR bind-target): each leaf binds its inferred component type,
// and a tuple arity mismatch is a clean error.
func TestDestructuringBindTypes(t *testing.T) {
	wantOK(t, "fn f() {\n\t(a, b) := (1, 2)\n\tprint a + b\n}\n")
	wantOK(t, "fn f() {\n\t(a, (b, c)) := (1, (2, 3))\n\tprint a + b + c\n}\n")
	wantErr(t, "fn f() {\n\t(a, b, c) := (1, 2)\n\tprint a\n}\n", "cannot destructure")
	const d = "struct Div {\n\tq: int\n\tr: int\n}\n"
	wantOK(t, d+"fn f(v: Div) {\n\tDiv{q, r} := v\n\tprint q + r\n}\n")
	wantErr(t, d+"fn f(v: Div) {\n\tDiv{q, z} := v\n\tprint q\n}\n", "has no field")
}

// TestRangeMembershipTypes covers `v in lo..hi` (GRAMMAR group 4): membership over an
// integral range yields bool, a range binds to a name as a value, and a type mismatch
// between the tested value and the range element is a clean error.
func TestRangeMembershipTypes(t *testing.T) {
	wantOK(t, "fn f() {\n\tprint 5 in 0..10\n\tprint 10 in 0..=10\n}\n")
	wantOK(t, "fn f() {\n\tr := 0..10\n\tprint 5 in r\n}\n")
	wantErr(t, "fn f() {\n\tprint \"x\" in 0..10\n}\n", "cannot test membership")
	wantErr(t, "fn f() {\n\tr := 1.5..2.5\n\tprint r\n}\n", "integral bounds")
}
