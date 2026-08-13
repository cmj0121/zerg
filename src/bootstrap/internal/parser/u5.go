// U5 parser: the group 7 surface — type expressions in every type position, the
// type-introducing declarations (struct/enum/type/spec/impl/init), generics with
// spec bounds, and the '#[…]' decorator prefix. A declaration is a kind of
// statement (GRAMMAR group 1's decorated-decl), so parseDecl leads the top level;
// const-exprs parse as ordinary expressions (the const-foldability restriction is
// a 1b semantic rule) wrapped so their position is marked.
package parser

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// --- top-level declaration dispatch -------------------------------------------

// parseDecl parses one decorated declaration: an optional '#[…]' decorator run
// (no statement separator binds it to its target), then the declaration itself.
//
// Follow-up F1: a doc-comment (or any trivia) sitting BETWEEN the decorator
// prefix and the declaration keyword is the leading trivia of that keyword, not
// of the '#['. The file-level attach hoists only the trivia before the '#[', so
// this stashes the between-trivia on the declaration node; attach then prepends
// the pre-decorator trivia, and both survive the reprint.
func (p *parser) parseDecl() ast.Decl {
	decos := p.parseDecorators()
	between := p.lead() // trivia between the last decorator and the declaration keyword
	d := p.parseBareDecl()
	setDecorators(d, decos)
	if len(decos) > 0 && len(between) > 0 {
		if t, ok := d.(trivial); ok {
			t.SetLead(between)
		}
	}
	return d
}

// parseBareDecl dispatches on the declaration keyword, peeking past the leading
// 'pub'/'unsafe'/'mut' modifiers to find it.
func (p *parser) parseBareDecl() ast.Decl {
	switch p.declHeadKind() {
	case token.Struct:
		return p.parseStructDecl()
	case token.Enum:
		return p.parseEnumDecl()
	case token.Type:
		return p.parseTypeDecl()
	case token.Spec:
		return p.parseSpecDecl()
	case token.Impl:
		return p.parseImplDecl()
	case token.Init:
		return p.parseInitDecl()
	case token.Fn:
		return p.parseFuncDecl(true)
	default:
		// declHeadKind peeked past the leading pub/unsafe/mut modifiers to find a
		// non-declaration keyword; consume those modifiers so the diagnostic and
		// the recovery both land on the offending token (otherwise syncDecl would
		// stop on the still-current modifier and make no progress — an infinite
		// loop). See syncDecl's stop set.
		for p.at(token.Pub) || p.at(token.Unsafe) || p.at(token.Mut) {
			p.advance()
		}
		p.fail(p.cur().Span, "expected a declaration, found %s", describe(p.cur().Kind))
		return nil
	}
}

// declHeadKind reports the declaration keyword that leads the current token
// stream, skipping the 'pub'/'unsafe'/'mut' modifiers that may precede it.
func (p *parser) declHeadKind() token.Kind {
	for i := p.pos; i < len(p.toks); i++ {
		switch p.toks[i].Kind {
		case token.Pub, token.Unsafe, token.Mut:
			continue
		default:
			return p.toks[i].Kind
		}
	}
	return token.EOF
}

// setDecorators attaches a decorator run to whichever declaration it leads.
func setDecorators(d ast.Decl, decos []*ast.Decorator) {
	if len(decos) == 0 {
		return
	}
	switch n := d.(type) {
	case *ast.FuncDecl:
		n.Decorators = decos
	case *ast.StructDecl:
		n.Decorators = decos
	case *ast.EnumDecl:
		n.Decorators = decos
	case *ast.TypeDecl:
		n.Decorators = decos
	case *ast.SpecDecl:
		n.Decorators = decos
	case *ast.ImplDecl:
		n.Decorators = decos
	case *ast.InitDecl:
		n.Decorators = decos
	case *ast.UnsafeGroup:
		n.Decorators = decos
	}
}

// --- decorators ---------------------------------------------------------------

// parseDecorators consumes a run of '#[…]' decorators. A newline after a
// decorator's ']' is not a separator (GRAMMAR group 2), so any inserted ';' is
// skipped before the next decorator or the target declaration.
func (p *parser) parseDecorators() []*ast.Decorator {
	var out []*ast.Decorator
	for p.at(token.Hash) {
		out = append(out, p.parseDecorator())
		p.skipSemis()
	}
	return out
}

