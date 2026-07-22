package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// --- calls & construction -----------------------------------------------------

// inferCall resolves a call to one of three callee kinds (DESIGN-1b §4.6): a type
// name is construction 'T(...)', a function name is a direct call, and a local
// value of function type is an indirect call. A complex callee expression is
// synthesized and, when it is a function value, called indirectly.
func (c *checker) inferCall(n *ast.Call) Type {
	if id, ok := n.Callee.(*ast.Ident); ok {
		if sym := c.module.lookup(id.Name); sym != nil && sym.Kind == SymType {
			return c.construct(n, sym)
		}
		if sym := c.module.lookup(id.Name); sym != nil && sym.Kind == SymVariant {
			return c.constructVariant(n, sym)
		}
		if sig, ok := c.info.Funcs[id.Name]; ok {
			return c.callFunc(n, sig)
		}
		if s := c.lookup(id.Name); s != nil {
			if fn, ok := underlyingFn(s.typ); ok {
				return c.callIndirect(n, fn)
			}
			if !bad(s.typ) {
				c.errorf(id.Span(), "%q is not callable", id.Name)
			}
			c.synthArgs(n)
			return types.Unknown
		}
		c.errorf(id.Span(), "undefined function %q", id.Name)
		c.synthArgs(n)
		return Invalid
	}
	ct := c.synth(n.Callee)
	if fn, ok := underlyingFn(ct); ok {
		return c.callIndirect(n, fn)
	}
	c.synthArgs(n)
	return types.Unknown
}

func (c *checker) synthArgs(n *ast.Call) {
	for _, a := range n.Args {
		c.synth(a.Value)
	}
}

// callFunc checks a direct call to a named function, dispatching to the generic
// path when the function has type or value parameters.
func (c *checker) callFunc(n *ast.Call, sig *FuncSig) Type {
	if sig.Generic != nil {
		return c.callGeneric(n, sig)
	}
	c.bindCallArgs(sig.Name, n, sig.ParamNames, sig.Params, sig.Defaults)
	return sig.Ret
}

// callIndirect checks a call through a function-typed value.
func (c *checker) callIndirect(n *ast.Call, fn *types.Fn) Type {
	ptypes := make([]Type, len(fn.Params))
	for i, p := range fn.Params {
		ptypes[i] = p.Type
	}
	c.bindCallArgs("closure", n, nil, ptypes, nil)
	if fn.Ret == nil {
		return Nil
	}
	return fn.Ret
}

// construct checks a construction 'T(...)' (DESIGN-1b §3.6/§4.6): a struct's
// fields become parameters in declaration order, matched positional-then-named by
// field name, with defaults. The result is the nominal struct type.
func (c *checker) construct(n *ast.Call, sym *Symbol) Type {
	def := sym.TypeDef
	if def == nil || def.Struct == nil {
		// enum-variant or alias construction is not modelled here (FORK-2/4).
		c.synthArgs(n)
		return c.namedTypeUse(sym, nil)
	}
	var pnames []string
	var ptypes []Type
	var defaults []bool
	for _, f := range def.Struct.Fields {
		pnames = append(pnames, f.Name)
		ptypes = append(ptypes, f.Type)
		defaults = append(defaults, f.HasDefault)
	}
	if len(def.Params) > 0 {
		return c.constructGeneric(n, sym, def, pnames, ptypes)
	}
	c.bindCallArgs(def.Name, n, pnames, ptypes, defaults)
	return c.namedTypeUse(sym, nil)
}

// constructGeneric checks a generic struct construction 'T(...)', inferring the
// type arguments from the field arguments — the constructor analogue of a generic
// call (DESIGN-1c §4). It unifies each field's declared type against its argument
// type to fix the struct's parameters, checks each argument against the resulting
// concrete field type, and yields the applied struct type 'T[args]' so
// monomorphization can specialize it.
func (c *checker) constructGeneric(n *ast.Call, sym *Symbol, def *types.TypeDef, pnames []string, ptypes []Type) Type {
	exprs := c.pairFields(def.Name, n, pnames)
	subT := map[string]Type{}
	subV := map[string]types.ConstVal{}
	argTypes := make([]Type, len(ptypes))
	for i := range ptypes {
		if exprs[i] == nil {
			continue
		}
		argTypes[i] = c.synth(exprs[i])
		unify(ptypes[i], argTypes[i], subT, subV)
	}
	for i := range ptypes {
		if exprs[i] == nil {
			c.errorf(n.Span(), "%q: missing field %q", def.Name, argName(pnames, i))
			continue
		}
		want := substitute(ptypes[i], subT, subV)
		if at := argTypes[i]; !bad(at) && !bad(want) && !c.assignable(want, exprs[i], at) {
			c.errorf(exprs[i].Span(), "field %d of %q: cannot use %s as %s", i+1, def.Name, at, want)
		}
	}
	args := make([]Type, len(def.Params))
	for i, p := range def.Params {
		if t, ok := subT[p.Name]; ok {
			args[i] = t
		} else {
			args[i] = types.Unknown
		}
	}
	return c.namedTypeUse(sym, args)
}

