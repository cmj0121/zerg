// Package sema is the Zerg semantic core. It runs a name-resolution pass (the
// scope tree, the module surface, and the 'const' shadow rule; see resolve.go and
// symbols.go) and then a bidirectional type checker (check.go / infer.go): two
// entry points, synth (the ':=' direction) and check (context-typing against an
// expected type), type every expression over the shared type IR (internal/types).
//
// It produces an Info the emitter consumes: the type of every expression, the
// resolved type of every binding, the collected function signatures, and the
// Phase 1b resolution side tables (Refs / Brackets / Patterns). An integer literal
// defaults to int and a fractional one to float, but a context type retypes it
// (e.g. 'x: u8 = 5', 'x: float = 1'); there is no other implicit numeric
// conversion. Unknown and Invalid types are compatible with anything so an
// unresolved value or a prior error does not cascade.
package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// Type is a Zerg type. Phase 1b carries the shared type IR (internal/types);
// sema re-exports it under the historic name so the emitter — which switches on
// sema.Int / sema.Str / … and spells parameters sema.Type — compiles unchanged.
type Type = types.Type

// The primitive singletons the checker and emitter compare against by identity.
// They alias the interned values in internal/types, so 'case sema.Int:' still
// matches by the same pointer the type IR hands out.
//
//nolint:gochecknoglobals // re-exported interned singletons.
var (
	Invalid Type = types.Invalid
	Nil     Type = types.Nil
	Int     Type = types.Int
	Float   Type = types.Float
	Bool    Type = types.Bool
	Str     Type = types.Str
)

// printable reports whether 'print' accepts a value of type t (Phase 0: a scalar).
func printable(t Type) bool { return isNumeric(t) || t == Bool || t == Str }

// GenericEnv is a generic function's (or type's) parameter environment: each name
// maps to the abstract type it denotes — a *types.Param for a type parameter or a
// *types.ValParam for a value parameter '[N: int]' — in declaration order.
type GenericEnv struct {
	Params map[string]Type
	Names  []string
}

// FuncSig is a resolved function signature. Generic is non-nil when the function
// has type or value parameters; Defaults[i] marks a parameter with a default.
type FuncSig struct {
	Name       string
	ParamNames []string
	Params     []Type
	Defaults   []bool
	Ret        Type // Nil when the function has no '-> type'
	Decl       *ast.FuncDecl
	Generic    *GenericEnv
	// Dyn is true when the function carries the '#[dyn]' decorator, asking the
	// backend to compile it to one shared witness-table body rather than
	// monomorphizing per type argument (Phase 1c §6).
	Dyn bool
	// TestLabel is the human-readable `module::surface` name of a `#[test]` function
	// (Phase 1i U1), set when the signature is collected into Info.Tests. It is empty
	// for a non-test function, so a normal build never reads it.
	TestLabel string
}

// Info is the result of a successful check, consumed by the emitter. The Phase 0
// fields (Funcs / ExprTypes / BindTypes) keep their historic names so the emitter
// reads them unchanged; the resolution side tables (Refs / Brackets / Patterns)
// are the Phase 1b additions that settle the provisional nodes without rewriting
// the AST (so 'zerg fmt' still round-trips).
type Info struct {
	Funcs     map[string]*FuncSig
	ExprTypes map[ast.Expr]Type
	BindTypes map[*ast.BindStmt]Type

	// resolution layer (Phase 1b)
	Refs     map[*ast.Ident]*Symbol       // each identifier's resolved symbol
	Brackets map[*ast.Bracket]BracketRes  // each '[ … ]' postfix: index vs type args
	Patterns map[*ast.NamePattern]NameRes // each bare-name pattern: variant vs binding

	// NsMembers records, for each namespace member access `ns.member` (the callee of a
	// namespace call, or a namespace value/const access), the merged top-level name it
	// resolved to — honoring a one-level `import pub` re-export (Phase 1g S2). mono and
	// emit read it to spell the C target, so a re-exported member reaches the right
	// module's definition. For a plain (non-re-exported) member it equals
	// NamespaceMemberName, so the emitted C is unchanged.
	NsMembers map[*ast.Field]string

	// spec/impl layer (Phase 1c, U1)
	Specs *SpecRegistry // collected specs, impls, and the per-type method namespaces

	// ConstOrder lists every module constant (a top-level `:=`) in dependency
	// (topological) order — a constant after every module constant its initializer
	// references (Phase 1g S3). The backend evaluates and emits constants in this
	// order so a forward reference reads its dependency's already-assigned value; the
	// checker also types them in this order so a forward reference infers the real
	// type. It is empty for a program with no module constant, which stays
	// byte-identical.
	ConstOrder []*ast.BindStmt

	// Tests lists every `#[test]` function's signature, in the flattened whole-program
	// order — entry-module tests (file order) first, then imported modules by canonical
	// name — so `zerg test` runs them deterministically (Phase 1i U1/U3). It is empty
	// for a program with no `#[test]`; a normal `zerg build` filters test functions out
	// before this pass, so it is empty there too.
	Tests []*FuncSig
}

