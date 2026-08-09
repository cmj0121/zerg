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

	// Lambdas records each NON-CAPTURING closure literal that is lifted to a top-level
	// function (docs/code/functions.md): the checker synthesizes a `FuncDecl` + `FuncSig` for
	// it (registered in Funcs under the lambda's name), and mono/emit treat it as an
	// ordinary function. A closure that captures is gated instead, so it never appears
	// here. Empty for a program with no closure literal, which stays byte-identical.
	Lambdas map[*ast.FnExpr]*Lambda

	// StrForIn is set when the program iterates a str (`for c in s`), which the backend
	// lowers over the runtime (zrt_str_runes + a temporary list). It pulls in the runtime
	// even when nothing else does. False for a program with no str iteration.
	StrForIn bool

	// FuncValues records each bare top-level function NAME used as a value (not called):
	// the identifier maps to the function's name. The backend generates one env-taking
	// thunk per named function so it fits the uniform closure value `{ code, env }`, and
	// spells the identifier as that closure literal. Empty for a program that takes no
	// function value.
	FuncValues map[*ast.Ident]string

	// NsFuncValues records each cross-module namespace member `ns.func` used as a VALUE
	// (not called): the member-access node maps to the resolved merged top-level name
	// (honoring a one-level `import pub` re-export). It is the Field analogue of
	// FuncValues — the backend emits the same env-taking thunk / closure value keyed on
	// that name, so `f := text.split; f(x)` calls it. Empty for a program that takes no
	// cross-module function value.
	NsFuncValues map[*ast.Field]string
}

// Lambda is a closure lifted to a top-level function. A capturing closure carries its
// captures (docs/code/functions.md: "a closure is a scope-owned struct whose fields are its
// captures"); the backend puts them in an environment the lifted function reads.
type Lambda struct {
	Name     string
	Decl     *ast.FuncDecl
	Captures []Capture // empty for a non-capturing closure
}

// Capture is one variable a closure captures by copy: its source name and type.
type Capture struct {
	Name string
	Type Type
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
		Funcs:        map[string]*FuncSig{},
		ExprTypes:    map[ast.Expr]Type{},
		BindTypes:    map[*ast.BindStmt]Type{},
		Refs:         map[*ast.Ident]*Symbol{},
		Brackets:     map[*ast.Bracket]BracketRes{},
		Patterns:     map[*ast.NamePattern]NameRes{},
		NsMembers:    map[*ast.Field]string{},
		Lambdas:      map[*ast.FnExpr]*Lambda{},
		FuncValues:   map[*ast.Ident]string{},
		NsFuncValues: map[*ast.Field]string{},
	}

	r := &resolver{info: info}
	r.resolveFile(file)

	c := &checker{info: info, module: r.module, constEdges: r.constEdges, frozenLists: map[*symbol]int{}}
	// Validate every declaration's decorators first (GRAMMAR group 7 is a fixed,
	// compiler-owned set): an unknown or not-yet-supported decorator is a clean error
	// rather than a silent no-op.
	c.checkDecorators(file)
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
	// byref marks a 'mut &x' parameter — a mutable reference to the CALLER's
	// variable rather than storage this body owns (GRAMMAR group 5). It is mutable
	// inside the callee, and the backend gives it pointer storage.
	byref bool
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

	// captureStack tracks the closures being checked so a reference to an ENCLOSING
	// local is recorded as a capture (docs/code/functions.md). Each frame remembers the
	// scope depth at the closure's entry; a lookup that resolves below that depth is a
	// capture of that closure. Empty outside any closure body.
	captureStack []*captureFrame
	// lambdaSeq numbers the lifted lambdas so each gets a distinct synthesized name.
	lambdaSeq int

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
	// (docs/code/collections.md). Editing an ELEMENT in place (`for mut x`) stays allowed.
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
		case *ast.TypeDecl:
			c.fillAlias(n)
		}
	}
	c.checkSizeCycles(file)
}

