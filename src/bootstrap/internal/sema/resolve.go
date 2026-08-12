package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// BracketKind tags how a provisional '[ … ]' postfix resolved.
type BracketKind int

const (
	// BracketIndex is a value subscript 'a[i]': the base resolved to a value.
	BracketIndex BracketKind = iota
	// BracketTypeArg is a type-argument list 'f[K, V]' / 'list[int]': the base
	// resolved to a generic function or a type constructor, or a comma made it
	// unambiguously type arguments.
	BracketTypeArg
)

// BracketRes records the resolution of an ast.Bracket. Elem and Args carry the
// result types once the checker fills them; the resolve pass sets only Kind (the
// type fields stay Unknown this iteration).
type BracketRes struct {
	Kind BracketKind
	Elem types.Type   // BracketIndex: the element type after indexing
	Args []types.Type // BracketTypeArg: the resolved type arguments
}

// NameResKind tags how a provisional bare-name pattern resolved.
type NameResKind int

const (
	// NameBinding is a fresh binding, which every BARE name in pattern position is.
	NameBinding NameResKind = iota
	// NameVariant is a nullary variant pattern: the name was QUALIFIED and its enum
	// declares it. A derived match's arms are registered as this directly (derive.go),
	// where the variant is known because the checker chose it rather than read it.
	NameVariant
)

// NameRes records the resolution of an ast.NamePattern.
type NameRes struct {
	Kind    NameResKind
	Variant *types.VariantDef // NameVariant: the matched variant
	Sym     *Symbol           // NameBinding: the newly bound symbol
}

// builtinTypeCtors are the built-in generic type constructors whose bare name in
// a bracket base position ('list[int]') marks type arguments rather than an index.
//
//nolint:gochecknoglobals // a fixed lookup set for the resolver.
var builtinTypeCtors = map[string]bool{
	"list": true, "map": true, "set": true, "chan": true, "ptr": true, "array": true,
}

// resolver is the Phase 1b name-resolution pass (DESIGN-1b §2). It builds the
// retained scope tree, declares symbols with the module-surface and 'const'
// shadow rules, resolves identifiers into Info.Refs, and settles the provisional
// nodes (ast.Bracket, ast.NamePattern) into Info's side tables — without
// rewriting the AST, so 'zerg fmt' still round-trips.
type resolver struct {
	info    *Info
	diags   diag.List
	module  *Scope
	scope   *Scope
	inUnsfe bool // inside a module-level 'unsafe { }' group (a mutable global is legal)

	// curConst is the module constant whose initializer is currently being resolved,
	// and constEdges is the dependency graph collected while resolving it: an edge
	// a -> b whenever a's initializer references module constant b (Phase 1g S3). The
	// checker topologically sorts this so a forward reference between module constants
	// is typed and evaluated in dependency order.
	curConst   *ast.BindStmt
	constEdges map[*ast.BindStmt][]*ast.BindStmt
}

// addConstEdge records that module constant `from`'s initializer references module
// constant `to`, de-duplicating repeated references.
func (r *resolver) addConstEdge(from, to *ast.BindStmt) {
	if r.constEdges == nil {
		r.constEdges = map[*ast.BindStmt][]*ast.BindStmt{}
	}
	for _, e := range r.constEdges[from] {
		if e == to {
			return
		}
	}
	r.constEdges[from] = append(r.constEdges[from], to)
}

func (r *resolver) errorf(span token.Span, format string, args ...any) {
	r.diags.Add(span, format, args...)
}

// resolveFile runs the two-stage pass: collect the module surface (so every
// top-level name is forward-visible), then resolve every body.
func (r *resolver) resolveFile(file *ast.File) {
	r.module = newScope(ScopeModule, nil)
	r.scope = r.module
	r.collectSurface(file.Items)
	r.resolveItems(file.Items)
}

// --- stage 1: module surface --------------------------------------------------

// collectSurface declares every top-level name into the module scope. All names
// are declared before any body is resolved, which gives the module surface its
// forward visibility (a function may call one declared later).
func (r *resolver) collectSurface(items []ast.Stmt) {
	for _, it := range items {
		r.collectItem(it, false)
	}
}