// Check resolves and type-checks the file, returning the analysis info and any
// diagnostics. When diagnostics are non-empty the Info is still returned but must
// not be used for emission.
//
// It runs the name-resolution pass first (building the scope tree, enforcing the
// module-surface and 'const' shadow rules, and settling the provisional nodes),
// then the bidirectional type checker; both write into the one Info and their
// diagnostics are concatenated.
func Check(file *ast.File) (*Info, []diag.Diagnostic) {
	info := &Info{
		Funcs:     map[string]*FuncSig{},
		ExprTypes: map[ast.Expr]Type{},
		BindTypes: map[*ast.BindStmt]Type{},
		Refs:      map[*ast.Ident]*Symbol{},
		Brackets:  map[*ast.Bracket]BracketRes{},
		Patterns:  map[*ast.NamePattern]NameRes{},
		NsMembers: map[*ast.Field]string{},
	}

	r := &resolver{info: info}
	r.resolveFile(file)

	c := &checker{info: info, module: r.module, constEdges: r.constEdges, frozenLists: map[*symbol]int{}}
	c.collectTypes(file)
	// The spec/impl registry is built before signatures so a generic parameter's
	// spec bound ('[T: Ord]') can be resolved to the SpecDef it carries (U3): the
	// registry must exist when collectFuncs runs genericEnv.
	c.collectSpecsAndImpls(file)
	c.collectFuncs(file)
	c.checkModuleConsts(file)
	c.checkFuncs(file)

	return info, append(r.diags.Items(), c.diags.Items()...)
}

// symbol is one value binding in the checker's local type environment.
type symbol struct {
	typ     Type
	mutable bool
}

type checker struct {
	info       *Info
	diags      diag.List
	module     *Scope
	scopes     []map[string]*symbol
	curFn      *FuncSig
	typeParams map[string]Type // generic parameters visible in the current body
	curSelf    Type            // the 'This' type inside a spec or impl body
	curSpec    *types.SpecDef  // the spec whose signatures are being resolved (U3 assoc-type)
	curImpl    *types.ImplDef  // the impl whose signatures are being resolved (U3 assoc-type)
	loopDepth  int
	inUnsafe   bool            // inside an `unsafe fn` body or an `unsafe { }` block-expression (group 12)
	derived    []*ast.ImplDecl // '#[derive]'-synthesized impls, type-checked after the file (U5)

	// constEdges is the module-constant dependency graph the resolver recorded: an
	// edge a -> b when constant a's initializer references module constant b. The
	// checker topologically sorts it to type and order the constants (Phase 1g S3).
	constEdges map[*ast.BindStmt][]*ast.BindStmt

	// dead tracks each binding's liveness after `del` within the current function
	// body (GRAMMAR group 11, DESIGN-1d §5.1). A name absent from the map is live; a
	// `del` marks it deadRevoked on that path; a control-flow merge where a name is
	// dead on some paths but not all marks it deadInconsistent. A use or reassignment
	// of a non-live name is rejected, turning a would-be use-after-free into a clean
	// compile error.
	dead map[*symbol]liveness

	// frozenLists counts, per list-binding symbol, the number of enclosing `for x in
	// xs` loops iterating it: a list is frozen against structural change (append or
	// rebind) while it is being iterated, so an iterator can never be invalidated
	// (docs/collections.md). Editing an ELEMENT in place (`for mut x`) stays allowed.
	frozenLists map[*symbol]int
}

// liveness is a binding's post-del state on the current path: liveOK (the default,
// absent from the dead map), deadRevoked (a `del` ran on every path reaching here),
// or deadInconsistent (dead on some merged paths, live on others).
type liveness uint8

const (
	liveOK liveness = iota
	deadRevoked
	deadInconsistent
)

func (c *checker) errorf(span token.Span, format string, args ...any) {
	c.diags.Add(span, format, args...)
}

// --- pass 1a: collect user types ----------------------------------------------

// collectTypes fills the field and payload types of every declared struct and
// enum (the resolver created the empty TypeDefs during surface collection). It
// runs before signatures so a function may mention a struct declared later.
func (c *checker) collectTypes(file *ast.File) {
	for _, d := range file.Items {
		switch n := d.(type) {
		case *ast.StructDecl:
			c.fillStruct(n)
		case *ast.EnumDecl:
			c.fillEnum(n)
		}
	}
}

func (c *checker) fillStruct(n *ast.StructDecl) {
	sym := c.module.local(n.Name)
	if sym == nil || sym.TypeDef == nil || sym.TypeDef.Struct == nil {
		return
	}
	env := c.genericEnv(n.Generics)
	sym.TypeDef.Params = typeParamsOf(env)
	saved := c.typeParams
	c.typeParams = env.merged(saved)
	own := map[string]bool{}
	for _, f := range n.Fields {
		own[f.Name] = true
	}
	for _, f := range n.Fields {
		sym.TypeDef.Struct.Fields = append(sym.TypeDef.Struct.Fields, types.FieldDef{
			Name: f.Name, Type: c.resolveType(f.Type), Pub: f.Pub, HasDefault: f.Default != nil,
		})
		// FORK-A5: a field default is backfilled verbatim at the construct site, so it
		// must be a self-contained constant that references no field of this struct.
		if f.Default != nil {
			c.checkConstDefault(f.Default, own)
		}
	}
	c.typeParams = saved
}

