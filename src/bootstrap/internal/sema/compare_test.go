package sema

import "testing"

// TestNominalCompareRequiresImpl rejects '==' on a struct with no Eq impl: the
// operator is not a native C comparison, it needs an Eq the type does not have (B2).
func TestNominalCompareRequiresImpl(t *testing.T) {
	wantErr(t, "struct P {\n  x: int\n}\n"+
		"fn main() {\n  print P(1) == P(1)\n}", "requires an impl of Eq")
}

// TestNominalOrderRequiresOrd rejects '<' on a struct with no Ord impl.
func TestNominalOrderRequiresOrd(t *testing.T) {
	wantErr(t, "struct P {\n  x: int\n}\n"+
		"fn main() {\n  print P(1) < P(2)\n}", "requires an impl of Ord")
}

// TestNominalCompareWithDerive accepts the comparison once Eq/Ord is derived.
func TestNominalCompareWithDerive(t *testing.T) {
	wantOK(t, "#[derive(Eq, Ord)]\nstruct P {\n  x: int\n}\n"+
		"fn main() {\n  print P(1) == P(1)\n  print P(1) < P(2)\n}")
}

// TestPrimitiveCompareUnaffected keeps native comparison on primitives: no impl is
// required and the examples are unchanged.
func TestPrimitiveCompareUnaffected(t *testing.T) {
	wantOK(t, "fn main() {\n  print 1 == 2\n  print 1 < 2\n  print 1.5 < 2.0\n}")
}