// parseDecorator parses one '#[ deco-item (',' deco-item)* ]'.
func (p *parser) parseDecorator() *ast.Decorator {
	h := p.expect(token.Hash) // '#['
	var items []*ast.DecoItem
	for {
		items = append(items, p.parseDecoItem())
		if !p.accept(token.Comma) {
			break
		}
	}
	rb := p.expect(token.RBrack)
	return spanned(&ast.Decorator{Items: items}, span(h.Span.Start, rb.Span.End))
}

// parseDecoItem parses one 'id' or 'id(arg, …)' decorator item; each argument is
// a type-name or a const-expr, kept uniformly as a const-expr.
func (p *parser) parseDecoItem() *ast.DecoItem {
	name := p.expect(token.Ident)
	it := &ast.DecoItem{Name: name.Lexeme}
	end := name.Span.End
	if p.at(token.LParen) {
		p.advance()
		p.withBraces(func() ast.Expr {
			for {
				it.Args = append(it.Args, p.parseConstExpr())
				if !p.accept(token.Comma) {
					break
				}
			}
			return nil
		})
		end = p.expect(token.RParen).Span.End
	}
	return spanned(it, span(name.Span.Start, end))
}

// --- declarations -------------------------------------------------------------

// preludeRole names what a prelude name is for, and returns "" for everything else.
// The prelude is bound in every module before a program is read
// (docs/runtime/package.md), so a declaration that takes one of its names leaves the
// form that desugars to it with no name to reach for.
//
// The set is what the toolchain BINDS — the scalars and list aliases a type position
// maps to a type, the container types whose '[...]' is the type's own, the Either
// carrier's spellings and constructors, and the two built-in specs. Names the prelude
// is specified to hold but nothing declares (Ord, Hash, Error, Iterator, Iterable,
// Ref, and set) are absent on purpose: a program's own 'spec Ord' is the only Ord
// there is. The shipping compiler states the same list in parser.zg's p_prelude_role.
func preludeRole(lx string) string {
	switch lx {
	case "int", "uint", "float", "bool", "str", "byte", "rune":
		return "a built-in scalar type"
	case "bytearray", "runearray":
		return "a built-in list alias"
	case "list", "map":
		return "a built-in container type"
	case "Either", "Result", "Err":
		return "an error-carrier type"
	case "Left", "Right":
		return "an error-carrier constructor"
	case "Eq", "Into":
		return "a built-in spec"
	}
	return ""
}

// preludeCalleeRole is preludeRole at the FUNCTION slot: the prelude names a CALL can
// spell. 'map' is the whole of the difference, and it is measured rather than argued.
// Declare 'fn N(zz: int) -> int' and call 'N(1)': for every other name in the set the
// declaration is unreachable, because the callee is read as the prelude's form before a
// user symbol is looked for — a conversion for the scalars and the list aliases, a
// construction for 'Either' / 'Result' / 'Err' / 'Eq' / 'Into', the carrier's own
// constructor for 'Left' / 'Right'. 'list' is reachable here but costs the seed the
// 'list[byte](s)' bridge. 'map' alone answers 2 in both compilers: 'map[...](...)' as a
// constructor is built by neither, so the name has no value form for a declaration to
// take, and 'fn map(xs, f)' is a function a real program wants.
//
// This is a NARROWER set than the type slots take, because a type slot's namespace is
// where every one of these names is bound. The same split the method rule draws.
func preludeCalleeRole(lx string) string {
	if lx == "map" {
		return ""
	}
	return preludeRole(lx)
}

// declName is expect(Ident) at a slot that DECLARES a top-level name. Only a
// declaration refuses a prelude name; a local binding that shadows one is a loud type
// error at its first use, while a top-level declaration removes the name from the
// whole program in silence.
// funcName is the 'fn' slot's half of that rule, and `free` is the whole of the
// question. A METHOD'S NAME IS NOT A TOP-LEVEL NAME: it lives in its type's namespace,
// where a call has already said which type it is on, so 'impl P { fn set(v: int) }'
// names nothing the prelude bound.
func (p *parser) funcName(free bool) token.Token {
	if !free {
		return p.expect(token.Ident)
	}
	return p.reservedName(preludeCalleeRole(p.cur().Lexeme), "a function")
}

func (p *parser) declName(what string) token.Token {
	return p.reservedName(preludeRole(p.cur().Lexeme), what)
}