// typeParamsOf returns a generic environment's type parameters in declaration
// order, backing a nominal type's TypeDef.Params (so a use site can bind them to
// its type arguments). Value parameters are not type parameters and are skipped.
func typeParamsOf(env *GenericEnv) []*types.Param {
	if env == nil {
		return nil
	}
	var out []*types.Param
	for _, name := range env.Names {
		if p, ok := env.Params[name].(*types.Param); ok {
			out = append(out, p)
		}
	}
	return out
}

func (c *checker) fillEnum(n *ast.EnumDecl) {
	sym := c.module.local(n.Name)
	if sym == nil || sym.TypeDef == nil || sym.TypeDef.Enum == nil {
		return
	}
	env := c.genericEnv(n.Generics)
	sym.TypeDef.Params = typeParamsOf(env)
	saved := c.typeParams
	c.typeParams = env.merged(saved)
	// C-style discriminants run continuously: an explicit `= n` sets the value and the
	// next unnannotated variant continues from n+1 (so `Red = 1; Green; Blue = 10` is
	// 1, 2, 10). Only a wholly fieldless enum (CStyle) carries discriminants; a variant
	// with a payload keeps its positional tag (Discr stays nil), so an enum with no
	// explicit values resolves to 0, 1, 2 — identical to the positional tags.
	var next int64
	cstyle := sym.TypeDef.Enum.CStyle
	for i, v := range n.Variants {
		if i >= len(sym.TypeDef.Enum.Variants) {
			break
		}
		vd := sym.TypeDef.Enum.Variants[i]
		vd.Payload = vd.Payload[:0]
		for _, p := range v.Payload {
			vd.Payload = append(vd.Payload, c.resolveType(p))
		}
		if cstyle {
			if v.Discr != nil {
				if kv, ok := c.foldConst(v.Discr.X); ok && kv.Kind == types.KInt {
					next = kv.I
				} else {
					c.errorf(v.Discr.Span(), "an enum discriminant must be a constant integer")
				}
			}
			d := next
			vd.Discr = &d
			next++
		} else if v.Discr != nil {
			// A payload-bearing enum keeps positional tags; an explicit discriminant on it
			// would be silently ignored, so reject it rather than dropping it.
			c.errorf(v.Discr.Span(), "an explicit discriminant is only allowed on a payload-free enum")
		}
	}
	c.typeParams = saved
}

// --- pass 1b: collect signatures ----------------------------------------------

func (c *checker) collectFuncs(file *ast.File) {
	c.collectFuncItems(file.Items, false)
}

// collectFuncItems registers each item's function signature, descending into a
// module-level `unsafe { }` group (GRAMMAR group 12, Phase 1h U2). A `fn` inside
// such a group is an UNSAFE fn — its whole body is an unsafe context and it is
// callable only from unsafe code — so it is marked `Unsafe` in place before its
// signature is built, which is what closes the gap where a group fn was neither
// registered, checked-as-unsafe, nor rejected when called from safe code.
func (c *checker) collectFuncItems(items []ast.Stmt, inUnsafe bool) {
	for _, d := range items {
		switch n := d.(type) {
		case *ast.FuncDecl:
			if inUnsafe {
				n.Unsafe = true
			}
			if _, dup := c.info.Funcs[n.Name]; dup {
				c.errorf(n.Span(), "function %q is already declared", n.Name)
				continue
			}
			sig := c.buildSig(n)
			c.info.Funcs[n.Name] = sig
			// A `#[test]` function is a compiler-owned decorator (like #[derive]/#[dyn]):
			// collect it into Info.Tests after validating its shape (Phase 1i U1). A normal
			// build never reaches here for a test function — the driver filters them out —
			// so this fires only under `zerg test`.
			if hasDecorator(n.Decorators, "test") {
				c.collectTest(n, sig)
			}
		case *ast.UnsafeGroup:
			c.collectFuncItems(n.Items, true)
		}
	}
}

// buildSig resolves a function's signature, with its generic parameters (if any)
// in scope so their names resolve inside parameter and return types.
func (c *checker) buildSig(fn *ast.FuncDecl) *FuncSig {
	sig := &FuncSig{Name: fn.Name, Ret: Nil, Decl: fn}
	sig.Generic = c.genericEnv(fn.Generics)
	sig.Dyn = c.checkDyn(fn, sig.Generic)
	saved := c.typeParams
	c.typeParams = sig.Generic.merged(saved)
	for _, p := range fn.Params {
		sig.ParamNames = append(sig.ParamNames, p.Name)
		sig.Params = append(sig.Params, c.resolveType(p.Type))
		sig.Defaults = append(sig.Defaults, p.Default != nil)
	}
	// FORK-A5: a default is backfilled verbatim at the call site by emit, so it must
	// be a self-contained constant that names none of this function's parameters
	// (referencing one would silently resolve to a same-named global) and calls no
	// function. Gate anything else cleanly before it can reach the backend.
	own := map[string]bool{}
	for _, p := range fn.Params {
		own[p.Name] = true
	}
	for _, p := range fn.Params {
		if p.Default != nil {
			c.checkConstDefault(p.Default, own)
		}
	}
	if fn.Ret != nil {
		sig.Ret = c.resolveType(fn.Ret)
	}
	c.typeParams = saved
	return sig
}

