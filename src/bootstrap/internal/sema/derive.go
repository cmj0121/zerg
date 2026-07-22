package sema

import (
	"strconv"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// This file is the Phase 1c derive layer (DESIGN-1c §5, U5, FORK-C). A
// '#[derive(Eq, Ord)]' on a struct or enum asks the compiler to generate the
// canonical impl of each named blessed spec by reading the type's structure. The
// synthesis produces an *ast.ImplDecl fed back through the ordinary impl collection
// (collectImpl), so a derived impl is just a normal impl: it participates in
// coherence and the orphan rule, registers in the type's method namespace,
// satisfies a bound, and — being type-checked with the file's impls — emits. The
// synthesized nodes never enter 'zerg fmt' (they are post-parse products), so
// round-trip is untouched.

// blessedDerive is the fixed set of specs a '#[derive(...)]' may name in this
// iteration (FORK-E — Eq and Ord first). A name outside it is rejected. The set is
// compiler-owned; users cannot extend it (GRAMMAR group 7).
//
//nolint:gochecknoglobals // a fixed, compiler-owned blessed-spec set.
var blessedDerive = map[string]bool{"Eq": true, "Ord": true}

// collectDerived scans every struct and enum for a '#[derive(...)]' decorator and,
// for each named blessed spec, synthesizes and collects the canonical impl. A
// non-blessed or unknown derive name is rejected; the derived impls are collected
// before the coherence check, so a conflict with a hand-written impl is reported
// like any other, and queued for body type-checking after the file's own impls.
func (c *checker) collectDerived(reg *SpecRegistry, file *ast.File) {
	for _, d := range file.Items {
		switch n := d.(type) {
		case *ast.StructDecl:
			c.deriveOn(reg, n.Decorators, n.Name, n.Span(), structFieldNames(n), nil)
		case *ast.EnumDecl:
			c.deriveOn(reg, n.Decorators, n.Name, n.Span(), nil, c.enumVariantDefs(n.Name))
		}
	}
}

// enumVariantDefs returns the resolved variant definitions of a named enum, so the
// derived match bodies can classify a nullary-variant pattern.
func (c *checker) enumVariantDefs(name string) []*types.VariantDef {
	if sym := c.module.local(name); sym != nil && sym.TypeDef != nil && sym.TypeDef.Enum != nil {
		return sym.TypeDef.Enum.Variants
	}
	return nil
}

// deriveOn synthesizes the derived impls named by a type's decorators. It reads
// each '#[derive(...)]' item, validates every argument is a blessed spec, and for
// each synthesizes an impl AST which it collects (marking Derived) and queues for
// later body checking.
func (c *checker) deriveOn(reg *SpecRegistry, decos []*ast.Decorator, typeName string, at token.Span,
	fields []string, variants []*types.VariantDef,
) {
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
				decl := c.synthImpl(name, typeName, at, fields, variants)
				if impl := c.collectImpl(reg, decl); impl != nil {
					impl.Derived = true
					c.derived = append(c.derived, decl)
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
// Every synthesized node is stamped with the deriving type's span so its
// diagnostics are locatable (N1).
func (c *checker) synthImpl(spec, typeName string, at token.Span, fields []string, variants []*types.VariantDef) *ast.ImplDecl {
	var items []ast.ImplItem
	switch spec {
	case "Eq":
		items = []ast.ImplItem{
			c.synthMethod("eq", typeName, at, c.eqBody(fields, variants, false)),
			c.synthMethod("ne", typeName, at, c.eqBody(fields, variants, true)),
		}
	case "Ord":
		items = []ast.ImplItem{
			c.synthMethod("lt", typeName, at, c.ordBody(fields, variants, token.Lt, false)),
			c.synthMethod("le", typeName, at, c.ordBody(fields, variants, token.Lt, true)),
			c.synthMethod("gt", typeName, at, c.ordBody(fields, variants, token.Gt, false)),
			c.synthMethod("ge", typeName, at, c.ordBody(fields, variants, token.Gt, true)),
		}
	}
	decl := &ast.ImplDecl{Spec: &ast.TypeRef{Name: spec}, Target: &ast.TypeRef{Name: typeName}, Items: items}
	decl.SetSpan(at)
	return decl
}

// synthMethod builds one canonical comparison method 'fn name(o: Target) -> bool {
// return expr }'. The receiver 'this' is implicit (GRAMMAR group 7).
func (c *checker) synthMethod(name, typeName string, at token.Span, expr ast.Expr) *ast.FuncDecl {
	fn := &ast.FuncDecl{
		Name:   name,
		Params: []ast.Param{{Name: "o", Type: &ast.TypeRef{Name: typeName}}},
		Ret:    &ast.TypeRef{Name: "bool"},
		Body:   &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: expr}}},
	}
	fn.SetSpan(at)
	return fn
}

