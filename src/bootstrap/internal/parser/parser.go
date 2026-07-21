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
	toks    []token.Token
	pos     int
	diags   diag.List
	noBrace bool // in an if/for/with/match head: a '{'-opening expr must be parenthesized (DESIGN-1a §3c)
}

// withBraces runs fn with the '{'-opening-expression restriction lifted (inside
// '(' '[' or an f-string hole a leading '{' is an ordinary map/block again),
// restoring the flag afterwards.
func (p *parser) withBraces(fn func() ast.Expr) ast.Expr {
	saved := p.noBrace
	p.noBrace = false
	defer func() { p.noBrace = saved }()
	return fn()
}

// headExpr parses the head of an if/for/with/match in no-brace mode, so a leading
// '{' starts the body block rather than a map/block expression.
func (p *parser) headExpr() ast.Expr {
	saved := p.noBrace
	p.noBrace = true
	defer func() { p.noBrace = saved }()
	return p.parseExpr()
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
		if p.at(token.Fn) || p.at(token.Pub) || p.at(token.Unsafe) || p.at(token.Mut) {
			return
		}
		p.advance()
	}
}

// parseFuncDecl parses 'pub? unsafe? mut? fn name(params) -> ret? block'
// (GRAMMAR group 5; generics '[T]' are group 7, out of this slice's scope).
func (p *parser) parseFuncDecl() ast.Decl {
	start := p.cur().Span.Start
	pub := p.accept(token.Pub) // single-file for now; visibility preserved for fmt
	unsafe := p.accept(token.Unsafe)
	mut := p.accept(token.Mut)
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
		Unsafe:  unsafe,
		Mut:     mut,
		Name:    name.Lexeme,
		NameEnd: name.Span.End,
		Params:  params,
		Ret:     ret,
		Body:    body,
	}, token.Span{Start: start, End: body.Span().End})
}

// parseParams parses a declared parameter list: 'mut &x'? name ': ' type ('=' default)?.
func (p *parser) parseParams() []ast.Param {
	if p.at(token.RParen) {
		return nil
	}
	var params []ast.Param
	for {
		start := p.cur().Span.Start
		ref := p.parseMutRef()
		name := p.expect(token.Ident)
		p.expect(token.Colon)
		typ := p.parseType()
		param := ast.Param{Ref: ref, Name: name.Lexeme, Type: typ}
		end := typ.Span().End
		if p.accept(token.Assign) {
			param.Default = p.parseExpr()
			end = param.Default.Span().End
		}
		param.SetSpan(token.Span{Start: start, End: end})
		params = append(params, param)
		if !p.accept(token.Comma) {
			break
		}
	}
	return params
}

// parseMutRef consumes a leading 'mut &' mutable-reference marker (GRAMMAR group 5).
func (p *parser) parseMutRef() bool {
	if p.at(token.Mut) && p.peek(1).Kind == token.Amp {
		p.advance() // mut
		p.advance() // &
		return true
	}
	return false
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
		case token.LBrace:
			if p.looksLikeStructReassign() {
				return p.parseStructReassign()
			}
		}
	}
	// Otherwise parse an expression; a following '=' makes it a reassignment
	// (assign-target = expr), else it is an expression statement.
	v := p.parseExpr()
	if p.at(token.Assign) {
		return p.finishReassign(v)
	}
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

