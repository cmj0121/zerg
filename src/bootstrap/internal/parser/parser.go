// Package parser turns a token stream into an ast.File for the Phase 0 subset of
// Zerg: top-level function declarations, the simple/compound statements, and the
// expression precedence ladder from GRAMMAR group 4. Constructs outside the subset
// (types with '?', pattern matching, generics, closures, ...) are reported as
// diagnostics rather than parsed.
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
	for {
		p.skipSemis()
		if p.at(token.EOF) {
			break
		}
		if d := p.tryDecl(); d != nil {
			file.Decls = append(file.Decls, d)
		}
	}
	return file
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
	p.accept(token.Pub) // Phase 0 is single-file; visibility is accepted but unused
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
	return &ast.FuncDecl{
		Name:    name.Lexeme,
		NameEnd: name.Span.End,
		Params:  params,
		Ret:     ret,
		Body:    body,
		Span:    token.Span{Start: start, End: body.Span.End},
	}
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
		params = append(params, ast.Param{
			Name: name.Lexeme,
			Type: typ,
			Span: token.Span{Start: name.Span.Start, End: typ.Span.End},
		})
		if !p.accept(token.Comma) {
			break
		}
	}
	return params
}

// parseType reads a Phase 0 type: a bare built-in name (int/float/bool/str).
func (p *parser) parseType() *ast.TypeRef {
	name := p.expect(token.Ident)
	return &ast.TypeRef{Name: name.Lexeme, Span: name.Span}
}

// --- statements ---------------------------------------------------------------

func (p *parser) parseBlock() *ast.Block {
	lb := p.expect(token.LBrace)
	var stmts []ast.Stmt
	for {
		p.skipSemis()
		if p.at(token.RBrace) || p.at(token.EOF) {
			break
		}
		if s := p.tryStmt(); s != nil {
			stmts = append(stmts, s)
		}
	}
	rb := p.expect(token.RBrace)
	return &ast.Block{Stmts: stmts, Span: token.Span{Start: lb.Span.Start, End: rb.Span.End}}
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
		return &ast.NopStmt{Span: t.Span}
	case token.Print:
		p.advance()
		v := p.parseExpr()
		return &ast.PrintStmt{Value: v, Span: token.Span{Start: t.Span.Start, End: exprEnd(v)}}
	case token.Return:
		return p.parseReturn(t)
	case token.Break:
		p.advance()
		return &ast.BreakStmt{Span: t.Span}
	case token.Continue:
		p.advance()
		return &ast.ContinueStmt{Span: t.Span}
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
	return &ast.ExprStmt{X: v, Span: token.Span{Start: t.Span.Start, End: exprEnd(v)}}
}

func (p *parser) parseReturn(kw token.Token) ast.Stmt {
	p.advance() // 'return'
	r := &ast.ReturnStmt{Span: kw.Span}
	// 'return if c' — a bare conditional early exit (no value)
	if p.at(token.If) {
		p.advance()
		r.Cond = p.parseExpr()
		r.Span = token.Span{Start: kw.Span.Start, End: exprEnd(r.Cond)}
		return r
	}
	if p.at(token.Semi) || p.at(token.RBrace) || p.at(token.EOF) {
		return r
	}
	r.Value = p.parseExpr()
	r.Span = token.Span{Start: kw.Span.Start, End: exprEnd(r.Value)}
	// 'return e if c' — conditional early exit with a value
	if p.accept(token.If) {
		r.Cond = p.parseExpr()
		r.Span = token.Span{Start: kw.Span.Start, End: exprEnd(r.Cond)}
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
	b.Span = token.Span{Start: start, End: exprEnd(b.Value)}
	return b
}

func (p *parser) parseReassign() ast.Stmt {
	name := p.expect(token.Ident)
	p.expect(token.Assign)
	v := p.parseExpr()
	return &ast.AssignStmt{Name: name.Lexeme, Value: v, Span: token.Span{Start: name.Span.Start, End: exprEnd(v)}}
}

func (p *parser) parseIf() ast.Stmt {
	start := p.expect(token.If).Span.Start
	var branches []ast.IfBranch
	cond := p.parseExpr()
	body := p.parseBlock()
	branches = append(branches, ast.IfBranch{Cond: cond, Body: body})
	end := body.Span.End

	var elseBlock *ast.Block
	for p.accept(token.Else) {
		if p.accept(token.If) {
			c := p.parseExpr()
			b := p.parseBlock()
			branches = append(branches, ast.IfBranch{Cond: c, Body: b})
			end = b.Span.End
			continue
		}
		elseBlock = p.parseBlock()
		end = elseBlock.Span.End
		break
	}
	return &ast.IfStmt{Branches: branches, Else: elseBlock, Span: token.Span{Start: start, End: end}}
}

func (p *parser) parseFor() ast.Stmt {
	start := p.expect(token.For).Span.Start
	var cond ast.Expr
	if !p.at(token.LBrace) {
		cond = p.parseExpr()
	}
	body := p.parseBlock()
	return &ast.ForStmt{Cond: cond, Body: body, Span: token.Span{Start: start, End: body.Span.End}}
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
		left = &ast.Binary{Op: op.Kind, L: left, R: right, Span: joinSpan(left, right)}
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
		return &ast.Binary{Op: op.Kind, L: left, R: right, Span: joinSpan(left, right)}
	}
	return left
}

