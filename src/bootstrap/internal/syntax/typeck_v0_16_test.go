package syntax

import "testing"

// ---------------------------------------------------------------------------
// v0.16 typeck — bare-identifier string interpolation.
//
// Rules verified here:
//   * Each var piece must resolve in scope; undefined names reject with the
//     standard "undefined name" diagnostic.
//   * The resolved type must be one of the six primitives at v0.16 (int,
//     float, bool, str, byte, rune). Composite-typed bindings reject with
//     the focused "not a primitive" diagnostic.
//   * The whole expression types as str.
// ---------------------------------------------------------------------------

// --- positive: each primitive type passes -----------------------------------

func TestCheckInterpInt(t *testing.T) {
	checkSrc(t, "n: int = 42\nprint \"n = {n}\"\n")
}

func TestCheckInterpFloat(t *testing.T) {
	checkSrc(t, "x: float = 3.14\nprint \"x = {x}\"\n")
}

func TestCheckInterpBool(t *testing.T) {
	checkSrc(t, "b: bool = true\nprint \"b = {b}\"\n")
}

func TestCheckInterpStr(t *testing.T) {
	checkSrc(t, "s: str = \"world\"\nprint \"hi {s}\"\n")
}

func TestCheckInterpByte(t *testing.T) {
	checkSrc(t, "c: byte = 'A'\nprint \"c = {c}\"\n")
}

func TestCheckInterpRune(t *testing.T) {
	checkSrc(t, "r: rune = '€'\nprint \"r = {r}\"\n")
}

// --- negative ---------------------------------------------------------------

func TestCheckInterpRejectsUndefined(t *testing.T) {
	checkErr(t,
		"print \"hi {missing}\"\n",
		`undefined name "missing"`)
}

func TestCheckInterpRejectsListType(t *testing.T) {
	checkErr(t,
		"xs: list[int] = [1, 2]\nprint \"xs = {xs}\"\n",
		`cannot interpolate "xs"`)
}

func TestCheckInterpRejectsTupleType(t *testing.T) {
	checkErr(t,
		"t: tuple[int, int] = (1, 2)\nprint \"t = {t}\"\n",
		`is not a primitive`)
}
