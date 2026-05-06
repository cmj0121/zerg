package syntax

import (
	"testing"
)

// ---------------------------------------------------------------------------
// v0.3 typeck tests — Unit 3: list-element assignment, real check.
//
// Unit 3 replaces the Unit-1 "work in progress" placeholder with real
// mutability / type checking on the IndexExpr LHS. Borrow-state checking
// (Owned / Moved / BorrowedShared) is the borrow checker's job and lives in
// borrow_test.go alongside the rest of the v0.3 borrow rules.
// ---------------------------------------------------------------------------

// TestCheckIndexAssignOnMutListOK admits the canonical mut-list element write.
func TestCheckIndexAssignOnMutListOK(t *testing.T) {
	checkSrc(t, "mut xs := [1, 2, 3]\nxs[0] = 99\n")
}

// TestCheckIndexAssignOnLetListRejected flags the immutable-binding case with
// a precise diagnostic that points the user at `mut`.
func TestCheckIndexAssignOnLetListRejected(t *testing.T) {
	checkErr(t, "xs := [1, 2, 3]\nxs[0] = 99\n", "declare with mut to allow element mutation")
}

// TestCheckIndexAssignTypeMismatch — RHS type must match the list element type.
func TestCheckIndexAssignTypeMismatch(t *testing.T) {
	checkErr(t, "mut xs := [1, 2, 3]\nxs[0] = \"hi\"\n", "cannot assign str")
}

// TestCheckIndexAssignNonListRejected — assigning into an indexed non-list
// (e.g. an int variable) is rejected with the "cannot index-assign" diagnostic.
func TestCheckIndexAssignNonListRejected(t *testing.T) {
	checkErr(t, "mut x := 5\nx[0] = 1\n", "cannot index-assign")
}

// TestCheckIdentAssignStillWorks is the regression guard — broadening the
// AST shape must not break the v0.1 simple-assignment path. typeck still
// validates the mut / immutable / type rules for `x = expr` exactly as before.
func TestCheckIdentAssignStillWorks(t *testing.T) {
	checkSrc(t, "mut x := 1\nx = 2\n")
}

// TestCheckIdentAssignLetStillRejected confirms the v0.1 "cannot assign to
// immutable-bound name" rule still fires after the AssignStmt restructure.
func TestCheckIdentAssignLetStillRejected(t *testing.T) {
	checkErr(t, "x := 1\nx = 2\n", "immutable binding")
}

// ---------------------------------------------------------------------------
// v0.3 typeck tests — Unit 2: `push(xs, v)` and `clone(xs)` builtins.
//
// Both register in the same fn table where `len` lives and dispatch at the
// call site. push has tight rules (mut-bound list, arity 2, element type
// match); clone is permissive on composites and rejects primitives. Move /
// borrow tracking lands at Unit 3 — these tests assert ONLY the typing rules.
// ---------------------------------------------------------------------------

// --- positive: push ---------------------------------------------------------

// TestCheckPushOnMutList admits the canonical `push(mut_list, v)` shape and
// confirms the call expression is typed as void.
func TestCheckPushOnMutList(t *testing.T) {
	prog := checkSrc(t, "mut xs := [1, 2]\npush(xs, 3)\n")
	expr := prog.Statements[1].(*ExprStmt).Expr
	if expr.Type() != TVoid() {
		t.Fatalf("Type = %s, want ()", expr.Type())
	}
}

// --- positive: clone --------------------------------------------------------

// TestCheckCloneList admits `clone(xs)` on a list and confirms the result type
// matches the source list type so the binding propagates correctly.
func TestCheckCloneList(t *testing.T) {
	prog := checkSrc(t, "xs := [1, 2]\nys := clone(xs)\n")
	let := prog.Statements[1].(*LetStmt)
	want := NewListType(TInt())
	if !typeEq(let.Value.Type(), want) {
		t.Fatalf("Type = %s, want %s", let.Value.Type(), want)
	}
}

// TestCheckCloneStruct admits `clone(point)` on a struct value and confirms
// the result is the same struct type — the runtime returns a fresh copy but
// typeck only cares about the shape.
func TestCheckCloneStruct(t *testing.T) {
	src := "struct Point { x: int, y: int }\np := Point { x: 1, y: 2 }\nq := clone(p)\n"
	prog := checkSrc(t, src)
	let := prog.Statements[2].(*LetStmt)
	if let.Value.Type() == nil || let.Value.Type().Kind != TypeStruct || let.Value.Type().Name != "Point" {
		t.Fatalf("Type = %s, want Point", let.Value.Type())
	}
}

// TestCheckCloneTuple admits `clone(t)` on a tuple value.
func TestCheckCloneTuple(t *testing.T) {
	prog := checkSrc(t, "t := (1, 2)\nu := clone(t)\n")
	let := prog.Statements[1].(*LetStmt)
	want := NewTupleType([]*Type{TInt(), TInt()})
	if !typeEq(let.Value.Type(), want) {
		t.Fatalf("Type = %s, want %s", let.Value.Type(), want)
	}
}

