package syntax

import "testing"

// ---------------------------------------------------------------------------
// v0.20 typeck — `print value` auto-dispatch through `value.to_str()`.
//
// When `print expr` types as a struct whose method table exposes a zero-arg
// `to_str() -> str`, checkPrint rewrites the statement to call that method.
// Otherwise the original expression is kept and formatValue's struct repr
// fires at run time.
// ---------------------------------------------------------------------------

// Struct with a matching to_str method gets rewritten: PrintStmt.Expr is now
// a MethodCallExpr whose Method is "to_str" and whose Type is str.
func TestCheckPrintAutoCallsToStr(t *testing.T) {
	src := `struct Tag {
    label: str,
}
impl Tag {
    pub fn to_str() -> str {
        return this.label
    }
}
t := Tag{ label: "hello" }
print t
`
	prog := checkSrc(t, src)
	stmt := findPrintStmt(t, prog)
	mc, ok := stmt.Expr.(*MethodCallExpr)
	if !ok {
		t.Fatalf("print expr was not lowered to MethodCallExpr; got %T", stmt.Expr)
	}
	if mc.Method != "to_str" {
		t.Fatalf("lowered method = %q, want %q", mc.Method, "to_str")
	}
	if mc.Type() != tStr {
		t.Fatalf("lowered call type = %s, want str", mc.Type())
	}
}

// Struct without a to_str method keeps the original expression; the struct
// repr fires at run time via formatValue.
func TestCheckPrintNoRewriteWithoutToStr(t *testing.T) {
	src := `struct Point {
    x: int,
    y: int,
}
p := Point{ x: 3, y: 4 }
print p
`
	prog := checkSrc(t, src)
	stmt := findPrintStmt(t, prog)
	if _, ok := stmt.Expr.(*IdentExpr); !ok {
		t.Fatalf("print expr should remain IdentExpr; got %T", stmt.Expr)
	}
}

// A to_str with a non-str return (or non-zero param count) doesn't qualify,
// so the rewrite shouldn't fire and the print falls through to struct repr.
func TestCheckPrintIgnoresWrongSignedToStr(t *testing.T) {
	src := `struct Tag {
    n: int,
}
impl Tag {
    pub fn to_str(prefix: str) -> str {
        return prefix
    }
}
t := Tag{ n: 1 }
print t
`
	prog := checkSrc(t, src)
	stmt := findPrintStmt(t, prog)
	if _, ok := stmt.Expr.(*IdentExpr); !ok {
		t.Fatalf("print expr should remain IdentExpr (signature mismatch); got %T", stmt.Expr)
	}
}

// findPrintStmt walks prog for the first top-level PrintStmt.
func findPrintStmt(t *testing.T, prog *Program) *PrintStmt {
	t.Helper()
	for _, stmt := range prog.Statements {
		if ps, ok := stmt.(*PrintStmt); ok {
			return ps
		}
	}
	t.Fatalf("no PrintStmt in program")
	return nil
}
