package syntax

import "testing"

// ---------------------------------------------------------------------------
// v0.4 Unit 4 typeck: structural == / != on lists, tuples, structs, and enums
// (with or without payloads). Spec-typed bindings reject — Comparable defers
// to v0.6.
//
// Helpers (checkSrc / checkErr) are shared with typeck_test.go.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Positive — composite == admitted.
// ---------------------------------------------------------------------------

func TestCheckEqListPrimitive(t *testing.T) {
	src := `let xs := [1, 2]
let ys := [1, 2]
print xs == ys
`
	checkSrc(t, src)
}

func TestCheckEqTuplePrimitive(t *testing.T) {
	src := `let a := (1, "x")
let b := (1, "x")
print a == b
`
	checkSrc(t, src)
}

func TestCheckEqStruct(t *testing.T) {
	src := `struct Point { x: int, y: int }
let p := Point { x: 1, y: 2 }
let q := Point { x: 1, y: 2 }
print p == q
`
	checkSrc(t, src)
}

func TestCheckEqEnumBareVariant(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
let c := Color.Red
let d := Color.Red
print c == d
`
	checkSrc(t, src)
}

func TestCheckEqEnumPayload(t *testing.T) {
	src := `enum Token { Eof, Ident(str) }
let t1 := Token.Ident("a")
let t2 := Token.Ident("a")
print t1 == t2
`
	checkSrc(t, src)
}

func TestCheckNeListPrimitive(t *testing.T) {
	src := `print [1, 2] != [1, 3]
`
	checkSrc(t, src)
}

func TestCheckEqNestedList(t *testing.T) {
	src := `let xs := [[1, 2], [3, 4]]
let ys := [[1, 2], [3, 4]]
print xs == ys
`
	checkSrc(t, src)
}

func TestCheckEqListOfStruct(t *testing.T) {
	src := `struct Point { x: int, y: int }
let ps := [Point { x: 1, y: 2 }]
let qs := [Point { x: 1, y: 2 }]
print ps == qs
`
	checkSrc(t, src)
}

func TestCheckEqEnumPayloadWithList(t *testing.T) {
	// Enum with list-typed payload still admits == because list[int] does.
	src := `enum Bag { Empty, Many(list[int]) }
let a := Bag.Many([1, 2])
let b := Bag.Many([1, 2])
print a == b
`
	checkSrc(t, src)
}

func TestCheckEqTupleNestedStruct(t *testing.T) {
	src := `struct Point { x: int, y: int }
let a := (Point { x: 1, y: 2 }, "ok")
let b := (Point { x: 1, y: 2 }, "ok")
print a == b
`
	checkSrc(t, src)
}

// ---------------------------------------------------------------------------
// Negative — type mismatch and spec-typed reject.
// ---------------------------------------------------------------------------

func TestCheckEqListElementTypeMismatch(t *testing.T) {
	// Mismatched element types between the two list literals. The literals
	// each infer fine; the comparison rejects because list[int] != list[str].
	src := `let xs := [1, 2]
let ys := ["a"]
print xs == ys
`
	checkErr(t, src, "operator == requires operands of the same type")
}

func TestCheckEqStructDifferentNominalRejected(t *testing.T) {
	src := `struct Point { x: int, y: int }
enum Color { Red, Blue }
let p := Point { x: 1, y: 2 }
let c := Color.Red
print p == c
`
	checkErr(t, src, "operator == requires operands of the same type")
}

func TestCheckEqListVsStructRejected(t *testing.T) {
	src := `struct Point { x: int, y: int }
let xs := [1, 2]
let p := Point { x: 1, y: 2 }
print xs == p
`
	checkErr(t, src, "operator == requires operands of the same type")
}

func TestCheckEqSpecTypedRejected(t *testing.T) {
	src := `spec Printable { fn show() -> int }
struct Counter { count: int }
impl Counter for Printable { fn show() -> int { return this.count } }
let p: Printable = Counter { count: 1 }
let q: Printable = Counter { count: 2 }
print p == q
`
	checkErr(t, src, `cannot compare values of spec type "Printable" — defer to v0.6`)
}

func TestCheckNeSpecTypedRejected(t *testing.T) {
	// != mirrors == — spec-typed values reject the same way.
	src := `spec Printable { fn show() -> int }
struct Counter { count: int }
impl Counter for Printable { fn show() -> int { return this.count } }
let p: Printable = Counter { count: 1 }
let q: Printable = Counter { count: 2 }
print p != q
`
	checkErr(t, src, `cannot compare values of spec type "Printable" — defer to v0.6`)
}

func TestCheckEqTwoStructsDifferentNamesRejected(t *testing.T) {
	src := `struct A { x: int }
struct B { x: int }
let a := A { x: 1 }
let b := B { x: 1 }
print a == b
`
	checkErr(t, src, "operator == requires operands of the same type")
}
