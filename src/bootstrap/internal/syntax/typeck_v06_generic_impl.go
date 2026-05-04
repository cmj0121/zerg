package syntax

// v0.6 Unit 3.5 — generic impl blocks.
//
// `impl[T: Bound] LocalType[T] for SomeSpec { ... }` (and its inherent variant
// `impl[T] LocalType[T] { ... }`) declares one impl block whose methods become
// available on every receiver-type instantiation. Each instantiation produces
// one concrete impl entry on the corresponding mono *Type, with the impl-
// level type-parameter substitution applied to method param / return types.
//
// Lifecycle:
//
//   1. resolveImplsCross collects generic ImplDecls into c.genericImpls
//      (bundle-shared via crossMod.bundleMono.genericImpls). The orphan rule
//      is applied on the BASE decls — at least one of (receiver-decl,
//      spec-decl) must be local.
//   2. instantiateGenericStruct / instantiateGenericEnum, after creating a
//      fresh mono *Type, call expandGenericImplsForType which walks every
//      generic impl in the bundle and clones a concrete impl for each
//      decl-match. The clone is registered in the owning module's c.impls /
//      c.implsByType / c.methodVisible tables.
//   3. Cloned method bodies are walked under c.activeSubst so bare `T`
//      references inside annotations resolve to the chosen concrete arg.
//
// Collision policy at v0.6:
//
//   - Two generic impls for the same (receiver-decl, spec) collide
//     unconditionally (no specialisation hierarchy).
//   - A generic impl + a concrete impl for the same (mono receiver, spec)
//     collide via the existing concrete-impl `(typeName, specName)` key.
//   - The first collision diagnostic anchors on the impl that was registered
//     SECOND.
//
// This is a deferred-permissive choice: a future v0.7+ may admit a
// specialisation hierarchy where `impl Box[int] for P` overrides
// `impl[T] Box[T] for P`, but v0.6 keeps the simpler unconditional rule
// so the collision check is a single equality on (recvDecl, specOwner,
// specName). The unit tests pin the v0.6 surface so any future loosening
// is force-checked against this baseline.

// genericImpl is the deferred record for a generic ImplDecl. The decl's
// receiver-type and spec are pre-validated (orphan rule, base-decl arity) so
// per-instance expansion only has to bind type-params and clone methods.
type genericImpl struct {
	ast *ImplDecl
	// owner is the checker of the module that DECLARED this impl. Cloned
	// concrete impls are registered against owner so cross-module dispatch
	// finds them via the existing methodVisible walk.
	owner *checker
	// recvDeclStruct / recvDeclEnum is the resolved generic decl backing the
	// receiver-type name. Exactly one of the two is non-nil.
	recvDeclStruct *StructDecl
	recvDeclEnum   *EnumDecl
	// recvOwner / specOwner mirror Impl.recvOwner / specOwner — the
	// modules that DEFINED the receiver decl / spec. Used by the orphan
	// rule on the base decl.
	recvOwner ModuleView
	specOwner ModuleView
	// spec is the resolved *Spec for SpecName, or nil for inherent impls.
	spec *Spec
}

// recvDeclName returns the user-visible base-decl name (`Box`, `MyList`,
// ...) — used for diagnostics and for matching against monomorphised
// instances.
func (g *genericImpl) recvDeclName() string {
	if g.recvDeclStruct != nil {
		return g.recvDeclStruct.Name
	}
	if g.recvDeclEnum != nil {
		return g.recvDeclEnum.Name
	}
	return ""
}

