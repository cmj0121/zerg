package lint

import "github.com/cmj0121/zerg/src/bootstrap/internal/ast"

// collector records every name referenced in a value, type, or pattern position of
// the module — the set that decides which imports and declarations are used. It
// mirrors the whole-program renamer's traversal (internal/module) so it visits the
// same reference positions; the only occurrences it deliberately skips are the
// declaring names themselves (a FuncDecl.Name, a struct field name, a `.member`
// selector), which are definitions, not references.
type collector struct {
	used map[string]bool
}

// mark records name as referenced.
func (c *collector) mark(name string) {
	if name != "" {
		c.used[name] = true
	}
}

// --- top-level items ----------------------------------------------------------

func (c *collector) item(it ast.Stmt) {
	switch n := it.(type) {
	case *ast.FuncDecl:
		c.fn(n)
	case *ast.StructDecl:
		for _, fld := range n.Fields {
			c.typ(fld.Type)
			c.expr(fld.Default)
		}
	case *ast.EnumDecl:
		for _, v := range n.Variants {
			for _, p := range v.Payload {
				c.typ(p)
			}
		}
	case *ast.TypeDecl:
		c.typ(n.Alias)
	case *ast.SpecDecl:
		c.spec(n)
	case *ast.ImplDecl:
		c.typ(n.Spec)
		c.typ(n.Target)
		for _, item := range n.Items {
			c.implItem(item)
		}
	case *ast.InitDecl:
		c.block(n.Body)
	case *ast.BindStmt:
		c.typ(n.Type)
		c.expr(n.Value)
	case *ast.UnsafeGroup:
		for _, sub := range n.Items {
			c.item(sub)
		}
	}
}

func (c *collector) fn(fn *ast.FuncDecl) {
	for i := range fn.Params {
		c.typ(fn.Params[i].Type)
	}
	c.typ(fn.Ret)
	c.block(fn.Body)
}

func (c *collector) spec(n *ast.SpecDecl) {
	for _, m := range n.Members {
		switch mm := m.(type) {
		case *ast.FuncDecl:
			c.fn(mm)
		case *ast.FnSig:
			for i := range mm.Params {
				c.typ(mm.Params[i].Type)
			}
			c.typ(mm.Ret)
		case *ast.AssocVal:
			c.typ(mm.Type)
		}
	}
}

func (c *collector) implItem(item ast.ImplItem) {
	switch it := item.(type) {
	case *ast.FuncDecl:
		c.fn(it)
	case *ast.AssocBind:
		c.typ(it.Type)
	case *ast.ValBind:
		if it.Value != nil {
			c.expr(it.Value.X)
		}
	}
}

// --- statements ---------------------------------------------------------------

func (c *collector) block(b *ast.Block) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		c.stmt(s)
	}
}

func (c *collector) stmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.BindStmt:
		c.typ(n.Type)
		c.expr(n.Value)
	case *ast.PrintStmt:
		c.expr(n.Value)
	case *ast.ReturnStmt:
		c.expr(n.Value)
		c.expr(n.Cond)
	case *ast.BreakStmt:
		c.expr(n.Cond)
	case *ast.ContinueStmt:
		c.expr(n.Cond)
	case *ast.ExprStmt:
		c.expr(n.X)
	case *ast.IfStmt:
		for i := range n.Branches {
			c.expr(n.Branches[i].Cond)
			c.block(n.Branches[i].Body)
		}
		c.block(n.Else)
	case *ast.ForStmt:
		c.expr(n.Cond)
		c.expr(n.Iter)
		c.block(n.Body)
	case *ast.Reassign:
		c.target(n.Target)
		c.expr(n.Value)
	case *ast.SpawnStmt:
		c.expr(n.Call)
	case *ast.SendStmt:
		c.expr(n.Chan)
		c.expr(n.Value)
	case *ast.SelectStmt:
		for i := range n.Arms {
			c.expr(n.Arms[i].Chan)
			c.expr(n.Arms[i].Value)
			c.expr(n.Arms[i].Body)
		}
	case *ast.DeferStmt:
		c.expr(n.Call)
	case *ast.WithStmt:
		c.expr(n.Resource)
		c.block(n.Body)
	case *ast.RaiseStmt:
		c.expr(n.Value)
		c.expr(n.From)
	case *ast.UnsafeGroup:
		for _, sub := range n.Items {
			c.item(sub)
		}
	}
}

// --- expressions --------------------------------------------------------------

