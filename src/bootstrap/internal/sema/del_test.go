package sema

import "testing"

// TestDelLiveness covers the flow-consistent liveness analysis for `del` (GRAMMAR
// group 11, DESIGN-1d §5.1): a use of a name after it is revoked is a compile error
// (turning a would-be use-after-free into a clean diagnostic), while an idempotent or
// symmetric revoke is fine.
func TestDelLiveness(t *testing.T) {
	// p5b: a straight-line use after del is rejected.
	wantErr(t, "fn main() {\n r := Ref(3)\n del r\n print deref(r)\n}", "used after del")

	// a reassignment after del is a use, also rejected.
	wantErr(t, "fn main() {\n mut r := Ref(3)\n del r\n r = Ref(4)\n}", "used after del")

	// asymmetric branch: del on one path leaves the name inconsistent, so a later use
	// (r6-style) is rejected.
	wantErr(t, "fn f(c: bool) {\n r := Ref(1)\n if c {\n  del r\n }\n print deref(r)\n}",
		"used after del on some paths")

	// a reassignment at an inconsistent merge is rejected (r6).
	wantErr(t, "fn f(c: bool) {\n mut r := Ref(1)\n if c {\n  del r\n }\n r = Ref(2)\n}",
		"used after del on some paths")

	// del of an undefined name is still reported.
	wantErr(t, "fn main() {\n del nope\n}", "undefined name")
}

// TestDelLivenessOK covers the valid shapes the analysis must NOT reject: a use
// before del, an idempotent (single) revoke with no later use, and a symmetric
// revoke on every branch.
func TestDelLivenessOK(t *testing.T) {
	// use before del is fine; nothing uses r afterwards.
	wantOK(t, "fn main() {\n r := Ref(7)\n print deref(r)\n del r\n}")

	// a single revoke with no later use is fine.
	wantOK(t, "fn main() {\n r := Ref(1)\n del r\n}")

	// symmetric revoke on both branches merges to dead consistently; no later use.
	wantOK(t, "fn f(c: bool) {\n r := Ref(1)\n if c {\n  del r\n } else {\n  del r\n }\n}")

	// del in a branch, but the name is only used on the path that did not del it.
	wantOK(t, "fn f(c: bool) {\n r := Ref(1)\n if c {\n  del r\n } else {\n  print deref(r)\n }\n}")
}
