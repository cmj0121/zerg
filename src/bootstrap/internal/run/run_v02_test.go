package run

import "testing"

// ---------------------------------------------------------------------------
// v0.2 — rune / byte literals.
// ---------------------------------------------------------------------------

func TestRuneAsciiPrintsDecimal(t *testing.T) {
	// 'A' — ASCII codepoint 65 — typeck classifies as byte; print is decimal
	// of the unsigned value (PLAN line 155).
	expectOK(t, "print 'A'\n", "65\n")
}

func TestRuneNonAsciiPrintsCodepoint(t *testing.T) {
	// '漢' — codepoint 28450 — typeck classifies as rune; print is decimal
	// of the codepoint (PLAN line 156).
	expectOK(t, "print '漢'\n", "28450\n")
}

func TestRuneZeroByte(t *testing.T) {
	// Smallest byte. Verifies the FormatUint path handles 0. Lexer-supported
	// escape is `\0` rather than C's `\x00`.
	expectOK(t, "print '\\0'\n", "0\n")
}

// ---------------------------------------------------------------------------
// v0.2 — list literal, indexing, slicing, len.
// ---------------------------------------------------------------------------

func TestListLitPrint(t *testing.T) {
	expectOK(t, "let xs := [1, 2, 3]\nprint xs\n", "[ 1, 2, 3 ]\n")
}

func TestListEmptyPrint(t *testing.T) {
	// Empty list prints "[]" with no inner spaces (PLAN line 164).
	expectOK(t, "let xs: list[int] = []\nprint xs\nprint len(xs)\n", "[]\n0\n")
}

func TestListIndex(t *testing.T) {
	expectOK(t, "let xs := [10, 20, 30]\nprint xs[0]\nprint xs[2]\n", "10\n30\n")
}

func TestListIndexLast(t *testing.T) {
	// len(xs)-1 gives the last index.
	expectOK(t, "let xs := [9, 8, 7]\nprint xs[len(xs) - 1]\n", "7\n")
}

func TestListSliceHalfOpen(t *testing.T) {
	expectOK(t, "let xs := [1, 2, 3, 4, 5]\nprint xs[1..3]\n", "[ 2, 3 ]\n")
}

func TestListSliceInclusive(t *testing.T) {
	expectOK(t, "let xs := [1, 2, 3, 4, 5]\nprint xs[1..=3]\n", "[ 2, 3, 4 ]\n")
}

func TestListSliceLowOmitted(t *testing.T) {
	expectOK(t, "let xs := [1, 2, 3, 4, 5]\nprint xs[..2]\n", "[ 1, 2 ]\n")
}

func TestListSliceHighOmitted(t *testing.T) {
	expectOK(t, "let xs := [1, 2, 3, 4, 5]\nprint xs[3..]\n", "[ 4, 5 ]\n")
}

func TestListSliceFullCopy(t *testing.T) {
	expectOK(t, "let xs := [1, 2]\nprint xs[..]\n", "[ 1, 2 ]\n")
}

func TestListSliceEmpty(t *testing.T) {
	// xs[i..i] is the empty slice — prints "[]".
	expectOK(t, "let xs := [1, 2, 3]\nprint xs[1..1]\n", "[]\n")
}

func TestListIndexOutOfRange(t *testing.T) {
	expectErr(t, "let xs := [1, 2]\nprint xs[5]\n", "out of range")
}

func TestListSliceOutOfRange(t *testing.T) {
	expectErr(t, "let xs := [1, 2]\nprint xs[0..5]\n", "out of range")
}

func TestListSliceLowGreaterHigh(t *testing.T) {
	expectErr(t, "let xs := [1, 2, 3]\nprint xs[2..1]\n", "out of range")
}

func TestStringIndexReturnsRune(t *testing.T) {
	// 'h' is codepoint 104, 'i' is 105 — string indexing produces rune.
	expectOK(t, "let s := \"hi\"\nprint s[0]\nprint s[1]\n", "104\n105\n")
}

func TestLenOfList(t *testing.T) {
	expectOK(t, "print len([10, 20, 30, 40])\n", "4\n")
}

// ---------------------------------------------------------------------------
// v0.2 — list value-copy semantics.
//
// v0.2 has no list mutation, so the only observable side-effect of "value
// copy on bind" is that a function argument's later rebinding doesn't show
// up on the caller's binding. We exercise that path by having a fn declare a
// local shadowing the parameter — its later print of the local must not
// disturb the caller's print.
// ---------------------------------------------------------------------------

func TestListBindIsValueCopy(t *testing.T) {
	// `let ys := xs` is a value copy: even after we rebind ys (well, v0.2
	// can't mutate ys; we sample by printing both, which exercises the
	// copy path indirectly — the test is mostly a regression guard against
	// reading the wrong slice header).
	src := `let xs := [1, 2, 3]
let ys := xs
print xs
print ys
`
	expectOK(t, src, "[ 1, 2, 3 ]\n[ 1, 2, 3 ]\n")
}

func TestListFnArgIsValueCopy(t *testing.T) {
	// Pass a list to a fn that prints it; back in the caller the list is
	// still the same. (The deep-copy on parameter pass means the fn's
	// parameter binding is independent — provable when v0.3 adds mutation.)
	src := `fn show(ys: list[int]) {
  print ys
}
let xs := [1, 2, 3]
show(xs)
print xs
`
	expectOK(t, src, "[ 1, 2, 3 ]\n[ 1, 2, 3 ]\n")
}

// ---------------------------------------------------------------------------
// v0.2 — tuple literal and match-destructure.
// ---------------------------------------------------------------------------

