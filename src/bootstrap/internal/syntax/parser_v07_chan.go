package syntax

// v0.7 Unit 1b — parser surface for channels, send / receive operators, and
// the discard / send statement forms. The lexer half lands `KindLArrow` for
// `<-` with longest-match disambiguation against `<<=`, `<<`, `<=`, and `<`;
// see lexOperator's `<` case.
//
// Three shapes ship in this unit:
//
//   - `chan[T]()` / `chan[T](N)` constructor expressions parse as a dedicated
//     ChanConstructorExpr node. The dispatch hook lives in parseAtom: when the
//     IDENT `chan` is followed by `[`, parseChanConstructorExpr takes over.
//   - `expr <- expr` send statement. The dispatch hook lives in
//     parseExprOrAssignStmt: after the LHS expression parses and a `<-`
//     follows, parseSendStmt narrows the shape to a SendStmt and rejects a
//     chained second `<-` so users split with parens.
//   - `<- expr` prefix-receive expression. parseUnary already routes the
//     `KindLArrow` lookahead into a RecvExpr at the same precedence rung as
//     `-`, `~`. This file does not own that path; it only owns the
//     constructor and send shapes plus the helper used by parseAtom.

// peekAfterIdentIs reports whether the token immediately after the current
// IDENT (which the cursor is sitting on, NOT yet advanced) is of the given
// kind. Mirrors peekAfterFnIs's contract: NEWLINEs are skipped only when
// parenDepth > 0 (matching p.peek's policy). Used by parseAtom's chan-
// constructor dispatch — we must not advance the IDENT before we know which
// shape to commit to, since the bare `chan` ident is still a valid identifier
// at the lexer level.
func (p *parser) peekAfterIdentIs(k Kind) bool {
	i := p.pos + 1
	if p.parenDepth > 0 {
		for i < len(p.tokens) && p.tokens[i].Kind == KindNewline {
			i++
		}
	}
	if i >= len(p.tokens) {
		return false
	}
	return p.tokens[i].Kind == k
}

// parseChanConstructorExpr parses `chan[T]()` (unbuffered) or `chan[T](N)`
// (buffered). The cursor is sitting on the IDENT `chan`; parseAtom's
// dispatch has already verified the lookahead-1 token is `[`.
//
// The element type parses through parseTypeArgList, which already enforces
// the v0.6 generic-arg-list shape (≥ 1 arg, no trailing comma, no empty
// list). chan takes exactly one type argument; more than one rejects with a
// focused diagnostic at parse time so the user is not steered into a typeck-
// level "wrong number of type-arguments" error.
//
// After the type-arg list a `(` is mandatory — `chan[T]` standalone in
// expression position is meaningless (it is a type, not a value). The arg
// list inside the parens is either empty (unbuffered) or a single capacity
// expression (buffered); two-or-more args reject at parse time. typeck (Unit
// 2) validates that the capacity expression types as `int`.
func (p *parser) parseChanConstructorExpr() (Expr, error) {
	chanTok := p.advance() // consume `chan`
	args, err := p.parseTypeArgList()
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, errorAt(chanTok.Pos, "chan[T] takes exactly one type argument, got %d", len(args))
	}
	if _, err := p.expectParen(KindLParen, "in chan constructor"); err != nil {
		return nil, err
	}
	var capacity Expr
	if p.peek().Kind != KindRParen {
		c, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		capacity = c
		if p.peek().Kind == KindComma {
			bad := p.peek()
			return nil, errorAt(bad.Pos, "chan constructor takes at most one capacity argument")
		}
	}
	if _, err := p.expectParen(KindRParen, "to close chan constructor"); err != nil {
		return nil, err
	}
	return &ChanConstructorExpr{
		Pos:      chanTok.Pos,
		Element:  args[0],
		Capacity: capacity,
	}, nil
}

// parseSendStmt narrows `<chan-expr> <- <value-expr>` to a SendStmt. The
// cursor is sitting on the `<-` token; the caller (parseExprOrAssignStmt)
// has already parsed the LHS expression and verified the lookahead.
//
// Chained sends (`a <- b <- c`) are rejected at parse time per PLAN.md: the
// shape is ambiguous between right-associative folding and a typeck error
// for sending an incompatible value, and parser-time rejection gives the
// best diagnostic. Users must split with explicit parens.
func (p *parser) parseSendStmt(chanExpr Expr) (Stmt, error) {
	arrowTok := p.advance() // consume `<-`
	value, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind == KindLArrow {
		bad := p.peek()
		return nil, errorAt(bad.Pos, "chained '<-' is not allowed; split with parens")
	}
	return &SendStmt{Pos: arrowTok.Pos, Chan: chanExpr, Value: value}, nil
}
