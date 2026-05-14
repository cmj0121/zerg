package syntax

// v0.6 — deep AST clone helpers for generic-fn body specialisation.
//
// Background: specialiseGenericFn produces one *FnDecl clone per
// (decl, type-args) instantiation. The clone shares its Body *Block pointer
// with the original generic decl. Every Stmt / Expr inside a Block embeds the
// `typed` storage that the type checker writes into via setType. With shared
// nodes, two instantiations of the same generic fn (T = Cat, then T = Dog)
// stomp each other's typed[] slot — the second walk overwrites every node's
// type, and the first instantiation's run / cgen pass sees the wrong type.
//
// The fix is to deep-clone every Stmt + Expr + Pattern in the body so each
// specialisation owns its own typed[] storage. We do NOT clone the TypeRef
// nodes — they are read-only after the parser produces them and the
// substitution-aware resolveTypeRefWithSubst path consults their Resolved
// field per call rather than storing per-call results.

// cloneTypeRef returns a deep copy of a TypeRef so each generic-impl
// instantiation owns its own Resolved slot — cgen and the type checker write
// into TypeRef.Resolved at instantiation time, and a shared pointer would let
// the second walk's substitution overwrite the first walk's resolution.
func cloneTypeRef(r *TypeRef) *TypeRef {
	if r == nil {
		return nil
	}
	out := *r
	out.Resolved = nil
	if r.Element != nil {
		out.Element = cloneTypeRef(r.Element)
	}
	if len(r.Elements) > 0 {
		out.Elements = make([]*TypeRef, len(r.Elements))
		for i, e := range r.Elements {
			out.Elements[i] = cloneTypeRef(e)
		}
	}
	if len(r.TypeArgs) > 0 {
		out.TypeArgs = make([]*TypeRef, len(r.TypeArgs))
		for i, a := range r.TypeArgs {
			out.TypeArgs[i] = cloneTypeRef(a)
		}
	}
	return &out
}

// cloneBlock returns a deep copy of b. Returns nil on a nil input so callers
// don't have to nil-guard.
func cloneBlock(b *Block) *Block {
	if b == nil {
		return nil
	}
	out := &Block{Pos: b.Pos}
	if len(b.Statements) > 0 {
		out.Statements = make([]Stmt, len(b.Statements))
		for i, s := range b.Statements {
			out.Statements[i] = cloneStmt(s)
		}
	}
	return out
}

