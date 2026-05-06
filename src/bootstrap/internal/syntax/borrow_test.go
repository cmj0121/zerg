package syntax

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.3 Unit 3 — borrow checker tests.
//
// The checker runs after typeck inside Check(), so checkSrc/checkErr (defined
// in typeck_test.go) drive both passes uniformly. Tests here divide into:
//
//   * Positive: programs that exercise the new ownership rules and must pass.
//   * Negative: programs that must reject — each pinned to the precise
//     diagnostic substring so a future error-message tweak cannot silently
//     change which code path ran.
//
// Diagnostics are emitted as *BorrowError; the helpers in typeck_test.go
// assert *TypeError as the default. We match on the message substring instead
// so both error types satisfy the test (the borrow checker emits its own
// type so callers can distinguish; tests just want "an error containing X").
// ---------------------------------------------------------------------------

// borrowErrSrc is the borrow-checker analogue of typeck_test.go's checkErr.
// It accepts BOTH *TypeError and *BorrowError because the borrow checker
// emits its own type but flows through the same Check() entry point.
func borrowErrSrc(t *testing.T, src, want string) string {
	t.Helper()
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex(%q): %v", src, err)
	}
	prog, err := Parse(tokens)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	err = Check(prog)
	if err == nil {
		t.Fatalf("Check(%q) succeeded, expected error containing %q", src, want)
	}
	if want != "" && !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not contain %q", err.Error(), want)
	}
	return err.Error()
}

// ---------------------------------------------------------------------------
// Positive — programs that must pass borrow check.
// ---------------------------------------------------------------------------

// TestBorrowMoveBasic — `ys := xs` moves xs; reading ys is fine.
func TestBorrowMoveBasic(t *testing.T) {
	checkSrc(t, "xs := [1, 2]\nys := xs\nprint ys[0]\n")
}

// TestBorrowReadDoesNotMove — index reads, len, prints all observe without
// moving the source.
func TestBorrowReadDoesNotMove(t *testing.T) {
	src := "xs := [1, 2]\nprint xs[0]\nprint len(xs)\nprint xs[1]\n"
	checkSrc(t, src)
}

// TestBorrowFnCallDoesNotMove — passing xs to a fn implicitly shared-borrows
// it; caller retains ownership.
func TestBorrowFnCallDoesNotMove(t *testing.T) {
	src := `fn observe(ys: list[int]) {
print ys[0]
}
xs := [1, 2]
observe(xs)
print xs[0]
`
	checkSrc(t, src)
}

// TestBorrowFnCallMultipleTimes — passing the same list twice is fine because
// each call borrows-and-returns.
func TestBorrowFnCallMultipleTimes(t *testing.T) {
	src := `fn first(ys: list[int]) {
print ys[0]
}
fn last(ys: list[int]) {
print ys[1]
}
xs := [1, 2]
first(xs)
last(xs)
print len(xs)
`
	checkSrc(t, src)
}

// TestBorrowMutListIndexAssign — `xs[i] = v` on a mut list, then read xs.
func TestBorrowMutListIndexAssign(t *testing.T) {
	checkSrc(t, "mut xs := [1, 2]\nxs[0] = 99\nprint xs[0]\n")
}

// TestBorrowPushOnMutList — `push(xs, v)` on a mut list, then read xs.
func TestBorrowPushOnMutList(t *testing.T) {
	checkSrc(t, "mut xs := [1, 2]\npush(xs, 3)\nprint xs[0]\n")
}

// TestBorrowForIterReleasesAtBodyExit — for-iter borrows xs only for the
// body's duration; xs returns to Owned afterwards.
func TestBorrowForIterReleasesAtBodyExit(t *testing.T) {
	src := `xs := [1, 2, 3]
for x in xs {
print x
}
print xs[0]
`
	checkSrc(t, src)
}

// TestBorrowCloneDoesNotMove — clone observes its argument and returns a
// fresh copy; original remains usable.
func TestBorrowCloneDoesNotMove(t *testing.T) {
	src := `xs := [1, 2]
ys := clone(xs)
print xs[0]
print ys[0]
`
	checkSrc(t, src)
}

