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
// the trailing trivia of its last one. The lead is PREPENDED to whatever the
// node already carries, so a declaration that stashed the trivia sitting between
// its decorator prefix and its keyword (parseDecl, follow-up F1) keeps it —
// ordinary nodes start with no lead, for which prepend is just assignment.
func attach(n ast.Node, lead, trail []token.Trivia) {
	if t, ok := n.(trivial); ok { // every node embeds base, so this always holds
		if existing := n.Lead(); len(existing) > 0 {
			lead = append(append([]token.Trivia{}, lead...), existing...)
		}
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
	// An Illegal token was already diagnosed by the lexer (unterminated string,
	// malformed number, stray character); do not stack a second, redundant
	// message on top of it — the lexer's is the precise one.
	if p.cur().Kind != token.Illegal {
		p.errorf(p.cur().Span, "expected %s, found %s", describe(k), describe(p.cur().Kind))
	}
	panic(bailout{})
}

// describe renders a token kind as a human-readable phrase for diagnostics: a
// category noun for the open-ended lexical classes ("an identifier", "a string
// literal", "end of file") and the quoted spelling for the fixed keywords and
// punctuation ("'}'", "':='"). It keeps every "expected …, found …" message
// readable instead of leaking the internal token names.
func describe(k token.Kind) string {
	switch k {
	case token.EOF:
		return "end of file"
	case token.Illegal:
		return "invalid input"
	case token.Ident:
		return "an identifier"
	case token.Int:
		return "an integer literal"
	case token.Float:
		return "a float literal"
	case token.Str:
		return "a string literal"
	case token.RawStr:
		return "a raw string literal"
	case token.Rune:
		return "a rune literal"
	case token.Byte:
		return "a byte literal"
	case token.Cmd:
		return "a command literal"
	}
	return "'" + k.String() + "'"
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
	// As in expect: a lexer-flagged Illegal token at the cursor already carries a
	// precise diagnostic, so suppress the parser's follow-on message and just
	// recover, keeping exactly one diagnostic per real error.
	if p.cur().Kind != token.Illegal {
		p.errorf(span, format, args...)
	}
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
		before := p.pos
		lead := p.lead()
		if s := p.tryTopStmt(); s != nil {
			attach(s, lead, p.trailBehind())
			file.Items = append(file.Items, s)
			p.requireStmtSep()
		} else if p.pos == before {
			// A recovered error whose sync made no progress would spin forever;
			// force one token of progress. The error was already diagnosed by the
			// bailout, so this only guarantees termination.
			p.advance()
		}
	}
	return spanned(file, token.Span{Start: start, End: p.cur().Span.End})
}

// tryTopStmt parses one top-level statement, recovering to the next declaration
// boundary on a parse error.
func (p *parser) tryTopStmt() (s ast.Stmt) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			p.syncDecl()
			s = nil
		}
	}()
	return p.parseTopStmt()
}

// parseTopStmt parses one item of the program's top-level stmt-list. Zerg's top
// level is a full statement list (GRAMMAR:36 'program ::= stmt-list') because the
// language supports SCRIPT MODE, so any statement is legal here — not only the
// module surface. This routes the items that need top-level-specific treatment
// (imports, the 'unsafe { … }' declaration GROUP vs. the block-expression, and
// decorated / visibility-prefixed declarations) and otherwise falls through to
// parseStmt so a bare 'print'/'if'/'for'/expression/etc. parses at the top level.
// (Script-vs-compile-mode semantics — top-level statements run in script mode,
// are inert in compile mode — are a 1b concern; 1a only parses and round-trips.)
func (p *parser) parseTopStmt() ast.Stmt {
	switch {
	case p.at(token.Import):
		return p.parseImport()
	case p.at(token.Hash), p.at(token.Pub):
		// '#[…]' or a 'pub' prefix can only introduce a declaration.
		return p.parseDecl()
	case p.startsDecl():
		// struct/enum/spec/impl/type/init/fn, possibly behind unsafe/mut modifiers.
		return p.parseDecl()
	}
	return p.parseStmt()
}

// startsDecl reports whether the cursor begins a declaration (a decl keyword,
// possibly behind pub/unsafe/mut modifiers), as opposed to an ordinary statement.
func (p *parser) startsDecl() bool {
	switch p.declHeadKind() {
	case token.Struct, token.Enum, token.Spec, token.Impl, token.Type, token.Init, token.Fn:
		return true
	}
	return false
}

// syncDecl advances to the start of the next declaration after an error.
func (p *parser) syncDecl() {
	for !p.at(token.EOF) {
		switch p.cur().Kind {
		case token.Fn, token.Pub, token.Unsafe, token.Mut, token.Struct, token.Enum,
			token.Spec, token.Impl, token.Type, token.Init, token.Hash:
			return
		}
		p.advance()
	}
}

