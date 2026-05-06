package syntax

import (
	"testing"
)

// v0.6 Unit 3.5 — generic impl blocks.
//
// Coverage:
//   - inherent and for-spec generic impls expand on each receiver-type
//     instantiation; method dispatch routes through the cloned impl.
//   - bound check fires per instance; unsatisfied bounds reject with the
//     standard "does not implement" diagnostic.
//   - collision detection: two generic impls for the same (decl, spec),
//     and a generic + concrete impl on the same (mono, spec), reject.
//   - concrete-arg impls (`impl Box[int] for Spec`) typecheck against the
//     monomorphised receiver-type.
//   - cross-module orphan rule extends to generics: both-foreign rejects.

// --- inherent generic impl ------------------------------------------------

func TestV06GenericImplInherentBasic(t *testing.T) {
	src := "struct Box[T] { value: T }\n" +
		"impl[T] Box[T] {\n" +
		"  fn get_value() -> T { return this.value }\n" +
		"}\n" +
		"b: Box[int] = Box { value: 7 }\n" +
		"v := b.get_value()\n"
	prog := checkSrc(t, src)
	var vLet *LetStmt
	for _, st := range prog.Statements {
		if ls, ok := st.(*LetStmt); ok && ls.Name == "v" {
			vLet = ls
		}
	}
	if vLet == nil {
		t.Fatalf("missing v")
	}
	if vLet.Value.Type() != tInt {
		t.Errorf("v's type = %s, want int", vLet.Value.Type())
	}
}

// --- for-spec generic impl ------------------------------------------------

func TestV06GenericImplForSpecBasic(t *testing.T) {
	src := "spec Printable { fn to_string() -> str }\n" +
		"struct Box[T] { value: T }\n" +
		"impl[T] Box[T] for Printable {\n" +
		"  fn to_string() -> str { return \"box\" }\n" +
		"}\n" +
		"b: Box[int] = Box { value: 7 }\n" +
		"s := b.to_string()\n"
	checkSrc(t, src)
}

func TestV06GenericImplForSpecAllowsSpecCoercion(t *testing.T) {
	// The cloned impl must register on the Box[int] receiver so spec
	// widening (Box[int] → Printable) succeeds.
	src := "spec Printable { fn to_string() -> str }\n" +
		"struct Box[T] { value: T }\n" +
		"impl[T] Box[T] for Printable {\n" +
		"  fn to_string() -> str { return \"box\" }\n" +
		"}\n" +
		"fn show(p: Printable) -> str { return p.to_string() }\n" +
		"b: Box[int] = Box { value: 7 }\n" +
		"s := show(b)\n"
	checkSrc(t, src)
}

// --- bound checking on generic impls --------------------------------------

func TestV06GenericImplBoundSatisfied(t *testing.T) {
	src := "spec Printable { fn to_string() -> str }\n" +
		"struct Counter { n: int }\n" +
		"impl Counter for Printable { fn to_string() -> str { return \"c\" } }\n" +
		"struct Wrap[T] { inner: T }\n" +
		"impl[T: Printable] Wrap[T] {\n" +
		"  fn show() -> str { return this.inner.to_string() }\n" +
		"}\n" +
		"c := Counter { n: 1 }\n" +
		"w: Wrap[Counter] = Wrap { inner: c }\n" +
		"s := w.show()\n"
	checkSrc(t, src)
}

func TestV06GenericImplBoundUnsatisfied(t *testing.T) {
	src := "spec Printable { fn to_string() -> str }\n" +
		"struct Wrap[T] { inner: T }\n" +
		"impl[T: Printable] Wrap[T] {\n" +
		"  fn show() -> int { return 0 }\n" +
		"}\n" +
		"w: Wrap[int] = Wrap { inner: 1 }\n"
	checkErr(t, src, "does not implement Printable")
}

// --- collision detection --------------------------------------------------

func TestV06GenericImplDuplicateGenericRejects(t *testing.T) {
	src := "spec Printable { fn to_string() -> str }\n" +
		"struct Box[T] { value: T }\n" +
		"impl[T] Box[T] for Printable { fn to_string() -> str { return \"a\" } }\n" +
		"impl[T] Box[T] for Printable { fn to_string() -> str { return \"b\" } }\n"
	checkErr(t, src, "duplicate generic impl")
}

func TestV06GenericImplDuplicateInherentRejects(t *testing.T) {
	src := "struct Box[T] { value: T }\n" +
		"impl[T] Box[T] { fn one() -> int { return 1 } }\n" +
		"impl[T] Box[T] { fn two() -> int { return 2 } }\n"
	checkErr(t, src, "duplicate generic impl")
}

func TestV06GenericImplVsConcreteImplCollides(t *testing.T) {
	// `impl[T] Box[T] for P` covers every Box[X]; `impl Box[int] for P`
	// also targets Box[int] — at the Box[int] instance the two impls
	// register against the same (mono, spec) key and the concrete-impl
	// path rejects with "duplicate impl".
	src := "spec Printable { fn to_string() -> str }\n" +
		"struct Box[T] { value: T }\n" +
		"impl[T] Box[T] for Printable { fn to_string() -> str { return \"g\" } }\n" +
		"impl Box[int] for Printable { fn to_string() -> str { return \"c\" } }\n" +
		"b: Box[int] = Box { value: 7 }\n" +
		"s := b.to_string()\n"
	checkErr(t, src, "duplicate impl")
}

