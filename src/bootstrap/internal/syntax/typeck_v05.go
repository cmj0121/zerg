package syntax

import (
	"fmt"
)

// ---------------------------------------------------------------------------
// v0.5 Unit 3 — cross-module typeck, pub gating, orphan rule.
//
// CheckBundle is the multi-module entry point. Single-file callers continue to
// use Check (which now delegates through CheckBundle for one-Module bundles).
//
// To avoid a syntax→loader import cycle, this file defines small abstract
// interfaces (BundleView, ModuleView, ImportView). The loader.Bundle type
// satisfies BundleView by virtue of having Entry and Modules fields whose
// shapes match the contract here.
//
// Invariants:
//   - Each Module has its own decl tables (fns, structs, enums, specs, impls).
//   - Imports[] in a Module bind a local name to a Module pointer. Identifier
//     resolution looks up the local name in the binding table; on hit, the
//     follow-up name is looked up in the target Module's decl tables, with a
//     `pub` gate enforced on access.
//   - Cross-module impls are admitted only when the importing module owns
//     either the receiver type or the spec (orphan rule). Inherent impls
//     require ownership of the receiver type.
//   - Cross-module impl collisions are detected on the (origin-module, type,
//     spec) tuple by walking every Module's impls into a global table.
// ---------------------------------------------------------------------------

// ModuleView is the interface a Module must satisfy for CheckBundle to walk
// it. Loader's *Module satisfies this directly; tests that bypass the loader
// can construct a struct with the same fields.
type ModuleView interface {
	// ModuleName is "main" for the entry module; otherwise the canonical
	// path (or the test's chosen module name).
	ModuleName() string
	// ModuleProgram returns the parsed AST.
	ModuleProgram() *Program
	// ModuleImports returns the resolved imports of this module, in source
	// order. Each ImportView ties a local binding name to a target Module.
	ModuleImports() []ImportView
}

// ImportView is one resolved import: a local binding name and a pointer at
// the imported module. The Decl is carried through so diagnostics can anchor
// on the originating ImportDecl.
type ImportView interface {
	ImportLocalName() string
	ImportTarget() ModuleView
	ImportDecl() *ImportDecl
}

// BundleView is the loader's Bundle as far as typeck is concerned: an entry
// module plus the full module list. CheckBundle iterates Modules() so every
// reachable module is checked.
type BundleView interface {
	BundleEntry() ModuleView
	BundleModules() []ModuleView
}

// ---------------------------------------------------------------------------
// CheckBundle entry point.
// ---------------------------------------------------------------------------

// CheckBundle type-checks every module in bundle, with cross-module name
// resolution, pub gating, the orphan rule, and cross-module impl collision
// detection. Backward-compat: a Bundle with a single module and no imports
// behaves identically to single-program Check.
func CheckBundle(bundle BundleView) error {
	if bundle == nil {
		return nil
	}
	mods := bundle.BundleModules()
	if len(mods) == 0 {
		return nil
	}

	// Phase 1: per-module decl-collection / signature-resolution. Each
	// module gets its own checker context. Cross-module access is OFF
	// during these phases — they only set up local tables.
	checkers := make(map[ModuleView]*checker, len(mods))
	for _, m := range mods {
		c := newChecker()
		c.ownProg = m.ModuleProgram()
		checkers[m] = c
		if err := c.collectTopLevel(m.ModuleProgram()); err != nil {
			return err
		}
	}

	// After every module's top-level decl table is built, check
	// module-name shadow conflicts in each module: imports must not
	// shadow each other or top-level names. These rules need the local
	// decl table (above) and the import list (below) to be visible
	// together, but they DO NOT require cross-module type resolution —
	// so they fire here, before resolveStructFields, which is where
	// cross-module type lookup begins.
	for _, m := range mods {
		c := checkers[m]
		if err := c.bindModuleImports(m); err != nil {
			return err
		}
	}

	// Phase 2: cross-module type / signature resolution. From here on,
	// resolveTypeRef can walk into a foreign module's decl tables via
	// the importing module's binding. We attach the per-bundle checkers
	// map onto each module's crossModCtx (created in Phase 1.5 by
	// bindModuleImports) so the import bindings populated above survive.
	//
	// v0.6 Unit 3: install the bundle-shared monomorphisation cache so a
	// generic instance constructed in any module canonicalises to one
	// *Type. The crossModCtx already carries the importing module's
	// binding state; tagging bundleMono on it makes the cache reachable
	// from any per-checker entrypoint.
	bMono := newBundleMono()
	for _, m := range mods {
		c := checkers[m]
		c.crossMod.checkers = checkers
		c.crossMod.bundleMono = bMono
		c.attachBundleMono()
		if err := c.resolveStructFields(m.ModuleProgram()); err != nil {
			return err
		}
		if err := c.resolveEnumPayloads(m.ModuleProgram()); err != nil {
			return err
		}
	}
	for _, m := range mods {
		c := checkers[m]
		if err := c.detectTypeCycles(); err != nil {
			return err
		}
	}
	for _, m := range mods {
		c := checkers[m]
		if err := c.resolveFnSignatures(m.ModuleProgram()); err != nil {
			return err
		}
		if err := c.resolveSpecs(m.ModuleProgram()); err != nil {
			return err
		}
	}
	// v0.6 Unit 3: register and validate generic-fn signatures after the
	// regular fn / spec passes so bound TypeRefs can resolve to spec types
	// across the bundle.
	for _, m := range mods {
		c := checkers[m]
		if err := c.resolveGenericFnSignatures(m.ModuleProgram()); err != nil {
			return err
		}
	}

	// Phase 3: per-module impl resolution + orphan rule. Impl resolution
	// reaches across module boundaries to look up the receiver type and
	// (optionally) the spec; the orphan rule requires the importing module
	// to own at least one of them.
	for _, m := range mods {
		c := checkers[m]
		if err := c.resolveImplsCross(m); err != nil {
			return err
		}
	}

	// v0.6 Unit 3.5: late-arriving generic impl registration. A concrete
	// impl earlier in the same module may have eagerly monomorphised a
	// receiver-type instance before the generic impl was discovered; the
	// post-pass here walks every cached mono *Type and expands any
	// generic impl that should have applied. Idempotent — already-
	// expanded (gi, mono) pairs are cached in bundleMono.expandedImpls.
	for _, m := range mods {
		c := checkers[m]
		if err := c.expandPendingGenericImpls(); err != nil {
			return err
		}
	}

	// Phase 4: cross-module impl collision detection. Two modules each
	// declaring the same (originType, originSpec) impl reject with a
	// dedicated diagnostic.
	if err := detectCrossModuleImplCollisions(mods, checkers); err != nil {
		return err
	}

	// Phase 5: per-module body checks. Each module's checker runs its
	// existing body-walk passes; cross-module references resolve through
	// the c.crossMod handle that was wired in Phase 2.
	for _, m := range mods {
		c := checkers[m]
		if err := c.buildMethodVisibility(); err != nil {
			return err
		}
	}
	for _, m := range mods {
		c := checkers[m]
		if err := c.checkSpecBodies(m.ModuleProgram()); err != nil {
			return err
		}
		if err := c.checkImplBodies(m.ModuleProgram()); err != nil {
			return err
		}
		for _, stmt := range m.ModuleProgram().Statements {
			if err := c.checkStmt(stmt); err != nil {
				return err
			}
		}
	}

	// Phase 6: borrow check, per module. Borrow rules don't change at
	// v0.5 — Unit 4 verifies that cross-module names borrow-check exactly
	// like local ones. We re-run borrowCheck on each module's program
	// with that module's tables.
	for _, m := range mods {
		c := checkers[m]
		if err := borrowCheck(m.ModuleProgram(), c.fns, c.structs, c.enums, c.specs); err != nil {
			return err
		}
	}
	return nil
}

