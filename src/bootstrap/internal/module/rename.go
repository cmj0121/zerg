package module

import "github.com/cmj0121/zerg/src/bootstrap/internal/ast"

// renamer rewrites one imported module's top-level names — and every in-module
// reference to them — to their canonical-path-mangled spelling `<tag>__name`.
// The whole-program flatten (loader.flatten) merges every module's items into a
// single compilation unit for the existing sema/mono/emit; prefixing an imported
// module's surface by its canonical tag lets two modules share a member name
// without colliding and keeps a module-private item from leaking into the entry
// module's unqualified scope. The entry (main) module is never renamed, so a
// program with no cross-module import keeps its exact historic C names.
//
// Reference rewriting is shadow-aware for the cases the modules in this iteration
// exercise: a generic type parameter and a local value binding whose name
// coincides with a surface name are left alone. As an MVP boundary (mirroring the
// pre-graph bundle's "do not shadow your own surface" rule) the shadow set is a
// per-function over-approximation — every value name introduced anywhere in a
// function body shadows the surface throughout that function. Visibility gating
// (only `pub` items being reachable across the boundary) is the next slice; here
// every top-level item is moved so resolution works.
type renamer struct {
	tag     string          // the module's canonical C-mangle tag, e.g. "util_text"
	surface map[string]bool // the module's top-level value+type names
	shadow  map[string]bool // value names shadowed in the current function
	tparams map[string]bool // generic type-parameter names in scope
}

// rename mangles every top-level declaration name in the module's files and
// rewrites every reference to those names. It returns the module's items in file
// order, ready to append to the whole-program unit.
func (r *renamer) rename(files []*ast.File) []ast.Stmt {
	var out []ast.Stmt
	for _, f := range files {
		for _, it := range f.Items {
			r.renameItem(it)
			out = append(out, it)
		}
	}
	return out
}

// mangle is a surface name's merged spelling.
func (r *renamer) mangle(name string) string { return r.tag + "__" + name }

// value reports whether name refers to the module surface in value position (not
// shadowed by a local binding); it returns the mangled name to substitute.
func (r *renamer) value(name string) (string, bool) {
	if r.surface[name] && !r.shadow[name] {
		return r.mangle(name), true
	}
	return name, false
}

// collectSurface records every top-level declaration name of a module (the names
// that must be mangled and whose references must be rewritten).
func collectSurface(files []*ast.File) map[string]bool {
	s := map[string]bool{}
	var add func(items []ast.Stmt)
	add = func(items []ast.Stmt) {
		for _, it := range items {
			switch n := it.(type) {
			case *ast.FuncDecl:
				s[n.Name] = true
			case *ast.StructDecl:
				s[n.Name] = true
			case *ast.EnumDecl:
				s[n.Name] = true
				for _, v := range n.Variants {
					s[v.Name] = true
				}
			case *ast.TypeDecl:
				s[n.Name] = true
			case *ast.SpecDecl:
				s[n.Name] = true
			case *ast.BindStmt:
				s[n.Name] = true
			case *ast.UnsafeGroup:
				add(n.Items)
			}
		}
	}
	for _, f := range files {
		add(f.Items)
	}
	return s
}

// --- top-level items ----------------------------------------------------------

