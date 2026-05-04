package syntax

// v0.6 Unit 5 — borrow check across monomorphized generics + ?, ??, ?.
//
// PLAN.md §Borrow check across generics pins:
//
//   * The composite vs primitive classification is keyed off canonical *Type
//     pointers, so monomorphized Option[T] / Result[T,E] / user generic
//     structs and enums inherit the v0.4 enum/struct treatment uniformly.
//   * `?` is a move-out site on its receiver (the inner Ok/Some moves out as
//     the expression's value; the Err/None path returns the original — also
//     a move).
//   * `??` reads its LHS as a match-scrutinee and moves whichever arm fires.
//     We conservatively treat both arms as taken so the LHS receiver and an
//     ident-shaped RHS are both consumed at the operator.
//   * `?.` is a read on the receiver; the expression's value is a fresh
//     synthetic Option[U] (the wrap is a *new* value so the receiver itself
//     is not moved).
//
// Helpers (checkSrc / checkErr / borrowErrSrc) come from typeck_test.go and
// borrow_test.go. Diagnostics flow through *BorrowError but the helpers
// already accept either error type.

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// (A) `?` propagation move semantics.
// ---------------------------------------------------------------------------

// `?` consumes its receiver: using the binding after `r?` rejects. We use a
// list-payload Result so the move-of-composite rule fires (Result is itself
// composite, but pinning a list payload makes the move site obvious).
func TestBorrowV06PropagateConsumesReceiver(t *testing.T) {
	src := "fn make() -> Result[list[int], str] {\n" +
		"return Result.Ok([1, 2])\n" +
		"}\n" +
		"fn observe(r: Result[list[int], str]) -> int { return 0 }\n" +
		"fn outer() -> Result[int, str] {\n" +
		"let r := make()\n" +
		"let v := r?\n" +
		"let n := observe(r)\n" +
		"return Result.Ok(v[0])\n" +
		"}\n"
	msg := borrowErrSrc(t, src, "use of moved value")
	if !strings.Contains(msg, `"r"`) {
		t.Errorf("error %q does not name r", msg)
	}
}

// `?` on a primitive-payload Option is a move on the receiver too — but the
// payload (int) is a primitive, so the BORROW checker only fires on the
// outer-binding move (the Option enum itself is composite).
func TestBorrowV06PropagateOnOptionConsumesReceiver(t *testing.T) {
	src := "fn maybe() -> int? { return Option.Some(7) }\n" +
		"fn outer() -> int? {\n" +
		"let r := maybe()\n" +
		"let v := r?\n" +
		"print r\n" +
		"return Option.Some(v)\n" +
		"}\n"
	borrowErrSrc(t, src, "use of moved value")
}

// `?` directly on a fresh call result has no source binding — no move
// applies. Inner walks find nothing to flip; the test pins the absence of
// a false-positive on the call form.
func TestBorrowV06PropagateOnFreshCallResultOK(t *testing.T) {
	src := "fn make() -> Result[int, str] { return Result.Ok(1) }\n" +
		"fn outer() -> Result[int, str] {\n" +
		"let v := make()?\n" +
		"return Result.Ok(v)\n" +
		"}\n"
	checkSrc(t, src)
}

// Two `?` calls on the same binding reject — the second observes the
// already-moved receiver.
func TestBorrowV06PropagateDoubleRejects(t *testing.T) {
	src := "fn make() -> Result[list[int], str] {\n" +
		"return Result.Ok([1, 2])\n" +
		"}\n" +
		"fn outer() -> Result[int, str] {\n" +
		"let r := make()\n" +
		"let a := r?\n" +
		"let b := r?\n" +
		"return Result.Ok(a[0])\n" +
		"}\n"
	borrowErrSrc(t, src, "use of moved value")
}

// ---------------------------------------------------------------------------
// (B) `??` coalesce move semantics.
// ---------------------------------------------------------------------------