// pairFields aligns a construction's arguments to a struct's fields, positional
// first then named, reporting an unknown or excess argument.
func (c *checker) pairFields(name string, n *ast.Call, pnames []string) []ast.Expr {
	exprs := make([]ast.Expr, len(pnames))
	idx := map[string]int{}
	for i, nm := range pnames {
		idx[nm] = i
	}
	pos := 0
	for _, a := range n.Args {
		if a.Name != "" {
			if i, ok := idx[a.Name]; ok {
				exprs[i] = a.Value
			} else {
				c.errorf(a.Value.Span(), "%q: unknown field %q", name, a.Name)
				c.synth(a.Value)
			}
			continue
		}
		if pos < len(exprs) {
			exprs[pos] = a.Value
			pos++
		} else {
			c.errorf(a.Value.Span(), "%q: too many arguments", name)
			c.synth(a.Value)
		}
	}
	return exprs
}

// constructVariant checks an enum variant used as a value constructor, e.g.
// 'Some(3)' (DESIGN-1b §3.6 C1): the variant's payload types are its parameters
// and the result is the owning enum's type. A nullary variant is not callable.
func (c *checker) constructVariant(n *ast.Call, sym *Symbol) Type {
	vd := sym.Variant
	if vd == nil {
		c.synthArgs(n)
		return c.enumType(sym)
	}
	if len(vd.Payload) == 0 {
		c.errorf(n.Span(), "variant %q is nullary and takes no payload", vd.Name)
		c.synthArgs(n)
		return c.enumType(sym)
	}
	pnames := make([]string, len(vd.Payload))
	c.bindCallArgs(vd.Name, n, pnames, vd.Payload, nil)
	return c.enumType(sym)
}

// enumType is the use-site enum type a variant symbol belongs to.
func (c *checker) enumType(sym *Symbol) Type {
	if sym.TypeDef != nil && sym.TypeDef.Enum != nil {
		return &types.Enum{Def: sym.TypeDef}
	}
	return types.Unknown
}

// bindCallArgs binds call arguments to declared parameters and checks each. The
// pure-positional call with no defaults keeps the Phase-0 arity/type messages; a
// call using named arguments or defaults goes through the general matcher.
func (c *checker) bindCallArgs(name string, n *ast.Call, pnames []string, ptypes []Type, defaults []bool) {
	if !hasNamedArg(n.Args) && !anyTrue(defaults) {
		if len(n.Args) != len(ptypes) {
			c.errorf(n.Span(), "function %q expects %d argument(s), got %d", name, len(ptypes), len(n.Args))
		}
		for i, a := range n.Args {
			if i < len(ptypes) {
				c.checkArg(name, i, a.Value, ptypes[i])
			} else {
				c.synth(a.Value)
			}
		}
		return
	}
	c.matchArgs(name, n, pnames, ptypes, defaults)
}

// matchArgs binds positional-then-named arguments to parameters, honoring
// defaults and reporting duplicate, unknown, and missing arguments.
func (c *checker) matchArgs(name string, n *ast.Call, pnames []string, ptypes []Type, defaults []bool) {
	seen := make([]bool, len(ptypes))
	idx := map[string]int{}
	for i, nm := range pnames {
		idx[nm] = i
	}
	pos := 0
	sawNamed := false
	for _, a := range n.Args {
		if a.Name == "" {
			if sawNamed {
				c.errorf(a.Value.Span(), "%q: a positional argument may not follow a named one", name)
			}
			if pos >= len(ptypes) {
				c.errorf(a.Value.Span(), "%q: too many arguments", name)
				c.synth(a.Value)
				continue
			}
			c.checkArg(name, pos, a.Value, ptypes[pos])
			seen[pos] = true
			pos++
			continue
		}
		sawNamed = true
		i, ok := idx[a.Name]
		if !ok {
			c.errorf(a.Value.Span(), "%q: unknown argument %q", name, a.Name)
			c.synth(a.Value)
			continue
		}
		if seen[i] {
			c.errorf(a.Value.Span(), "%q: argument %q set more than once", name, a.Name)
		}
		c.checkArg(name, i, a.Value, ptypes[i])
		seen[i] = true
	}
	for i := range ptypes {
		if !seen[i] && !hasDefault(defaults, i) {
			c.errorf(n.Span(), "%q: missing argument %q", name, argName(pnames, i))
		}
	}
}

