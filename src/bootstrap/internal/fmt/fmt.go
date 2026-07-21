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
	for _, it := range file.Items {
		p.leadTrivia(it.Lead())
		p.topItem(it)
		p.trailTrivia(it.Trail())
		p.nl()
	}
	p.leadTrivia(file.End)
	return p.sb.String()
}

// topItem prints one item of the top-level stmt-list: a declaration (which owns
// its own indent and decorator prefix) or a plain statement (an import or a
// module-level binding) at the current indent.
func (p *printer) topItem(it ast.Stmt) {
	if d, ok := it.(ast.Decl); ok {
		p.decl(d)
		return
	}
	p.ind()
	p.stmt(it)
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
	if fn.Unsafe {
		p.write("unsafe ")
	}
	if fn.Mut {
		p.write("mut ")
	}
	p.write("fn ")
	p.write(fn.Name)
	p.generics(fn.Generics)
	p.paramList(fn.Params)
	if fn.Ret != nil {
		p.write(" -> ")
		p.typ(fn.Ret)
	}
	p.write(" ")
	p.block(fn.Body)
}

// paramList prints a parenthesized declared-parameter list.
func (p *printer) paramList(params []ast.Param) {
	p.write("(")
	for i := range params {
		if i > 0 {
			p.write(", ")
		}
		p.param(&params[i])
	}
	p.write(")")
}

// param prints one declared function parameter: 'mut &x'? name ': type' (= default)?.
func (p *printer) param(pm *ast.Param) {
	if pm.Ref {
		p.write("mut &")
	}
	p.write(pm.Name)
	p.write(": ")
	p.typ(pm.Type)
	if pm.Default != nil {
		p.write(" = ")
		p.expr(pm.Default, precLowest)
	}
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
	case *ast.Reassign:
		p.assignTarget(n.Target)
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
		if n.Cond != nil {
			p.write(" if ")
			p.expr(n.Cond, precLowest)
		}
	case *ast.ContinueStmt:
		p.write("continue")
		if n.Cond != nil {
			p.write(" if ")
			p.expr(n.Cond, precLowest)
		}
	case *ast.IfStmt:
		p.ifStmt(n)
	case *ast.ForStmt:
		p.forStmt(n)
	case *ast.WithStmt:
		p.withStmt(n)
	case *ast.RaiseStmt:
		p.raiseStmt(n)
	case *ast.SpawnStmt:
		p.write("spawn ")
		p.expr(n.Call, precLowest)
	case *ast.SendStmt:
		p.expr(n.Chan, precLowest)
		p.write(" <- ")
		p.expr(n.Value, precLowest)
	case *ast.DeferStmt:
		p.write("defer ")
		p.expr(n.Call, precLowest)
	case *ast.DelStmt:
		p.write("del ")
		p.write(n.Name)
	case *ast.ImportStmt:
		p.importStmt(n)
	case *ast.SelectStmt:
		p.selectStmt(n)
	case *ast.ExprStmt:
		p.expr(n.X, precLowest)
	}
}

// head prints an if/for/with/match head expression, wrapping a '{'-opening
// expression (a map literal or a block) in parentheses as GRAMMAR requires
// (DESIGN-1a §3c), so the reprinted head does not swallow the body's '{'.
func (p *printer) head(e ast.Expr) {
	switch e.(type) {
	case *ast.MapLit, *ast.Block:
		p.write("(")
		p.expr(e, precLowest)
		p.write(")")
	default:
		p.expr(e, precLowest)
	}
}