func (r *resolver) collectItem(it ast.Stmt, inUnsafe bool) {
	switch n := it.(type) {
	case *ast.FuncDecl:
		r.declareSurface(&Symbol{Name: n.Name, Kind: SymFunc, Pub: n.Pub, Span: n.Span(), Decl: n})
	case *ast.StructDecl:
		def := &types.TypeDef{Name: n.Name, Struct: &types.StructDef{}}
		r.declareSurface(&Symbol{Name: n.Name, Kind: SymType, Pub: n.Pub, Span: n.Span(), Decl: n, TypeDef: def})
	case *ast.EnumDecl:
		r.collectEnum(n)
	case *ast.TypeDecl:
		def := &types.TypeDef{Name: n.Name}
		r.declareSurface(&Symbol{Name: n.Name, Kind: SymType, Pub: n.Pub, Span: n.Span(), Decl: n, TypeDef: def})
	case *ast.SpecDecl:
		def := &types.TypeDef{Name: n.Name}
		r.declareSurface(&Symbol{Name: n.Name, Kind: SymType, Pub: n.Pub, Span: n.Span(), Decl: n, TypeDef: def})
	case *ast.ImportStmt:
		r.collectImport(n)
	case *ast.BindStmt:
		r.collectModuleBind(n, inUnsafe)
	case *ast.UnsafeGroup:
		for _, sub := range n.Items {
			r.collectItem(sub, true)
		}
	}
}

func (r *resolver) collectEnum(n *ast.EnumDecl) {
	enum := &types.EnumDef{CStyle: true}
	def := &types.TypeDef{Name: n.Name, Enum: enum}
	for _, v := range n.Variants {
		if len(v.Payload) != 0 {
			enum.CStyle = false
		}
	}
	r.declareSurface(&Symbol{Name: n.Name, Kind: SymType, Pub: n.Pub, Span: n.Span(), Decl: n, TypeDef: def})
	// Variants live in the value namespace: a callable constructor, and the name a
	// QUALIFIED pattern resolves through. A bare one in pattern position never reaches
	// this table — it binds (GRAMMAR#pattern).
	for _, v := range n.Variants {
		vd := &types.VariantDef{Name: v.Name}
		for range v.Payload {
			vd.Payload = append(vd.Payload, types.Unknown)
		}
		enum.Variants = append(enum.Variants, vd)
		// TypeDef back-references the owning enum so a variant used as a value
		// ('Red', 'Some(3)') can name its enum type (§3.6 C1).
		r.declareSurface(&Symbol{
			Name: v.Name, Kind: SymVariant, Pub: n.Pub, Span: v.Span(), Decl: v, Variant: vd, TypeDef: def,
		})
	}
}

// collectImport binds an import's namespace. The last path segment (or the 'as'
// alias) names the binding, which lives in the one value namespace, so colliding
// with any existing top-level name is an error (GRAMMAR group 10).
func (r *resolver) collectImport(n *ast.ImportStmt) {
	for _, spec := range n.Specs {
		name := importName(spec)
		if name == "" {
			continue
		}
		if prev := r.module.local(name); prev != nil {
			// The whole-program flatten (Phase 1g) merges every module's imports into one
			// unit, so a module imported from two places (a diamond, or the entry and a
			// dependency both importing it) yields two identical specs bound to the same
			// namespace: keep the first and ignore the duplicate. A genuinely different
			// module claiming a taken name is still a collision.
			if prev.Kind == SymNamespace && spec.Module != "" && prev.Module == spec.Module {
				continue
			}
			r.errorf(spec.Span(), "import %q collides with the existing name %q", spec.Path, name)
			continue
		}
		r.module.declareLocal(&Symbol{
			Name: name, Kind: SymNamespace, Pub: spec.Pub, Span: spec.Span(), Decl: spec,
			Module: spec.Module, Reexports: spec.Reexports,
		})
	}
}

// nsTag is the C-mangle tag a namespace member is keyed on: the module loader's
// canonical tag (Symbol.Module) when a loader pass set it, else the local binding
// name. The fallback keeps a file checked directly (no loader) resolving members
// under the local name, exactly as the pre-graph bundle did.
func nsTag(sym *Symbol, local string) string {
	if sym.Module != "" {
		return sym.Module
	}
	return local
}

