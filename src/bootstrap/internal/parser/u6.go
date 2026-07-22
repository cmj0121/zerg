// U6 parser: the group 6 surface (control flow & pattern matching) and the two
// remaining group 8 statements (guard-expr, raise). It holds the 'if' statement
// and expression forms with their binding heads, the three 'for' loops, 'with',
// 'break'/'continue' with a trailing condition, and the full pattern grammar —
// variant / struct / tuple / list / or / as patterns, the provisional bare-name
// NamePattern, and the match-only range arm.
package parser

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// --- if (statement & expression) ----------------------------------------------

// parseIfBranch parses one 'if'/'else if' head and its block. The head is a
// binding form 'id := e' (Bind set) or a plain expression, both parsed in
// no-brace mode so a leading '{' opens the body block.
func (p *parser) parseIfBranch() ast.IfBranch {
	var br ast.IfBranch
	if p.at(token.Ident) && p.peek(1).Kind == token.Walrus {
		name := p.advance()
		p.advance() // ':='
		br.Bind = name.Lexeme
	}
	br.Cond = p.headExpr()
	br.Body = p.parseBlock()
	return br
}

// parseIfChain parses the shared 'if head block (else if head block)*' prefix,
// returning the branches, the optional trailing else block, and the end position.
func (p *parser) parseIfChain() ([]ast.IfBranch, *ast.Block, token.Pos) {
	branches := []ast.IfBranch{p.parseIfBranch()}
	end := branches[0].Body.Span().End
	var elseBlock *ast.Block
	for {
		if !p.at(token.Else) {
			// W4: 'else' must sit on the same line as the preceding branch's '}'
			// (Go-style). A newline before it inserts an ASI ';'; tolerate that
			// with one dedicated diagnostic and still attach the 'else', so the
			// isolated keyword does not cascade into 'expected an expression' at
			// the 'else' plus 'expected a declaration' at the stray '}'.
			if p.at(token.Semi) && p.elseAfterSemis() {
				p.skipSemis()
				p.errorf(p.cur().Span,
					"'else' must be on the same line as the closing '}' of the preceding branch")
			} else {
				break
			}
		}
		p.advance() // 'else'
		if p.accept(token.If) {
			br := p.parseIfBranch()
			branches = append(branches, br)
			end = br.Body.Span().End
			continue
		}
		elseBlock = p.parseBlock()
		end = elseBlock.Span().End
		break
	}
	return branches, elseBlock, end
}

// elseAfterSemis reports whether, skipping a run of statement separators, the
// next token is 'else' — the "newline before else" case (W4).
func (p *parser) elseAfterSemis() bool {
	for i := p.pos; i < len(p.toks); i++ {
		if p.toks[i].Kind == token.Semi {
			continue
		}
		return p.toks[i].Kind == token.Else
	}
	return false
}

// parseIf parses the 'if' statement (GRAMMAR group 6): the trailing 'else' is
// optional and no value is produced. At statement position the statement form
// always wins.
func (p *parser) parseIf() ast.Stmt {
	start := p.expect(token.If).Span.Start
	branches, elseBlock, end := p.parseIfChain()
	return spanned(&ast.IfStmt{Branches: branches, Else: elseBlock}, span(start, end))
}

// parseIfExpr parses the 'if' expression (GRAMMAR group 4's primary): the
// trailing 'else' is MANDATORY, and the expression yields the taken branch's
// value.
func (p *parser) parseIfExpr() ast.Expr {
	start := p.expect(token.If).Span.Start
	branches, elseBlock, end := p.parseIfChain()
	if elseBlock == nil {
		p.fail(span(start, end), "an if-expression requires a trailing 'else'")
	}
	return spanned(&ast.IfExpr{Branches: branches, Else: elseBlock}, span(start, end))
}

// --- for / with / guard / raise / break / continue ----------------------------