func (r *renamer) renameItem(it ast.Stmt) {
	switch n := it.(type) {
	case *ast.FuncDecl:
		n.Name = r.mangle(n.Name)
		n.Module = r.tag
		r.renameFunc(n)
	case *ast.StructDecl:
		n.Name = r.mangle(n.Name)
		r.withTParams(n.Generics, func() {
			for _, fld := range n.Fields {
				r.renameType(fld.Type)
				r.renameExpr(fld.Default)
			}
		})
	case *ast.EnumDecl:
		n.Name = r.mangle(n.Name)
		r.withTParams(n.Generics, func() {
			for _, v := range n.Variants {
				v.Name = r.mangle(v.Name)
				for _, p := range v.Payload {
					r.renameType(p)
				}
			}
		})
	case *ast.TypeDecl:
		n.Name = r.mangle(n.Name)
		r.withTParams(n.Generics, func() { r.renameType(n.Alias) })
	case *ast.SpecDecl:
		n.Name = r.mangle(n.Name)
		r.withTParams(n.Generics, func() { r.renameSpec(n) })
	case *ast.ImplDecl:
		r.withTParams(n.Generics, func() {
			r.renameType(n.Spec)
			r.renameType(n.Target)
			for _, item := range n.Items {
				r.renameImplItem(item)
			}
		})
	case *ast.InitDecl:
		r.renameBlockScoped(n.Body)
	case *ast.BindStmt:
		n.Name = r.mangle(n.Name)
		r.renameType(n.Type)
		r.renameExpr(n.Value)
	case *ast.UnsafeGroup:
		for _, sub := range n.Items {
			r.renameItem(sub)
		}
	}
}

// renameFunc rewrites a function signature and body under its own generic and
// value-binding shadow scope.
func (r *renamer) renameFunc(fn *ast.FuncDecl) {
	r.withTParams(fn.Generics, func() {
		savedShadow := r.shadow
		r.shadow = map[string]bool{}
		for k := range savedShadow {
			r.shadow[k] = true
		}
		for i := range fn.Params {
			p := &fn.Params[i]
			r.shadow[p.Name] = true
			r.renameType(p.Type)
		}
		if fn.Body != nil {
			collectLocals(fn.Body, r.shadow)
			for _, s := range fn.Body.Stmts {
				r.renameStmt(s)
			}
		}
		r.renameType(fn.Ret)
		r.shadow = savedShadow
	})
}

func (r *renamer) renameSpec(n *ast.SpecDecl) {
	for _, m := range n.Members {
		switch mm := m.(type) {
		case *ast.FuncDecl:
			r.renameFunc(mm)
		case *ast.FnSig:
			r.withTParams(mm.Generics, func() {
				for i := range mm.Params {
					r.renameType(mm.Params[i].Type)
				}
				r.renameType(mm.Ret)
			})
		case *ast.AssocType:
			// bound names are spec names, rare across a module boundary; leave as-is
		case *ast.AssocVal:
			r.renameType(mm.Type)
		}
	}
}

func (r *renamer) renameImplItem(item ast.ImplItem) {
	switch it := item.(type) {
	case *ast.FuncDecl:
		it.Module = r.tag
		r.renameFunc(it)
	case *ast.AssocBind:
		r.renameType(it.Type)
	case *ast.ValBind:
		if it.Value != nil {
			r.renameExpr(it.Value.X)
		}
	}
}

// --- statements ---------------------------------------------------------------

// renameBlockScoped rewrites a block that introduces its own shadow scope.
func (r *renamer) renameBlockScoped(b *ast.Block) {
	if b == nil {
		return
	}
	saved := r.shadow
	r.shadow = map[string]bool{}
	for k := range saved {
		r.shadow[k] = true
	}
	collectLocals(b, r.shadow)
	for _, s := range b.Stmts {
		r.renameStmt(s)
	}
	r.shadow = saved
}

