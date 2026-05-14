package fmt

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.16 fmt — string-interpolation round-trip.
//
// Canonical form is `"<lit>{var}<lit>{var}..."`. Literal chunks re-escape
// `{` and `}` as `\{` / `\}` (alongside the existing `\n`, `\t`, `\"`,
// `\\` escapes), so a representative source program round-trips byte-for-
// byte and the second pass through the formatter is idempotent.
// ---------------------------------------------------------------------------

func TestFmtInterpRoundTripMixed(t *testing.T) {
	canonical(t, `n: int = 42
greeting: str = "world"
print "hi {greeting}, n is {n}"
`)
}

func TestFmtInterpRoundTripEscapedBraces(t *testing.T) {
	canonical(t, `print "literal \{ and \} braces"
`)
}

func TestFmtInterpRoundTripSingleVar(t *testing.T) {
	canonical(t, `n: int = 1
print "{n}"
`)
}

func TestFmtInterpRoundTripAdjacentVars(t *testing.T) {
	canonical(t, `a: int = 1
b: int = 2
print "{a}{b}"
`)
}

// Idempotence: formatting twice produces the same bytes. Catches a
// reformatter that adds or drops escapes on the second pass.
func TestFmtInterpIdempotent(t *testing.T) {
	src := `n: int = 42
print "answer = {n}"
`
	out1 := roundTrip(t, src)
	if !strings.Contains(out1, `"answer = {n}"`) {
		t.Errorf("first-pass output missing canonical interp form: %s", out1)
	}
	idempotent(t, out1)
}
