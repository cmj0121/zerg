// U9 parser: the final surface — group 9 (concurrency: spawn, send, chan-new,
// select), group 10 (modules: import, and the top-level stmt-list finalized in
// parser.go), group 11 (cleanup: defer, del), and group 12 (unsafe: the block-
// expression, the module-level declaration group, and inline assembly). The two
// shapes of 'unsafe { … }' are told apart by POSITION: a function-body primary is
// the block-expression (parseUnsafeExpr), a module-level item is the declaration
// group (parseUnsafeGroup).
package parser

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// --- group 9: concurrency -----------------------------------------------------

// parseSpawn parses 'spawn expr' (GRAMMAR group 9): a fire-and-forget coroutine.
func (p *parser) parseSpawn() ast.Stmt {
	start := p.expect(token.Spawn).Span.Start
	call := p.parseExpr()
	return spanned(&ast.SpawnStmt{Call: call}, span(start, call.Span().End))
}

// finishSend parses the value after 'chan <- ' and builds a send statement
// (GRAMMAR group 9): the channel expression was already parsed.
func (p *parser) finishSend(ch ast.Expr) ast.Stmt {
	p.expect(token.LArrow)
	val := p.parseExpr()
	return spanned(&ast.SendStmt{Chan: ch, Value: val}, span(ch.Span().Start, val.Span().End))
}

// parseChanNew parses the channel constructor expression 'chan[T](cap?)' (GRAMMAR
// group 9) — distinct from the chan TYPE, which parses in type position.
func (p *parser) parseChanNew() ast.Expr {
	start := p.expect(token.Chan).Span.Start
	p.expect(token.LBrack)
	var elem ast.Type
	p.withBraces(func() ast.Expr {
		elem = p.parseType()
		return nil
	})
	p.expect(token.RBrack)
	p.expect(token.LParen)
	cn := &ast.ChanNew{Elem: elem}
	if !p.at(token.RParen) {
		cn.Cap = p.withBraces(p.parseExpr)
	}
	end := p.expect(token.RParen).Span.End
	return spanned(cn, span(start, end))
}

// parseSelect parses 'select { arm+ }' (GRAMMAR group 9). Arms are separated like
// statements and each keeps its own line's lead/trail trivia (as a match does).
func (p *parser) parseSelect() ast.Stmt {
	start := p.expect(token.Select).Span.Start
	p.expect(token.LBrace)
	var arms []ast.SelectArm
	for {
		p.skipSemis()
		if p.at(token.RBrace) || p.at(token.EOF) {
			break
		}
		lead := p.lead()
		arm, ok := p.trySelectArm()
		if !ok {
			continue // diagnosed and skipped, cursor synced within the braces
		}
		arm.SetLead(lead)
		arm.SetTrail(p.trailBehind())
		arms = append(arms, arm)
	}
	end := p.expect(token.RBrace).Span.End
	// GRAMMAR says 'select-arm+'. An empty one has nothing to wait FOR: it would park on no
	// channel at all and never wake, which is a hang with no cause to find.
	if len(arms) == 0 {
		p.errorf(span(start, end), "a select needs at least one arm — an empty one waits on nothing and never wakes")
	}
	return spanned(&ast.SelectStmt{Arms: arms}, span(start, end))
}

// trySelectArm parses one select arm, recovering within the select's braces on
// a parse error (as tryMatchArm does for a match): it reports, syncs to the next
// separator or the closing '}', and returns ok=false so the arm is skipped
// without unwinding past the select and cascading.
func (p *parser) trySelectArm() (arm ast.SelectArm, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, isBail := r.(bailout); !isBail {
				panic(r)
			}
			p.syncArm()
			ok = false
		}
	}()
	return p.parseSelectArm(), true
}

// parseSelectArm parses one select arm: '_ => e', a recv arm
// '((id|_) :=)? <- e => e', or a send arm 'e <- e => e'.
//
// There is no terminal arm: a select PICKS a ready arm, and 'for select { … }' is the loop
// that ENDS when every watched receive channel has. '_' is the only bare identifier an arm
// may open with, and it stays contextual because it is punctuation.
func (p *parser) parseSelectArm() ast.SelectArm {
	start := p.cur().Span.Start
	// The one bare head. `_` is matched on the spelling because this lexer's keyword table
	// does not reserve it, and ANYTHING else here is refused by name — including `close`,
	// which was the terminal arm until it was replaced by `for select`. A head that is not
	// understood used to become the `_` arm silently, which turned a typo into a select that
	// no longer blocks and no longer ends.
	if p.at(token.Ident) && p.peek(1).Kind == token.FatArrow {
		if p.cur().Lexeme == "_" {
			return p.finishSelectHead(start, ast.SelectDefault)
		}
		p.fail(p.cur().Span, "`%s` is not a select arm head — a select picks a ready arm, and `for select { … }` is the loop that ends when every watched channel has", p.cur().Lexeme)
	}
	// `close` is a keyword to the shipped compiler and an ordinary name here, so it arrives
	// as an Ident and is caught above — except when it opens a call, which is the statement
	// `close(ch)` and belongs to a body rather than to a head.
	// recv arm with a bind target '(id|_) := <- e => e'
	if p.at(token.Ident) && p.peek(1).Kind == token.Walrus {
		bind := p.advance().Lexeme
		p.advance() // ':='
		return p.finishSelectRecv(start, bind, true)
	}
	// recv arm without a bind '<- e => e'
	if p.at(token.LArrow) {
		return p.finishSelectRecv(start, "", false)
	}
	// send arm 'e <- e => e'
	ch := p.parseExpr()
	p.expect(token.LArrow)
	val := p.parseExpr()
	p.expect(token.FatArrow)
	body := p.parseSelectArmBody()
	arm := ast.SelectArm{Kind: ast.SelectSend, Chan: ch, Value: val, Body: body}
	arm.SetSpan(span(start, body.Span().End))
	return arm
}