func (p *parser) reservedName(role, what string) token.Token {
	if role != "" && p.at(token.Ident) {
		p.fail(p.cur().Span, "%q is a prelude name (%s) and cannot name %s", p.cur().Lexeme, role, what)
	}
	return p.expect(token.Ident)
}

// parseStructDecl parses 'pub? struct Name generics? { field-list }'.
func (p *parser) parseStructDecl() ast.Decl {
	start := p.cur().Span.Start
	pub := p.accept(token.Pub)
	p.expect(token.Struct)
	name := p.declName("a struct")
	generics := p.optGenerics()
	fields, end, endTrivia := p.parseFieldList()
	return spanned(&ast.StructDecl{
		Pub: pub, Name: name.Lexeme, Generics: generics, Fields: fields, End: endTrivia,
	}, span(start, end))
}

// parseEnumDecl parses 'pub? enum Name generics? { variant-list }'.
func (p *parser) parseEnumDecl() ast.Decl {
	start := p.cur().Span.Start
	pub := p.accept(token.Pub)
	p.expect(token.Enum)
	name := p.declName("an enum")
	generics := p.optGenerics()
	variants, end, endTrivia := p.parseVariantList()
	return spanned(&ast.EnumDecl{
		Pub: pub, Name: name.Lexeme, Generics: generics, Variants: variants, End: endTrivia,
	}, span(start, end))
}

// parseTypeDecl parses the strong typedef 'pub? type Name generics? = Type'.
func (p *parser) parseTypeDecl() ast.Decl {
	start := p.cur().Span.Start
	pub := p.accept(token.Pub)
	p.expect(token.Type)
	name := p.declName("a type alias")
	generics := p.optGenerics()
	p.expect(token.Assign)
	alias := p.parseType()
	return spanned(&ast.TypeDecl{
		Pub: pub, Name: name.Lexeme, Generics: generics, Alias: alias,
	}, span(start, alias.Span().End))
}

// parseSpecDecl parses 'pub? spec Name generics? (: super)? { spec-member* }'.
func (p *parser) parseSpecDecl() ast.Decl {
	start := p.cur().Span.Start
	pub := p.accept(token.Pub)
	p.expect(token.Spec)
	name := p.declName("a spec")
	generics := p.optGenerics()
	var super *ast.Bound
	if p.accept(token.Colon) {
		super = p.parseBound()
	}
	members, end, endTrivia := p.parseSpecMembers()
	return spanned(&ast.SpecDecl{
		Pub: pub, Name: name.Lexeme, Generics: generics, Super: super, Members: members, End: endTrivia,
	}, span(start, end))
}

// parseImplDecl parses a spec impl 'impl generics? Spec for Target { … }' or an
// inherent impl 'impl generics? Target { … }'.
func (p *parser) parseImplDecl() ast.Decl {
	start := p.expect(token.Impl).Span.Start
	generics := p.optGenerics()
	first := p.parseType()
	var spec, target ast.Type
	if p.accept(token.For) {
		spec = first
		target = p.parseType()
	} else {
		target = first
	}
	items, end, endTrivia := p.parseImplItems()
	return spanned(&ast.ImplDecl{
		Generics: generics, Spec: spec, Target: target, Items: items, End: endTrivia,
	}, span(start, end))
}

// parseInitDecl parses the lazy module-setup block 'init() { … }'.
func (p *parser) parseInitDecl() ast.Decl {
	start := p.expect(token.Init).Span.Start
	p.expect(token.LParen)
	p.expect(token.RParen)
	body := p.parseBlock()
	return spanned(&ast.InitDecl{Body: body}, span(start, body.Span().End))
}

// --- fields, variants, members ------------------------------------------------

// parseFieldList parses '{ field-list }', returning the fields, the closing '}'
// end position, and any dangling trivia before it. Fields are separated like
// statements (a newline or ';'), never by commas.
func (p *parser) parseFieldList() ([]*ast.StructField, token.Pos, []token.Trivia) {
	p.expect(token.LBrace)
	var fields []*ast.StructField
	for {
		p.skipSemis()
		if p.at(token.RBrace) || p.at(token.EOF) {
			break
		}
		lead := p.lead()
		f := p.parseField()
		f.SetLead(lead)
		f.SetTrail(p.trailBehind())
		fields = append(fields, f)
	}
	rb := p.expect(token.RBrace)
	return fields, rb.Span.End, rb.Leading
}