func (p *parser) parseIf() ast.Stmt {
	start := p.expect(token.If).Span.Start
	var branches []ast.IfBranch
	cond := p.headExpr()
	body := p.parseBlock()
	branches = append(branches, ast.IfBranch{Cond: cond, Body: body})
	end := body.Span().End

	var elseBlock *ast.Block
	for p.accept(token.Else) {
		if p.accept(token.If) {
			c := p.headExpr()
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
		cond = p.headExpr()
	}
	body := p.parseBlock()
	return spanned(&ast.ForStmt{Cond: cond, Body: body}, token.Span{Start: start, End: body.Span().End})
}

// --- expressions --------------------------------------------------------------

func (p *parser) parseExpr() ast.Expr { return p.parseCoalesce() }

// parseCoalesce parses the loosest binary '??' (GRAMMAR group 8), right
// associative; its right side may be a Diverge (break/continue/return/raise).
func (p *parser) parseCoalesce() ast.Expr {
	left := p.parseOr()
	if p.at(token.Coalesce) {
		p.advance()
		right := p.parseCoalesceRHS()
		return spanned(&ast.Coalesce{X: left, Y: right}, joinSpan(left, right))
	}
	return left
}

// parseCoalesceRHS parses the right side of '??': another coalesce-expr, or a
// divergent control-flow escape.
func (p *parser) parseCoalesceRHS() ast.Expr {
	switch p.cur().Kind {
	case token.Break, token.Continue, token.Return, token.Raise:
		return p.parseDiverge()
	}
	return p.parseCoalesce()
}

// parseDiverge parses a '??' right side that never yields a value: 'break',
// 'continue', 'return e?', or 'raise e (from c)?'.
func (p *parser) parseDiverge() ast.Expr {
	t := p.advance()
	d := &ast.Diverge{Kw: t.Kind}
	end := t.Span.End
	switch t.Kind {
	case token.Return:
		if !p.atExprEnd() {
			d.Value = p.parseExpr()
			end = d.Value.Span().End
		}
	case token.Raise:
		d.Value = p.parseExpr()
		end = d.Value.Span().End
		if p.accept(token.From) {
			d.From = p.parseExpr()
			end = d.From.Span().End
		}
	}
	return spanned(d, token.Span{Start: t.Span.Start, End: end})
}

// atExprEnd reports whether the cursor sits where an expression cannot continue —
// a separator or a closing bracket — so an optional operand is absent.
func (p *parser) atExprEnd() bool {
	switch p.cur().Kind {
	case token.Semi, token.RBrace, token.RParen, token.RBrack, token.Comma, token.EOF:
		return true
	}
	return false
}

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

// parseCmp is non-associative: at most one comparison, a type test 'is', or a
// membership test 'in' (GRAMMAR group 4).
func (p *parser) parseCmp() ast.Expr {
	left := p.parseRange()
	switch {
	case isCmpOp(p.cur().Kind):
		op := p.advance()
		right := p.parseRange()
		return spanned(&ast.Binary{Op: op.Kind, L: left, R: right}, joinSpan(left, right))
	case p.at(token.Is):
		p.advance()
		name := p.expect(token.Ident)
		return spanned(&ast.IsExpr{X: left, TypeName: name.Lexeme},
			token.Span{Start: left.Span().Start, End: name.Span.End})
	case p.at(token.In):
		op := p.advance()
		right := p.parseRange()
		return spanned(&ast.Binary{Op: op.Kind, L: left, R: right}, joinSpan(left, right))
	}
	return left
}

// parseRange parses 'lo..hi', 'lo..=hi', or the open range 'lo..' (GRAMMAR group 4);
// a range binds tighter than comparison so 'v in 0..10' reads 'v in (0..10)'.
func (p *parser) parseRange() ast.Expr {
	lo := p.parseAdd()
	switch p.cur().Kind {
	case token.DotDot:
		op := p.advance()
		r := &ast.Range{Lo: lo}
		end := op.Span.End
		if !p.atExprEnd() && !p.at(token.LBrace) {
			r.Hi = p.parseAdd()
			end = r.Hi.Span().End
		}
		return spanned(r, token.Span{Start: lo.Span().Start, End: end})
	case token.DotDotEq:
		p.advance()
		hi := p.parseAdd()
		return spanned(&ast.Range{Lo: lo, Hi: hi, Inclusive: true},
			token.Span{Start: lo.Span().Start, End: hi.Span().End})
	}
	return lo
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

// parsePostfix parses the recv-base then its postfix chain: '.id', '.N', a call
// '(args)', an index/type-args '[elems]', '?', '!', and '?.id' (GRAMMAR group 4).
func (p *parser) parsePostfix() ast.Expr {
	e := p.parseRecvBase()
	for {
		switch p.cur().Kind {
		case token.Dot:
			e = p.parseDot(e)
		case token.OptDot:
			p.advance()
			name := p.expect(token.Ident)
			e = spanned(&ast.OptChain{X: e, Name: name.Lexeme},
				token.Span{Start: e.Span().Start, End: name.Span.End})
		case token.LParen:
			e = p.parseCall(e)
		case token.LBrack:
			e = p.parseBracket(e)
		case token.Question:
			q := p.advance()
			e = spanned(&ast.Try{X: e}, token.Span{Start: e.Span().Start, End: q.Span.End})
		case token.Bang:
			b := p.advance()
			e = spanned(&ast.Force{X: e}, token.Span{Start: e.Span().Start, End: b.Span.End})
		default:
			return e
		}
	}
}

// parseDot parses a '.id' field access or a '.N' tuple index.
func (p *parser) parseDot(e ast.Expr) ast.Expr {
	p.advance() // '.'
	t := p.cur()
	switch t.Kind {
	case token.Ident:
		p.advance()
		return spanned(&ast.Field{X: e, Name: t.Lexeme},
			token.Span{Start: e.Span().Start, End: t.Span.End})
	case token.Int:
		p.advance()
		return spanned(&ast.TupleIndex{X: e, Index: int(p.parseIntValue(t)), Text: t.Lexeme},
			token.Span{Start: e.Span().Start, End: t.Span.End})
	}
	p.fail(t.Span, "expected a field name or tuple index after '.', found %q", t.Kind.String())
	return nil
}

// parseRecvBase parses '<-e' channel receives (right-recursive) or a primary
// (GRAMMAR group 4/9).
func (p *parser) parseRecvBase() ast.Expr {
	if p.at(token.LArrow) {
		arrow := p.advance()
		x := p.parseRecvBase()
		return spanned(&ast.Recv{X: x}, token.Span{Start: arrow.Span.Start, End: x.Span().End})
	}
	return p.parsePrimary()
}

// parseCall parses a call's argument list: positional arguments first, then named
// 'name: value' arguments (GRAMMAR group 5).
func (p *parser) parseCall(callee ast.Expr) ast.Expr {
	p.expect(token.LParen)
	var args []ast.Arg
	if !p.at(token.RParen) {
		p.withBraces(func() ast.Expr {
			for {
				args = append(args, p.parseArg())
				if !p.accept(token.Comma) {
					break
				}
			}
			return nil
		})
	}
	rp := p.expect(token.RParen)
	return spanned(&ast.Call{Callee: callee, Args: args},
		token.Span{Start: callee.Span().Start, End: rp.Span.End})
}

// parseArg parses one call argument, named when an 'identifier :' leads it.
func (p *parser) parseArg() ast.Arg {
	if p.at(token.Ident) && p.peek(1).Kind == token.Colon {
		name := p.advance()
		p.advance() // ':'
		return ast.Arg{Name: name.Lexeme, Value: p.parseExpr()}
	}
	return ast.Arg{Value: p.parseExpr()}
}

// parseBracket parses the provisional '[elems]' postfix (index or type-args,
// DESIGN-1a §3a); a comma marks it as unambiguously type arguments.
func (p *parser) parseBracket(base ast.Expr) ast.Expr {
	p.expect(token.LBrack)
	var elems []ast.Expr
	comma := false
	p.withBraces(func() ast.Expr {
		for {
			elems = append(elems, p.parseExpr())
			if !p.accept(token.Comma) {
				break
			}
			comma = true
		}
		return nil
	})
	rb := p.expect(token.RBrack)
	return spanned(&ast.Bracket{Base: base, Elems: elems, Comma: comma},
		token.Span{Start: base.Span().Start, End: rb.Span.End})
}

func (p *parser) parsePrimary() ast.Expr {
	t := p.cur()
	switch t.Kind {
	case token.Int:
		p.advance()
		return spanned(&ast.IntLit{Value: p.parseIntValue(t), Text: t.Lexeme}, t.Span)
	case token.Float:
		p.advance()
		return spanned(&ast.FloatLit{Value: p.parseFloatValue(t), Text: t.Lexeme}, t.Span)
	case token.True, token.False:
		p.advance()
		return spanned(&ast.BoolLit{Value: t.Kind == token.True}, t.Span)
	case token.Str:
		p.advance()
		return spanned(&ast.StrLit{Value: t.Str}, t.Span)
	case token.RawStr:
		p.advance()
		return spanned(&ast.RawStrLit{Text: t.Lexeme, Value: t.Str}, t.Span)
	case token.Rune:
		p.advance()
		return spanned(&ast.RuneLit{Text: t.Lexeme, Value: decodeRune(t.Str)}, t.Span)
	case token.Byte:
		p.advance()
		return spanned(&ast.ByteLit{Text: t.Lexeme, Value: decodeByte(t.Str)}, t.Span)
	case token.Cmd:
		p.advance()
		return spanned(&ast.CmdLit{Text: t.Lexeme, Value: t.Str}, t.Span)
	case token.Nil:
		p.advance()
		return spanned(&ast.NilLit{}, t.Span)
	case token.Ident:
		p.advance()
		return spanned(&ast.Ident{Name: t.Lexeme}, t.Span)
	case token.This:
		p.advance()
		return spanned(&ast.Ident{Name: "this"}, t.Span)
	case token.Match:
		return p.parseMatch()
	case token.Fn:
		return p.parseFnExpr()
	case token.FStrBegin:
		return p.parseFStr()
	case token.FCmdBegin:
		return p.parseFCmd()
	case token.LParen:
		return p.parseParenOrTuple()
	case token.LBrack:
		return p.parseListLit()
	case token.LBrace:
		if !p.noBrace {
			return p.parseBraceExpr()
		}
	}
	p.fail(t.Span, "expected an expression, found %q", t.Kind.String())
	return nil // unreachable
}

// parseMatch parses 'match subject { arm+ }' (GRAMMAR group 6). Arms are
// separated like statements (a newline or ';') and each keeps its own line's
// lead/trail trivia.
func (p *parser) parseMatch() ast.Expr {
	start := p.expect(token.Match).Span.Start
	subject := p.headExpr()
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
		return spanned(&ast.FloatLit{Value: p.parseFloatValue(t), Text: t.Lexeme}, t.Span)
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
