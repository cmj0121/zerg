package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// This file is the Phase 1c bound-resolution layer (DESIGN-1c §3, U3): resolving a
// spec bound to the concrete impl that satisfies it, closing bounds over their
// super-specs, and mapping the grammar's comparison operators to the spec that
// backs them so generic code 'a < b' resolves through the parameter's bound.

// operatorSpec maps a comparison operator to the spec and method that back it in
// generic code (DESIGN-1c §3.3, FORK-E — kept small: Eq and Ord). In a generic
// body 'a < b' on a type parameter is valid only if the parameter's bounds provide
// Ord; concrete operands keep their primitive C semantics and never reach here.
//
//nolint:gochecknoglobals // a fixed, compiler-owned operator→spec table.
var operatorSpec = map[token.Kind]struct{ Spec, Method string }{
	token.Lt:   {"Ord", "lt"},
	token.Le:   {"Ord", "le"},
	token.Gt:   {"Ord", "gt"},
	token.Ge:   {"Ord", "ge"},
	token.EqEq: {"Eq", "eq"},
	token.Ne:   {"Eq", "ne"},
}

// registerBlessed pre-registers the built-in blessed specs Eq and Ord (Ord: Eq)
// so a generic bound '[T: Ord]' resolves and an 'impl Eq for int' can be judged an
// orphan (both foreign). They are foreign (Local false) and only added when the
// user has not declared a spec of the same name, in which case the user owns it.
// Their canonical method sets let operator resolution find lt/eq by name; deriving
// the impls themselves is U5, not this iteration.
func registerBlessed(reg *SpecRegistry) {
	if reg.Specs["Eq"] == nil {
		reg.Specs["Eq"] = &types.SpecDef{
			Name:    "Eq",
			Local:   false,
			Methods: blessedMethods("eq", "ne"),
		}
	}
	if reg.Specs["Ord"] == nil {
		ord := &types.SpecDef{
			Name:    "Ord",
			Local:   false,
			Methods: blessedMethods("lt", "le", "gt", "ge"),
		}
		if eq := reg.Specs["Eq"]; eq != nil {
			ord.Supers = []*types.SpecDef{eq}
		}
		reg.Specs["Ord"] = ord
	}
}

// blessedMethods builds the required-method set of a blessed spec: each is a method
// 'fn name(o: This) -> bool'. Only the names matter to bound resolution; the bodies
// are synthesized by derive (U5).
func blessedMethods(names ...string) []*types.SpecMethod {
	out := make([]*types.SpecMethod, len(names))
	for i, name := range names {
		out[i] = &types.SpecMethod{Name: name, Sig: &types.Fn{Ret: types.Bool}}
	}
	return out
}

// resolveImpl finds the unique impl of a spec for a concrete target type, or nil
// when none exists (DESIGN-1c §3.1). Coherence guarantees uniqueness, so a single
// keyed lookup suffices. It is used to satisfy a bound at a use site and to check a
// super-spec requirement.
func (reg *SpecRegistry) resolveImpl(spec *types.SpecDef, target types.Type) *types.ImplDef {
	if spec == nil || target == nil || reg.byKey == nil {
		return nil
	}
	return reg.byKey[implKey{spec: spec, head: targetHead(target)}]
}

// satisfies reports whether a concrete type meets every spec in bounds — an impl
// exists for each, and recursively for each spec's super-specs — reporting the
// first gap (DESIGN-1c §3.2). It is the bound-satisfaction check a future
// instantiation runs once a type parameter is fixed to a concrete type.
func (reg *SpecRegistry) satisfies(c *checker, t types.Type, bounds []*types.SpecDef, at token.Span) bool {
	ok := true
	for _, sp := range bounds {
		if reg.resolveImpl(sp, t) == nil {
			c.errorf(at, "type %s does not satisfy bound %s (no impl %s for %s)", t, sp.Name, sp.Name, t)
			ok = false
			continue
		}
		ok = reg.satisfies(c, t, sp.Supers, at) && ok
	}
	return ok
}

// resolveProjection resolves an associated-type projection 'X.Item' in type
// position to the concrete binding of the impl for X, when one is known (DESIGN-1c
// §3 associated-type resolution). Only a single-segment path is resolved this
// iteration; a longer path or an un-impl'd base stays abstract.
func (reg *SpecRegistry) resolveProjection(base types.Type, path []string) (types.Type, bool) {
	if base == nil || len(path) != 1 {
		return nil, false
	}
	head := targetHead(base)
	for _, im := range reg.Impls {
		if targetHead(im.Target) == head {
			if t, ok := im.AssocBind[path[0]]; ok {
				return t, true
			}
		}
	}
	return nil, false
}

// closeSpecInto appends a spec and its transitive super-specs to out, de-duplicated
// via seen. It closes a bound over its super-specs so '[T: Ord]' carries Eq too.
func closeSpecInto(out []*types.SpecDef, sp *types.SpecDef, seen map[*types.SpecDef]bool) []*types.SpecDef {
	if sp == nil || seen[sp] {
		return out
	}
	seen[sp] = true
	out = append(out, sp)
	for _, sup := range sp.Supers {
		out = closeSpecInto(out, sup, seen)
	}
	return out
}

// closeSupers returns a spec's transitive super-specs (excluding the spec itself),
// used to enforce that 'impl Ord for T' also has 'impl Eq for T'.
func closeSupers(sp *types.SpecDef) []*types.SpecDef {
	seen := map[*types.SpecDef]bool{sp: true}
	var out []*types.SpecDef
	for _, sup := range sp.Supers {
		out = closeSpecInto(out, sup, seen)
	}
	return out
}

// --- generic-body operator resolution -----------------------------------------

// inferGenericOp resolves a binary operator whose operand is a type parameter to
// the spec that backs it (DESIGN-1c §3.3): '<' needs Ord, '==' needs Eq. It reports
// an error when the operator has no spec mapping (e.g. arithmetic on a parameter,
// deferred in 1c) or when the parameter's bounds do not provide the spec's method,
// and yields bool for a satisfied comparison.
func (c *checker) inferGenericOp(n *ast.Binary, lt, rt Type) Type {
	p, _ := lt.(*types.Param)
	if p == nil {
		p, _ = rt.(*types.Param)
	}
	if p == nil {
		return Invalid
	}
	want, ok := operatorSpec[n.Op]
	if !ok {
		c.errorf(n.Span(), "operator %q is not available on type parameter %s in generic code", n.Op, p.Name)
		return Invalid
	}
	if !boundProvides(p.Bounds, want.Spec, want.Method) {
		c.errorf(n.Span(),
			"operator %q on type parameter %s requires bound %s; add '%s: %s'",
			n.Op, p.Name, want.Spec, p.Name, want.Spec)
		return Invalid
	}
	return Bool
}

// boundProvides reports whether a parameter's (super-closed) bounds include a spec
// of the given name that declares the given method — the resolution generic code's
// 'a < b' needs. Because the bounds are already closed over super-specs, a method
// inherited from a super-spec (Eq's 'eq' via 'T: Ord') is found directly.
func boundProvides(bounds []*types.SpecDef, specName, method string) bool {
	for _, sp := range bounds {
		if sp.Name != specName {
			continue
		}
		for _, m := range sp.Methods {
			if m.Name == method {
				return true
			}
		}
	}
	return false
}