// moduleMember is the top-level name a bundled stdlib module's public member takes
// after flattening: `<namespace>__<member>`.
func moduleMember(namespace, member string) string { return namespace + "__" + member }

// NamespaceMemberName is the merged top-level name of member reached through the
// namespace symbol sym (bound to the local name local): the canonical-module
// mangling `<tag>__member`. It lets a downstream pass (mono) key a cross-module
// member on the same name sema and the loader used, honoring the loader's
// canonical tag and the no-loader local-name fallback (nsTag).
func NamespaceMemberName(sym *Symbol, local, member string) string {
	return moduleMember(nsTag(sym, local), member)
}

// importName is the binding an import spec introduces: its 'as' alias, else the
// last '/'-separated segment of its path.
func importName(spec *ast.ImportSpec) string {
	if spec.Alias != "" {
		return spec.Alias
	}
	path := spec.Path
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			start = i + 1
		}
	}
	return path[start:]
}

// collectModuleBind declares a module-level binding and enforces the no-mutable-
// global rule: a top-level ':=' is an immutable module constant, so 'mut' is an
// error unless the binding sits in a module-level 'unsafe { }' group (GRAMMAR
// group 10/12).
func (r *resolver) collectModuleBind(n *ast.BindStmt, inUnsafe bool) {
	if n.Mut && !inUnsafe {
		r.errorf(n.Span(), "a module-level binding may not be 'mut'; a top-level ':=' is an immutable module constant")
	}
	kind := SymVar
	if n.Const {
		kind = SymConst
	}
	r.declareSurface(&Symbol{
		Name: n.Name, Kind: kind, Mutable: n.Mut, Const: n.Const, Pub: n.Pub,
		Span: n.Span(), Decl: n, Type: types.Unknown,
	})
}

// declareSurface inserts a top-level symbol and reports two collisions the top
// level cannot hold, in the order a reader meets them.
//
// The FIRST is a declaration taking the name an IMPORT already bound. GRAMMAR
// group 10 puts the bound name in the one value namespace, so colliding with a
// local name is an error — and collectImport catches that collision only when the
// IMPORT comes second. The other order was silent, so a 'fn text()' beside
// 'import "util/text"' coexisted with it and which of the two a 'text…' reached
// depended on whether the reader wrote a '(' or a '.'.
//
// The SECOND is a name the top level already holds under a DIFFERENT kind of
// declaration. The top level is one namespace. A type name is not merely beside
// the value names — 'struct User' is what makes 'User(…)' a call — so GRAMMAR's
// construction note states the rule outright: "The type name is SHARED with
// functions: a type and a function cannot share a name (a duplicate is an error
// — Zerg has no overloading)." A module constant shares it too, since every one
// of them mangles to 'zg_<name>'. Unchecked, 'struct Foo' beside 'fn Foo' built
// and ran here, and the shipping compiler emitted C that cc refused as
// "redefinition of 'zg_Foo' as different kind of symbol" — an error against
// generated code nobody wrote.
//
// SAME-KIND duplicates are left where they already are: two functions are
// collectFuncItems', and two types are the type pass'. This is the one question
// neither of them can ask, because neither sees the other's list.
func (r *resolver) declareSurface(sym *Symbol) {
	if prev := r.module.local(sym.Name); prev != nil && prev.Kind == SymNamespace && sym.Kind != SymNamespace {
		r.errorf(sym.Span, "%q is already the namespace an import bound; rename the import with 'as'", sym.Name)
	}
	prev := r.module.declareLocal(sym)
	if prev == nil {
		return
	}
	was, now := surfaceKind(prev.Kind), surfaceKind(sym.Kind)
	if was == "" || now == "" || was == now {
		return
	}
	r.errorf(sym.Span, "%q is declared twice — once as %s, once as %s; the top level is one namespace", sym.Name, was, now)
}