// parseField parses one 'pub? id: Type (= default)?'.
func (p *parser) parseField() *ast.StructField {
	start := p.cur().Span.Start
	pub := p.accept(token.Pub)
	name := p.expect(token.Ident)
	p.expect(token.Colon)
	typ := p.parseType()
	f := &ast.StructField{Pub: pub, Name: name.Lexeme, Type: typ}
	end := typ.Span().End
	if p.accept(token.Assign) {
		f.Default = p.parseExpr()
		end = f.Default.Span().End
	}
	f.SetSpan(span(start, end))
	return f
}

// parseVariantList parses '{ variant-list }' like parseFieldList.
func (p *parser) parseVariantList() ([]*ast.Variant, token.Pos, []token.Trivia) {
	p.expect(token.LBrace)
	var variants []*ast.Variant
	for {
		p.skipSemis()
		if p.at(token.RBrace) || p.at(token.EOF) {
			break
		}
		lead := p.lead()
		v := p.parseVariant()
		v.SetLead(lead)
		v.SetTrail(p.trailBehind())
		variants = append(variants, v)
	}
	rb := p.expect(token.RBrace)
	return variants, rb.Span.End, rb.Leading
}

// parseVariant parses one 'id (payload)? (= discriminant)?': a tuple-style
// payload or a C-style integer discriminant.
func (p *parser) parseVariant() *ast.Variant {
	name := p.expect(token.Ident)
	v := &ast.Variant{Name: name.Lexeme}
	end := name.Span.End
	if p.at(token.LParen) {
		p.advance()
		for {
			v.Payload = append(v.Payload, p.parseType())
			if !p.accept(token.Comma) {
				break
			}
		}
		end = p.expect(token.RParen).Span.End
	}
	if p.accept(token.Assign) {
		v.Discr = p.parseConstExpr()
		end = v.Discr.Span().End
	}
	v.SetSpan(span(name.Span.Start, end))
	return v
}

// parseSpecMembers parses '{ spec-member* }': a required signature, a provided
// method, an associated type, or an associated value, separated like statements.
func (p *parser) parseSpecMembers() ([]ast.SpecMember, token.Pos, []token.Trivia) {
	p.expect(token.LBrace)
	var members []ast.SpecMember
	for {
		p.skipSemis()
		if p.at(token.RBrace) || p.at(token.EOF) {
			break
		}
		lead := p.lead()
		m := p.parseSpecMember()
		if n, ok := m.(interface {
			SetLead([]token.Trivia)
			SetTrail([]token.Trivia)
		}); ok {
			n.SetLead(lead)
			n.SetTrail(p.trailBehind())
		}
		members = append(members, m)
	}
	rb := p.expect(token.RBrace)
	return members, rb.Span.End, rb.Leading
}

// parseSpecMember dispatches one spec member.
func (p *parser) parseSpecMember() ast.SpecMember {
	switch p.cur().Kind {
	case token.Type:
		return p.parseAssocType()
	case token.Unsafe, token.Mut, token.Fn:
		return p.parseFnMember()
	case token.Ident:
		return p.parseAssocVal()
	default:
		p.fail(p.cur().Span, "expected a spec member, found %s", describe(p.cur().Kind))
		return nil
	}
}