func (r *renamer) renameStmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.BindStmt:
		r.renameType(n.Type)
		r.renameExpr(n.Value)
	case *ast.PrintStmt:
		r.renameExpr(n.Value)
	case *ast.ReturnStmt:
		r.renameExpr(n.Value)
		r.renameExpr(n.Cond)
	case *ast.BreakStmt:
		r.renameExpr(n.Cond)
	case *ast.ContinueStmt:
		r.renameExpr(n.Cond)
	case *ast.ExprStmt:
		r.renameExpr(n.X)
	case *ast.IfStmt:
		for i := range n.Branches {
			r.renameExpr(n.Branches[i].Cond)
			r.renameBlockScoped(n.Branches[i].Body)
		}
		r.renameBlockScoped(n.Else)
	case *ast.ForStmt:
		r.renameExpr(n.Cond)
		r.renameExpr(n.Iter)
		r.renameBlockScoped(n.Body)
	case *ast.Reassign:
		r.renameTarget(n.Target)
		r.renameExpr(n.Value)
	case *ast.SpawnStmt:
		r.renameExpr(n.Call)
	case *ast.SendStmt:
		r.renameExpr(n.Chan)
		r.renameExpr(n.Value)
	case *ast.SelectStmt:
		for i := range n.Arms {
			r.renameExpr(n.Arms[i].Chan)
			r.renameExpr(n.Arms[i].Value)
			r.renameExpr(n.Arms[i].Body)
		}
	case *ast.DeferStmt:
		r.renameExpr(n.Call)
	case *ast.WithStmt:
		r.renameExpr(n.Resource)
		r.renameBlockScoped(n.Body)
	case *ast.RaiseStmt:
		r.renameExpr(n.Value)
		r.renameExpr(n.From)
	case *ast.UnsafeGroup:
		for _, sub := range n.Items {
			r.renameItem(sub)
		}
	}
}

// --- expressions --------------------------------------------------------------

func (r *renamer) renameExpr(e ast.Expr) {
	switch n := e.(type) {
	case nil:
		return
	case *ast.Ident:
		if m, ok := r.value(n.Name); ok {
			n.Name = m
		}
	case *ast.Unary:
		r.renameExpr(n.X)
	case *ast.Binary:
		r.renameExpr(n.L)
		r.renameExpr(n.R)
	case *ast.Call:
		r.renameExpr(n.Callee)
		for i := range n.Args {
			r.renameExpr(n.Args[i].Value)
		}
	case *ast.Field:
		// The base may name the surface; the '.Name' member is never a surface name.
		r.renameExpr(n.X)
	case *ast.OptChain:
		r.renameExpr(n.X)
	case *ast.TupleIndex:
		r.renameExpr(n.X)
	case *ast.Bracket:
		r.renameExpr(n.Base)
		for _, el := range n.Elems {
			r.renameExpr(el)
		}
	case *ast.Range:
		r.renameExpr(n.Lo)
		r.renameExpr(n.Hi)
	case *ast.IsExpr:
		r.renameExpr(n.X)
		if r.surface[n.TypeName] && !r.tparams[n.TypeName] {
			n.TypeName = r.mangle(n.TypeName)
		}
	case *ast.Coalesce:
		r.renameExpr(n.X)
		r.renameExpr(n.Y)
	case *ast.Diverge:
		r.renameExpr(n.Value)
		r.renameExpr(n.From)
	case *ast.Try:
		r.renameExpr(n.X)
	case *ast.Force:
		r.renameExpr(n.X)
	case *ast.Recv:
		r.renameExpr(n.X)
	case *ast.TupleLit:
		for _, el := range n.Elems {
			r.renameExpr(el)
		}
	case *ast.ListLit:
		for _, el := range n.Elems {
			r.renameExpr(el)
		}
	case *ast.ListFill:
		r.renameExpr(n.Value)
		r.renameExpr(n.Count)
	case *ast.MapLit:
		for i := range n.Entries {
			r.renameExpr(n.Entries[i].Key)
			r.renameExpr(n.Entries[i].Value)
		}
	case *ast.FStr:
		r.renameParts(n.Parts)
	case *ast.FCmd:
		r.renameParts(n.Parts)
	case *ast.MatchExpr:
		r.renameExpr(n.Subject)
		for i := range n.Arms {
			r.renamePattern(n.Arms[i].Pat)
			r.renameExpr(n.Arms[i].Guard)
			r.renameExpr(n.Arms[i].Body)
		}
	case *ast.IfExpr:
		for i := range n.Branches {
			r.renameExpr(n.Branches[i].Cond)
			r.renameBlockScoped(n.Branches[i].Body)
		}
		r.renameBlockScoped(n.Else)
	case *ast.GuardExpr:
		r.renameBlockScoped(n.Body)
	case *ast.AsmExpr:
		for _, op := range n.Operands {
			r.renameExpr(op.Value)
		}
	case *ast.FnExpr:
		r.renameClosure(n)
	case *ast.ChanNew:
		r.renameType(n.Elem)
		r.renameExpr(n.Cap)
	case *ast.UnsafeExpr:
		r.renameBlockScoped(n.Body)
	case *ast.Block:
		r.renameBlockScoped(n)
	}
}

