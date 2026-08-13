package parser

import (
	gofmt "fmt"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
)

// TestTopLevelStatements checks the widened top level (Slice-E W1): Zerg
// supports script mode, so GRAMMAR#program 'program ::= stmt-list' means any
// statement is legal at the top level, not only the module surface. The bare
// expression, print, if, and for below must parse cleanly and become File.Items,
// while declarations continue to parse as before.
func TestTopLevelStatements(t *testing.T) {
	src := "1 + 2\n" +
		"print 42\n" +
		"x := 3\n" +
		"if x > 0 {\n\tnop\n}\n" +
		"for i in items {\n\tnop\n}\n" +
		"fn main() {\n\tnop\n}\n" +
		"struct S {\n\tpub v: int\n}\n"
	file, diags := Parse(src)
	if len(diags) != 0 {
		t.Fatalf("top-level statements should parse cleanly, got: %v", diags)
	}
	if len(file.Items) != 7 {
		t.Fatalf("got %d top-level items, want 7", len(file.Items))
	}
	// spot-check that the mix of statement and declaration kinds is preserved.
	wantKinds := []any{
		&ast.ExprStmt{}, &ast.PrintStmt{}, &ast.BindStmt{}, &ast.IfStmt{},
		&ast.ForStmt{}, &ast.FuncDecl{}, &ast.StructDecl{},
	}
	for i, want := range wantKinds {
		gotT := gofmt.Sprintf("%T", file.Items[i])
		wantT := gofmt.Sprintf("%T", want)
		if gotT != wantT {
			t.Errorf("item %d is %s, want %s", i, gotT, wantT)
		}
	}
}

// TestPubPreserved checks the 'pub' visibility keyword survives on the node
// (QA FIX 2): the parser must record it so fmt can reprint it.
func TestPubPreserved(t *testing.T) {
	pub := onlyFunc(t, "pub fn f() { nop }")
	if !pub.Pub {
		t.Fatalf("pub fn must set FuncDecl.Pub")
	}
	plain := onlyFunc(t, "fn f() { nop }")
	if plain.Pub {
		t.Fatalf("a plain fn must not be marked pub")
	}
}

// TestIntLitSurface checks the original lexeme is preserved on the node (QA
// FIX 1), while Value still decodes for sema/emit.
func TestIntLitSurface(t *testing.T) {
	cases := []struct {
		lexeme string
		value  int64
	}{
		{"0xFF", 255},
		{"0b1100", 12},
		{"0o755", 493},
		{"1_000", 1000},
		{"42", 42},
	}
	for _, tc := range cases {
		fn := onlyFunc(t, "fn main() { print "+tc.lexeme+" }")
		ps := fn.Body.Stmts[0].(*ast.PrintStmt)
		lit, ok := ps.Value.(*ast.IntLit)
		if !ok {
			t.Fatalf("%s: value is %T, want *ast.IntLit", tc.lexeme, ps.Value)
		}
		if lit.Text != tc.lexeme {
			t.Errorf("%s: Text = %q, want %q", tc.lexeme, lit.Text, tc.lexeme)
		}
		if lit.Value != tc.value {
			t.Errorf("%s: Value = %d, want %d", tc.lexeme, lit.Value, tc.value)
		}
	}
}