// parseFnMember parses a spec method: a provided method (a FuncDecl with a body)
// when a '{' follows the signature, or a required FnSig otherwise.
func (p *parser) parseFnMember() ast.SpecMember {
	start := p.cur().Span.Start
	unsafeTok := p.cur()
	unsafe := p.accept(token.Unsafe)
	mut := p.accept(token.Mut)
	p.expect(token.Fn)

	// GRAMMAR#fn-sig derives `'unsafe'? 'mut'? 'fn'`, so an `unsafe` required signature IS
	// a spec member — and this read it, recorded Unsafe on the FnSig, and then dropped the
	// requirement: an `impl` supplying a plain `fn peek()` for a spec asking `unsafe fn
	// peek()` was accepted and RAN, with the one thing the keyword says silently gone. The
	// impl side cannot spell `unsafe` at all (parseImplItem has no path for it), so a spec
	// that asks for one can never be satisfied honestly. It is refused after the `fn` is
	// consumed so that a `spec` body's `unsafe { … }` still gets expect's answer about the
	// missing `fn` rather than this one about a signature it never wrote.
	if unsafe {
		p.fail(unsafeTok.Span, "an `unsafe` spec signature is not yet supported by the bootstrap seed; the trust boundary `unsafe` marks is not enforced across an impl, so the requirement would be dropped")
		return nil
	}

	name := p.expect(token.Ident)
	generics := p.optGenerics()
	p.expect(token.LParen)
	params := p.parseParams()
	p.expect(token.RParen)
	var ret ast.Type
	if p.accept(token.Arrow) {
		ret = p.parseType()
	}
	if p.at(token.LBrace) {
		body := p.parseBlock()
		return spanned(&ast.FuncDecl{
			Unsafe: unsafe, Mut: mut, Name: name.Lexeme, NameEnd: name.Span.End,
			Generics: generics, Params: params, Ret: ret, Body: body,
		}, span(start, body.Span().End))
	}
	end := name.Span.End
	if ret != nil {
		end = ret.Span().End
	} else if len(params) > 0 {
		end = params[len(params)-1].Span().End
	}
	return spanned(&ast.FnSig{
		Unsafe: unsafe, Mut: mut, Name: name.Lexeme, Generics: generics, Params: params, Ret: ret,
	}, span(start, end))
}

// parseAssocType parses a spec's 'type Item (: bound)?'.
func (p *parser) parseAssocType() ast.SpecMember {
	start := p.expect(token.Type).Span.Start
	name := p.expect(token.Ident)
	at := &ast.AssocType{Name: name.Lexeme}
	end := name.Span.End
	if p.accept(token.Colon) {
		at.Bound = p.parseBound()
		end = at.Bound.Span().End
	}
	return spanned(at, span(start, end))
}

// parseAssocVal parses a spec's required associated value 'BITS: int'.
func (p *parser) parseAssocVal() ast.SpecMember {
	name := p.expect(token.Ident)
	p.expect(token.Colon)
	typ := p.parseType()
	return spanned(&ast.AssocVal{Name: name.Lexeme, Type: typ}, span(name.Span.Start, typ.Span().End))
}

// parseImplItems parses '{ impl-item* }': a method, an associated-type binding,
// or a value binding, separated like statements.
func (p *parser) parseImplItems() ([]ast.ImplItem, token.Pos, []token.Trivia) {
	p.expect(token.LBrace)
	var items []ast.ImplItem
	for {
		p.skipSemis()
		if p.at(token.RBrace) || p.at(token.EOF) {
			break
		}
		lead := p.lead()
		it := p.parseImplItem()
		if n, ok := it.(interface {
			SetLead([]token.Trivia)
			SetTrail([]token.Trivia)
		}); ok {
			n.SetLead(lead)
			n.SetTrail(p.trailBehind())
		}
		items = append(items, it)
	}
	rb := p.expect(token.RBrace)
	return items, rb.Span.End, rb.Leading
}

// parseImplItem dispatches one impl item.
func (p *parser) parseImplItem() ast.ImplItem {
	switch p.cur().Kind {
	case token.Type:
		return p.parseAssocBind()
	case token.Ident:
		return p.parseValBind()
	default: // pub / unsafe / mut / fn — a method or associated function
		return p.parseFuncDecl(false).(*ast.FuncDecl)
	}
}

// parseAssocBind parses an impl's 'type Item = Type'.
func (p *parser) parseAssocBind() ast.ImplItem {
	start := p.expect(token.Type).Span.Start
	name := p.expect(token.Ident)
	p.expect(token.Assign)
	typ := p.parseType()
	return spanned(&ast.AssocBind{Name: name.Lexeme, Type: typ}, span(start, typ.Span().End))
}

// parseValBind parses an impl's 'BITS := const-expr'.
func (p *parser) parseValBind() ast.ImplItem {
	name := p.expect(token.Ident)
	p.expect(token.Walrus)
	val := p.parseConstExpr()
	return spanned(&ast.ValBind{Name: name.Lexeme, Value: val}, span(name.Span.Start, val.Span().End))
}

// --- generics & bounds --------------------------------------------------------

// optGenerics parses a '[T: Bound, …]' generic parameter list when one leads.
func (p *parser) optGenerics() *ast.Generics {
	if p.at(token.LBrack) {
		return p.parseGenerics()
	}
	return nil
}

