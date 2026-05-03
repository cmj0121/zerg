package syntax

import (
	"fmt"
)

// ParseError is returned when the parser cannot fit the token stream into the
// v0.1 grammar.
type ParseError struct {
	Pos     Position
	Message string
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	return fmt.Sprintf("parse error at %s: %s", e.Pos, e.Message)
}

// Parse consumes a token slice (typically produced by Lex) and returns the
// program AST. Leading, trailing, and repeated NEWLINE tokens are tolerated;
// the v0.0 examples both contain a shebang+comment block followed by blank
// lines, so this is required for the parity test to even reach the
// statements.
func Parse(tokens []Token) (*Program, error) {
	p := newParser(tokens)
	return p.parseProgram()
}

// ParseStatement parses exactly one statement from a single line of input —
// the shape the REPL feeds in. Trailing whitespace/EOF is fine, but a second
// statement on the same line is rejected.
func ParseStatement(tokens []Token) (Stmt, error) {
	p := newParser(tokens)
	p.skipNewlines()
	if p.peek().Kind == KindEOF {
		return nil, nil
	}
	stmt, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	p.skipNewlines()
	if p.peek().Kind != KindEOF {
		t := p.peek()
		return nil, &ParseError{
			Pos:     t.Pos,
			Message: fmt.Sprintf("unexpected %s after statement", t.Kind),
		}
	}
	return stmt, nil
}

// ---------------------------------------------------------------------------
// parser state and primitives.
//
// `parenDepth` tracks open `(` and `[`. While > 0 the parser silently skips
// NEWLINE tokens, giving line-continuation semantics inside parens and
// brackets. This matches the lexer-emits-newlines-always design — we keep
// the lexer simple and let context-sensitive significance live here. PLAN
// allows either approach; this choice was made because realistic v0.1
// programs (e.g. multi-line argument lists) read better than forcing
// trailing-operator continuation.
// ---------------------------------------------------------------------------

type parser struct {
	tokens     []Token
	pos        int
	parenDepth int
}

func newParser(tokens []Token) *parser {
	return &parser{tokens: tokens}
}

// peek returns the current token without consuming it. While inside a paren
// or bracket group, NEWLINE tokens are transparent — peek skips past them.
func (p *parser) peek() Token {
	for {
		if p.pos >= len(p.tokens) {
			return Token{Kind: KindEOF}
		}
		t := p.tokens[p.pos]
		if p.parenDepth > 0 && t.Kind == KindNewline {
			p.pos++
			continue
		}
		return t
	}
}

// peekRaw returns the current token without skipping NEWLINE — used by the
// statement-level loop where NEWLINE is significant.
func (p *parser) peekRaw() Token {
	if p.pos >= len(p.tokens) {
		return Token{Kind: KindEOF}
	}
	return p.tokens[p.pos]
}

// advance consumes and returns the current significant token (after any
// NEWLINE-skip done by peek when parenDepth > 0).
func (p *parser) advance() Token {
	t := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return t
}

// skipNewlines drops any number of NEWLINE tokens at the current cursor.
func (p *parser) skipNewlines() {
	for p.pos < len(p.tokens) && p.tokens[p.pos].Kind == KindNewline {
		p.pos++
	}
}

// expect consumes a token of the given kind or returns a parse error tagged
// with the offending token's position.
func (p *parser) expect(k Kind, ctx string) (Token, error) {
	t := p.peek()
	if t.Kind != k {
		return Token{}, &ParseError{
			Pos:     t.Pos,
			Message: fmt.Sprintf("expected %s%s, got %s", k, ctxSuffix(ctx), t.Kind),
		}
	}
	return p.advance(), nil
}

func ctxSuffix(ctx string) string {
	if ctx == "" {
		return ""
	}
	return " " + ctx
}

// errorAt builds a ParseError at the given position.
func errorAt(pos Position, format string, args ...any) error {
	return &ParseError{Pos: pos, Message: fmt.Sprintf(format, args...)}
}

// ---------------------------------------------------------------------------
// Top-level program and statement terminator handling.
// ---------------------------------------------------------------------------

func (p *parser) parseProgram() (*Program, error) {
	prog := &Program{}
	for {
		p.skipNewlines()
		if p.peekRaw().Kind == KindEOF {
			return prog, nil
		}
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		prog.Statements = append(prog.Statements, stmt)
		if err := p.terminateStatement(); err != nil {
			return nil, err
		}
	}
}

