package syntax

import (
	"testing"
)

// ---------------------------------------------------------------------------
// v0.15 parser tests — bare-comma tuple parallel reassignment.
//
//	IDENT (',' IDENT)+ '=' expr (',' expr)*
//
// The new sniff lives in parseExprOrAssignStmt alongside the existing `:=` /
// `:` bind-form sniffs. The cursor is at the leading IDENT; lookahead-1 is
// `,`; the sniff confirms a closing `=` before consuming so any non-matching
// shape falls back to the generic "expression statements must be function
// calls" error path with the cursor unmoved.
// ---------------------------------------------------------------------------

// --- positive ---------------------------------------------------------------

func TestParseMultiAssignBareCommaPair(t *testing.T) {
	prog := parseProgramSrc(t, "mut a := 0\nmut b := 1\na, b = b, a + b\n")
	if got := len(prog.Statements); got != 3 {
		t.Fatalf("got %d stmts, want 3", got)
	}
	ma, ok := prog.Statements[2].(*MultiAssignStmt)
	if !ok {
		t.Fatalf("stmt 2 is %T, want *MultiAssignStmt", prog.Statements[2])
	}
	if got := len(ma.Targets); got != 2 {
		t.Fatalf("Targets len = %d, want 2", got)
	}
	for i, want := range []string{"a", "b"} {
		id, ok := ma.Targets[i].(*IdentExpr)
		if !ok {
			t.Fatalf("Targets[%d] = %T, want *IdentExpr", i, ma.Targets[i])
		}
		if id.Name != want {
			t.Errorf("Targets[%d].Name = %q, want %q", i, id.Name, want)
		}
	}
	tup, ok := ma.Value.(*TupleLit)
	if !ok {
		t.Fatalf("Value = %T, want *TupleLit (synthetic)", ma.Value)
	}
	if got := len(tup.Elements); got != 2 {
		t.Errorf("synthetic TupleLit arity = %d, want 2", got)
	}
}

func TestParseMultiAssignThreeWay(t *testing.T) {
	src := "mut a := 1\nmut b := 2\nmut c := 3\na, b, c = c, a, b\n"
	prog := parseProgramSrc(t, src)
	ma, ok := prog.Statements[3].(*MultiAssignStmt)
	if !ok {
		t.Fatalf("last stmt is %T, want *MultiAssignStmt", prog.Statements[3])
	}
	if got := len(ma.Targets); got != 3 {
		t.Fatalf("Targets len = %d, want 3", got)
	}
}

func TestParseMultiAssignTupleCallRHS(t *testing.T) {
	// Single RHS expression (a tuple-returning call) is stored directly as
	// Value, NOT wrapped in a synthetic TupleLit.
	src := `fn divmod(a: int, b: int) -> tuple[int, int] { return (a // b, a % b) }
mut q := 0
mut r := 0
q, r = divmod(10, 3)
`
	prog := parseProgramSrc(t, src)
	ma, ok := prog.Statements[3].(*MultiAssignStmt)
	if !ok {
		t.Fatalf("last stmt is %T, want *MultiAssignStmt", prog.Statements[3])
	}
	if _, isTuple := ma.Value.(*TupleLit); isTuple {
		t.Errorf("Value is *TupleLit, want the raw CallExpr — single-RHS form should not synthesize")
	}
	if _, ok := ma.Value.(*CallExpr); !ok {
		t.Errorf("Value = %T, want *CallExpr", ma.Value)
	}
}

// --- negative ---------------------------------------------------------------

func TestParseMultiAssignRejectsDuplicateLHS(t *testing.T) {
	expectParseErr(t,
		"mut a := 0\na, a = 1, 2\n",
		`name "a" repeated in multi-assign LHS`)
}

func TestParseMultiAssignRejectsNonIdentLHS(t *testing.T) {
	// `1, b = 2, 3` — the leading `1` is not an IDENT so the sniff doesn't
	// fire; `parseExpr` consumes `1`, peek is `,`, falls through to the
	// generic "expression statements must be function calls" error.
	expectParseErr(t,
		"mut b := 0\n1, b = 2, 3\n",
		"expression statements must be function calls")
}

func TestParseMultiAssignWalrusFallsThroughToBindError(t *testing.T) {
	// `a, b := 1, 2` doesn't match the new sniff (sniff requires trailing
	// `=`, not `:=`). The cursor stays at the leading IDENT; parseExpr
	// consumes `a`, peek `,`, falls through to the existing diagnostic.
	expectParseErr(t,
		"a, b := 1, 2\n",
		"expression statements must be function calls")
}

func TestParseMultiAssignBareCommaWithoutAssignFallsThrough(t *testing.T) {
	// `a, b` with no `=` — the sniff walks but finds no closing `=`, so it
	// returns false without consuming. parseExpr then handles `a` and the
	// trailing `,` triggers the standard expression-statement error.
	expectParseErr(t,
		"mut a := 0\nmut b := 1\na, b\n",
		"expression statements must be function calls")
}