// resolveGenericImplDecl is invoked from resolveImplsCross when an ImplDecl
// carries a non-empty TypeParams list. It validates the impl's structure
// (orphan rule, spec arity, type-arg count vs declared type-params) and
// records a deferred genericImpl entry. The actual concrete-impl registration
// happens at each per-receiver-type monomorphisation.
func (c *checker) resolveGenericImplDecl(id *ImplDecl) error {
	tparamSet := map[string]Position{}
	for _, tp := range id.TypeParams {
		if prev, dup := tparamSet[tp.Name]; dup {
			_ = prev
			return typeErr(tp.Pos, "type parameter %q declared twice", tp.Name)
		}
		tparamSet[tp.Name] = tp.Pos
		for _, b := range tp.Bounds {
			if err := c.validateBoundRef(b); err != nil {
				return err
			}
		}
	}
	// Resolve the receiver to a generic decl. The decl may live in this
	// module or in a foreign module (via id.TypeModule).
	recvStruct, recvEnum, recvOwner, err := c.resolveGenericImplReceiver(id)
	if err != nil {
		return err
	}
	var recvTPs []TypeParam
	if recvStruct != nil {
		recvTPs = recvStruct.TypeParams
	} else {
		recvTPs = recvEnum.TypeParams
	}
	if len(id.TypeArgs) != len(recvTPs) {
		return typeErr(id.Pos,
			"generic type %q expects %d type argument(s), got %d",
			id.Type, len(recvTPs), len(id.TypeArgs))
	}
	// Each TypeArg slot must mention only declared impl-level type-params
	// or in-scope concrete types. We reuse validateGenericTypeRef for the
	// recursive walk.
	for _, arg := range id.TypeArgs {
		if err := c.validateGenericTypeRef(arg, boolSetFromPos(tparamSet)); err != nil {
			return err
		}
	}
	// Spec lookup (if any).
	var spec *Spec
	var specOwner ModuleView
	if id.Spec != "" {
		s, owner, err := c.resolveImplSpec(id)
		if err != nil {
			return err
		}
		spec = s
		specOwner = owner
	}
	// Orphan rule on the BASE decls. In a Bundle, at least one of
	// (recv-decl-owner, spec-decl-owner) must be self.
	if c.crossMod != nil {
		selfMod := c.crossMod.self
		ownsRecv := recvOwner == selfMod
		if id.Spec == "" {
			if !ownsRecv {
				return typeErr(id.Pos,
					"cross-module orphan impl: must define %q in this module", id.Type)
			}
		} else {
			ownsSpec := specOwner == selfMod
			if !ownsRecv && !ownsSpec {
				return typeErr(id.Pos,
					"cross-module orphan impl: must define %q or %q in this module", id.Type, id.Spec)
			}
		}
	}
	// Generic-impl collision: two generic impls for the same (decl, spec)
	// reject. The collision check fires here so the diagnostic anchors on
	// the second impl's position. We index on the resolved decl pointer
	// (cross-module-stable) and the spec name.
	gi := &genericImpl{
		ast:            id,
		owner:          c,
		recvDeclStruct: recvStruct,
		recvDeclEnum:   recvEnum,
		recvOwner:      recvOwner,
		specOwner:      specOwner,
		spec:           spec,
	}
	if c.crossMod != nil && c.crossMod.bundleMono != nil {
		for _, prev := range c.crossMod.bundleMono.genericImpls {
			if genericImplBaseCollides(prev, gi) {
				return typeErr(id.Pos,
					"duplicate generic impl: %s already implements %s at %s",
					describeGenericImplBase(gi), describeGenericImplSpec(gi), prev.ast.Pos)
			}
		}
		c.crossMod.bundleMono.genericImpls = append(c.crossMod.bundleMono.genericImpls, gi)
	} else {
		for _, prev := range c.genericImpls {
			if genericImplBaseCollides(prev, gi) {
				return typeErr(id.Pos,
					"duplicate generic impl: %s already implements %s at %s",
					describeGenericImplBase(gi), describeGenericImplSpec(gi), prev.ast.Pos)
			}
		}
	}
	c.genericImpls = append(c.genericImpls, gi)
	return nil
}

// resolveGenericImplReceiver locates the generic StructDecl / EnumDecl for a
// generic impl's receiver-type name, walking the importing module's bundle
// when id.TypeModule is set. Returns (struct, enum, owner, err) with exactly
// one of struct / enum non-nil on success.
func (c *checker) resolveGenericImplReceiver(id *ImplDecl) (*StructDecl, *EnumDecl, ModuleView, error) {
	if id.TypeModule != "" {
		mod := c.lookupImportedModule(id.TypeModule)
		if mod == nil {
			return nil, nil, nil, typeErr(id.Pos, "unknown module %q", id.TypeModule)
		}
		fc := c.crossMod.checkers[mod]
		if fc == nil {
			return nil, nil, nil, typeErr(id.Pos, "internal: no checker for module %q", id.TypeModule)
		}
		if d, ok := fc.structAST[id.Type]; ok && len(d.TypeParams) > 0 {
			if !d.Pub {
				return nil, nil, nil, typeErr(id.Pos,
					"cannot access '%s.%s': %q is not pub in module %q",
					id.TypeModule, id.Type, id.Type, id.TypeModule)
			}
			return d, nil, mod, nil
		}
		if d, ok := fc.enumAST[id.Type]; ok && len(d.TypeParams) > 0 {
			if !d.Pub {
				return nil, nil, nil, typeErr(id.Pos,
					"cannot access '%s.%s': %q is not pub in module %q",
					id.TypeModule, id.Type, id.Type, id.TypeModule)
			}
			return nil, d, mod, nil
		}
		return nil, nil, nil, typeErr(id.Pos, "module %q has no generic type %q", id.TypeModule, id.Type)
	}
	if d, ok := c.structAST[id.Type]; ok && len(d.TypeParams) > 0 {
		return d, nil, c.selfMod(), nil
	}
	if d, ok := c.enumAST[id.Type]; ok && len(d.TypeParams) > 0 {
		return nil, d, c.selfMod(), nil
	}
	return nil, nil, nil, typeErr(id.Pos,
		"generic impl receiver %q is not a known generic struct or enum", id.Type)
}

