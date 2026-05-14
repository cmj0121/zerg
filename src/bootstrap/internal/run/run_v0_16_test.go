package run

import "testing"

// ---------------------------------------------------------------------------
// v0.16 interpreter tests — bare-identifier string interpolation.
//
// Each primitive type produces the canonical text form formatValue gives it.
// The cgen-side parity is asserted separately in cgen_v0_16_test.go via the
// expectV05Parity helper.
// ---------------------------------------------------------------------------

func TestRunInterpInt(t *testing.T) {
	expectOK(t, "n: int = 42\nprint \"answer = {n}\"\n", "answer = 42\n")
}

func TestRunInterpFloat(t *testing.T) {
	// %.17g of 0.5 is exactly "0.5" — no precision drift, matches print.
	expectOK(t, "x: float = 0.5\nprint \"x = {x}\"\n", "x = 0.5\n")
}

func TestRunInterpBool(t *testing.T) {
	expectOK(t, "b: bool = true\nprint \"b = {b}\"\n", "b = true\n")
}

func TestRunInterpStr(t *testing.T) {
	expectOK(t, "s: str = \"world\"\nprint \"hi {s}\"\n", "hi world\n")
}

func TestRunInterpByte(t *testing.T) {
	// 'A' is 65; decimal form per the print contract.
	expectOK(t, "c: byte = 'A'\nprint \"c = {c}\"\n", "c = 65\n")
}

func TestRunInterpRune(t *testing.T) {
	// '€' is codepoint 8364; decimal form per the print contract.
	expectOK(t, "r: rune = '€'\nprint \"r = {r}\"\n", "r = 8364\n")
}

func TestRunInterpMixed(t *testing.T) {
	src := `n: int = 42
greeting: str = "world"
print "hi {greeting}, n is {n}"
`
	expectOK(t, src, "hi world, n is 42\n")
}

func TestRunInterpAdjacentVars(t *testing.T) {
	expectOK(t,
		"a: int = 1\nb: int = 2\nprint \"{a}{b}\"\n",
		"12\n")
}

func TestRunInterpEscapedBraces(t *testing.T) {
	expectOK(t,
		`print "literal \{ and \} braces"`+"\n",
		"literal { and } braces\n")
}

func TestRunInterpLeadingAndTrailingVars(t *testing.T) {
	expectOK(t,
		"a: int = 1\nb: int = 2\nprint \"{a} mid {b}\"\n",
		"1 mid 2\n")
}