// checkConstDefault validates that a defaulted parameter's or field's default
// expression is a self-contained constant (FORK-A5): a literal, a reference to a
// module-level constant, or arithmetic/unary over those. `own` is the set of the
// declaration's own parameter/field names, which a default must not reference —
// emit backfills the expression verbatim at the call/construct site, where such a
// name would wrongly bind to a same-named global (a silent wrong value). Any
// function/method call is likewise disallowed (it need not be enqueued for
// monomorphization and would reach cc as bad C). A gated default records a clean
// diagnostic, stopping compilation before the backend runs. Returns whether the
// expression is an acceptable constant.
func (c *checker) checkConstDefault(e ast.Expr, own map[string]bool) bool {
	const msg = "a default value must be a constant expression that does not reference a parameter/field"
	switch x := e.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.StrLit,
		*ast.RawStrLit, *ast.NilLit, *ast.RuneLit, *ast.ByteLit:
		return true
	case *ast.Ident:
		if own[x.Name] {
			c.errorf(x.Span(), msg)
			return false
		}
		// Only a module-level binding (an immutable module constant or a plain
		// module `:=`) names storage that survives to the call site unchanged; a
		// parameter/field or any other unresolved name does not.
		if sym := c.module.lookup(x.Name); sym == nil || (sym.Kind != SymConst && sym.Kind != SymVar) {
			c.errorf(x.Span(), msg)
			return false
		}
		return true
	case *ast.Unary:
		return c.checkConstDefault(x.X, own)
	case *ast.Binary:
		if !c.checkConstDefault(x.L, own) {
			return false
		}
		return c.checkConstDefault(x.R, own)
	default:
		c.errorf(e.Span(), msg)
		return false
	}
}

// checkDyn reports whether a function carries the '#[dyn]' decorator and enforces
// its one rule (DESIGN-1c §6.3): '#[dyn]' cannot erase a value (const) generic,
// since a compile-time value has no witness-table slot. A '#[dyn]' over a
// parameter list holding any '[N: int]' value parameter is a compile-time error.
func (c *checker) checkDyn(fn *ast.FuncDecl, env *GenericEnv) bool {
	if !hasDecorator(fn.Decorators, "dyn") {
		return false
	}
	if env != nil {
		for _, name := range env.Names {
			if _, isVal := env.Params[name].(*types.ValParam); isVal {
				c.errorf(fn.Span(), "#[dyn] cannot erase value generic %q: a compile-time value has no witness-table slot", name)
			}
		}
	}
	return true
}

// hasDecorator reports whether a decorator run contains an item of the given name.
func hasDecorator(decos []*ast.Decorator, name string) bool {
	for _, deco := range decos {
		for _, item := range deco.Items {
			if item.Name == name {
				return true
			}
		}
	}
	return false
}

// genericEnv builds the abstract parameter environment for a generic list. A
// single concrete-type bound ('[N: int]') marks a value parameter; a bare or
// spec-bounded parameter ('[T]', '[T: Ord]') is a type parameter (DESIGN-1b §3.4).
func (c *checker) genericEnv(g *ast.Generics) *GenericEnv {
	if g == nil || len(g.Params) == 0 {
		return nil
	}
	env := &GenericEnv{Params: map[string]Type{}}
	for _, tp := range g.Params {
		if of, ok := c.valueParamType(tp.Bound); ok {
			env.Params[tp.Name] = &types.ValParam{Name: tp.Name, Of: of}
		} else {
			env.Params[tp.Name] = &types.Param{Name: tp.Name, Bounds: c.paramBounds(tp.Bound)}
		}
		env.Names = append(env.Names, tp.Name)
	}
	return env
}

// paramBounds resolves a type parameter's spec bound '[T: Ord + Hash]' to the
// SpecDefs it names, each closed over its super-specs so 'T: Ord' carries Eq too
// (U3, DESIGN-1c §3). An unknown name — or a name the registry does not yet hold —
// contributes nothing; bound-name validity is not this pass's concern.
func (c *checker) paramBounds(b *ast.Bound) []*types.SpecDef {
	if b == nil || c.info.Specs == nil {
		return nil
	}
	var out []*types.SpecDef
	seen := map[*types.SpecDef]bool{}
	for _, name := range b.Names {
		if sp := c.info.Specs.Specs[name]; sp != nil {
			out = closeSpecInto(out, sp, seen)
		}
	}
	return out
}

// valueParamType reports whether a generic bound names a single concrete type
// (making the parameter a value parameter) and returns that type.
func (c *checker) valueParamType(b *ast.Bound) (Type, bool) {
	if b == nil || len(b.Names) != 1 {
		return nil, false
	}
	if p := primitiveNamed(b.Names[0]); p != nil {
		return p, true
	}
	return nil, false
}

// merged returns this environment's parameters overlaid on an outer scope's, so a
// nested generic sees both. A nil environment returns the outer map unchanged.
func (e *GenericEnv) merged(outer map[string]Type) map[string]Type {
	if e == nil {
		return outer
	}
	out := make(map[string]Type, len(outer)+len(e.Params))
	for k, v := range outer {
		out[k] = v
	}
	for k, v := range e.Params {
		out[k] = v
	}
	return out
}

// --- pass 2: check bodies -----------------------------------------------------