// checkArg checks one argument against its parameter type and reports a mismatch.
func (c *checker) checkArg(name string, i int, e ast.Expr, want Type) Type {
	at := c.check(e, want)
	if !bad(at) && !bad(want) && !c.assignable(want, e, at) {
		c.errorf(e.Span(), "argument %d of %q: cannot use %s as %s", i+1, name, at, want)
	}
	return at
}

// --- value & type generic inference -------------------------------------------

// callGeneric checks a call to a generic function, binding its type and value
// parameters by local structural inference over the argument types (DESIGN-1b
// §4.7): a value parameter 'N' is fixed by the first array argument that mentions
// it and enforced on the rest; a type parameter 'T' is fixed by an argument's
// type. This is local, single-directional matching — not global unification — and
// the function body is never monomorphized.
func (c *checker) callGeneric(n *ast.Call, sig *FuncSig) Type {
	exprs := c.pairArgs(sig, n)
	subT := map[string]Type{}
	subV := map[string]types.ConstVal{}
	argTypes := make([]Type, len(sig.Params))
	for i := range sig.Params {
		if exprs[i] == nil {
			continue
		}
		argTypes[i] = c.synth(exprs[i])
		unify(sig.Params[i], argTypes[i], subT, subV)
	}
	for i := range sig.Params {
		if exprs[i] == nil {
			if !hasDefault(sig.Defaults, i) {
				c.errorf(n.Span(), "%q: missing argument %q", sig.Name, argName(sig.ParamNames, i))
			}
			continue
		}
		want := substitute(sig.Params[i], subT, subV)
		at := argTypes[i]
		if !bad(at) && !bad(want) && !c.assignable(want, exprs[i], at) {
			c.errorf(exprs[i].Span(), "argument %d of %q: cannot use %s as %s", i+1, sig.Name, at, want)
		}
	}
	if sig.Ret == nil {
		return Nil
	}
	return substitute(sig.Ret, subT, subV)
}

// pairArgs aligns a call's arguments to parameter positions (positional first,
// then named), returning nil in a position left to its default.
func (c *checker) pairArgs(sig *FuncSig, n *ast.Call) []ast.Expr {
	exprs := make([]ast.Expr, len(sig.Params))
	idx := map[string]int{}
	for i, nm := range sig.ParamNames {
		idx[nm] = i
	}
	pos := 0
	for _, a := range n.Args {
		if a.Name != "" {
			i, ok := idx[a.Name]
			switch {
			case !ok:
				c.errorf(a.Value.Span(), "%q: unknown argument %q", sig.Name, a.Name)
				c.synth(a.Value)
			case exprs[i] != nil:
				c.errorf(a.Value.Span(), "%q: argument %q set more than once", sig.Name, a.Name)
			default:
				exprs[i] = a.Value
			}
			continue
		}
		if pos < len(exprs) {
			exprs[pos] = a.Value
			pos++
		} else {
			c.errorf(a.Value.Span(), "%q: too many arguments", sig.Name)
			c.synth(a.Value)
		}
	}
	return exprs
}

// unify matches a declared parameter type against an argument type, binding any
// type parameters (subT) and value parameters (subV) it structurally exposes.
func unify(decl, actual Type, subT map[string]Type, subV map[string]types.ConstVal) {
	switch d := decl.(type) {
	case *types.Param:
		if _, ok := subT[d.Name]; !ok {
			subT[d.Name] = actual
		}
	case *types.Array:
		if d.N.Name != "" {
			if a, ok := actual.(*types.Array); ok && a.N.Known {
				if _, seen := subV[d.N.Name]; !seen {
					subV[d.N.Name] = a.N
				}
			}
		}
		if a, ok := actual.(*types.Array); ok {
			unify(d.Elem, a.Elem, subT, subV)
		}
	case *types.List:
		if a, ok := actual.(*types.List); ok {
			unify(d.Elem, a.Elem, subT, subV)
		}
	case *types.Set:
		if a, ok := actual.(*types.Set); ok {
			unify(d.Elem, a.Elem, subT, subV)
		}
	case *types.Map:
		if a, ok := actual.(*types.Map); ok {
			unify(d.Key, a.Key, subT, subV)
			unify(d.Val, a.Val, subT, subV)
		}
	case *types.Opt:
		if a, ok := actual.(*types.Opt); ok {
			unify(d.Elem, a.Elem, subT, subV)
		}
	}
}

