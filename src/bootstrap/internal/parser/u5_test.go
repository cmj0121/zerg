package parser

import (
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
