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
	// NameBinding is a fresh binding: the name matched no variant in scope.
	NameBinding NameResKind = iota
	// NameVariant is a nullary variant pattern: the name resolved to a variant.
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
	// Variants live in the value namespace: a callable constructor and a name
	// usable in pattern position. A nullary variant settles a bare NamePattern.
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
			r.errorf(spec.Span(), "import %q collides with the existing name %q", spec.Path, name)
			continue
		}
		r.module.declareLocal(&Symbol{Name: name, Kind: SymNamespace, Pub: spec.Pub, Span: spec.Span(), Decl: spec})
	}
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
		Name: n.Name, Kind: kind, Mutable: n.Mut, Const: n.Const,
		Span: n.Span(), Decl: n, Type: types.Unknown,
	})
}

// declareSurface inserts a top-level symbol, ignoring a duplicate (the Phase 0
// checker reports duplicate functions; other duplicates are future work).
func (r *resolver) declareSurface(sym *Symbol) { r.module.declareLocal(sym) }

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
		// top-level name is already visible
		if n.Value != nil {
			r.resolveExpr(n.Value)
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
	case *ast.Try:
		r.resolveExpr(n.X)
	case *ast.Force:
		r.resolveExpr(n.X)
	case *ast.OptChain:
		r.resolveExpr(n.X)
	case *ast.Recv:
		r.resolveExpr(n.X)
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

// resolveBracket settles a provisional '[ … ]' postfix into an index or a
// type-argument list (DESIGN-1b §2.2, GRAMMAR group 4): a comma is unambiguously
// type arguments; otherwise the base's binding decides — a generic function or a
// type constructor takes type arguments, any value is indexed.
func (r *resolver) resolveBracket(n *ast.Bracket) {
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

// resolveNamePattern settles a bare-name pattern (DESIGN-1b §2.3, GRAMMAR group
// 6): a name that resolves to a variant in scope is a nullary variant pattern;
// otherwise it is a fresh binding declared in the arm scope.
func (r *resolver) resolveNamePattern(pat *ast.NamePattern) {
	if sym := r.scope.lookup(pat.Name); sym != nil && sym.Kind == SymVariant {
		r.info.Patterns[pat] = NameRes{Kind: NameVariant, Variant: sym.Variant}
		return
	}
	sym := &Symbol{Name: pat.Name, Kind: SymVar, Span: pat.Span(), Type: types.Unknown}
	r.declareBinding(sym)
	r.info.Patterns[pat] = NameRes{Kind: NameBinding, Sym: sym}
}
