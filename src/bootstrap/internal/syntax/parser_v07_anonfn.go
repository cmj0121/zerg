package syntax

// v0.7 Unit 1a — parser surface for anonymous functions, `spawn`, and
// `defer`.
//
// Three shapes ship in this unit:
//
//   - Anon-fn expression: `fn(params) [-> R] { body }` in expression
//     position. Same shape as FnDecl minus the name. The dispatch hooks live
//     in parseStatement (statement-position IIFE) and parseAtom (any other
//     expression position); the parsing itself is parseAnonFnExpr below.
//   - `spawn <fn-call-expr>` statement. Narrows the parsed expression to a
//     *CallExpr at parse time per the grammar's "spawn admits only fn calls"
//     rule.
//   - `defer <stmt>` / `defer <block>` statement. v0.7 admits defer only at
//     fn-body top-level scope; nested-block defer is rejected at parse time
//     via the fnBodyDepths stack tracked in parser state.

// parseAnonFnExpr parses `fn(params) [-> R] { body }` as an expression. The
// cursor is sitting on `fn`; the caller (parseAtom or parseStatement
// dispatch) has already verified the lookahead-1 token is `(` per the
// disambiguation rule.
//
// The body parses through parseFnBody so a defer at the immediate body level
// is admitted; defer nested in inner blocks rejects with the same diagnostic
// as a top-level fn-decl. `pub` in the param list is rejected for parity
// with the named-fn rule (parseFnDecl never admits `pub` on params either).
func (p *parser) parseAnonFnExpr() (Expr, error) {
	kw := p.advance() // consume `fn`
	if _, err := p.expectParen(KindLParen, "in anonymous function signature"); err != nil {
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
	if _, err := p.expectParen(KindRParen, "in anonymous function signature"); err != nil {
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

	body, err := p.parseFnBody("anonymous function body")
	if err != nil {
		return nil, err
	}
	return &AnonFnExpr{
		Pos:    kw.Pos,
		Params: params,
		Return: ret,
		Body:   body,
	}, nil
}

// parseSpawnStmt parses `spawn <expr>` and narrows the parsed expression to
// a fn-call shape per the grammar. Two concrete shapes admit:
//
//   - *CallExpr — `spawn do_work()`, `spawn fn() { ... }()` (IIFE).
//   - *MethodCallExpr — `spawn mod.do_work()`, `spawn obj.method()`. Cross-
//     module fn calls naturally land on MethodCallExpr at v0.5; spawn
//     accepts both shapes so the caller's syntax stays unchanged.
//
// Anything else (literal, bare ident, arithmetic, struct literal) rejects
// with the focused diagnostic.
func (p *parser) parseSpawnStmt() (Stmt, error) {
	kw := p.advance() // consume `spawn`
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	switch expr.(type) {
	case *CallExpr, *MethodCallExpr:
		return &SpawnStmt{Pos: kw.Pos, Call: expr}, nil
	}
	return nil, errorAt(kw.Pos, "spawn requires a function call expression")
}

// parseDeferStmt parses `defer <stmt>` or `defer <block>`. v0.7 admits defer
// only at fn-body top-level scope: rejected at REPL (no enclosing fn) and
// rejected when nested inside if / for / match / inner blocks. The check
// reads p.fnBodyDepths — len 0 means no enclosing fn, and any deeper
// blockDepth than the current fn body's recorded depth means we are nested
// in an inner block.
//
// The single-statement form is wrapped in a one-element Block so downstream
// consumers walk a single shape (DeferStmt.Body is always a *Block).
func (p *parser) parseDeferStmt() (Stmt, error) {
	kw := p.advance() // consume `defer`
	if len(p.fnBodyDepths) == 0 {
		return nil, errorAt(kw.Pos, "defer only allowed inside a function body")
	}
	if p.blockDepth != p.fnBodyDepths[len(p.fnBodyDepths)-1] {
		return nil, errorAt(kw.Pos, "defer only allowed at fn-body scope")
	}

	if p.peek().Kind == KindLBrace {
		body, err := p.parseBlock("defer body")
		if err != nil {
			return nil, err
		}
		return &DeferStmt{Pos: kw.Pos, Body: body}, nil
	}

	inner, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	body := &Block{Pos: inner.StmtPos(), Statements: []Stmt{inner}}
	return &DeferStmt{Pos: kw.Pos, Body: body}, nil
}