// parseGenerics parses '[' type-param (',' type-param)* ']'.
func (p *parser) parseGenerics() *ast.Generics {
	lb := p.expect(token.LBrack)
	var params []*ast.TypeParam
	for {
		start := p.cur().Span.Start
		name := p.expect(token.Ident)
		tp := &ast.TypeParam{Name: name.Lexeme}
		end := name.Span.End
		if p.accept(token.Colon) {
			tp.Bound = p.parseBound()
			end = tp.Bound.Span().End
		}
		tp.SetSpan(span(start, end))
		params = append(params, tp)
		if !p.accept(token.Comma) {
			break
		}
	}
	rb := p.expect(token.RBrack)
	return spanned(&ast.Generics{Params: params}, span(lb.Span.Start, rb.Span.End))
}

// parseBound parses a spec conjunction 'A + B' (a bound or a super-spec).
func (p *parser) parseBound() *ast.Bound {
	first := p.expect(token.Ident)
	b := &ast.Bound{Names: []string{first.Lexeme}}
	end := first.Span.End
	for p.accept(token.Plus) {
		n := p.expect(token.Ident)
		b.Names = append(b.Names, n.Lexeme)
		end = n.Span.End
	}
	return spanned(b, span(first.Span.Start, end))
}

// --- const-expr ---------------------------------------------------------------

// parseConstExpr parses an ordinary expression and wraps it as a const-expr; the
// compile-time-foldability restriction is a 1b semantic rule, not enforced here.
func (p *parser) parseConstExpr() *ast.ConstExpr {
	e := p.withBraces(p.parseExpr)
	return spanned(&ast.ConstExpr{X: e}, e.Span())
}

// --- type expressions ---------------------------------------------------------

// parseType parses 'base-type ?' — a base type with an optional trailing '?'
// making it nullable (GRAMMAR group 7).
func (p *parser) parseType() ast.Type {
	t := p.parseBaseType()
	if p.at(token.Question) {
		q := p.advance()
		return spanned(&ast.OptType{Elem: t}, span(t.Span().Start, q.Span.End))
	}
	return t
}

// parseBaseType parses one base-type: a named type (with type-args and
// projection), a tuple, an array, a channel, a function type, or a pointer.
func (p *parser) parseBaseType() ast.Type {
	switch p.cur().Kind {
	case token.LParen:
		return p.parseTupleType()
	case token.LBrack:
		return p.parseArrayType()
	case token.Chan:
		return p.parseChanType(p.cur().Span.Start, ast.ChanBidi)
	case token.LArrow:
		arrow := p.advance() // '<-' chan[T] — receive-only
		return p.parseChanType(arrow.Span.Start, ast.ChanRecv)
	case token.Fn:
		return p.parseFnType(p.cur().Span.Start, false)
	case token.Unsafe:
		start := p.advance().Span.Start
		return p.parseFnType(start, true)
	case token.Ptr:
		return p.parsePtrType()
	case token.Ident:
		return p.parseNamedType()
	default:
		p.fail(p.cur().Span, "expected a type, found %s", describe(p.cur().Kind))
		return nil
	}
}

// parseNamedType parses 'type-name type-args? ('.' id)*' — a named type with
// optional type arguments and an associated-type projection chain 'I.Item.Sub'.
func (p *parser) parseNamedType() ast.Type {
	name := p.expect(token.Ident)
	tr := &ast.TypeRef{Name: name.Lexeme}
	end := name.Span.End
	if p.at(token.LBrack) {
		tr.Args, end = p.parseTypeArgs()
	}
	for p.at(token.Dot) {
		p.advance()
		id := p.expect(token.Ident)
		tr.Proj = append(tr.Proj, id.Lexeme)
		end = id.Span.End
	}
	return spanned(tr, span(name.Span.Start, end))
}

// parseTypeArgs parses '[' generic-arg (',' generic-arg)* ']', returning the
// arguments and the closing ']' end position.
func (p *parser) parseTypeArgs() ([]ast.Type, token.Pos) {
	p.expect(token.LBrack)
	var args []ast.Type
	p.withBraces(func() ast.Expr {
		for {
			args = append(args, p.parseGenericArg())
			if !p.accept(token.Comma) {
				break
			}
		}
		return nil
	})
	rb := p.expect(token.RBrack)
	return args, rb.Span.End
}

