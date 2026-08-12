// U3 parser helpers: the group 3–5 surface that does not fit the shared ladder in
// parser.go — the remaining literals' decode, closures, f-string / f-cmd holes,
// composite literals (tuple / list / fill / map, with the block-vs-map
// disambiguation), and the reassignment target shapes.
package parser

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// span builds a source span from a start and end position.
func span(start, end token.Pos) token.Span { return token.Span{Start: start, End: end} }

// decodeRune returns the single rune of a decoded rune-literal value.
func decodeRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

// decodeByte returns the single octet of a decoded byte-literal value.
func decodeByte(s string) byte {
	if len(s) > 0 {
		return s[0]
	}
	return 0
}

// --- closures -----------------------------------------------------------------

// parseFnExpr parses an anonymous function / closure 'fn(params) -> ret? block'
// (GRAMMAR group 5); a closure parameter's type may be omitted (inferred).
func (p *parser) parseFnExpr() ast.Expr {
	saved := p.noBrace
	p.noBrace = false
	defer func() { p.noBrace = saved }()

	start := p.expect(token.Fn).Span.Start
	p.expect(token.LParen)
	params := p.parseClosureParams()
	p.expect(token.RParen)
	var ret ast.Type
	if p.accept(token.Arrow) {
		ret = p.parseType()
	}
	body := p.parseBlock()
	return spanned(&ast.FnExpr{Params: params, Ret: ret, Body: body},
		span(start, body.Span().End))
}

func (p *parser) parseClosureParams() []ast.ClosureParam {
	if p.at(token.RParen) {
		return nil
	}
	var out []ast.ClosureParam
	for {
		start := p.cur().Span.Start
		ref := p.parseMutRef()
		name := p.expect(token.Ident)
		cp := ast.ClosureParam{Ref: ref, Name: name.Lexeme}
		end := name.Span.End
		if p.accept(token.Colon) {
			cp.Type = p.parseType()
			end = cp.Type.Span().End
		}
		if p.accept(token.Assign) {
			cp.Default = p.parseExpr()
			end = cp.Default.Span().End
		}
		cp.SetSpan(span(start, end))
		out = append(out, cp)
		if !p.accept(token.Comma) {
			break
		}
	}
	return out
}

// --- f-strings & f-cmds -------------------------------------------------------

// parseFStr parses an f-string from the structured token stream (DESIGN-1a §4):
// FStrBegin, then a run of text chunks and '{ … }' holes, then FStrEnd.
func (p *parser) parseFStr() ast.Expr {
	begin := p.expect(token.FStrBegin)
	parts := p.textPart(begin)
	for {
		switch p.cur().Kind {
		case token.FStrEnd:
			// the FStrEnd token is the closing '"', so it carries the whole span's end
			end := p.advance().Span.End
			return spanned(&ast.FStr{Parts: parts}, span(begin.Span.Start, end))
		case token.FStrText:
			parts = append(parts, p.textPart(p.advance())...)
		case token.LBrace:
			parts = append(parts, p.parseHole())
		default:
			p.fail(p.cur().Span, "malformed f-string near %s", describe(p.cur().Kind))
		}
	}
}

// parseFCmd parses an interpolating command literal f`…{expr}…`.
func (p *parser) parseFCmd() ast.Expr {
	begin := p.expect(token.FCmdBegin)
	parts := p.textPart(begin)
	for {
		switch p.cur().Kind {
		case token.FCmdEnd:
			// the FCmdEnd token is the closing '`', so it carries the whole span's end
			end := p.advance().Span.End
			return spanned(&ast.FCmd{Parts: parts}, span(begin.Span.Start, end))
		case token.FCmdText:
			parts = append(parts, p.textPart(p.advance())...)
		case token.LBrace:
			parts = append(parts, p.parseHole())
		default:
			p.fail(p.cur().Span, "malformed f-cmd near %s", describe(p.cur().Kind))
		}
	}
}

