// Package fmt renders a parsed Zerg file back to canonical source form
// (DESIGN-1a §7): one statement per line — never ';' as a separator — single
// spaces around binary operators and '='/':='/'->'/'=>', no space between a
// callee and its '(', block bodies indented one tab, and node-attached trivia
// reprinted in place (lead comments each on their own line at the node's indent,
// at most one blank line, trailing comments on the node's own line).
//
// The canonical form is the parser's oracle: parse(fmt(parse(src))) must equal
// parse(src) modulo normalization, and fmt must be a byte-idempotent fixpoint
// (see RoundTrip).
package fmt

import (
	"strconv"
	"strings"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// Format parses src and prints it in canonical form. When the diagnostics are
// non-empty the source either did not parse cleanly or carries a comment a
// canonical reprint would lose (see conservationDiags); in both cases the output
// is empty and must not replace the source.
func Format(src string) (string, []diag.Diagnostic) {
	file, diags := parser.Parse(src)
	if len(diags) > 0 {
		return "", diags
	}
	if diags := conservationDiags(src, file); len(diags) > 0 {
		return "", diags
	}
	return Print(file), nil
}

// Print renders a parsed file in canonical form.
func Print(file *ast.File) string {
	p := &printer{}
	for _, d := range file.Decls {
		p.leadTrivia(d.Lead())
		p.ind()
		if fn, ok := d.(*ast.FuncDecl); ok {
			p.funcDecl(fn)
		}
		p.trailTrivia(d.Trail())
		p.nl()
	}
	p.leadTrivia(file.End)
	return p.sb.String()
}

// printer accumulates the canonical text; indent is the current block depth.
type printer struct {
	sb     strings.Builder
	indent int
}

func (p *printer) write(s string) { p.sb.WriteString(s) }
func (p *printer) nl()            { p.sb.WriteByte('\n') }

// ind writes the current indentation (one tab per block level).
func (p *printer) ind() {
	for i := 0; i < p.indent; i++ {
		p.sb.WriteByte('\t')
	}
}

// leadTrivia prints a node's leading trivia: each comment on its own line at the
// current indent, a DocComment block line by line, and at most one blank line in
// a row.
func (p *printer) leadTrivia(list []token.Trivia) {
	prevBlank := false
	for _, tr := range list {
		switch tr.Kind {
		case token.BlankLine:
			if prevBlank {
				continue
			}
			prevBlank = true
			p.nl()
		case token.DocComment:
			prevBlank = false
			for _, line := range strings.Split(tr.Text, "\n") {
				p.ind()
				p.write(line)
				p.nl()
			}
		default: // LineComment
			prevBlank = false
			p.ind()
			p.write(tr.Text)
			p.nl()
		}
	}
}

// trailTrivia prints a node's trailing comment(s) on the current line.
func (p *printer) trailTrivia(list []token.Trivia) {
	for _, tr := range list {
		p.write(" ")
		p.write(tr.Text)
	}
}

// --- declarations & blocks -----------------------------------------------------

func (p *printer) funcDecl(fn *ast.FuncDecl) {
	if fn.Pub {
		p.write("pub ")
	}
	p.write("fn ")
	p.write(fn.Name)
	p.write("(")
	for i := range fn.Params {
		if i > 0 {
			p.write(", ")
		}
		p.write(fn.Params[i].Name)
		p.write(": ")
		p.write(fn.Params[i].Type.Name)
	}
	p.write(")")
	if fn.Ret != nil {
		p.write(" -> ")
		p.write(fn.Ret.Name)
	}
	p.write(" ")
	p.block(fn.Body)
}

// block prints '{ ... }' with the body one tab deeper; the cursor is left just
// after the closing '}' so the caller can append 'else' or trailing trivia.
func (p *printer) block(b *ast.Block) {
	p.write("{")
	p.nl()
	p.indent++
	for _, s := range b.Stmts {
		p.leadTrivia(s.Lead())
		p.ind()
		p.stmt(s)
		p.trailTrivia(s.Trail())
		p.nl()
	}
	p.leadTrivia(b.End)
	p.indent--
	p.ind()
	p.write("}")
}

// --- statements ---------------------------------------------------------------

func (p *printer) stmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.NopStmt:
		p.write("nop")
	case *ast.BindStmt:
		p.bind(n)
	case *ast.AssignStmt:
		p.write(n.Name)
		p.write(" = ")
		p.expr(n.Value, precLowest)
	case *ast.PrintStmt:
		p.write("print ")
		p.expr(n.Value, precLowest)
	case *ast.ReturnStmt:
		p.write("return")
		if n.Value != nil {
			p.write(" ")
			p.expr(n.Value, precLowest)
		}
		if n.Cond != nil {
			p.write(" if ")
			p.expr(n.Cond, precLowest)
		}
	case *ast.BreakStmt:
		p.write("break")
	case *ast.ContinueStmt:
		p.write("continue")
	case *ast.IfStmt:
		p.ifStmt(n)
	case *ast.ForStmt:
		p.write("for ")
		if n.Cond != nil {
			p.expr(n.Cond, precLowest)
			p.write(" ")
		}
		p.block(n.Body)
	case *ast.ExprStmt:
		p.expr(n.X, precLowest)
	}
}