// TestCheckCloneEnum admits `clone(c)` on an enum value (variant-index ints).
// PLAN explicitly admits enum as a composite for clone's purposes, even though
// at runtime the value is a small integer — keeps the surface uniform.
func TestCheckCloneEnum(t *testing.T) {
	src := "enum Color { Red, Blue }\nc := Color.Red\nd := clone(c)\n"
	prog := checkSrc(t, src)
	let := prog.Statements[2].(*LetStmt)
	if let.Value.Type() == nil || let.Value.Type().Kind != TypeEnum || let.Value.Type().Name != "Color" {
		t.Fatalf("Type = %s, want Color", let.Value.Type())
	}
}

// --- negative: push ---------------------------------------------------------

// TestCheckPushOnLetListRejected confirms an immutable list binding is
// rejected with the precise "must be mut" diagnostic.
func TestCheckPushOnLetListRejected(t *testing.T) {
	checkErr(t, "xs := [1, 2]\npush(xs, 3)\n", "must be mut")
}

// TestCheckPushArityZeroRejected — `push()`.
func TestCheckPushArityZeroRejected(t *testing.T) {
	checkErr(t, "push()\n", "expects 2 argument")
}

// TestCheckPushArityOneRejected — `push(xs)` missing the value.
func TestCheckPushArityOneRejected(t *testing.T) {
	checkErr(t, "mut xs := [1, 2]\npush(xs)\n", "expects 2 argument")
}

// TestCheckPushElementTypeMismatch — value type doesn't match list element.
func TestCheckPushElementTypeMismatch(t *testing.T) {
	checkErr(t, "mut xs := [1, 2]\npush(xs, \"three\")\n", "list element type is int")
}

// TestCheckPushOnNonListRejected — `mut xs := 5; push(xs, 3)` rejects on the
// "must be a mut-bound list variable" rule even though xs is mut.
func TestCheckPushOnNonListRejected(t *testing.T) {
	checkErr(t, "mut xs := 5\npush(xs, 3)\n", "must be a mut-bound list variable")
}

// TestCheckPushOnLiteralRejected — `push([1], 2)` rejects because the first
// arg isn't an ident at all (no top-level mut binding to mutate).
func TestCheckPushOnLiteralRejected(t *testing.T) {
	checkErr(t, "push([1], 2)\n", "must be a mut-bound list variable")
}

// --- negative: clone --------------------------------------------------------

// TestCheckCloneOnPrimitiveRejected — `clone(5)` rejects with the precise
// "primitives don't need cloning" diagnostic.
func TestCheckCloneOnPrimitiveRejected(t *testing.T) {
	checkErr(t, "x := 5\ny := clone(x)\n", "primitives don't need cloning")
}

// TestCheckCloneOnStrRejected — str is a primitive at v0.3 (no list[byte]
// view yet); clone rejects.
func TestCheckCloneOnStrRejected(t *testing.T) {
	checkErr(t, "s := \"hi\"\nt := clone(s)\n", "primitives don't need cloning")
}

// TestCheckCloneArityZeroRejected — `clone()`.
func TestCheckCloneArityZeroRejected(t *testing.T) {
	checkErr(t, "y := clone()\n", "expects 1 argument")
}

// TestCheckCloneArityTwoRejected — `clone(xs, ys)`.
func TestCheckCloneArityTwoRejected(t *testing.T) {
	checkErr(t, "xs := [1]\nys := [2]\nzs := clone(xs, ys)\n", "expects 1 argument")
}

// --- negative: top-level shadow rejection -----------------------------------

// TestCheckUserRedefinePushRejected — user code may not declare a top-level
// fn named push (mirroring the existing len rule).
func TestCheckUserRedefinePushRejected(t *testing.T) {
	src := "fn push(xs: list[int], v: int) {\nnop\n}\n"
	checkErr(t, src, "cannot redefine built-in 'push'")
}

// TestCheckUserRedefineCloneRejected — same for clone.
func TestCheckUserRedefineCloneRejected(t *testing.T) {
	src := "fn clone(xs: list[int]) -> list[int] {\nreturn xs\n}\n"
	checkErr(t, src, "cannot redefine built-in 'clone'")
}

// TestCheckInnerShadowingOfPushOK — inner-block `push := ...` is fine
// because it shadows a name, not the fn-table entry. Mirrors the existing
// inner-shadowing-of-len test.
func TestCheckInnerShadowingOfPushOK(t *testing.T) {
	checkSrc(t, "if true {\npush := 5\nprint push\n}\n")
}

// TestCheckInnerShadowingOfCloneOK — same for clone.
func TestCheckInnerShadowingOfCloneOK(t *testing.T) {
	checkSrc(t, "if true {\nclone := 7\nprint clone\n}\n")
}

// --- regression: len still works -------------------------------------------

// TestCheckLenRegression — the existing `len(xs)` builtin continues to type-
// check after push/clone register in the same fn table.
func TestCheckLenRegression(t *testing.T) {
	prog := checkSrc(t, "xs := [1, 2, 3]\nn := len(xs)\n")
	let := prog.Statements[1].(*LetStmt)
	if let.Value.Type() != TInt() {
		t.Fatalf("Type = %s, want int", let.Value.Type())
	}
}
