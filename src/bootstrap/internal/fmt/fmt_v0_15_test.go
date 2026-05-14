package fmt

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.15 fmt — multi-assign statement formatting.
//
// Canonical form is bare-comma on both sides:
//
//	a, b = b, a + b
//
// A user-written paren-wrapped RHS (`a, b = (b, a + b)`) canonicalizes to
// the bare form because the parser stores either shape as Value = *TupleLit
// and the formatter unwraps TupleLit RHS to bare commas. A tuple-returning
// CallExpr RHS prints unchanged.
// ---------------------------------------------------------------------------

func TestFmtMultiAssignCanonicalPair(t *testing.T) {
	canonical(t, `mut a := 0
mut b := 1
a, b = b, a + b
`)
}

func TestFmtMultiAssignCanonicalThreeWay(t *testing.T) {
	canonical(t, `mut a := 1
mut b := 2
mut c := 3
a, b, c = c, a, b
`)
}

// Leading-line comments above a multi-assign must survive a round trip,
// just like every other statement form. Regression for the v0.15 parser-
// side miss: parser.go:setLeadingComments needs a *MultiAssignStmt arm so
// the threader populates the new node's LeadingComments slice.
func TestFmtMultiAssignLeadingCommentsSurvive(t *testing.T) {
	src := `mut a := 0
mut b := 1

# leading line one
# leading line two
a, b = b, a + b
`
	got := roundTrip(t, src)
	if !strings.Contains(got, "# leading line one") || !strings.Contains(got, "# leading line two") {
		t.Errorf("leading comments lost on multi-assign: %s", got)
	}
	idempotent(t, got)
}

func TestFmtMultiAssignTupleCallRHS(t *testing.T) {
	canonical(t, `fn divmod(a: int, b: int) -> tuple[int, int] {
    return (a // b, a % b)
}
mut q := 0
mut r := 0
q, r = divmod(10, 3)
`)
}
