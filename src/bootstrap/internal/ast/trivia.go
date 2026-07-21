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
	case *AssignStmt:
		c.self(v)
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
		c.block(v.Body)
	case *ExprStmt:
		c.self(v)
		c.expr(v.X)
	case *NopStmt, *BreakStmt, *ContinueStmt:
		c.self(v)
	case *LitPattern:
		c.self(v)
		c.expr(v.Lit)
	case *BindPattern, *WildPattern:
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
			c.expr(a)
		}
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