// parseFor parses the one loop in its three forms (GRAMMAR group 6): infinite
// 'for { }', iterate 'for mut? id in e { }', or while 'for expr { }'. The iterate
// form is taken when 'mut' or an 'identifier in' follows 'for'; a while-loop on
// membership must be parenthesized ('for (v in r) { }').
func (p *parser) parseFor() ast.Stmt {
	start := p.expect(token.For).Span.Start
	if p.at(token.LBrace) {
		body := p.parseBlock()
		return spanned(&ast.ForStmt{Body: body}, span(start, body.Span().End))
	}
	if p.at(token.Mut) || (p.at(token.Ident) && p.peek(1).Kind == token.In) {
		mut := p.accept(token.Mut)
		v := p.expect(token.Ident)
		p.expect(token.In)
		iter := p.headExpr()
		body := p.parseBlock()
		return spanned(&ast.ForStmt{Mut: mut, Var: v.Lexeme, Iter: iter, Body: body},
			span(start, body.Span().End))
	}
	cond := p.headExpr()
	body := p.parseBlock()
	return spanned(&ast.ForStmt{Cond: cond, Body: body}, span(start, body.Span().End))
}

// parseWith parses 'with e (as id)? block' (GRAMMAR group 6): a scoped resource
// whose teardown runs on every exit; 'as id' is optional.
func (p *parser) parseWith() ast.Stmt {
	start := p.expect(token.With).Span.Start
	w := &ast.WithStmt{Resource: p.headExpr()}
	if p.accept(token.As) {
		w.Var = p.expect(token.Ident).Lexeme
	}
	w.Body = p.parseBlock()
	return spanned(w, span(start, w.Body.Span().End))
}

// parseGuard parses the 'guard { block }' expression (GRAMMAR group 8): it always
// takes a block, so it needs no no-brace treatment.
func (p *parser) parseGuard() ast.Expr {
	start := p.expect(token.Guard).Span.Start
	body := p.parseBlock()
	return spanned(&ast.GuardExpr{Body: body}, span(start, body.Span().End))
}

// parseRaise parses the simple statement 'raise e (from c)?' (GRAMMAR group 8).
func (p *parser) parseRaise() ast.Stmt {
	start := p.expect(token.Raise).Span.Start
	r := &ast.RaiseStmt{Value: p.parseExpr()}
	end := r.Value.Span().End
	if p.accept(token.From) {
		r.From = p.parseExpr()
		end = r.From.Span().End
	}
	return spanned(r, span(start, end))
}

// parseBreakContinue parses 'break'/'continue' with an optional trailing 'if'
// condition (GRAMMAR group 6); brk selects which statement.
func (p *parser) parseBreakContinue(brk bool) ast.Stmt {
	t := p.advance()
	var cond ast.Expr
	end := t.Span.End
	if p.accept(token.If) {
		cond = p.parseExpr()
		end = cond.Span().End
	}
	if brk {
		return spanned(&ast.BreakStmt{Cond: cond}, span(t.Span.Start, end))
	}
	return spanned(&ast.ContinueStmt{Cond: cond}, span(t.Span.Start, end))
}

// --- patterns -----------------------------------------------------------------

// parseArmPattern parses a match arm's head: a range arm when the head is a
// range bound followed by '..'/'..=', otherwise an ordinary pattern.
func (p *parser) parseArmPattern() ast.Pattern {
	if p.looksLikeRangeArm() {
		return p.parseRangeArm()
	}
	return p.parsePattern()
}

// looksLikeRangeArm reports whether the arm head is a range arm — a range bound
// ('-'? number, or a literal / identifier) immediately followed by '..' / '..='.
func (p *parser) looksLikeRangeArm() bool {
	off := 0
	if p.peek(0).Kind == token.Minus {
		off = 1
		if k := p.peek(off).Kind; k != token.Int && k != token.Float {
			return false
		}
	} else {
		switch p.peek(0).Kind {
		case token.Int, token.Float, token.Rune, token.Byte, token.Str, token.Ident:
		default:
			return false
		}
	}
	switch p.peek(off + 1).Kind {
	case token.DotDot, token.DotDotEq:
		return true
	}
	return false
}