// `??` consumes its LHS conservatively — using the LHS binding afterwards
// rejects.
func TestBorrowV06CoalesceConsumesLhs(t *testing.T) {
	src := "let opt: list[int]? = nil\n" +
		"let default := [9]\n" +
		"let v := opt ?? default\n" +
		"print opt\n"
	msg := borrowErrSrc(t, src, "use of moved value")
	if !strings.Contains(msg, `"opt"`) {
		t.Errorf("error %q does not name opt", msg)
	}
}

// `??` consumes its RHS too (conservative): when the None arm fires at
// runtime an ident-shaped RHS would be moved.
func TestBorrowV06CoalesceConsumesRhs(t *testing.T) {
	src := "let opt: list[int]? = nil\n" +
		"let fallback := [9]\n" +
		"let v := opt ?? fallback\n" +
		"print fallback[0]\n"
	msg := borrowErrSrc(t, src, "use of moved value")
	if !strings.Contains(msg, `"fallback"`) {
		t.Errorf("error %q does not name fallback", msg)
	}
}

// `??` with a literal RHS still consumes the LHS but does not flag the
// literal — pins the absence of a false positive on the literal arm.
func TestBorrowV06CoalesceLiteralRhsOnlyConsumesLhs(t *testing.T) {
	src := "let opt: int? = nil\n" +
		"let v: int = opt ?? 0\n" +
		"print v\n"
	checkSrc(t, src)
}

// `??` on a primitive-payload Option still consumes the LHS binding.
func TestBorrowV06CoalescePrimitiveLhsConsumed(t *testing.T) {
	src := "let opt: int? = Option.Some(1)\n" +
		"let v: int = opt ?? 0\n" +
		"print opt\n"
	// opt is Option[int] — composite (enum) — so the use-after-move rule fires.
	borrowErrSrc(t, src, "use of moved value")
}

// ---------------------------------------------------------------------------
// (C) `?.` safe-navigation read semantics.
// ---------------------------------------------------------------------------

// `?.` is a read on the receiver — the binding remains usable afterwards.
func TestBorrowV06SafeFieldAccessIsRead(t *testing.T) {
	src := "struct Box { v: int }\n" +
		"let b: Box? = Option.Some(Box { v: 7 })\n" +
		"let x := b?.v\n" +
		"let y := b?.v\n" +
		"print x\n" +
		"print y\n"
	checkSrc(t, src)
}

// `?.` chains stay reads — pin the absence of a false-positive on the
// outer chain link consuming the inner receiver.
func TestBorrowV06SafeFieldAccessChainIsRead(t *testing.T) {
	src := "struct Inner { v: int }\n" +
		"struct Outer { inner: Inner }\n" +
		"let o: Outer? = Option.Some(Outer { inner: Inner { v: 1 } })\n" +
		"let a := o?.inner?.v\n" +
		"let b := o?.inner?.v\n" +
		"print a\n" +
		"print b\n"
	checkSrc(t, src)
}

// ---------------------------------------------------------------------------
// (D) Cross-monomorph regression — v0.3/v0.4 rules across generic instances.
// ---------------------------------------------------------------------------

// Move-then-use on a `Box[int]` value rejects with the same diagnostic the
// v0.3 list/struct rule emits — composite classification keys off the
// canonical *Type, so the monomorphized struct inherits move semantics.
func TestBorrowV06MoveBoxIntThenUse(t *testing.T) {
	src := "struct Box[T] { value: T }\n" +
		"let b: Box[int] = Box { value: 7 }\n" +
		"let c := b\n" +
		"print b.value\n"
	msg := borrowErrSrc(t, src, "use of moved value")
	if !strings.Contains(msg, `"b"`) {
		t.Errorf("error %q does not name b", msg)
	}
}