// assignTarget prints a reassignment target: an lvalue, a tuple shape, or a
// struct shape.
func (p *printer) assignTarget(t ast.AssignTarget) {
	switch n := t.(type) {
	case *ast.LValueTarget:
		p.expr(n.X, precLowest)
	case *ast.TupleTarget:
		p.write("(")
		for i, e := range n.Elems {
			if i > 0 {
				p.write(", ")
			}
			p.assignTarget(e)
		}
		p.write(")")
	case *ast.StructTarget:
		p.write(n.TypeName)
		p.write("{")
		for i, f := range n.Fields {
			if i > 0 {
				p.write(", ")
			}
			p.write(f.Name)
			if f.Target != nil {
				p.write(": ")
				p.assignTarget(f.Target)
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
		p.typ(n.Type)
		p.write(" = ")
	} else {
		p.write(" := ")
	}
	p.expr(n.Value, precLowest)
}

func (p *printer) ifStmt(n *ast.IfStmt) {
	p.ifBranches(n.Branches)
	if n.Else != nil {
		p.write(" else ")
		p.block(n.Else)
	}
}

// ifBranches prints an if/else-if chain's head branches (shared by the statement
// and expression forms).
func (p *printer) ifBranches(branches []ast.IfBranch) {
	for i, br := range branches {
		if i == 0 {
			p.write("if ")
		} else {
			p.write(" else if ")
		}
		p.ifHead(br)
		p.write(" ")
		p.block(br.Body)
	}
}

// ifHead prints an if head: a binding form 'x := e' or a plain head expression,
// each wrapping a '{'-opening head in parens as GRAMMAR requires.
func (p *printer) ifHead(br ast.IfBranch) {
	if br.Bind != "" {
		p.write(br.Bind)
		p.write(" := ")
	}
	p.head(br.Cond)
}

// --- expressions --------------------------------------------------------------

// Precedence levels of the expression ladder, lowest first; a child printed in a
// context tighter than its own precedence is parenthesized so the reprinted text
// parses back to the same tree.
const (
	precLowest = iota
	precCoalesce
	precOr
	precAnd
	precCmp
	precRange
	precAdd
	precMul
	precUnary
	precRecv
	precPostfix
)

// binPrec maps a binary operator to its precedence level.
func binPrec(k token.Kind) int {
	switch k {
	case token.Or:
		return precOr
	case token.And:
		return precAnd
	case token.EqEq, token.Ne, token.Lt, token.Gt, token.Le, token.Ge, token.In:
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
		p.write(n.Text) // the author's lexeme, exponent form and '_' grouping intact
	case *ast.BoolLit:
		p.write(strconv.FormatBool(n.Value))
	case *ast.StrLit:
		p.write(quote(n.Value))
	case *ast.NilLit:
		p.write("nil")
	case *ast.RawStrLit:
		p.write(n.Text) // raw string: reprinted verbatim (processes no escapes)
	case *ast.RuneLit:
		p.write(n.Text) // rune literal: surface form kept verbatim
	case *ast.ByteLit:
		p.write(n.Text) // byte literal: surface form kept verbatim
	case *ast.CmdLit:
		p.write(n.Text) // command literal: surface backtick form kept verbatim
	case *ast.Ident:
		p.write(n.Name)
	case *ast.Unary:
		p.unary(n, prec)
	case *ast.Binary:
		p.binary(n, prec)
	case *ast.Range:
		p.rangeExpr(n, prec)
	case *ast.IsExpr:
		p.paren(prec, precCmp, func() {
			p.expr(n.X, precRange)
			p.write(" is ")
			p.write(n.TypeName)
		})
	case *ast.Coalesce:
		p.paren(prec, precCoalesce, func() {
			p.expr(n.X, precCoalesce+1)
			p.write(" ?? ")
			p.expr(n.Y, precCoalesce)
		})
	case *ast.Diverge:
		p.diverge(n)
	case *ast.Recv:
		p.paren(prec, precRecv, func() {
			p.write("<-")
			p.expr(n.X, precRecv)
		})
	case *ast.Try:
		p.expr(n.X, precPostfix)
		p.write("?")
	case *ast.Force:
		p.expr(n.X, precPostfix)
		p.write("!")
	case *ast.OptChain:
		p.expr(n.X, precPostfix)
		p.write("?.")
		p.write(n.Name)
	case *ast.Field:
		p.expr(n.X, precPostfix)
		p.write(".")
		p.write(n.Name)
	case *ast.TupleIndex:
		p.expr(n.X, precPostfix)
		p.write(".")
		p.write(n.Text)
	case *ast.Call:
		p.expr(n.Callee, precPostfix)
		p.write("(")
		for i, a := range n.Args {
			if i > 0 {
				p.write(", ")
			}
			if a.Name != "" {
				p.write(a.Name)
				p.write(": ")
			}
			p.expr(a.Value, precLowest)
		}
		p.write(")")
	case *ast.Bracket:
		p.expr(n.Base, precPostfix)
		p.write("[")
		for i, e := range n.Elems {
			if i > 0 {
				p.write(", ")
			}
			p.expr(e, precLowest)
		}
		p.write("]")
	case *ast.TupleLit:
		p.write("(")
		for i, e := range n.Elems {
			if i > 0 {
				p.write(", ")
			}
			p.expr(e, precLowest)
		}
		p.write(")")
	case *ast.ListLit:
		p.write("[")
		for i, e := range n.Elems {
			if i > 0 {
				p.write(", ")
			}
			p.expr(e, precLowest)
		}
		p.write("]")
	case *ast.ListFill:
		p.write("[")
		p.expr(n.Value, precLowest)
		p.write("; ")
		p.expr(n.Count, precLowest)
		p.write("]")
	case *ast.MapLit:
		p.mapLit(n)
	case *ast.FStr:
		p.fstr(n)
	case *ast.FCmd:
		p.fcmd(n)
	case *ast.FnExpr:
		p.fnExpr(n)
	case *ast.Block:
		p.block(n)
	case *ast.MatchExpr:
		p.match(n)
	case *ast.IfExpr:
		p.ifExpr(n)
	case *ast.GuardExpr:
		p.guardExpr(n)
	case *ast.ChanNew:
		p.chanNew(n)
	case *ast.UnsafeExpr:
		p.write("unsafe ")
		p.block(n.Body)
	case *ast.AsmExpr:
		p.asmExpr(n)
	}
}

// paren runs body, wrapping it in parentheses when the node's precedence (my) is
// looser than the surrounding context requires.
func (p *printer) paren(ctx, my int, body func()) {
	if my < ctx {
		p.write("(")
		body()
		p.write(")")
		return
	}
	body()
}

// rangeExpr prints 'lo..hi', 'lo..=hi', or the open range 'lo..'.
func (p *printer) rangeExpr(n *ast.Range, prec int) {
	p.paren(prec, precRange, func() {
		p.expr(n.Lo, precRange+1)
		if n.Inclusive {
			p.write("..=")
		} else {
			p.write("..")
		}
		if n.Hi != nil {
			p.expr(n.Hi, precRange+1)
		}
	})
}

// diverge prints a '??' right-side control-flow escape.
func (p *printer) diverge(n *ast.Diverge) {
	switch n.Kw {
	case token.Break:
		p.write("break")
	case token.Continue:
		p.write("continue")
	case token.Return:
		p.write("return")
		if n.Value != nil {
			p.write(" ")
			p.expr(n.Value, precLowest)
		}
	case token.Raise:
		p.write("raise ")
		p.expr(n.Value, precLowest)
		if n.From != nil {
			p.write(" from ")
			p.expr(n.From, precLowest)
		}
	}
}

// mapLit prints a map literal, or the empty map '{:}'.
func (p *printer) mapLit(n *ast.MapLit) {
	if len(n.Entries) == 0 {
		p.write("{:}")
		return
	}
	p.write("{")
	for i, e := range n.Entries {
		if i > 0 {
			p.write(", ")
		}
		p.expr(e.Key, precLowest)
		p.write(": ")
		p.expr(e.Value, precLowest)
	}
	p.write("}")
}

// fnExpr prints a closure 'fn(params) -> ret? { body }'.
func (p *printer) fnExpr(n *ast.FnExpr) {
	p.write("fn(")
	for i := range n.Params {
		if i > 0 {
			p.write(", ")
		}
		p.closureParam(&n.Params[i])
	}
	p.write(")")
	if n.Ret != nil {
		p.write(" -> ")
		p.typ(n.Ret)
	}
	p.write(" ")
	p.block(n.Body)
}

func (p *printer) closureParam(cp *ast.ClosureParam) {
	if cp.Ref {
		p.write("mut &")
	}
	p.write(cp.Name)
	if cp.Type != nil {
		p.write(": ")
		p.typ(cp.Type)
	}
	if cp.Default != nil {
		p.write(" = ")
		p.expr(cp.Default, precLowest)
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
	p.head(n.Subject)
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
	case *ast.NamePattern:
		p.write(n.Name)
	case *ast.LitPattern:
		if n.Neg {
			p.write("-")
		}
		p.expr(n.Lit, precLowest)
	case *ast.VariantPattern:
		p.variantPattern(n)
	case *ast.StructPattern:
		p.structPattern(n)
	case *ast.TuplePattern:
		p.patternList("(", ")", n.Elems)
	case *ast.ListPattern:
		p.listPattern(n)
	case *ast.AsPattern:
		p.pattern(n.Inner)
		p.write(" as ")
		p.write(n.Name)
	case *ast.OrPattern:
		for i, alt := range n.Alts {
			if i > 0 {
				p.write(" | ")
			}
			p.pattern(alt)
		}
	case *ast.RangeArm:
		p.rangeArm(n)
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