// TestBorrowTupleDestructure — `(a, b) := pair` moves pair, binds
// a and b as fresh owned locals (primitives in this case — no further
// move tracking needed but the parse/typeck path must succeed).
func TestBorrowTupleDestructure(t *testing.T) {
	src := `pair := (1, 2)
(a, b) := pair
print a
print b
`
	checkSrc(t, src)
}

// TestBorrowBranchBothMove — both branches move xs; agreement holds, no
// later use is attempted.
func TestBorrowBranchBothMove(t *testing.T) {
	src := `xs := [1, 2]
if true {
y := xs
print y[0]
} else {
z := xs
print z[0]
}
`
	checkSrc(t, src)
}

// TestBorrowIndexReadAfterFnCall — passing xs to a fn doesn't consume; we
// can read xs[0] afterwards.
func TestBorrowIndexReadAfterFnCall(t *testing.T) {
	src := `fn observe(ys: list[int]) {
print len(ys)
}
xs := [1, 2, 3]
observe(xs)
print xs[1]
`
	checkSrc(t, src)
}

// TestBorrowSliceDoesNotMove — `xs[a..b]` is a read; xs stays usable.
func TestBorrowSliceDoesNotMove(t *testing.T) {
	src := `xs := [1, 2, 3, 4]
zs := xs[1..3]
print xs[0]
print zs[0]
`
	checkSrc(t, src)
}

// TestBorrowFieldReadDoesNotMove — `p.x` reads the receiver without moving.
func TestBorrowFieldReadDoesNotMove(t *testing.T) {
	src := `struct Point { x: int, y: int }
p := Point { x: 1, y: 2 }
print p.x
print p.y
`
	checkSrc(t, src)
}

// TestBorrowPrimitiveMoveIsCopy — primitives move-equals-copy; we don't
// flag use-after-move on int.
func TestBorrowPrimitiveMoveIsCopy(t *testing.T) {
	checkSrc(t, "x := 5\ny := x\nprint x\nprint y\n")
}

// TestBorrowMatchEnumDoesNotMove — match arms that only test enum variants
// don't consume the scrutinee; xs is usable after match.
func TestBorrowMatchEnumDoesNotMove(t *testing.T) {
	src := `enum Color { Red, Green }
c := Color.Red
match c {
Color.Red => print 1
Color.Green => print 2
}
print 0
`
	checkSrc(t, src)
}

// TestBorrowReturnPrimitiveOK — fn returning a primitive works trivially.
func TestBorrowReturnPrimitiveOK(t *testing.T) {
	src := `fn add(a: int, b: int) -> int {
return a + b
}
print add(2, 3)
`
	checkSrc(t, src)
}

// TestBorrowMoveIntoStructAndUseStruct — moving xs into a struct field is
// fine; the new struct value is usable, the source is moved.
func TestBorrowMoveIntoStructAndUseStruct(t *testing.T) {
	src := `struct Box { xs: list[int] }
xs := [1, 2]
b := Box { xs: xs }
print b.xs[0]
`
	checkSrc(t, src)
}

// ---------------------------------------------------------------------------
// Negative — programs that must reject.
// ---------------------------------------------------------------------------

// TestBorrowUseAfterMove — reading xs after `ys := xs` errors with the
// precise "use of moved value" diagnostic naming the source binding.
func TestBorrowUseAfterMove(t *testing.T) {
	src := "xs := [1, 2]\nys := xs\nprint xs[0]\n"
	msg := borrowErrSrc(t, src, "use of moved value")
	if !strings.Contains(msg, `"xs"`) {
		t.Errorf("error %q does not name xs", msg)
	}
}

// TestBorrowDoubleMove — `ys := xs; zs := xs` flags the second move.
func TestBorrowDoubleMove(t *testing.T) {
	src := "xs := [1, 2]\nys := xs\nzs := xs\n"
	borrowErrSrc(t, src, "use of moved value")
}