// newChecker constructs a fresh per-module checker context. Equivalent to
// the body of Check up to the collectTopLevel call.
func newChecker() *checker {
	c := &checker{
		scope:         newScope(nil),
		fns:           map[string]fnSig{},
		structs:       map[string]*Type{},
		enums:         map[string]*Type{},
		structAST:     map[string]*StructDecl{},
		enumAST:       map[string]*EnumDecl{},
		specs:         map[string]*Spec{},
		impls:         map[implKey]*Impl{},
		implsByType:   map[string][]*Impl{},
		methodVisible: map[string]map[string][]*methodSource{},
	}
	// Pre-populate built-ins: same as Check.
	c.fns["len"] = fnSig{
		params:  []*Type{NewListType(tInt)},
		ret:     tInt,
		builtin: true,
	}
	c.fns["push"] = fnSig{
		params:  []*Type{NewListType(tInt), tInt},
		ret:     tVoid,
		builtin: true,
	}
	c.fns["clone"] = fnSig{
		params:  []*Type{NewListType(tInt)},
		ret:     NewListType(tInt),
		builtin: true,
	}
	// v0.14 str ↔ list[byte] bridge: these are the primitives that
	// unblock pure-Zerg stdlib strings work (split, trim, replace, …).
	// `bytes(s: str) -> list[byte]` allocates a fresh copy of s's bytes;
	// `to_str(buf: list[byte]) -> str` allocates a fresh str from buf.
	// Both signatures use a placeholder element type in the registry —
	// the typeck dispatch (checkCall: ident.Name == "bytes" / "to_str")
	// validates the real argument type and rejects mismatches.
	c.fns["bytes"] = fnSig{
		params:  []*Type{tStr},
		ret:     NewListType(tByte),
		builtin: true,
	}
	c.fns["to_str"] = fnSig{
		params:  []*Type{NewListType(tByte)},
		ret:     tStr,
		builtin: true,
	}
	// v0.6 Unit 3: per-checker monomorphisation caches. CheckBundle replaces
	// these with bundle-shared maps in attachBundleMono so cross-module
	// instances canonicalise to one *Type / *FnDecl.
	c.monoStructs = map[string]*Type{}
	c.monoFns = map[string]*FnDecl{}
	c.genericFnAST = map[string]*FnDecl{}
	// v0.6 Unit 2: register synthetic generic enum decls (Option, Result)
	// before any user-decl walk so the names are visible to every module
	// without an explicit import and the reservation diagnostic in
	// collectTopLevel can fire against a same-named user decl.
	injectBuiltinEnums(c)
	// v0.7 Unit 3: register the synthetic WaitGroup struct type and the
	// `wait_group()` constructor fn. The reservation set in
	// reservedBuiltinTypeNames / collectTopLevel rejects user redecls of
	// the same names with the uniform diagnostic.
	injectWaitGroupBuiltin(c)
	return c
}

