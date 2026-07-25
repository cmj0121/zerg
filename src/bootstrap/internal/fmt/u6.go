// U6 printing: the group 6 control-flow statements and expressions (for / with /
// raise / if-expr / guard) and the full pattern grammar (variant / struct /
// tuple / list / range-arm), in the same canonical layout as the rest — one
// statement per line, single spaces, block bodies one tab deeper, '=>' bodies,
// and verbatim range-arm bounds. The one subtlety is the 'for' while head: a
// membership condition 'v in r' is re-parenthesized so the reprint does not read
// as the iterate form 'for v in r'.
package fmt

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// --- control-flow statements & expressions ------------------------------------

// forStmt prints the three loop forms: infinite 'for { }', iterate
// 'for mut? v in e { }', or while 'for cond { }'.
func (p *printer) forStmt(n *ast.ForStmt) {
	p.write("for ")
	switch {
	case n.Var != "":
		if n.Mut {
			p.write("mut ")
		}
		p.write(n.Var)
		p.write(" in ")
		p.head(n.Iter)
		p.write(" ")
	case n.Cond != nil:
		p.forWhileHead(n.Cond)
		p.write(" ")
	}
	p.block(n.Body)
}

// forWhileHead prints a while condition, parenthesizing a membership 'v in r'
// (whose canonical print would begin 'identifier in') so it does not re-parse as
// the iterate form.
func (p *printer) forWhileHead(cond ast.Expr) {
	if forCondNeedsParens(cond) {
		p.write("(")
		p.expr(cond, precLowest)
		p.write(")")
		return
	}
	p.head(cond)
}

// forCondNeedsParens reports whether a for while-condition's canonical print
// would begin with 'identifier in' — the shape the parser reads as the iterate
// form — by descending the left spine to its leftmost leaf and its 'in' parent.
func forCondNeedsParens(e ast.Expr) bool {
	var deepest *ast.Binary
	node := e
	for {
		b, ok := node.(*ast.Binary)
		if !ok {
			break
		}
		deepest = b
		node = b.L
	}
	if deepest == nil || deepest.Op != token.In {
		return false
	}
	_, isIdent := node.(*ast.Ident)
	return isIdent
}

// withStmt prints 'with e (as id)? { body }'.
func (p *printer) withStmt(n *ast.WithStmt) {
	p.write("with ")
	p.head(n.Resource)
	if n.Var != "" {
		p.write(" as ")
		p.write(n.Var)
	}
	p.write(" ")
	p.block(n.Body)
}

// raiseStmt prints 'raise e (from c)?'.
func (p *printer) raiseStmt(n *ast.RaiseStmt) {
	p.write("raise ")
	p.expr(n.Value, precLowest)
	if n.From != nil {
		p.write(" from ")
		p.expr(n.From, precLowest)
	}
}

// ifExpr prints the if expression form; its trailing 'else' is mandatory.
func (p *printer) ifExpr(n *ast.IfExpr) {
	p.ifBranches(n.Branches)
	p.write(" else ")
	p.block(n.Else)
}

// guardExpr prints 'guard { body }'.
func (p *printer) guardExpr(n *ast.GuardExpr) {
	p.write("guard ")
	p.block(n.Body)
}

// --- patterns -----------------------------------------------------------------

// patternList prints a comma-separated pattern list between the given brackets.
func (p *printer) patternList(open, shut string, elems []ast.Pattern) {
	p.write(open)
	for i, e := range elems {
		if i > 0 {
			p.write(", ")
		}
		p.pattern(e)
	}
	p.write(shut)
}

// variantPattern prints 'Name(patterns)'.
func (p *printer) variantPattern(n *ast.VariantPattern) {
	p.write(n.Name)
	p.patternList("(", ")", n.Elems)
}

// structPattern prints 'Name{field-pats}' with an optional trailing '..' rest.
func (p *printer) structPattern(n *ast.StructPattern) {
	p.write(n.Name)
	p.write("{")
	for i, f := range n.Fields {
		if i > 0 {
			p.write(", ")
		}
		p.write(f.Name)
		if f.Pat != nil {
			p.write(": ")
			p.pattern(f.Pat)
		}
	}
	if n.Rest {
		if len(n.Fields) > 0 {
			p.write(", ")
		}
		p.write("..")
	}
	p.write("}")
}

// listPattern prints '[elems]' with a rest element written '..name'.
func (p *printer) listPattern(n *ast.ListPattern) {
	p.write("[")
	for i, el := range n.Elems {
		if i > 0 {
			p.write(", ")
		}
		if el.Rest {
			p.write("..")
			p.write(el.Name)
			continue
		}
		p.pattern(el.Pat)
	}
	p.write("]")
}

// rangeArm prints a match range arm 'lo..hi', 'lo..=hi', or the open 'lo..',
// with its compile-time-constant bounds reprinted verbatim.
func (p *printer) rangeArm(n *ast.RangeArm) {
	p.expr(n.Lo, precLowest)
	if n.Inclusive {
		p.write("..=")
	} else {
		p.write("..")
	}
	if n.Hi != nil {
		p.expr(n.Hi, precLowest)
	}
}
