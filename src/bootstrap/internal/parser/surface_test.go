package parser

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
)

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