// terminateStatement consumes the NEWLINE (or EOF) that ends a statement at
// either the top level or inside a block. It is NOT called for statements
// like FnDecl/IfStmt/ForStmt that end with `}` — those manage their own
// trailing whitespace.
func (p *parser) terminateStatement() error {
	t := p.peekRaw()
	switch t.Kind {
	case KindNewline:
		p.pos++
		return nil
	case KindEOF:
		return nil
	case KindRBrace:
		// A closing brace ends a block; the block parser handles consuming
		// it. The last statement before the brace is allowed to omit the
		// trailing NEWLINE.
		return nil
	default:
		return errorAt(t.Pos, "expected newline or end of statement, got %s", t.Kind)
	}
}

// ---------------------------------------------------------------------------
// Statements.
// ---------------------------------------------------------------------------

func (p *parser) parseStatement() (Stmt, error) {
	t := p.peek()
	switch t.Kind {
	case KindNop:
		p.advance()
		return &NopStmt{Pos: t.Pos}, nil
	case KindPrint:
		return p.parsePrint()
	case KindLet:
		return p.parseDecl(declLet)
	case KindMut:
		return p.parseDecl(declMut)
	case KindConst:
		return p.parseDecl(declConst)
	case KindFn:
		return p.parseFnDecl()
	case KindIf:
		return p.parseIfStmt()
	case KindFor:
		return p.parseForStmt()
	case KindReturn:
		return p.parseReturnStmt()
	case KindBreak:
		return p.parseBreakLikeStmt(true)
	case KindContinue:
		return p.parseBreakLikeStmt(false)
	default:
		return p.parseExprOrAssignStmt()
	}
}

// parsePrint handles `print expr`. The expression is parsed by the full
// expression parser, so v0.1 programs can `print x + 1` etc.
func (p *parser) parsePrint() (Stmt, error) {
	pt := p.advance() // consume `print`
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &PrintStmt{Pos: pt.Pos, Expr: expr}, nil
}

type declKind int

const (
	declLet declKind = iota
	declMut
	declConst
)

// parseDecl handles let/mut/const with both `name := expr` and
// `name: T = expr` forms.
func (p *parser) parseDecl(kind declKind) (Stmt, error) {
	keyword := p.advance() // consume let/mut/const
	nameTok, err := p.expect(KindIdent, "after "+kindLabel(kind))
	if err != nil {
		return nil, err
	}

	var typeRef *TypeRef
	switch p.peek().Kind {
	case KindWalrus:
		// `:=` ⇒ no annotation, infer from RHS.
		p.advance()
	case KindColon:
		p.advance()
		tr, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		typeRef = tr
		if _, err := p.expect(KindAssign, "after type annotation"); err != nil {
			return nil, err
		}
	default:
		t := p.peek()
		return nil, errorAt(t.Pos, "expected ':=' or ': T =' after %s name, got %s", kindLabel(kind), t.Kind)
	}

	value, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	switch kind {
	case declLet:
		return &LetStmt{Pos: keyword.Pos, Name: nameTok.Value, Type: typeRef, Value: value}, nil
	case declMut:
		return &MutStmt{Pos: keyword.Pos, Name: nameTok.Value, Type: typeRef, Value: value}, nil
	case declConst:
		return &ConstStmt{Pos: keyword.Pos, Name: nameTok.Value, Type: typeRef, Value: value}, nil
	}
	// Unreachable: declKind exhausts to three values handled above.
	return nil, errorAt(keyword.Pos, "internal error: unknown decl kind")
}

func kindLabel(k declKind) string {
	switch k {
	case declLet:
		return "let"
	case declMut:
		return "mut"
	case declConst:
		return "const"
	}
	return "<decl>"
}

// parseTypeRef parses a single identifier as a type name. Compound type
// syntax is not in v0.1.
func (p *parser) parseTypeRef() (*TypeRef, error) {
	t := p.peek()
	if t.Kind != KindIdent {
		return nil, errorAt(t.Pos, "expected type name, got %s", t.Kind)
	}
	p.advance()
	return &TypeRef{Name: t.Value, Pos: t.Pos}, nil
}