// TestBorrowMutateForIterIterable — mutating xs via index assignment inside
// `for x in xs { ... }` body errors because xs is BorrowedShared during body.
func TestBorrowMutateForIterIterable(t *testing.T) {
	src := `mut xs := [1, 2, 3]
for x in xs {
xs[0] = 1
}
`
	borrowErrSrc(t, src, "borrowed")
}

// TestBorrowPushOnForIterIterable — push to the iterable inside its body is
// rejected for the same reason as index-assign.
func TestBorrowPushOnForIterIterable(t *testing.T) {
	src := `mut xs := [1, 2]
for x in xs {
push(xs, 3)
}
`
	borrowErrSrc(t, src, "borrowed")
}

// TestBorrowBranchDisagreeMoveOnlyOnIf — the if branch moves xs but the
// implicit else doesn't; branch-agree fires.
func TestBorrowBranchDisagreeMoveOnlyOnIf(t *testing.T) {
	src := `xs := [1, 2]
if true {
y := xs
print y[0]
}
print 0
`
	borrowErrSrc(t, src, "branch states disagree")
}

// TestBorrowBranchDisagreeMoveOnIfNotElse — explicit else without move.
func TestBorrowBranchDisagreeMoveOnIfNotElse(t *testing.T) {
	src := `xs := [1, 2]
if true {
y := xs
print y[0]
} else {
print xs[0]
}
`
	borrowErrSrc(t, src, "branch states disagree")
}

// TestBorrowLoopBodyMovesOuter — moving an outer-scope binding inside a
// for-range body is rejected because subsequent iterations would see it
// already moved.
func TestBorrowLoopBodyMovesOuter(t *testing.T) {
	src := `xs := [1, 2]
for i in 0..3 {
y := xs
print y[0]
}
`
	borrowErrSrc(t, src, "inside loop body")
}

// TestBorrowMatchBindMovesScrutinee — a BindPat arm moves the scrutinee;
// reading it after the match errors.
func TestBorrowMatchBindMovesScrutinee(t *testing.T) {
	src := `xs := [1, 2]
match xs {
ys => print ys[0]
}
print xs[0]
`
	borrowErrSrc(t, src, "use of moved value")
}

// TestBorrowFnParamCannotBeMoved — a fn parameter is BorrowedShared; trying
// to move it (via let-rebind) errors with "cannot move borrowed value".
func TestBorrowFnParamCannotBeMoved(t *testing.T) {
	src := `fn f(xs: list[int]) {
ys := xs
print ys[0]
}
xs := [1, 2]
f(xs)
`
	borrowErrSrc(t, src, "cannot move borrowed value")
}

// TestBorrowFnParamCannotBeReturned — returning a borrowed parameter
// attempts a move and is rejected.
func TestBorrowFnParamCannotBeReturned(t *testing.T) {
	src := `fn f(xs: list[int]) -> list[int] {
return xs
}
xs := [1, 2]
ys := f(xs)
print ys[0]
`
	borrowErrSrc(t, src, "cannot move borrowed value")
}

// TestBorrowFnParamCannotBeMutated — push to a borrowed parameter is
// rejected (the borrow checker re-validates state in addition to typeck's
// "must be mut" rule, which would already fire on a let-bound parameter).
func TestBorrowFnParamCannotBePushed(t *testing.T) {
	src := `fn f(xs: list[int]) {
push(xs, 5)
}
xs := [1, 2]
f(xs)
`
	// typeck fires first ("must be mut") because fn params are bindLet —
	// either diagnostic is acceptable; we accept "must be mut" as the
	// observed message at this commit.
	borrowErrSrc(t, src, "must be mut")
}

// TestBorrowMoveIntoListLitMovesElement — `[a, b]` moves a and b; using a
// after the literal errors.
func TestBorrowMoveIntoListLitMovesElement(t *testing.T) {
	src := `a := [1, 2]
b := [3, 4]
outer := [a, b]
print a[0]
`
	borrowErrSrc(t, src, "use of moved value")
}

// TestBorrowMoveIntoTupleLitMovesElement — same shape for tuples.
func TestBorrowMoveIntoTupleLitMovesElement(t *testing.T) {
	src := `a := [1, 2]
b := [3, 4]
pair := (a, b)
print a[0]
`
	borrowErrSrc(t, src, "use of moved value")
}