// eqBody builds the canonical Eq body: a struct is a conjunction of field
// equalities 'this.f == o.f' (true when fieldless); an enum matches on the variant
// and compares payloads element-wise. 'neg' flips it for 'ne'.
func (c *checker) eqBody(fields []string, variants []*types.VariantDef, neg bool) ast.Expr {
	var eq ast.Expr
	switch {
	case variants != nil:
		eq = c.enumEq(variants)
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
// compares lexicographically by field, an enum by variant order then payload. 'op'
// is Lt (lt/le) or Gt (gt/ge) and 'tie' is the result when all fields (or payloads)
// are equal (true for le/ge, false for lt/gt).
func (c *checker) ordBody(fields []string, variants []*types.VariantDef, op token.Kind, tie bool) ast.Expr {
	if variants != nil {
		return c.enumOrd(variants, op, tie)
	}
	acc := ast.Expr(&ast.BoolLit{Value: tie})
	for i := len(fields) - 1; i >= 0; i-- {
		less := fieldCompare(fields[i], op)
		eq := fieldCompare(fields[i], token.EqEq)
		acc = &ast.Binary{Op: token.Or, L: less, R: &ast.Binary{Op: token.And, L: eq, R: acc}}
	}
	return acc
}

// enumEq builds the payload-aware enum Eq body (M2): it matches this, and within
// each variant's arm matches o for the same variant (comparing payloads
// element-wise) with a catch-all false, so equality holds only for the same variant
// with equal payloads.
func (c *checker) enumEq(variants []*types.VariantDef) ast.Expr {
	outer := &ast.MatchExpr{Subject: &ast.Ident{Name: "this"}}
	for _, v := range variants {
		inner := &ast.MatchExpr{Subject: &ast.Ident{Name: "o"}}
		inner.Arms = append(inner.Arms,
			ast.MatchArm{Pat: c.sidePattern(v, "r"), Body: payloadEqual(len(v.Payload))},
			ast.MatchArm{Pat: &ast.WildPattern{}, Body: &ast.BoolLit{Value: false}},
		)
		outer.Arms = append(outer.Arms, ast.MatchArm{Pat: c.sidePattern(v, "l"), Body: inner})
	}
	return outer
}

// enumOrd builds the payload-aware enum Ord body (M2): earlier variants compare
// less, and equal variants compare their payloads lexicographically. It matches
// this against o over every variant pair.
func (c *checker) enumOrd(variants []*types.VariantDef, op token.Kind, tie bool) ast.Expr {
	outer := &ast.MatchExpr{Subject: &ast.Ident{Name: "this"}}
	for i, vi := range variants {
		inner := &ast.MatchExpr{Subject: &ast.Ident{Name: "o"}}
		for j, vj := range variants {
			var body ast.Expr
			switch {
			case i == j:
				body = payloadLex(len(vi.Payload), op, tie)
			case (op == token.Lt && i < j) || (op == token.Gt && i > j):
				body = &ast.BoolLit{Value: true}
			default:
				body = &ast.BoolLit{Value: false}
			}
			inner.Arms = append(inner.Arms, ast.MatchArm{Pat: c.sidePattern(vj, "r"), Body: body})
		}
		outer.Arms = append(outer.Arms, ast.MatchArm{Pat: c.sidePattern(vi, "l"), Body: inner})
	}
	return outer
}

// sidePattern builds one variant's match pattern, binding its payload to names
// '<side>0, <side>1, …' (l for this, r for o). A nullary variant is a bare
// NamePattern, which the derived match creates after name resolution, so it is
// registered as a variant pattern here.
func (c *checker) sidePattern(v *types.VariantDef, side string) ast.Pattern {
	if len(v.Payload) == 0 {
		np := &ast.NamePattern{Name: v.Name}
		c.info.Patterns[np] = NameRes{Kind: NameVariant, Variant: v}
		return np
	}
	vp := &ast.VariantPattern{Name: v.Name}
	for i := range v.Payload {
		vp.Elems = append(vp.Elems, &ast.NamePattern{Name: side + strconv.Itoa(i)})
	}
	return vp
}

// payloadEqual builds the element-wise equality of a variant's k payload values
// (true when k is 0).
func payloadEqual(k int) ast.Expr {
	if k == 0 {
		return &ast.BoolLit{Value: true}
	}
	acc := payloadCompare(0, token.EqEq)
	for i := 1; i < k; i++ {
		acc = &ast.Binary{Op: token.And, L: acc, R: payloadCompare(i, token.EqEq)}
	}
	return acc
}

// payloadLex builds the lexicographic comparison of a variant's k payload values
// under op (Lt or Gt), yielding tie when all are equal.
func payloadLex(k int, op token.Kind, tie bool) ast.Expr {
	acc := ast.Expr(&ast.BoolLit{Value: tie})
	for i := k - 1; i >= 0; i-- {
		less := payloadCompare(i, op)
		eq := payloadCompare(i, token.EqEq)
		acc = &ast.Binary{Op: token.Or, L: less, R: &ast.Binary{Op: token.And, L: eq, R: acc}}
	}
	return acc
}

// payloadCompare builds 'l<i> <op> r<i>' for one payload position.
func payloadCompare(i int, op token.Kind) ast.Expr {
	return &ast.Binary{
		Op: op,
		L:  &ast.Ident{Name: "l" + strconv.Itoa(i)},
		R:  &ast.Ident{Name: "r" + strconv.Itoa(i)},
	}
}

// fieldCompare builds 'this.f <op> o.f' for one field.
func fieldCompare(field string, op token.Kind) ast.Expr {
	return &ast.Binary{
		Op: op,
		L:  &ast.Field{X: &ast.Ident{Name: "this"}, Name: field},
		R:  &ast.Field{X: &ast.Ident{Name: "o"}, Name: field},
	}
}