// checkSizeCycles refuses a struct that contains itself BY VALUE, however indirectly.
//
// Such a type has no size: `struct P { p: P }` is P plus a P plus a P. There was no check here
// at all, so the seed accepted it and handed cc a struct containing itself — and the shipping
// compiler, whose declaration sweep skipped a self-dependency, died of signal 11 on the same
// three lines, which is what sent anybody looking.
//
// Only a BY-VALUE field is an edge. A `list[P]` field holds a heap buffer, a `P?` holds a
// carrier, and an enum's self-recursive payload is auto-boxed: those are the ways to write a
// recursive shape, and none of them is a size that depends on itself.
func (c *checker) checkSizeCycles(file *ast.File) {
	state := map[string]sizeState{}
	for _, d := range file.Items {
		if n, ok := d.(*ast.StructDecl); ok {
			c.sizeWalk(n.Name, state, n)
		}
	}
}

// sizeState is where one declaration stands in the walk below.
type sizeState uint8

const (
	sizeUnvisited sizeState = iota
	sizeOnStack
	sizeSettled
)

// sizeWalk is the depth-first half, and it reports whether `name` is on the walk's own stack —
// which is what a cycle is. `blame` is the declaration the report hangs on: the one the walk
// started from, since a cycle has no first member and the reader is looking at theirs.
func (c *checker) sizeWalk(name string, state map[string]sizeState, blame *ast.StructDecl) bool {
	switch state[name] {
	case sizeOnStack:
		return true
	case sizeSettled:
		return false
	}
	// settled on EVERY exit, including the one that reports: a name is walked once, so a
	// diamond of by-value fields costs one visit rather than one per path into it.
	state[name] = sizeOnStack
	defer func() { state[name] = sizeSettled }()

	sym := c.module.local(name)
	if sym == nil || sym.TypeDef == nil || sym.TypeDef.Struct == nil {
		return false
	}
	for _, f := range sym.TypeDef.Struct.Fields {
		nm, ok := byValueStructName(f.Type)
		if !ok {
			continue
		}
		if c.sizeWalk(nm, state, blame) {
			c.errorf(blame.Span(), "%q is part of a cycle of by-value declarations — a type holding itself, however indirectly, has no size; hold it behind a `list`, or box it as a self-recursive enum payload", blame.Name)
			return false
		}
	}
	return false
}

// byValueStructName is the named struct a field holds DIRECTLY, if it holds one.
//
// A field spelled with a struct's NAME resolves to the STRUCT type, not to a *types.Named
// wrapping it — which a first attempt at this asked for, and so matched nothing at all.
// Anything reached through a list, a map, a carrier or a channel is behind an indirection and
// contributes no edge.
func byValueStructName(t types.Type) (string, bool) {
	st, ok := t.(*types.Struct)
	if !ok || st.Def == nil {
		return "", false
	}
	return st.Def.Name, true
}