// genericImplBaseCollides reports whether two generic-impl records target
// the same (receiver decl, spec) base. Receiver decls compare by pointer
// (pre-resolved at registration); specs compare by (specOwner, specName)
// pair so two modules' same-named specs don't accidentally fold.
func genericImplBaseCollides(a, b *genericImpl) bool {
	if a.recvDeclStruct != b.recvDeclStruct {
		return false
	}
	if a.recvDeclEnum != b.recvDeclEnum {
		return false
	}
	if a.ast.Spec != b.ast.Spec {
		return false
	}
	if a.specOwner != b.specOwner {
		return false
	}
	return true
}

// describeGenericImplBase renders the receiver-decl name for diagnostics
// (`Box`, `MyList[T, U]` collapses to `MyList`). Used in the duplicate-impl
// diagnostic phrasing.
func describeGenericImplBase(g *genericImpl) string {
	return g.recvDeclName()
}

// describeGenericImplSpec renders the spec slot for diagnostics — the
// spec name for spec impls, "(inherent)" for inherent impls.
func describeGenericImplSpec(g *genericImpl) string {
	if g.ast.Spec == "" {
		return "(inherent)"
	}
	return g.ast.Spec
}

// boolSetFromPos converts a {name → Position} map into a {name → true} set
// — the validateGenericTypeRef helper takes the latter.
func boolSetFromPos(in map[string]Position) map[string]bool {
	out := make(map[string]bool, len(in))
	for k := range in {
		out[k] = true
	}
	return out
}

// expandGenericImplsForType is the per-instantiation hook invoked by
// instantiateGenericStruct / instantiateGenericEnum after a fresh mono *Type
// is produced. It walks every generic-impl record in the bundle, finds those
// whose recv decl matches `decl`, derives the impl-level type-arg
// substitution, runs the bound check, and registers a concrete impl entry.
//
// `decl` is the originating generic decl (StructDecl or EnumDecl). The
// caller passes the decl pointer used as the receiver decl; mono is the
// freshly-built canonical *Type; args is the resolved type-arg vector
// (positionally aligned with decl.TypeParams). refPos is the user-visible
// position the diagnostic should anchor on (the type-use site).
//
// Returns an error if any matching generic impl fails its bound check or
// collides with a previously-registered impl on the same (mono, spec) pair.
func (c *checker) expandGenericImplsForType(declStruct *StructDecl, declEnum *EnumDecl, mono *Type, args []*Type, refPos Position) error {
	impls := c.genericImpls
	if c.crossMod != nil && c.crossMod.bundleMono != nil {
		impls = c.crossMod.bundleMono.genericImpls
	}
	for _, gi := range impls {
		if declStruct != nil && gi.recvDeclStruct != declStruct {
			continue
		}
		if declEnum != nil && gi.recvDeclEnum != declEnum {
			continue
		}
		if gi.recvDeclStruct == nil && gi.recvDeclEnum == nil {
			continue
		}
		if err := c.expandOneGenericImpl(gi, mono, args, refPos); err != nil {
			return err
		}
	}
	return nil
}

