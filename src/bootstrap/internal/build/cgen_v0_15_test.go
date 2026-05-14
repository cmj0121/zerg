package build

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// ---------------------------------------------------------------------------
// v0.15 codegen tests — verify the temp-then-write lowering pattern for
// tuple parallel reassignment. The emitted C must construct the RHS tuple
// into a fresh temporary BEFORE writing any LHS slot; that boundary is what
// makes `a, b = b, a + b` read the OLD `a` and `b` on the right.
// ---------------------------------------------------------------------------

func emitC(t *testing.T, src string) string {
	t.Helper()
	tokens, err := syntax.Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	prog, err := syntax.Parse(tokens)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := syntax.Check(prog); err != nil {
		t.Fatalf("Check: %v", err)
	}
	var buf bytes.Buffer
	if err := Emit(prog, &buf); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	return buf.String()
}

func TestCgenMultiAssignEmitsTempThenWrites(t *testing.T) {
	c := emitC(t, "mut a := 0\nmut b := 1\na, b = b, a + b\nprint a\n")
	// One temp declaration carrying the synthetic tuple RHS, followed by two
	// writes through that temp. We don't pin the exact mangled names — only
	// the shape — so renaming the temp prefix doesn't churn the test.
	idx := strings.Index(c, "__zg_multi_assign_")
	if idx < 0 {
		t.Fatalf("expected `__zg_multi_assign_` temp in emitted C; got:\n%s", c)
	}
	// The temp's declaration line must be a tuple construction (compound
	// literal), and the two writes must reference `.e0` and `.e1` of that
	// temp.
	if !strings.Contains(c, ".e0") || !strings.Contains(c, ".e1") {
		t.Errorf("expected `.e0` and `.e1` writes from the temp; got:\n%s", c)
	}
	// Sanity: a temp declaration must precede its read. Check order in the
	// emitted text — a substring search is enough since the snippet is
	// linear.
	declIdx := strings.Index(c, "__zg_multi_assign_")
	useIdx := strings.LastIndex(c, "__zg_multi_assign_")
	if declIdx >= useIdx {
		t.Errorf("temp declaration not before its reads (declIdx=%d useIdx=%d)", declIdx, useIdx)
	}
}

// Parity: the interpreter and the cgen-compiled binary must produce
// byte-identical stdout for a multi-assign program. Uses the v0.5+ parity
// harness (expectV05Parity / buildBundleFromFiles / runBundleFromFiles
// defined in cgen_v05_test.go — same package). The fib-10 loop is the
// canonical exerciser: if either half diverges (e.g. cgen forgets the
// temp boundary), the interpreter still reads OLD values on the RHS but
// the compiled binary doesn't, yielding different output.
func TestCgenMultiAssignParity_FibTen(t *testing.T) {
	src := `mut a := 0
mut b := 1
mut i := 0
for i < 10 {
	a, b = b, a + b
	i = i + 1
}
print a
print b
`
	expectV05Parity(t, "main.zg", map[string]string{"main.zg": src}, "55\n89\n")
}

func TestCgenMultiAssignThreeWayRotation(t *testing.T) {
	c := emitC(t, "mut a := 1\nmut b := 2\nmut c := 3\na, b, c = c, a, b\n")
	for _, want := range []string{".e0", ".e1", ".e2"} {
		if !strings.Contains(c, want) {
			t.Errorf("expected `%s` access in emitted C for 3-way rotation; got:\n%s", want, c)
		}
	}
}