// fillAlias resolves a strong typedef's underlying representation into its TypeDef
// (the resolver created the empty TypeDef during surface collection), so a use site
// can reach it through types.Underlying and a conversion knows the scalar it targets.
// A generic alias `type X[T] = …` is not yet supported and is gated at its use site,
// so it is skipped here.
func (c *checker) fillAlias(n *ast.TypeDecl) {
	if n.Generics != nil || n.Alias == nil {
		return
	}
	sym := c.module.local(n.Name)
	if sym == nil || sym.TypeDef == nil {
		return
	}
	sym.TypeDef.Alias = c.resolveType(n.Alias)
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
		// A `mut fn` is exactly the `mut &this` case (GRAMMAR group 5): the method
		// mutates its receiver IN PLACE. The backend still passes the receiver by value,
		// so the mutation would be made to the callee's copy and silently discarded at
		// the call site. Gate it loudly rather than emit that. (A `mut &` PARAMETER does
		// write back — that lowering is in emit_byref.go; only the receiver is missing,
		// because its dispatch also runs through the dyn/erased witness paths.)
		if fn.Mut {
			c.errorf(fn.Span(), "a `mut fn` method's receiver is not yet written back to the caller; take a `mut &` parameter instead")
		}
		c.declare(fn.Span(), "this", recv, fn.Mut)
	}
	for i, p := range fn.Params {
		var pt Type = types.Unknown
		if i < len(sig.Params) {
			pt = sig.Params[i]
		}
		// A parameter passes BY VALUE and is immutable; `mut &x` instead binds a
		// mutable reference to the caller's variable, so it IS assignable here and the
		// write is visible to the caller (GRAMMAR group 5).
		c.declare(p.Span(), p.Name, pt, p.Ref)
		if p.Ref {
			if sym := c.lookup(p.Name); sym != nil {
				sym.byref = true
			}
		}
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
	// body, so an iterator is never invalidated (docs/code/collections.md). A `for mut x`
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
	// `for k in m` binds the KEY, walking the map in insertion order (docs/code/collections.md;
	// FORK-MAP-ITER: the value is reached via `m[k]`).
	if mp, ok := it.(*types.Map); ok {
		return mp.Key
	}
	// `for v in ch` receives until the channel closes: the channel IS the iterator, which is
	// why the language needs no `yield` keyword and no generator type (docs/code/coroutine.md).
	// The loop variable binds the ELEMENT, not the `Result[T]` a bare `<-ch` yields — the
	// Right that ends the stream is what ends the loop, so it is never handed to the body.
	if ch, ok := it.(*types.Chan); ok {
		if ch.Dir == types.ChanSend {
			c.errorf(n.Iter.Span(), "cannot receive from a send-only channel %s", ch)
			return Invalid
		}
		return ch.Elem
	}
	// `for c in s` iterates a str as its code points (docs/code/collections.md, types.md): the
	// loop variable binds a rune. A str is not indexable, so this is the forward scan.
	if it == Str {
		c.info.StrForIn = true
		return types.Rune
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
			c.checkBranch(br)
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
		// A channel is no exception. `del ch` used to keep the name live, because it was the
		// only spelling for "I am done sending" — but that made `del` mean two different
		// things, and it left `ch <- v` after a `del ch` typechecking on a handle whose send
		// end was gone. Giving up the send end is `close(ch)` now, so `del` means one thing
		// again for every type.
		sym := c.lookup(n.Name)
		if sym == nil {
			c.errorf(n.Span(), "undefined name %q", n.Name)
		} else {
			c.revoke(sym)
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
// type-checks 'ch <- v' as a send statement does; the '_' arm carries only a body and is
// the only bare identifier an arm may open with. There is no terminal arm — 'for select'
// is what ends.
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
				// the arm binds the VALUE: an arm that fires has one by construction,
				// since a cleanly closed channel drops out of the wait and a crash close
				// raises. Neither reaches a body, so neither needs a carrier around it.
				if arm.HasBind && arm.Bind != "" && arm.Bind != "_" {
					c.declare(arm.Span(), arm.Bind, ch.Elem, false)
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
		case ast.SelectDefault:
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

// checkBranch type-checks one if/else-if branch (statement form). A plain head is a
// boolean condition; a binding head `if x := opt` binds x to the unwrapped optional
// element in a scope wrapping the body, which runs only when the optional is present
// (like Swift `if let`).
func (c *checker) checkBranch(br ast.IfBranch) {
	if br.Bind != "" {
		elem := c.ifBindElem(br)
		c.pushScope()
		c.declare(br.Body.Span(), br.Bind, elem, false)
		c.checkBlock(br.Body)
		c.popScope()
		return
	}
	c.checkCond(br.Cond)
	c.checkBlock(br.Body)
}

// ifBindElem types an `if x := e` binding head: its operand (carried in br.Cond) must be
// an optional 'T?' or a 'Result[T]', and the head binds T — the block runs only on the
// present (Some / Left) case. Both are "a value or nothing", and both carry it in the same
// tag-0 slot, which is what lets `if v := <-ch { … }` read a receive (docs/code/coroutine.md).
// A general 'Either[L, R]' is NOT one of these: its Right is a value in its own right, not
// an absence, so it goes through `match`. Anything else is a clean error.
func (c *checker) ifBindElem(br ast.IfBranch) Type {
	t := c.synth(br.Cond)
	if bad(t) {
		return t
	}
	switch x := t.(type) {
	case *types.Opt:
		return x.Elem
	case *types.Either:
		if isErr(x.Right) {
			return x.Left
		}
	}
	c.errorf(br.Cond.Span(), "an `if %s :=` binding head requires an optional or a Result value, found %s", br.Bind, t)
	return Invalid
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
			// A name found BELOW a closure's entry depth is a capture of that closure —
			// and of every closure nested outside it that also encloses this reference.
			for _, f := range c.captureStack {
				if i < f.boundary {
					f.captured[name] = s
				}
			}
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