// surfaceKind words the kind of top-level declaration a symbol is, for the
// one-namespace rule, and answers "" for a symbol the rule does not reach.
//
// An enum VARIANT is the one deliberate omission: GRAMMAR builds a variant
// THROUGH its enum ('Shape.Circle(3.0)'), so "a variant name is therefore never a
// name on its own, which is what keeps two enums that both declare a 'Red' from
// competing for it". This resolver still binds one into the module scope as a
// Phase 0 convenience, and a rule reading that table has to say so or it would
// refuse two enums the language allows. An imported namespace is omitted for the
// opposite reason: collectImport already reports its own collisions, with a
// sentence about the import rather than about the declaration.
func surfaceKind(k SymKind) string {
	switch k {
	case SymType:
		return "a type"
	case SymFunc:
		return "a function"
	case SymVar, SymConst:
		return "a module constant"
	}
	return ""
}

// --- stage 2: bodies ----------------------------------------------------------

func (r *resolver) resolveItems(items []ast.Stmt) {
	for _, it := range items {
		r.resolveItem(it)
	}
}

func (r *resolver) resolveItem(it ast.Stmt) {
	switch n := it.(type) {
	case *ast.FuncDecl:
		r.resolveFunc(n)
	case *ast.BindStmt:
		// a module-level binding's value resolves in the module scope, where every
		// top-level name is already visible. Resolving it under curConst records the
		// constant's dependency edges (an ident that resolves to another module
		// constant) so the checker can order them (Phase 1g S3).
		if n.Value != nil {
			prev := r.curConst
			r.curConst = n
			r.resolveExpr(n.Value)
			r.curConst = prev
		}
	case *ast.UnsafeGroup:
		saved := r.inUnsfe
		r.inUnsfe = true
		for _, sub := range n.Items {
			r.resolveItem(sub)
		}
		r.inUnsfe = saved
	case *ast.InitDecl:
		if n.Body != nil {
			r.resolveBlock(n.Body, ScopeFunc)
		}
	case *ast.ImplDecl:
		r.resolveImpl(n)
	}
}

func (r *resolver) resolveFunc(fn *ast.FuncDecl) {
	r.push(ScopeFunc)
	for i := range fn.Params {
		p := &fn.Params[i]
		r.scope.declareLocal(&Symbol{
			Name: p.Name, Kind: SymVar, ByRef: p.Ref, Span: p.Span(), Decl: fn, Type: types.Unknown,
		})
	}
	if fn.Body != nil {
		r.resolveBlock(fn.Body, ScopeBlock)
	}
	r.pop()
}

func (r *resolver) resolveImpl(n *ast.ImplDecl) {
	r.push(ScopeImpl)
	for _, item := range n.Items {
		if fn, ok := item.(*ast.FuncDecl); ok {
			r.resolveFunc(fn)
		}
	}
	r.pop()
}

// --- scopes -------------------------------------------------------------------

func (r *resolver) push(kind ScopeKind) { r.scope = newScope(kind, r.scope) }
func (r *resolver) pop()                { r.scope = r.scope.Parent }

// declareBinding declares a local binding, enforcing the 'const' bidirectional
// shadow-proof rule (GRAMMAR group 4): a 'const' may not shadow a visible outer
// binding, and no binding may shadow a visible outer 'const'. A plain ':=' may
// still shadow a plain ':='.
func (r *resolver) declareBinding(sym *Symbol) {
	if sym.Const {
		if outer := lookupVisible(r.scope.Parent, sym.Name); outer != nil {
			r.errorf(sym.Span, "const %q may not shadow a visible binding", sym.Name)
		}
	}
	if outer := lookupVisible(r.scope.Parent, sym.Name); outer != nil && outer.Const {
		r.errorf(sym.Span, "%q may not shadow the const binding %q", sym.Name, outer.Name)
	}
	r.scope.declareLocal(sym) // a same-scope duplicate is left to the checker
}

// --- statements ---------------------------------------------------------------

func (r *resolver) resolveBlock(b *ast.Block, kind ScopeKind) {
	r.push(kind)
	for _, s := range b.Stmts {
		r.resolveStmt(s)
	}
	r.pop()
}

