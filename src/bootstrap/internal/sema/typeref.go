package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// resolveType turns a syntactic type into a types.Type, reporting a diagnostic
// for an unknown or ill-formed type (DESIGN-1b §3.7). It consults the generic
// parameters in scope (c.typeParams) and the module surface for user types, and
// builds the composite forms (optional, tuple, array, channel, pointer, function,
// and the built-in generic constructors list/map/set/chan/ptr).
func (c *checker) resolveType(t ast.Type) types.Type {
	switch n := t.(type) {
	case nil:
		return Nil
	case *ast.TypeRef:
		return c.resolveTypeRef(n)
	case *ast.OptType:
		return &types.Opt{Elem: c.resolveType(n.Elem)}
	case *ast.TupleType:
		elems := make([]types.Type, len(n.Elems))
		for i, e := range n.Elems {
			elems[i] = c.resolveType(e)
		}
		return &types.Tuple{Elems: elems}
	case *ast.ArrayType:
		return &types.Array{Elem: c.resolveType(n.Elem), N: c.arrayLen(n.Count)}
	case *ast.ChanType:
		return &types.Chan{Elem: c.resolveType(n.Elem), Dir: types.ChanDir(n.Dir)}
	case *ast.PtrType:
		if n.Elem == nil {
			return &types.Ptr{}
		}
		return &types.Ptr{Elem: c.resolveType(n.Elem)}
	case *ast.FnType:
		return c.resolveFnType(n)
	case *ast.ConstExpr:
		// a value-generic argument sitting in a type-args list; not a type itself
		return types.Unknown
	}
	c.errorf(t.Span(), "unsupported type in this phase")
	return Invalid
}

// resolveFnType builds a function type from its syntactic form.
func (c *checker) resolveFnType(n *ast.FnType) types.Type {
	fn := &types.Fn{Unsafe: n.Unsafe}
	for _, p := range n.Params {
		fn.Params = append(fn.Params, types.Param0{Type: c.resolveType(p.Type), ByRef: p.Ref})
	}
	if n.Ret != nil {
		fn.Ret = c.resolveType(n.Ret)
	}
	return fn
}

// arrayLen resolves an array length position. A bare value-generic parameter name
// becomes a symbolic length (bound by inference at each call site); anything else
// is folded to a compile-time value.
func (c *checker) arrayLen(ce *ast.ConstExpr) types.ConstVal {
	if ce == nil {
		return types.ConstVal{}
	}
	if id, ok := ce.X.(*ast.Ident); ok {
		if vp, ok := c.typeParams[id.Name].(*types.ValParam); ok {
			return types.ConstVal{Name: vp.Name}
		}
	}
	if v, ok := c.foldConst(ce.X); ok {
		return v
	}
	c.errorf(ce.Span(), "array length must be a compile-time constant")
	return types.ConstVal{}
}