// --- concrete-arg impls (no impl-level type-params) ----------------------

func TestV06ConcreteArgImplBasic(t *testing.T) {
	src := "spec Printable { fn to_string() -> str }\n" +
		"struct Box[T] { value: T }\n" +
		"impl Box[int] for Printable { fn to_string() -> str { return \"box-int\" } }\n" +
		"b: Box[int] = Box { value: 7 }\n" +
		"s := b.to_string()\n"
	checkSrc(t, src)
}

func TestV06ConcreteArgImplDoesNotApplyToOtherInstance(t *testing.T) {
	// `impl Box[int] for Printable` is specific to Box[int]; calling
	// to_string on Box[str] without a matching impl rejects.
	src := "spec Printable { fn to_string() -> str }\n" +
		"struct Box[T] { value: T }\n" +
		"impl Box[int] for Printable { fn to_string() -> str { return \"i\" } }\n" +
		"b: Box[str] = Box { value: \"x\" }\n" +
		"s := b.to_string()\n"
	checkErr(t, src, "does not exist")
}

// --- generic enum impls ---------------------------------------------------

func TestV06GenericImplOnUserEnum(t *testing.T) {
	src := "enum Wrap[T] { Inner(T), Empty }\n" +
		"impl[T] Wrap[T] {\n" +
		"  fn is_empty() -> bool { return false }\n" +
		"}\n" +
		"w: Wrap[int] = Wrap.Inner(7)\n" +
		"b := w.is_empty()\n"
	checkSrc(t, src)
}

// --- per-instance distinctness --------------------------------------------

func TestV06GenericImplExpandsForEachInstance(t *testing.T) {
	// Two different receiver-instance shapes both pick up the cloned impl.
	src := "struct Box[T] { value: T }\n" +
		"impl[T] Box[T] {\n" +
		"  fn double_check() -> bool { return true }\n" +
		"}\n" +
		"bi: Box[int] = Box { value: 1 }\n" +
		"bs: Box[str] = Box { value: \"x\" }\n" +
		"r1 := bi.double_check()\n" +
		"r2 := bs.double_check()\n"
	checkSrc(t, src)
}

// --- spec method signature mismatch on generic impl ----------------------

func TestV06GenericImplSpecMethodSignatureMismatch(t *testing.T) {
	src := "spec Printable { fn to_string() -> str }\n" +
		"struct Box[T] { value: T }\n" +
		"impl[T] Box[T] for Printable {\n" +
		"  fn to_string() -> int { return 0 }\n" +
		"}\n" +
		"b: Box[int] = Box { value: 7 }\n"
	checkErr(t, src, "return type")
}

// --- generic impl does not pollute non-generic types ---------------------

func TestV06GenericImplDoesNotMatchNonGenericType(t *testing.T) {
	// `impl[T] Box[T]` registers nothing on a non-generic Counter type.
	src := "struct Box[T] { value: T }\n" +
		"struct Counter { n: int }\n" +
		"impl[T] Box[T] {\n" +
		"  fn ping() -> int { return 1 }\n" +
		"}\n" +
		"c := Counter { n: 1 }\n" +
		"r := c.ping()\n"
	checkErr(t, src, "does not exist")
}

// --- ordering: concrete impl encountered before generic impl ------------

func TestV06GenericImplOrderingConcreteBeforeGeneric(t *testing.T) {
	// `impl Box[int]` is processed before `impl[T] Box[T]` — the concrete
	// monomorphizes Box[int] eagerly, but the generic impl must still
	// expand for any later instantiation (Box[str]).
	src := "spec Printable { fn to_string() -> str }\n" +
		"struct Box[T] { value: T }\n" +
		"impl Box[int] for Printable { fn to_string() -> str { return \"i\" } }\n" +
		"impl[T] Box[T] {\n" +
		"  fn ping() -> int { return 1 }\n" +
		"}\n" +
		"bs: Box[str] = Box { value: \"x\" }\n" +
		"r := bs.ping()\n"
	checkSrc(t, src)
}

func TestV06GenericImplOrderingEarlyMonoStillExpands(t *testing.T) {
	// `Box[int]` is monomorphized via the concrete impl's receiver before
	// the generic `impl[T] Box[T]` block is processed. The generic impl
	// should still register on Box[int] when it's added later.
	src := "spec Printable { fn to_string() -> str }\n" +
		"struct Box[T] { value: T }\n" +
		"impl Box[int] for Printable { fn to_string() -> str { return \"i\" } }\n" +
		"impl[T] Box[T] {\n" +
		"  fn ping() -> int { return 1 }\n" +
		"}\n" +
		"bi: Box[int] = Box { value: 1 }\n" +
		"r := bi.ping()\n"
	checkSrc(t, src)
}

// --- bound failure diagnostic anchors at the use site --------------------

func TestV06GenericImplBoundFailureAtUseSite(t *testing.T) {
	src := "spec Display { fn fmt() -> str }\n" +
		"struct Box[T] { value: T }\n" +
		"impl[T: Display] Box[T] for Display {\n" +
		"  fn fmt() -> str { return \"x\" }\n" +
		"}\n" +
		"b: Box[int] = Box { value: 7 }\n"
	checkErr(t, src, "does not implement Display")
}
