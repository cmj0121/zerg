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
	}

	r := &resolver{info: info}
	r.resolveFile(file)

	c := &checker{info: info, module: r.module}
	c.collectTypes(file)
	c.collectFuncs(file)
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
	curSelf    Type            // the 'This' type inside an impl body
	loopDepth  int
}

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
	saved := c.typeParams
	c.typeParams = c.genericEnv(n.Generics).merged(saved)
	for _, f := range n.Fields {
		sym.TypeDef.Struct.Fields = append(sym.TypeDef.Struct.Fields, types.FieldDef{
			Name: f.Name, Type: c.resolveType(f.Type), Pub: f.Pub, HasDefault: f.Default != nil,
		})
	}
	c.typeParams = saved
}

func (c *checker) fillEnum(n *ast.EnumDecl) {
	sym := c.module.local(n.Name)
	if sym == nil || sym.TypeDef == nil || sym.TypeDef.Enum == nil {
		return
	}
	saved := c.typeParams
	c.typeParams = c.genericEnv(n.Generics).merged(saved)
	for i, v := range n.Variants {
		if i >= len(sym.TypeDef.Enum.Variants) {
			break
		}
		vd := sym.TypeDef.Enum.Variants[i]
		vd.Payload = vd.Payload[:0]
		for _, p := range v.Payload {
			vd.Payload = append(vd.Payload, c.resolveType(p))
		}
	}
	c.typeParams = saved
}

// --- pass 1b: collect signatures ----------------------------------------------

func (c *checker) collectFuncs(file *ast.File) {
	for _, d := range file.Items {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if _, dup := c.info.Funcs[fn.Name]; dup {
			c.errorf(fn.Span(), "function %q is already declared", fn.Name)
			continue
		}
		c.info.Funcs[fn.Name] = c.buildSig(fn)
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
	if fn.Ret != nil {
		sig.Ret = c.resolveType(fn.Ret)
	}
	c.typeParams = saved
	return sig
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
			env.Params[tp.Name] = &types.Param{Name: tp.Name}
		}
		env.Names = append(env.Names, tp.Name)
	}
	return env
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
		}
	}
}

// checkImpl type-checks the method bodies of an inherent impl, binding 'this' to
// the target type and 'This' to the same. Spec impls need program-wide coherence
// (1c), so their bodies are not typed here.
func (c *checker) checkImpl(n *ast.ImplDecl) {
	if n.Spec != nil {
		return
	}
	target := c.resolveType(n.Target)
	saved := c.curSelf
	c.curSelf = target
	for _, item := range n.Items {
		if fn, ok := item.(*ast.FuncDecl); ok {
			c.checkFunc(fn, target)
		}
	}
	c.curSelf = saved
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

	c.curFn, c.typeParams = savedFn, savedTP
}

func (c *checker) checkBlock(b *ast.Block) {
	c.pushScope()
	for _, s := range b.Stmts {
		c.checkStmt(s)
	}
	c.popScope()
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
		if !bad(t) && !printable(t) {
			c.errorf(n.Span(), "cannot print a value of type %s", t)
		}
	case *ast.ReturnStmt:
		c.checkReturn(n)
	case *ast.BreakStmt:
		if c.loopDepth == 0 {
			c.errorf(n.Span(), "break outside of a loop")
		}
	case *ast.ContinueStmt:
		if c.loopDepth == 0 {
			c.errorf(n.Span(), "continue outside of a loop")
		}
	case *ast.IfStmt:
		for _, br := range n.Branches {
			c.checkCond(br.Cond)
			c.checkBlock(br.Body)
		}
		if n.Else != nil {
			c.checkBlock(n.Else)
		}
	case *ast.ForStmt:
		if n.Cond != nil {
			c.checkCond(n.Cond)
		}
		c.loopDepth++
		c.checkBlock(n.Body)
		c.loopDepth--
	case *ast.ExprStmt:
		c.synth(n.X)
	case *ast.RaiseStmt:
		// 'raise e (from c)' diverges (never). With no stdlib Error spec yet, any
		// value is accepted as the error and its cause (FORK-4); the operands are
		// still synthesized so nested errors surface.
		c.synth(n.Value)
		if n.From != nil {
			c.synth(n.From)
		}
	}
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