// attachBundleMono points c's per-checker mono caches at the bundle-shared
// tables. Called by CheckBundle right after each checker's collect / import-
// bind passes so subsequent type / fn resolution canonicalises bundle-wide.
//
// For correctness on cache contents: the per-checker maps populated by
// injectBuiltinEnums hold only placeholder entries for the bare-name Option /
// Result enums; no instance has been constructed yet. We replace the maps
// rather than copy, because instantiateGenericEnum and friends always
// dereference c.monoEnums / monoStructs / monoFns to look up entries.
func (c *checker) attachBundleMono() {
	if c.crossMod == nil || c.crossMod.bundleMono == nil {
		return
	}
	c.monoEnums = c.crossMod.bundleMono.enums
	c.monoStructs = c.crossMod.bundleMono.structs
	c.monoFns = c.crossMod.bundleMono.fns
}

// crossModCtx is the per-module cross-module resolution state. Lives on
// checker.crossMod and is non-nil only when CheckBundle drives the check.
type crossModCtx struct {
	self     ModuleView
	checkers map[ModuleView]*checker
	// imports binds local name → target Module.
	imports map[string]ModuleView
	// importDecl records which ImportDecl introduced the binding (for
	// diagnostics).
	importDecl map[string]*ImportDecl
	// bundleMono is the v0.6 Unit 3 shared monomorphisation cache. Every
	// module's checker shares one set of maps so a generic instance
	// constructed in module A and module B canonicalises to one *Type.
	bundleMono *bundleMono
}

// bundleMono holds the bundle-wide monomorphisation caches. Three tables —
// generic enum / struct types, and generic fn specialisations — keyed by the
// canonical instance name (`Decl[arg1,arg2,...]`).
//
// v0.6 Unit 3.5 extends the bundle-shared state with generic-impl records.
// Each module's resolveImplsCross discovers its generic impls and appends
// them here; per-instantiation expansion (driven from
// instantiateGenericStruct / instantiateGenericEnum) walks the union so a
// foreign module's `impl[T] LocalType[T] for SomeSpec` lights up regardless
// of which module first triggered the monomorphisation.
type bundleMono struct {
	enums         map[string]*Type
	structs       map[string]*Type
	fns           map[string]*FnDecl
	genericImpls  []*genericImpl
	expandedImpls map[expandedKey]bool
}

// expandedKey deduplicates per-instance expansion of a (genericImpl, mono
// receiver) pair across the bundle so the same (impl, instance) tuple is
// never expanded twice — once expansion has happened, the resulting
// concrete impl entry is shared by every checker.
type expandedKey struct {
	gi       *genericImpl
	receiver *Type
}

func newBundleMono() *bundleMono {
	return &bundleMono{
		enums:         map[string]*Type{},
		structs:       map[string]*Type{},
		fns:           map[string]*FnDecl{},
		expandedImpls: map[expandedKey]bool{},
	}
}

// bindModuleImports populates the import-binding table on c.crossMod and
// rejects the four module-name shadow conflicts:
//
//   - duplicate import (`import "a"; import "a"`)
//   - alias collision (`import "a"; import "b" as a`)
//   - top-level decl shadowing the binding name (`import "a"; struct a {...}`)
//   - reserved-name collision is handled at parse time (Unit 1b)
//
// Local-binding shadowing (`import "u"; u := 1`) is checked at the binding
// site during the body walk because locals aren't in scope yet at this phase.
func (c *checker) bindModuleImports(self ModuleView) error {
	if c.crossMod == nil {
		c.crossMod = &crossModCtx{self: self}
	}
	c.crossMod.imports = map[string]ModuleView{}
	c.crossMod.importDecl = map[string]*ImportDecl{}
	// Build a quick lookup of local top-level decl names so we can spot a
	// module binding name colliding with an existing decl.
	localDecls := map[string]Position{}
	for _, stmt := range self.ModuleProgram().Statements {
		switch s := stmt.(type) {
		case *FnDecl:
			localDecls[s.Name] = s.Pos
		case *StructDecl:
			localDecls[s.Name] = s.Pos
		case *EnumDecl:
			localDecls[s.Name] = s.Pos
		case *SpecDecl:
			localDecls[s.Name] = s.Pos
		}
	}
	for _, imp := range self.ModuleImports() {
		local := imp.ImportLocalName()
		decl := imp.ImportDecl()
		if prev, dup := c.crossMod.imports[local]; dup {
			_ = prev
			prevDecl := c.crossMod.importDecl[local]
			// Distinguish duplicate-path (same imported file under same
			// binding) from alias-collision. The loader dedupes Module
			// pointers by path, so a true `import "a"; import "a"` produces
			// two ImportDecls binding the same name — the diagnostic shape
			// is the same either way: "binding %q already imported".
			return typeErr(decl.Pos, "module binding %q already declared (first at %s)", local, prevDecl.Pos)
		}
		if pos, conflict := localDecls[local]; conflict {
			return typeErr(decl.Pos, "import binding %q collides with top-level declaration at %s", local, pos)
		}
		c.crossMod.imports[local] = imp.ImportTarget()
		c.crossMod.importDecl[local] = decl
	}
	return nil
}