// textPart wraps a non-empty text token as a literal FStrPart (an empty chunk
// carries no information and is dropped so the reprint stays canonical).
func (p *parser) textPart(t token.Token) []ast.FStrPart {
	if t.Str == "" {
		return nil
	}
	return []ast.FStrPart{{Text: t.Str}}
}

// parseHole parses one '{ expr =? conv? spec? }' interpolation hole.
func (p *parser) parseHole() ast.FStrPart {
	p.expect(token.LBrace)
	part := ast.FStrPart{Expr: p.withBraces(p.parseExpr)}
	if p.accept(token.Assign) {
		part.Debug = true
	}
	if p.at(token.Conv) {
		part.Conv = p.advance().Str[0]
	}
	if p.at(token.FmtSpec) {
		spec := p.advance()
		part.Spec = spec.Str
		part.HasSpec = true
	}
	p.expect(token.RBrace)
	return part
}

// --- composite literals -------------------------------------------------------

// parseParenOrTuple parses '( expr )' grouping (the parens are dropped) or a
// 2+ element tuple literal '(a, b, …)' (GRAMMAR group 4).
func (p *parser) parseParenOrTuple() ast.Expr {
	saved := p.noBrace
	p.noBrace = false
	defer func() { p.noBrace = saved }()

	lp := p.expect(token.LParen)
	first := p.parseExpr()
	if p.at(token.Comma) {
		elems := []ast.Expr{first}
		for p.accept(token.Comma) {
			if p.at(token.RParen) {
				// A comma with nothing after it is two different ill-formed programs,
				// told apart by how many elements are already in hand: after one it is
				// the 1-tuple, caught below by the arity rule; after two or more it is a
				// trailing comma, which no comma-separated production in GRAMMAR derives.
				if len(elems) >= 2 {
					p.fail(p.cur().Span, "a trailing comma before the closing `)` of a tuple literal")
				}
				break
			}
			elems = append(elems, p.parseExpr())
		}
		rp := p.expect(token.RParen)
		// GRAMMAR#tuple-lit — a tuple literal is 2+ elements; '(a)' is grouping and a
		// trailing-comma '(a,)' is not a 1-tuple, it is a syntax error.
		if len(elems) < 2 {
			p.fail(span(lp.Span.Start, rp.Span.End), "a tuple literal needs 2 or more elements")
		}
		return spanned(&ast.TupleLit{Elems: elems}, span(lp.Span.Start, rp.Span.End))
	}
	p.expect(token.RParen)
	return first
}

// parseListLit parses a list literal: the comma form '[a, b]' / empty '[]' or the
// fill form '[v; N]' (GRAMMAR group 4).
func (p *parser) parseListLit() ast.Expr {
	saved := p.noBrace
	p.noBrace = false
	defer func() { p.noBrace = saved }()

	lb := p.expect(token.LBrack)
	if p.at(token.RBrack) {
		rb := p.advance()
		return spanned(&ast.ListLit{}, span(lb.Span.Start, rb.Span.End))
	}
	first := p.parseExpr()
	if p.accept(token.Semi) { // fill form '[v; N]'
		count := p.parseExpr()
		rb := p.expect(token.RBrack)
		return spanned(&ast.ListFill{Value: first, Count: count}, span(lb.Span.Start, rb.Span.End))
	}
	elems := []ast.Expr{first}
	for p.accept(token.Comma) {
		if p.at(token.RBrack) {
			// GRAMMAR#list-lit writes the comma BETWEEN elements and derives none before
			// the closer; this loop used to leave on one and build the list anyway.
			p.fail(p.cur().Span, "a trailing comma before the closing `]` of a list literal")
		}
		elems = append(elems, p.parseExpr())
	}
	rb := p.expect(token.RBrack)
	return spanned(&ast.ListLit{Elems: elems}, span(lb.Span.Start, rb.Span.End))
}

// parseBraceExpr resolves a '{'-opening expression: a map literal when the braces
// carry a ':'-bearing entry, otherwise a block expression (GRAMMAR group 4).
func (p *parser) parseBraceExpr() ast.Expr {
	if p.looksLikeMap() {
		return p.parseMapLit()
	}
	return p.parseBlock()
}

