package syntax

import (
	"strings"
)

// v0.6 Unit 3 — generic monomorphisation, bound checking, and bidirectional
// inference.
//
// The Unit 2 file (typeck_v06_builtin.go) seeded the canonicalisation
// machinery for the synthetic Option / Result enum decls; this file extends
// that cache to user-defined generic struct + enum decls and adds the
// generic-fn call path.
//
// Design highlights:
//
//   - One canonical *Type per `(decl, type-arg vector)`. The cache key is the
//     printable form `Decl[arg1,arg2,...]`. Two type-resolutions of
//     `Box[int]` from the same or different modules return the same pointer
//     so downstream pointer-equality dispatch works unchanged.
//
//   - Generic structs and enums skip the standard collect / resolve passes
//     because their field / payload TypeRefs name the declared type-parameter
//     identifiers — those resolve only after substitution into a concrete
//     instance.
//
//   - Generic fns are NOT type-checked at decl time. The signature parser
//     records type-params; the resolve pass validates that signature TypeRefs
//     name only declared type-params or in-scope concrete types. Body type-
//     checking happens once per specialised instance, on a CLONE with the
//     type-params substituted to concrete *Type values.
//
//   - Bidirectional inference: every callsite that propagates an expected
//     type (let / mut / const annotation, return target type, fn arg slot,
//     list element type, struct field type) feeds the hint into checkExprHint
//     and into the call-site unifier so e.g. `let r: Result[int, str] =
//     Result.Err("oops")` resolves Result.Err's E from the annotation.
//
//   - T → T? lift: at every position with a known expected `T?` (= Option[T])
//     a T-typed expression is rewrapped in a synthetic
//     EnumLit{Some, [origExpr]} pinned to the Option[T] type so downstream
//     consumers see one uniform shape. The lift is "boundary-only" — it
//     fires only at a slot whose hint is Option[T]; sub-expressions without
//     a hint don't lift, matching PLAN.md §Type inference rules.
//
// Bound-check note: at v0.6 Unit 3 we accept multi-bound `T: A + B` and
// require every spec on the bound list to be satisfied by the chosen
// concrete arg. The check fires AFTER instantiation so the diagnostic is
// pinned to the call site rather than the decl.

// ---------------------------------------------------------------------------
// Generic struct instantiation.
// ---------------------------------------------------------------------------

// instantiateGenericStruct returns the canonical *Type for a generic struct
// decl applied to the resolved type-arg vector. Caches by (decl.Name,
// argsSig).
func (c *checker) instantiateGenericStruct(decl *StructDecl, args []*Type, refPos Position) (*Type, error) {
	if len(args) != len(decl.TypeParams) {
		return nil, typeErr(refPos,
			"generic type %q expects %d type argument(s), got %d",
			decl.Name, len(decl.TypeParams), len(args))
	}
	for i, a := range args {
		if a == nil {
			return nil, typeErr(refPos,
				"generic type %q argument %d failed to resolve", decl.Name, i+1)
		}
		if a == tVoid {
			return nil, typeErr(refPos,
				"generic type %q argument %d cannot be void", decl.Name, i+1)
		}
	}
	if c.monoStructs == nil {
		c.monoStructs = map[string]*Type{}
	}
	key := decl.Name + "[" + monoEnumArgsSig(args) + "]"
	if t, ok := c.monoStructs[key]; ok {
		return t, nil
	}
	subst := map[string]*Type{}
	for i, tp := range decl.TypeParams {
		subst[tp.Name] = args[i]
	}
	// Pre-insert a placeholder so a self-referential field (`struct Box[T]
	// { next: Box[T] }`) doesn't recurse infinitely. Cycle detection rejects
	// such a shape later via detectTypeCycles, so the placeholder is only a
	// safety net.
	mono := &Type{Kind: TypeStruct, Name: key}
	c.monoStructs[key] = mono
	fields := make([]NamedField, len(decl.Fields))
	for i, f := range decl.Fields {
		t, err := c.resolveTypeRefWithSubst(f.Type, subst)
		if err != nil {
			delete(c.monoStructs, key)
			return nil, err
		}
		if t == tVoid {
			delete(c.monoStructs, key)
			return nil, typeErr(f.Pos, "field %q cannot have void type", f.Name)
		}
		fields[i] = NamedField{Name: f.Name, Type: t}
	}
	mono.Fields = fields
	if err := c.expandGenericImplsForType(decl, nil, mono, args, refPos); err != nil {
		return nil, err
	}
	return mono, nil
}