// parseSelectArmBody parses an arm's body, which GRAMMAR group 9 makes a STATEMENT. A
// select arm runs rather than yields: the select is a statement and produces no value, and
// both compilers lower an arm to if/else, which is exactly what can hold a statement — the
// contrast is a `match` arm, whose body must yield the match's value.
//
// It was an expression, so the two spellings the chapter itself uses — `<-quit => break` and
// a bare `_ => tick()` beside `v := <-a => print v!` — did not parse here, while the
// shipped compiler took them. A body that already is an expression stays one, so a call or
// a `{ … }` block is unchanged; anything else becomes a one-statement block, which is the
// shape the arm lowering already handles.
func (p *parser) parseSelectArmBody() ast.Expr {
	s := p.parseStmt()
	if es, ok := s.(*ast.ExprStmt); ok {
		return es.X
	}
	b := &ast.Block{Stmts: []ast.Stmt{s}}
	b.SetSpan(s.Span())
	return b
}

// finishSelectHead parses the '=> body' of the bare '_' arm.
func (p *parser) finishSelectHead(start token.Pos, kind ast.SelectArmKind) ast.SelectArm {
	p.advance() // '_'
	p.expect(token.FatArrow)
	body := p.parseSelectArmBody()
	arm := ast.SelectArm{Kind: kind, Body: body}
	arm.SetSpan(span(start, body.Span().End))
	return arm
}

// finishSelectRecv parses the '<- e => body' of a recv arm; the optional bind
// target ('(id|_) :=') was already consumed.
func (p *parser) finishSelectRecv(start token.Pos, bind string, hasBind bool) ast.SelectArm {
	p.expect(token.LArrow)
	ch := p.parseExpr()
	p.expect(token.FatArrow)
	body := p.parseSelectArmBody()
	arm := ast.SelectArm{Kind: ast.SelectRecv, Bind: bind, HasBind: hasBind, Chan: ch, Body: body}
	arm.SetSpan(span(start, body.Span().End))
	return arm
}

// --- group 10: modules --------------------------------------------------------

// parseImport parses 'import (spec | '(' spec* ')')' (GRAMMAR group 10). The
// grouped form is one spec per line inside '(' — self-delimiting, so no separator
// is needed (an explicit ';' is skipped, and ASI is suppressed inside '(').
func (p *parser) parseImport() ast.Stmt {
	start := p.expect(token.Import).Span.Start
	if p.at(token.LParen) {
		p.advance()
		imp := &ast.ImportStmt{Grouped: true}
		for {
			p.skipSemis()
			if p.at(token.RParen) || p.at(token.EOF) {
				break
			}
			lead := p.lead()
			spec := p.parseImportSpec()
			spec.SetLead(lead)
			spec.SetTrail(p.trailBehind())
			imp.Specs = append(imp.Specs, spec)
		}
		rp := p.expect(token.RParen)
		imp.End = rp.Leading
		return spanned(imp, span(start, rp.Span.End))
	}
	spec := p.parseImportSpec()
	return spanned(&ast.ImportStmt{Specs: []*ast.ImportSpec{spec}}, span(start, spec.Span().End))
}

// parseImportSpec parses 'pub? import-path (as id)?' (GRAMMAR group 10); the path
// is a string literal.
func (p *parser) parseImportSpec() *ast.ImportSpec {
	start := p.cur().Span.Start
	pub := p.accept(token.Pub)
	if !p.at(token.Str) {
		p.fail(p.cur().Span, "an import path must be a string literal, e.g. import \"std/io\", found %s",
			describe(p.cur().Kind))
	}
	path := p.advance()
	spec := &ast.ImportSpec{Pub: pub, Path: path.Str}
	end := path.Span.End
	if p.accept(token.As) {
		name := p.expect(token.Ident)
		spec.Alias = name.Lexeme
		end = name.Span.End
	}
	return spanned(spec, span(start, end))
}

// --- group 11: resource cleanup -----------------------------------------------

// parseDefer parses 'defer expr' (GRAMMAR group 11).
func (p *parser) parseDefer() ast.Stmt {
	start := p.expect(token.Defer).Span.Start
	call := p.parseExpr()
	return spanned(&ast.DeferStmt{Call: call}, span(start, call.Span().End))
}

// parseDel parses 'del identifier' (GRAMMAR group 11).
func (p *parser) parseDel() ast.Stmt {
	start := p.expect(token.Del).Span.Start
	name := p.expect(token.Ident)
	return spanned(&ast.DelStmt{Name: name.Lexeme}, span(start, name.Span.End))
}