// parseFuncDecl parses 'pub? unsafe? mut? fn name generics? (params) -> ret? block'
// (GRAMMAR group 5, with the group 7 generics). It also serves as an impl method
// or a spec's provided method.
func (p *parser) parseFuncDecl() ast.Decl {
	start := p.cur().Span.Start
	pub := p.accept(token.Pub) // single-file for now; visibility preserved for fmt
	mut := p.accept(token.Mut)
	if !p.at(token.Fn) {
		p.fail(p.cur().Span, "expected a function declaration")
	}
	p.expect(token.Fn)
	name := p.expect(token.Ident)
	var generics *ast.Generics
	if p.at(token.LBrack) {
		generics = p.parseGenerics()
	}
	p.expect(token.LParen)
	params := p.parseParams()
	p.expect(token.RParen)

	var ret ast.Type
	if p.accept(token.Arrow) {
		ret = p.parseType()
	}
	body := p.parseBlock()
	return spanned(&ast.FuncDecl{
		Pub:      pub,
		Mut:      mut,
		Name:     name.Lexeme,
		NameEnd:  name.Span.End,
		Generics: generics,
		Params:   params,
		Ret:      ret,
		Body:     body,
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

// --- statements ---------------------------------------------------------------

func (p *parser) parseBlock() *ast.Block {
	// A block body is never a head, so the '{'-opening-expression restriction is
	// lifted inside it (a leading '{' statement is a block/map again); it is
	// restored on the way out for the enclosing head.
	saved := p.noBrace
	p.noBrace = false
	defer func() { p.noBrace = saved }()

	lb := p.expect(token.LBrace)
	b := &ast.Block{}
	for {
		p.skipSemis()
		if p.at(token.RBrace) || p.at(token.EOF) {
			break
		}
		before := p.pos
		lead := p.lead()
		if s := p.tryStmt(); s != nil {
			attach(s, lead, p.trailBehind())
			b.Stmts = append(b.Stmts, s)
			p.requireStmtSep()
		} else if p.pos == before {
			p.advance() // guarantee progress (see parseFile) so recovery cannot spin
		}
	}
	if !p.at(token.RBrace) { // the loop only exits early at EOF
		p.fail(p.cur().Span, "unclosed block: expected '}' to close the '{' opened at %s, found %s",
			lb.Span.Start, describe(p.cur().Kind))
	}
	rb := p.advance()
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

// requireStmtSep enforces GRAMMAR:41 — statements in a stmt-list are separated by
// stmt-sep+ (a newline, which the lexer turns into an ASI ';', or an explicit
// ';'). After a statement the cursor must be at a ';', a closing '}', or EOF; any
// other token means two statements share a line with no separator, so it reports
// a span-anchored diagnostic. Inside an unclosed '('/'[' the lexer suppresses ASI
// and the whole thing is one expression, so this is only ever reached between two
// genuine statements. A leading '{' gets a targeted hint: Zerg has no struct
// brace-literal, so 'x := T{a: 1}' is really 'x := T' followed by a stray map —
// the value is built with a call, 'T(a: 1)'.
func (p *parser) requireStmtSep() {
	if p.at(token.Semi) || p.at(token.RBrace) || p.at(token.EOF) {
		return
	}
	if p.at(token.LBrace) {
		p.errorf(p.cur().Span, "expected a newline or ';' to separate statements; "+
			"a struct value is written as a call like T(a: 1), not with braces")
		return
	}
	p.errorf(p.cur().Span, "expected a newline or ';' to separate statements, found %s",
		describe(p.cur().Kind))
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
		return p.parseBreakContinue(true)
	case token.Continue:
		return p.parseBreakContinue(false)
	case token.If:
		return p.parseIf()
	case token.For:
		return p.parseFor()
	case token.With:
		return p.parseWith()
	case token.Raise:
		return p.parseRaise()
	case token.Spawn:
		return p.parseSpawn()
	case token.Defer:
		return p.parseDefer()
	case token.Del:
		return p.parseDel()
	case token.Select:
		return p.parseSelect()
	case token.Mut:
		p.advance()
		return p.parseBinding(t.Span.Start, true, false)
	case token.Const:
		p.advance()
		return p.parseBinding(t.Span.Start, false, true)
	case token.LParen:
		// '(a, b) := e' — a tuple destructuring bind; otherwise fall through to the
		// expression path (a parenthesized expression or a tuple literal statement).
		if p.looksLikeTupleBind() {
			return p.parseBinding(t.Span.Start, false, false)
		}
	case token.Ident:
		switch p.peek(1).Kind {
		case token.Walrus, token.Colon:
			return p.parseBinding(t.Span.Start, false, false)
		case token.LBrace:
			// 'P{x, y} := e' — a struct destructuring bind; else 'P{x, y} = e' reassign.
			if p.looksLikeStructBind() {
				return p.parseBinding(t.Span.Start, false, false)
			}
			if p.looksLikeStructReassign() {
				return p.parseStructReassign()
			}
		}
	}
	// Otherwise parse an expression; a following '=' makes it a reassignment
	// (assign-target = expr), a following '<-' a send statement (ch <- v), else
	// it is an expression statement.
	v := p.parseExpr()
	if p.at(token.Assign) {
		return p.finishReassign(v)
	}
	if p.at(token.LArrow) {
		return p.finishSend(v)
	}
	return spanned(&ast.ExprStmt{X: v}, token.Span{Start: t.Span.Start, End: v.Span().End})
}

func (p *parser) parseReturn(kw token.Token) ast.Stmt {
	p.advance() // 'return'
	r := spanned(&ast.ReturnStmt{}, kw.Span)
	// A leading 'if' is disambiguated: an if-expression being returned, or a bare
	// conditional early exit (GRAMMAR group 5).
	if p.at(token.If) {
		return p.parseReturnIf(kw, r)
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

// parseReturnIf disambiguates a leading 'if' after 'return' (GRAMMAR group 5): an
// 'if' whose condition is followed by a '{' block is an if-EXPRESSION being returned
// ('return if c { a } else { b }'), so the whole if-expression becomes the return
// value; otherwise it is a bare conditional early exit with no value ('return if c').
func (p *parser) parseReturnIf(kw token.Token, r *ast.ReturnStmt) ast.Stmt {
	ifKw := p.expect(token.If)
	br := p.parseIfHead()
	if !p.at(token.LBrace) {
		// bare conditional early exit: 'return if c' (no value).
		r.Cond = br.Cond
		return spanned(r, span(kw.Span.Start, br.Cond.Span().End))
	}
	// the leading 'if' is an if-expression being returned; parse its block and continue
	// the else chain, then hang the whole if-expression off the return's value.
	br.Body = p.parseBlock()
	branches, elseBlock, end := p.continueIfChain(br)
	if elseBlock == nil {
		p.fail(span(ifKw.Span.Start, end), "an if-expression requires a trailing 'else'")
	}
	r.Value = spanned(&ast.IfExpr{Branches: branches, Else: elseBlock}, span(ifKw.Span.Start, end))
	return spanned(r, span(kw.Span.Start, end))
}

// parseBinding parses 'name := e' / 'name: T = e', or a destructuring bind
// 'tuple-pat := e' / 'struct-pat := e' (the leading mut/const keyword, when present,
// has already been consumed). A destructuring bind takes the inferred ':=' form only
// (GRAMMAR bind-target).
func (p *parser) parseBinding(start token.Pos, mut, konst bool) ast.Stmt {
	if p.atBindTarget() {
		target := p.parseBindTarget()
		p.expect(token.Walrus)
		val := p.parseExpr()
		return spanned(&ast.BindStmt{Mut: mut, Const: konst, Target: target, Value: val},
			token.Span{Start: start, End: val.Span().End})
	}
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

// atBindTarget reports whether the cursor begins a destructuring bind target — a
// tuple '(…)' or a struct 'Type{…}' pattern — as opposed to a plain identifier name.
func (p *parser) atBindTarget() bool {
	return p.at(token.LParen) || (p.at(token.Ident) && p.peek(1).Kind == token.LBrace)
}

// parseBindTarget parses a destructuring bind-target pattern: a tuple '(a, b)' or a
// struct 'Type{x, y}' shape (its leaves are names or nested tuple/struct patterns), or
// a bare identifier NamePattern (GRAMMAR bind-target). It reuses the group-6 pattern
// parsers, so a nested '(a, (b, c))' works.
func (p *parser) parseBindTarget() ast.Pattern {
	switch {
	case p.at(token.LParen):
		return p.parseTuplePattern()
	case p.at(token.Ident) && p.peek(1).Kind == token.LBrace:
		name := p.advance()
		return p.parseStructPattern(name)
	default:
		id := p.expect(token.Ident)
		return spanned(&ast.NamePattern{Name: id.Lexeme}, id.Span)
	}
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
	return p.parseChain(p.parseRecvBase(), true)
}

// parseChain applies a postfix chain to e. withTry decides whether '?' and '!' belong to
// it, and the two answers are what put a receive at the right level.
//
// A receive's OPERAND takes the chain without them: `<-a.b` is a receive from the field
// `a.b`, not a field of `<-a` (GRAMMAR group 9 — recv-base is its own level). Without
// that, a channel held in a struct field could be sent on but never received from, and
// the diagnostic named the struct: _receive '<-' requires a channel, found Hub_.
//
// The receive's RESULT takes them: `<-ch!` forces the Result the receive produced, which
// is what the corpus and the chapter write. Applied to the operand instead it would force
// the CHANNEL, which is not a Result at all.
func (p *parser) parseChain(e ast.Expr, withTry bool) ast.Expr {
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
			if !withTry {
				return e
			}
			q := p.advance()
			e = spanned(&ast.Try{X: e}, token.Span{Start: e.Span().Start, End: q.Span.End})
		case token.Bang:
			if !withTry {
				return e
			}
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
	p.fail(t.Span, "expected a field name or tuple index after '.', found %s", describe(t.Kind))
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
	return p.parseChain(p.parsePrimary(), false)
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
	case token.If:
		return p.parseIfExpr()
	case token.Guard:
		return p.parseGuard()
	case token.Fn:
		return p.parseFnExpr()
	case token.FStrBegin:
		return p.parseFStr()
	case token.FCmdBegin:
		return p.parseFCmd()
	case token.Chan:
		return p.parseChanNew()
	case token.LParen:
		return p.parseParenOrTuple()
	case token.LBrack:
		return p.parseListLit()
	case token.LBrace:
		if !p.noBrace {
			return p.parseBraceExpr()
		}
	}
	p.fail(t.Span, "expected an expression, found %s", describe(t.Kind))
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
	sawArm := false // did the source have any arm at all (even a malformed one)?
	for {
		p.skipSemis()
		if p.at(token.RBrace) || p.at(token.EOF) {
			break
		}
		sawArm = true
		lead := p.lead()
		arm, ok := p.tryMatchArm()
		if !ok {
			// a malformed arm was diagnosed and skipped; syncArm left the cursor
			// at the next arm separator or the match's own '}', so the remaining
			// arms (and the match) still parse instead of cascading.
			continue
		}
		arm.SetLead(lead)
		arm.SetTrail(p.trailBehind())
		arms = append(arms, arm)
	}
	rb := p.expect(token.RBrace)
	if !sawArm {
		// GRAMMAR:421 'match-body ::= match-arm+' — at least one arm is required.
		// This is syntactic arity (not 1b exhaustiveness); anchor it at the match.
		// Only reported when the braces were genuinely empty: a malformed arm that
		// was diagnosed and skipped already carries its own error, so this must not
		// pile a second diagnostic on top of it.
		p.errorf(token.Span{Start: start, End: rb.Span.End}, "a match needs at least one arm")
	}
	return spanned(&ast.MatchExpr{Subject: subject, Arms: arms}, token.Span{Start: start, End: rb.Span.End})
}

// tryMatchArm parses one match arm, recovering within the match's braces on a
// parse error: it reports the error, syncs to the next arm separator or the
// closing '}', and returns ok=false so the arm is skipped without unwinding past
// the match (which would leave the braces unbalanced and cascade).
func (p *parser) tryMatchArm() (arm ast.MatchArm, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, isBail := r.(bailout); !isBail {
				panic(r)
			}
			p.syncArm()
			ok = false
		}
	}()
	return p.parseMatchArm(), true
}

// syncArm advances to the next arm/entry separator after a parse error inside a
// braced construct (match/select/...): a statement separator or the enclosing
// '}', neither of which it consumes, so the construct's own loop resumes cleanly.
func (p *parser) syncArm() {
	for !p.at(token.Semi) && !p.at(token.RBrace) && !p.at(token.EOF) {
		p.advance()
	}
}

func (p *parser) parseMatchArm() ast.MatchArm {
	pat := p.parseArmPattern()
	var guard ast.Expr
	if p.accept(token.If) {
		guard = p.parseExpr()
	}
	if !p.at(token.FatArrow) {
		p.fail(p.cur().Span, "a match arm needs '=>' between its pattern and its body, found %s",
			describe(p.cur().Kind))
	}
	p.advance() // '=>'
	body := p.parseExpr()
	arm := ast.MatchArm{Pat: pat, Guard: guard, Body: body}
	arm.SetSpan(token.Span{Start: pat.Span().Start, End: body.Span().End})
	return arm
}

// parseLiteralNode parses a single literal token into its expression node — the
// literals a match literal-pattern or a range-arm bound may carry.
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
	case token.RawStr:
		p.advance()
		return spanned(&ast.RawStrLit{Text: t.Lexeme, Value: t.Str}, t.Span)
	case token.Rune:
		p.advance()
		return spanned(&ast.RuneLit{Text: t.Lexeme, Value: decodeRune(t.Str)}, t.Span)
	case token.Byte:
		p.advance()
		return spanned(&ast.ByteLit{Text: t.Lexeme, Value: decodeByte(t.Str)}, t.Span)
	case token.True, token.False:
		p.advance()
		return spanned(&ast.BoolLit{Value: t.Kind == token.True}, t.Span)
	case token.Nil:
		p.advance()
		return spanned(&ast.NilLit{}, t.Span)
	}
	p.fail(t.Span, "expected a literal, found %s", describe(t.Kind))
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