// expandOneGenericImpl materialises a concrete impl record for `gi` applied
// to `mono` with the receiver-decl args `args`. Idempotent: a (gi, mono)
// pair is expanded at most once across the bundle.
func (c *checker) expandOneGenericImpl(gi *genericImpl, mono *Type, args []*Type, refPos Position) error {
	bMono := bundleMonoFor(c)
	if bMono != nil {
		if bMono.expandedImpls[expandedKey{gi: gi, receiver: mono}] {
			return nil
		}
		bMono.expandedImpls[expandedKey{gi: gi, receiver: mono}] = true
	}
	// Bind the impl-level TypeParams via positional unification of the
	// impl's TypeArgs (declared on the receiver) against `args`.
	subst, err := c.bindImplSubstitution(gi, args, refPos)
	if err != nil {
		// Roll back the dedup mark so a future call can produce the same
		// diagnostic again at a different use site.
		if bMono != nil {
			delete(bMono.expandedImpls, expandedKey{gi: gi, receiver: mono})
		}
		return err
	}
	// Bound check: every TypeParam's bounds must hold on its substituted
	// concrete arg.
	for _, tp := range gi.ast.TypeParams {
		conc := subst[tp.Name]
		for _, bRef := range tp.Bounds {
			specT, err := gi.owner.resolveTypeRef(bRef)
			if err != nil {
				return err
			}
			if specT == nil || specT.Kind != TypeSpec {
				return typeErr(bRef.Pos, "bound %q is not a spec", bRef.String())
			}
			if !c.assignableTo(conc, specT) {
				return typeErr(refPos,
					"type %q does not implement %s", conc, specT.Name)
			}
		}
	}
	// Concrete-impl collision: a previous concrete impl on (mono, spec) or
	// a previous generic-impl expansion for the same (mono, spec) reject.
	owner := gi.owner
	key := implKey{typeName: mono.Name, specName: gi.ast.Spec}
	if prev, exists := owner.impls[key]; exists {
		return typeErr(refPos,
			"duplicate impl: %s already implements %s at %s",
			mono.Name, implSpecOrInherent(prev), prev.Pos)
	}
	// Build a synthetic ImplDecl with deep-cloned method FnDecls so each
	// instantiation owns its own typed[] storage. Sharing gi.ast.Methods
	// across instantiations would let the second walk overwrite the first
	// walk's recorded types — every downstream consumer (run, cgen) reads
	// expr.Type() and TypeRef.Resolved so the last instantiation would win
	// for every node. We also clone every TypeRef in the signature because
	// cgen reads p.Type.Resolved / fn.Return.Resolved directly.
	clonedMethods := make([]*FnDecl, len(gi.ast.Methods))
	for i, m := range gi.ast.Methods {
		cm := *m
		cm.Body = cloneBlock(m.Body)
		cm.Params = make([]FnParam, len(m.Params))
		for j, p := range m.Params {
			cm.Params[j] = FnParam{Name: p.Name, Pos: p.Pos, Type: cloneTypeRef(p.Type)}
		}
		cm.Return = cloneTypeRef(m.Return)
		clonedMethods[i] = &cm
	}
	implAST := *gi.ast
	implAST.Methods = clonedMethods
	implAST.Receiver = mono
	saved := owner.activeSubst
	owner.activeSubst = subst
	methods, methodIdx, err := owner.buildImplMethods(&implAST)
	owner.activeSubst = saved
	if err != nil {
		return err
	}
	// Spec validation against the substituted method signatures.
	if gi.spec != nil {
		if err := owner.validateImplAgainstSpec(&implAST, methods, gi.spec); err != nil {
			return err
		}
	}
	impl := &Impl{
		Pos:       gi.ast.Pos,
		TypeName:  mono.Name,
		SpecName:  gi.ast.Spec,
		Receiver:  mono,
		Methods:   methods,
		methodIdx: methodIdx,
		ast:       &implAST,
		recvOwner: gi.recvOwner,
		specOwner: gi.specOwner,
	}
	owner.impls[key] = impl
	owner.implsByType[mono.Name] = append(owner.implsByType[mono.Name], impl)
	// Refresh the per-receiver method-visibility entry so dispatch sees
	// the new methods. We splice into the existing map; the only consumer
	// is dispatchConcreteMethod which keys by recv.Name.
	if owner.methodVisible == nil {
		owner.methodVisible = map[string]map[string][]*methodSource{}
	}
	visible := owner.methodVisible[mono.Name]
	if visible == nil {
		visible = map[string][]*methodSource{}
		owner.methodVisible[mono.Name] = visible
	}
	if gi.ast.Spec == "" {
		for _, im := range methods {
			src := &methodSource{
				kind:     mskInherent,
				pos:      im.pos,
				name:     im.name,
				impl:     impl,
				implFn:   im,
				inherent: im,
			}
			visible[im.name] = append(visible[im.name], src)
		}
	} else {
		for _, sm := range gi.spec.Methods {
			src := &methodSource{
				kind:     mskSpec,
				name:     sm.name,
				specName: gi.spec.Name,
				impl:     impl,
			}
			if im, ok := impl.methodIdx[sm.name]; ok {
				src.pos = im.pos
				src.implFn = im
			} else {
				src.pos = sm.pos
				src.defaultM = sm
			}
			visible[sm.name] = append(visible[sm.name], src)
		}
	}
	// Walk the cloned method bodies under the substitution so any
	// internal type annotations resolve to concrete types.
	if err := c.walkExpandedImplBodies(gi, impl, methods, subst, mono); err != nil {
		return err
	}
	// Surface the synthetic ImplDecl on the owner module's Program so
	// codegen can iterate it in the same pipeline as user-written impls.
	// The owner is the checker that DECLARED the generic impl; if it has
	// no Program (single-program Check) fall back to the call-site
	// checker's Program.
	if owner.ownProg != nil {
		owner.ownProg.MonoImpls = append(owner.ownProg.MonoImpls, &implAST)
	} else if c.ownProg != nil {
		c.ownProg.MonoImpls = append(c.ownProg.MonoImpls, &implAST)
	}
	return nil
}

