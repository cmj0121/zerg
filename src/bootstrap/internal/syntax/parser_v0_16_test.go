package syntax

import "testing"

// ---------------------------------------------------------------------------
// v0.16 parser tests — bare-identifier string interpolation.
//
// The lexer produces a structured token sequence; the parser unwraps it into
// an *InterpolatedStringLit with empty Lit pieces dropped so the AST slice
// is minimal-clean.
// ---------------------------------------------------------------------------

func TestParseInterpolatedStringMixed(t *testing.T) {
	// `print "hi {name}, n is {n}"` → InterpolatedStringLit with three pieces:
	// Lit("hi "), Var(name), Lit(", n is "), Var(n). The trailing empty Lit
	// the lexer emits is dropped by the parser.
	prog := parseProgramSrc(t, "name := \"bob\"\nn := 1\nprint \"hi {name}, n is {n}\"\n")
	if got := len(prog.Statements); got != 3 {
		t.Fatalf("stmt count = %d, want 3", got)
	}
	ps, ok := prog.Statements[2].(*PrintStmt)
	if !ok {
		t.Fatalf("stmt 2 = %T, want *PrintStmt", prog.Statements[2])
	}
	lit, ok := ps.Expr.(*InterpolatedStringLit)
	if !ok {
		t.Fatalf("PrintStmt.Expr = %T, want *InterpolatedStringLit", ps.Expr)
	}
	if got := len(lit.Pieces); got != 4 {
		t.Fatalf("piece count = %d, want 4 (empty Lits dropped)", got)
	}
	if l, ok := lit.Pieces[0].(*StringLitPiece); !ok || l.Text != "hi " {
		t.Errorf("piece 0 = %#v, want StringLitPiece{Text:\"hi \"}", lit.Pieces[0])
	}
	if v, ok := lit.Pieces[1].(*StringVarPiece); !ok || v.Ident.Name != "name" {
		t.Errorf("piece 1 = %#v, want StringVarPiece{Ident.Name:\"name\"}", lit.Pieces[1])
	}
	if l, ok := lit.Pieces[2].(*StringLitPiece); !ok || l.Text != ", n is " {
		t.Errorf("piece 2 = %#v, want StringLitPiece{Text:\", n is \"}", lit.Pieces[2])
	}
	if v, ok := lit.Pieces[3].(*StringVarPiece); !ok || v.Ident.Name != "n" {
		t.Errorf("piece 3 = %#v, want StringVarPiece{Ident.Name:\"n\"}", lit.Pieces[3])
	}
}

func TestParseInterpolatedStringSingleVar(t *testing.T) {
	prog := parseProgramSrc(t, "n := 1\nprint \"{n}\"\n")
	ps, ok := prog.Statements[1].(*PrintStmt)
	if !ok {
		t.Fatalf("stmt 1 = %T, want *PrintStmt", prog.Statements[1])
	}
	lit, ok := ps.Expr.(*InterpolatedStringLit)
	if !ok {
		t.Fatalf("PrintStmt.Expr = %T, want *InterpolatedStringLit", ps.Expr)
	}
	// "{n}" → lexer emits empty Lit, Var, empty Lit. Parser drops both empty
	// Lits, leaving just the Var.
	if got := len(lit.Pieces); got != 1 {
		t.Fatalf("piece count = %d, want 1", got)
	}
	if v, ok := lit.Pieces[0].(*StringVarPiece); !ok || v.Ident.Name != "n" {
		t.Errorf("piece 0 = %#v, want StringVarPiece{Ident.Name:\"n\"}", lit.Pieces[0])
	}
}

func TestParseInterpolatedStringAdjacentVars(t *testing.T) {
	prog := parseProgramSrc(t, "a := 1\nb := 2\nprint \"{a}{b}\"\n")
	ps, ok := prog.Statements[2].(*PrintStmt)
	if !ok {
		t.Fatalf("stmt 2 = %T, want *PrintStmt", prog.Statements[2])
	}
	lit, ok := ps.Expr.(*InterpolatedStringLit)
	if !ok {
		t.Fatalf("PrintStmt.Expr = %T, want *InterpolatedStringLit", ps.Expr)
	}
	// "{a}{b}" → empty Lit / Var(a) / empty Lit / Var(b) / empty Lit. Parser
	// drops all three empty Lits, leaving two adjacent Vars.
	if got := len(lit.Pieces); got != 2 {
		t.Fatalf("piece count = %d, want 2", got)
	}
	if _, ok := lit.Pieces[0].(*StringVarPiece); !ok {
		t.Errorf("piece 0 = %T, want *StringVarPiece", lit.Pieces[0])
	}
	if _, ok := lit.Pieces[1].(*StringVarPiece); !ok {
		t.Errorf("piece 1 = %T, want *StringVarPiece", lit.Pieces[1])
	}
}