// parseFnDecl handles `fn name(p1: T1, p2: T2) -> R { body }`.
func (p *parser) parseFnDecl() (Stmt, error) {
	kw := p.advance() // consume `fn`
	nameTok, err := p.expect(KindIdent, "after 'fn'")
	if err != nil {
		return nil, err
	}
	if _, err := p.expectParen(KindLParen, "in function signature"); err != nil {
		return nil, err
	}

	var params []FnParam
	if p.peek().Kind != KindRParen {
		for {
			pname, err := p.expect(KindIdent, "in parameter list")
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(KindColon, "after parameter name"); err != nil {
				return nil, err
			}
			ptype, err := p.parseTypeRef()
			if err != nil {
				return nil, err
			}
			params = append(params, FnParam{
				Name: pname.Value,
				Type: ptype,
				Pos:  pname.Pos,
			})
			if p.peek().Kind == KindComma {
				p.advance()
				continue
			}
			break
		}
	}
	if _, err := p.expectParen(KindRParen, "in function signature"); err != nil {
		return nil, err
	}

	var ret *TypeRef
	if p.peek().Kind == KindArrow {
		p.advance()
		tr, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		ret = tr
	}

	body, err := p.parseBlock("function body")
	if err != nil {
		return nil, err
	}
	return &FnDecl{
		Pos:    kw.Pos,
		Name:   nameTok.Value,
		Params: params,
		Return: ret,
		Body:   body,
	}, nil
}

// parseIfStmt handles `if … {} [elif … {}]* [else {}]`.
func (p *parser) parseIfStmt() (Stmt, error) {
	kw := p.advance() // consume `if`
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	thenBlk, err := p.parseBlock("'if' body")
	if err != nil {
		return nil, err
	}
	stmt := &IfStmt{Pos: kw.Pos, Cond: cond, Then: thenBlk}

	for p.peek().Kind == KindElif {
		elif := p.advance()
		ec, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		body, err := p.parseBlock("'elif' body")
		if err != nil {
			return nil, err
		}
		stmt.Elifs = append(stmt.Elifs, ElifClause{Pos: elif.Pos, Cond: ec, Body: body})
	}
	if p.peek().Kind == KindElse {
		p.advance()
		body, err := p.parseBlock("'else' body")
		if err != nil {
			return nil, err
		}
		stmt.Else = body
	}
	return stmt, nil
}

// parseForStmt handles all three for-loop shapes.
func (p *parser) parseForStmt() (Stmt, error) {
	kw := p.advance() // consume `for`

	// `for { ... }` — infinite.
	if p.peek().Kind == KindLBrace {
		body, err := p.parseBlock("'for' body")
		if err != nil {
			return nil, err
		}
		return &ForStmt{Pos: kw.Pos, Kind: ForInfinite, Body: body}, nil
	}

	// Disambiguate `for x in ...` from `for cond { ... }` by trying the
	// range head first when the next two tokens look like one. This is a
	// LL(2) check, which the grammar can absorb without a backtrack.
	if p.peek().Kind == KindIdent {
		// Look two ahead via the raw tokens. We can't use peek alone because
		// peek is only one-token lookahead; we want to know if the next
		// non-newline token after the ident is `in`.
		if k := p.peekKindAfterIdent(); k == KindIn {
			return p.parseForRange(kw.Pos)
		}
	}

	// `for cond { ... }`.
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock("'for' body")
	if err != nil {
		return nil, err
	}
	return &ForStmt{Pos: kw.Pos, Kind: ForCond, Cond: cond, Body: body}, nil
}

// peekKindAfterIdent peeks at the kind of the token after a leading ident,
// skipping any NEWLINEs (we do this transparently for parens elsewhere; the
// `for` head is a single line in practice but we stay tolerant).
func (p *parser) peekKindAfterIdent() Kind {
	i := p.pos + 1
	for i < len(p.tokens) && p.tokens[i].Kind == KindNewline {
		i++
	}
	if i >= len(p.tokens) {
		return KindEOF
	}
	return p.tokens[i].Kind
}

// parseForRange handles `for IDENT in EXPR..EXPR { ... }` /
// `for IDENT in EXPR..=EXPR { ... }`. RangeExprs are produced ONLY here at
// v0.1 — the general expression parser refuses to construct them.
func (p *parser) parseForRange(forPos Position) (Stmt, error) {
	nameTok, _ := p.expect(KindIdent, "in 'for' header") // already known ident
	if _, err := p.expect(KindIn, "in 'for' header"); err != nil {
		return nil, err
	}
	// parseOr (not parseExpr) so the trailing-range guard in parseExpr
	// doesn't fire on the `..` / `..=` we are about to consume.
	start, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	rangeTok := p.peek()
	var inclusive bool
	switch rangeTok.Kind {
	case KindRange:
		inclusive = false
	case KindRangeEq:
		inclusive = true
	default:
		return nil, errorAt(rangeTok.Pos, "expected '..' or '..=' in 'for' range, got %s", rangeTok.Kind)
	}
	p.advance()
	end, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock("'for' body")
	if err != nil {
		return nil, err
	}
	return &ForStmt{
		Pos:    forPos,
		Kind:   ForRange,
		Var:    nameTok.Value,
		VarPos: nameTok.Pos,
		Range: &RangeExpr{
			Pos:       rangeTok.Pos,
			Start:     start,
			End:       end,
			Inclusive: inclusive,
		},
		Body: body,
	}, nil
}