func (r *resolver) resolveStmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.BindStmt:
		if n.Value != nil {
			r.resolveExpr(n.Value) // RHS resolves before the name is in scope
		}
		if n.Target != nil {
			// a destructuring bind '(a, b) := e' / 'P{x, y} := e': declare every leaf name.
			r.declareBindTarget(n.Target, n.Mut, n.Const)
			return
		}
		kind := SymVar
		if n.Const {
			kind = SymConst
		}
		r.declareBinding(&Symbol{
			Name: n.Name, Kind: kind, Mutable: n.Mut, Const: n.Const,
			Span: n.Span(), Decl: n, Type: types.Unknown,
		})
	case *ast.Reassign:
		r.resolveExpr(n.Value)
		r.resolveAssignTarget(n.Target)
	case *ast.PrintStmt:
		r.resolveExpr(n.Value)
	case *ast.ReturnStmt:
		if n.Value != nil {
			r.resolveExpr(n.Value)
		}
		if n.Cond != nil {
			r.resolveExpr(n.Cond)
		}
	case *ast.BreakStmt:
		if n.Cond != nil {
			r.resolveExpr(n.Cond)
		}
	case *ast.ContinueStmt:
		if n.Cond != nil {
			r.resolveExpr(n.Cond)
		}
	case *ast.IfStmt:
		for _, br := range n.Branches {
			r.resolveBranch(br)
		}
		if n.Else != nil {
			r.resolveBlock(n.Else, ScopeBlock)
		}
	case *ast.ForStmt:
		r.resolveFor(n)
	case *ast.ExprStmt:
		r.resolveExpr(n.X)
	case *ast.SendStmt:
		r.resolveExpr(n.Chan)
		r.resolveExpr(n.Value)
	case *ast.SpawnStmt:
		r.resolveExpr(n.Call)
	case *ast.SelectStmt:
		r.resolveSelect(n)
	case *ast.DeferStmt:
		r.resolveExpr(n.Call)
	case *ast.RaiseStmt:
		r.resolveExpr(n.Value)
		if n.From != nil {
			r.resolveExpr(n.From)
		}
	case *ast.WithStmt:
		r.resolveWith(n)
	}
}

// resolveSelect resolves a 'select { arm+ }' (GRAMMAR group 9). Each arm gets its own
// scope: a recv arm's channel and body resolve there, with its '(id :=)' bind (unless
// the wildcard '_') declared so the body may read it; a send arm resolves its channel,
// value, and body; the '_' arm resolves only its body.
func (r *resolver) resolveSelect(n *ast.SelectStmt) {
	for i := range n.Arms {
		arm := &n.Arms[i]
		r.push(ScopeBlock)
		switch arm.Kind {
		case ast.SelectRecv:
			r.resolveExpr(arm.Chan)
			if arm.HasBind && arm.Bind != "" && arm.Bind != "_" {
				r.declareBinding(&Symbol{Name: arm.Bind, Kind: SymVar, Span: arm.Span(), Type: types.Unknown})
			}
		case ast.SelectSend:
			r.resolveExpr(arm.Chan)
			r.resolveExpr(arm.Value)
		case ast.SelectDefault:
		}
		r.resolveExpr(arm.Body)
		r.pop()
	}
}

// declareBindTarget declares every leaf name of a destructuring bind target as a
// fresh binding (GRAMMAR bind-target): a tuple/struct pattern's leaves are names or
// nested tuple/struct patterns, and a struct shorthand '{x}' binds the field name x.
// A bind-target leaf is a NAME and nothing else — there is no qualified form here to
// tell apart, so it needs none of resolveNamePattern's reading.
func (r *resolver) declareBindTarget(pat ast.Pattern, mut, konst bool) {
	kind := SymVar
	if konst {
		kind = SymConst
	}
	switch p := pat.(type) {
	case *ast.NamePattern:
		r.declareBinding(&Symbol{
			Name: p.Name, Kind: kind, Mutable: mut, Const: konst, Span: p.Span(), Type: types.Unknown,
		})
	case *ast.TuplePattern:
		for _, sub := range p.Elems {
			r.declareBindTarget(sub, mut, konst)
		}
	case *ast.StructPattern:
		for _, f := range p.Fields {
			if f.Pat != nil {
				r.declareBindTarget(f.Pat, mut, konst)
			} else {
				r.declareBinding(&Symbol{
					Name: f.Name, Kind: kind, Mutable: mut, Const: konst, Span: p.Span(), Type: types.Unknown,
				})
			}
		}
	default:
		r.errorf(pat.Span(), "a bind target must be a name, tuple, or struct pattern")
	}
}