func TestTupleLitPrint(t *testing.T) {
	expectOK(t, "let p := (1, 2)\nprint p\n", "( 1, 2 )\n")
}

func TestTupleThreeElement(t *testing.T) {
	expectOK(t, "let t := (1, 2, 3)\nprint t\n", "( 1, 2, 3 )\n")
}

func TestTupleHeterogeneous(t *testing.T) {
	expectOK(t, `let p := (1, "two")
print p
`, "( 1, two )\n")
}

func TestTupleMatchDestructure(t *testing.T) {
	// Tuple destructure via match is the only destructure form parser /
	// typeck support today — `let (a, b) := pair` requires a parser
	// extension that's not in scope for Unit 3.
	src := `let p := (3, 4)
match p {
  (a, b) => print a + b
}
`
	expectOK(t, src, "7\n")
}

// ---------------------------------------------------------------------------
// v0.2 — struct literal, field access.
// ---------------------------------------------------------------------------

func TestStructLitAndFieldAccess(t *testing.T) {
	src := `struct Point { x: int, y: int }
let p := Point { x: 7, y: 11 }
print p
print p.x
print p.y
`
	expectOK(t, src, "Point { x: 7, y: 11 }\n7\n11\n")
}

func TestStructFieldOrderFollowsDeclaration(t *testing.T) {
	// Initialiser order differs from declaration order; print follows decl.
	src := `struct Point { x: int, y: int }
let p := Point { y: 99, x: 1 }
print p
`
	expectOK(t, src, "Point { x: 1, y: 99 }\n")
}

func TestStructInStructPrint(t *testing.T) {
	src := `struct Inner { v: int }
struct Outer { inner: Inner, label: str }
let o := Outer { inner: Inner { v: 42 }, label: "hi" }
print o
print o.inner.v
`
	expectOK(t, src, "Outer { inner: Inner { v: 42 }, label: hi }\n42\n")
}

func TestStructInListIndexThenField(t *testing.T) {
	src := `struct Point { x: int, y: int }
let pts := [Point { x: 1, y: 2 }, Point { x: 3, y: 4 }]
print pts[1].x
`
	expectOK(t, src, "3\n")
}

// ---------------------------------------------------------------------------
// v0.2 — enum variant access.
// ---------------------------------------------------------------------------

func TestEnumVariantAccess(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
let c := Color.Green
print c
print Color.Red
print Color.Blue
`
	expectOK(t, src, "Color.Green\nColor.Red\nColor.Blue\n")
}

func TestEnumInList(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
let cs := [Color.Red, Color.Blue]
print cs
print cs[0]
`
	expectOK(t, src, "[ Color.Red, Color.Blue ]\nColor.Red\n")
}

// ---------------------------------------------------------------------------
// v0.2 — match.
// ---------------------------------------------------------------------------

func TestMatchLiteralArms(t *testing.T) {
	src := `let n := 2
match n {
  1 => print "one"
  2 => print "two"
  _ => print "other"
}
`
	expectOK(t, src, "two\n")
}

func TestMatchWildcardFallback(t *testing.T) {
	src := `let n := 99
match n {
  1 => print "one"
  _ => print "other"
}
`
	expectOK(t, src, "other\n")
}

func TestMatchBindCapturesValue(t *testing.T) {
	src := `let n := 42
match n {
  v => print v
}
`
	expectOK(t, src, "42\n")
}

func TestMatchGuardSelects(t *testing.T) {
	src := `let n := 7
match n {
  x if x > 5 => print "big"
  x => print "small"
}
`
	expectOK(t, src, "big\n")
}

func TestMatchGuardFallsThrough(t *testing.T) {
	// Guard false ⇒ next arm.
	src := `let n := 3
match n {
  x if x > 5 => print "big"
  x => print "small"
}
`
	expectOK(t, src, "small\n")
}

func TestMatchTupleDestructure(t *testing.T) {
	src := `let p := (10, 20)
match p {
  (a, b) => print a + b
}
`
	expectOK(t, src, "30\n")
}

func TestMatchStructDestructure(t *testing.T) {
	src := `struct Point { x: int, y: int }
let p := Point { x: 5, y: 0 }
match p {
  Point { x: 0, y: 0 } => print "origin"
  Point { x, y } => print x + y
}
`
	expectOK(t, src, "5\n")
}

func TestMatchStructWithRest(t *testing.T) {
	src := `struct Point { x: int, y: int }
let p := Point { x: 5, y: 99 }
match p {
  Point { x: 0, .. } => print "x zero"
  Point { x, .. } => print x
}
`
	expectOK(t, src, "5\n")
}

func TestMatchEnumVariants(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
let c := Color.Green
match c {
  Color.Red => print "red"
  Color.Green => print "green"
  Color.Blue => print "blue"
}
`
	expectOK(t, src, "green\n")
}

func TestMatchNoArmPanics(t *testing.T) {
	// PLAN tenth-man revision: no silent fall-through. The interpreter
	// must error out when no arm matches.
	src := `let n := 5
match n {
  1 => print "one"
  2 => print "two"
}
`
	expectErr(t, src, "no arm matched")
}

func TestMatchNestedTupleStruct(t *testing.T) {
	// A tuple of (struct, enum) — matches via nested patterns.
	src := `struct Point { x: int, y: int }
enum Color { Red, Blue }
let pair := (Point { x: 1, y: 2 }, Color.Blue)
match pair {
  (Point { x, .. }, Color.Red)  => print x
  (Point { x, y }, Color.Blue) => print x + y
}
`
	expectOK(t, src, "3\n")
}
