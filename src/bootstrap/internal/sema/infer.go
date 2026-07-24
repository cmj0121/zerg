package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// --- calls & construction -----------------------------------------------------

// inferCall resolves a call to one of three callee kinds (DESIGN-1b §4.6): a type
// name is construction 'T(...)', a function name is a direct call, and a local
// value of function type is an indirect call. A complex callee expression is
// synthesized and, when it is a function value, called indirectly.
func (c *checker) inferCall(n *ast.Call) Type {
	if t, ok := c.builtinCall(n); ok {
		return t
	}
	if fld, ok := n.Callee.(*ast.Field); ok {
		if t, handled := c.namespaceCall(n, fld); handled {
			return t
		}
		// A method on a raw-pointer receiver (`p.load()` / `.store()` / `.offset()`) is
		// a compiler-recognized unsafe intrinsic, dispatched on the receiver's static
		// type; a receiver of any other type falls through to the ordinary paths.
		if pt, ok := c.synth(fld.X).(*types.Ptr); ok {
			t, _ := c.ptrMethodCall(n, fld, pt)
			return t
		}
		// A method on a built-in list receiver (`.len()` / `.get(i)` / `.append(v)`) is a
		// compiler intrinsic (list is not a user struct), dispatched on the receiver's
		// static type before the nominal-method path.
		if lt, ok := c.synth(fld.X).(*types.List); ok {
			return c.listMethodCall(n, fld, lt)
		}
		// A method on a built-in map receiver (`.len()` / `.get(k)`) is a compiler
		// intrinsic, dispatched on the receiver's static type before the nominal path.
		if mt, ok := c.synth(fld.X).(*types.Map); ok {
			return c.mapMethodCall(n, fld, mt)
		}
		// A '.method()' on a concrete nominal receiver dispatches to the type's method
		// namespace (inherent or spec method alike); only when no such method exists does
		// it fall through to the field-access path and its "no field" diagnostic.
		if t, ok := c.methodCall(n, fld); ok {
			return t
		}
	}
	if br, ok := n.Callee.(*ast.Bracket); ok {
		if id, ok := br.Base.(*ast.Ident); ok {
			if sig, ok := c.info.Funcs[id.Name]; ok && sig.Generic != nil {
				return c.callGenericExplicit(n, br, sig)
			}
		}
	}
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
				// record the callee's function type so the backend spells the call as an
				// indirect call through the value, not a direct named call.
				c.info.ExprTypes[id] = s.typ
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

// builtinCall handles the Phase 1d runtime builtins that no stdlib supplies yet:
// the 'Ref[T]' box constructor and its reader 'deref'. It recognises 'Ref(v)'
// (element inferred from v), 'Ref[T](v)' (element given explicitly), and
// 'deref(r) -> T'. A name shadowed by a local or module binding is left to the
// ordinary call paths, so the builtins never mask a user symbol.
func (c *checker) builtinCall(n *ast.Call) (Type, bool) {
	switch callee := n.Callee.(type) {
	case *ast.Ident:
		if c.shadowed(callee.Name) {
			return nil, false
		}
		switch callee.Name {
		case "Ref":
			return c.constructRef(n, nil), true
		case "deref":
			return c.derefRef(n), true
		case "__zrt_write":
			return c.writeIntrinsic(n, Str), true
		case "__zrt_write_int":
			return c.writeIntrinsic(n, Int), true
		case "__zrt_atomic_load":
			return c.atomicIntrinsic(n, 1, Int), true
		case "__zrt_atomic_store", "__zrt_atomic_swap", "__zrt_atomic_add":
			return c.atomicIntrinsic(n, 2, Int), true
		case "__zrt_atomic_cas":
			return c.atomicIntrinsic(n, 3, Bool), true
		case "addr":
			// addr(x) -> ptr[T]: the address of an addressable value (U1).
			return c.ptrAddr(n), true
		case "ptr":
			// ptr(p) / ptr(u) -> bare `ptr`: a raw-address cast (any ptr or uint).
			return c.ptrCast(n, &types.Ptr{}), true
		case "uint":
			// uint(p) -> uint: a ptr-to-integer cast, recognized only when the argument
			// is a raw pointer, so it does not mask the numeric `uint(x)` conversion the
			// scalar path below handles.
			if len(n.Args) == 1 {
				if _, ok := c.synth(n.Args[0].Value).(*types.Ptr); ok {
					c.unsafeOp(n.Span(), "a pointer cast")
					return types.Uint, true
				}
			}
		}
		// A callee naming a primitive type is a CONVERSION, `T(x)` — a re-construction
		// of x's value as a T (docs/types.md). Building a `str` is not this mechanism
		// (it validates a list[byte]/list[rune]), so it is reported rather than guessed.
		if target := primitiveNamed(callee.Name); target != nil {
			if _, ok := ScalarOf(target); ok {
				return c.scalarConversion(n, callee.Name, target), true
			}
			if target == Str {
				c.errorf(n.Span(), "converting to str is not yet supported; build one from a list[byte]/list[rune]")
				c.synthArgs(n)
				return Str, true
			}
		}
	case *ast.Bracket:
		id, ok := callee.Base.(*ast.Ident)
		if !ok {
			return nil, false
		}
		switch id.Name {
		case "Ref":
			if c.shadowed("Ref") {
				return nil, false
			}
			var elem Type
			if len(callee.Elems) == 1 {
				elem = c.exprAsType(callee.Elems[0])
			}
			return c.constructRef(n, elem), true
		case "ptr":
			// ptr[T](p) -> ptr[T]: a typed-pointer cast (U1).
			if c.shadowed("ptr") {
				return nil, false
			}
			var elem Type
			if len(callee.Elems) == 1 {
				elem = c.exprAsType(callee.Elems[0])
			}
			return c.ptrCast(n, &types.Ptr{Elem: elem}), true
		}
	}
	return nil, false
}

// shadowed reports whether a name is bound by a local or module symbol, so a
// builtin spelling defers to the user's binding of the same name.
func (c *checker) shadowed(name string) bool {
	return c.lookup(name) != nil || c.module.lookup(name) != nil
}

// constructRef checks a 'Ref[T](v)' box construction: it takes exactly one value
// argument, uses the explicit element type when given (else infers it from the
// argument), and yields 'Ref[elem]'. The argument is checked against the element
// type so a mismatch is reported.
func (c *checker) constructRef(n *ast.Call, elem Type) Type {
	if len(n.Args) != 1 {
		c.errorf(n.Span(), "Ref(v) takes exactly one value argument, got %d", len(n.Args))
		c.synthArgs(n)
		return Invalid
	}
	arg := n.Args[0].Value
	if elem == nil || bad(elem) {
		elem = c.synth(arg)
	} else {
		vt := c.check(arg, elem)
		if !bad(elem) && !bad(vt) && !c.assignable(elem, arg, vt) {
			c.errorf(n.Span(), "cannot store %s in a Ref[%s]", vt, elem)
		}
	}
	if elem == Nil {
		c.errorf(n.Span(), "cannot infer a Ref element type from nil")
		return Invalid
	}
	return &types.Ref{Elem: elem}
}

// derefRef checks 'deref(r)': it takes one 'Ref[T]' argument and yields a copy of
// the boxed T. Reading a Ref whose element itself holds a Ref is deferred (the
// reader would have to retain the inner boxes); this iteration reads POD elements.
func (c *checker) derefRef(n *ast.Call) Type {
	if len(n.Args) != 1 {
		c.errorf(n.Span(), "deref(r) takes exactly one argument, got %d", len(n.Args))
		c.synthArgs(n)
		return Invalid
	}
	rt := c.synth(n.Args[0].Value)
	if ref, ok := rt.(*types.Ref); ok {
		return ref.Elem
	}
	if !bad(rt) {
		c.errorf(n.Span(), "deref expects a Ref[T], got %s", rt)
	}
	return Invalid
}

// namespaceCall checks a single-level imported-module member call `ns.member(...)`
// (the bundle-import MVP, Decision C). When the callee's base names an imported
// namespace, the member resolves to the bundled module's public function under its
// mangled name `ns__member` (build.bundle), and the call is checked like any direct
// call. It reports true once it has taken responsibility for a namespace callee, so
// inferCall does not fall through to its generic-callee handling.
func (c *checker) namespaceCall(n *ast.Call, fld *ast.Field) (Type, bool) {
	id, ok := fld.X.(*ast.Ident)
	if !ok {
		return nil, false
	}
	sym := c.module.lookup(id.Name)
	if sym == nil || sym.Kind != SymNamespace {
		return nil, false
	}
	res := c.resolveMember(sym, id.Name, fld.Name)
	if !res.Found {
		c.memberError(fld, id.Name, fld.Name, res.Private)
		c.synthArgs(n)
		return Invalid, true
	}
	c.recordMember(fld, res.Key)
	if sig, ok := c.info.Funcs[res.Key]; ok {
		return c.callFunc(n, sig), true
	}
	// A cross-module TYPE reached through the namespace as a constructor callee
	// `ns.T(...)` builds that type (generalizing beyond the function-only bundle).
	if res.Sym != nil && res.Sym.Kind == SymType {
		return c.construct(n, res.Sym), true
	}
	c.memberError(fld, id.Name, fld.Name, false)
	c.synthArgs(n)
	return Invalid, true
}

// namespaceMember types a non-call member access `ns.member` on an imported
// namespace: a cross-module constant or value binding yields its type, and a
// cross-module function named without a call is rejected (functions are not first-
// class values in this phase, matching the bare-name rule). It generalizes the
// function-only bundle to constants (member types are handled in resolveTypeRef).
func (c *checker) namespaceMember(n *ast.Field, sym *Symbol, local string) Type {
	res := c.resolveMember(sym, local, n.Name)
	if !res.Found {
		c.memberError(n, local, n.Name, res.Private)
		return Invalid
	}
	c.recordMember(n, res.Key)
	if res.Sym != nil {
		switch res.Sym.Kind {
		case SymConst, SymVar:
			return res.Sym.Type
		case SymType:
			c.errorf(n.Span(), "type %q used as a value", n.Name)
			return Invalid
		}
	}
	if _, ok := c.info.Funcs[res.Key]; ok {
		// A same-module function name is a first-class value (check.go); a cross-module
		// one reached through a namespace member needs its resolved cross-module linkage
		// as a value, which is not wired yet.
		c.errorf(n.Span(), "a function from another module is not yet a first-class value: %q; wrap it in a local function", n.Name)
		return Invalid
	}
	c.memberError(n, local, n.Name, false)
	return Invalid
}

// writeIntrinsic checks a compiler write intrinsic `__zrt_write(fd, value)`: a file
// descriptor and a value of vt (str or int). It is the MVP leaf the stdlib `io`
// module lowers onto until the FFI binder lands; it yields the byte count (int).
func (c *checker) writeIntrinsic(n *ast.Call, vt Type) Type {
	if len(n.Args) != 2 {
		c.errorf(n.Span(), "write intrinsic takes (fd, value), got %d argument(s)", len(n.Args))
		c.synthArgs(n)
		return Invalid
	}
	c.check(n.Args[0].Value, Int)
	c.check(n.Args[1].Value, vt)
	return Int
}

// atomicIntrinsic checks a compiler atomic-cell intrinsic `__zrt_atomic_<op>(a,
// …)` — the MVP leaf the stdlib `atomic` module lowers onto (Phase 1f U2). The
// first argument is the shared cell, a `Ref[int]`; any remaining arguments are the
// int operands (the value to store/swap/add, or the expect+desired of a CAS). It
// yields ret (Int for the value ops, Bool for a CAS).
func (c *checker) atomicIntrinsic(n *ast.Call, arity int, ret Type) Type {
	if len(n.Args) != arity {
		c.errorf(n.Span(), "atomic intrinsic takes %d argument(s), got %d", arity, len(n.Args))
		c.synthArgs(n)
		return Invalid
	}
	at := c.synth(n.Args[0].Value)
	if ref, ok := at.(*types.Ref); !ok || ref.Elem != Int {
		if !bad(at) {
			c.errorf(n.Args[0].Value.Span(), "atomic intrinsic expects a Ref[int] cell, got %s", at)
		}
	}
	for _, a := range n.Args[1:] {
		c.check(a.Value, Int)
	}
	return ret
}

func (c *checker) synthArgs(n *ast.Call) {
	for _, a := range n.Args {
		c.synth(a.Value)
	}
}

// callFunc checks a direct or namespaced call to a named function, dispatching to
// the generic path when the function has type or value parameters.
func (c *checker) callFunc(n *ast.Call, sig *FuncSig) Type {
	c.checkUnsafeCall(sig, n.Span())
	if sig.Generic != nil {
		return c.callGeneric(n, sig)
	}
	c.bindCallArgs(sig.Name, n, sig.ParamNames, sig.Params, sig.Defaults)
	c.checkByRefArgs(sig, n)
	return sig.Ret
}

// checkUnsafeCall enforces group 12's rule that an unsafe call is legal only inside
// an unsafe context: a call to an `unsafe fn` — including a `fn` declared inside a
// module-level `unsafe { }` group (Phase 1h U2) — is rejected outside an `unsafe fn`
// body or `unsafe { }` block.
func (c *checker) checkUnsafeCall(sig *FuncSig, span token.Span) {
	if sig == nil || sig.Decl == nil || c.inUnsafe {
		return
	}
	if sig.Decl.Unsafe {
		c.errorf(span, "call to unsafe fn %q is unsafe; call it inside an `unsafe { }` block", sig.Name)
	}
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
	if def := sym.TypeDef; def != nil && len(def.Params) > 0 {
		return c.constructVariantGeneric(n, def, vd)
	}
	pnames := make([]string, len(vd.Payload))
	c.bindCallArgs(vd.Name, n, pnames, vd.Payload, nil)
	return c.enumType(sym)
}

// constructVariantGeneric checks a generic enum's payload variant construction —
// 'Full(7)' on 'enum Box[T]' — inferring the enum's type arguments from the
// payload arguments, the enum analogue of constructGeneric (DESIGN-1c §4, generic
// enum specialization). It unifies each declared payload type against its
// argument, checks the argument against the resulting concrete type, and yields
// the applied enum type 'Box[int]' so monomorphization specializes it.
func (c *checker) constructVariantGeneric(n *ast.Call, def *types.TypeDef, vd *types.VariantDef) Type {
	subT := map[string]Type{}
	subV := map[string]types.ConstVal{}
	argTypes := make([]Type, len(vd.Payload))
	for i := range vd.Payload {
		if i < len(n.Args) {
			argTypes[i] = c.synth(n.Args[i].Value)
			unify(vd.Payload[i], argTypes[i], subT, subV)
		}
	}
	if len(n.Args) != len(vd.Payload) {
		c.errorf(n.Span(), "variant %q expects %d payload value(s), got %d", vd.Name, len(vd.Payload), len(n.Args))
	}
	for i := range vd.Payload {
		if i >= len(n.Args) {
			continue
		}
		want := substitute(vd.Payload[i], subT, subV)
		if at := argTypes[i]; !bad(at) && !bad(want) && !c.assignable(want, n.Args[i].Value, at) {
			c.errorf(n.Args[i].Value.Span(), "payload %d of variant %q: cannot use %s as %s", i+1, vd.Name, at, want)
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
	return &types.Enum{Def: def, Args: args}
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
	c.checkBounds(n.Span(), sig.Generic, subT)
	if sig.Ret == nil {
		return Nil
	}
	return substitute(sig.Ret, subT, subV)
}

// callGenericExplicit types an explicit type/value-argument call 'f[int](x)' /
// 'fill[3](9)' (B1): it binds the callee's generic parameters from the bracket's
// arguments — a value parameter from a folded const, a type parameter from the
// resolved type argument — infers any remaining from the value arguments, checks
// each argument against the resulting concrete parameter type, and yields the
// substituted return type.
func (c *checker) callGenericExplicit(n *ast.Call, br *ast.Bracket, sig *FuncSig) Type {
	subT := map[string]Type{}
	subV := map[string]types.ConstVal{}
	res := c.info.Brackets[br]
	for i, name := range sig.Generic.Names {
		if _, isVal := sig.Generic.Params[name].(*types.ValParam); isVal {
			if i < len(br.Elems) {
				if v, ok := c.foldConst(br.Elems[i]); ok {
					subV[name] = v
					continue
				}
			}
		}
		if i < len(res.Args) && res.Args[i] != nil && !bad(res.Args[i]) {
			subT[name] = res.Args[i]
		}
	}
	exprs := c.pairArgs(sig, n)
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
		if at := argTypes[i]; !bad(at) && !bad(want) && !c.assignable(want, exprs[i], at) {
			c.errorf(exprs[i].Span(), "argument %d of %q: cannot use %s as %s", i+1, sig.Name, at, want)
		}
	}
	c.checkBounds(n.Span(), sig.Generic, subT)
	if sig.Ret == nil {
		return Nil
	}
	return substitute(sig.Ret, subT, subV)
}

// checkBounds verifies that each of a generic call's inferred type arguments
// satisfies its parameter's spec bounds (DESIGN-1c §3.2): '[T: Ord]' called at a
// concrete type needs an 'impl Ord' for it — one written by hand or synthesized by
// '#[derive(Ord)]'. It is the use-site half of bound resolution that 1c adds over
// 1b's abstract body check.
func (c *checker) checkBounds(at token.Span, env *GenericEnv, subT map[string]Type) {
	if env == nil || c.info.Specs == nil {
		return
	}
	for name, pt := range env.Params {
		p, ok := pt.(*types.Param)
		if !ok || len(p.Bounds) == 0 {
			continue
		}
		concrete, ok := subT[name]
		if !ok || bad(concrete) {
			continue
		}
		c.info.Specs.satisfies(c, concrete, p.Bounds, at)
	}
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
	case *types.Ref:
		if a, ok := actual.(*types.Ref); ok {
			unify(d.Elem, a.Elem, subT, subV)
		}
	case *types.Tuple:
		if a, ok := actual.(*types.Tuple); ok && len(d.Elems) == len(a.Elems) {
			for i := range d.Elems {
				unify(d.Elems[i], a.Elems[i], subT, subV)
			}
		}
	case *types.Struct:
		if a, ok := actual.(*types.Struct); ok && a.Def == d.Def {
			for i := range d.Args {
				if i < len(a.Args) {
					unify(d.Args[i], a.Args[i], subT, subV)
				}
			}
		}
	case *types.Enum:
		if a, ok := actual.(*types.Enum); ok && a.Def == d.Def {
			for i := range d.Args {
				if i < len(a.Args) {
					unify(d.Args[i], a.Args[i], subT, subV)
				}
			}
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
	case *types.Ref:
		return &types.Ref{Elem: substitute(x.Elem, subT, subV)}
	case *types.Chan:
		return &types.Chan{Elem: substitute(x.Elem, subT, subV), Dir: x.Dir}
	case *types.Either:
		return &types.Either{Left: substitute(x.Left, subT, subV), Right: substitute(x.Right, subT, subV)}
	case *types.Opt:
		return &types.Opt{Elem: substitute(x.Elem, subT, subV)}
	case *types.Tuple:
		return &types.Tuple{Elems: substituteAll(x.Elems, subT, subV)}
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
	return substitute(member, paramMap(params, args), nil)
}

// paramMap zips a nominal type's declared parameters against its use-site type
// arguments, mapping each parameter name to the argument at the same index (any
// parameter past the arguments is left unmapped). Shared by substituteArgs and
// nominalArgs.
func paramMap(params []*types.Param, args []Type) map[string]Type {
	m := map[string]Type{}
	for i, p := range params {
		if i < len(args) {
			m[p.Name] = args[i]
		}
	}
	return m
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
	if id, ok := n.X.(*ast.Ident); ok {
		if sym := c.module.lookup(id.Name); sym != nil && sym.Kind == SymNamespace {
			return c.namespaceMember(n, sym, id.Name)
		}
	}
	xt := c.synth(n.X)
	if st, ok := xt.(*types.Struct); ok && st.Def.Struct != nil {
		if f := findField(st.Def, n.Name); f != nil {
			return substituteArgs(st.Def.Params, st.Args, f.Type)
		}
		c.errorf(n.Span(), "type %s has no field %q", st.Def.Name, n.Name)
		return Invalid
	}
	// `ch.recv` / `ch.send` value narrowing is not part of the language: a channel is
	// narrowed to a receive-only or send-only view by a type annotation, not by a
	// `.recv`/`.send` field. Reject it cleanly rather than silently type it as void and
	// emit a bad field access. The send/receive operations stay Go-style (`ch <- v`,
	// `<-ch`) and are unaffected.
	if _, ok := xt.(*types.Chan); ok && (n.Name == "recv" || n.Name == "send") {
		c.errorf(n.Span(), "a channel is narrowed by a type annotation "+
			"(`<-chan[T]` for receive-only, `chan[T]<-` for send-only), not by `.%s`", n.Name)
		return Invalid
	}
	return types.Unknown
}

// methodCall types a call to an inherent (or spec) method on a concrete nominal
// receiver 'recv.method(args)'. It consults the receiver type's one method
// namespace (GRAMMAR group 7), substitutes the receiver's type arguments through the
// method's signature, checks the arguments, and yields the substituted return type.
// It reports false when the receiver is not a nominal type or has no such method, so
// the ordinary field-access path (and its "has no field" diagnostic) still applies.
// mono's walkMethodCall and the emitter's methodCall already lower a resolved
// inherent method, so this sema type is the only piece a concrete '.method()' needed.
func (c *checker) methodCall(n *ast.Call, fld *ast.Field) (Type, bool) {
	if c.info.Specs == nil {
		return nil, false
	}
	rt := c.synth(fld.X)
	head := nominalHead(rt)
	if head == nil {
		return nil, false
	}
	ms := c.info.Specs.Methods[head]
	if ms == nil {
		return nil, false
	}
	ref, ok := ms.Methods[fld.Name]
	if !ok {
		return nil, false
	}
	m := ref.Method
	subT := nominalArgs(rt)
	var pnames []string
	if fn, ok := m.Decl.(*ast.FuncDecl); ok {
		for _, p := range fn.Params {
			pnames = append(pnames, p.Name)
		}
	}
	ptypes := make([]Type, len(m.Sig.Params))
	for i, p := range m.Sig.Params {
		ptypes[i] = substitute(p.Type, subT, nil)
	}
	c.bindCallArgs(head.Name+"."+fld.Name, n, pnames, ptypes, make([]bool, len(ptypes)))
	if m.Sig.Ret == nil {
		return Nil, true
	}
	return substitute(m.Sig.Ret, subT, nil), true
}

// nominalArgs maps a nominal receiver type's declared parameters to its use-site
// type arguments, so a method's abstract 'T' substitutes to the concrete argument
// (the sema counterpart of mono's nominalSubT).
func nominalArgs(recv Type) map[string]Type {
	switch x := recv.(type) {
	case *types.Struct:
		return paramMap(x.Def.Params, x.Args)
	case *types.Enum:
		return paramMap(x.Def.Params, x.Args)
	}
	return map[string]Type{}
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
		// a single index `xs[i]` must be assignable to int (mirroring the map key path
		// below and `.get`); without this a bad index type (`xs[true]`, `xs["x"]`)
		// reaches the backend as a silent coercion / bad C. A slice-shaped bracket is
		// left to synth.
		if len(n.Elems) == 1 {
			c.checkElem(n.Elems[0], Int, "list index")
		} else {
			c.synthIndices(n.Elems)
		}
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
	c.enterClosure()
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
	c.resolveClosure(fe, want, c.leaveClosure())
	return want
}

// synthFn synthesizes a closure's type when there is no expected type; every
// parameter must then carry an explicit type annotation.
func (c *checker) synthFn(fe *ast.FnExpr) Type {
	fn := &types.Fn{}
	c.enterClosure()
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
	c.resolveClosure(fe, fn, c.leaveClosure())
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