// bindImplSubstitution derives the impl-level type-parameter substitution by
// positionally unifying the impl's declared TypeArgs against the receiver-
// type instance's resolved args. v0.6 keeps it simple: each TypeArg is
// expected to be either a bare reference to a declared type-parameter
// (binding it to the corresponding instance arg) or a concrete type that
// must equal the instance arg.
func (c *checker) bindImplSubstitution(gi *genericImpl, args []*Type, refPos Position) (map[string]*Type, error) {
	subst := map[string]*Type{}
	for _, tp := range gi.ast.TypeParams {
		subst[tp.Name] = nil
	}
	for i, ref := range gi.ast.TypeArgs {
		if err := gi.owner.unify(ref, args[i], subst, refPos); err != nil {
			return nil, err
		}
	}
	for _, tp := range gi.ast.TypeParams {
		if subst[tp.Name] == nil {
			return nil, typeErr(gi.ast.Pos,
				"cannot infer type parameter %q for impl on %s", tp.Name, gi.recvDeclName())
		}
	}
	return subst, nil
}

// walkExpandedImplBodies type-checks every cloned method body against its
// resolved signature, with c.activeSubst set so bare `T` annotations bind
// to the substituted concrete type. The receiver type is the freshly-built
// mono *Type.
func (c *checker) walkExpandedImplBodies(gi *genericImpl, impl *Impl, methods []*implMethod, subst map[string]*Type, mono *Type) error {
	owner := gi.owner
	for _, im := range methods {
		fn := im.ast
		sig := fnSig{params: im.params, ret: im.ret, pos: fn.Pos}
		savedFn := owner.currentFn
		savedRecv := owner.currentReceiver
		savedSubst := owner.activeSubst
		owner.currentFn = &sig
		owner.currentReceiver = mono
		owner.activeSubst = subst
		owner.scope = newScope(owner.scope)
		var err error
		for i, p := range fn.Params {
			if !owner.scope.declare(p.Name, binding{kind: bindLet, typ: im.params[i]}) {
				err = typeErr(p.Pos, "parameter %q already declared", p.Name)
				break
			}
		}
		if err == nil {
			for _, st := range fn.Body.Statements {
				if e := owner.checkStmt(st); e != nil {
					err = e
					break
				}
			}
		}
		owner.scope = owner.scope.parent
		owner.currentFn = savedFn
		owner.currentReceiver = savedRecv
		owner.activeSubst = savedSubst
		if err != nil {
			return err
		}
	}
	_ = impl
	return nil
}