func (p *parser) parseAdd() ast.Expr { return p.parseBinaryLeft(isAddOp, p.parseMul) }
func (p *parser) parseMul() ast.Expr { return p.parseBinaryLeft(isMulOp, p.parseUnary) }

func (p *parser) parseUnary() ast.Expr {
	if isUnaryOp(p.cur().Kind) {
		op := p.advance()
		x := p.parseUnary()
		return &ast.Unary{Op: op.Kind, X: x, Span: token.Span{Start: op.Span.Start, End: exprEnd(x)}}
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
	return &ast.Call{Callee: callee, Args: args, Span: token.Span{Start: exprStart(callee), End: rp.Span.End}}
}

func (p *parser) parsePrimary() ast.Expr {
	t := p.cur()
	switch t.Kind {
	case token.Int:
		p.advance()
		return &ast.IntLit{Value: p.parseIntValue(t), Span: t.Span}
	case token.Float:
		p.advance()
		return &ast.FloatLit{Value: p.parseFloatValue(t), Span: t.Span}
	case token.True, token.False:
		p.advance()
		return &ast.BoolLit{Value: t.Kind == token.True, Span: t.Span}
	case token.Str:
		p.advance()
		return &ast.StrLit{Value: t.Str, Span: t.Span}
	case token.Nil:
		p.advance()
		return &ast.NilLit{Span: t.Span}
	case token.Ident:
		p.advance()
		return &ast.Ident{Name: t.Lexeme, Span: t.Span}
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
// separated like statements (a newline or ';').
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
		arms = append(arms, p.parseMatchArm())
	}
	end := p.expect(token.RBrace).Span.End
	return &ast.MatchExpr{Subject: subject, Arms: arms, Span: token.Span{Start: start, End: end}}
}

func (p *parser) parseMatchArm() ast.MatchArm {
	pat := p.parsePattern()
	var guard ast.Expr
	if p.accept(token.If) {
		guard = p.parseExpr()
	}
	p.expect(token.FatArrow)
	body := p.parseExpr()
	return ast.MatchArm{Pat: pat, Guard: guard, Body: body}
}

// parsePattern parses a Phase 0 pattern: a literal (with optional '-'), a binding
// name, or the wildcard '_'.
func (p *parser) parsePattern() ast.Pattern {
	t := p.cur()
	switch t.Kind {
	case token.Ident:
		p.advance()
		if t.Lexeme == "_" {
			return &ast.WildPattern{Span: t.Span}
		}
		return &ast.BindPattern{Name: t.Lexeme, Span: t.Span}
	case token.Minus:
		p.advance()
		if !p.at(token.Int) && !p.at(token.Float) {
			p.fail(p.cur().Span, "expected a number after '-' in a pattern")
		}
		lit := p.parseLiteralNode()
		return &ast.LitPattern{Lit: lit, Neg: true, Span: token.Span{Start: t.Span.Start, End: exprEnd(lit)}}
	case token.Int, token.Float, token.Str, token.True, token.False, token.Nil:
		lit := p.parseLiteralNode()
		return &ast.LitPattern{Lit: lit, Span: exprSpan(lit)}
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
		return &ast.IntLit{Value: p.parseIntValue(t), Span: t.Span}
	case token.Float:
		p.advance()
		return &ast.FloatLit{Value: p.parseFloatValue(t), Span: t.Span}
	case token.Str:
		p.advance()
		return &ast.StrLit{Value: t.Str, Span: t.Span}
	case token.True, token.False:
		p.advance()
		return &ast.BoolLit{Value: t.Kind == token.True, Span: t.Span}
	case token.Nil:
		p.advance()
		return &ast.NilLit{Span: t.Span}
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
	return token.Span{Start: exprStart(l), End: exprEnd(r)}
}

func exprStart(e ast.Expr) token.Pos { return exprSpan(e).Start }
func exprEnd(e ast.Expr) token.Pos   { return exprSpan(e).End }

func exprSpan(e ast.Expr) token.Span {
	switch v := e.(type) {
	case *ast.IntLit:
		return v.Span
	case *ast.FloatLit:
		return v.Span
	case *ast.BoolLit:
		return v.Span
	case *ast.StrLit:
		return v.Span
	case *ast.NilLit:
		return v.Span
	case *ast.Ident:
		return v.Span
	case *ast.Unary:
		return v.Span
	case *ast.Binary:
		return v.Span
	case *ast.Call:
		return v.Span
	case *ast.MatchExpr:
		return v.Span
	default:
		return token.Span{}
	}
}