// Move into a `Box[T]` field — the source binding is moved at the struct
// literal aggregation point. Same shape as v0.3's TestBorrowMoveIntoStructAndUseStruct.
func TestBorrowV06MoveListIntoBoxField(t *testing.T) {
	src := "struct Box[T] { value: T }\n" +
		"let xs := [1, 2]\n" +
		"let b: Box[list[int]] = Box { value: xs }\n" +
		"print xs[0]\n"
	borrowErrSrc(t, src, "use of moved value")
}

// Move a generic enum value (Option[list[int]]) on rebind — the canonical
// type is an enum, so the v0.4 enum-as-composite rule fires.
func TestBorrowV06MoveOptionOfListThenUse(t *testing.T) {
	src := "let o: list[int]? = Option.Some([1, 2])\n" +
		"let p := o\n" +
		"print o\n"
	borrowErrSrc(t, src, "use of moved value")
}

// Reading `Box[int].value` is a field access — receiver observed, not moved.
func TestBorrowV06ReadBoxFieldOK(t *testing.T) {
	src := "struct Box[T] { value: T }\n" +
		"let b: Box[int] = Box { value: 7 }\n" +
		"print b.value\n" +
		"print b.value\n"
	checkSrc(t, src)
}

// ---------------------------------------------------------------------------
// (E) NilLit / synthetic EnumLit lift.
// ---------------------------------------------------------------------------

// A bare `nil` is a constant — never a move. Pin the absence of any walker
// crash on NilLit at a binding site.
func TestBorrowV06NilLitNeverMoves(t *testing.T) {
	src := "let x: int? = nil\n" +
		"let y: int? = nil\n" +
		"print x\n"
	// x and y are Option[int] (composite enums). Both are bound from a fresh
	// NilLit — there is no source binding to move.
	checkSrc(t, src)
}

// `nil` at multiple positions (let, return, fn-arg) — the borrow walker
// stays clean.
func TestBorrowV06NilLitAtMultiplePositions(t *testing.T) {
	src := "fn take(x: int?) -> int { return 0 }\n" +
		"fn maybe() -> int? { return nil }\n" +
		"let r := take(nil)\n" +
		"print r\n"
	checkSrc(t, src)
}

// Synthetic Some-wrap at a fn-arg boundary moves the supplied list. After
// the call, the source binding is consumed (the lift wraps a *value*; the
// borrow walker recurses into the EnumLit payload, where the bare ident
// triggers the consume-on-aggregation rule).
func TestBorrowV06SomeLiftMovesListAtFnArg(t *testing.T) {
	src := "fn take(x: list[int]?) -> int { return 0 }\n" +
		"let xs := [1, 2]\n" +
		"let r := take(xs)\n" +
		"print xs[0]\n"
	// xs is moved into the synthesized Option.Some(xs); using xs after rejects.
	borrowErrSrc(t, src, "use of moved value")
}

// Synthetic Some-wrap at a let-binding boundary moves the supplied list.
func TestBorrowV06SomeLiftMovesListAtLetInit(t *testing.T) {
	src := "let xs := [1, 2]\n" +
		"let opt: list[int]? = xs\n" +
		"print xs[0]\n"
	borrowErrSrc(t, src, "use of moved value")
}

// Mixed list of `int?` with literals + nil + a Some(value) — the literal /
// nil elements never move; only the bare-ident Some-wrap moves its source.
// Here every element is a literal/nil so the list literal is admissible
// without moves.
func TestBorrowV06ListOfIntOptionLiteralsOK(t *testing.T) {
	src := "let xs: list[int?] = [1, nil, 2]\n" +
		"print xs[0]\n"
	checkSrc(t, src)
}

// Move a list[int?] binding on rebind — the outer list is composite.
func TestBorrowV06MoveListOfIntOptionThenUse(t *testing.T) {
	src := "let xs: list[int?] = [1, nil, 2]\n" +
		"let ys := xs\n" +
		"print xs[0]\n"
	borrowErrSrc(t, src, "use of moved value")
}