func (c *checker) checkFuncs(file *ast.File) {
	for _, d := range file.Items {
		switch n := d.(type) {
		case *ast.FuncDecl:
			c.checkFunc(n, nil)
		case *ast.ImplDecl:
			c.checkImpl(n)
		case *ast.SpecDecl:
			c.checkSpecDefaults(n)
		case *ast.InitDecl:
			c.checkInit(n)
		case *ast.UnsafeGroup:
			// Descend into a module-level unsafe group: its `fn`s (marked Unsafe in
			// collectFuncItems) are type-checked as unsafe contexts, and its `init()`
			// blocks are checked like any other. A `mut` binding is a mutable global,
			// already typed by checkModuleConsts.
			for _, sub := range n.Items {
				switch it := sub.(type) {
				case *ast.FuncDecl:
					c.checkFunc(it, nil)
				case *ast.InitDecl:
					c.checkInit(it)
				}
			}
		}
	}
	// '#[derive]'-synthesized impls are not in the file, so their canonical method
	// bodies are type-checked here — recording the field/payload comparison types
	// the backend reads when it emits them (U5, DESIGN-1c §5).
	for _, n := range c.derived {
		c.checkImpl(n)
	}
}

// checkSpecDefaults type-checks a spec's PROVIDED default method bodies (A10). A
// default body is written against the abstract self 'This' bound by its own spec, so
// its sibling method calls (`this.other()`) resolve through that bound — the same way
// a bounded type parameter's method calls do. Recording these body types is what lets
// the '#[dyn]' witness path lower a default the impl does not override (mono
// substitutes This -> the concrete receiver). A required member (an *ast.FnSig with no
// body) is skipped.
func (c *checker) checkSpecDefaults(n *ast.SpecDecl) {
	if c.info.Specs == nil {
		return
	}
	def := c.info.Specs.Specs[n.Name]
	if def == nil {
		return
	}
	this := &types.Param{Name: "This", Bounds: []*types.SpecDef{def}}
	savedSelf, savedSpec := c.curSelf, c.curSpec
	c.curSelf, c.curSpec = this, def
	for _, m := range n.Members {
		if fn, ok := m.(*ast.FuncDecl); ok {
			c.checkFunc(fn, this)
		}
	}
	c.curSelf, c.curSpec = savedSelf, savedSpec
}

// checkImpl type-checks the method bodies of an impl, binding 'this' to the
// target type and 'This' to the same. Phase 1c checks spec-impl bodies too (the
// one body check 1c adds beyond 1b): each spec-impl method body is typed against
// its own signature with This = the target type (DESIGN-1c §1, the abstract
// mechanism 1b already uses for inherent-impl and free-fn bodies).
func (c *checker) checkImpl(n *ast.ImplDecl) {
	target := c.resolveType(n.Target)
	savedSelf, savedImpl := c.curSelf, c.curImpl
	c.curSelf = target
	c.curImpl = c.implFor(n) // so a method signature re-resolves its associated types
	for _, item := range n.Items {
		if fn, ok := item.(*ast.FuncDecl); ok {
			c.checkFunc(fn, target)
		}
	}
	c.curSelf, c.curImpl = savedSelf, savedImpl
}

// implFor returns the collected ImplDef for an impl declaration, or nil, matching
// on the originating AST node.
func (c *checker) implFor(n *ast.ImplDecl) *types.ImplDef {
	if c.info.Specs == nil {
		return nil
	}
	for _, im := range c.info.Specs.Impls {
		if im.Decl == n {
			return im
		}
	}
	return nil
}

// checkFunc type-checks a function (or, when recv is non-nil, a method) body. A
// method uses a locally built signature that is not registered globally, and
// binds 'this' to its receiver type.
func (c *checker) checkFunc(fn *ast.FuncDecl, recv Type) {
	sig := c.info.Funcs[fn.Name]
	if sig == nil || recv != nil {
		sig = c.buildSig(fn)
	}
	savedFn, savedTP := c.curFn, c.typeParams
	c.curFn = sig
	c.typeParams = sig.Generic.merged(savedTP)

	// The body of an `unsafe fn` is an unsafe context throughout (group 12), so a
	// foreign call inside it is legal without a nested `unsafe { }`.
	savedUnsafe := c.inUnsafe
	c.inUnsafe = c.inUnsafe || fn.Unsafe

	savedDead := c.dead
	c.dead = map[*symbol]liveness{} // per-body del flow-analysis, fresh per function

	c.pushScope() // parameter scope; the body opens a nested scope so 'mut n := n' can shadow
	if recv != nil {
		c.declare(fn.Span(), "this", recv, fn.Mut)
	}
	for i, p := range fn.Params {
		var pt Type = types.Unknown
		if i < len(sig.Params) {
			pt = sig.Params[i]
		}
		c.declare(p.Span(), p.Name, pt, false)
	}
	if fn.Body != nil {
		c.checkBlock(fn.Body)
	}
	c.popScope()

	c.dead = savedDead
	c.inUnsafe = savedUnsafe
	c.curFn, c.typeParams = savedFn, savedTP
}

func (c *checker) checkBlock(b *ast.Block) {
	c.pushScope()
	for _, s := range b.Stmts {
		c.checkStmt(s)
	}
	c.popScope()
}