// parseReturnStmt handles `return [expr] [if cond]`.
func (p *parser) parseReturnStmt() (Stmt, error) {
	kw := p.advance() // consume `return`
	stmt := &ReturnStmt{Pos: kw.Pos}

	// Bare `return` (with optional guard) ⇒ Value is nil.
	switch p.peekRaw().Kind {
	case KindNewline, KindEOF, KindRBrace:
		return stmt, nil
	case KindIf:
		// `return if cond` ⇒ no value.
		p.advance()
		guard, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Guard = guard
		return stmt, nil
	}

	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	stmt.Value = val
	if p.peek().Kind == KindIf {
		p.advance()
		guard, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Guard = guard
	}
	return stmt, nil
}

// parseBreakLikeStmt handles `break [if cond]` / `continue [if cond]`. The
// boolean `isBreak` selects which AST node to return — the parsing logic is
// identical, so factoring keeps both grammars on one path.
func (p *parser) parseBreakLikeStmt(isBreak bool) (Stmt, error) {
	kw := p.advance() // consume `break` / `continue`
	var guard Expr
	if p.peek().Kind == KindIf {
		p.advance()
		g, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		guard = g
	}
	if isBreak {
		return &BreakStmt{Pos: kw.Pos, Guard: guard}, nil
	}
	return &ContinueStmt{Pos: kw.Pos, Guard: guard}, nil
}

// parseExprOrAssignStmt handles expression statements (function calls only at
// v0.1) and assignment statements. We parse the LHS as a full expression and
// then look for an assignment operator.
func (p *parser) parseExprOrAssignStmt() (Stmt, error) {
	startTok := p.peek()
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	// Check for an assignment operator.
	if op, ok := assignOpFor(p.peek().Kind); ok {
		opTok := p.advance()
		ident, ok := expr.(*IdentExpr)
		if !ok {
			return nil, errorAt(opTok.Pos, "left-hand side of '%s' must be an identifier", op)
		}
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &AssignStmt{
			Pos:    ident.Pos,
			Target: ident,
			Op:     op,
			Value:  val,
		}, nil
	}

	// Plain expression statement: only a CallExpr is meaningful at v0.1.
	if _, ok := expr.(*CallExpr); !ok {
		return nil, errorAt(startTok.Pos, "expression statements must be function calls at v0.1")
	}
	return &ExprStmt{Pos: startTok.Pos, Expr: expr}, nil
}