// parseRangeArm parses 'range-bound (.. range-bound? | ..= range-bound)'.
func (p *parser) parseRangeArm() ast.Pattern {
	lo := p.parseRangeBound()
	start := lo.Span().Start
	if p.accept(token.DotDotEq) {
		hi := p.parseRangeBound()
		return spanned(&ast.RangeArm{Lo: lo, Hi: hi, Inclusive: true}, span(start, hi.Span().End))
	}
	dd := p.expect(token.DotDot)
	ra := &ast.RangeArm{Lo: lo}
	end := dd.Span.End
	if p.atRangeBoundStart() {
		ra.Hi = p.parseRangeBound()
		end = ra.Hi.Span().End
	}
	return spanned(ra, span(start, end))
}

// atRangeBoundStart reports whether the cursor begins a range bound (so a '..'
// has an upper bound, rather than being the open form '500..').
func (p *parser) atRangeBoundStart() bool {
	if p.at(token.Minus) {
		k := p.peek(1).Kind
		return k == token.Int || k == token.Float
	}
	switch p.cur().Kind {
	case token.Int, token.Float, token.Rune, token.Byte, token.Str, token.Ident:
		return true
	}
	return false
}

// parseRangeBound parses one compile-time-constant range bound: '-'? number, a
// literal, or an identifier, kept verbatim through the literal nodes' Text.
func (p *parser) parseRangeBound() ast.Expr {
	if p.at(token.Minus) {
		m := p.advance()
		lit := p.parseLiteralNode()
		return spanned(&ast.Unary{Op: token.Minus, X: lit}, span(m.Span.Start, lit.Span().End))
	}
	if p.at(token.Ident) {
		id := p.advance()
		return spanned(&ast.Ident{Name: id.Lexeme}, id.Span)
	}
	return p.parseLiteralNode()
}

// parsePattern parses a full pattern: a sub-pattern, or an or-pattern
// 'sub-pattern (| sub-pattern)+' when '|' follows (GRAMMAR group 6).
func (p *parser) parsePattern() ast.Pattern {
	first := p.parseSubPattern()
	if !p.at(token.Pipe) {
		return first
	}
	alts := []ast.Pattern{first}
	end := first.Span().End
	for p.accept(token.Pipe) {
		alt := p.parseSubPattern()
		alts = append(alts, alt)
		end = alt.Span().End
	}
	return spanned(&ast.OrPattern{Alts: alts}, span(first.Span().Start, end))
}

// parseSubPattern parses 'pattern-core (as identifier)?' (GRAMMAR group 6).
func (p *parser) parseSubPattern() ast.Pattern {
	core := p.parsePatternCore()
	if p.accept(token.As) {
		name := p.expect(token.Ident)
		return spanned(&ast.AsPattern{Inner: core, Name: name.Lexeme},
			span(core.Span().Start, name.Span.End))
	}
	return core
}

// parsePatternCore parses one pattern core: a variant / struct / tuple / list /
// literal pattern, the wildcard '_', or the provisional bare-name NamePattern.
func (p *parser) parsePatternCore() ast.Pattern {
	t := p.cur()
	switch t.Kind {
	case token.Ident:
		return p.parseNamedPattern()
	case token.LParen:
		return p.parseTuplePattern()
	case token.LBrack:
		return p.parseListPattern()
	case token.Minus:
		p.advance()
		if !p.at(token.Int) && !p.at(token.Float) {
			p.fail(p.cur().Span, "expected a number after '-' in a pattern")
		}
		lit := p.parseLiteralNode()
		return spanned(&ast.LitPattern{Lit: lit, Neg: true}, span(t.Span.Start, lit.Span().End))
	case token.Int, token.Float, token.Str, token.RawStr, token.Rune, token.Byte,
		token.True, token.False, token.Nil:
		lit := p.parseLiteralNode()
		return spanned(&ast.LitPattern{Lit: lit}, lit.Span())
	}
	p.fail(t.Span, "expected a pattern, found %s", describe(t.Kind))
	return nil
}

