// Package parser turns a token stream into an ast.File for the Phase 0 subset of
// Zerg: top-level function declarations, the simple/compound statements, and the
// expression precedence ladder from GRAMMAR group 4. Constructs outside the subset
// (types with '?', pattern matching, generics, closures, ...) are reported as
// diagnostics rather than parsed.
//
// Every node is stamped with its source span, and token trivia is re-homed onto
// the tree (DESIGN-1a §2): the leading trivia of a statement's (or declaration's,
// or match arm's) first token becomes the node's Lead, and the trailing trivia of
// its last token becomes the node's Trail, so 'zerg fmt' can reprint comments and
// blank lines where they stood.
//
// Error recovery is panic/recover based: a parse error records a diagnostic and
// unwinds to the nearest statement or declaration boundary, where the parser
// resynchronizes and continues, so one mistake does not abandon the whole file.
package parser

import (
	"strconv"
	"strings"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/lexer"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// Parse lexes and parses src, returning the file and every diagnostic gathered by
// the lexer and the parser.
func Parse(src string) (*ast.File, []diag.Diagnostic) {
	toks, lexDiags := lexer.Tokenize(src)
	p := &parser{toks: toks}
	for _, d := range lexDiags {
		p.diags.Append(d)
	}
	file := p.parseFile()
	return file, p.diags.Items()
}

type parser struct {
	toks  []token.Token
	pos   int
	diags diag.List
}

// bailout unwinds the stack to the nearest recovery point after a parse error.
type bailout struct{}

// spanned stamps a span on a freshly built node and returns it, so construction
// sites stay one expression (every node embeds ast's base).
func spanned[T interface{ SetSpan(token.Span) }](n T, s token.Span) T {
	n.SetSpan(s)
	return n
}

// trivial is the setter surface every AST node exposes through its embedded
// base; the parser uses it to attach hoisted trivia.
type trivial interface {
	SetLead([]token.Trivia)
	SetTrail([]token.Trivia)
}

// attach hoists trivia onto a node: the leading trivia of its first token and
// the trailing trivia of its last one.
func attach(n ast.Node, lead, trail []token.Trivia) {
	if t, ok := n.(trivial); ok { // every node embeds base, so this always holds
		t.SetLead(lead)
		t.SetTrail(trail)
	}
}

// --- token cursor -------------------------------------------------------------

func (p *parser) cur() token.Token { return p.toks[p.pos] }

func (p *parser) peek(n int) token.Token {
	j := p.pos + n
	if j >= len(p.toks) {
		return p.toks[len(p.toks)-1] // EOF
	}
	return p.toks[j]
}

func (p *parser) at(k token.Kind) bool { return p.cur().Kind == k }

func (p *parser) advance() token.Token {
	t := p.cur()
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) accept(k token.Kind) bool {
	if p.at(k) {
		p.advance()
		return true
	}
	return false
}

func (p *parser) expect(k token.Kind) token.Token {
	if p.at(k) {
		return p.advance()
	}
	p.errorf(p.cur().Span, "expected %q, found %q", k.String(), p.cur().Kind.String())
	panic(bailout{})
}

func (p *parser) skipSemis() {
	for p.at(token.Semi) {
		p.advance()
	}
}

// lead returns the pending leading trivia of the current token — the comments
// and blank lines that stand before the construct about to be parsed.
func (p *parser) lead() []token.Trivia { return p.cur().Leading }

// trailBehind returns the trailing trivia of the most recently consumed token —
// the same-line comment after the construct just parsed.
func (p *parser) trailBehind() []token.Trivia {
	if p.pos == 0 {
		return nil
	}
	return p.toks[p.pos-1].Trailing
}

func (p *parser) errorf(span token.Span, format string, args ...any) {
	p.diags.Add(span, format, args...)
}

func (p *parser) fail(span token.Span, format string, args ...any) {
	p.errorf(span, format, args...)
	panic(bailout{})
}

// --- file & declarations ------------------------------------------------------

func (p *parser) parseFile() *ast.File {
	file := &ast.File{}
	start := p.cur().Span.Start
	for {
		p.skipSemis()
		if p.at(token.EOF) {
			file.End = p.cur().Leading
			break
		}
		lead := p.lead()
		if d := p.tryDecl(); d != nil {
			attach(d, lead, p.trailBehind())
			file.Decls = append(file.Decls, d)
		}
	}
	return spanned(file, token.Span{Start: start, End: p.cur().Span.End})
}

func (p *parser) tryDecl() (d ast.Decl) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			p.syncDecl()
			d = nil
		}
	}()
	return p.parseFuncDecl()
}