func (r *resolver) resolveBranch(br ast.IfBranch) {
	r.push(ScopeBlock)
	if br.Cond != nil {
		r.resolveExpr(br.Cond)
	}
	if br.Bind != "" {
		r.declareBinding(&Symbol{Name: br.Bind, Kind: SymVar, Span: br.Body.Span(), Type: types.Unknown})
	}
	// the branch body shares this head scope so the head binding is visible
	for _, s := range br.Body.Stmts {
		r.resolveStmt(s)
	}
	r.pop()
}

func (r *resolver) resolveFor(n *ast.ForStmt) {
	if n.Cond != nil {
		r.resolveExpr(n.Cond)
	}
	if n.Iter != nil {
		r.resolveExpr(n.Iter)
	}
	r.push(ScopeBlock)
	if n.Var != "" {
		r.declareBinding(&Symbol{Name: n.Var, Kind: SymVar, Mutable: n.Mut, Span: n.Body.Span(), Type: types.Unknown})
	}
	for _, s := range n.Body.Stmts {
		r.resolveStmt(s)
	}
	r.pop()
}

func (r *resolver) resolveWith(n *ast.WithStmt) {
	r.resolveExpr(n.Resource)
	r.push(ScopeBlock)
	if n.Var != "" {
		r.declareBinding(&Symbol{Name: n.Var, Kind: SymVar, Span: n.Body.Span(), Type: types.Unknown})
	}
	for _, s := range n.Body.Stmts {
		r.resolveStmt(s)
	}
	r.pop()
}

func (r *resolver) resolveAssignTarget(t ast.AssignTarget) {
	switch tg := t.(type) {
	case *ast.LValueTarget:
		r.resolveExpr(tg.X)
	case *ast.TupleTarget:
		for _, e := range tg.Elems {
			r.resolveAssignTarget(e)
		}
	case *ast.StructTarget:
		for _, f := range tg.Fields {
			if f.Target != nil {
				r.resolveAssignTarget(f.Target)
			}
		}
	}
}

// --- expressions --------------------------------------------------------------

func (r *resolver) resolveExpr(e ast.Expr) {
	switch n := e.(type) {
	case *ast.Ident:
		if sym := r.scope.lookup(n.Name); sym != nil {
			r.info.Refs[n] = sym
			// While resolving a module constant's initializer, an ident that resolves to a
			// module constant (its declaring node is a top-level BindStmt) is a dependency
			// edge for the constant-initialization order. A self-reference is recorded too,
			// so `x := x + 1` is caught as a cycle rather than reading its own zero value.
			if r.curConst != nil {
				if dep, ok := sym.Decl.(*ast.BindStmt); ok {
					r.addConstEdge(r.curConst, dep)
				}
			}
		}
	case *ast.Unary:
		r.resolveExpr(n.X)
	case *ast.Binary:
		r.resolveExpr(n.L)
		r.resolveExpr(n.R)
	case *ast.Call:
		r.resolveExpr(n.Callee)
		for _, a := range n.Args {
			r.resolveExpr(a.Value)
		}
	case *ast.Bracket:
		r.resolveBracket(n)
	case *ast.Field:
		r.resolveExpr(n.X)
	case *ast.TupleIndex:
		r.resolveExpr(n.X)
	case *ast.Range:
		r.resolveExpr(n.Lo)
		if n.Hi != nil {
			r.resolveExpr(n.Hi)
		}
	case *ast.ListLit:
		for _, el := range n.Elems {
			r.resolveExpr(el)
		}
	case *ast.ListFill:
		r.resolveExpr(n.Value)
		r.resolveExpr(n.Count)
	case *ast.TupleLit:
		for _, el := range n.Elems {
			r.resolveExpr(el)
		}
	case *ast.MapLit:
		for _, en := range n.Entries {
			r.resolveExpr(en.Key)
			r.resolveExpr(en.Value)
		}
	case *ast.Coalesce:
		r.resolveExpr(n.X)
		r.resolveExpr(n.Y)
	case *ast.Diverge:
		if n.Value != nil {
			r.resolveExpr(n.Value)
		}
		if n.From != nil {
			r.resolveExpr(n.From)
		}
	case *ast.GuardExpr:
		r.resolveBlock(n.Body, ScopeBlock)
	case *ast.UnsafeExpr:
		r.resolveBlock(n.Body, ScopeBlock)
	case *ast.FStr:
		r.resolveFStrParts(n.Parts)
	case *ast.FCmd:
		r.resolveFStrParts(n.Parts)
	case *ast.Try:
		r.resolveExpr(n.X)
	case *ast.Force:
		r.resolveExpr(n.X)
	case *ast.OptChain:
		r.resolveExpr(n.X)
	case *ast.Recv:
		r.resolveExpr(n.X)
	case *ast.ChanNew:
		if n.Cap != nil {
			r.resolveExpr(n.Cap)
		}
	case *ast.IsExpr:
		r.resolveExpr(n.X)
	case *ast.MatchExpr:
		r.resolveMatch(n)
	case *ast.IfExpr:
		r.resolveIfExpr(n)
	case *ast.Block:
		r.resolveBlock(n, ScopeBlock)
	}
}

