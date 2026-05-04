package syntax

// v0.6 Unit 1a — generic type parameters (decl-side) and generic type
// arguments (use-side).
//
// This file holds the parser routines for the v0.6 generics surface:
//
//   - parseTypeParams parses `[T]`, `[T: Spec]`, `[T: A + B]`, and the
//     mixed-arity `[K: Hashable, V]` forms that appear immediately after the
//     declared name on fn / struct / enum / spec / spec-method / impl-method,
//     and immediately after the `impl` keyword on a generic impl block.
//   - parseTypeArgList parses the `[T1, T2, ...]` tail of a use-site generic
//     type reference (`Box[int]`, `Result[int, str]`, `list[Option[int]]`).
//
// Both routines reject empty bracket lists (`[]`) and trailing commas, in
// keeping with the rest of the v0.5 parser's "no trailing comma in
// declaration positions" stance — the codebase admits a trailing comma only
// inside expression-position lists (`[1, 2, 3,]`) and tuple literals.

// parseTypeParams consumes a `[ TypeParam (, TypeParam)* ]` list. The opening
// `[` has been peeked but not yet consumed. Returns nil and a parse error on
// any malformed shape; an empty list is rejected.
//
// Each TypeParam is `IDENT [ ":" type_ref { "+" type_ref } ]`. The bound
// list captures multi-bound shapes; bounds that name a `pub` keyword (or any
// reserved word) reject before the resulting AST escapes the parser. PLAN.md
// §Generic type parameters keeps the syntax flat — no `where` clauses, no
// variance annotations.
func (p *parser) parseTypeParams() ([]TypeParam, error) {
	openTok, err := p.expectParen(KindLBracket, "in type-parameter list")
	if err != nil {
		return nil, err
	}
	if p.peek().Kind == KindRBracket {
		closeTok := p.advance()
		// parenDepth was bumped on the opening `[` and must symmetrically
		// drop here even though we are erroring out — keep the bookkeeping
		// honest before returning.
		p.parenDepth--
		_ = closeTok
		return nil, errorAt(openTok.Pos, "type parameter list cannot be empty")
	}

	var params []TypeParam
	seen := map[string]bool{}
	for {
		nameTok := p.peek()
		if nameTok.Kind != KindIdent {
			if isKeywordKind(nameTok.Kind) {
				return nil, errorAt(nameTok.Pos, "type parameter name cannot be a reserved keyword %q", keywordSpelling(nameTok))
			}
			return nil, errorAtTok(nameTok, "expected type parameter name, got %s", nameTok.Kind)
		}
		p.advance()
		if seen[nameTok.Value] {
			return nil, errorAt(nameTok.Pos, "type parameter %q declared twice", nameTok.Value)
		}
		seen[nameTok.Value] = true
		tp := TypeParam{Name: nameTok.Value, Pos: nameTok.Pos}

		if p.peek().Kind == KindColon {
			p.advance() // consume `:`
			bound, err := p.parseTypeParamBound()
			if err != nil {
				return nil, err
			}
			tp.Bounds = append(tp.Bounds, bound)
			for p.peek().Kind == KindPlus {
				p.advance() // consume `+`
				more, err := p.parseTypeParamBound()
				if err != nil {
					return nil, err
				}
				tp.Bounds = append(tp.Bounds, more)
			}
		}
		params = append(params, tp)

		if p.peek().Kind == KindComma {
			commaTok := p.advance()
			if p.peek().Kind == KindRBracket {
				return nil, errorAt(commaTok.Pos, "trailing comma not allowed in type parameter list")
			}
			continue
		}
		break
	}
	if _, err := p.expectParen(KindRBracket, "to close type-parameter list"); err != nil {
		return nil, err
	}
	return params, nil
}

// parseTypeParamBound parses a single spec bound on a type-parameter slot.
// `pub` (or any other reserved keyword) inside a bound is rejected here so
// `[T: pub]` and `[T: pub Foo]` produce a clean diagnostic. The bound is
// otherwise a regular TypeRef so `[T: list[int]]`, `[T: Box[int]]`, or
// `[T: Foo[Bar]]` parse with the same machinery — typeck (Unit 3) decides
// whether the named type is admissible as a spec.
func (p *parser) parseTypeParamBound() (*TypeRef, error) {
	t := p.peek()
	if isKeywordKind(t.Kind) {
		return nil, errorAt(t.Pos, "type parameter bound cannot be a reserved keyword %q", keywordSpelling(t))
	}
	return p.parseTypeRef()
}

// parseTypeArgList consumes the `[ TypeRef (, TypeRef)* ]` tail of a use-site
// generic type reference. The opening `[` has been peeked but not yet
// consumed; the head identifier is the caller's responsibility.
//
// Empty arg lists (`Foo[]`) and trailing commas are rejected to match the
// type-parameter-list style. Nested type-args (`Box[Result[int, str]]`)
// recurse through the regular parseTypeRef path, so any depth is admitted
// naturally.
func (p *parser) parseTypeArgList() ([]*TypeRef, error) {
	openTok, err := p.expectParen(KindLBracket, "in type-argument list")
	if err != nil {
		return nil, err
	}
	if p.peek().Kind == KindRBracket {
		closeTok := p.advance()
		p.parenDepth--
		_ = closeTok
		return nil, errorAt(openTok.Pos, "type argument list cannot be empty")
	}
	var args []*TypeRef
	for {
		arg, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if p.peek().Kind == KindComma {
			commaTok := p.advance()
			if p.peek().Kind == KindRBracket {
				return nil, errorAt(commaTok.Pos, "trailing comma not allowed in type argument list")
			}
			continue
		}
		break
	}
	if _, err := p.expectParen(KindRBracket, "to close type-argument list"); err != nil {
		return nil, err
	}
	return args, nil
}