// resolveTypeRef resolves a named type, applying any type arguments.
func (c *checker) resolveTypeRef(ref *ast.TypeRef) types.Type {
	// A cross-module type reached through a namespace, `text.Slice` — parsed as a
	// single-segment projection on a name that resolves to an imported namespace.
	// It resolves to the merged module type under its canonical mangling, with any
	// type arguments applied, so an imported type is nameable in a signature and a
	// generic imported type monomorphizes across the module boundary.
	if len(ref.Proj) == 1 {
		if sym := c.module.lookup(ref.Name); sym != nil && sym.Kind == SymNamespace {
			res := c.resolveMember(sym, ref.Name, ref.Proj[0])
			if res.Found && res.Sym != nil && res.Sym.Kind == SymType {
				return c.namedTypeUse(res.Sym, c.typeArgs(ref.Args))
			}
			if res.Private {
				c.errorf(ref.Span(), "%q is not a public type of module %q", ref.Proj[0], ref.Name)
				return Invalid
			}
			c.errorf(ref.Span(), "module %q has no public type %q", ref.Name, ref.Proj[0])
			return Invalid
		}
	}
	if len(ref.Proj) != 0 {
		// an associated-type projection 'X.Item': resolve to the impl's binding when
		// the concrete impl is known, else keep it abstract (U3, DESIGN-1c §3).
		base := c.projBase(ref)
		if c.info.Specs != nil {
			if concrete, ok := c.info.Specs.resolveProjection(base, ref.Proj); ok {
				return concrete
			}
		}
		return &types.Proj{On: base, Path: ref.Proj}
	}
	if ref.Name == "This" && c.curSelf != nil {
		return c.curSelf
	}
	// An associated type named in a spec or impl signature ('fn get() -> Item')
	// resolves to the impl's concrete binding when known, else to the abstract
	// projection This.Item (U3, DESIGN-1c §3 associated-type resolution).
	if c.curImpl != nil {
		if t, ok := c.curImpl.AssocBind[ref.Name]; ok {
			return t
		}
	}
	if c.curSpec != nil {
		for _, at := range c.curSpec.AssocTypes {
			if at.Name == ref.Name {
				return &types.Proj{On: c.curSelf, Path: []string{ref.Name}}
			}
		}
	}
	if tp, ok := c.typeParams[ref.Name]; ok {
		return tp
	}
	if p := primitiveNamed(ref.Name); p != nil {
		if len(ref.Args) != 0 {
			c.errorf(ref.Span(), "type %q takes no type arguments", ref.Name)
		}
		return p
	}
	if ctor, ok := c.builtinGeneric(ref); ok {
		return ctor
	}
	if sum, ok := c.builtinSum(ref); ok {
		return sum
	}
	if sym := c.module.lookup(ref.Name); sym != nil && sym.Kind == SymType {
		// A9: a transparent type alias `type X = Y` resolves to its UNDERLYING type, so
		// the alias name is never retained in ExprTypes/BindTypes (and never splits a
		// carrier/tuple String() key). A generic alias `type X[T] = …` is not yet
		// supported and is gated cleanly (FORK-A9: non-generic aliases only).
		if td, ok := sym.Decl.(*ast.TypeDecl); ok && td.Alias != nil {
			if td.Generics != nil {
				c.errorf(ref.Span(), "a generic type alias is not yet supported")
				return Invalid
			}
			return c.resolveType(td.Alias)
		}
		return c.namedTypeUse(sym, c.typeArgs(refArgExprs(ref)))
	}
	c.errorf(ref.Span(), "unknown type %q", ref.Name)
	return Invalid
}

// builtinGeneric builds one of the built-in generic constructors (list/set/map/
// chan/ptr) when the name matches, reporting an arity mismatch.
func (c *checker) builtinGeneric(ref *ast.TypeRef) (types.Type, bool) {
	arg := func(i int) types.Type {
		if i < len(ref.Args) {
			return c.resolveType(ref.Args[i])
		}
		return types.Unknown
	}
	switch ref.Name {
	case "list":
		return &types.List{Elem: arg(0)}, true
	case "set":
		return &types.Set{Elem: arg(0)}, true
	case "map":
		return &types.Map{Key: arg(0), Val: arg(1)}, true
	case "chan":
		return &types.Chan{Elem: arg(0)}, true
	case "Ref":
		return &types.Ref{Elem: arg(0)}, true
	case "ptr":
		if len(ref.Args) == 0 {
			return &types.Ptr{}, true
		}
		return &types.Ptr{Elem: arg(0)}, true
	}
	return nil, false
}

// builtinSum resolves the built-in sum types 'Either[L, R]' and 'Result[T]'
// (= 'Either[T, Err]') so a function may name them in its signature and the
// null-safety operators can type against them (DESIGN-1b §6 C2).
func (c *checker) builtinSum(ref *ast.TypeRef) (types.Type, bool) {
	switch ref.Name {
	case "Either":
		if len(ref.Args) != 2 {
			c.errorf(ref.Span(), "Either takes 2 type arguments, got %d", len(ref.Args))
			return Invalid, true
		}
		return &types.Either{Left: c.resolveType(ref.Args[0]), Right: c.resolveType(ref.Args[1])}, true
	case "Result":
		if len(ref.Args) != 1 {
			c.errorf(ref.Span(), "Result takes 1 type argument, got %d", len(ref.Args))
			return Invalid, true
		}
		return &types.Either{Left: c.resolveType(ref.Args[0]), Right: errType}, true
	}
	return nil, false
}