func (r *renamer) renameParts(parts []ast.FStrPart) {
	for i := range parts {
		r.renameExpr(parts[i].Expr)
	}
}

func (r *renamer) renameClosure(n *ast.FnExpr) {
	saved := r.shadow
	r.shadow = map[string]bool{}
	for k := range saved {
		r.shadow[k] = true
	}
	for i := range n.Params {
		r.shadow[n.Params[i].Name] = true
		r.renameType(n.Params[i].Type)
	}
	r.renameType(n.Ret)
	r.renameBlockScoped(n.Body)
	r.shadow = saved
}

// --- reassignment targets -----------------------------------------------------

func (r *renamer) renameTarget(t ast.AssignTarget) {
	switch n := t.(type) {
	case *ast.LValueTarget:
		r.renameExpr(n.X)
	case *ast.TupleTarget:
		for _, el := range n.Elems {
			r.renameTarget(el)
		}
	case *ast.StructTarget:
		if r.surface[n.TypeName] && !r.tparams[n.TypeName] {
			n.TypeName = r.mangle(n.TypeName)
		}
		for i := range n.Fields {
			r.renameTarget(n.Fields[i].Target)
		}
	}
}

// --- patterns -----------------------------------------------------------------

func (r *renamer) renamePattern(p ast.Pattern) {
	switch n := p.(type) {
	case *ast.LitPattern:
		r.renameExpr(n.Lit)
	case *ast.NamePattern:
		// A bare name that matches a nullary variant of the surface is that variant;
		// otherwise it is a fresh binding (left as-is, shadowing the surface).
		if r.surface[n.Name] {
			n.Name = r.mangle(n.Name)
		}
	case *ast.VariantPattern:
		if r.surface[n.Name] {
			n.Name = r.mangle(n.Name)
		}
		for _, sub := range n.Elems {
			r.renamePattern(sub)
		}
	case *ast.StructPattern:
		if r.surface[n.Name] {
			n.Name = r.mangle(n.Name)
		}
		for i := range n.Fields {
			r.renamePattern(n.Fields[i].Pat)
		}
	case *ast.TuplePattern:
		for _, sub := range n.Elems {
			r.renamePattern(sub)
		}
	case *ast.ListPattern:
		for i := range n.Elems {
			r.renamePattern(n.Elems[i].Pat)
		}
	case *ast.AsPattern:
		r.renamePattern(n.Inner)
	case *ast.OrPattern:
		for _, sub := range n.Alts {
			r.renamePattern(sub)
		}
	case *ast.RangeArm:
		r.renameExpr(n.Lo)
		r.renameExpr(n.Hi)
	}
}

// --- types --------------------------------------------------------------------

func (r *renamer) renameType(t ast.Type) {
	switch n := t.(type) {
	case nil:
		return
	case *ast.TypeRef:
		if len(n.Proj) == 0 && r.surface[n.Name] && !r.tparams[n.Name] {
			n.Name = r.mangle(n.Name)
		}
		for _, a := range n.Args {
			r.renameType(a)
		}
	case *ast.OptType:
		r.renameType(n.Elem)
	case *ast.TupleType:
		for _, el := range n.Elems {
			r.renameType(el)
		}
	case *ast.ArrayType:
		r.renameType(n.Elem)
		if n.Count != nil {
			r.renameExpr(n.Count.X)
		}
	case *ast.ChanType:
		r.renameType(n.Elem)
	case *ast.PtrType:
		r.renameType(n.Elem)
	case *ast.FnType:
		for i := range n.Params {
			r.renameType(n.Params[i].Type)
		}
		r.renameType(n.Ret)
	case *ast.ConstExpr:
		r.renameExpr(n.X)
	}
}

