package syntax

import (
	"fmt"
	"strconv"
)

// ParseError is returned when the parser cannot fit the token stream into the
// v0.1 grammar.
//
// Incomplete is true when the failure was caused by the input running out
// mid-construct (EOF where more tokens were required) — e.g. a brace-block
// without its closing `}`, an expression that stops at EOF, a `let` whose
// `:=`/`: T =` is missing because the user has not typed the rest yet. The
// REPL uses this flag to keep accumulating input rather than reporting a
// hard error.
type ParseError struct {
	Pos        Position
	Message    string
	Incomplete bool
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	return fmt.Sprintf("parse error at %s: %s", e.Pos, e.Message)
}

// IsIncomplete reports whether this error was caused by the input running
// out mid-construct. The REPL's try-parse loop uses it to decide whether to
// keep reading more input or to surface the error to the user.
func (e *ParseError) IsIncomplete() bool { return e != nil && e.Incomplete }

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
// with the offending token's position. If the offending token is EOF the
// error is flagged Incomplete so the REPL can keep accumulating input.
func (p *parser) expect(k Kind, ctx string) (Token, error) {
	t := p.peek()
	if t.Kind != k {
		return Token{}, &ParseError{
			Pos:        t.Pos,
			Message:    fmt.Sprintf("expected %s%s, got %s", k, ctxSuffix(ctx), t.Kind),
			Incomplete: t.Kind == KindEOF,
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

// errorAtTok builds a ParseError at the given token's position. If the
// offending token is EOF the error is flagged Incomplete — the REPL relies
// on that flag to decide whether to keep reading input.
func errorAtTok(t Token, format string, args ...any) error {
	return &ParseError{
		Pos:        t.Pos,
		Message:    fmt.Sprintf(format, args...),
		Incomplete: t.Kind == KindEOF,
	}
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
		return errorAtTok(t, "expected newline or end of statement, got %s", t.Kind)
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
	case KindStruct:
		return p.parseStructDecl()
	case KindEnum:
		return p.parseEnumDecl()
	case KindMatch:
		return p.parseMatchStmt()
	case KindSpec:
		return p.parseSpecDecl()
	case KindImpl:
		return p.parseImplDecl()
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

// parseDecl handles let/mut/const in three shapes:
//
//   - `let name := expr` / `mut name := expr` / `const name := expr`
//   - `let name : T = expr` (annotated)
//   - `let (a, b, ...) := expr` — v0.2 tuple-destructure declaration. The
//     parenthesised LHS introduces ≥ 2 fresh names in the current scope; the
//     RHS must be a tuple of matching arity (typeck enforces). v0.2 does not
//     admit a type annotation on the destructure form — typeck infers from
//     the RHS shape.
func (p *parser) parseDecl(kind declKind) (Stmt, error) {
	keyword := p.advance() // consume let/mut/const

	// Tuple-destructure LHS: `let (a, b) := expr`.
	if p.peek().Kind == KindLParen {
		return p.parseTupleDestructureDecl(kind, keyword.Pos)
	}

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
		return nil, errorAtTok(t, "expected ':=' or ': T =' after %s name, got %s", kindLabel(kind), t.Kind)
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

// parseTupleDestructureDecl consumes the parenthesised LHS of a destructure
// declaration plus its `:=` and RHS. The leading keyword (`let`/`mut`/
// `const`) and its position have been consumed by the caller; the cursor is
// at `(`.
//
// Grammar: `'(' IDENT (',' IDENT)+ ','? ')' ':=' expr`. PLAN-pinned: ≥ 2
// names — `let (a) := …` is a parse error (ParenExpr-grouping is reserved
// for expression position). Repeated names within the same LHS are a
// parse-time error so typeck doesn't have to catch a contradictory binding
// pair. v0.2 admits no type annotation on the destructure form — the parser
// rejects `: T` between `)` and `:=` so we do not have to teach typeck about
// annotated destructures.
func (p *parser) parseTupleDestructureDecl(kind declKind, keywordPos Position) (Stmt, error) {
	openTok, err := p.expectParen(KindLParen, "in destructure "+kindLabel(kind))
	if err != nil {
		return nil, err
	}
	var names []string
	var positions []Position
	seen := map[string]bool{}
	for {
		if p.peek().Kind == KindRParen {
			break
		}
		nameTok, err := p.expect(KindIdent, "in destructure name list")
		if err != nil {
			return nil, err
		}
		if seen[nameTok.Value] {
			return nil, errorAt(nameTok.Pos, "name %q repeated in destructure pattern", nameTok.Value)
		}
		seen[nameTok.Value] = true
		names = append(names, nameTok.Value)
		positions = append(positions, nameTok.Pos)
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expectParen(KindRParen, "to close destructure pattern"); err != nil {
		return nil, err
	}
	if len(names) < 2 {
		return nil, errorAt(openTok.Pos, "destructure pattern requires at least 2 names (use the single-name form for one)")
	}
	// `:=` only — annotated destructure deferred at v0.2.
	if k := p.peek().Kind; k == KindColon {
		bad := p.peek()
		return nil, errorAt(bad.Pos, "type annotations on destructure declarations are not supported at v0.2")
	}
	if _, err := p.expect(KindWalrus, "after destructure pattern"); err != nil {
		return nil, err
	}
	value, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	tb := &TupleBinding{Pos: openTok.Pos, Names: names, NamePos: positions}
	switch kind {
	case declLet:
		return &LetStmt{Pos: keywordPos, Tuple: tb, Value: value}, nil
	case declMut:
		return &MutStmt{Pos: keywordPos, Tuple: tb, Value: value}, nil
	case declConst:
		return &ConstStmt{Pos: keywordPos, Tuple: tb, Value: value}, nil
	}
	return nil, errorAt(keywordPos, "internal error: unknown decl kind")
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

// parseTypeRef parses a type reference. v0.2 admits three shapes:
//
//   - bare identifier ("int", "Point") → TypeRefNamed
//   - "list" '[' type_ref ']'           → TypeRefList
//   - "tuple" '[' type_ref (',' type_ref)+ ','? ']' → TypeRefTuple (≥ 2)
//
// `list` and `tuple` are not reserved keywords (they remain regular
// identifiers so users can still bind names like `let list := ...`); they
// trigger compound parsing only when they appear in type-ref position
// followed by `[`.
func (p *parser) parseTypeRef() (*TypeRef, error) {
	t := p.peek()
	if t.Kind != KindIdent {
		return nil, errorAtTok(t, "expected type name, got %s", t.Kind)
	}
	p.advance()
	// Compound shapes: `list[T]` and `tuple[T1, T2, ...]`. We lookahead one
	// token; if `[` follows the constructor name we parse the brackets,
	// otherwise the bare ident stands alone (lets the user keep `list` as a
	// plain user-defined type if they really want).
	if (t.Value == "list" || t.Value == "tuple") && p.peek().Kind == KindLBracket {
		if t.Value == "list" {
			return p.parseListTypeRef(t.Pos)
		}
		return p.parseTupleTypeRef(t.Pos)
	}
	return &TypeRef{Kind: TypeRefNamed, Name: t.Value, Pos: t.Pos}, nil
}

// parseListTypeRef consumes the `[ T ]` tail of a `list[T]` type reference.
// The opening `[` has been peeked but not yet consumed.
func (p *parser) parseListTypeRef(headPos Position) (*TypeRef, error) {
	if _, err := p.expectParen(KindLBracket, "in 'list' type"); err != nil {
		return nil, err
	}
	elt, err := p.parseTypeRef()
	if err != nil {
		return nil, err
	}
	if _, err := p.expectParen(KindRBracket, "to close 'list' type"); err != nil {
		return nil, err
	}
	return &TypeRef{Kind: TypeRefList, Pos: headPos, Element: elt}, nil
}

// parseTupleTypeRef consumes the `[ T1, T2, ...]` tail of `tuple[...]`.
func (p *parser) parseTupleTypeRef(headPos Position) (*TypeRef, error) {
	if _, err := p.expectParen(KindLBracket, "in 'tuple' type"); err != nil {
		return nil, err
	}
	var elements []*TypeRef
	for {
		// Allow trailing comma by peeking for `]` first.
		if p.peek().Kind == KindRBracket {
			break
		}
		elt, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		elements = append(elements, elt)
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	closeTok, err := p.expectParen(KindRBracket, "to close 'tuple' type")
	if err != nil {
		return nil, err
	}
	if len(elements) < 2 {
		// PLAN: tuple types are ≥ 2 elements. Emit a precise diagnostic at
		// the closing bracket so users see exactly where the shape fails.
		return nil, errorAt(closeTok.Pos, "tuple type requires at least 2 element types, got %d", len(elements))
	}
	return &TypeRef{Kind: TypeRefTuple, Pos: headPos, Elements: elements}, nil
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

// parseStructDecl handles `struct Name { field: T, ... }`. NEWLINE between
// fields is allowed but not required; `parenDepth` is bumped for the brace
// region so multi-line decls work without the user juggling separators.
func (p *parser) parseStructDecl() (Stmt, error) {
	kw := p.advance() // consume `struct`
	nameTok, err := p.expect(KindIdent, "after 'struct'")
	if err != nil {
		return nil, err
	}
	open, err := p.expect(KindLBrace, "in struct declaration")
	if err != nil {
		return nil, err
	}
	// Treat the brace region like a paren region for NEWLINE skipping so
	// multi-line struct decls Just Work without the user having to align
	// commas at line ends. The open brace has already been consumed; we
	// bump parenDepth manually to mirror what expectParen would do for
	// brackets/parens.
	p.parenDepth++
	defer func() { p.parenDepth-- }()
	_ = open

	var fields []FieldDecl
	for {
		if p.peek().Kind == KindRBrace {
			break
		}
		nameT, err := p.expect(KindIdent, "in struct field list")
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(KindColon, "after struct field name"); err != nil {
			return nil, err
		}
		fieldType, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		fields = append(fields, FieldDecl{
			Name: nameT.Value,
			Type: fieldType,
			Pos:  nameT.Pos,
		})
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expect(KindRBrace, "to close struct declaration"); err != nil {
		return nil, err
	}
	return &StructDecl{Pos: kw.Pos, Name: nameTok.Value, Fields: fields}, nil
}

// parseEnumDecl handles `enum Name { Variant, Variant(T1, T2), ... }`. v0.2
// admitted only bare variant identifiers; v0.4 (Unit 2) extends each variant
// to carry an optional `( type_list )` payload.
//
// Bare form (no parens) and payload form (≥ 1 type, no trailing comma) are
// both admitted. An empty `()` is rejected with a focused diagnostic — bare
// variants MUST drop the parentheses entirely.
func (p *parser) parseEnumDecl() (Stmt, error) {
	kw := p.advance() // consume `enum`
	nameTok, err := p.expect(KindIdent, "after 'enum'")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(KindLBrace, "in enum declaration"); err != nil {
		return nil, err
	}
	p.parenDepth++
	defer func() { p.parenDepth-- }()

	var variants []VariantDecl
	for {
		if p.peek().Kind == KindRBrace {
			break
		}
		v, err := p.expect(KindIdent, "in enum variant list")
		if err != nil {
			return nil, err
		}
		variant := VariantDecl{Name: v.Value, Pos: v.Pos}
		if p.peek().Kind == KindLParen {
			payload, err := p.parseVariantPayloadTypes()
			if err != nil {
				return nil, err
			}
			variant.Payload = payload
		}
		variants = append(variants, variant)
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expect(KindRBrace, "to close enum declaration"); err != nil {
		return nil, err
	}
	return &EnumDecl{Pos: kw.Pos, Name: nameTok.Value, Variants: variants}, nil
}

// parseVariantPayloadTypes consumes the `( type ( ',' type )* )` tail of a
// variant declaration. The opening `(` has been peeked but not yet consumed.
//
// Empty parens (`V()`) are rejected: bare variants drop the parentheses
// entirely, so an empty payload is never the user's intent. A trailing comma
// (`V(int,)`) is also rejected — variant payload type lists are pinned to no
// trailing comma.
func (p *parser) parseVariantPayloadTypes() ([]*TypeRef, error) {
	openTok, err := p.expectParen(KindLParen, "in enum variant payload")
	if err != nil {
		return nil, err
	}
	if p.peek().Kind == KindRParen {
		return nil, errorAt(openTok.Pos,
			"empty parentheses are not allowed; use the bare variant name")
	}
	var types []*TypeRef
	for {
		ty, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		types = append(types, ty)
		if p.peek().Kind == KindComma {
			commaTok := p.advance()
			if p.peek().Kind == KindRParen {
				return nil, errorAt(commaTok.Pos,
					"trailing comma not allowed in enum variant payload type list")
			}
			continue
		}
		break
	}
	if _, err := p.expectParen(KindRParen, "to close enum variant payload"); err != nil {
		return nil, err
	}
	return types, nil
}

// parseSpecDecl handles `spec Name { method_decl* }`. A method decl is the
// `fn` form with an optional brace-block body: signature-only (no block)
// declares a method that every impl MUST provide; with-block declares a
// default that an impl may inherit or override.
//
// Empty bodies (`spec Empty {}`) are admitted — useful as marker specs.
// NEWLINEs between methods are absorbed by the brace-region parenDepth bump.
func (p *parser) parseSpecDecl() (Stmt, error) {
	kw := p.advance() // consume `spec`
	nameTok, err := p.expect(KindIdent, "after 'spec'")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(KindLBrace, "in spec declaration"); err != nil {
		return nil, err
	}

	var methods []*SpecMethod
	for {
		p.skipNewlines()
		if p.peekRaw().Kind == KindRBrace {
			break
		}
		m, err := p.parseSpecMethod()
		if err != nil {
			return nil, err
		}
		methods = append(methods, m)
	}
	if _, err := p.expect(KindRBrace, "to close spec declaration"); err != nil {
		return nil, err
	}
	return &SpecDecl{Pos: kw.Pos, Name: nameTok.Value, Methods: methods}, nil
}

// parseSpecMethod consumes one `fn IDENT (params?) (-> type)? block?` entry.
// The block is optional inside a spec — its absence marks the method as
// signature-only (must be implemented by every type that impls the spec).
func (p *parser) parseSpecMethod() (*SpecMethod, error) {
	kw, err := p.expect(KindFn, "in spec method")
	if err != nil {
		return nil, err
	}
	nameTok, err := p.expect(KindIdent, "after 'fn'")
	if err != nil {
		return nil, err
	}
	if _, err := p.expectParen(KindLParen, "in spec method signature"); err != nil {
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
	if _, err := p.expectParen(KindRParen, "in spec method signature"); err != nil {
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
	// A `{` here means a default implementation; otherwise the method is
	// signature-only and the next token is either a NEWLINE separator or `}`
	// closing the spec body.
	var body *Block
	if p.peek().Kind == KindLBrace {
		b, err := p.parseBlock("spec method default body")
		if err != nil {
			return nil, err
		}
		body = b
	}
	return &SpecMethod{
		Pos:    kw.Pos,
		Name:   nameTok.Value,
		Params: params,
		Return: ret,
		Body:   body,
	}, nil
}

// parseImplDecl handles both inherent and for-spec impl blocks:
//
//   - `impl Type { fn ... fn ... }`               — inherent
//   - `impl Type for Spec { fn ... }`             — for-spec
//   - `impl Type for Spec {}`                     — for-spec, empty body
//
// We reject the bare `for` followed by `{` (missing spec name) with a focused
// diagnostic rather than letting the lower-level "expected identifier" leak.
func (p *parser) parseImplDecl() (Stmt, error) {
	kw := p.advance() // consume `impl`
	typeNameTok, err := p.expect(KindIdent, "after 'impl'")
	if err != nil {
		return nil, err
	}
	specName := ""
	if p.peek().Kind == KindFor {
		forTok := p.advance()
		st := p.peek()
		if st.Kind != KindIdent {
			return nil, errorAt(forTok.Pos, "expected spec name after 'for', got %s", st.Kind)
		}
		p.advance()
		specName = st.Value
	}
	if _, err := p.expect(KindLBrace, "in impl declaration"); err != nil {
		return nil, err
	}

	var methods []*FnDecl
	for {
		p.skipNewlines()
		if p.peekRaw().Kind == KindRBrace {
			break
		}
		// Methods inside an impl reuse the FnDecl shape unchanged. Typeck
		// (Unit 3) routes them to the impl rather than to the global fn
		// table, so the parser doesn't need a separate node type.
		stmt, err := p.parseFnDecl()
		if err != nil {
			return nil, err
		}
		fn, ok := stmt.(*FnDecl)
		if !ok {
			return nil, errorAt(stmt.StmtPos(), "internal: parseFnDecl produced %T", stmt)
		}
		methods = append(methods, fn)
	}
	if _, err := p.expect(KindRBrace, "to close impl declaration"); err != nil {
		return nil, err
	}
	return &ImplDecl{
		Pos:     kw.Pos,
		Type:    typeNameTok.Value,
		Spec:    specName,
		Methods: methods,
	}, nil
}

// parseMatchStmt handles `match expr { arm; arm; ... }`. Each arm is
// `pattern [if guard] => block-or-single-statement`. Arm separator is
// NEWLINE — we lift parenDepth across the brace region so newlines inside
// pattern parens (e.g. multi-line tuple patterns) stay transparent, then
// drop back to NEWLINE-significant mode at the arm-separator level by
// peeking at the raw stream when we need to.
func (p *parser) parseMatchStmt() (Stmt, error) {
	kw := p.advance() // consume `match`
	subject, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(KindLBrace, "in match"); err != nil {
		return nil, err
	}

	stmt := &MatchStmt{Pos: kw.Pos, Subject: subject}
	for {
		// Skip blank lines between arms.
		p.skipNewlines()
		if p.peekRaw().Kind == KindRBrace {
			p.pos++ // consume `}`
			return stmt, nil
		}
		if p.peekRaw().Kind == KindEOF {
			return nil, &ParseError{
				Pos:        kw.Pos,
				Message:    "unterminated match (missing '}')",
				Incomplete: true,
			}
		}
		arm, err := p.parseMatchArm()
		if err != nil {
			return nil, err
		}
		stmt.Arms = append(stmt.Arms, arm)
		// Arm separator: NEWLINE or `}`. The `}` case loops back to detect
		// and consume above.
		switch p.peekRaw().Kind {
		case KindNewline:
			p.pos++
		case KindRBrace, KindEOF:
			// Loop iteration handles both cases.
		default:
			t := p.peekRaw()
			return nil, errorAtTok(t, "expected newline or '}' between match arms, got %s", t.Kind)
		}
	}
}

// parseMatchArm reads one arm: `pattern [if guard] => body`. If body is a
// brace block we consume it directly; if it's a single statement we wrap it
// in a one-element Block so downstream consumers always see a Block.
func (p *parser) parseMatchArm() (MatchArm, error) {
	armPos := p.peek().Pos
	pat, err := p.parsePattern()
	if err != nil {
		return MatchArm{}, err
	}
	var guard Expr
	if p.peek().Kind == KindIf {
		p.advance()
		g, err := p.parseExpr()
		if err != nil {
			return MatchArm{}, err
		}
		guard = g
	}
	if _, err := p.expect(KindFatArrow, "in match arm"); err != nil {
		return MatchArm{}, err
	}
	// Body shape: block or single statement.
	if p.peek().Kind == KindLBrace {
		body, err := p.parseBlock("match arm body")
		if err != nil {
			return MatchArm{}, err
		}
		return MatchArm{Pos: armPos, Pattern: pat, Guard: guard, Body: body}, nil
	}
	// Single-statement body. Build a synthetic one-element Block so callers
	// don't have to special-case the shape. The synthetic block carries the
	// single statement's own position so diagnostics stay precise.
	stmtPos := p.peek().Pos
	stmt, err := p.parseStatement()
	if err != nil {
		return MatchArm{}, err
	}
	body := &Block{Pos: stmtPos, Statements: []Stmt{stmt}}
	return MatchArm{Pos: armPos, Pattern: pat, Guard: guard, Body: body}, nil
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

// parseForStmt handles all four for-loop shapes.
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
	// `IDENT in` head first. This is a LL(2) check, which the grammar can
	// absorb without a backtrack. Once we see `IDENT in`, the head expr is
	// either a range (`expr..expr` / `expr..=expr`) or a list-typed expr;
	// parseForInHeader picks the right shape after parsing the start expr.
	if p.peek().Kind == KindIdent {
		// Look two ahead via the raw tokens. We can't use peek alone because
		// peek is only one-token lookahead; we want to know if the next
		// non-newline token after the ident is `in`.
		if k := p.peekKindAfterIdent(); k == KindIn {
			return p.parseForInHeader(kw.Pos)
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

// parseForInHeader handles both v0.1's range form and v0.2's list-iter form:
//
//   - `for IDENT in EXPR..EXPR { ... }` / `for IDENT in EXPR..=EXPR { ... }`
//     — range iteration. RangeExprs are produced ONLY here — the general
//     expression parser refuses to construct them.
//   - `for IDENT in EXPR { ... }` — iterate over a list-typed expression,
//     binding IDENT to a deep copy of each element on each iteration.
//
// We parse the head expression with parseOr (not parseExpr) so the
// trailing-range guard inside parseExpr doesn't fire on the `..` / `..=` we
// might be about to consume. After the start expression we peek: if `..` or
// `..=` follows we're in the range form; otherwise we treat the expression
// as the iterable. typeck owns the "iterable must be a list" rule — the
// parser stays liberal so future iterables (str, etc.) need only a typeck
// extension.
func (p *parser) parseForInHeader(forPos Position) (Stmt, error) {
	nameTok, _ := p.expect(KindIdent, "in 'for' header") // already known ident
	if _, err := p.expect(KindIn, "in 'for' header"); err != nil {
		return nil, err
	}
	// parseOr (not parseExpr) so the trailing-range guard in parseExpr
	// doesn't fire on the `..` / `..=` we may be about to consume.
	headExpr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	switch p.peek().Kind {
	case KindRange, KindRangeEq:
		rangeTok := p.advance()
		inclusive := rangeTok.Kind == KindRangeEq
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
				Start:     headExpr,
				End:       end,
				Inclusive: inclusive,
			},
			Body: body,
		}, nil
	}
	// No range follows: list-iter form.
	body, err := p.parseBlock("'for' body")
	if err != nil {
		return nil, err
	}
	return &ForStmt{
		Pos:    forPos,
		Kind:   ForIter,
		Var:    nameTok.Value,
		VarPos: nameTok.Pos,
		Iter:   headExpr,
		Body:   body,
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
		// v0.3 admits two LHS shapes: a bare identifier (the v0.1 form) and a
		// single-level list index `xs[i]` (the v0.3 list-mutation surface).
		// Compound operators stay identifier-only at v0.3 — `xs[i] += 1` is
		// sugar that belongs to a later unit and is rejected here so the user
		// sees a focused diagnostic instead of an opaque downstream type
		// error.
		switch lhs := expr.(type) {
		case *IdentExpr:
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			return &AssignStmt{
				Pos:    lhs.Pos,
				Target: lhs,
				Op:     op,
				Value:  val,
			}, nil
		case *IndexExpr:
			if op != AssignSet {
				return nil, errorAt(opTok.Pos, "compound assignment '%s' to a list element is not supported at v0.3 — use `xs[i] = ...` instead", op)
			}
			// At v0.3 we admit only single-level indexing (`xs[i] = v`).
			// Chained indexing (`xs[i][j] = v`) parses fine as a postfix
			// chain, but the borrow / mutation rules for nested mutation
			// aren't in scope yet, so reject early with a precise message.
			if _, nested := lhs.Receiver.(*IndexExpr); nested {
				return nil, errorAt(opTok.Pos, "left-hand side of assignment must be an identifier or list[i] (chained indexing is not supported at v0.3)")
			}
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			return &AssignStmt{
				Pos:    lhs.Pos,
				Target: lhs,
				Op:     op,
				Value:  val,
			}, nil
		default:
			return nil, errorAt(opTok.Pos, "left-hand side of assignment must be an identifier or list[i]")
		}
	}

	// Plain expression statement: only a CallExpr is meaningful at v0.1.
	// v0.4 broadens this to also accept a MethodCallExpr so `c.method()` is
	// a valid statement (it has side effects via the impl method body).
	switch expr.(type) {
	case *CallExpr, *MethodCallExpr:
		return &ExprStmt{Pos: startTok.Pos, Expr: expr}, nil
	}
	return nil, errorAt(startTok.Pos, "expression statements must be function calls at v0.1")
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
			// Flagged Incomplete: the user has opened a block but not
			// closed it; the REPL keeps reading until `}` arrives.
			return nil, &ParseError{
				Pos:        open.Pos,
				Message:    "unterminated block (missing '}')",
				Incomplete: true,
			}
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

// Level 12: postfix call / index / slice / field access (left-assoc). The
// chain is built left-to-right so `xs[0].field(arg)` walks
// CallExpr ← FieldAccessExpr ← IndexExpr ← Ident.
func (p *parser) parsePostfix() (Expr, error) {
	expr, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek().Kind {
		case KindLParen:
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
		case KindLBracket:
			next, err := p.parseIndexOrSlice(expr)
			if err != nil {
				return nil, err
			}
			expr = next
		case KindDot:
			dotTok := p.advance()
			nameTok, err := p.expect(KindIdent, "after '.'")
			if err != nil {
				return nil, err
			}
			// v0.4: `expr DOT IDENT (` is a method call; otherwise the form
			// stays a field access (the v0.3 shape). The disambiguation is
			// purely additive — every existing field-access test continues to
			// match the no-paren branch.
			if p.peek().Kind == KindLParen {
				if _, err := p.expectParen(KindLParen, "in method call"); err != nil {
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
				if _, err := p.expectParen(KindRParen, "in method call"); err != nil {
					return nil, err
				}
				expr = &MethodCallExpr{
					Pos:       dotTok.Pos,
					Receiver:  expr,
					Method:    nameTok.Value,
					MethodPos: nameTok.Pos,
					Args:      args,
				}
				continue
			}
			expr = &FieldAccessExpr{
				Pos:       dotTok.Pos,
				Receiver:  expr,
				FieldName: nameTok.Value,
				NamePos:   nameTok.Pos,
			}
		default:
			return expr, nil
		}
	}
}

// parseIndexOrSlice handles `[ ... ]` after a postfix receiver. Inside the
// brackets `..` and `..=` are admitted (and ONLY here at v0.2 outside the
// for-in head), giving slicing its own context-sensitive corner of the
// grammar without loosening the global ban on free-standing ranges.
func (p *parser) parseIndexOrSlice(receiver Expr) (Expr, error) {
	open, err := p.expectParen(KindLBracket, "in index/slice")
	if err != nil {
		return nil, err
	}

	// `[..]`, `[..b]`, `[..=b]` — slice with no low bound.
	if k := p.peek().Kind; k == KindRange || k == KindRangeEq {
		rangeTok := p.advance()
		inclusive := rangeTok.Kind == KindRangeEq
		var high Expr
		if p.peek().Kind != KindRBracket {
			h, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			high = h
		} else if inclusive {
			// `[..=]` is meaningless — `..=` requires an upper bound.
			return nil, errorAt(rangeTok.Pos, "'..=' requires an upper bound in slice expression")
		}
		if _, err := p.expectParen(KindRBracket, "to close slice"); err != nil {
			return nil, err
		}
		return &SliceExpr{
			Pos:       open.Pos,
			Receiver:  receiver,
			Low:       nil,
			High:      high,
			Inclusive: inclusive,
		}, nil
	}

	// `[expr ...]`. We parse the first expression with parseOr (not parseExpr)
	// so the trailing-range guard inside parseExpr does not fire on the `..`
	// / `..=` we may be about to consume.
	first, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	switch p.peek().Kind {
	case KindRBracket:
		// Plain index. Use expectParen to keep parenDepth bookkeeping
		// symmetric with the opening `[`.
		if _, err := p.expectParen(KindRBracket, "to close index"); err != nil {
			return nil, err
		}
		return &IndexExpr{Pos: open.Pos, Receiver: receiver, Index: first}, nil
	case KindRange, KindRangeEq:
		rangeTok := p.advance()
		inclusive := rangeTok.Kind == KindRangeEq
		var high Expr
		if p.peek().Kind != KindRBracket {
			h, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			high = h
		} else if inclusive {
			return nil, errorAt(rangeTok.Pos, "'..=' requires an upper bound in slice expression")
		}
		if _, err := p.expectParen(KindRBracket, "to close slice"); err != nil {
			return nil, err
		}
		return &SliceExpr{
			Pos:       open.Pos,
			Receiver:  receiver,
			Low:       first,
			High:      high,
			Inclusive: inclusive,
		}, nil
	}
	t := p.peek()
	return nil, errorAtTok(t, "expected ']' or '..' in index/slice, got %s", t.Kind)
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
		// Struct-literal disambiguation (v0.2). `Ident '{' ...` is a struct
		// literal only when the inside is empty (`{}`) or starts with
		// `IDENT ':'`. Otherwise the `{` belongs to the surrounding
		// statement (an `if`/`for`/`match` body) and we leave it alone.
		if p.peek().Kind == KindLBrace && p.looksLikeStructLitBody() {
			return p.parseStructLitBody(t.Pos, t.Value)
		}
		return &IdentExpr{Pos: t.Pos, Name: t.Value}, nil
	case KindRune:
		// Token.Value is the codepoint as a decimal string; the lexer guards
		// it. We parse here so downstream consumers don't have to. Overflow
		// of an int64 from a 21-bit Unicode codepoint is impossible.
		p.advance()
		v, err := strconv.ParseInt(t.Value, 10, 64)
		if err != nil {
			return nil, errorAt(t.Pos, "invalid rune codepoint: %v", err)
		}
		return &RuneLit{Pos: t.Pos, Value: v}, nil
	case KindLParen:
		return p.parseTupleOrParen()
	case KindLBracket:
		return p.parseListLit()
	case KindThis:
		p.advance()
		return &ThisExpr{Pos: t.Pos}, nil
	case KindRange, KindRangeEq:
		return nil, errorAt(t.Pos, "range expressions are only allowed in for-in heads or slice brackets")
	case KindBang:
		return nil, errorAt(t.Pos, "use 'not' for boolean negation; '!' is reserved")
	}
	return nil, errorAtTok(t, "expected expression, got %s", t.Kind)
}

// parseTupleOrParen handles `(` after a postfix-position dispatch. We parse
// the first expression; if a comma follows we collect the rest as a tuple
// literal, otherwise we close as a ParenExpr. PLAN.md: 1-tuples are not
// admitted at v0.2, so a single `(e,)` form produces a parse error.
func (p *parser) parseTupleOrParen() (Expr, error) {
	open := p.peek()
	if _, err := p.expectParen(KindLParen, ""); err != nil {
		return nil, err
	}
	first, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind == KindComma {
		// Tuple literal: collect remaining elements (≥ 1 more makes ≥ 2 total).
		elements := []Expr{first}
		for p.peek().Kind == KindComma {
			p.advance()
			// Trailing comma allowed: `(a, b,)`.
			if p.peek().Kind == KindRParen {
				break
			}
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			elements = append(elements, e)
		}
		if _, err := p.expectParen(KindRParen, "to close tuple literal"); err != nil {
			return nil, err
		}
		// PLAN: at least 2 elements. The collection above guarantees ≥ 2 if
		// at least one extra element was parsed; the trailing-comma case
		// `(a,)` produces exactly 1 element.
		if len(elements) < 2 {
			return nil, errorAt(open.Pos, "tuple literal requires at least 2 elements (use parentheses for grouping)")
		}
		return &TupleLit{Pos: open.Pos, Elements: elements}, nil
	}
	if _, err := p.expectParen(KindRParen, "to close '('"); err != nil {
		return nil, err
	}
	return &ParenExpr{Pos: open.Pos, Inner: first}, nil
}

// parseListLit handles `[` in expression position. Empty list is allowed at
// the parser level; typeck rejects empty lists outside annotated contexts.
func (p *parser) parseListLit() (Expr, error) {
	open, err := p.expectParen(KindLBracket, "in list literal")
	if err != nil {
		return nil, err
	}
	var elements []Expr
	for {
		if p.peek().Kind == KindRBracket {
			break
		}
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		elements = append(elements, e)
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expectParen(KindRBracket, "to close list literal"); err != nil {
		return nil, err
	}
	return &ListLit{Pos: open.Pos, Elements: elements}, nil
}

// looksLikeStructLitBody peeks past the `{` (without consuming) to decide
// whether `Ident { ... }` is a struct literal or a brace block (i.e. the
// caller's statement body). Rule: empty `{}` OR opens with `IDENT ':'`
// where `:` is not `:=` (a walrus would mean the inside is a let-decl,
// which structurally cannot happen in expression position but happens
// inside an `if x { let y := ... }` body).
//
// The function inspects raw token positions and never advances. We treat
// NEWLINEs as transparent inside the brace because struct literals can
// legitimately span lines.
func (p *parser) looksLikeStructLitBody() bool {
	// p.pos currently sits on `{`. Walk forward.
	i := p.pos + 1
	skipNL := func() {
		for i < len(p.tokens) && p.tokens[i].Kind == KindNewline {
			i++
		}
	}
	skipNL()
	if i >= len(p.tokens) {
		return false
	}
	if p.tokens[i].Kind == KindRBrace {
		return true // empty struct literal `Name {}`
	}
	if p.tokens[i].Kind != KindIdent {
		return false
	}
	i++
	skipNL()
	if i >= len(p.tokens) {
		return false
	}
	// `:` (struct field) yes; `:=` (walrus) no.
	return p.tokens[i].Kind == KindColon
}

// parseStructLitBody consumes the `{ field: value, ... }` tail of a struct
// literal. The opening `{` is at p.peek(); typeName is the already-consumed
// type identifier.
func (p *parser) parseStructLitBody(headPos Position, typeName string) (Expr, error) {
	open, err := p.expect(KindLBrace, "in struct literal")
	if err != nil {
		return nil, err
	}
	// Brace region behaves like a paren region for NEWLINE skipping.
	p.parenDepth++
	defer func() { p.parenDepth-- }()
	_ = open

	var fields []FieldInit
	for {
		if p.peek().Kind == KindRBrace {
			break
		}
		nameT, err := p.expect(KindIdent, "in struct literal field list")
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(KindColon, "after struct literal field name"); err != nil {
			return nil, err
		}
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		fields = append(fields, FieldInit{
			Name:  nameT.Value,
			Value: val,
			Pos:   nameT.Pos,
		})
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expect(KindRBrace, "to close struct literal"); err != nil {
		return nil, err
	}
	return &StructLit{Pos: headPos, TypeName: typeName, Fields: fields}, nil
}

// ---------------------------------------------------------------------------
// Match patterns.
// ---------------------------------------------------------------------------

// parsePattern is the entry point for the pattern grammar. Top-level
// dispatch is by the first token: `_` is wildcard, `(` opens a tuple
// pattern, an IDENT is followed by lookahead to disambiguate Bind vs
// Struct vs Enum, and any literal kind starts a literal pattern.
//
// NEWLINE significance inside patterns is handled the same way as inside
// expressions: parens/brackets bump parenDepth and silence NEWLINEs, and
// outside those the pattern is a single line by construction (the arm
// parser does not intentionally span newlines on a flat pattern).
func (p *parser) parsePattern() (Pattern, error) {
	t := p.peek()
	switch t.Kind {
	case KindIdent:
		if t.Value == "_" {
			p.advance()
			return &WildcardPat{Pos: t.Pos}, nil
		}
		// IDENT alone, IDENT '{' — struct, IDENT '.' IDENT — enum, otherwise bind.
		p.advance()
		switch p.peek().Kind {
		case KindLBrace:
			return p.parseStructPatBody(t.Pos, t.Value)
		case KindDot:
			p.advance()
			vT, err := p.expect(KindIdent, "in enum pattern after '.'")
			if err != nil {
				return nil, err
			}
			ep := &EnumPat{Pos: t.Pos, TypeName: t.Value, VariantName: vT.Value}
			// v0.4 (Unit 2): optional payload destructure
			// `Token.Ident(name)`, `Token.Number(0, _)`. Empty parens are
			// rejected — bare variants drop the parentheses entirely.
			if p.peek().Kind == KindLParen {
				payload, err := p.parseVariantPayloadPatterns()
				if err != nil {
					return nil, err
				}
				ep.Payload = payload
			}
			return ep, nil
		}
		return &BindPat{Pos: t.Pos, Name: t.Value}, nil
	case KindLParen:
		return p.parseTuplePat()
	case KindMinus:
		// Optional unary `-` for numeric literal patterns.
		minus := p.advance()
		next := p.peek()
		if next.Kind != KindInt && next.Kind != KindFloat {
			return nil, errorAt(minus.Pos, "unary '-' in a pattern must precede a numeric literal, got %s", next.Kind)
		}
		litExpr, err := p.parseAtom()
		if err != nil {
			return nil, err
		}
		return &LitPat{
			Pos: minus.Pos,
			Lit: &UnaryExpr{Pos: minus.Pos, Op: UnaryNeg, Operand: litExpr},
		}, nil
	case KindInt, KindFloat, KindString, KindTrue, KindFalse, KindRune:
		// Literal patterns. We re-use parseAtom to read the literal so we
		// pick up the same Token.Value parsing as expressions do.
		litExpr, err := p.parseAtom()
		if err != nil {
			return nil, err
		}
		return &LitPat{Pos: t.Pos, Lit: litExpr}, nil
	}
	return nil, errorAtTok(t, "expected pattern, got %s", t.Kind)
}

// parseTuplePat handles `( pat, pat, ... )`. PLAN.md: ≥ 2 elements are
// required; a single-paren pattern is a parse error rather than silent
// grouping (patterns don't have a grouping operator at v0.2).
func (p *parser) parseTuplePat() (Pattern, error) {
	open, err := p.expectParen(KindLParen, "in tuple pattern")
	if err != nil {
		return nil, err
	}
	var elements []Pattern
	for {
		if p.peek().Kind == KindRParen {
			break
		}
		pat, err := p.parsePattern()
		if err != nil {
			return nil, err
		}
		elements = append(elements, pat)
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expectParen(KindRParen, "to close tuple pattern"); err != nil {
		return nil, err
	}
	if len(elements) < 2 {
		return nil, errorAt(open.Pos, "tuple pattern requires at least 2 elements")
	}
	return &TuplePat{Pos: open.Pos, Elements: elements}, nil
}

// parseVariantPayloadPatterns consumes the `( pat ( ',' pat )* )` tail of an
// enum variant pattern. The opening `(` has been peeked but not yet consumed.
//
// Empty parens (`Token.Ident()`) are rejected: bare variants drop the
// parentheses, mirroring the variant-decl rule. A trailing comma is also
// rejected — payload pattern lists carry no trailing comma.
func (p *parser) parseVariantPayloadPatterns() ([]Pattern, error) {
	openTok, err := p.expectParen(KindLParen, "in enum variant pattern")
	if err != nil {
		return nil, err
	}
	if p.peek().Kind == KindRParen {
		return nil, errorAt(openTok.Pos,
			"empty parentheses are not allowed; use the bare variant name")
	}
	var pats []Pattern
	for {
		pat, err := p.parsePattern()
		if err != nil {
			return nil, err
		}
		pats = append(pats, pat)
		if p.peek().Kind == KindComma {
			commaTok := p.advance()
			if p.peek().Kind == KindRParen {
				return nil, errorAt(commaTok.Pos,
					"trailing comma not allowed in enum variant pattern")
			}
			continue
		}
		break
	}
	if _, err := p.expectParen(KindRParen, "to close enum variant pattern"); err != nil {
		return nil, err
	}
	return pats, nil
}

// parseStructPatBody consumes the `{ field: pat, field, ..., .. }` tail of
// a struct pattern. The opening `{` has been peeked but not consumed; the
// type name has already been parsed.
//
// Shorthand: `Point { x, y }` desugars at parse time to
// `Point { x: BindPat{x}, y: BindPat{y} }` so downstream consumers walk a
// uniform shape. PLAN.md §10 (acted-upon) pins this behaviour.
func (p *parser) parseStructPatBody(headPos Position, typeName string) (Pattern, error) {
	if _, err := p.expect(KindLBrace, "in struct pattern"); err != nil {
		return nil, err
	}
	p.parenDepth++
	defer func() { p.parenDepth-- }()

	var fields []StructPatField
	rest := false
	for {
		if p.peek().Kind == KindRBrace {
			break
		}
		// `..` rest marker. Must be the last token before `}`.
		if p.peek().Kind == KindRange {
			rest = true
			p.advance()
			break
		}
		nameT, err := p.expect(KindIdent, "in struct pattern field list")
		if err != nil {
			return nil, err
		}
		var sub Pattern
		if p.peek().Kind == KindColon {
			p.advance()
			s, err := p.parsePattern()
			if err != nil {
				return nil, err
			}
			sub = s
		} else {
			// Shorthand: bind to a same-named local.
			sub = &BindPat{Pos: nameT.Pos, Name: nameT.Value}
		}
		fields = append(fields, StructPatField{
			Name:    nameT.Value,
			Pattern: sub,
			Pos:     nameT.Pos,
		})
		if p.peek().Kind == KindComma {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expect(KindRBrace, "to close struct pattern"); err != nil {
		return nil, err
	}
	return &StructPat{Pos: headPos, TypeName: typeName, Fields: fields, Rest: rest}, nil
}