// assignOpFor maps a token Kind to the matching AssignOp value, if any.
func assignOpFor(k Kind) (AssignOp, bool) {
	switch k {
	case KindAssign:
		return AssignSet, true
	case KindPlusEq:
		return AssignAdd, true
	case KindMinusEq:
		return AssignSub, true
	case KindStarEq:
		return AssignMul, true
	case KindSlashEq:
		return AssignDiv, true
	case KindPctEq:
		return AssignMod, true
	case KindAmpEq:
		return AssignAnd, true
	case KindPipeEq:
		return AssignOr, true
	case KindCaretEq:
		return AssignXor, true
	case KindShlEq:
		return AssignShl, true
	case KindShrEq:
		return AssignShr, true
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Blocks.
// ---------------------------------------------------------------------------

// parseBlock consumes `{ statements }`. The optional `ctx` is a phrase
// inserted into error messages ("'if' body", "function body").
func (p *parser) parseBlock(ctx string) (*Block, error) {
	open, err := p.expect(KindLBrace, "for "+ctx)
	if err != nil {
		return nil, err
	}
	blk := &Block{Pos: open.Pos}
	for {
		// Skip newlines between statements inside the block.
		p.skipNewlines()
		if p.peekRaw().Kind == KindRBrace {
			p.pos++ // consume `}`
			return blk, nil
		}
		if p.peekRaw().Kind == KindEOF {
			return nil, errorAt(open.Pos, "unterminated block (missing '}')")
		}
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		blk.Statements = append(blk.Statements, stmt)
		if err := p.terminateStatement(); err != nil {
			return nil, err
		}
	}
}

// expectParen consumes a `(` `)` `[` `]` and updates parenDepth so that
// NEWLINE tokens inside the bracketed region are skipped transparently.
func (p *parser) expectParen(k Kind, ctx string) (Token, error) {
	t, err := p.expect(k, ctx)
	if err != nil {
		return t, err
	}
	switch k {
	case KindLParen, KindLBracket:
		p.parenDepth++
	case KindRParen, KindRBracket:
		p.parenDepth--
	}
	return t, nil
}

// ---------------------------------------------------------------------------
// Expressions: precedence climbing.
//
// The grammar table lives in PLAN.md; the Pratt-style structure here mirrors
// it level-for-level. Levels with non-associative operators (comparison)
// implement non-associativity by refusing to recurse on the same level after
// a successful parse — the second compare-op produces a clear diagnostic.
// ---------------------------------------------------------------------------

// parseExpr is the public entry into the expression grammar. It dispatches
// to parseOr (the lowest-precedence rung) and then guards against `..` /
// `..=` appearing at the trailing edge of an otherwise complete expression
// — at v0.1 ranges are restricted to the head of `for x in ...` and any
// other appearance gets the dedicated diagnostic instead of a generic
// "expected newline" message.
//
// parseForRange bypasses this guard by calling parseOr directly so it can
// itself consume the `..` / `..=` token.
func (p *parser) parseExpr() (Expr, error) {
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if k := p.peek().Kind; k == KindRange || k == KindRangeEq {
		bad := p.peek()
		return nil, errorAt(bad.Pos, "range expressions are only allowed in for-in heads at v0.1")
	}
	return expr, nil
}

// Level 1: or, xor (left-assoc).
func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		var op BinaryOp
		switch p.peek().Kind {
		case KindOr:
			op = BinOr
		case KindXor:
			op = BinXor
		default:
			return left, nil
		}
		opTok := p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: op, Left: left, Right: right}
	}
}

// Level 2: and (left-assoc).
func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == KindAnd {
		opTok := p.advance()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: BinAnd, Left: left, Right: right}
	}
	return left, nil
}

// Level 3: not (prefix, right-assoc). Comparison binds tighter (PLAN level 4),
// so `not a == b` parses as `not (a == b)` — the recursive call to parseNot
// drops into parseComparison once the prefix runs out, giving the comparison
// the chance to grab `a == b` before `not` wraps the whole thing.
func (p *parser) parseNot() (Expr, error) {
	if p.peek().Kind == KindNot {
		opTok := p.advance()
		operand, err := p.parseNot() // allow stacking: `not not x`
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Pos: opTok.Pos, Op: UnaryNot, Operand: operand}, nil
	}
	return p.parseComparison()
}

// Level 4: comparison ==, !=, <, >, <=, >= (NON-associative).
func (p *parser) parseComparison() (Expr, error) {
	left, err := p.parseBitOr()
	if err != nil {
		return nil, err
	}
	op, ok := comparisonOpFor(p.peek().Kind)
	if !ok {
		return left, nil
	}
	opTok := p.advance()
	right, err := p.parseBitOr()
	if err != nil {
		return nil, err
	}
	// Non-associativity: a chained comparison is rejected with a precise
	// error pointing at the second operator.
	if next, ok2 := comparisonOpFor(p.peek().Kind); ok2 {
		bad := p.peek()
		return nil, errorAt(bad.Pos, "comparison operators are non-associative; '%s ... %s' must be parenthesised or split", op, next)
	}
	return &BinaryExpr{Pos: opTok.Pos, Op: op, Left: left, Right: right}, nil
}

func comparisonOpFor(k Kind) (BinaryOp, bool) {
	switch k {
	case KindEq:
		return BinEq, true
	case KindNE:
		return BinNE, true
	case KindLT:
		return BinLT, true
	case KindGT:
		return BinGT, true
	case KindLE:
		return BinLE, true
	case KindGE:
		return BinGE, true
	}
	return 0, false
}

// Level 5: bitwise OR (left-assoc).
func (p *parser) parseBitOr() (Expr, error) {
	left, err := p.parseBitXor()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == KindPipe {
		opTok := p.advance()
		right, err := p.parseBitXor()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: BinBitOr, Left: left, Right: right}
	}
	return left, nil
}

// Level 6: bitwise XOR (left-assoc).
func (p *parser) parseBitXor() (Expr, error) {
	left, err := p.parseBitAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == KindCaret {
		opTok := p.advance()
		right, err := p.parseBitAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: BinBitXor, Left: left, Right: right}
	}
	return left, nil
}