// checkForIn types the iterate form `for mut? v in iter { body }` (GRAMMAR group 6).
// Two iterables are supported today: an integer RANGE (`a..b` / `a..=b`), whose element
// type is int, and a FIXED ARRAY `[T; N]`, whose element type is T. Any other iterable
// (a list/map/set, a string, or a bare value) has no lowering yet and is rejected with a
// clean diagnostic rather than silently accepted — its emit would loop forever. The loop
// variable is declared in a fresh scope for the body, and the body's end liveness joins
// the entry state because the loop may run zero times.
func (c *checker) checkForIn(n *ast.ForStmt) {
	elem := c.forInElem(n)
	incoming := c.snapshotDead()
	c.loopDepth++
	// Freeze the iterated list against structural change (append/rebind) for the loop
	// body, so an iterator is never invalidated (docs/collections.md). A `for mut x`
	// in-place element edit stays allowed; only append/rebind of the list are rejected.
	if frozen := c.iteratedListSym(n); frozen != nil {
		c.frozenLists[frozen]++
		defer func() { c.frozenLists[frozen]-- }()
	}
	c.pushScope()
	if n.Var != "" {
		c.declare(n.Body.Span(), n.Var, elem, n.Mut)
	}
	for _, s := range n.Body.Stmts {
		c.checkStmt(s)
	}
	c.popScope()
	c.loopDepth--
	c.mergeDead([]map[*symbol]liveness{incoming, c.dead})
}

// forInElem types a for-in iterable and returns its element type. A range yields int
// (its bounds must be int, and it must be bounded); a fixed array yields its element
// type. Any other iterable is not yet supported.
func (c *checker) forInElem(n *ast.ForStmt) Type {
	if rng, ok := n.Iter.(*ast.Range); ok {
		lo := c.synth(rng.Lo)
		if !bad(lo) && lo != Int {
			c.errorf(rng.Lo.Span(), "a for-in range bound must be int, got %s", lo)
		}
		if rng.Hi == nil {
			c.errorf(n.Iter.Span(), "an unbounded range cannot drive a for-in loop")
			return Invalid
		}
		hi := c.synth(rng.Hi)
		if !bad(hi) && hi != Int {
			c.errorf(rng.Hi.Span(), "a for-in range bound must be int, got %s", hi)
		}
		return Int
	}
	it := c.synth(n.Iter)
	if arr, ok := it.(*types.Array); ok {
		return arr.Elem
	}
	if lst, ok := it.(*types.List); ok {
		return lst.Elem
	}
	// `for k in m` binds the KEY, walking the map in insertion order (docs/collections.md;
	// FORK-MAP-ITER: the value is reached via `m[k]`).
	if mp, ok := it.(*types.Map); ok {
		return mp.Key
	}
	if !bad(it) {
		c.errorf(n.Iter.Span(), "for-in over %s is not yet supported", it)
	}
	return Invalid
}

// iteratedListSym returns the container-binding symbol a for-in iterates when the
// iterable is a bare name bound to a list or a map, so the loop can freeze it against
// structural change for its duration. It is nil for a range, an array, or a non-name
// container expression.
func (c *checker) iteratedListSym(n *ast.ForStmt) *symbol {
	id, ok := n.Iter.(*ast.Ident)
	if !ok {
		return nil
	}
	sym := c.lookup(id.Name)
	if sym == nil {
		return nil
	}
	switch sym.typ.(type) {
	case *types.List, *types.Map:
		return sym
	}
	return nil
}