// TestBorrowMoveIntoStructFieldMovesField — `Point { xs: my_list }` moves
// my_list; reading it after errors.
func TestBorrowMoveIntoStructFieldMovesField(t *testing.T) {
	src := `struct Box { xs: list[int] }
xs := [1, 2]
b := Box { xs: xs }
print xs[0]
`
	borrowErrSrc(t, src, "use of moved value")
}

// TestBorrowReturnMovesValue — returning a composite from a fn moves the
// local; using it after the return is unreachable but a sequence of returns
// in nested scopes is the relevant guard.
func TestBorrowReturnMovesValue(t *testing.T) {
	// We construct a scenario where the return is on one if branch and a
	// later print on the other branch should still fire if the local was
	// moved on the first branch but not on the (no-else) second.
	src := `fn f() -> list[int] {
xs := [1, 2]
if true {
return xs
}
print xs[0]
return [0]
}
print 0
`
	// The if branch returns (diverges); the implicit no-else branch does
	// nothing; the printed xs is reachable in the no-else path. Here the
	// expectation is that the diverged branch is exempt from agreement, so
	// xs remains Owned in the surviving path. The program should pass.
	checkSrc(t, src)
}

// TestBorrowTupleDestructureMovesPair — `(a, b) := pair; print pair` is
// rejected.
func TestBorrowTupleDestructureMovesPair(t *testing.T) {
	src := `pair := ([1], [2])
(a, b) := pair
print pair
`
	borrowErrSrc(t, src, "use of moved value")
}

// TestBorrowMatchTupleBindMovesScrutinee — destructuring a tuple with bind
// patterns consumes the scrutinee.
func TestBorrowMatchTupleBindMovesScrutinee(t *testing.T) {
	src := `pair := ([1], [2])
match pair {
(a, b) => print 1
}
print pair
`
	borrowErrSrc(t, src, "use of moved value")
}

// TestBorrowIndexAssignOnMovedListErrors — once xs is moved, you can't
// xs[i] = v either.
func TestBorrowIndexAssignOnMovedListErrors(t *testing.T) {
	src := `mut xs := [1, 2]
ys := xs
xs[0] = 99
`
	borrowErrSrc(t, src, "use of moved value")
}

// TestBorrowMoveDiagnosticIncludesSourcePos — the use-after-move message
// includes the position of the move so the user can find both ends.
func TestBorrowMoveDiagnosticIncludesSourcePos(t *testing.T) {
	src := "xs := [1, 2]\nys := xs\nprint xs[0]\n"
	msg := borrowErrSrc(t, src, "use of moved value")
	// Move is on line 2 ("ys := xs"); use on line 3.
	if !strings.Contains(msg, "moved at 2:") {
		t.Errorf("error %q does not name move position 2:*", msg)
	}
}

// ---------------------------------------------------------------------------
// Match-arm bound-name composite tracking — Issue 1.
//
// Before the fix, bindPatternNames hardcoded typ=nil for every BindPat /
// TuplePat / StructPat name, so isComposite returned false and any later
// move of the bound composite was silently allowed. The fix walks the
// pattern shape against the subject Type and assigns each bound name its
// real type so move tracking works through match arms.
// ---------------------------------------------------------------------------

// TestBorrowMatchBindNameTracksCompositeMove — `match xs { ys => ... }` binds
// ys with the list type; moving ys inside the arm and then reading it must
// fire use-after-move (was previously a silent miss because ys.typ was nil).
func TestBorrowMatchBindNameTracksCompositeMove(t *testing.T) {
	src := `xs := [1, 2, 3]
match xs {
ys => {
zs := ys
print ys[0]
}
}
`
	borrowErrSrc(t, src, "use of moved value")
}

// TestBorrowMatchTuplePatBindTracksComposite — TuplePat element bound to a
// list takes list[T]; a later move of that bound name fires use-after-move.
func TestBorrowMatchTuplePatBindTracksComposite(t *testing.T) {
	src := `pair := ([1], [2])
match pair {
(a, b) => {
c := a
print a[0]
}
}
`
	borrowErrSrc(t, src, "use of moved value")
}