// Substitute is the exported form of substitute: it replaces the bound type and
// value parameters in t, for the monomorphization stage (internal/mono), which
// reuses this one substitution engine to build each instance's type overlay
// (DESIGN-1c §4, FORK-A) rather than re-deriving types.
func Substitute(t Type, subT map[string]Type, subV map[string]types.ConstVal) Type {
	return substitute(t, subT, subV)
}

// Unify is the exported form of unify, matching a declared parameter type against
// a concrete argument type to bind a callee's type and value parameters. The
// monomorphization stage reuses it to resolve a generic call's type arguments from
// the concrete argument types it already holds (DESIGN-1c §4.2).
func Unify(decl, actual Type, subT map[string]Type, subV map[string]types.ConstVal) {
	unify(decl, actual, subT, subV)
}

// substitute replaces bound type parameters and symbolic array lengths in t with
// their inferred values, recursing through composite and nominal (struct/enum)
// type arguments so a container like 'Box[T]' specializes to 'Box[int]'.
func substitute(t Type, subT map[string]Type, subV map[string]types.ConstVal) Type {
	switch x := t.(type) {
	case *types.Param:
		if b, ok := subT[x.Name]; ok {
			return b
		}
		return x
	case *types.Array:
		n := x.N
		if n.Name != "" {
			if b, ok := subV[n.Name]; ok {
				n = b
			}
		}
		return &types.Array{Elem: substitute(x.Elem, subT, subV), N: n}
	case *types.List:
		return &types.List{Elem: substitute(x.Elem, subT, subV)}
	case *types.Set:
		return &types.Set{Elem: substitute(x.Elem, subT, subV)}
	case *types.Map:
		return &types.Map{Key: substitute(x.Key, subT, subV), Val: substitute(x.Val, subT, subV)}
	case *types.Opt:
		return &types.Opt{Elem: substitute(x.Elem, subT, subV)}
	case *types.Struct:
		if len(x.Args) == 0 {
			return x
		}
		return &types.Struct{Def: x.Def, Args: substituteAll(x.Args, subT, subV)}
	case *types.Enum:
		if len(x.Args) == 0 {
			return x
		}
		return &types.Enum{Def: x.Def, Args: substituteAll(x.Args, subT, subV)}
	}
	return t
}

// substituteArgs specializes a nominal type's member type (a field or payload) by
// binding the type's declared parameters to the use-site arguments: 'Box[int]'
// reads field 'value: T' as 'int'. With no parameters or arguments it is a no-op,
// so a non-generic type's field type is returned unchanged.
func substituteArgs(params []*types.Param, args []Type, member Type) Type {
	if len(params) == 0 || len(args) == 0 {
		return member
	}
	subT := map[string]Type{}
	for i, p := range params {
		if i < len(args) {
			subT[p.Name] = args[i]
		}
	}
	return substitute(member, subT, nil)
}

// substituteAll substitutes every type in a slice, returning a fresh slice.
func substituteAll(ts []Type, subT map[string]Type, subV map[string]types.ConstVal) []Type {
	out := make([]Type, len(ts))
	for i, t := range ts {
		out[i] = substitute(t, subT, subV)
	}
	return out
}

// --- access -------------------------------------------------------------------

// inferField types a '.id' field access on a struct (DESIGN-1b §4.6). A method or
// a field on a non-struct is not modelled beyond the grammar-needed built-ins
// (FORK-4), so it yields Unknown rather than cascading.
func (c *checker) inferField(n *ast.Field) Type {
	xt := c.synth(n.X)
	if st, ok := xt.(*types.Struct); ok && st.Def.Struct != nil {
		if f := findField(st.Def, n.Name); f != nil {
			return substituteArgs(st.Def.Params, st.Args, f.Type)
		}
		c.errorf(n.Span(), "type %s has no field %q", st.Def.Name, n.Name)
		return Invalid
	}
	return types.Unknown
}

// inferTupleIndex types a static tuple element access 't.0'.
func (c *checker) inferTupleIndex(n *ast.TupleIndex) Type {
	xt := c.synth(n.X)
	if t, ok := xt.(*types.Tuple); ok {
		if n.Index >= 0 && n.Index < len(t.Elems) {
			return t.Elems[n.Index]
		}
		c.errorf(n.Span(), "tuple index %d is out of range (length %d)", n.Index, len(t.Elems))
		return Invalid
	}
	return types.Unknown
}