func (c *checker) checkStmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.NopStmt:
		// nothing
	case *ast.BindStmt:
		c.checkBind(n)
	case *ast.Reassign:
		c.checkAssign(n)
	case *ast.PrintStmt:
		t := c.synth(n.Value)
		switch {
		case t != nil && t.Kind() == types.KUnknown:
			// An Unknown-typed operand (a str/container method or field result the stdlib
			// has not typed yet, GRAMMAR/FORK-4) has no concrete C value to print, so the
			// backend would silently emit nothing. Fail loud here rather than vanish.
			c.errorf(n.Span(), "cannot use a value of unknown type here "+
				"(a string/container method result is not yet typed)")
		case !bad(t) && !printable(t):
			c.errorf(n.Span(), "cannot print a value of type %s", t)
		}
	case *ast.ReturnStmt:
		c.checkReturn(n)
	case *ast.BreakStmt:
		if c.loopDepth == 0 {
			c.errorf(n.Span(), "break outside of a loop")
		}
		if n.Cond != nil {
			c.checkCond(n.Cond)
		}
	case *ast.ContinueStmt:
		if c.loopDepth == 0 {
			c.errorf(n.Span(), "continue outside of a loop")
		}
		if n.Cond != nil {
			c.checkCond(n.Cond)
		}
	case *ast.IfStmt:
		// Analyse each branch (and the implicit fall-through when there is no else)
		// from the same entry liveness, then merge: a name dead on every path stays
		// dead, dead on some but not all is inconsistent (DESIGN-1d §5.1).
		incoming := c.snapshotDead()
		var ends []map[*symbol]liveness
		for _, br := range n.Branches {
			c.dead = c.snapshotFrom(incoming)
			c.checkCond(br.Cond)
			c.checkBlock(br.Body)
			ends = append(ends, c.dead)
		}
		if n.Else != nil {
			c.dead = c.snapshotFrom(incoming)
			c.checkBlock(n.Else)
			ends = append(ends, c.dead)
		} else {
			ends = append(ends, incoming) // fall-through path: no branch ran
		}
		c.mergeDead(ends)
	case *ast.ForStmt:
		if n.Iter != nil {
			c.checkForIn(n)
			return
		}
		if n.Cond != nil {
			c.checkCond(n.Cond)
		}
		// The body may run zero times, so join its end liveness with the entry state:
		// a name revoked inside the loop is inconsistent after it (dead if it ran).
		incoming := c.snapshotDead()
		c.loopDepth++
		c.checkBlock(n.Body)
		c.loopDepth--
		c.mergeDead([]map[*symbol]liveness{incoming, c.dead})
	case *ast.ExprStmt:
		c.synth(n.X)
	case *ast.DelStmt:
		// 'del name' revokes a binding's access now (GRAMMAR group 11); for a Ref the
		// emitter drops a refcount. It marks the name dead on this path so a later use
		// is rejected (DESIGN-1d §5.1); revoking is idempotent.
		if sym := c.lookup(n.Name); sym != nil {
			c.revoke(sym)
		} else {
			c.errorf(n.Span(), "undefined name %q", n.Name)
		}
	case *ast.RaiseStmt:
		// 'raise e (from c)' diverges (never). With no stdlib Error spec yet, any
		// value is accepted as the error and its cause (FORK-4); the operands are
		// still synthesized so nested errors surface.
		c.synth(n.Value)
		if n.From != nil {
			c.synth(n.From)
		}
	case *ast.DeferStmt:
		// 'defer expr' (GRAMMAR group 11) runs expr at the enclosing block's exit on
		// every path. The operand is synthesized so its type overlay reaches the
		// emitter, which schedules it on the runtime cleanup stack.
		c.synth(n.Call)
	case *ast.SpawnStmt:
		// 'spawn f(args)' (GRAMMAR group 9) starts a fire-and-forget coroutine. The
		// call is synthesized so its callee and argument types reach the emitter, which
		// marshals the call into a coroutine entry; the spawn statement itself yields no
		// value.
		c.synth(n.Call)
	case *ast.SendStmt:
		// 'ch <- v' (GRAMMAR group 9) sends v on the channel ch and yields no value.
		// Sending on a receive-only channel is rejected (directional narrowing), and the
		// value must be assignable to the channel's element type.
		ct := c.synth(n.Chan)
		vt := c.synth(n.Value)
		ch, ok := ct.(*types.Chan)
		if !ok {
			if !bad(ct) {
				c.errorf(n.Chan.Span(), "send '<-' requires a channel, found %s", ct)
			}
			return
		}
		if ch.Dir == types.ChanRecv {
			c.errorf(n.Span(), "cannot send on a receive-only channel %s", ch)
		}
		if !bad(vt) && !c.assignable(ch.Elem, n.Value, vt) {
			c.errorf(n.Value.Span(), "cannot send a value of type %s on a channel of %s", vt, ch.Elem)
		}
	case *ast.SelectStmt:
		c.checkSelect(n)
	case *ast.WithStmt:
		c.checkWith(n)
	}
}

// checkSelect type-checks a 'select { arm+ }' (GRAMMAR group 9), the one multi-way wait.
// It runs for effect (it yields no value). Each arm is checked in its own scope: a recv
// arm's channel must be a receivable channel and its '(id :=)' bind takes Result[T]
// (Left = a received value, Right = the channel closed) like a bare '<-ch'; a send arm
// type-checks 'ch <- v' as a send statement does; the 'done' and '_' arms carry only a
// body. Both 'done' and '_' are contextual — special only as an arm head here.
func (c *checker) checkSelect(n *ast.SelectStmt) {
	for i := range n.Arms {
		arm := &n.Arms[i]
		c.pushScope()
		switch arm.Kind {
		case ast.SelectRecv:
			ct := c.synth(arm.Chan)
			if ch, ok := ct.(*types.Chan); ok {
				if ch.Dir == types.ChanSend {
					c.errorf(arm.Chan.Span(), "cannot receive from a send-only channel %s", ch)
				}
				if arm.HasBind && arm.Bind != "" && arm.Bind != "_" {
					c.declare(arm.Span(), arm.Bind, resultType(ch.Elem), false)
				}
			} else if !bad(ct) {
				c.errorf(arm.Chan.Span(), "receive '<-' requires a channel, found %s", ct)
			}
		case ast.SelectSend:
			ct := c.synth(arm.Chan)
			vt := c.synth(arm.Value)
			if ch, ok := ct.(*types.Chan); ok {
				if ch.Dir == types.ChanRecv {
					c.errorf(arm.Chan.Span(), "cannot send on a receive-only channel %s", ch)
				}
				if !bad(vt) && !c.assignable(ch.Elem, arm.Value, vt) {
					c.errorf(arm.Value.Span(), "cannot send a value of type %s on a channel of %s", vt, ch.Elem)
				}
			} else if !bad(ct) {
				c.errorf(arm.Chan.Span(), "send '<-' requires a channel, found %s", ct)
			}
		case ast.SelectDone, ast.SelectDefault:
		}
		// The arm body runs for effect. A '{ … }' block body is checked statement by
		// statement (synthExpr alone would treat a block as Unknown without descending),
		// so a recv arm's binding is usable inside it; any other expression is synthesized.
		if blk, ok := arm.Body.(*ast.Block); ok {
			c.synthBlock(blk)
		} else {
			c.synth(arm.Body)
		}
		c.popScope()
	}
}