func (c *collector) expr(e ast.Expr) {
	switch n := e.(type) {
	case nil:
		return
	case *ast.Ident:
		c.mark(n.Name)
	case *ast.Unary:
		c.expr(n.X)
	case *ast.Binary:
		c.expr(n.L)
		c.expr(n.R)
	case *ast.Call:
		c.expr(n.Callee)
		for i := range n.Args {
			c.expr(n.Args[i].Value)
		}
	case *ast.Field:
		// The base may name an import or a surface value; the '.Name' member is a
		// selector, never a top-level name in its own right.
		c.expr(n.X)
	case *ast.OptChain:
		c.expr(n.X)
	case *ast.TupleIndex:
		c.expr(n.X)
	case *ast.Bracket:
		c.expr(n.Base)
		for _, el := range n.Elems {
			c.expr(el)
		}
	case *ast.Range:
		c.expr(n.Lo)
		c.expr(n.Hi)
	case *ast.IsExpr:
		c.expr(n.X)
		c.mark(n.TypeName)
	case *ast.Coalesce:
		c.expr(n.X)
		c.expr(n.Y)
	case *ast.Diverge:
		c.expr(n.Value)
		c.expr(n.From)
	case *ast.Try:
		c.expr(n.X)
	case *ast.Force:
		c.expr(n.X)
	case *ast.Recv:
		c.expr(n.X)
	case *ast.TupleLit:
		for _, el := range n.Elems {
			c.expr(el)
		}
	case *ast.ListLit:
		for _, el := range n.Elems {
			c.expr(el)
		}
	case *ast.ListFill:
		c.expr(n.Value)
		c.expr(n.Count)
	case *ast.MapLit:
		for i := range n.Entries {
			c.expr(n.Entries[i].Key)
			c.expr(n.Entries[i].Value)
		}
	case *ast.FStr:
		c.parts(n.Parts)
	case *ast.FCmd:
		c.parts(n.Parts)
	case *ast.MatchExpr:
		c.expr(n.Subject)
		for i := range n.Arms {
			c.pattern(n.Arms[i].Pat)
			c.expr(n.Arms[i].Guard)
			c.expr(n.Arms[i].Body)
		}
	case *ast.IfExpr:
		for i := range n.Branches {
			c.expr(n.Branches[i].Cond)
			c.block(n.Branches[i].Body)
		}
		c.block(n.Else)
	case *ast.GuardExpr:
		c.block(n.Body)
	case *ast.AsmExpr:
		for _, op := range n.Operands {
			c.expr(op.Value)
		}
	case *ast.FnExpr:
		for i := range n.Params {
			c.typ(n.Params[i].Type)
		}
		c.typ(n.Ret)
		c.block(n.Body)
	case *ast.ChanNew:
		c.typ(n.Elem)
		c.expr(n.Cap)
	case *ast.UnsafeExpr:
		c.block(n.Body)
	case *ast.Block:
		c.block(n)
	}
}

func (c *collector) parts(parts []ast.FStrPart) {
	for i := range parts {
		c.expr(parts[i].Expr)
	}
}

// --- reassignment targets -----------------------------------------------------

func (c *collector) target(t ast.AssignTarget) {
	switch n := t.(type) {
	case *ast.LValueTarget:
		c.expr(n.X)
	case *ast.TupleTarget:
		for _, el := range n.Elems {
			c.target(el)
		}
	case *ast.StructTarget:
		c.mark(n.TypeName)
		for i := range n.Fields {
			c.target(n.Fields[i].Target)
		}
	}
}

// --- patterns -----------------------------------------------------------------

func (c *collector) pattern(p ast.Pattern) {
	switch n := p.(type) {
	case *ast.LitPattern:
		c.expr(n.Lit)
	case *ast.NamePattern:
		c.mark(n.Name)
	case *ast.VariantPattern:
		c.mark(n.Name)
		for _, sub := range n.Elems {
			c.pattern(sub)
		}
	case *ast.StructPattern:
		c.mark(n.Name)
		for i := range n.Fields {
			c.pattern(n.Fields[i].Pat)
		}
	case *ast.TuplePattern:
		for _, sub := range n.Elems {
			c.pattern(sub)
		}
	case *ast.ListPattern:
		for i := range n.Elems {
			c.pattern(n.Elems[i].Pat)
		}
	case *ast.AsPattern:
		c.pattern(n.Inner)
	case *ast.OrPattern:
		for _, sub := range n.Alts {
			c.pattern(sub)
		}
	case *ast.RangeArm:
		c.expr(n.Lo)
		c.expr(n.Hi)
	}
}

// --- types --------------------------------------------------------------------

func (c *collector) typ(t ast.Type) {
	switch n := t.(type) {
	case nil:
		return
	case *ast.TypeRef:
		c.mark(n.Name)
		for _, a := range n.Args {
			c.typ(a)
		}
	case *ast.OptType:
		c.typ(n.Elem)
	case *ast.TupleType:
		for _, el := range n.Elems {
			c.typ(el)
		}
	case *ast.ArrayType:
		c.typ(n.Elem)
		if n.Count != nil {
			c.expr(n.Count.X)
		}
	case *ast.ChanType:
		c.typ(n.Elem)
	case *ast.PtrType:
		c.typ(n.Elem)
	case *ast.FnType:
		for i := range n.Params {
			c.typ(n.Params[i].Type)
		}
		c.typ(n.Ret)
	case *ast.ConstExpr:
		c.expr(n.X)
	}
}