// looksLikeMap peeks from the current '{' to its matching '}': the braces are a map
// when a ':' appears at brace depth 1 before any statement separator, and a block
// otherwise (the same rule that makes a bare '{}' a block and '{:}' the empty map).
func (p *parser) looksLikeMap() bool {
	depth := 0
	for i := p.pos; i < len(p.toks); i++ {
		switch p.toks[i].Kind {
		case token.LBrace, token.LParen, token.LBrack:
			depth++
		case token.RBrace, token.RParen, token.RBrack:
			depth--
			if depth == 0 {
				return false
			}
		case token.Colon:
			if depth == 1 {
				return true
			}
		case token.Semi:
			if depth == 1 {
				return false
			}
		case token.EOF:
			return false
		}
	}
	return false
}

// parseMapLit parses a map literal '{k: v, …}' or the empty map '{:}'.
func (p *parser) parseMapLit() ast.Expr {
	saved := p.noBrace
	p.noBrace = false
	defer func() { p.noBrace = saved }()

	lb := p.expect(token.LBrace)
	if p.at(token.Colon) && p.peek(1).Kind == token.RBrace {
		p.advance() // ':'
		rb := p.advance()
		return spanned(&ast.MapLit{}, span(lb.Span.Start, rb.Span.End))
	}
	var entries []ast.MapEntry
	for {
		p.skipSemis()
		key := p.parseExpr()
		p.expect(token.Colon)
		val := p.parseExpr()
		entries = append(entries, ast.MapEntry{Key: key, Value: val})
		if !p.accept(token.Comma) {
			break
		}
		p.skipSemis()
		if p.at(token.RBrace) {
			// GRAMMAR#map-lit writes the comma BETWEEN entries and derives none before the
			// closer, so `{"a": 1,}` is not a map literal — this loop used to leave on it.
			//
			// It reports and BREAKS rather than bailing out: unwinding from inside a brace
			// leaves the recovery point past the literal's own '}', so the enclosing block's
			// closer was then read as a stray one and a second, invented diagnostic came
			// with it. Reading the map to its end costs nothing and says the rule once.
			p.errorf(p.cur().Span, "a trailing comma before the closing `}` of a map literal")
			break
		}
	}
	p.skipSemis()
	rb := p.expect(token.RBrace)
	return spanned(&ast.MapLit{Entries: entries}, span(lb.Span.Start, rb.Span.End))
}

// --- reassignment targets -----------------------------------------------------

// finishReassign consumes the '=' after a parsed target expression and builds a
// Reassign; the expression becomes an lvalue or (a tuple literal) a tuple shape.
func (p *parser) finishReassign(target ast.Expr) ast.Stmt {
	p.expect(token.Assign)
	val := p.parseExpr()
	tgt := p.exprToTarget(target)
	return spanned(&ast.Reassign{Target: tgt, Value: val},
		span(target.Span().Start, val.Span().End))
}

// exprToTarget reinterprets a parsed expression as an assignment target: a tuple
// literal becomes a tuple shape, anything else an lvalue.
func (p *parser) exprToTarget(e ast.Expr) ast.AssignTarget {
	if tl, ok := e.(*ast.TupleLit); ok {
		var elems []ast.AssignTarget
		for _, el := range tl.Elems {
			elems = append(elems, p.exprToTarget(el))
		}
		return spanned(&ast.TupleTarget{Elems: elems}, tl.Span())
	}
	return spanned(&ast.LValueTarget{X: e}, e.Span())
}

// looksLikeStructReassign peeks past an 'Ident { … }' to decide whether it is a
// struct-shape reassignment target (its matching '}' is followed by '='), rather
// than an identifier expression followed by a block.
func (p *parser) looksLikeStructReassign() bool {
	depth := 0
	for i := p.pos + 1; i < len(p.toks); i++ { // start at the '{'
		switch p.toks[i].Kind {
		case token.LBrace, token.LParen, token.LBrack:
			depth++
		case token.RBrace, token.RParen, token.RBrack:
			depth--
			if depth == 0 {
				return i+1 < len(p.toks) && p.toks[i+1].Kind == token.Assign
			}
		case token.Semi:
			if depth == 1 {
				return false
			}
		case token.EOF:
			return false
		}
	}
	return false
}

