// Trivia collection backs the fmt oracle's conservation check: every comment the
// lexer produced must land on some node, or 'zerg fmt' would silently drop it.
// Trivia walks the whole tree — every node's Lead and Trail, plus the dangling
// File.End and Block.End slots and each match arm — and returns the flat list of
// trivia actually attached, so a caller can compare its comment count against the
// source token stream.
package ast

import "github.com/cmj0121/zerg/src/bootstrap/internal/token"

// Trivia returns every piece of trivia attached anywhere in the tree rooted at n.
func Trivia(n Node) []token.Trivia {
	var c triviaCollector
	c.node(n)
	return c.out
}

type triviaCollector struct{ out []token.Trivia }

func (c *triviaCollector) add(list []token.Trivia) { c.out = append(c.out, list...) }

// block collects a block's trivia, tolerating a nil block (an absent 'else').
func (c *triviaCollector) block(b *Block) {
	if b == nil {
		return
	}
	c.self(b)
	for _, s := range b.Stmts {
		c.node(s)
	}
	c.add(b.End)
}

// self collects a node's own lead and trail trivia.
func (c *triviaCollector) self(n Node) {
	c.add(n.Lead())
	c.add(n.Trail())
}

// target collects trivia from an assignment target subtree.
func (c *triviaCollector) target(t AssignTarget) {
	switch v := t.(type) {
	case nil:
		return
	case *LValueTarget:
		c.self(v)
		c.expr(v.X)
	case *TupleTarget:
		c.self(v)
		for _, e := range v.Elems {
			c.target(e)
		}
	case *StructTarget:
		c.self(v)
		for _, f := range v.Fields {
			c.target(f.Target)
		}
	}
}

// constExpr collects trivia from a const-expr's inner expression, tolerating nil.
func (c *triviaCollector) constExpr(ce *ConstExpr) {
	if ce == nil {
		return
	}
	c.self(ce)
	c.expr(ce.X)
}

// fstrParts collects trivia from the hole expressions of an f-string or f-cmd.
func (c *triviaCollector) fstrParts(parts []FStrPart) {
	for i := range parts {
		c.expr(parts[i].Expr)
	}
}

func (c *triviaCollector) node(n Node) {
	if n == nil {
		return
	}
	switch v := n.(type) {
	case *File:
		for _, d := range v.Decls {
			c.node(d)
		}
		c.add(v.End)
	case *FuncDecl:
		c.self(v)
		c.block(v.Body)
	case *Block:
		c.block(v)
	case *BindStmt:
		c.self(v)
		c.expr(v.Value)
	case *Reassign:
		c.self(v)
		c.target(v.Target)
		c.expr(v.Value)
	case *PrintStmt:
		c.self(v)
		c.expr(v.Value)
	case *ReturnStmt:
		c.self(v)
		c.expr(v.Value)
		c.expr(v.Cond)
	case *IfStmt:
		c.self(v)
		for _, br := range v.Branches {
			c.expr(br.Cond)
			c.block(br.Body)
		}
		c.block(v.Else)
	case *ForStmt:
		c.self(v)
		c.expr(v.Cond)
		c.expr(v.Iter)
		c.block(v.Body)
	case *WithStmt:
		c.self(v)
		c.expr(v.Resource)
		c.block(v.Body)
	case *RaiseStmt:
		c.self(v)
		c.expr(v.Value)
		c.expr(v.From)
	case *ExprStmt:
		c.self(v)
		c.expr(v.X)
	case *BreakStmt:
		c.self(v)
		c.expr(v.Cond)
	case *ContinueStmt:
		c.self(v)
		c.expr(v.Cond)
	case *NopStmt:
		c.self(v)
	case *StructDecl:
		c.self(v)
		for _, f := range v.Fields {
			c.self(f)
			c.expr(f.Default)
		}
		c.add(v.End)
	case *EnumDecl:
		c.self(v)
		for _, vr := range v.Variants {
			c.self(vr)
			c.constExpr(vr.Discr)
		}
		c.add(v.End)
	case *TypeDecl:
		c.self(v)
	case *SpecDecl:
		c.self(v)
		for _, m := range v.Members {
			c.node(m)
		}
		c.add(v.End)
	case *ImplDecl:
		c.self(v)
		for _, it := range v.Items {
			c.node(it)
		}
		c.add(v.End)
	case *InitDecl:
		c.self(v)
		c.block(v.Body)
	case *FnSig, *AssocType, *AssocVal, *AssocBind:
		c.self(v)
	case *ValBind:
		c.self(v)
		c.constExpr(v.Value)
	case *LitPattern:
		c.self(v)
		c.expr(v.Lit)
	case *RangeArm:
		c.self(v)
		c.expr(v.Lo)
		c.expr(v.Hi)
	case *VariantPattern:
		c.self(v)
		for _, e := range v.Elems {
			c.node(e)
		}
	case *StructPattern:
		c.self(v)
		for _, f := range v.Fields {
			if f.Pat != nil {
				c.node(f.Pat)
			}
		}
	case *TuplePattern:
		c.self(v)
		for _, e := range v.Elems {
			c.node(e)
		}
	case *ListPattern:
		c.self(v)
		for _, el := range v.Elems {
			if el.Pat != nil {
				c.node(el.Pat)
			}
		}
	case *AsPattern:
		c.self(v)
		c.node(v.Inner)
	case *OrPattern:
		c.self(v)
		for _, alt := range v.Alts {
			c.node(alt)
		}
	case *NamePattern, *WildPattern:
		c.self(v)
	default:
		// expressions and patterns share the expr path
		if e, ok := n.(Expr); ok {
			c.expr(e)
			return
		}
		c.self(n)
	}
}