// expandPendingGenericImpls runs after every module's resolveImplsCross to
// reconcile any mono *Type that was created BEFORE the matching generic
// impl was registered. The instantiateGeneric* path expands eagerly when a
// generic impl is already in the bundle, but a concrete impl earlier in
// the same module can monomorphise the receiver-type before the generic
// impl is discovered. This pass walks every cached mono and re-applies the
// expansion; cache deduplication keeps it idempotent.
func (c *checker) expandPendingGenericImpls() error {
	impls := c.genericImpls
	if c.crossMod != nil && c.crossMod.bundleMono != nil {
		impls = c.crossMod.bundleMono.genericImpls
	}
	if len(impls) == 0 {
		return nil
	}
	// Walk every mono struct.
	for _, mono := range c.monoStructs {
		for _, gi := range impls {
			if gi.recvDeclStruct == nil {
				continue
			}
			args, ok := genericInstanceArgsForStruct(gi.recvDeclStruct, mono)
			if !ok {
				continue
			}
			if err := c.expandOneGenericImpl(gi, mono, args, gi.ast.Pos); err != nil {
				return err
			}
		}
	}
	for _, mono := range c.monoEnums {
		for _, gi := range impls {
			if gi.recvDeclEnum == nil {
				continue
			}
			args, ok := genericInstanceArgsForEnum(gi.recvDeclEnum, mono)
			if !ok {
				continue
			}
			if err := c.expandOneGenericImpl(gi, mono, args, gi.ast.Pos); err != nil {
				return err
			}
		}
	}
	return nil
}

// genericInstanceArgsForStruct recovers the type-arg vector from a
// canonical mono struct instance whose Name is `Decl[arg1, arg2, ...]`. It
// inverts the substitution by walking the decl's field types alongside the
// instance's substituted field types — for every field-type position that
// names a declared type-param, the corresponding instance field's *Type is
// the arg.
func genericInstanceArgsForStruct(decl *StructDecl, mono *Type) ([]*Type, bool) {
	if mono == nil || mono.Kind != TypeStruct {
		return nil, false
	}
	if !instanceMatchesDecl(mono.Name, decl.Name) {
		return nil, false
	}
	if len(mono.Fields) != len(decl.Fields) {
		return nil, false
	}
	out := make([]*Type, len(decl.TypeParams))
	tpIdx := map[string]int{}
	for i, tp := range decl.TypeParams {
		tpIdx[tp.Name] = i
	}
	for i, f := range decl.Fields {
		walkRefForArgs(f.Type, mono.Fields[i].Type, tpIdx, out)
	}
	for _, v := range out {
		if v == nil {
			return nil, false
		}
	}
	return out, true
}

// genericInstanceArgsForEnum mirrors genericInstanceArgsForStruct on enum
// instances. We walk every variant payload position to find type-param
// references.
func genericInstanceArgsForEnum(decl *EnumDecl, mono *Type) ([]*Type, bool) {
	if mono == nil || mono.Kind != TypeEnum {
		return nil, false
	}
	if !instanceMatchesDecl(mono.Name, decl.Name) {
		return nil, false
	}
	if len(mono.Variants) != len(decl.Variants) {
		return nil, false
	}
	out := make([]*Type, len(decl.TypeParams))
	tpIdx := map[string]int{}
	for i, tp := range decl.TypeParams {
		tpIdx[tp.Name] = i
	}
	for vi, v := range decl.Variants {
		concPayload := mono.VariantPayloads[vi]
		if len(v.Payload) != len(concPayload) {
			continue
		}
		for pi, ref := range v.Payload {
			walkRefForArgs(ref, concPayload[pi], tpIdx, out)
		}
	}
	for _, v := range out {
		if v == nil {
			return nil, false
		}
	}
	return out, true
}

// instanceMatchesDecl reports whether mono.Name has the shape
// `Decl[<args>]` for the given decl name. Excludes other decls with
// names that share a prefix (`Box2[...]` is not `Box`).
func instanceMatchesDecl(monoName, declName string) bool {
	prefix := declName + "["
	if len(monoName) <= len(prefix) {
		return false
	}
	if monoName[:len(prefix)] != prefix {
		return false
	}
	return monoName[len(monoName)-1] == ']'
}