// syncDecl advances to the start of the next declaration after an error.
func (p *parser) syncDecl() {
	for !p.at(token.EOF) {
		if p.at(token.Fn) || p.at(token.Pub) {
			return
		}
		p.advance()
	}
}

func (p *parser) parseFuncDecl() ast.Decl {
	start := p.cur().Span.Start
	pub := p.accept(token.Pub) // Phase 0 is single-file; visibility is preserved for fmt but unused by sema
	if !p.at(token.Fn) {
		p.fail(p.cur().Span, "expected a function declaration")
	}
	p.expect(token.Fn)
	name := p.expect(token.Ident)
	p.expect(token.LParen)
	params := p.parseParams()
	p.expect(token.RParen)

	var ret *ast.TypeRef
	if p.accept(token.Arrow) {
		ret = p.parseType()
	}
	body := p.parseBlock()
	return spanned(&ast.FuncDecl{
		Pub:     pub,
		Name:    name.Lexeme,
		NameEnd: name.Span.End,
		Params:  params,
		Ret:     ret,
		Body:    body,
	}, token.Span{Start: start, End: body.Span().End})
}

func (p *parser) parseParams() []ast.Param {
	if p.at(token.RParen) {
		return nil
	}
	var params []ast.Param
	for {
		name := p.expect(token.Ident)
		p.expect(token.Colon)
		typ := p.parseType()
		param := ast.Param{Name: name.Lexeme, Type: typ}
		param.SetSpan(token.Span{Start: name.Span.Start, End: typ.Span().End})
		params = append(params, param)
		if !p.accept(token.Comma) {
			break
		}
	}
	return params
}

// parseType reads a Phase 0 type: a bare built-in name (int/float/bool/str).
func (p *parser) parseType() *ast.TypeRef {
	name := p.expect(token.Ident)
	return spanned(&ast.TypeRef{Name: name.Lexeme}, name.Span)
}

// --- statements ---------------------------------------------------------------

func (p *parser) parseBlock() *ast.Block {
	lb := p.expect(token.LBrace)
	b := &ast.Block{}
	for {
		p.skipSemis()
		if p.at(token.RBrace) || p.at(token.EOF) {
			break
		}
		lead := p.lead()
		if s := p.tryStmt(); s != nil {
			attach(s, lead, p.trailBehind())
			b.Stmts = append(b.Stmts, s)
		}
	}
	rb := p.expect(token.RBrace)
	b.End = rb.Leading // dangling trivia between the last statement and '}'
	return spanned(b, token.Span{Start: lb.Span.Start, End: rb.Span.End})
}

func (p *parser) tryStmt() (s ast.Stmt) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			p.syncStmt()
			s = nil
		}
	}()
	return p.parseStmt()
}

// syncStmt advances to the next statement separator after an error.
func (p *parser) syncStmt() {
	for !p.at(token.Semi) && !p.at(token.RBrace) && !p.at(token.EOF) {
		p.advance()
	}
}

func (p *parser) parseStmt() ast.Stmt {
	t := p.cur()
	switch t.Kind {
	case token.Nop:
		p.advance()
		return spanned(&ast.NopStmt{}, t.Span)
	case token.Print:
		p.advance()
		v := p.parseExpr()
		return spanned(&ast.PrintStmt{Value: v}, token.Span{Start: t.Span.Start, End: v.Span().End})
	case token.Return:
		return p.parseReturn(t)
	case token.Break:
		p.advance()
		return spanned(&ast.BreakStmt{}, t.Span)
	case token.Continue:
		p.advance()
		return spanned(&ast.ContinueStmt{}, t.Span)
	case token.If:
		return p.parseIf()
	case token.For:
		return p.parseFor()
	case token.Mut:
		p.advance()
		return p.parseBinding(t.Span.Start, true, false)
	case token.Const:
		p.advance()
		return p.parseBinding(t.Span.Start, false, true)
	case token.Ident:
		switch p.peek(1).Kind {
		case token.Walrus, token.Colon:
			return p.parseBinding(t.Span.Start, false, false)
		case token.Assign:
			return p.parseReassign()
		}
	}
	// fall through: an expression statement (e.g. a call)
	v := p.parseExpr()
	return spanned(&ast.ExprStmt{X: v}, token.Span{Start: t.Span.Start, End: v.Span().End})
}