// expr collects trivia from an expression subtree, including interior nodes so a
// comment attached anywhere inside an expression is counted (today the parser
// attaches none there, which is exactly what the conservation check guards).
func (c *triviaCollector) expr(e Expr) {
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *Unary:
		c.self(v)
		c.expr(v.X)
	case *Binary:
		c.self(v)
		c.expr(v.L)
		c.expr(v.R)
	case *Call:
		c.self(v)
		c.expr(v.Callee)
		for _, a := range v.Args {
			c.expr(a.Value)
		}
	case *Range:
		c.self(v)
		c.expr(v.Lo)
		c.expr(v.Hi)
	case *IsExpr:
		c.self(v)
		c.expr(v.X)
	case *Coalesce:
		c.self(v)
		c.expr(v.X)
		c.expr(v.Y)
	case *Diverge:
		c.self(v)
		c.expr(v.Value)
		c.expr(v.From)
	case *Try:
		c.self(v)
		c.expr(v.X)
	case *Force:
		c.self(v)
		c.expr(v.X)
	case *OptChain:
		c.self(v)
		c.expr(v.X)
	case *Recv:
		c.self(v)
		c.expr(v.X)
	case *Field:
		c.self(v)
		c.expr(v.X)
	case *TupleIndex:
		c.self(v)
		c.expr(v.X)
	case *Bracket:
		c.self(v)
		c.expr(v.Base)
		for _, e := range v.Elems {
			c.expr(e)
		}
	case *TupleLit:
		c.self(v)
		for _, e := range v.Elems {
			c.expr(e)
		}
	case *ListLit:
		c.self(v)
		for _, e := range v.Elems {
			c.expr(e)
		}
	case *ListFill:
		c.self(v)
		c.expr(v.Value)
		c.expr(v.Count)
	case *MapLit:
		c.self(v)
		for _, e := range v.Entries {
			c.expr(e.Key)
			c.expr(e.Value)
		}
	case *FStr:
		c.self(v)
		c.fstrParts(v.Parts)
	case *FCmd:
		c.self(v)
		c.fstrParts(v.Parts)
	case *FnExpr:
		c.self(v)
		for i := range v.Params {
			c.expr(v.Params[i].Default)
		}
		c.block(v.Body)
	case *Block:
		c.block(v)
	case *IfExpr:
		c.self(v)
		for _, br := range v.Branches {
			c.expr(br.Cond)
			c.block(br.Body)
		}
		c.block(v.Else)
	case *GuardExpr:
		c.self(v)
		c.block(v.Body)
	case *MatchExpr:
		c.self(v)
		c.expr(v.Subject)
		for i := range v.Arms {
			arm := &v.Arms[i]
			c.add(arm.Lead())
			c.add(arm.Trail())
			c.node(arm.Pat)
			c.expr(arm.Guard)
			c.expr(arm.Body)
		}
	default:
		c.self(v)
	}
}
