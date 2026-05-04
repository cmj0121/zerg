package syntax

import (
	"testing"
)

// ---------------------------------------------------------------------------
// v0.3 typeck tests — Unit 1: list-element assignment placeholder.
//
// At Unit 1 the parser admits `xs[i] = v` but typeck stubs it with a
// "v0.3 work in progress" diagnostic so the v0.0 / v0.1 / v0.2 corpora
// are unaffected and any program that exercises the new surface fails
// with a clear message until Unit 3 (borrow checker) lands.
// ---------------------------------------------------------------------------

// TestCheckIndexAssignWorkInProgress confirms typeck rejects `xs[i] = v` with
// the placeholder message — Units 2 and 3 will replace this with real mut /
// borrow / type checking.
func TestCheckIndexAssignWorkInProgress(t *testing.T) {
	src := "mut xs := [1, 2, 3]\nxs[0] = 99\n"
	checkErr(t, src, "v0.3 work in progress")
}

// TestCheckIdentAssignStillWorks is the regression guard — broadening the
// AST shape must not break the v0.1 simple-assignment path. typeck still
// validates the mut / let / type rules for `x = expr` exactly as before.
func TestCheckIdentAssignStillWorks(t *testing.T) {
	checkSrc(t, "mut x := 1\nx = 2\n")
}

// TestCheckIdentAssignLetStillRejected confirms the v0.1 "cannot assign to
// let-bound name" rule still fires after the AssignStmt restructure.
func TestCheckIdentAssignLetStillRejected(t *testing.T) {
	checkErr(t, "let x := 1\nx = 2\n", "declared with let")
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
	prog := checkSrc(t, "let xs := [1, 2]\nlet ys := clone(xs)\n")
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
	src := "struct Point { x: int, y: int }\nlet p := Point { x: 1, y: 2 }\nlet q := clone(p)\n"
	prog := checkSrc(t, src)
	let := prog.Statements[2].(*LetStmt)
	if let.Value.Type() == nil || let.Value.Type().Kind != TypeStruct || let.Value.Type().Name != "Point" {
		t.Fatalf("Type = %s, want Point", let.Value.Type())
	}
}

// TestCheckCloneTuple admits `clone(t)` on a tuple value.
func TestCheckCloneTuple(t *testing.T) {
	prog := checkSrc(t, "let t := (1, 2)\nlet u := clone(t)\n")
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
	src := "enum Color { Red, Blue }\nlet c := Color.Red\nlet d := clone(c)\n"
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
	checkErr(t, "let xs := [1, 2]\npush(xs, 3)\n", "must be mut")
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
	checkErr(t, "let x := 5\nlet y := clone(x)\n", "primitives don't need cloning")
}

// TestCheckCloneOnStrRejected — str is a primitive at v0.3 (no list[byte]
// view yet); clone rejects.
func TestCheckCloneOnStrRejected(t *testing.T) {
	checkErr(t, "let s := \"hi\"\nlet t := clone(s)\n", "primitives don't need cloning")
}

// TestCheckCloneArityZeroRejected — `clone()`.
func TestCheckCloneArityZeroRejected(t *testing.T) {
	checkErr(t, "let y := clone()\n", "expects 1 argument")
}

// TestCheckCloneArityTwoRejected — `clone(xs, ys)`.
func TestCheckCloneArityTwoRejected(t *testing.T) {
	checkErr(t, "let xs := [1]\nlet ys := [2]\nlet zs := clone(xs, ys)\n", "expects 1 argument")
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

// TestCheckInnerShadowingOfPushOK — inner-block `let push := ...` is fine
// because it shadows a name, not the fn-table entry. Mirrors the existing
// inner-shadowing-of-len test.
func TestCheckInnerShadowingOfPushOK(t *testing.T) {
	checkSrc(t, "if true {\nlet push := 5\nprint push\n}\n")
}

// TestCheckInnerShadowingOfCloneOK — same for clone.
func TestCheckInnerShadowingOfCloneOK(t *testing.T) {
	checkSrc(t, "if true {\nlet clone := 7\nprint clone\n}\n")
}

// --- regression: len still works -------------------------------------------

// TestCheckLenRegression — the existing `len(xs)` builtin continues to type-
// check after push/clone register in the same fn table.
func TestCheckLenRegression(t *testing.T) {
	prog := checkSrc(t, "let xs := [1, 2, 3]\nlet n := len(xs)\n")
	let := prog.Statements[1].(*LetStmt)
	if let.Value.Type() != TInt() {
		t.Fatalf("Type = %s, want int", let.Value.Type())
	}
}