func (p *printer) bind(n *ast.BindStmt) {
	if n.Const {
		p.write("const ")
	}
	if n.Mut {
		p.write("mut ")
	}
	p.write(n.Name)
	if n.Type != nil {
		p.write(": ")
		p.write(n.Type.Name)
		p.write(" = ")
	} else {
		p.write(" := ")
	}
	p.expr(n.Value, precLowest)
}

func (p *printer) ifStmt(n *ast.IfStmt) {
	for i, br := range n.Branches {
		if i == 0 {
			p.write("if ")
		} else {
			p.write(" else if ")
		}
		p.expr(br.Cond, precLowest)
		p.write(" ")
		p.block(br.Body)
	}
	if n.Else != nil {
		p.write(" else ")
		p.block(n.Else)
	}
}

// --- expressions --------------------------------------------------------------

// Precedence levels of the expression ladder, lowest first; a child printed in a
// context tighter than its own precedence is parenthesized so the reprinted text
// parses back to the same tree.
const (
	precLowest = iota
	precOr
	precAnd
	precCmp
	precAdd
	precMul
	precUnary
	precPostfix
)

// binPrec maps a binary operator to its precedence level.
func binPrec(k token.Kind) int {
	switch k {
	case token.Or:
		return precOr
	case token.And:
		return precAnd
	case token.EqEq, token.Ne, token.Lt, token.Gt, token.Le, token.Ge:
		return precCmp
	case token.Plus, token.Minus, token.PlusMod, token.MinusMod, token.Pipe, token.Caret:
		return precAdd
	default: // *, /, %, *%, <<, >>, &
		return precMul
	}
}

// expr prints e in a context of the given minimum precedence, wrapping in
// parentheses when the node binds looser than the context requires.
func (p *printer) expr(e ast.Expr, prec int) {
	switch n := e.(type) {
	case *ast.IntLit:
		p.write(n.Text) // the author's lexeme, base and '_' grouping intact
	case *ast.FloatLit:
		p.write(floatText(n.Value))
	case *ast.BoolLit:
		p.write(strconv.FormatBool(n.Value))
	case *ast.StrLit:
		p.write(quote(n.Value))
	case *ast.NilLit:
		p.write("nil")
	case *ast.Ident:
		p.write(n.Name)
	case *ast.Unary:
		p.unary(n, prec)
	case *ast.Binary:
		p.binary(n, prec)
	case *ast.Call:
		p.expr(n.Callee, precPostfix)
		p.write("(")
		for i, a := range n.Args {
			if i > 0 {
				p.write(", ")
			}
			p.expr(a, precLowest)
		}
		p.write(")")
	case *ast.MatchExpr:
		p.match(n)
	}
}

func (p *printer) unary(n *ast.Unary, prec int) {
	paren := precUnary < prec
	if paren {
		p.write("(")
	}
	if n.Op == token.Not {
		p.write("not ")
	} else {
		p.write(n.Op.String())
	}
	p.expr(n.X, precUnary)
	if paren {
		p.write(")")
	}
}

func (p *printer) binary(n *ast.Binary, prec int) {
	my := binPrec(n.Op)
	paren := my < prec
	if paren {
		p.write("(")
	}
	// left-associative: an equal-precedence left child needs no parens, an
	// equal-precedence right child does; comparison is non-associative, so a
	// comparison child on either side is parenthesized.
	leftCtx := my
	if my == precCmp {
		leftCtx = my + 1
	}
	p.expr(n.L, leftCtx)
	p.write(" ")
	p.write(n.Op.String())
	p.write(" ")
	p.expr(n.R, my+1)
	if paren {
		p.write(")")
	}
}

func (p *printer) match(n *ast.MatchExpr) {
	p.write("match ")
	p.expr(n.Subject, precLowest)
	p.write(" {")
	p.nl()
	p.indent++
	for i := range n.Arms {
		arm := &n.Arms[i]
		p.leadTrivia(arm.Lead())
		p.ind()
		p.pattern(arm.Pat)
		if arm.Guard != nil {
			p.write(" if ")
			p.expr(arm.Guard, precLowest)
		}
		p.write(" => ")
		p.expr(arm.Body, precLowest)
		p.trailTrivia(arm.Trail())
		p.nl()
	}
	p.indent--
	p.ind()
	p.write("}")
}

func (p *printer) pattern(pat ast.Pattern) {
	switch n := pat.(type) {
	case *ast.WildPattern:
		p.write("_")
	case *ast.BindPattern:
		p.write(n.Name)
	case *ast.LitPattern:
		if n.Neg {
			p.write("-")
		}
		p.expr(n.Lit, precLowest)
	}
}

// --- literal rendering ---------------------------------------------------------

// floatText renders a float literal so it re-lexes as a Float (always carrying a
// '.' or an exponent).
func floatText(v float64) string {
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// quote renders a decoded string value as a Zerg string literal, using only the
// escapes GRAMMAR defines ('\n', '\t', '\r', '\0', '\\', '\"', and '\u{...}' for
// other control characters); printable text — including non-ASCII — is kept
// verbatim.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == 0:
			b.WriteString(`\0`)
		case r < 0x20:
			b.WriteString(`\u{` + strconv.FormatInt(int64(r), 16) + `}`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