// Level 7: bitwise AND (left-assoc).
func (p *parser) parseBitAnd() (Expr, error) {
	left, err := p.parseShift()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == KindAmp {
		opTok := p.advance()
		right, err := p.parseShift()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: BinBitAnd, Left: left, Right: right}
	}
	return left, nil
}

// Level 8: shifts <<, >> (left-assoc).
func (p *parser) parseShift() (Expr, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	for {
		var op BinaryOp
		switch p.peek().Kind {
		case KindShl:
			op = BinShl
		case KindShr:
			op = BinShr
		default:
			return left, nil
		}
		opTok := p.advance()
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: op, Left: left, Right: right}
	}
}

// Level 9: additive +, - (left-assoc).
func (p *parser) parseAdd() (Expr, error) {
	left, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for {
		var op BinaryOp
		switch p.peek().Kind {
		case KindPlus:
			op = BinAdd
		case KindMinus:
			op = BinSub
		default:
			return left, nil
		}
		opTok := p.advance()
		right, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: op, Left: left, Right: right}
	}
}

// Level 10: multiplicative *, /, //, % (left-assoc).
func (p *parser) parseMul() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		var op BinaryOp
		switch p.peek().Kind {
		case KindStar:
			op = BinMul
		case KindSlash:
			op = BinDiv
		case KindFloorDiv:
			op = BinFloorDiv
		case KindPercent:
			op = BinMod
		default:
			return left, nil
		}
		opTok := p.advance()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Pos: opTok.Pos, Op: op, Left: left, Right: right}
	}
}

// Level 11: unary -, ~ (right-assoc).
func (p *parser) parseUnary() (Expr, error) {
	switch p.peek().Kind {
	case KindMinus:
		opTok := p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Pos: opTok.Pos, Op: UnaryNeg, Operand: operand}, nil
	case KindTilde:
		opTok := p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Pos: opTok.Pos, Op: UnaryBitNot, Operand: operand}, nil
	}
	return p.parsePostfix()
}

// Level 12: postfix call (left-assoc).
func (p *parser) parsePostfix() (Expr, error) {
	expr, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == KindLParen {
		callPos := p.peek().Pos
		if _, err := p.expectParen(KindLParen, "in call"); err != nil {
			return nil, err
		}
		var args []Expr
		if p.peek().Kind != KindRParen {
			for {
				a, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				args = append(args, a)
				if p.peek().Kind == KindComma {
					p.advance()
					continue
				}
				break
			}
		}
		if _, err := p.expectParen(KindRParen, "in call"); err != nil {
			return nil, err
		}
		expr = &CallExpr{Pos: callPos, Callee: expr, Args: args}
	}
	return expr, nil
}

// Level 13: atoms — literals, identifiers, parenthesised expressions.
//
// Range tokens (`..`, `..=`) MUST NOT appear here. v0.1 admits them only in
// the head of `for x in ...`; anywhere else is a parse error with a clear
// message so the user knows the form is reserved, not a typo.
func (p *parser) parseAtom() (Expr, error) {
	t := p.peek()
	switch t.Kind {
	case KindInt:
		p.advance()
		return &IntLit{Pos: t.Pos, Text: t.Value}, nil
	case KindFloat:
		p.advance()
		return &FloatLit{Pos: t.Pos, Text: t.Value}, nil
	case KindString:
		p.advance()
		return &StringLit{Pos: t.Pos, Value: t.Value}, nil
	case KindTrue:
		p.advance()
		return &BoolLit{Pos: t.Pos, Value: true}, nil
	case KindFalse:
		p.advance()
		return &BoolLit{Pos: t.Pos, Value: false}, nil
	case KindIdent:
		p.advance()
		return &IdentExpr{Pos: t.Pos, Name: t.Value}, nil
	case KindLParen:
		open := t
		if _, err := p.expectParen(KindLParen, ""); err != nil {
			return nil, err
		}
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expectParen(KindRParen, "to close '('"); err != nil {
			return nil, err
		}
		return &ParenExpr{Pos: open.Pos, Inner: inner}, nil
	case KindRange, KindRangeEq:
		return nil, errorAt(t.Pos, "range expressions are only allowed in for-in heads at v0.1")
	case KindBang:
		return nil, errorAt(t.Pos, "use 'not' for boolean negation; '!' is reserved")
	}
	return nil, errorAt(t.Pos, "expected expression, got %s", t.Kind)
}
