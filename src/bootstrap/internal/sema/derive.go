package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// This file is the Phase 1c derive layer (DESIGN-1c §5, U5, FORK-C). A
// '#[derive(Eq, Ord)]' on a struct or enum asks the compiler to generate the
// canonical impl of each named blessed spec by reading the type's structure. The
// synthesis produces an *ast.ImplDecl fed back through the ordinary impl collection
// (collectImpl), so a derived impl is just a normal impl: it participates in
// coherence and the orphan rule, registers in the type's method namespace, and
// satisfies a bound at a use site. The synthesized nodes never enter 'zerg fmt'
// (they are post-parse products), so round-trip is untouched.

// blessedDerive is the fixed set of specs a '#[derive(...)]' may name in this
// iteration (FORK-E — Eq and Ord first). A name outside it is rejected. The set is
// compiler-owned; users cannot extend it (GRAMMAR group 7).
//
//nolint:gochecknoglobals // a fixed, compiler-owned blessed-spec set.
var blessedDerive = map[string]bool{"Eq": true, "Ord": true}

// collectDerived scans every struct and enum for a '#[derive(...)]' decorator and,
// for each named blessed spec, synthesizes and collects the canonical impl. A
// non-blessed or unknown derive name is rejected; the resulting derived impls are
// collected before the coherence check, so a conflict with a hand-written impl is
// reported like any other.
func (c *checker) collectDerived(reg *SpecRegistry, file *ast.File) {
	for _, d := range file.Items {
		switch n := d.(type) {
		case *ast.StructDecl:
			c.deriveOn(reg, n.Decorators, n.Name, structFieldNames(n), nil)
		case *ast.EnumDecl:
			c.deriveOn(reg, n.Decorators, n.Name, nil, n.Variants)
		}
	}
}

// deriveOn synthesizes the derived impls named by a type's decorators. It reads
// each '#[derive(...)]' item, validates every argument is a blessed spec, and for
// each synthesizes an impl AST which it collects and marks Derived.
func (c *checker) deriveOn(reg *SpecRegistry, decos []*ast.Decorator, typeName string, fields []string, variants []*ast.Variant) {
	for _, deco := range decos {
		for _, item := range deco.Items {
			if item.Name != "derive" {
				continue // other decorators (layout, FFI, …) are not this pass's concern
			}
			for _, arg := range item.Args {
				name := deriveArgName(arg)
				if !blessedDerive[name] {
					c.errorf(arg.Span(), "cannot derive %q: the blessed derivable specs are Eq and Ord", name)
					continue
				}
				decl := synthImpl(name, typeName, fields, variants)
				if impl := c.collectImpl(reg, decl); impl != nil {
					impl.Derived = true
				}
			}
		}
	}
}

// deriveArgName reads a derive argument's spec name — a bare type-name wrapped in
// the const-expr the parser keeps uniformly — or "" when it is not a plain name.
func deriveArgName(arg *ast.ConstExpr) string {
	if id, ok := arg.X.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// structFieldNames returns a struct's field names in declaration order, the order
// the canonical Eq/Ord bodies compare in.
func structFieldNames(n *ast.StructDecl) []string {
	out := make([]string, len(n.Fields))
	for i, f := range n.Fields {
		out[i] = f.Name
	}
	return out
}

// synthImpl builds the canonical impl AST of a blessed spec for a type: an
// '#[derive]'d 'impl Spec for Target { … }' whose methods read the type's
// structure (DESIGN-1c §5.2). Eq yields 'eq'/'ne'; Ord yields 'lt'/'le'/'gt'/'ge'.
func synthImpl(spec, typeName string, fields []string, variants []*ast.Variant) *ast.ImplDecl {
	var items []ast.ImplItem
	switch spec {
	case "Eq":
		items = []ast.ImplItem{
			synthMethod("eq", typeName, eqBody(fields, variants, false)),
			synthMethod("ne", typeName, eqBody(fields, variants, true)),
		}
	case "Ord":
		items = []ast.ImplItem{
			synthMethod("lt", typeName, ordBody(fields, variants, token.Lt, false)),
			synthMethod("le", typeName, ordBody(fields, variants, token.Lt, true)),
			synthMethod("gt", typeName, ordBody(fields, variants, token.Gt, false)),
			synthMethod("ge", typeName, ordBody(fields, variants, token.Gt, true)),
		}
	}
	return &ast.ImplDecl{
		Spec:   &ast.TypeRef{Name: spec},
		Target: &ast.TypeRef{Name: typeName},
		Items:  items,
	}
}

// synthMethod builds one canonical comparison method 'fn name(o: Target) -> bool {
// return expr }'. The receiver 'this' is implicit (GRAMMAR group 7).
func synthMethod(name, typeName string, expr ast.Expr) *ast.FuncDecl {
	return &ast.FuncDecl{
		Name:   name,
		Params: []ast.Param{{Name: "o", Type: &ast.TypeRef{Name: typeName}}},
		Ret:    &ast.TypeRef{Name: "bool"},
		Body:   &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: expr}}},
	}
}

// eqBody builds the canonical Eq body: for a struct, the conjunction of each
// field's equality 'this.f == o.f' (true for a fieldless struct); for an enum, a
// discriminant comparison. 'neg' flips it for 'ne'.
func eqBody(fields []string, variants []*ast.Variant, neg bool) ast.Expr {
	var eq ast.Expr
	switch {
	case variants != nil:
		eq = &ast.Binary{Op: token.EqEq, L: discr("this"), R: discr("o")}
	case len(fields) == 0:
		eq = &ast.BoolLit{Value: true}
	default:
		eq = fieldCompare(fields[0], token.EqEq)
		for _, f := range fields[1:] {
			eq = &ast.Binary{Op: token.And, L: eq, R: fieldCompare(f, token.EqEq)}
		}
	}
	if neg {
		return &ast.Unary{Op: token.Not, X: eq}
	}
	return eq
}

// ordBody builds the canonical Ord body for one comparison method: a struct
// compares lexicographically by field, an enum by discriminant. 'op' is Lt (for
// lt/le) or Gt (for gt/ge) and 'tie' is the value when all fields are equal (true
// for le/ge, false for lt/gt).
func ordBody(fields []string, variants []*ast.Variant, op token.Kind, tie bool) ast.Expr {
	if variants != nil {
		return &ast.Binary{Op: op, L: discr("this"), R: discr("o")}
	}
	acc := ast.Expr(&ast.BoolLit{Value: tie})
	for i := len(fields) - 1; i >= 0; i-- {
		less := fieldCompare(fields[i], op)
		eq := fieldCompare(fields[i], token.EqEq)
		acc = &ast.Binary{Op: token.Or, L: less, R: &ast.Binary{Op: token.And, L: eq, R: acc}}
	}
	return acc
}

// fieldCompare builds 'this.f <op> o.f' for one field.
func fieldCompare(field string, op token.Kind) ast.Expr {
	return &ast.Binary{
		Op: op,
		L:  &ast.Field{X: &ast.Ident{Name: "this"}, Name: field},
		R:  &ast.Field{X: &ast.Ident{Name: "o"}, Name: field},
	}
}

// discr builds 'int(x)', the observable enum discriminant read (GRAMMAR group 7)
// the canonical enum Eq/Ord bodies compare on.
func discr(recv string) ast.Expr {
	return &ast.Call{
		Callee: &ast.Ident{Name: "int"},
		Args:   []ast.Arg{{Value: &ast.Ident{Name: recv}}},
	}
}