// checkWith type-checks a 'with e (as y)? { body }' (GRAMMAR group 6). The resource
// expression is synthesized, an 'as y' binding takes the resource's type in a fresh
// scope, and the body is checked there — so the emitter can desugar it to a scoped
// binding whose Scoped teardown (a Ref's drop) runs on every exit.
func (c *checker) checkWith(n *ast.WithStmt) {
	rt := c.synth(n.Resource)
	c.pushScope()
	if n.Var != "" {
		c.declare(n.Body.Span(), n.Var, rt, false)
	}
	for _, s := range n.Body.Stmts {
		c.checkStmt(s)
	}
	c.popScope()
}

func (c *checker) checkCond(e ast.Expr) {
	if t := c.synth(e); !bad(t) && t != Bool {
		c.errorf(e.Span(), "condition must be bool, found %s", t)
	}
}

func (c *checker) checkReturn(n *ast.ReturnStmt) {
	if n.Cond != nil {
		c.checkCond(n.Cond)
	}
	want := Nil
	if c.curFn != nil {
		want = c.curFn.Ret
	}
	if n.Value == nil {
		if want != Nil {
			c.errorf(n.Span(), "return with no value in a function returning %s", want)
		}
		return
	}
	vt := c.check(n.Value, want)
	if want == Nil {
		c.errorf(n.Span(), "unexpected return value in a function returning nil")
		return
	}
	if !bad(vt) && !bad(want) && !c.assignable(want, n.Value, vt) {
		c.errorf(n.Span(), "cannot return %s from a function returning %s", vt, want)
	}
}

// The match expression, its exhaustiveness, and pattern binding live in match.go.

// --- scopes -------------------------------------------------------------------

func (c *checker) pushScope() { c.scopes = append(c.scopes, map[string]*symbol{}) }
func (c *checker) popScope()  { c.scopes = c.scopes[:len(c.scopes)-1] }

func (c *checker) declare(span token.Span, name string, typ Type, mutable bool) {
	scope := c.scopes[len(c.scopes)-1]
	if _, dup := scope[name]; dup {
		c.errorf(span, "%q is already declared in this scope", name)
		return
	}
	scope[name] = &symbol{typ: typ, mutable: mutable}
}

func (c *checker) lookup(name string) *symbol {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if s, ok := c.scopes[i][name]; ok {
			return s
		}
	}
	return nil
}

// --- del liveness (flow-consistent) -------------------------------------------

// checkLive rejects a use or reassignment of a binding that a `del` revoked on the
// path reaching here (DESIGN-1d §5.1). A name dead on every path is a hard
// use-after-del; a name dead on only some merged paths is an inconsistent use.
func (c *checker) checkLive(sym *symbol, span token.Span, name string) {
	switch c.dead[sym] {
	case deadRevoked:
		c.errorf(span, "%q is used after del", name)
	case deadInconsistent:
		c.errorf(span, "%q is used after del on some paths", name)
	}
}

// revoke marks a binding dead on the current path (a `del`). It is idempotent, so a
// symmetric `del` on both branches of an `if` merges cleanly to dead.
func (c *checker) revoke(sym *symbol) { c.dead[sym] = deadRevoked }

// snapshotDead copies the current liveness map, so a branch can be analysed from a
// shared entry state and its result merged.
func (c *checker) snapshotDead() map[*symbol]liveness { return c.snapshotFrom(c.dead) }

// snapshotFrom copies a liveness map, so each branch is analysed from a fresh copy
// of the shared entry state.
func (c *checker) snapshotFrom(m map[*symbol]liveness) map[*symbol]liveness {
	out := make(map[*symbol]liveness, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// mergeDead replaces the current liveness with the join of the given per-path end
// states: a binding dead on every path stays dead; live on every path stays live;
// dead on some but not all is inconsistent (a later use of it is rejected).
func (c *checker) mergeDead(ends []map[*symbol]liveness) {
	seen := map[*symbol]bool{}
	for _, m := range ends {
		for sym := range m {
			seen[sym] = true
		}
	}
	merged := map[*symbol]liveness{}
	for sym := range seen {
		allDead, anyDead := true, false
		for _, m := range ends {
			switch m[sym] {
			case deadRevoked:
				anyDead = true
			case deadInconsistent:
				anyDead, allDead = true, false
			default: // liveOK
				allDead = false
			}
		}
		switch {
		case allDead:
			merged[sym] = deadRevoked
		case anyDead:
			merged[sym] = deadInconsistent
		}
	}
	c.dead = merged
}

// --- operator classification --------------------------------------------------

func isArithOp(k token.Kind) bool {
	switch k {
	case token.Plus, token.Minus, token.Star, token.Slash, token.Percent,
		token.PlusMod, token.MinusMod, token.StarMod:
		return true
	}
	return false
}

func isBitOp(k token.Kind) bool {
	switch k {
	case token.Amp, token.Pipe, token.Caret, token.Shl, token.Shr:
		return true
	}
	return false
}

func isOrderOp(k token.Kind) bool {
	switch k {
	case token.Lt, token.Gt, token.Le, token.Ge:
		return true
	}
	return false
}

func isEqOp(k token.Kind) bool { return k == token.EqEq || k == token.Ne }

func isLogicOp(k token.Kind) bool { return k == token.And || k == token.Or }