// resolveFStrParts resolves the hole expressions of an f-string or f-cmd (GRAMMAR
// group 5): each hole is an ordinary expression, so its identifiers must be entered
// into info.Refs like any other — without this a namespace member call in a hole
// (`f"{math.abs(x)}"`) never records `math` as a namespace and the backend
// miscompiles its callee. Text parts (Expr nil) carry no names.
func (r *resolver) resolveFStrParts(parts []ast.FStrPart) {
	for i := range parts {
		if parts[i].Expr != nil {
			r.resolveExpr(parts[i].Expr)
		}
	}
}

// resolveBracket settles a provisional '[ … ]' postfix into an index or a
// type-argument list (DESIGN-1b §2.2, GRAMMAR group 4): a comma is unambiguously
// type arguments; otherwise the base's binding decides — a generic function or a
// type constructor takes type arguments, any value is indexed.
// IsSizeofBuiltin reports whether name is one of the compile-time layout built-ins whose
// bracket holds a TYPE argument — `sizeof` / `alignof`. It is the single authority for
// that name set, shared by resolve, infer, mono, and emit so the four stages agree on
// which brackets are a type-arg built-in rather than an index.
func IsSizeofBuiltin(name string) bool {
	return name == "sizeof" || name == "alignof"
}

func (r *resolver) resolveBracket(n *ast.Bracket) {
	// sizeof[T] / alignof[T] are compile-time built-ins whose bracket holds a TYPE, not an
	// index — so neither the base name nor the type argument is resolved as a value (a user
	// may still shadow the name with an ordinary binding, which takes over).
	if id, ok := n.Base.(*ast.Ident); ok && IsSizeofBuiltin(id.Name) &&
		r.scope.lookup(id.Name) == nil {
		r.info.Brackets[n] = BracketRes{Kind: BracketTypeArg}
		// resolve the type argument (so a user type like Point links) but NOT the base name
		// (sizeof/alignof are built-ins, not symbols).
		for _, el := range n.Elems {
			r.resolveExpr(el)
		}
		return
	}
	r.resolveExpr(n.Base)
	kind := r.bracketByBase(n.Base)
	if n.Comma {
		kind = BracketTypeArg // a comma is unambiguously type arguments
	}
	r.info.Brackets[n] = BracketRes{Kind: kind}
	for _, el := range n.Elems {
		r.resolveExpr(el)
	}
}