// parseGenericArg parses one generic argument: a type, or a const-expr filling a
// value-generic parameter. Without name resolution (1b) the two share a surface,
// so a value that is clearly not a type (a literal or an operator expression) is
// taken as a const-expr, and a type that does not end cleanly at ',' or ']' is
// re-read as one.
func (p *parser) parseGenericArg() ast.Type {
	switch p.cur().Kind {
	case token.Int, token.Float, token.Str, token.RawStr, token.Rune, token.Byte,
		token.True, token.False, token.Nil, token.Minus, token.MinusMod, token.Tilde, token.Not:
		return p.constArg()
	}
	save := p.pos
	t := p.parseType()
	if p.at(token.Comma) || p.at(token.RBrack) {
		return t
	}
	p.pos = save // the "type" was really the head of a const-expr (e.g. 'N * 2')
	return p.constArg()
}

// constArg parses a const-expr in type-argument position (it satisfies Type).
func (p *parser) constArg() ast.Type { return p.parseConstExpr() }

// parseTupleType parses '(' type (',' type)+ ')' — a 2+-element tuple type.
func (p *parser) parseTupleType() ast.Type {
	lp := p.expect(token.LParen)
	var elems []ast.Type
	p.withBraces(func() ast.Expr {
		for {
			elems = append(elems, p.parseType())
			if !p.accept(token.Comma) {
				break
			}
		}
		return nil
	})
	rp := p.expect(token.RParen)
	if len(elems) < 2 {
		p.fail(span(lp.Span.Start, rp.Span.End), "a tuple type needs 2 or more elements")
	}
	return spanned(&ast.TupleType{Elems: elems}, span(lp.Span.Start, rp.Span.End))
}

// parseArrayType parses '[' type ';' const-expr ']' — a fixed-length array type.
func (p *parser) parseArrayType() ast.Type {
	lb := p.expect(token.LBrack)
	var elem ast.Type
	var count *ast.ConstExpr
	p.withBraces(func() ast.Expr {
		elem = p.parseType()
		p.expect(token.Semi)
		count = p.parseConstExpr()
		return nil
	})
	rb := p.expect(token.RBrack)
	return spanned(&ast.ArrayType{Elem: elem, Count: count}, span(lb.Span.Start, rb.Span.End))
}

// parseChanType parses the '[' type ']' body of a channel type. The '<-' of a
// receive-only channel is already consumed (dir); a trailing '<-' after ']'
// makes it send-only.
func (p *parser) parseChanType(start token.Pos, dir ast.ChanDir) ast.Type {
	p.expect(token.Chan)
	p.expect(token.LBrack)
	var elem ast.Type
	p.withBraces(func() ast.Expr {
		elem = p.parseType()
		return nil
	})
	rb := p.expect(token.RBrack)
	end := rb.Span.End
	if dir == ast.ChanBidi && p.at(token.LArrow) {
		dir = ast.ChanSend
		end = p.advance().Span.End
	}
	return spanned(&ast.ChanType{Elem: elem, Dir: dir}, span(start, end))
}

// parseFnType parses 'unsafe? fn(param-types) -> ret?' in type position; the
// leading 'unsafe' (when present) is already consumed.
func (p *parser) parseFnType(start token.Pos, unsafe bool) ast.Type {
	p.expect(token.Fn)
	p.expect(token.LParen)
	var params []ast.ParamType
	if !p.at(token.RParen) {
		p.withBraces(func() ast.Expr {
			for {
				ref := p.parseMutRef()
				t := p.parseType()
				params = append(params, ast.ParamType{Ref: ref, Type: t})
				if !p.accept(token.Comma) {
					break
				}
			}
			return nil
		})
	}
	end := p.expect(token.RParen).Span.End
	var ret ast.Type
	if p.accept(token.Arrow) {
		ret = p.parseType()
		end = ret.Span().End
	}
	return spanned(&ast.FnType{Unsafe: unsafe, Params: params, Ret: ret}, span(start, end))
}

// parsePtrType parses 'ptr' or 'ptr[T]' (GRAMMAR group 12).
func (p *parser) parsePtrType() ast.Type {
	kw := p.expect(token.Ptr)
	pt := &ast.PtrType{}
	end := kw.Span.End
	if p.at(token.LBrack) {
		p.advance()
		p.withBraces(func() ast.Expr {
			pt.Elem = p.parseType()
			return nil
		})
		end = p.expect(token.RBrack).Span.End
	}
	return spanned(pt, span(kw.Span.Start, end))
}