// --- scope helpers ------------------------------------------------------------

// withTParams runs body with the names in gen added to the type-parameter shadow
// set, restoring it afterward.
func (r *renamer) withTParams(gen *ast.Generics, body func()) {
	if gen == nil {
		body()
		return
	}
	saved := r.tparams
	r.tparams = map[string]bool{}
	for k := range saved {
		r.tparams[k] = true
	}
	for _, p := range gen.Params {
		r.tparams[p.Name] = true
	}
	body()
	r.tparams = saved
}

// collectLocals records every value name a block introduces (bindings, loop and
// closure variables, if-bind heads, select binds, and pattern binds) into set —
// the per-function shadow over-approximation.
func collectLocals(b *ast.Block, set map[string]bool) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		collectLocalsStmt(s, set)
	}
}

func collectLocalsStmt(s ast.Stmt, set map[string]bool) {
	switch n := s.(type) {
	case *ast.BindStmt:
		set[n.Name] = true
		collectLocalsExpr(n.Value, set)
	case *ast.ForStmt:
		if n.Var != "" {
			set[n.Var] = true
		}
		collectLocals(n.Body, set)
	case *ast.IfStmt:
		for i := range n.Branches {
			if n.Branches[i].Bind != "" {
				set[n.Branches[i].Bind] = true
			}
			collectLocals(n.Branches[i].Body, set)
		}
		collectLocals(n.Else, set)
	case *ast.SelectStmt:
		for i := range n.Arms {
			if n.Arms[i].HasBind && n.Arms[i].Bind != "" {
				set[n.Arms[i].Bind] = true
			}
		}
	case *ast.ExprStmt:
		collectLocalsExpr(n.X, set)
	case *ast.PrintStmt:
		collectLocalsExpr(n.Value, set)
	case *ast.ReturnStmt:
		collectLocalsExpr(n.Value, set)
	case *ast.WithStmt:
		if n.Var != "" {
			set[n.Var] = true
		}
		collectLocals(n.Body, set)
	}
}

// collectLocalsExpr descends into the block-bearing expressions so a binding made
// inside a match arm or nested block still shadows the surface for the function.
func collectLocalsExpr(e ast.Expr, set map[string]bool) {
	switch n := e.(type) {
	case *ast.Block:
		collectLocals(n, set)
	case *ast.UnsafeExpr:
		collectLocals(n.Body, set)
	case *ast.MatchExpr:
		for i := range n.Arms {
			collectPatternBinds(n.Arms[i].Pat, set)
			collectLocalsExpr(n.Arms[i].Body, set)
		}
	case *ast.IfExpr:
		for i := range n.Branches {
			if n.Branches[i].Bind != "" {
				set[n.Branches[i].Bind] = true
			}
			collectLocals(n.Branches[i].Body, set)
		}
		collectLocals(n.Else, set)
	case *ast.GuardExpr:
		collectLocals(n.Body, set)
	}
}

func collectPatternBinds(p ast.Pattern, set map[string]bool) {
	switch n := p.(type) {
	case *ast.NamePattern:
		set[n.Name] = true
	case *ast.VariantPattern:
		for _, sub := range n.Elems {
			collectPatternBinds(sub, set)
		}
	case *ast.StructPattern:
		for i := range n.Fields {
			collectPatternBinds(n.Fields[i].Pat, set)
		}
	case *ast.TuplePattern:
		for _, sub := range n.Elems {
			collectPatternBinds(sub, set)
		}
	case *ast.ListPattern:
		for i := range n.Elems {
			if n.Elems[i].Rest {
				if n.Elems[i].Name != "" {
					set[n.Elems[i].Name] = true
				}
				continue
			}
			collectPatternBinds(n.Elems[i].Pat, set)
		}
	case *ast.AsPattern:
		set[n.Name] = true
		collectPatternBinds(n.Inner, set)
	case *ast.OrPattern:
		for _, sub := range n.Alts {
			collectPatternBinds(sub, set)
		}
	}
}