// walkRefForArgs walks a TypeRef alongside its substituted concrete *Type;
// every TypeRef leaf that names a declared type-parameter records the
// corresponding *Type in out at the type-param's index.
func walkRefForArgs(ref *TypeRef, conc *Type, tpIdx map[string]int, out []*Type) {
	if ref == nil || conc == nil {
		return
	}
	switch ref.Kind {
	case TypeRefNamed:
		if ref.Module == "" && len(ref.TypeArgs) == 0 && !ref.Nullable {
			if i, ok := tpIdx[ref.Name]; ok && out[i] == nil {
				out[i] = conc
			}
		}
	case TypeRefList:
		if conc.Kind == TypeList {
			walkRefForArgs(ref.Element, conc.Element, tpIdx, out)
		}
	case TypeRefTuple:
		if conc.Kind == TypeTuple && len(ref.Elements) == len(conc.Tuple) {
			for i, sub := range ref.Elements {
				walkRefForArgs(sub, conc.Tuple[i], tpIdx, out)
			}
		}
	}
}

// bundleMonoFor returns the bundle-shared bundleMono for c, or nil for
// single-program checks that don't share state.
func bundleMonoFor(c *checker) *bundleMono {
	if c.crossMod == nil {
		return nil
	}
	return c.crossMod.bundleMono
}

// resolveConcreteGenericImplDecl handles `impl Box[int] for Spec { ... }` —
// a non-generic impl whose receiver-type carries concrete generic type-args.
// We synthesise a TypeRef from id.Type / id.TypeModule / id.TypeArgs, run it
// through the regular resolveTypeRef path (which monomorphises Box[int] into
// a canonical *Type), then register the impl against that mono receiver.
//
// The v0.5 orphan rule still applies — the importing module must own either
// the receiver decl or the spec; we use the resolved mono's owning module
// (the module that defined the generic decl) for the receiver-owner slot.
func (c *checker) resolveConcreteGenericImplDecl(id *ImplDecl) error {
	// Resolve the generic decl to find its owner module so the orphan
	// rule fires on the BASE decl (consistent with the generic-impl path).
	recvStruct, recvEnum, recvOwner, err := c.resolveGenericImplReceiver(id)
	if err != nil {
		return err
	}
	_ = recvStruct
	_ = recvEnum
	// Build a synthetic TypeRef and resolve it. resolveTypeRef walks the
	// decl + arg list and writes into the mono cache; we get back the
	// canonical *Type for the instance.
	synth := &TypeRef{
		Pos:      id.Pos,
		Kind:     TypeRefNamed,
		Name:     id.Type,
		Module:   id.TypeModule,
		TypeArgs: id.TypeArgs,
	}
	mono, err := c.resolveTypeRef(synth)
	if err != nil {
		return err
	}
	if mono == nil {
		return typeErr(id.Pos, "internal: failed to monomorphise %q", id.Type)
	}
	// Spec resolution + orphan rule on base decls.
	var spec *Spec
	var specOwner ModuleView
	if id.Spec != "" {
		s, owner, err := c.resolveImplSpec(id)
		if err != nil {
			return err
		}
		spec = s
		specOwner = owner
	}
	if c.crossMod != nil {
		selfMod := c.crossMod.self
		ownsRecv := recvOwner == selfMod
		if id.Spec == "" {
			if !ownsRecv {
				return typeErr(id.Pos,
					"cross-module orphan impl: must define %q in this module", id.Type)
			}
		} else {
			ownsSpec := specOwner == selfMod
			if !ownsRecv && !ownsSpec {
				return typeErr(id.Pos,
					"cross-module orphan impl: must define %q or %q in this module", id.Type, id.Spec)
			}
		}
	}
	key := implKey{typeName: mono.Name, specName: id.Spec}
	if prev, exists := c.impls[key]; exists {
		if id.Spec != "" {
			return typeErr(id.Pos,
				"duplicate impl: %s already implements %s at %s",
				mono.Name, id.Spec, prev.Pos)
		}
	}
	methods, methodIdx, err := c.buildImplMethods(id)
	if err != nil {
		return err
	}
	if spec != nil {
		if err := c.validateImplAgainstSpec(id, methods, spec); err != nil {
			return err
		}
	}
	id.Receiver = mono
	impl := &Impl{
		Pos:       id.Pos,
		TypeName:  mono.Name,
		SpecName:  id.Spec,
		Receiver:  mono,
		Methods:   methods,
		methodIdx: methodIdx,
		ast:       id,
		recvOwner: recvOwner,
		specOwner: specOwner,
	}
	if id.Spec == "" {
		c.rawInherentDecls = append(c.rawInherentDecls, id)
	}
	c.impls[key] = impl
	c.implsByType[mono.Name] = append(c.implsByType[mono.Name], impl)
	return nil
}