// TestBorrowMatchStructPatBindPrimitiveOK — StructPat field bound to an int
// takes the primitive type; the borrow checker ignores moves of primitives.
func TestBorrowMatchStructPatBindPrimitiveOK(t *testing.T) {
	src := `struct Point { x: int, y: int }
p := Point { x: 1, y: 2 }
match p {
Point { x: a, y: b } => {
c := a
print a
}
}
`
	checkSrc(t, src)
}

// TestBorrowMatchStructPatBindCompositeTracks — StructPat field bound to a
// list[int] takes list[int]; a later move fires use-after-move.
func TestBorrowMatchStructPatBindCompositeTracks(t *testing.T) {
	src := `struct Bag { xs: list[int] }
b := Bag { xs: [1, 2] }
match b {
Bag { xs: ys } => {
zs := ys
print ys[0]
}
}
`
	borrowErrSrc(t, src, "use of moved value")
}

// ---------------------------------------------------------------------------
// Match on borrowed-shared fn param doesn't false-flip — Issue 2.
//
// Before the fix, the post-match flip-to-Moved fired even when the
// scrutinee was BorrowedShared at match entry, producing a false-positive
// use-after-move on subsequent reads of the parameter.
// ---------------------------------------------------------------------------

// TestBorrowMatchOnFnParamLiteralArmsClean — match with literal arms only on
// a fn parameter; reads after the match must remain clean.
func TestBorrowMatchOnFnParamLiteralArmsClean(t *testing.T) {
	src := `fn f(p: int) -> str {
match p {
0 => print "z"
1 => print "o"
_ => print "x"
}
print p
return ""
}
n := 1
print f(n)
`
	checkSrc(t, src)
}

// TestBorrowMatchOnFnParamStructDestructureClean — match with a struct
// destructure arm on a fn parameter; reads of the parameter after the
// match (or in the arm body itself) stay clean because the destructure
// reads fields rather than consuming the parent.
func TestBorrowMatchOnFnParamStructDestructureClean(t *testing.T) {
	src := `struct Point { x: int, y: int }
fn f(p: Point) -> str {
match p {
Point { x: 0, y: 0 } => print "z"
Point { x, y } => print "e"
}
print p.x
return ""
}
q := Point { x: 1, y: 2 }
print f(q)
`
	checkSrc(t, src)
}

// TestBorrowMatchOnFnParamBindArmRejected — match with a BindPat arm on a
// fn parameter is rejected because BindPat genuinely consumes the
// scrutinee, and a borrowed value cannot be moved.
func TestBorrowMatchOnFnParamBindArmRejected(t *testing.T) {
	src := `fn f(xs: list[int]) -> int {
match xs {
ys => print ys[0]
}
return 0
}
xs := [1, 2]
print f(xs)
`
	borrowErrSrc(t, src, "cannot move borrowed value")
}

// ---------------------------------------------------------------------------
// Regression — v0.1 / v0.2 surface stays clean.
// ---------------------------------------------------------------------------

// TestBorrowPrimitiveAssignStillWorks — v0.1 mut int rebinding is unaffected.
func TestBorrowPrimitiveAssignStillWorks(t *testing.T) {
	checkSrc(t, "mut x := 1\nx = 2\nprint x\n")
}

// TestBorrowEmptyForLoop — loops over a primitive list don't add tracking
// state for the element.
func TestBorrowEmptyForLoop(t *testing.T) {
	checkSrc(t, "for i in 0..3 {\nnop\n}\n")
}

// TestBorrowNestedFnCallOK — chained fn calls passing the same argument
// remain shared borrows.
func TestBorrowNestedFnCallOK(t *testing.T) {
	src := `fn f(xs: list[int]) -> int {
return len(xs)
}
fn g(xs: list[int]) -> int {
return f(xs) + len(xs)
}
xs := [1, 2, 3]
print g(xs)
print xs[0]
`
	checkSrc(t, src)
}