// cloneStmt deep-copies one statement. Every concrete Stmt type is handled
// explicitly; an unhandled shape falls through to a panic so a future Stmt
// addition force-updates this walker.
func cloneStmt(s Stmt) Stmt {
	if s == nil {
		return nil
	}
	switch n := s.(type) {
	case *LetStmt:
		out := *n
		out.Value = cloneExpr(n.Value)
		return &out
	case *MutStmt:
		out := *n
		out.Value = cloneExpr(n.Value)
		return &out
	case *ConstStmt:
		out := *n
		out.Value = cloneExpr(n.Value)
		return &out
	case *AssignStmt:
		out := *n
		out.Target = cloneExpr(n.Target)
		out.Value = cloneExpr(n.Value)
		return &out
	case *MultiAssignStmt:
		out := *n
		if len(n.Targets) > 0 {
			out.Targets = make([]Expr, len(n.Targets))
			for i, t := range n.Targets {
				out.Targets[i] = cloneExpr(t)
			}
		}
		out.Value = cloneExpr(n.Value)
		return &out
	case *ExprStmt:
		out := *n
		out.Expr = cloneExpr(n.Expr)
		return &out
	case *PrintStmt:
		out := *n
		out.Expr = cloneExpr(n.Expr)
		return &out
	case *ReturnStmt:
		out := *n
		out.Value = cloneExpr(n.Value)
		out.Guard = cloneExpr(n.Guard)
		return &out
	case *BreakStmt:
		out := *n
		out.Guard = cloneExpr(n.Guard)
		return &out
	case *ContinueStmt:
		out := *n
		out.Guard = cloneExpr(n.Guard)
		return &out
	case *NopStmt:
		out := *n
		return &out
	case *IfStmt:
		out := *n
		out.Cond = cloneExpr(n.Cond)
		out.Then = cloneBlock(n.Then)
		if len(n.Elifs) > 0 {
			out.Elifs = make([]ElifClause, len(n.Elifs))
			for i, ec := range n.Elifs {
				out.Elifs[i] = ElifClause{Pos: ec.Pos, Cond: cloneExpr(ec.Cond), Body: cloneBlock(ec.Body)}
			}
		}
		out.Else = cloneBlock(n.Else)
		return &out
	case *ForStmt:
		out := *n
		out.Cond = cloneExpr(n.Cond)
		if n.Range != nil {
			cloned, _ := cloneExpr(n.Range).(*RangeExpr)
			out.Range = cloned
		}
		out.Iter = cloneExpr(n.Iter)
		out.Body = cloneBlock(n.Body)
		return &out
	case *MatchStmt:
		out := *n
		out.Subject = cloneExpr(n.Subject)
		if len(n.Arms) > 0 {
			out.Arms = make([]MatchArm, len(n.Arms))
			for i, a := range n.Arms {
				out.Arms[i] = MatchArm{
					Pos:     a.Pos,
					Pattern: clonePattern(a.Pattern),
					Guard:   cloneExpr(a.Guard),
					Body:    cloneBlock(a.Body),
				}
			}
		}
		return &out
	case *FnDecl:
		// Nested fn decls are not part of the v0.6 surface inside a fn body,
		// but cloning conservatively avoids surprises.
		out := *n
		out.Body = cloneBlock(n.Body)
		return &out
	case *StructDecl, *EnumDecl, *SpecDecl, *ImplDecl, *ImportDecl:
		// Top-level decls are never nested inside a generic fn body, but if
		// they appear here just pass through unchanged — they carry no
		// per-typed mutable state.
		return s
	}
	panic("clone_v06: unhandled Stmt")
}