// inferBracket types a settled '[ … ]' postfix (DESIGN-1b §4.6): an index yields
// the container's element/value type; a type-argument bracket in value position
// is un-modelled (its resolved type arguments are recorded for later phases).
func (c *checker) inferBracket(n *ast.Bracket) Type {
	res := c.info.Brackets[n]
	if res.Kind == BracketTypeArg {
		res.Args = c.typeArgExprs(n.Elems)
		c.info.Brackets[n] = res
		return types.Unknown
	}
	xt := c.synth(n.Base)
	var elem Type = types.Unknown
	switch b := xt.(type) {
	case *types.List:
		elem = b.Elem
		c.synthIndices(n.Elems)
	case *types.Array:
		elem = b.Elem
		c.synthIndices(n.Elems)
	case *types.Map:
		elem = b.Val
		if len(n.Elems) == 1 {
			c.checkElem(n.Elems[0], b.Key, "map key")
		} else {
			c.synthIndices(n.Elems)
		}
	default:
		c.synthIndices(n.Elems)
	}
	res.Elem = elem
	c.info.Brackets[n] = res
	return elem
}

func (c *checker) synthIndices(elems []ast.Expr) {
	for _, e := range elems {
		c.synth(e)
	}
}

// typeArgExprs reads the type arguments written in a value-position bracket,
// best-effort (Unknown for anything that is not a recognizable type).
func (c *checker) typeArgExprs(elems []ast.Expr) []Type {
	if len(elems) == 0 {
		return nil
	}
	out := make([]Type, len(elems))
	for i, e := range elems {
		out[i] = c.exprAsType(e)
	}
	return out
}

// --- closures -----------------------------------------------------------------

// checkClosure checks a closure against an expected function type, inferring an
// omitted parameter type from the expected type's corresponding parameter
// (DESIGN-1b §4.4).
func (c *checker) checkClosure(fe *ast.FnExpr, want *types.Fn) Type {
	c.pushScope()
	for i := range fe.Params {
		p := &fe.Params[i]
		var pt Type
		switch {
		case p.Type != nil:
			pt = c.resolveType(p.Type)
		case i < len(want.Params):
			pt = want.Params[i].Type
		default:
			c.errorf(p.Span(), "cannot infer the type of closure parameter %q", p.Name)
			pt = Invalid
		}
		c.declare(p.Span(), p.Name, pt, false)
	}
	saved := c.curFn
	c.curFn = &FuncSig{Ret: retOrUnknown(want.Ret)}
	if fe.Body != nil {
		c.checkBlock(fe.Body)
	}
	c.curFn = saved
	c.popScope()
	return want
}

// synthFn synthesizes a closure's type when there is no expected type; every
// parameter must then carry an explicit type annotation.
func (c *checker) synthFn(fe *ast.FnExpr) Type {
	fn := &types.Fn{}
	c.pushScope()
	for i := range fe.Params {
		p := &fe.Params[i]
		var pt Type
		if p.Type != nil {
			pt = c.resolveType(p.Type)
		} else {
			c.errorf(p.Span(), "cannot infer the type of closure parameter %q; add ': type' or a context", p.Name)
			pt = Invalid
		}
		fn.Params = append(fn.Params, types.Param0{Type: pt, ByRef: p.Ref})
		c.declare(p.Span(), p.Name, pt, false)
	}
	if fe.Ret != nil {
		fn.Ret = c.resolveType(fe.Ret)
	}
	saved := c.curFn
	c.curFn = &FuncSig{Ret: retOrUnknown(fn.Ret)}
	if fe.Body != nil {
		c.checkBlock(fe.Body)
	}
	c.curFn = saved
	c.popScope()
	return fn
}

// retOrUnknown is a closure body's expected return type: the declared return, or
// Unknown when it is inferred (so a 'return e' inside is not flagged).
func retOrUnknown(ret Type) Type {
	if ret == nil {
		return types.Unknown
	}
	return ret
}

// --- helpers ------------------------------------------------------------------

func hasNamedArg(args []ast.Arg) bool {
	for _, a := range args {
		if a.Name != "" {
			return true
		}
	}
	return false
}

// hasDefault reports whether parameter i has a declared default value.
func hasDefault(defaults []bool, i int) bool {
	return i < len(defaults) && defaults[i]
}

func anyTrue(bs []bool) bool {
	for _, b := range bs {
		if b {
			return true
		}
	}
	return false
}

func argName(names []string, i int) string {
	if i < len(names) {
		return names[i]
	}
	return "?"
}
