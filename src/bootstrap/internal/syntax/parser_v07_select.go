package syntax

// v0.7 Unit 1c — parser surface for the `select` statement: multiplexed
// channel wait. Mirrors the shape of parseMatch (a brace-delimited list of
// arms, NEWLINE-separated) but each arm is one of four channel ops instead
// of a pattern, and the arm separator after the op is `->` rather than
// `=>`.
//
// The four arm shapes admitted by `select_op`:
//
//   - `_`                   — default (non-blocking) arm.
//   - `<- expr`             — receive-discard.
//   - `IDENT := <- expr`    — receive-bind.
//   - `expr <- expr`        — send.
//
// Disambiguation between recv-bind and a normal `let`-style declaration is
// done by a 3-token lookahead at arm-start: IDENT, `:=`, `<-`. Outside a
// select arm, `IDENT := <- ch` is not valid syntax (a bare walrus form is
// only allowed after `let` / `mut` / `const`), so the lookahead does not
// false-trigger on top-level declarations.

// parseSelectStmt consumes `select { arm NEWLINE { arm NEWLINE } }`. The
// cursor is sitting on `select`. An empty arm list is rejected at parse time
// per PLAN.md — a select with zero arms can never make progress and so is
// almost certainly user error.
func (p *parser) parseSelectStmt() (Stmt, error) {
	kw := p.advance() // consume `select`
	if _, err := p.expect(KindLBrace, "in select"); err != nil {
		return nil, err
	}
	p.blockDepth++
	defer func() { p.blockDepth-- }()

	stmt := &SelectStmt{Pos: kw.Pos}
	for {
		p.skipNewlines()
		if p.peekRaw().Kind == KindRBrace {
			p.pos++ // consume `}`
			if len(stmt.Arms) == 0 {
				return nil, errorAt(kw.Pos, "select must have at least one arm")
			}
			return stmt, nil
		}
		if p.peekRaw().Kind == KindEOF {
			return nil, &ParseError{
				Pos:        kw.Pos,
				Message:    "unterminated select (missing '}')",
				Incomplete: true,
			}
		}
		arm, err := p.parseSelectArm()
		if err != nil {
			return nil, err
		}
		stmt.Arms = append(stmt.Arms, arm)
		switch p.peekRaw().Kind {
		case KindNewline:
			p.pos++
		case KindRBrace, KindEOF:
			// Loop iteration handles both cases.
		default:
			t := p.peekRaw()
			return nil, errorAtTok(t, "expected newline or '}' between select arms, got %s", t.Kind)
		}
	}
}

// parseSelectArm reads one arm: `select_op -> ( statement | block )`. The
// arm-op shape is decided by lookahead so we never parse-then-roll-back; in
// particular, the recv-bind triple (`IDENT := <-`) is detected by a 3-token
// peek before any expression parsing commits.
func (p *parser) parseSelectArm() (SelectArm, error) {
	armPos := p.peek().Pos
	var arm SelectArm
	arm.Pos = armPos

	switch {
	case p.peekIsWildcard():
		t := p.advance()
		arm.Op = SelectDefault
		arm.Pos = t.Pos
	case p.peek().Kind == KindLArrow:
		p.advance()
		ch, err := p.parseExpr()
		if err != nil {
			return SelectArm{}, err
		}
		arm.Op = SelectRecvDiscard
		arm.Chan = ch
	case p.peekRecvBindHead():
		nameTok := p.advance() // IDENT
		p.advance()            // `:=`
		p.advance()            // `<-`
		ch, err := p.parseExpr()
		if err != nil {
			return SelectArm{}, err
		}
		arm.Op = SelectRecvBind
		arm.BindName = nameTok.Value
		arm.BindNamePos = nameTok.Pos
		arm.Chan = ch
	default:
		// Send: `expr <- expr`. We parse the LHS as a full expression, then
		// require `<-`. Anything else is a parse error pointing at the
		// offending token — this is the catch-all branch, so the diagnostic
		// names the four legal arm shapes.
		lhs, err := p.parseExpr()
		if err != nil {
			return SelectArm{}, err
		}
		if p.peek().Kind != KindLArrow {
			bad := p.peek()
			return SelectArm{}, errorAt(bad.Pos, "expected '<-' or '->' in select arm")
		}
		p.advance() // consume `<-`
		val, err := p.parseExpr()
		if err != nil {
			return SelectArm{}, err
		}
		arm.Op = SelectSend
		arm.Chan = lhs
		arm.Value = val
	}

	if _, err := p.expect(KindArrow, "in select arm"); err != nil {
		return SelectArm{}, err
	}

	if p.peek().Kind == KindLBrace {
		body, err := p.parseBlock("select arm body")
		if err != nil {
			return SelectArm{}, err
		}
		arm.Body = body
		return arm, nil
	}

	stmtPos := p.peek().Pos
	stmt, err := p.parseStatement()
	if err != nil {
		return SelectArm{}, err
	}
	arm.Body = &Block{Pos: stmtPos, Statements: []Stmt{stmt}}
	return arm, nil
}

// peekIsWildcard reports whether the current significant token is the bare
// identifier `_`. The wildcard arm is admitted only when `_` stands alone —
// `_ <- ch` would still be a `_` arm followed by a stray `<-` (caught at the
// `->` expectation), but in practice `_` cannot be sent into, so the next
// token after the wildcard is always `->`.
func (p *parser) peekIsWildcard() bool {
	t := p.peek()
	return t.Kind == KindIdent && t.Value == "_"
}

// peekRecvBindHead reports whether the next three significant tokens are
// IDENT (not `_`), `:=`, `<-`. Used by parseSelectArm to commit to the
// receive-bind shape without parse-then-rewind on a normal expression.
//
// The lookahead skips NEWLINEs only when parenDepth > 0 (matching p.peek's
// contract). Inside a select-brace block the parser sits at parenDepth == 0
// for the arm head, so the simple `+1`/`+2` slot inspection is correct.
func (p *parser) peekRecvBindHead() bool {
	if p.peek().Kind != KindIdent || p.peek().Value == "_" {
		return false
	}
	i := p.pos + 1
	if i >= len(p.tokens) || p.tokens[i].Kind != KindWalrus {
		return false
	}
	j := i + 1
	if j >= len(p.tokens) || p.tokens[j].Kind != KindLArrow {
		return false
	}
	return true
}