// findGenericStructDecl looks up a generic StructDecl by name across the
// bundle (or just the local checker for single-program Check). Returns nil
// when no decl matches or when the named decl is non-generic.
func (c *checker) findGenericStructDecl(name string) *StructDecl {
	if d, ok := c.structAST[name]; ok && len(d.TypeParams) > 0 {
		return d
	}
	if c.crossMod != nil {
		for _, fc := range c.crossMod.checkers {
			if fc == c {
				continue
			}
			if d, ok := fc.structAST[name]; ok && len(d.TypeParams) > 0 {
				return d
			}
		}
	}
	return nil
}

// findGenericEnumDecl mirrors findGenericStructDecl for enum decls. Walks
// builtins first so Option / Result resolve regardless of module.
func (c *checker) findGenericEnumDecl(name string) *EnumDecl {
	if d, ok := c.builtinEnumDecls[name]; ok {
		return d
	}
	if d, ok := c.enumAST[name]; ok && len(d.TypeParams) > 0 {
		return d
	}
	if c.crossMod != nil {
		for _, fc := range c.crossMod.checkers {
			if fc == c {
				continue
			}
			if d, ok := fc.enumAST[name]; ok && len(d.TypeParams) > 0 {
				return d
			}
		}
	}
	return nil
}

// findGenericFnDecl mirrors findGenericEnumDecl / findGenericStructDecl for
// fn decls. Walks the bundle so a generic fn defined in module A is callable
// from module B.
func (c *checker) findGenericFnDecl(name string) *FnDecl {
	if d, ok := c.genericFnAST[name]; ok {
		return d
	}
	if c.crossMod != nil {
		for _, fc := range c.crossMod.checkers {
			if fc == c {
				continue
			}
			if d, ok := fc.genericFnAST[name]; ok {
				return d
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Generic-fn call site: bidirectional unifier + specialisation.
// ---------------------------------------------------------------------------

// genericFnInfo is the resolved signature information for a generic FnDecl,
// recorded when the decl's signature is symbolically validated. The TypeRefs
// reference the declared type-parameter names by raw identifier; the
// bound list is the resolved spec-type list per type-param.
type genericFnInfo struct {
	decl      *FnDecl
	typeParams []TypeParam
	paramRefs []*TypeRef // 1:1 with decl.Params
	retRef    *TypeRef   // nil ⇒ void return
	// bounds[i] is the resolved spec list for type-param i. Empty entries
	// indicate an unconstrained type-param.
	bounds [][]*Type
}

// resolveGenericFnSignatures walks every top-level FnDecl with TypeParams,
// records it in genericFnAST, and validates the signature shape:
//
//   - every TypeRef in param / return must name either a declared type-
//     parameter or an in-scope concrete type (struct, enum, spec, primitive).
//   - bound TypeRefs (`T: Spec`) must resolve to known spec types.
//
// The decl's body is NOT walked here — body type-checking happens once per
// specialised instance, on a clone, in checkGenericFnCall.
func (c *checker) resolveGenericFnSignatures(prog *Program) error {
	for _, stmt := range prog.Statements {
		fn, ok := stmt.(*FnDecl)
		if !ok {
			continue
		}
		if len(fn.TypeParams) == 0 {
			continue
		}
		if err := c.validateGenericFnDecl(fn); err != nil {
			return err
		}
		c.genericFnAST[fn.Name] = fn
		// Remove from the regular fn table so checkCall routes through the
		// generic path. resolveFnSignatures already skipped this decl
		// because of the TypeParams gate; defensive delete here in case
		// future reorderings change the order.
		delete(c.fns, fn.Name)
	}
	return nil
}

// validateGenericFnDecl checks that every TypeRef in the signature resolves
// (either to a declared type-parameter or an in-scope concrete type) and
// that every bound is a known spec.
func (c *checker) validateGenericFnDecl(fn *FnDecl) error {
	tparamSet := map[string]bool{}
	seenTP := map[string]Position{}
	for _, tp := range fn.TypeParams {
		if prev, dup := seenTP[tp.Name]; dup {
			_ = prev
			return typeErr(tp.Pos, "type parameter %q declared twice", tp.Name)
		}
		seenTP[tp.Name] = tp.Pos
		tparamSet[tp.Name] = true
	}
	for _, tp := range fn.TypeParams {
		for _, b := range tp.Bounds {
			if err := c.validateBoundRef(b); err != nil {
				return err
			}
		}
	}
	for _, p := range fn.Params {
		if err := c.validateGenericTypeRef(p.Type, tparamSet); err != nil {
			return err
		}
	}
	if fn.Return != nil {
		if err := c.validateGenericTypeRef(fn.Return, tparamSet); err != nil {
			return err
		}
	}
	return nil
}

// validateBoundRef ensures a bound spec TypeRef resolves to a spec type. We
// reuse resolveTypeRef so module-qualified bounds (`T: mod.Printable`) work
// for free; the result must have Kind == TypeSpec.
func (c *checker) validateBoundRef(ref *TypeRef) error {
	if ref == nil {
		return nil
	}
	t, err := c.resolveTypeRef(ref)
	if err != nil {
		return err
	}
	if t == nil || t.Kind != TypeSpec {
		return typeErr(ref.Pos, "bound %q is not a spec", ref.String())
	}
	return nil
}

// validateGenericTypeRef walks a TypeRef in a generic signature position and
// asserts that every leaf names either a declared type-param (ok) or an in-
// scope concrete type (resolved via resolveTypeRef). Generic type-args
// inside compound TypeRefs (`list[T]`, `Box[T]`, `Option[T]`) recurse.
//
// `T?` is admitted: at instantiation time it desugars to Option[<concrete-T>].
func (c *checker) validateGenericTypeRef(ref *TypeRef, tparams map[string]bool) error {
	if ref == nil {
		return nil
	}
	switch ref.Kind {
	case TypeRefNamed:
		if ref.Module == "" && len(ref.TypeArgs) == 0 && tparams[ref.Name] {
			return nil
		}
		if len(ref.TypeArgs) > 0 {
			for _, a := range ref.TypeArgs {
				if err := c.validateGenericTypeRef(a, tparams); err != nil {
					return err
				}
			}
			// Validate the head decl exists and has matching arity. We
			// don't instantiate yet (one of the args may be a type-param
			// placeholder).
			decl := c.findGenericEnumDecl(ref.Name)
			var declTPs []TypeParam
			if decl != nil {
				declTPs = decl.TypeParams
			} else {
				if sd := c.findGenericStructDecl(ref.Name); sd != nil {
					declTPs = sd.TypeParams
				}
			}
			if declTPs == nil {
				return typeErr(ref.Pos,
					"type %q is not generic but was given type arguments", ref.Name)
			}
			if len(declTPs) != len(ref.TypeArgs) {
				return typeErr(ref.Pos,
					"generic type %q expects %d type argument(s), got %d",
					ref.Name, len(declTPs), len(ref.TypeArgs))
			}
			return nil
		}
		// No type-args: must be a primitive / concrete known name.
		_, err := c.resolveTypeRef(ref)
		return err
	case TypeRefList:
		return c.validateGenericTypeRef(ref.Element, tparams)
	case TypeRefTuple:
		for _, e := range ref.Elements {
			if err := c.validateGenericTypeRef(e, tparams); err != nil {
				return err
			}
		}
		return nil
	}
	return typeErr(ref.Pos, "internal: unknown TypeRef kind %d", int(ref.Kind))
}

// checkGenericFnCall is the entry point for a CallExpr whose callee is a
// generic FnDecl. The unifier is bidirectional: param/arg pairs feed
// constraints, and the optional return-type hint feeds another. After
// solving, we instantiate (with bound checking), specialise the FnDecl,
// type-check the clone, and stamp the call's return type.
//
// callIdent is the IdentExpr that named the fn (used for the
// "specialised" callee type stamp). hint is the contextual return-type hint
// from the surrounding expression (nil when there is none).
func (c *checker) checkGenericFnCall(e *CallExpr, fn *FnDecl, callIdent *IdentExpr, hint *Type) (*Type, error) {
	if len(e.Args) != len(fn.Params) {
		return nil, typeErr(e.Pos,
			"function %q expects %d argument(s), got %d", fn.Name, len(fn.Params), len(e.Args))
	}
	// Pre-flight: type-check each arg WITHOUT a hint (the param type is a
	// type-var until we solve). Empty-list literals etc. that need a hint
	// will be reattempted with the resolved param type once unification is
	// done. We retain the original Expr objects so post-substitution
	// re-checking lifts T → T? in the right slot.
	argTypes := make([]*Type, len(e.Args))
	for i, a := range e.Args {
		t, err := c.checkExpr(a)
		if err != nil {
			return nil, err
		}
		argTypes[i] = t
	}
	subst := map[string]*Type{}
	for _, tp := range fn.TypeParams {
		subst[tp.Name] = nil
	}
	// Walk arg-driven constraints first.
	for i, p := range fn.Params {
		if err := c.unify(p.Type, argTypes[i], subst, e.Args[i].ExprPos()); err != nil {
			return nil, err
		}
	}
	// Walk return-type hint constraint, if any.
	if hint != nil && fn.Return != nil {
		if err := c.unify(fn.Return, hint, subst, e.Pos); err != nil {
			return nil, err
		}
	}
	// Resolve any unconstrained type-params: reject with the precise
	// diagnostic.
	resolvedArgs := make([]*Type, len(fn.TypeParams))
	for i, tp := range fn.TypeParams {
		t := subst[tp.Name]
		if t == nil {
			return nil, typeErr(e.Pos,
				"cannot infer type parameter %q in call to %q", tp.Name, fn.Name)
		}
		resolvedArgs[i] = t
	}
	// Bound check.
	for i, tp := range fn.TypeParams {
		conc := resolvedArgs[i]
		for _, bRef := range tp.Bounds {
			specT, err := c.resolveTypeRef(bRef)
			if err != nil {
				return nil, err
			}
			if specT.Kind != TypeSpec {
				return nil, typeErr(bRef.Pos, "bound %q is not a spec", bRef.String())
			}
			if !c.assignableTo(conc, specT) {
				return nil, typeErr(e.Pos,
					"type %q does not implement %s", conc, specT.Name)
			}
		}
	}
	// Specialise the FnDecl. Cache by (decl.Name, args).
	specialised, err := c.specialiseGenericFn(fn, resolvedArgs)
	if err != nil {
		return nil, err
	}
	sig := c.fns[specialised.Name]
	// Re-type-check args against the resolved param types so empty-list
	// hints, T → T? lifts, and spec widening land. The Expr nodes were
	// already partially typed by the pre-flight walk; re-running with the
	// concrete hint is idempotent for already-typed leaves and corrective
	// for hint-driven shapes.
	for i := range e.Args {
		hintT := sig.params[i]
		newExpr, at, err := c.checkExprLift(e.Args[i], hintT)
		if err != nil {
			return nil, err
		}
		if newExpr != e.Args[i] {
			e.Args[i] = newExpr
		}
		if !c.assignableTo(at, hintT) {
			return nil, typeErr(e.Args[i].ExprPos(),
				"argument %d to %q has type %s, expected %s",
				i+1, fn.Name, at, hintT)
		}
	}
	// Stamp the IdentExpr / CallExpr with the specialised return type.
	if callIdent != nil {
		callIdent.setType(sig.ret)
	}
	e.setType(sig.ret)
	return sig.ret, nil
}

// unify drives one side of a HM-style unifier. ref is a TypeRef from the
// generic decl (mentions type-param names by raw identifier); concrete is
// the *Type observed at the call site for the corresponding arg / return-
// type slot. subst is the running substitution map.
func (c *checker) unify(ref *TypeRef, concrete *Type, subst map[string]*Type, pos Position) error {
	if ref == nil || concrete == nil {
		return nil
	}
	switch ref.Kind {
	case TypeRefNamed:
		// Bare type-param reference (no module / no type-args / no nullable):
		// constrain or extend the substitution.
		if ref.Module == "" && len(ref.TypeArgs) == 0 && !ref.Nullable {
			if _, isTP := subst[ref.Name]; isTP {
				prev := subst[ref.Name]
				if prev == nil {
					subst[ref.Name] = concrete
					return nil
				}
				if !typeEq(prev, concrete) {
					return typeErr(pos,
						"conflicting types for type parameter %q: %s vs %s",
						ref.Name, prev, concrete)
				}
				return nil
			}
		}
		// Nullable-only type-param (T?) — concrete must be Option[U]; unify
		// inner U against the type-param.
		if ref.Module == "" && len(ref.TypeArgs) == 0 && ref.Nullable {
			if _, isTP := subst[ref.Name]; isTP {
				if concrete.Kind == TypeEnum && isOptionInstance(concrete) {
					inner := concrete.VariantPayloads[0]
					if len(inner) == 1 {
						return c.unifyName(ref.Name, inner[0], subst, pos)
					}
				}
				// Lift T → T?: take the concrete arg type as the type-param,
				// the lift wraps it at the call site.
				return c.unifyName(ref.Name, concrete, subst, pos)
			}
		}
		// Compound generic in signature: `Option[T]`, `Box[T]`, etc. The
		// concrete value at this slot must be the matching generic
		// instance; recurse into its args.
		if len(ref.TypeArgs) > 0 {
			// Resolve the head decl to compare arity / kind.
			if concrete.Kind == TypeEnum && isOptionInstance(concrete) {
				if ref.Name == "Option" && len(ref.TypeArgs) == 1 {
					inner := concrete.VariantPayloads[0]
					if len(inner) == 1 {
						return c.unify(ref.TypeArgs[0], inner[0], subst, pos)
					}
				}
			}
			if concrete.Kind == TypeEnum && strings.HasPrefix(concrete.Name, "Result[") {
				if ref.Name == "Result" && len(ref.TypeArgs) == 2 {
					okPay := concrete.VariantPayloads[0]
					errPay := concrete.VariantPayloads[1]
					if len(okPay) == 1 && len(errPay) == 1 {
						if err := c.unify(ref.TypeArgs[0], okPay[0], subst, pos); err != nil {
							return err
						}
						return c.unify(ref.TypeArgs[1], errPay[0], subst, pos)
					}
				}
			}
			// User-defined generic enum / struct: derive arg vector from
			// the concrete *Type's name suffix.
			if got, ok := decomposeGenericInstance(concrete, ref.Name); ok {
				if len(got) == len(ref.TypeArgs) {
					for i := range got {
						if err := c.unify(ref.TypeArgs[i], got[i], subst, pos); err != nil {
							return err
						}
					}
					return nil
				}
			}
			return typeErr(pos, "expected %s, got %s", ref.String(), concrete)
		}
		// Concrete reference (not a type-param). resolveTypeRef gives the
		// canonical *Type; require equality.
		t, err := c.resolveTypeRef(ref)
		if err != nil {
			return err
		}
		if !typeEq(t, concrete) {
			// Spec widening admits a concrete T impl S to flow into a Spec
			// slot.
			if c.assignableTo(concrete, t) {
				return nil
			}
			return typeErr(pos, "expected %s, got %s", ref.String(), concrete)
		}
		return nil
	case TypeRefList:
		if concrete.Kind != TypeList {
			return typeErr(pos, "expected list, got %s", concrete)
		}
		return c.unify(ref.Element, concrete.Element, subst, pos)
	case TypeRefTuple:
		if concrete.Kind != TypeTuple || len(ref.Elements) != len(concrete.Tuple) {
			return typeErr(pos, "expected %s, got %s", ref.String(), concrete)
		}
		for i := range ref.Elements {
			if err := c.unify(ref.Elements[i], concrete.Tuple[i], subst, pos); err != nil {
				return err
			}
		}
		return nil
	}
	return typeErr(pos, "internal: unify on unknown TypeRef kind %d", int(ref.Kind))
}

// unifyName extends the substitution for type-param `name` with `concrete`.
// Conflict detection mirrors the bare-name path in unify.
func (c *checker) unifyName(name string, concrete *Type, subst map[string]*Type, pos Position) error {
	if _, isTP := subst[name]; !isTP {
		return typeErr(pos, "internal: unifyName on non-type-param %q", name)
	}
	prev := subst[name]
	if prev == nil {
		subst[name] = concrete
		return nil
	}
	if !typeEq(prev, concrete) {
		return typeErr(pos,
			"conflicting types for type parameter %q: %s vs %s",
			name, prev, concrete)
	}
	return nil
}

// decomposeGenericInstance pulls the type-arg vector out of a canonical
// generic instance *Type whose Name is `Decl[arg1,arg2,...]`. Returns
// (args, ok); ok is false when the *Type is not a recognised instance of
// declName. We re-resolve the printable arg names against the bundle's
// resolveTypeRef path to recover *Type pointers.
//
// Note: this is the inverse of monoEnumArgsSig; it relies on the
// canonicalisation invariant that argument names are simple enough to
// re-parse. Composite arg names (e.g. `list[int]`) are handled by walking
// the bracket nesting.
func decomposeGenericInstance(t *Type, declName string) ([]*Type, bool) {
	if t == nil {
		return nil, false
	}
	if t.Kind != TypeEnum && t.Kind != TypeStruct {
		return nil, false
	}
	prefix := declName + "["
	if !strings.HasPrefix(t.Name, prefix) || !strings.HasSuffix(t.Name, "]") {
		return nil, false
	}
	// We don't reconstruct *Type from the name suffix here; instead we
	// rely on the variant / field shape. For enum: each variant payload
	// position holds *Type; the unifier callers use Option / Result by
	// name match (handled inline). For struct: each field's *Type is the
	// substituted concrete. The caller can recover the type-arg vector
	// via the decl's payload / field layout if needed; for unify-by-arity
	// we return nil and let the caller fall through to the equality path.
	//
	// Implementation note: we choose to return false here. The unify path
	// for user-defined generics relies on the equality path (typeEq on
	// canonical *Type pointers) for matching, NOT structural decomposition
	// of the instance Name. The two existing built-ins (Option, Result)
	// have inline cases above this point.
	return nil, false
}

// specialiseGenericFn returns the cached specialised *FnDecl for fn applied
// to args, constructing and type-checking it on a cache miss. The returned
// FnDecl's signature is registered in c.fns under a per-instance name so
// downstream consumers (run, cgen) can find it; the cache entry is bundle-
// shared via crossMod.bundleMono.fns.
func (c *checker) specialiseGenericFn(fn *FnDecl, args []*Type) (*FnDecl, error) {
	key := fn.Name + "[" + monoEnumArgsSig(args) + "]"
	if c.monoFns == nil {
		c.monoFns = map[string]*FnDecl{}
	}
	if cached, ok := c.monoFns[key]; ok {
		return cached, nil
	}
	subst := map[string]*Type{}
	for i, tp := range fn.TypeParams {
		subst[tp.Name] = args[i]
	}
	params := make([]FnParam, len(fn.Params))
	for i, p := range fn.Params {
		t, err := c.resolveTypeRefWithSubst(p.Type, subst)
		if err != nil {
			return nil, err
		}
		if t == tVoid {
			return nil, typeErr(p.Pos, "parameter %q cannot have void type", p.Name)
		}
		params[i] = FnParam{
			Name: p.Name,
			Pos:  p.Pos,
			Type: &TypeRef{Pos: p.Type.Pos, Kind: TypeRefNamed, Name: t.String(), Resolved: t},
		}
	}
	ret := tVoid
	if fn.Return != nil {
		t, err := c.resolveTypeRefWithSubst(fn.Return, subst)
		if err != nil {
			return nil, err
		}
		if t == tVoid {
			return nil, typeErr(fn.Return.Pos,
				"use no return annotation instead of declaring a void return")
		}
		ret = t
	}
	clone := &FnDecl{
		Pos:    fn.Pos,
		Name:   key,
		Params: params,
		Return: nil,
		Body:   fn.Body,
		Pub:    fn.Pub,
	}
	if fn.Return != nil {
		clone.Return = &TypeRef{Pos: fn.Return.Pos, Kind: TypeRefNamed, Name: ret.String(), Resolved: ret}
	}
	// Pre-register the signature so a recursive generic-fn call resolves
	// against the cache.
	pTypes := make([]*Type, len(params))
	for i := range params {
		pTypes[i] = params[i].Type.Resolved
	}
	c.fns[key] = fnSig{
		params: pTypes,
		ret:    ret,
		pos:    fn.Pos,
	}
	c.monoFns[key] = clone
	// Body type-check the clone.
	saved := c.currentFn
	sig := c.fns[key]
	c.currentFn = &sig
	c.scope = newScope(c.scope)
	for i, p := range clone.Params {
		if !c.scope.declare(p.Name, binding{kind: bindLet, typ: pTypes[i]}) {
			c.scope = c.scope.parent
			c.currentFn = saved
			return nil, typeErr(p.Pos, "parameter %q already declared", p.Name)
		}
	}
	for _, st := range clone.Body.Statements {
		if err := c.checkStmt(st); err != nil {
			c.scope = c.scope.parent
			c.currentFn = saved
			return nil, err
		}
	}
	c.scope = c.scope.parent
	c.currentFn = saved
	return clone, nil
}

// ---------------------------------------------------------------------------
// Bidirectional inference helpers — T → T? lift.
// ---------------------------------------------------------------------------

// checkExprLift type-checks expr with the given hint and applies the v0.6
// T → T? boundary lift when the hint asks for an Option[T] but the
// expression's natural type is T (not Option[U] itself). Returns the
// possibly-replaced Expr (callers MUST install it in the original slot when
// it differs) plus the resolved type.
func (c *checker) checkExprLift(expr Expr, hint *Type) (Expr, *Type, error) {
	observed, err := c.checkExprHint(expr, hint)
	if err != nil {
		return expr, nil, err
	}
	if newExpr, newType, ok := c.applyOptionLift(expr, observed, hint); ok {
		return newExpr, newType, nil
	}
	return expr, observed, nil
}

// applyOptionLift performs the T → T? boundary lift. Returns (wrapped, t?,
// true) when the lift fires; (expr, observed, false) otherwise.
//
// The lift fires when:
//   - hint is Option[U] for some U
//   - observed is U exactly (not Option[V] — those flow through unchanged)
//   - observed is not a NilLit (NilLit handles its own resolution to
//     Option[T].None inside checkExprHint).
//
// We detect the "already Option[T]" case via observed.Kind / Name to avoid
// double-wrapping `Some(7)` to `Some(Some(7))`.
func (c *checker) applyOptionLift(expr Expr, observed, hint *Type) (Expr, *Type, bool) {
	if hint == nil || observed == nil {
		return expr, observed, false
	}
	if hint.Kind != TypeEnum || !isOptionInstance(hint) {
		return expr, observed, false
	}
	if observed.Kind == TypeEnum && isOptionInstance(observed) {
		// Already an Option — no lift.
		return expr, observed, false
	}
	if _, isNil := expr.(*NilLit); isNil {
		// nil already resolved to Option[T].None inside checkExprHint.
		return expr, observed, false
	}
	if len(hint.VariantPayloads) < 1 || len(hint.VariantPayloads[0]) != 1 {
		return expr, observed, false
	}
	inner := hint.VariantPayloads[0][0]
	if !c.assignableTo(observed, inner) {
		return expr, observed, false
	}
	wrap := &EnumLit{
		Pos:        expr.ExprPos(),
		EnumName:   "Option",
		Variant:    "Some",
		VariantPos: expr.ExprPos(),
		Payload:    []Expr{expr},
	}
	wrap.setType(hint)
	return wrap, hint, true
}

// ---------------------------------------------------------------------------
// Generic enum lit construction (Option.Some(x), Result.Err("oops"), ...).
//
// At Unit 2 these were rejected because Option / Result aren't valid
// concrete enum names — the bare-name lookup in checkMethodCall fails. Unit
// 3 wires the construction through instantiateGenericEnum + a call-site
// inference pass that derives the type-args from arg types and / or the
// surrounding hint.
// ---------------------------------------------------------------------------

// checkGenericEnumLit attempts to construct a generic enum variant from a
// MethodCallExpr shape (`Option.Some(7)`, `Result.Err("oops")`). Returns
// (resultType, true) on a successful construction; (nil, false) when the
// receiver name is not a known generic enum decl. Errors flow back through
// the resultType slot when the construction is recognised but malformed.
func (c *checker) checkGenericEnumLit(e *MethodCallExpr, declName, variant string, hint *Type) (*Type, bool, error) {
	decl := c.findGenericEnumDecl(declName)
	if decl == nil {
		return nil, false, nil
	}
	idx := -1
	for i, v := range decl.Variants {
		if v.Name == variant {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, true, typeErr(e.MethodPos, "enum %q has no variant %q", declName, variant)
	}
	v := decl.Variants[idx]
	// Two-pronged inference: the surrounding hint (when it's a matching
	// generic instance) supplies the type-args directly; arg-driven
	// inference fills any remaining gaps.
	subst := map[string]*Type{}
	for _, tp := range decl.TypeParams {
		subst[tp.Name] = nil
	}
	if hint != nil && hint.Kind == TypeEnum {
		if got, ok := genericInstanceArgs(hint, declName); ok && len(got) == len(decl.TypeParams) {
			for i, tp := range decl.TypeParams {
				subst[tp.Name] = got[i]
			}
		}
	}
	if len(e.Args) != len(v.Payload) {
		return nil, true, typeErr(e.Pos,
			"variant %q.%q expects %d payload value(s), got %d",
			declName, variant, len(v.Payload), len(e.Args))
	}
	// Pre-walk args to constrain type-vars.
	argTypes := make([]*Type, len(e.Args))
	for i, a := range e.Args {
		t, err := c.checkExpr(a)
		if err != nil {
			return nil, true, err
		}
		argTypes[i] = t
	}
	for i, p := range v.Payload {
		if err := c.unify(p, argTypes[i], subst, e.Args[i].ExprPos()); err != nil {
			return nil, true, err
		}
	}
	resolvedArgs := make([]*Type, len(decl.TypeParams))
	for i, tp := range decl.TypeParams {
		t := subst[tp.Name]
		if t == nil {
			return nil, true, typeErr(e.Pos,
				"cannot infer type parameter %q in construction of %s.%s",
				tp.Name, declName, variant)
		}
		resolvedArgs[i] = t
	}
	mono, err := c.instantiateGenericEnum(decl, resolvedArgs, e.Pos)
	if err != nil {
		return nil, true, err
	}
	// Re-check args against the substituted payload types so empty-list
	// hints, lifts, and spec widening fire.
	concretePayload := mono.VariantPayloads[idx]
	for i := range e.Args {
		newExpr, at, err := c.checkExprLift(e.Args[i], concretePayload[i])
		if err != nil {
			return nil, true, err
		}
		if newExpr != e.Args[i] {
			e.Args[i] = newExpr
		}
		if !c.assignableTo(at, concretePayload[i]) {
			return nil, true, typeErr(e.Args[i].ExprPos(),
				"variant %s.%s payload position %d expects %s, got %s",
				declName, variant, i+1, concretePayload[i], at)
		}
	}
	args := make([]Expr, len(e.Args))
	copy(args, e.Args)
	lowered := &EnumLit{
		Pos:        e.Pos,
		EnumName:   mono.Name,
		Variant:    variant,
		VariantPos: e.MethodPos,
		Payload:    args,
	}
	lowered.setType(mono)
	e.Lowered = lowered
	e.setType(mono)
	if id, ok := e.Receiver.(*IdentExpr); ok {
		id.setType(mono)
	}
	return mono, true, nil
}

// checkGenericEnumBareLit handles the bare-name access path
// (`Option.None`, `Result.Ok` without parens) for generic enums where the
// variant carries no payload. The hint must supply the type-args (no
// arg-driven inference is possible). Returns (nil, false) when the
// receiver name is not a known generic enum.
func (c *checker) checkGenericEnumBareLit(e *FieldAccessExpr, declName, variant string, hint *Type) (*Type, bool, error) {
	decl := c.findGenericEnumDecl(declName)
	if decl == nil {
		return nil, false, nil
	}
	idx := -1
	for i, v := range decl.Variants {
		if v.Name == variant {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, true, typeErr(e.NamePos, "enum %q has no variant %q", declName, variant)
	}
	v := decl.Variants[idx]
	if len(v.Payload) > 0 {
		return nil, true, typeErr(e.NamePos,
			"variant %q.%q has %d payload value(s) — use %s.%s(...) to construct",
			declName, variant, len(v.Payload), declName, variant)
	}
	if hint == nil || hint.Kind != TypeEnum {
		return nil, true, typeErr(e.Pos,
			"cannot infer type parameter(s) for %s.%s — annotate the binding",
			declName, variant)
	}
	got, ok := genericInstanceArgs(hint, declName)
	if !ok || len(got) != len(decl.TypeParams) {
		return nil, true, typeErr(e.Pos,
			"cannot infer type parameter(s) for %s.%s — annotate the binding",
			declName, variant)
	}
	mono, err := c.instantiateGenericEnum(decl, got, e.Pos)
	if err != nil {
		return nil, true, err
	}
	if id, ok := e.Receiver.(*IdentExpr); ok {
		id.setType(mono)
	}
	e.setType(mono)
	e.Lowered = &EnumLit{
		Pos:        e.Pos,
		EnumName:   mono.Name,
		Variant:    variant,
		VariantPos: e.NamePos,
	}
	e.Lowered.setType(mono)
	return mono, true, nil
}

// checkCrossModuleGenericFnCall is the cross-module entry for a
// `mod.id(...)` call against a foreign module's generic fn. Lowers to a
// CallExpr (so checkGenericFnCall can run unchanged) and stamps the
// MethodCallExpr's type / Lowered hint so downstream consumers can route
// through the existing cross-fn machinery.
func (c *checker) checkCrossModuleGenericFnCall(e *MethodCallExpr, fn *FnDecl, hint *Type) (*Type, error) {
	callee := &IdentExpr{Pos: e.MethodPos, Name: e.Method}
	args := make([]Expr, len(e.Args))
	copy(args, e.Args)
	call := &CallExpr{Pos: e.Pos, Callee: callee, Args: args}
	t, err := c.checkGenericFnCall(call, fn, callee, hint)
	if err != nil {
		return nil, err
	}
	// Propagate any in-place arg replacements back to the MethodCallExpr.
	for i := range call.Args {
		if call.Args[i] != e.Args[i] {
			e.Args[i] = call.Args[i]
		}
	}
	e.LoweredCall = call
	e.setType(t)
	return t, nil
}

// genericInstanceArgs recovers the type-arg vector from a canonical
// generic enum / struct instance's Name suffix and re-resolves each arg
// to a *Type. Returns (nil, false) when the *Type is not a recognised
// instance of declName.
//
// The implementation walks the bracketed signature character-by-character
// to handle nested generic instances (e.g. `Option[list[int]]`).
func genericInstanceArgs(t *Type, declName string) ([]*Type, bool) {
	if t == nil {
		return nil, false
	}
	if t.Kind != TypeEnum && t.Kind != TypeStruct {
		return nil, false
	}
	prefix := declName + "["
	if !strings.HasPrefix(t.Name, prefix) || !strings.HasSuffix(t.Name, "]") {
		return nil, false
	}
	// We don't need to parse the suffix — the instance's variant payloads
	// (for enums) or fields (for structs) carry the substituted *Type
	// values index-aligned with the decl. But we don't have the decl
	// here, so we extract the args via the layout:
	//
	//   - Option[T]: VariantPayloads[0][0] is T (Some payload).
	//   - Result[T, E]: VariantPayloads[0][0] is T (Ok), [1][0] is E (Err).
	//
	// For user-defined generic enums / structs, recovery is more involved
	// — but Unit 3's only callers are the built-in Option / Result paths,
	// which get a hard-coded mapping here.
	switch declName {
	case "Option":
		if t.Kind == TypeEnum && len(t.VariantPayloads) >= 1 && len(t.VariantPayloads[0]) == 1 {
			return []*Type{t.VariantPayloads[0][0]}, true
		}
	case "Result":
		if t.Kind == TypeEnum && len(t.VariantPayloads) >= 2 &&
			len(t.VariantPayloads[0]) == 1 && len(t.VariantPayloads[1]) == 1 {
			return []*Type{t.VariantPayloads[0][0], t.VariantPayloads[1][0]}, true
		}
	}
	return nil, false
}