func (p *parser) parseReturn(kw token.Token) ast.Stmt {
	p.advance() // 'return'
	r := spanned(&ast.ReturnStmt{}, kw.Span)
	// 'return if c' — a bare conditional early exit (no value)
	if p.at(token.If) {
		p.advance()
		r.Cond = p.parseExpr()
		return spanned(r, token.Span{Start: kw.Span.Start, End: r.Cond.Span().End})
	}
	if p.at(token.Semi) || p.at(token.RBrace) || p.at(token.EOF) {
		return r
	}
	r.Value = p.parseExpr()
	r.SetSpan(token.Span{Start: kw.Span.Start, End: r.Value.Span().End})
	// 'return e if c' — conditional early exit with a value
	if p.accept(token.If) {
		r.Cond = p.parseExpr()
		r.SetSpan(token.Span{Start: kw.Span.Start, End: r.Cond.Span().End})
	}
	return r
}

// parseBinding parses 'name := e' / 'name: T = e' (the leading mut/const keyword,
// when present, has already been consumed).
func (p *parser) parseBinding(start token.Pos, mut, konst bool) ast.Stmt {
	name := p.expect(token.Ident)
	b := &ast.BindStmt{Mut: mut, Const: konst, Name: name.Lexeme}
	switch {
	case p.accept(token.Walrus):
		b.Value = p.parseExpr()
	case p.accept(token.Colon):
		b.Type = p.parseType()
		p.expect(token.Assign)
		b.Value = p.parseExpr()
	default:
		p.fail(p.cur().Span, "expected ':=' or ': type =' in binding")
	}
	return spanned(b, token.Span{Start: start, End: b.Value.Span().End})
}

func (p *parser) parseReassign() ast.Stmt {
	name := p.expect(token.Ident)
	p.expect(token.Assign)
	v := p.parseExpr()
	return spanned(&ast.AssignStmt{Name: name.Lexeme, Value: v},
		token.Span{Start: name.Span.Start, End: v.Span().End})
}

func (p *parser) parseIf() ast.Stmt {
	start := p.expect(token.If).Span.Start
	var branches []ast.IfBranch
	cond := p.parseExpr()
	body := p.parseBlock()
	branches = append(branches, ast.IfBranch{Cond: cond, Body: body})
	end := body.Span().End

	var elseBlock *ast.Block
	for p.accept(token.Else) {
		if p.accept(token.If) {
			c := p.parseExpr()
			b := p.parseBlock()
			branches = append(branches, ast.IfBranch{Cond: c, Body: b})
			end = b.Span().End
			continue
		}
		elseBlock = p.parseBlock()
		end = elseBlock.Span().End
		break
	}
	return spanned(&ast.IfStmt{Branches: branches, Else: elseBlock}, token.Span{Start: start, End: end})
}

func (p *parser) parseFor() ast.Stmt {
	start := p.expect(token.For).Span.Start
	var cond ast.Expr
	if !p.at(token.LBrace) {
		cond = p.parseExpr()
	}
	body := p.parseBlock()
	return spanned(&ast.ForStmt{Cond: cond, Body: body}, token.Span{Start: start, End: body.Span().End})
}

// --- expressions --------------------------------------------------------------

func (p *parser) parseExpr() ast.Expr { return p.parseOr() }

// parseBinaryLeft parses one left-associative precedence level: a `next` operand
// followed by zero or more `op next` pairs whose operator `isOp` accepts.
func (p *parser) parseBinaryLeft(isOp func(token.Kind) bool, next func() ast.Expr) ast.Expr {
	left := next()
	for isOp(p.cur().Kind) {
		op := p.advance()
		right := next()
		left = spanned(&ast.Binary{Op: op.Kind, L: left, R: right}, joinSpan(left, right))
	}
	return left
}