// bracketByBase decides a comma-free bracket from what its base names.
func (r *resolver) bracketByBase(base ast.Expr) BracketKind {
	id, ok := base.(*ast.Ident)
	if !ok {
		return BracketIndex // a complex base is a value, so this is an index
	}
	if sym := r.scope.lookup(id.Name); sym != nil {
		switch sym.Kind {
		case SymFunc, SymType, SymTypeParam:
			return BracketTypeArg
		default:
			return BracketIndex
		}
	}
	if builtinTypeCtors[id.Name] {
		return BracketTypeArg // 'list[int]' etc. — a built-in type constructor
	}
	return BracketIndex
}

func (r *resolver) resolveIfExpr(n *ast.IfExpr) {
	for _, br := range n.Branches {
		r.resolveBranch(br)
	}
	r.resolveBlock(n.Else, ScopeBlock)
}

// --- match & patterns ---------------------------------------------------------

func (r *resolver) resolveMatch(n *ast.MatchExpr) {
	r.resolveExpr(n.Subject)
	for _, arm := range n.Arms {
		r.push(ScopeBlock)
		r.resolvePattern(arm.Pat)
		if arm.Guard != nil {
			r.resolveExpr(arm.Guard)
		}
		r.resolveExpr(arm.Body)
		r.pop()
	}
}

// resolvePattern settles the provisional bare-name pattern and declares any
// bindings a pattern introduces into the current arm scope.
func (r *resolver) resolvePattern(p ast.Pattern) {
	switch pat := p.(type) {
	case *ast.NamePattern:
		r.resolveNamePattern(pat)
	case *ast.LitPattern:
		r.resolveExpr(pat.Lit)
	case *ast.VariantPattern:
		for _, sub := range pat.Elems {
			r.resolvePattern(sub)
		}
	case *ast.StructPattern:
		for _, f := range pat.Fields {
			if f.Pat != nil {
				r.resolvePattern(f.Pat)
			}
		}
	case *ast.TuplePattern:
		for _, sub := range pat.Elems {
			r.resolvePattern(sub)
		}
	case *ast.ListPattern:
		for _, el := range pat.Elems {
			if el.Rest {
				if el.Name != "" {
					r.declareBinding(&Symbol{Name: el.Name, Kind: SymVar, Span: pat.Span(), Type: types.Unknown})
				}
			} else if el.Pat != nil {
				r.resolvePattern(el.Pat)
			}
		}
	case *ast.AsPattern:
		r.resolvePattern(pat.Inner)
		r.declareBinding(&Symbol{Name: pat.Name, Kind: SymVar, Span: pat.Span(), Type: types.Unknown})
	case *ast.OrPattern:
		for _, alt := range pat.Alts {
			r.resolvePattern(alt)
		}
	case *ast.RangeArm:
		r.resolveExpr(pat.Lo)
		if pat.Hi != nil {
			r.resolveExpr(pat.Hi)
		}
	}
}

// resolveNamePattern settles a name pattern (GRAMMAR#pattern). A QUALIFIED one —
// 'Color.Red' — names a nullary variant, and a BARE one is a fresh binding declared in
// the arm scope, always, whatever else the name means in scope.
//
// The bare case used to be looked up first, and a name that happened to match a variant
// became a variant pattern. That is the coupling GRAMMAR names and rejects: declaring a
// variant in one file silently changed what a pattern in another file matched, and two
// enums that each declare a 'Red' could not both have it. It also made the arm's meaning
// a naming convention in the shipping compiler, which read the same fork off the leading
// CAPITAL and refused a bare capitalized binding outright.
//
// Nothing is lost by the mistake this used to absorb: 'match c { Red => …  Green => … }'
// now binds on its first arm, so every arm below it is unreachable and checkUnreachable
// says so, which is a diagnostic rather than a silently different program.
func (r *resolver) resolveNamePattern(pat *ast.NamePattern) {
	if pat.Qualified {
		if sym := r.scope.lookup(pat.Name); sym != nil && sym.Kind == SymVariant {
			r.info.Patterns[pat] = NameRes{Kind: NameVariant, Variant: sym.Variant}
			return
		}
	}
	sym := &Symbol{Name: pat.Name, Kind: SymVar, Span: pat.Span(), Type: types.Unknown}
	r.declareBinding(sym)
	r.info.Patterns[pat] = NameRes{Kind: NameBinding, Sym: sym}
}
