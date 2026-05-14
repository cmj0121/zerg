package build

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.16 codegen tests — bare-identifier string interpolation.
//
// The cgen lowering is a left-fold of zerg_str_concat calls. Each piece is
// either a zerg_str_lit (literal chunk) or a zerg_<T>_to_str helper call
// (var piece). The text form each to-str helper produces is matched to the
// corresponding zerg_print_* helper so the run-vs-build parity contract
// holds; the parity tests below assert that explicitly.
// ---------------------------------------------------------------------------

// Shape: the emitted C for an interpolated string must contain a
// zerg_str_concat chain plus the per-type helper call.
func TestCgenInterpEmitsConcatChain(t *testing.T) {
	c := emitC(t, "n: int = 42\nprint \"answer = {n}\"\n")
	if !strings.Contains(c, "zerg_str_concat(") {
		t.Errorf("expected zerg_str_concat call in emitted C; got:\n%s", c)
	}
	if !strings.Contains(c, "zerg_int_to_str(") {
		t.Errorf("expected zerg_int_to_str call in emitted C; got:\n%s", c)
	}
}

// Per-type dispatch: each primitive type must route to its matching helper.
func TestCgenInterpDispatchesPerType(t *testing.T) {
	cases := []struct {
		decl       string
		wantHelper string
	}{
		{`n: int = 1`, "zerg_int_to_str"},
		{`x: float = 1.0`, "zerg_float_to_str"},
		{`b: bool = true`, "zerg_bool_to_str"},
		{`c: byte = 'A'`, "zerg_byte_to_str"},
		{`r: rune = '€'`, "zerg_rune_to_str"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.wantHelper, func(t *testing.T) {
			// Use the first character of the binding name in the print form.
			name := tc.decl[:1]
			src := tc.decl + "\nprint \"v = {" + name + "}\"\n"
			c := emitC(t, src)
			if !strings.Contains(c, tc.wantHelper+"(") {
				t.Errorf("expected %s in emitted C; got:\n%s", tc.wantHelper, c)
			}
		})
	}
}

// Str-typed bindings are identity — no to-str helper call, just a direct
// reference inside the concat chain.
func TestCgenInterpStrIsIdentity(t *testing.T) {
	c := emitC(t, "s: str = \"world\"\nprint \"hi {s}\"\n")
	if strings.Contains(c, "zerg_str_to_str(") {
		t.Errorf("str piece should be identity, not wrapped: %s", c)
	}
	if !strings.Contains(c, "zerg_str_concat(") {
		t.Errorf("expected zerg_str_concat call; got:\n%s", c)
	}
}

// --- Parity: interpreter and compiled binary produce byte-identical stdout.
// Every primitive type is exercised so a precision drift or format mismatch
// in any single helper is caught here.

func TestCgenInterpParity_Int(t *testing.T) {
	src := "n: int = 42\nprint \"answer = {n}\"\n"
	expectV05Parity(t, "main.zg", map[string]string{"main.zg": src}, "answer = 42\n")
}

// Float parity: %.17g vs Go's FormatFloat(..., 'g', 17, 64) must agree. We
// use a value with an exact float64 representation (0.5) so the test asserts
// the helper-vs-print agreement without entangling with the well-known "0.1
// is not representable" float-string drift. The cgen interp helper and the
// interp's formatValue MUST both use 17 significant digits regardless.
func TestCgenInterpParity_Float(t *testing.T) {
	src := "x: float = 0.5\nprint \"x = {x}\"\n"
	expectV05Parity(t, "main.zg", map[string]string{"main.zg": src}, "x = 0.5\n")
}

func TestCgenInterpParity_Bool(t *testing.T) {
	src := "b: bool = false\nprint \"b = {b}\"\n"
	expectV05Parity(t, "main.zg", map[string]string{"main.zg": src}, "b = false\n")
}

func TestCgenInterpParity_Str(t *testing.T) {
	src := "s: str = \"world\"\nprint \"hi {s}\"\n"
	expectV05Parity(t, "main.zg", map[string]string{"main.zg": src}, "hi world\n")
}

func TestCgenInterpParity_Byte(t *testing.T) {
	src := "c: byte = 'A'\nprint \"c = {c}\"\n"
	expectV05Parity(t, "main.zg", map[string]string{"main.zg": src}, "c = 65\n")
}

func TestCgenInterpParity_Rune(t *testing.T) {
	src := "r: rune = '€'\nprint \"r = {r}\"\n"
	expectV05Parity(t, "main.zg", map[string]string{"main.zg": src}, "r = 8364\n")
}

func TestCgenInterpParity_Mixed(t *testing.T) {
	src := `n: int = 42
greeting: str = "world"
print "hi {greeting}, n is {n}"
`
	expectV05Parity(t, "main.zg", map[string]string{"main.zg": src}, "hi world, n is 42\n")
}

func TestCgenInterpParity_EscapedBraces(t *testing.T) {
	src := `print "literal \{ and \} braces"` + "\n"
	expectV05Parity(t, "main.zg", map[string]string{"main.zg": src}, "literal { and } braces\n")
}