// parseNamedPattern parses an identifier-led pattern: '_' is the wildcard, a name
// followed by '(' is a variant, a name followed by '{' is a struct pattern, and a
// bare name is the provisional NamePattern (DESIGN §3b).
func (p *parser) parseNamedPattern() ast.Pattern {
	name := p.advance()
	if name.Lexeme == "_" {
		return spanned(&ast.WildPattern{}, name.Span)
	}
	switch p.cur().Kind {
	case token.LParen:
		return p.parseVariantPattern(name)
	case token.LBrace:
		return p.parseStructPattern(name)
	default:
		return spanned(&ast.NamePattern{Name: name.Lexeme}, name.Span)
	}
}

// parseVariantPattern parses 'type-name ( pattern-list )' — the name and its '('
// are already the current position's lexeme and next token.
func (p *parser) parseVariantPattern(name token.Token) ast.Pattern {
	p.expect(token.LParen)
	var elems []ast.Pattern
	for !p.at(token.RParen) {
		elems = append(elems, p.parsePattern())
		if !p.accept(token.Comma) {
			break
		}
	}
	rp := p.expect(token.RParen)
	return spanned(&ast.VariantPattern{Name: name.Lexeme, Elems: elems},
		span(name.Span.Start, rp.Span.End))
}

// parseStructPattern parses 'type-name { struct-fields }': field patterns with a
// shorthand or 'id: pattern' form, ending with an optional '..' rest that ignores
// the remaining fields.
func (p *parser) parseStructPattern(name token.Token) ast.Pattern {
	p.expect(token.LBrace)
	sp := &ast.StructPattern{Name: name.Lexeme}
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		if p.at(token.DotDot) {
			p.advance()
			sp.Rest = true
			break
		}
		fname := p.expect(token.Ident)
		fp := ast.FieldPattern{Name: fname.Lexeme}
		if p.accept(token.Colon) {
			fp.Pat = p.parsePattern()
		}
		sp.Fields = append(sp.Fields, fp)
		if !p.accept(token.Comma) {
			break
		}
	}
	rb := p.expect(token.RBrace)
	return spanned(sp, span(name.Span.Start, rb.Span.End))
}

// parseTuplePattern parses '( pattern-list )' (GRAMMAR group 6's tuple-pat).
func (p *parser) parseTuplePattern() ast.Pattern {
	lp := p.expect(token.LParen)
	var elems []ast.Pattern
	for !p.at(token.RParen) {
		elems = append(elems, p.parsePattern())
		if !p.accept(token.Comma) {
			break
		}
	}
	rp := p.expect(token.RParen)
	return spanned(&ast.TuplePattern{Elems: elems}, span(lp.Span.Start, rp.Span.End))
}

// parseListPattern parses '[ list-pat-elem* ]': element patterns with at most one
// rest '..name' (a bare '..' drops the run).
func (p *parser) parseListPattern() ast.Pattern {
	lb := p.expect(token.LBrack)
	lp := &ast.ListPattern{}
	for !p.at(token.RBrack) && !p.at(token.EOF) {
		if p.at(token.DotDot) {
			p.advance()
			el := ast.ListPatElem{Rest: true}
			if p.at(token.Ident) {
				el.Name = p.advance().Lexeme
			}
			lp.Elems = append(lp.Elems, el)
		} else {
			lp.Elems = append(lp.Elems, ast.ListPatElem{Pat: p.parsePattern()})
		}
		if !p.accept(token.Comma) {
			break
		}
	}
	rb := p.expect(token.RBrack)
	return spanned(lp, span(lb.Span.Start, rb.Span.End))
}
