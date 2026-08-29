package parser

import (
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
)

// onlyDecl parses src and returns its single declaration.
func onlyDecl(t *testing.T, src string) ast.Decl {
	t.Helper()
	file := parseOK(t, src)
	if len(file.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(file.Items))
	}
	return file.Items[0].(ast.Decl)
}

func TestStructDecl(t *testing.T) {
	d := onlyDecl(t, "pub struct Point {\n\tpub x: int\n\ty: int = 0\n}")
	s, ok := d.(*ast.StructDecl)
	if !ok {
		t.Fatalf("decl is %T, want *ast.StructDecl", d)
	}
	if !s.Pub || s.Name != "Point" || len(s.Fields) != 2 {
		t.Fatalf("unexpected struct: %+v", s)
	}
	if !s.Fields[0].Pub || s.Fields[0].Name != "x" {
		t.Fatalf("unexpected field 0: %+v", s.Fields[0])
	}
	if s.Fields[1].Pub || s.Fields[1].Default == nil {
		t.Fatalf("field 1 should be private with a default: %+v", s.Fields[1])
	}
}

func TestEnumDecl(t *testing.T) {
	d := onlyDecl(t, "enum Shape {\n\tCircle(float)\n\tRect(float, float)\n}")
	e := d.(*ast.EnumDecl)
	if len(e.Variants) != 2 || len(e.Variants[0].Payload) != 1 || len(e.Variants[1].Payload) != 2 {
		t.Fatalf("unexpected variants: %+v", e.Variants)
	}
}

func TestEnumDiscriminant(t *testing.T) {
	d := onlyDecl(t, "enum Color {\n\tRed = 1\n\tGreen = 0xFF\n\tBlue\n}")
	e := d.(*ast.EnumDecl)
	if e.Variants[1].Discr == nil {
		t.Fatalf("Green should carry a discriminant")
	}
	lit, ok := e.Variants[1].Discr.X.(*ast.IntLit)
	if !ok || lit.Text != "0xFF" {
		t.Fatalf("discriminant surface not preserved: %+v", e.Variants[1].Discr.X)
	}
	if e.Variants[2].Discr != nil {
		t.Fatalf("Blue should have no discriminant")
	}
}

func TestGenericBound(t *testing.T) {
	d := onlyDecl(t, "fn f[T: Ord + Eq]() { nop }")
	fn := d.(*ast.FuncDecl)
	if fn.Generics == nil || len(fn.Generics.Params) != 1 {
		t.Fatalf("expected one generic param: %+v", fn.Generics)
	}
	b := fn.Generics.Params[0].Bound
	if b == nil || len(b.Names) != 2 || b.Names[0] != "Ord" || b.Names[1] != "Eq" {
		t.Fatalf("unexpected bound: %+v", b)
	}
}

func TestImplForms(t *testing.T) {
	specImpl := onlyDecl(t, "impl Ord for int { BITS := 0 }").(*ast.ImplDecl)
	if specImpl.Spec == nil {
		t.Fatalf("spec impl should carry a Spec")
	}
	inherent := onlyDecl(t, "impl User { fn m() { nop } }").(*ast.ImplDecl)
	if inherent.Spec != nil {
		t.Fatalf("inherent impl should leave Spec nil")
	}
}

func TestTypeProjection(t *testing.T) {
	d := onlyDecl(t, "type Alias = Iterator.Item.Sub")
	td := d.(*ast.TypeDecl)
	ref, ok := td.Alias.(*ast.TypeRef)
	if !ok || ref.Name != "Iterator" || len(ref.Proj) != 2 {
		t.Fatalf("unexpected projection: %+v", td.Alias)
	}
}

func TestDecoratorAttachment(t *testing.T) {
	d := onlyDecl(t, "#[derive(Encode, Decode)]\nstruct D { pub x: int }")
	s := d.(*ast.StructDecl)
	if len(s.Decorators) != 1 || len(s.Decorators[0].Items) != 1 {
		t.Fatalf("unexpected decorators: %+v", s.Decorators)
	}
	if s.Decorators[0].Items[0].Name != "derive" || len(s.Decorators[0].Items[0].Args) != 2 {
		t.Fatalf("unexpected deco item: %+v", s.Decorators[0].Items[0])
	}
}

// TestAllowOnAStatement pins #91. `#[allow(…)]` is documented normatively in
// docs/tooling/lint.md, the shipping compiler builds it, and the seed died on the
// `#[` — so a documented feature could not be used in any file the seed must build,
// which is the compiler's own sources and the whole standard library.
//
// The seed does not lint, so it does not honour the suppression; it only has to read
// the decorator and hand back the statement it leads.
func TestAllowOnAStatement(t *testing.T) {
	fn := onlyFunc(t, "fn main() {\n\tmut x := 1\n\t#[allow(L101)]\n\tx = 2\n\tprint x\n}")
	if len(fn.Body.Stmts) != 3 {
		t.Fatalf("got %d stmts, want 3 — the decorator is read and the statement it leads is kept", len(fn.Body.Stmts))
	}
	if _, ok := fn.Body.Stmts[2].(*ast.PrintStmt); !ok {
		t.Fatalf("stmt 2 is %T, want *ast.PrintStmt — the decorator became a statement of its own", fn.Body.Stmts[2])
	}
}

// TestDecoratorOnAStatementIsAllowOnly pins the other half: a statement takes
// `#[allow(…)]` and no other decorator, so the seed agrees with the shipping compiler
// about which programs exist rather than accepting a wider language than it.
func TestDecoratorOnAStatementIsAllowOnly(t *testing.T) {
	_, diags := Parse("fn main() {\n\t#[derive(Eq)]\n\tprint 1\n}")
	if len(diags) == 0 {
		t.Fatalf("a #[derive] leading a statement was accepted")
	}
	// The REASON is the assertion. Before the seed read a statement decorator at all,
	// this failed too — with `expected an expression, found '#['`, which says nothing
	// about the rule and would go on saying it if the rule were dropped.
	if !strings.Contains(diags[0].Error(), "#[allow") {
		t.Fatalf("refused for the wrong reason: %v", diags[0])
	}
}