func (p *parser) parseOr() ast.Expr  { return p.parseBinaryLeft(isOrOp, p.parseAnd) }
func (p *parser) parseAnd() ast.Expr { return p.parseBinaryLeft(isAndOp, p.parseCmp) }

// parseCmp is non-associative: at most one comparison operator (GRAMMAR group 4).
func (p *parser) parseCmp() ast.Expr {
	left := p.parseAdd()
	if isCmpOp(p.cur().Kind) {
		op := p.advance()
		right := p.parseAdd()
		return spanned(&ast.Binary{Op: op.Kind, L: left, R: right}, joinSpan(left, right))
	}
	return left
}

func (p *parser) parseAdd() ast.Expr { return p.parseBinaryLeft(isAddOp, p.parseMul) }
func (p *parser) parseMul() ast.Expr { return p.parseBinaryLeft(isMulOp, p.parseUnary) }

func (p *parser) parseUnary() ast.Expr {
	if isUnaryOp(p.cur().Kind) {
		op := p.advance()
		x := p.parseUnary()
		return spanned(&ast.Unary{Op: op.Kind, X: x}, token.Span{Start: op.Span.Start, End: x.Span().End})
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() ast.Expr {
	e := p.parsePrimary()
	for p.at(token.LParen) {
		e = p.parseCall(e)
	}
	return e
}

func (p *parser) parseCall(callee ast.Expr) ast.Expr {
	p.expect(token.LParen)
	var args []ast.Expr
	if !p.at(token.RParen) {
		for {
			args = append(args, p.parseExpr())
			if !p.accept(token.Comma) {
				break
			}
		}
	}
	rp := p.expect(token.RParen)
	return spanned(&ast.Call{Callee: callee, Args: args},
		token.Span{Start: callee.Span().Start, End: rp.Span.End})
}

func (p *parser) parsePrimary() ast.Expr {
	t := p.cur()
	switch t.Kind {
	case token.Int:
		p.advance()
		return spanned(&ast.IntLit{Value: p.parseIntValue(t), Text: t.Lexeme}, t.Span)
	case token.Float:
		p.advance()
		return spanned(&ast.FloatLit{Value: p.parseFloatValue(t)}, t.Span)
	case token.True, token.False:
		p.advance()
		return spanned(&ast.BoolLit{Value: t.Kind == token.True}, t.Span)
	case token.Str:
		p.advance()
		return spanned(&ast.StrLit{Value: t.Str}, t.Span)
	case token.Nil:
		p.advance()
		return spanned(&ast.NilLit{}, t.Span)
	case token.Ident:
		p.advance()
		return spanned(&ast.Ident{Name: t.Lexeme}, t.Span)
	case token.Match:
		return p.parseMatch()
	case token.LParen:
		p.advance()
		e := p.parseExpr()
		p.expect(token.RParen)
		return e
	}
	p.fail(t.Span, "expected an expression, found %q", t.Kind.String())
	return nil // unreachable
}

// parseMatch parses 'match subject { arm+ }' (GRAMMAR group 6). Arms are
// separated like statements (a newline or ';') and each keeps its own line's
// lead/trail trivia.
func (p *parser) parseMatch() ast.Expr {
	start := p.expect(token.Match).Span.Start
	subject := p.parseExpr()
	p.expect(token.LBrace)
	var arms []ast.MatchArm
	for {
		p.skipSemis()
		if p.at(token.RBrace) || p.at(token.EOF) {
			break
		}
		lead := p.lead()
		arm := p.parseMatchArm()
		arm.SetLead(lead)
		arm.SetTrail(p.trailBehind())
		arms = append(arms, arm)
	}
	end := p.expect(token.RBrace).Span.End
	return spanned(&ast.MatchExpr{Subject: subject, Arms: arms}, token.Span{Start: start, End: end})
}

func (p *parser) parseMatchArm() ast.MatchArm {
	pat := p.parsePattern()
	var guard ast.Expr
	if p.accept(token.If) {
		guard = p.parseExpr()
	}
	p.expect(token.FatArrow)
	body := p.parseExpr()
	arm := ast.MatchArm{Pat: pat, Guard: guard, Body: body}
	arm.SetSpan(token.Span{Start: pat.Span().Start, End: body.Span().End})
	return arm
}

// parsePattern parses a Phase 0 pattern: a literal (with optional '-'), a binding
// name, or the wildcard '_'.
func (p *parser) parsePattern() ast.Pattern {
	t := p.cur()
	switch t.Kind {
	case token.Ident:
		p.advance()
		if t.Lexeme == "_" {
			return spanned(&ast.WildPattern{}, t.Span)
		}
		return spanned(&ast.BindPattern{Name: t.Lexeme}, t.Span)
	case token.Minus:
		p.advance()
		if !p.at(token.Int) && !p.at(token.Float) {
			p.fail(p.cur().Span, "expected a number after '-' in a pattern")
		}
		lit := p.parseLiteralNode()
		return spanned(&ast.LitPattern{Lit: lit, Neg: true},
			token.Span{Start: t.Span.Start, End: lit.Span().End})
	case token.Int, token.Float, token.Str, token.True, token.False, token.Nil:
		lit := p.parseLiteralNode()
		return spanned(&ast.LitPattern{Lit: lit}, lit.Span())
	}
	p.fail(t.Span, "expected a pattern, found %q", t.Kind.String())
	return nil
}

// parseLiteralNode parses a single literal token into its expression node.
func (p *parser) parseLiteralNode() ast.Expr {
	t := p.cur()
	switch t.Kind {
	case token.Int:
		p.advance()
		return spanned(&ast.IntLit{Value: p.parseIntValue(t), Text: t.Lexeme}, t.Span)
	case token.Float:
		p.advance()
		return spanned(&ast.FloatLit{Value: p.parseFloatValue(t)}, t.Span)
	case token.Str:
		p.advance()
		return spanned(&ast.StrLit{Value: t.Str}, t.Span)
	case token.True, token.False:
		p.advance()
		return spanned(&ast.BoolLit{Value: t.Kind == token.True}, t.Span)
	case token.Nil:
		p.advance()
		return spanned(&ast.NilLit{}, t.Span)
	}
	p.fail(t.Span, "expected a literal, found %q", t.Kind.String())
	return nil
}

func (p *parser) parseIntValue(t token.Token) int64 {
	s := strings.ReplaceAll(t.Lexeme, "_", "")
	base := 10
	if len(s) >= 2 && s[0] == '0' {
		switch s[1] {
		case 'x', 'X', 'o', 'O', 'b', 'B':
			base = 0 // let strconv read the 0x/0o/0b prefix
		}
	}
	v, err := strconv.ParseInt(s, base, 64)
	if err != nil {
		p.errorf(t.Span, "invalid integer literal %q", t.Lexeme)
		return 0
	}
	return v
}

func (p *parser) parseFloatValue(t token.Token) float64 {
	s := strings.ReplaceAll(t.Lexeme, "_", "")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		p.errorf(t.Span, "invalid float literal %q", t.Lexeme)
		return 0
	}
	return v
}

// --- operator classification --------------------------------------------------

func isOrOp(k token.Kind) bool  { return k == token.Or }
func isAndOp(k token.Kind) bool { return k == token.And }

func isCmpOp(k token.Kind) bool {
	switch k {
	case token.EqEq, token.Ne, token.Lt, token.Gt, token.Le, token.Ge:
		return true
	}
	return false
}

func isAddOp(k token.Kind) bool {
	switch k {
	case token.Plus, token.Minus, token.PlusMod, token.MinusMod, token.Pipe, token.Caret:
		return true
	}
	return false
}

func isMulOp(k token.Kind) bool {
	switch k {
	case token.Star, token.Slash, token.Percent, token.StarMod, token.Shl, token.Shr, token.Amp:
		return true
	}
	return false
}

func isUnaryOp(k token.Kind) bool {
	switch k {
	case token.Minus, token.MinusMod, token.Not, token.Tilde:
		return true
	}
	return false
}

// --- span helpers -------------------------------------------------------------

func joinSpan(l, r ast.Expr) token.Span {
	return token.Span{Start: l.Span().Start, End: r.Span().End}
}