// namedTypeUse is the use-site type of a user type symbol (a nominal struct or
// enum with any type arguments applied); an alias or spec is opaque this
// iteration.
func (c *checker) namedTypeUse(sym *Symbol, args []types.Type) types.Type {
	def := sym.TypeDef
	switch {
	case def == nil:
		return types.Unknown
	case def.Struct != nil:
		return &types.Struct{Def: def, Args: args}
	case def.Enum != nil:
		return &types.Enum{Def: def, Args: args}
	default:
		return types.Unknown
	}
}

// projBase resolves the head of a projection 'X.Item' to a type (best-effort, no
// diagnostic): 'This' inside an impl/spec, a generic parameter, or a named type.
func (c *checker) projBase(ref *ast.TypeRef) types.Type {
	if ref.Name == "This" && c.curSelf != nil {
		return c.curSelf
	}
	if tp, ok := c.typeParams[ref.Name]; ok {
		return tp
	}
	return c.resolveTypeName(ref.Name)
}

// resolveTypeName resolves a bare type name without reporting a diagnostic,
// returning Unknown when the name is not a type. It backs the best-effort reading
// of type arguments and value-generic bounds.
func (c *checker) resolveTypeName(name string) types.Type {
	if tp, ok := c.typeParams[name]; ok {
		return tp
	}
	if p := primitiveNamed(name); p != nil {
		return p
	}
	if sym := c.module.lookup(name); sym != nil && sym.Kind == SymType {
		return c.namedTypeUse(sym, nil)
	}
	return types.Unknown
}

// exprAsType reads an expression that sits in a type-argument position as a type,
// returning Unknown (without a diagnostic) when it is not a recognizable type.
func (c *checker) exprAsType(e ast.Expr) types.Type {
	switch x := e.(type) {
	case *ast.Ident:
		return c.resolveTypeName(x.Name)
	case *ast.Bracket:
		if base, ok := x.Base.(*ast.Ident); ok {
			ref := &ast.TypeRef{Name: base.Name}
			if t, ok := c.builtinGeneric2(ref.Name, x.Elems); ok {
				return t
			}
		}
	}
	return types.Unknown
}

// builtinGeneric2 reads a nested generic type from a bracket's element
// expressions ('list[int]' written in value position), best-effort.
func (c *checker) builtinGeneric2(name string, elems []ast.Expr) (types.Type, bool) {
	arg := func(i int) types.Type {
		if i < len(elems) {
			return c.exprAsType(elems[i])
		}
		return types.Unknown
	}
	switch name {
	case "list":
		return &types.List{Elem: arg(0)}, true
	case "set":
		return &types.Set{Elem: arg(0)}, true
	case "map":
		return &types.Map{Key: arg(0), Val: arg(1)}, true
	}
	return nil, false
}

func (c *checker) typeArgs(elems []ast.Type) []types.Type {
	if len(elems) == 0 {
		return nil
	}
	out := make([]types.Type, len(elems))
	for i, e := range elems {
		out[i] = c.resolveType(e)
	}
	return out
}

// refArgExprs returns a TypeRef's type-argument nodes (already typed as ast.Type).
func refArgExprs(ref *ast.TypeRef) []ast.Type { return ref.Args }

// primitiveNamed returns the primitive or fixed-width type a name spells, or nil.
func primitiveNamed(name string) types.Type {
	switch name {
	case "int":
		return types.Int
	case "uint":
		return types.Uint
	case "float":
		return types.Float
	case "bool":
		return types.Bool
	case "str":
		return types.Str
	case "nil":
		return types.Nil
	case "rune":
		return types.Rune
	case "byte":
		return types.Byte
	}
	if f := fixedNamed(name); f != nil {
		return f
	}
	return nil
}

// fixedNamed parses a fixed-width numeric name (i8..i64, u8..u64, f32/f64).
func fixedNamed(name string) types.Type {
	if len(name) < 2 {
		return nil
	}
	var signed, float bool
	switch name[0] {
	case 'i':
		signed = true
	case 'u':
		signed = false
	case 'f':
		float = true
	default:
		return nil
	}
	bits := 0
	for _, ch := range name[1:] {
		if ch < '0' || ch > '9' {
			return nil
		}
		bits = bits*10 + int(ch-'0')
	}
	switch {
	case float && (bits == 32 || bits == 64):
		return &types.Fixed{Bits: bits, Float: true}
	case !float && (bits == 8 || bits == 16 || bits == 32 || bits == 64):
		return &types.Fixed{Bits: bits, Signed: signed}
	}
	return nil
}