// cloneExpr deep-copies one expression, producing fresh `typed` storage on
// every node so a re-walk under a different substitution doesn't stomp the
// previous walk's recorded types.
func cloneExpr(e Expr) Expr {
	if e == nil {
		return nil
	}
	switch n := e.(type) {
	case *IntLit:
		out := *n
		out.typed = typed{}
		return &out
	case *FloatLit:
		out := *n
		out.typed = typed{}
		return &out
	case *StringLit:
		out := *n
		out.typed = typed{}
		return &out
	case *InterpolatedStringLit:
		out := *n
		out.typed = typed{}
		if len(n.Pieces) > 0 {
			out.Pieces = make([]StringPiece, len(n.Pieces))
			for i, p := range n.Pieces {
				switch pp := p.(type) {
				case *StringLitPiece:
					out.Pieces[i] = &StringLitPiece{Text: pp.Text}
				case *StringVarPiece:
					out.Pieces[i] = &StringVarPiece{Ident: cloneExpr(pp.Ident).(*IdentExpr)}
				}
			}
		}
		return &out
	case *BoolLit:
		out := *n
		out.typed = typed{}
		return &out
	case *RuneLit:
		out := *n
		out.typed = typed{}
		return &out
	case *NilLit:
		out := *n
		out.typed = typed{}
		return &out
	case *IdentExpr:
		out := *n
		out.typed = typed{}
		return &out
	case *ThisExpr:
		out := *n
		out.typed = typed{}
		return &out
	case *BinaryExpr:
		out := *n
		out.typed = typed{}
		out.Left = cloneExpr(n.Left)
		out.Right = cloneExpr(n.Right)
		// v0.17: Lowered is regenerated by typeck when the cloned
		// generic-fn specialisation is re-checked. Reset here.
		out.Lowered = nil
		out.LoweredNot = false
		return &out
	case *UnaryExpr:
		out := *n
		out.typed = typed{}
		out.Operand = cloneExpr(n.Operand)
		out.Lowered = nil
		return &out
	case *CallExpr:
		out := *n
		out.typed = typed{}
		out.Callee = cloneExpr(n.Callee)
		out.Args = cloneExprs(n.Args)
		out.Specialised = nil
		out.Lowered = nil
		return &out
	case *RangeExpr:
		out := *n
		out.typed = typed{}
		out.Start = cloneExpr(n.Start)
		out.End = cloneExpr(n.End)
		return &out
	case *ParenExpr:
		out := *n
		out.typed = typed{}
		out.Inner = cloneExpr(n.Inner)
		return &out
	case *ListLit:
		out := *n
		out.typed = typed{}
		out.Elements = cloneExprs(n.Elements)
		return &out
	case *TupleLit:
		out := *n
		out.typed = typed{}
		out.Elements = cloneExprs(n.Elements)
		return &out
	case *StructLit:
		out := *n
		out.typed = typed{}
		if len(n.Fields) > 0 {
			out.Fields = make([]FieldInit, len(n.Fields))
			for i, f := range n.Fields {
				out.Fields[i] = FieldInit{Name: f.Name, Pos: f.Pos, Value: cloneExpr(f.Value)}
			}
		}
		return &out
	case *IndexExpr:
		out := *n
		out.typed = typed{}
		out.Receiver = cloneExpr(n.Receiver)
		out.Index = cloneExpr(n.Index)
		return &out
	case *SliceExpr:
		out := *n
		out.typed = typed{}
		out.Receiver = cloneExpr(n.Receiver)
		out.Low = cloneExpr(n.Low)
		out.High = cloneExpr(n.High)
		return &out
	case *FieldAccessExpr:
		out := *n
		out.typed = typed{}
		out.Receiver = cloneExpr(n.Receiver)
		out.Lowered = nil
		return &out
	case *MethodCallExpr:
		out := *n
		out.typed = typed{}
		out.Receiver = cloneExpr(n.Receiver)
		out.Args = cloneExprs(n.Args)
		out.Lowered = nil
		out.LoweredCall = nil
		return &out
	case *EnumLit:
		out := *n
		out.typed = typed{}
		out.Payload = cloneExprs(n.Payload)
		return &out
	case *PropagateExpr:
		out := *n
		out.typed = typed{}
		out.Inner = cloneExpr(n.Inner)
		return &out
	case *CoalesceExpr:
		out := *n
		out.typed = typed{}
		out.Left = cloneExpr(n.Left)
		out.Right = cloneExpr(n.Right)
		return &out
	}
	panic("clone_v06: unhandled Expr")
}

func cloneExprs(in []Expr) []Expr {
	if in == nil {
		return nil
	}
	out := make([]Expr, len(in))
	for i, e := range in {
		out[i] = cloneExpr(e)
	}
	return out
}

// clonePattern deep-copies a match pattern. Patterns embed Expr nodes inside
// LitPat so the embedded literals get fresh typed storage too.
func clonePattern(p Pattern) Pattern {
	if p == nil {
		return nil
	}
	switch n := p.(type) {
	case *LitPat:
		out := *n
		out.Lit = cloneExpr(n.Lit)
		return &out
	case *WildcardPat:
		out := *n
		return &out
	case *BindPat:
		out := *n
		return &out
	case *TuplePat:
		out := *n
		if len(n.Elements) > 0 {
			out.Elements = make([]Pattern, len(n.Elements))
			for i, sub := range n.Elements {
				out.Elements[i] = clonePattern(sub)
			}
		}
		return &out
	case *StructPat:
		out := *n
		if len(n.Fields) > 0 {
			out.Fields = make([]StructPatField, len(n.Fields))
			for i, f := range n.Fields {
				out.Fields[i] = StructPatField{Name: f.Name, Pos: f.Pos, Pattern: clonePattern(f.Pattern)}
			}
		}
		return &out
	case *EnumPat:
		out := *n
		if len(n.Payload) > 0 {
			out.Payload = make([]Pattern, len(n.Payload))
			for i, sub := range n.Payload {
				out.Payload[i] = clonePattern(sub)
			}
		}
		return &out
	}
	panic("clone_v06: unhandled Pattern")
}