// afterMatched returns the kind of the token immediately after the bracket opened at
// index `open` closes (its nesting depth returns to zero). A statement separator at
// depth 1 aborts the scan (returns Illegal), so a peek never runs past a statement
// boundary; unbalanced or exhausted input returns EOF.
func (p *parser) afterMatched(open int) token.Kind {
	depth := 0
	for i := open; i < len(p.toks); i++ {
		switch p.toks[i].Kind {
		case token.LBrace, token.LParen, token.LBrack:
			depth++
		case token.RBrace, token.RParen, token.RBrack:
			depth--
			if depth == 0 {
				if i+1 < len(p.toks) {
					return p.toks[i+1].Kind
				}
				return token.EOF
			}
		case token.Semi:
			if depth == 1 {
				return token.Illegal
			}
		case token.EOF:
			return token.EOF
		}
	}
	return token.EOF
}

// looksLikeTupleBind reports whether a '(' at the cursor opens a tuple destructuring
// bind target (its matching ')' is followed by ':='), rather than a parenthesized
// expression or a tuple literal statement.
func (p *parser) looksLikeTupleBind() bool {
	return p.at(token.LParen) && p.afterMatched(p.pos) == token.Walrus
}

// looksLikeStructBind reports whether an 'Ident {' at the cursor opens a struct
// destructuring bind target (its matching '}' is followed by ':='), rather than a
// struct-shape reassignment ('… } =') or an identifier followed by a block.
func (p *parser) looksLikeStructBind() bool {
	return p.afterMatched(p.pos+1) == token.Walrus
}

// parseStructReassign parses 'Type{field-targets} = expr'.
func (p *parser) parseStructReassign() ast.Stmt {
	name := p.expect(token.Ident)
	tgt := p.parseStructTarget(name)
	p.expect(token.Assign)
	val := p.parseExpr()
	return spanned(&ast.Reassign{Target: tgt, Value: val},
		span(name.Span.Start, val.Span().End))
}

// parseStructTarget parses the '{ field-target, … (, ..)? }' of a struct-shape
// target; a trailing '..' ignores the remaining fields.
func (p *parser) parseStructTarget(name token.Token) ast.AssignTarget {
	p.expect(token.LBrace)
	st := &ast.StructTarget{TypeName: name.Lexeme}
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		if p.at(token.DotDot) {
			p.advance()
			st.Rest = true
			break
		}
		fname := p.expect(token.Ident)
		ft := ast.FieldTarget{Name: fname.Lexeme}
		if p.accept(token.Colon) {
			ft.Target = p.parseAssignTarget()
		}
		st.Fields = append(st.Fields, ft)
		if !p.accept(token.Comma) {
			break
		}
	}
	rb := p.expect(token.RBrace)
	return spanned(st, span(name.Span.Start, rb.Span.End))
}

// parseAssignTarget parses a nested assignment target (a struct field's value): a
// tuple shape, a struct shape, or an lvalue.
func (p *parser) parseAssignTarget() ast.AssignTarget {
	switch {
	case p.at(token.LParen):
		lp := p.advance()
		var elems []ast.AssignTarget
		for !p.at(token.RParen) {
			elems = append(elems, p.parseAssignTarget())
			if !p.accept(token.Comma) {
				break
			}
		}
		rp := p.expect(token.RParen)
		// same 2+-element rule as a tuple literal (GRAMMAR#tuple-lit)
		if len(elems) < 2 {
			p.fail(span(lp.Span.Start, rp.Span.End), "a tuple target needs 2 or more elements")
		}
		return spanned(&ast.TupleTarget{Elems: elems}, span(lp.Span.Start, rp.Span.End))
	case p.at(token.Ident) && p.peek(1).Kind == token.LBrace:
		name := p.advance()
		return p.parseStructTarget(name)
	default:
		e := p.parsePostfix()
		return spanned(&ast.LValueTarget{X: e}, e.Span())
	}
}