// lookupImportedModule returns the Module bound to local in the importing
// module's import table, or nil if local is not a module binding.
func (c *checker) lookupImportedModule(local string) ModuleView {
	if c.crossMod == nil {
		return nil
	}
	return c.crossMod.imports[local]
}

// resolveImplsCross is the v0.5 replacement for resolveImpls when running
// in a Bundle: it cross-resolves the receiver type / spec via the
// importing module's import bindings, applies the orphan rule, and
// records origin-module info for the cross-module collision pass.
func (c *checker) resolveImplsCross(self ModuleView) error {
	for _, stmt := range self.ModuleProgram().Statements {
		id, ok := stmt.(*ImplDecl)
		if !ok {
			continue
		}
		// v0.6 Unit 3.5: a generic impl block (`impl[T] LocalType[T] for
		// SomeSpec { ... }`) is recorded for deferred per-instantiation
		// expansion. The expansion happens inside instantiateGeneric*
		// when the receiver-type instance is monomorphised.
		if len(id.TypeParams) > 0 {
			if err := c.resolveGenericImplDecl(id); err != nil {
				return err
			}
			continue
		}
		// v0.6: a concrete-arg impl (`impl Box[int] for Spec { ... }`)
		// resolves the receiver to the monomorphised *Type and proceeds
		// through the regular concrete-impl path.
		if len(id.TypeArgs) > 0 {
			if err := c.resolveConcreteGenericImplDecl(id); err != nil {
				return err
			}
			continue
		}
		// Resolve the receiver type. id.Type is a bare name today —
		// Unit 1 carries no qualified-impl shape. The receiver MUST
		// resolve to a struct or enum either in this module or, via an
		// import binding, in a sibling module. The orphan rule fires
		// later if both type and spec are foreign.
		recvType, recvOwner, err := c.resolveImplType(id, "")
		if err != nil {
			return err
		}
		// Spec name (if any) — also bare today; module-qualified specs
		// arrive with the impl-qualifier surface in v0.6+.
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
		// Orphan rule: if a cross-module Bundle is in play, the
		// importing module must own either the receiver type or the
		// spec. Inherent impls only need to own the receiver.
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
		key := implKey{typeName: id.Type, specName: id.Spec}
		if prev, exists := c.impls[key]; exists {
			if id.Spec == "" {
				_ = prev
			} else {
				return typeErr(id.Pos,
					"duplicate impl: %s already implements %s at %s",
					id.Type, id.Spec, prev.Pos)
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
		// Stamp the resolved canonical receiver *Type onto the AST node.
		// Downstream consumers (interp's RunBundle, build's EmitBundle)
		// read id.Receiver to key impl tables by canonical pointer, so
		// two modules' same-named types disambiguate without a bundle-
		// wide name scan.
		id.Receiver = recvType
		impl := &Impl{
			Pos:       id.Pos,
			TypeName:  id.Type,
			SpecName:  id.Spec,
			Receiver:  recvType,
			Methods:   methods,
			methodIdx: methodIdx,
			ast:       id,
		}
		// Record origin-module info on the Impl for the cross-module
		// collision pass. We stash both owners on the impl — the type-
		// owner and the spec-owner — keyed off the impl's own module
		// (which is implicit: it lives in c).
		impl.recvOwner = recvOwner
		impl.specOwner = specOwner
		if id.Spec == "" {
			c.rawInherentDecls = append(c.rawInherentDecls, id)
			if prev, exists := c.impls[key]; exists {
				prev.Methods = append(prev.Methods, methods...)
				for _, im := range methods {
					if _, dup := prev.methodIdx[im.name]; !dup {
						prev.methodIdx[im.name] = im
					}
				}
			} else {
				c.impls[key] = impl
				c.implsByType[id.Type] = append(c.implsByType[id.Type], impl)
			}
		} else {
			c.impls[key] = impl
			c.implsByType[id.Type] = append(c.implsByType[id.Type], impl)
		}
	}
	return nil
}

// resolveImplType resolves an impl's receiver type to a (Type, owning
// Module) pair. owningMod is the Module pointer that DEFINED the type
// (could be self, could be an imported module). When id.TypeModule is
// non-empty, the lookup is module-qualified (`impl mod.T for ...`);
// otherwise the lookup is local-only (the bare name must be defined in
// this module).
func (c *checker) resolveImplType(id *ImplDecl, modulePrefix string) (*Type, ModuleView, error) {
	if id.TypeModule != "" {
		mod := c.lookupImportedModule(id.TypeModule)
		if mod == nil {
			return nil, nil, typeErr(id.Pos, "unknown module %q", id.TypeModule)
		}
		fc := c.crossMod.checkers[mod]
		if fc == nil {
			return nil, nil, typeErr(id.Pos, "internal: no checker for module %q", id.TypeModule)
		}
		if st, ok := fc.structs[id.Type]; ok {
			ast := fc.structAST[id.Type]
			if ast == nil || !ast.Pub {
				return nil, nil, typeErr(id.Pos,
					"cannot access '%s.%s': %q is not pub in module %q",
					id.TypeModule, id.Type, id.Type, id.TypeModule)
			}
			return st, mod, nil
		}
		if en, ok := fc.enums[id.Type]; ok {
			ast := fc.enumAST[id.Type]
			if ast == nil || !ast.Pub {
				return nil, nil, typeErr(id.Pos,
					"cannot access '%s.%s': %q is not pub in module %q",
					id.TypeModule, id.Type, id.Type, id.TypeModule)
			}
			return en, mod, nil
		}
		if _, ok := fc.specs[id.Type]; ok {
			return nil, nil, typeErr(id.Pos, "%q cannot impl spec at v0.4 — only struct and enum types can implement specs", id.Type)
		}
		return nil, nil, typeErr(id.Pos, "module %q has no type %q", id.TypeModule, id.Type)
	}
	// Local name lookup (no qualifier).
	if st, ok := c.structs[id.Type]; ok {
		return st, c.selfMod(), nil
	}
	if en, ok := c.enums[id.Type]; ok {
		return en, c.selfMod(), nil
	}
	if _, ok := c.specs[id.Type]; ok {
		return nil, nil, typeErr(id.Pos, "%q cannot impl spec at v0.4 — only struct and enum types can implement specs", id.Type)
	}
	switch id.Type {
	case "int", "float", "bool", "str", "byte", "rune":
		return nil, nil, typeErr(id.Pos, "%q cannot impl spec at v0.4 — only struct and enum types can implement specs", id.Type)
	}
	return nil, nil, typeErr(id.Pos, "unknown type %q", id.Type)
}

// resolveImplSpec resolves an impl's spec to a (Spec, owning Module) pair.
// Mirrors resolveImplType for the spec slot. id.SpecModule, when set,
// drives the module-qualified lookup.
func (c *checker) resolveImplSpec(id *ImplDecl) (*Spec, ModuleView, error) {
	if id.SpecModule != "" {
		mod := c.lookupImportedModule(id.SpecModule)
		if mod == nil {
			return nil, nil, typeErr(id.Pos, "unknown module %q", id.SpecModule)
		}
		fc := c.crossMod.checkers[mod]
		if fc == nil {
			return nil, nil, typeErr(id.Pos, "internal: no checker for module %q", id.SpecModule)
		}
		if s, ok := fc.specs[id.Spec]; ok {
			if !specIsPub(fc, id.Spec) {
				return nil, nil, typeErr(id.Pos,
					"cannot access '%s.%s': %q is not pub in module %q",
					id.SpecModule, id.Spec, id.Spec, id.SpecModule)
			}
			return s, mod, nil
		}
		return nil, nil, typeErr(id.Pos, "module %q has no spec %q", id.SpecModule, id.Spec)
	}
	if s, ok := c.specs[id.Spec]; ok {
		return s, c.selfMod(), nil
	}
	return nil, nil, typeErr(id.Pos, "unknown spec %q", id.Spec)
}

// selfMod returns the ModuleView pointer of the importing module, or nil
// if not in a Bundle context (single-program Check).
func (c *checker) selfMod() ModuleView {
	if c.crossMod == nil {
		return nil
	}
	return c.crossMod.self
}

// buildImplMethods is the impl-method builder split out of resolveImpls so
// the v0.5 path can reuse it without copying.
func (c *checker) buildImplMethods(id *ImplDecl) ([]*implMethod, map[string]*implMethod, error) {
	methods := make([]*implMethod, 0, len(id.Methods))
	methodIdx := map[string]*implMethod{}
	for _, fn := range id.Methods {
		if fn.Name == "this" {
			return nil, nil, typeErr(fn.Pos, "method must not be named 'this'")
		}
		for _, p := range fn.Params {
			if p.Name == "this" {
				return nil, nil, typeErr(p.Pos, "method %q in impl %s must not declare an explicit 'this' parameter ('this' is implicit)", fn.Name, id.Type)
			}
		}
		if _, dup := methodIdx[fn.Name]; dup {
			return nil, nil, typeErr(fn.Pos, "duplicate method %q in impl block for %s", fn.Name, id.Type)
		}
		params := make([]*Type, len(fn.Params))
		for i, p := range fn.Params {
			t, err := c.resolveTypeRef(p.Type)
			if err != nil {
				return nil, nil, err
			}
			if t == tVoid {
				return nil, nil, typeErr(p.Pos, "parameter %q cannot have void type", p.Name)
			}
			params[i] = t
		}
		ret := tVoid
		if fn.Return != nil {
			rt, err := c.resolveTypeRef(fn.Return)
			if err != nil {
				return nil, nil, err
			}
			if rt == tVoid {
				return nil, nil, typeErr(fn.Return.Pos, "use no return annotation instead of declaring a void return")
			}
			ret = rt
		}
		im := &implMethod{
			pos:    fn.Pos,
			name:   fn.Name,
			ast:    fn,
			params: params,
			ret:    ret,
		}
		methods = append(methods, im)
		methodIdx[fn.Name] = im
	}
	return methods, methodIdx, nil
}

// validateImplAgainstSpec is the spec/impl signature comparison split out
// of resolveImpls. Same logic — kept here so the v0.5 path can use it.
func (c *checker) validateImplAgainstSpec(id *ImplDecl, methods []*implMethod, spec *Spec) error {
	for _, im := range methods {
		sm, ok := spec.methodIdx[im.name]
		if !ok {
			return typeErr(im.pos, "method %q is not declared in spec %q", im.name, spec.Name)
		}
		if len(im.params) != len(sm.params) {
			return typeErr(im.pos, "method %q expects %d parameter(s) per spec %q, got %d", im.name, len(sm.params), spec.Name, len(im.params))
		}
		for i := range im.params {
			if !typeEq(im.params[i], sm.params[i]) {
				return typeErr(im.pos, "method %q parameter %d type %s does not match spec %q signature %s", im.name, i+1, im.params[i], spec.Name, sm.params[i])
			}
		}
		if !typeEq(im.ret, sm.ret) {
			return typeErr(im.pos, "method %q return type %s does not match spec %q return type %s", im.name, im.ret, spec.Name, sm.ret)
		}
	}
	_ = id
	return nil
}

// detectCrossModuleImplCollisions walks every module's impls and rejects two
// modules each declaring the same canonical (origin-Type, origin-Spec) pair.
// origin-Type is keyed off the Module that DEFINED the type; origin-Spec
// likewise. Two impls colliding must therefore agree on both origins —
// otherwise they target distinct (Type, Spec) tuples and don't collide.
//
// Inherent impls (no spec) coexist across modules per the orphan rule (each
// must own the type — the orphan check guarantees only one such module
// exists, so collision is impossible by construction). We still walk
// inherent impls here to defend against misconfiguration in test fixtures.
func detectCrossModuleImplCollisions(mods []ModuleView, checkers map[ModuleView]*checker) error {
	type collisionKey struct {
		typeOwner ModuleView
		typeName  string
		specOwner ModuleView
		specName  string
	}
	type collisionEntry struct {
		mod ModuleView
		pos Position
	}
	seen := map[collisionKey]collisionEntry{}
	for _, m := range mods {
		c := checkers[m]
		for _, impl := range c.impls {
			// v0.7 Unit 3: synthetic built-in impls (e.g. WaitGroup) are
			// injected per checker — every module sees the same name. Skip
			// them so two modules in a bundle don't collide on the
			// shared builtin.
			if isReservedBuiltinTypeName(impl.TypeName) {
				continue
			}
			key := collisionKey{
				typeOwner: impl.recvOwner,
				typeName:  impl.TypeName,
				specOwner: impl.specOwner,
				specName:  impl.SpecName,
			}
			if prev, dup := seen[key]; dup && prev.mod != m {
				return typeErr(impl.Pos,
					"cross-module impl collision: %s for %s already implemented in module %q at %s",
					impl.TypeName, implSpecOrInherent(impl), moduleName(prev.mod), prev.pos)
			}
			seen[key] = collisionEntry{mod: m, pos: impl.Pos}
		}
	}
	return nil
}

func implSpecOrInherent(i *Impl) string {
	if i.SpecName == "" {
		return "(inherent)"
	}
	return i.SpecName
}

func moduleName(m ModuleView) string {
	if m == nil {
		return "<unknown>"
	}
	return m.ModuleName()
}

// ---------------------------------------------------------------------------
// loaderBundleAdapter: the helper Run/Build can use to wrap a loader.Bundle.
//
// We can't import loader from syntax (cycle), so the adapter is defined in
// the loader package itself (or as a thin shim by the caller). The methods
// of BundleView/ModuleView/ImportView are deliberately small so the loader
// can satisfy them with two-line method definitions.
// ---------------------------------------------------------------------------

// CheckSingle is a convenience wrapper that builds a one-module bundle from
// prog and calls CheckBundle. Equivalent to calling Check directly when the
// program has no imports — useful for tests and the REPL.
func CheckSingle(prog *Program) error {
	return CheckBundle(&singleProgramBundle{prog: prog})
}

// singleProgramBundle is the trivial BundleView for a single-Program input
// (no imports). Used by Check and CheckSingle to route through the same
// CheckBundle entry point.
type singleProgramBundle struct {
	prog *Program
}

func (b *singleProgramBundle) BundleEntry() ModuleView   { return &singleProgramModule{b.prog} }
func (b *singleProgramBundle) BundleModules() []ModuleView { return []ModuleView{&singleProgramModule{b.prog}} }

type singleProgramModule struct {
	prog *Program
}

func (m *singleProgramModule) ModuleName() string         { return "main" }
func (m *singleProgramModule) ModuleProgram() *Program    { return m.prog }
func (m *singleProgramModule) ModuleImports() []ImportView { return nil }


// ---------------------------------------------------------------------------
// Cross-module name resolution helpers.
//
// These are called from the existing typeck.go entry points (resolveTypeRef,
// checkFieldAccess, checkMethodCall, checkStructLit, checkCall) when the
// expression carries a module qualifier.
// ---------------------------------------------------------------------------

// resolveCrossModuleType handles `mod.Type` in type position. mod must be an
// imported module binding; Type must be a pub struct, enum, or spec in that
// module. Returns the resolved canonical *Type.
func (c *checker) resolveCrossModuleType(ref *TypeRef) (*Type, error) {
	mod := c.lookupImportedModule(ref.Module)
	if mod == nil {
		return nil, typeErr(ref.Pos, "unknown module %q", ref.Module)
	}
	fc := c.crossMod.checkers[mod]
	if fc == nil {
		return nil, typeErr(ref.Pos, "internal: no checker for module %q", ref.Module)
	}
	// Look up the name in the foreign module's struct/enum/spec tables.
	if st, ok := fc.structs[ref.Name]; ok {
		ast := fc.structAST[ref.Name]
		if !ast.Pub {
			return nil, typeErr(ref.Pos,
				"cannot access '%s.%s': %q is not pub in module %q",
				ref.Module, ref.Name, ref.Name, ref.Module)
		}
		return st, nil
	}
	if en, ok := fc.enums[ref.Name]; ok {
		ast := fc.enumAST[ref.Name]
		if !ast.Pub {
			return nil, typeErr(ref.Pos,
				"cannot access '%s.%s': %q is not pub in module %q",
				ref.Module, ref.Name, ref.Name, ref.Module)
		}
		return en, nil
	}
	if sp, ok := fc.specs[ref.Name]; ok {
		// Spec pubness — find the SpecDecl AST in the foreign module.
		if !specIsPub(fc, ref.Name) {
			return nil, typeErr(ref.Pos,
				"cannot access '%s.%s': %q is not pub in module %q",
				ref.Module, ref.Name, ref.Name, ref.Module)
		}
		return sp.typ, nil
	}
	return nil, typeErr(ref.Pos, "module %q has no type %q", ref.Module, ref.Name)
}

// specIsPub reports whether the SpecDecl named `name` in fc is `pub`. We
// walk the foreign module's AST because the *Spec record itself doesn't
// carry the bit; the cost is O(n) per probe but n is small (a module's
// top-level decl count).
func specIsPub(fc *checker, name string) bool {
	for _, stmt := range moduleStatements(fc) {
		if sd, ok := stmt.(*SpecDecl); ok && sd.Name == name {
			return sd.Pub
		}
	}
	return false
}

// moduleStatements returns the AST statements of fc's owning module, or
// nil when fc isn't part of a Bundle.
func moduleStatements(fc *checker) []Stmt {
	if fc == nil || fc.crossMod == nil || fc.crossMod.self == nil {
		return nil
	}
	return fc.crossMod.self.ModuleProgram().Statements
}

// fnIsPub reports whether the FnDecl named `name` in fc is `pub`.
func fnIsPub(fc *checker, name string) bool {
	for _, stmt := range moduleStatements(fc) {
		if fn, ok := stmt.(*FnDecl); ok && fn.Name == name {
			return fn.Pub
		}
	}
	return false
}

// resolveCrossModuleEnum looks up an enum by name in the foreign module.
// pub-gated.
func (c *checker) resolveCrossModuleEnum(modName, enumName string) (*Type, bool, error) {
	mod := c.lookupImportedModule(modName)
	if mod == nil {
		return nil, false, nil
	}
	fc := c.crossMod.checkers[mod]
	if fc == nil {
		return nil, false, nil
	}
	en, ok := fc.enums[enumName]
	if !ok {
		return nil, false, nil
	}
	ast := fc.enumAST[enumName]
	if ast == nil || !ast.Pub {
		return en, true, fmt.Errorf("not pub")
	}
	return en, true, nil
}

// resolveCrossModuleStruct looks up a struct by name in the foreign module.
// pub-gated.
func (c *checker) resolveCrossModuleStruct(modName, structName string) (*Type, bool, error) {
	mod := c.lookupImportedModule(modName)
	if mod == nil {
		return nil, false, nil
	}
	fc := c.crossMod.checkers[mod]
	if fc == nil {
		return nil, false, nil
	}
	st, ok := fc.structs[structName]
	if !ok {
		return nil, false, nil
	}
	ast := fc.structAST[structName]
	if ast == nil || !ast.Pub {
		return st, true, fmt.Errorf("not pub")
	}
	return st, true, nil
}

// checkCrossModuleFnCall type-checks the args of a `mod.fn(args)` call
// against the foreign fn's resolved signature. The receiver IdentExpr's
// type is set to a sentinel TypeUnknown to flag that it isn't a value
// reference (it's a module binding); downstream consumers don't read the
// type because the MethodCallExpr's lowered form can convey the call.
func (c *checker) checkCrossModuleFnCall(e *MethodCallExpr, sig fnSig) (*Type, error) {
	if len(e.Args) != len(sig.params) {
		return nil, typeErr(e.Pos,
			"function %q expects %d argument(s), got %d", e.Method, len(sig.params), len(e.Args))
	}
	for i, a := range e.Args {
		at, err := c.checkExprHint(a, sig.params[i])
		if err != nil {
			return nil, err
		}
		if !typeEq(at, sig.params[i]) {
			return nil, typeErr(a.ExprPos(),
				"argument %d to %q has type %s, expected %s",
				i+1, e.Method, at, sig.params[i])
		}
	}
	// Lower into a CallExpr that downstream layers can route through the
	// existing cross-fn machinery once Unit 5 wires it up. For typeck
	// purposes the lowered call is documentation only — the MethodCallExpr
	// keeps its return type set below.
	e.setType(sig.ret)
	return sig.ret, nil
}

// lowerCrossModuleEnumLitFromMethodCall lowers `mod.Enum.Variant(args)`
// to an EnumLit with Module set. Mirrors lowerEnumLitFromMethodCall but
// records the module so codegen / interpreter can route through the
// foreign module's enum table.
func (c *checker) lowerCrossModuleEnumLitFromMethodCall(e *MethodCallExpr, en *Type, variantIdx int, modName string) (*Type, error) {
	variantName := en.Variants[variantIdx]
	payload := en.VariantPayloads[variantIdx]
	if len(payload) == 0 {
		return nil, typeErr(e.MethodPos,
			"variant %q.%q has no payload — drop the parentheses to construct",
			en.Name, variantName)
	}
	if len(e.Args) != len(payload) {
		return nil, typeErr(e.Pos,
			"variant %q.%q expects %d payload value(s), got %d",
			en.Name, variantName, len(payload), len(e.Args))
	}
	args := make([]Expr, len(e.Args))
	for i, a := range e.Args {
		at, err := c.checkExprHint(a, payload[i])
		if err != nil {
			return nil, err
		}
		if !typeEq(at, payload[i]) {
			return nil, typeErr(a.ExprPos(),
				"variant %q.%q payload position %d expects %s, got %s",
				en.Name, variantName, i+1, payload[i], at)
		}
		args[i] = a
	}
	lowered := &EnumLit{
		Pos:        e.Pos,
		EnumName:   en.Name,
		Module:     modName,
		Variant:    variantName,
		VariantPos: e.MethodPos,
		Payload:    args,
	}
	lowered.setType(en)
	e.Lowered = lowered
	e.setType(en)
	return en, nil
}

// collectAllVisibleMethods walks every module's methodVisible map for
// receiver type `recv` and returns the union — gated by `pub` for impls
// declared in foreign modules. Method dispatch over a foreign receiver
// must see all impls registered against that type, regardless of which
// module declared the impl.
//
// The receiver type's *Type pointer is canonical (one instance per name
// in its owning module's table), which lets us key by Type.Name +
// Type.Kind. The method-source list is a flat union; the dispatch caller
// disambiguates inherent vs spec vs collisions exactly as it did at v0.4.
func (c *checker) collectAllVisibleMethods(recv *Type, methodName string) []*methodSource {
	var srcs []*methodSource
	// Local module's visibility map.
	local := c.methodVisible[recv.Name]
	for _, src := range local[methodName] {
		// v0.5: receiver-pointer gate. Two modules may both declare a
		// type named e.g. "Counter" — the visibility map is keyed by
		// the bare name, but each Impl.Receiver carries the canonical
		// *Type pointer for that module's type. When dispatching on
		// the OTHER module's Counter, we must skip the local module's
		// same-named impl. Pointer equality is the dispatch key.
		if src.impl != nil && src.impl.Receiver != nil && src.impl.Receiver != recv {
			continue
		}
		// LOCAL impls (registered on c) need no pub gating.
		srcs = append(srcs, src)
	}
	// Foreign modules' visibility maps. Walk every checker in the
	// bundle except c itself; if the receiver type matches and a
	// method by that name is registered, append (after pub gating).
	if c.crossMod != nil {
		for _, mod := range c.crossMod.checkers {
			if mod == c {
				continue
			}
			// The foreign module may have impls keyed by recv.Name —
			// match only when the receiver type pointer (from c's
			// perspective) matches what the foreign module saw. Since
			// the type *Type is canonical across the bundle (we share
			// the *Type pointer wherever we resolve a cross-module
			// reference), receiver pointer equality is sufficient.
			fc := mod
			fSrcs := fc.methodVisible[recv.Name]
			for _, src := range fSrcs[methodName] {
				if src.impl == nil || src.impl.Receiver != recv {
					continue
				}
				if !methodSrcIsPub(src) {
					continue
				}
				srcs = append(srcs, src)
			}
		}
	}
	return srcs
}

// methodSrcIsPub reports whether a methodSource's underlying FnDecl is
// pub. Used to filter foreign-module impls during cross-module dispatch.
//
//   - mskInherent: the inherent FnDecl's Pub bit.
//   - mskSpec with implFn: the override FnDecl's Pub bit.
//   - mskSpec with defaultM: the spec's default-method body inherits the
//     spec method's Pub bit (carried on SpecMethod.Pub).
func methodSrcIsPub(src *methodSource) bool {
	if src == nil {
		return false
	}
	switch src.kind {
	case mskInherent:
		if src.inherent != nil && src.inherent.ast != nil {
			return src.inherent.ast.Pub
		}
		if src.implFn != nil && src.implFn.ast != nil {
			return src.implFn.ast.Pub
		}
	case mskSpec:
		if src.implFn != nil && src.implFn.ast != nil {
			return src.implFn.ast.Pub
		}
		if src.defaultM != nil && src.defaultM.ast != nil {
			return src.defaultM.ast.Pub
		}
	}
	return false
}

// lookupSpecForImpl returns the *Spec record for impl's spec, walking the
// foreign module if specOwner is non-self. Used by buildMethodVisibility
// and bindResolvedMethodCall when an impl crosses a module boundary.
func (c *checker) lookupSpecForImpl(impl *Impl) *Spec {
	if impl == nil || impl.SpecName == "" {
		return nil
	}
	if s, ok := c.specs[impl.SpecName]; ok {
		return s
	}
	if c.crossMod != nil && impl.specOwner != nil {
		fc := c.crossMod.checkers[impl.specOwner]
		if fc != nil {
			if s, ok := fc.specs[impl.SpecName]; ok {
				return s
			}
		}
	}
	// Last-ditch: walk every module's specs in the bundle. Defends
	// against fixtures where specOwner wasn't recorded for some reason.
	if c.crossMod != nil {
		for _, fc := range c.crossMod.checkers {
			if s, ok := fc.specs[impl.SpecName]; ok {
				return s
			}
		}
	}
	return nil
}
