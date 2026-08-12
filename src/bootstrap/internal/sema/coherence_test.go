package sema

import "testing"

// TestCoherenceConflict reports a duplicate 'impl Spec for Type': at most one impl
// of a spec for a type may exist program-wide (DESIGN-1c §2.1, U2).
func TestCoherenceConflict(t *testing.T) {
	src := "spec Eq {\n  fn eq(o: This) -> bool\n}\n" +
		"struct Point {\n  pub x: int\n}\n" +
		"impl Eq for Point {\n}\n" +
		"impl Eq for Point {\n}"
	wantErr(t, src, "conflicting impl")
}

// TestOrphanImpl reports an orphan 'impl Eq for int': a blessed spec and a
// primitive type are both foreign, so the impl owns neither (DESIGN-1c §2.2, U2).
func TestOrphanImpl(t *testing.T) {
	wantErr(t, "impl Eq for int {\n}", "orphan impl")
}

// TestOrphanOKWhenTypeLocal accepts an impl of a foreign (blessed) spec for a local
// type: owning the target type satisfies the orphan rule.
func TestOrphanOKWhenTypeLocal(t *testing.T) {
	wantOK(t, "struct Point {\n  pub x: int\n}\n"+
		"impl Eq for Point {\n  fn eq(o: This) -> bool { return true }\n}")
}

// TestSuperSpecRequirementMissing reports an 'impl Ord for Point' with no matching
// 'impl Eq for Point': a super-spec of the implemented spec must also be
// implemented for the type (DESIGN-1c §2/§3.4, U3).
func TestSuperSpecRequirementMissing(t *testing.T) {
	src := "struct Point {\n  pub x: int\n}\n" +
		"impl Ord for Point {\n  fn lt(o: This) -> bool { return true }\n}"
	wantErr(t, src, "no impl Eq for Point")
}

// TestSuperSpecRequirementSatisfied accepts an 'impl Ord for Point' when 'impl Eq
// for Point' is also present.
func TestSuperSpecRequirementSatisfied(t *testing.T) {
	src := "struct Point {\n  pub x: int\n}\n" +
		"impl Eq for Point {\n  fn eq(o: This) -> bool { return true }\n}\n" +
		"impl Ord for Point {\n  fn lt(o: This) -> bool { return true }\n}"
	wantOK(t, src)
}
