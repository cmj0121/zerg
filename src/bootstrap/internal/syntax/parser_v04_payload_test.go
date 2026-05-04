package syntax

import (
	"testing"
)

// ---------------------------------------------------------------------------
// v0.4 parser tests — Unit 2: enum payloads.
//
// Three related shape extensions:
//
//  1. Variant declaration with optional payload type list:
//     `enum Token { Eof, Ident(str), Number(int, int) }`.
//  2. Construction with payload values is left as a typeck-Unit-3 lowering
//     from MethodCallExpr / FieldAccessExpr — Unit 2 only stages the AST
//     node `EnumLit` and leaves the parser unchanged for construction.
//  3. Match patterns binding enum payload positions:
//     `match t { Token.Ident(name) => ..., Token.Number(0, _) => ... }`.
//
// Bare-variant shape (no parens) keeps working as a strict superset of v0.2.
// Empty parens (`Token.Eof()` decl, `Token.Ident()` pattern) are rejected;
// the user must drop the parentheses entirely for a bare variant.
//
// Typeck still rejects payloadful enums with a "v0.4 work in progress"
// diagnostic — Unit 3 lights up the type-resolution side.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Variant declarations.
// ---------------------------------------------------------------------------

// TestParseEnumDeclMixedBareAndPayload covers the canonical shape from the
// PLAN: a mix of bare and payloadful variants in one enum declaration.
func TestParseEnumDeclMixedBareAndPayload(t *testing.T) {
	prog := parseProgramSrc(t, "enum Token { Eof, Ident(str), Number(int, int) }\n")
	s := expectOne[*EnumDecl](t, prog)
	if s.Name != "Token" {
		t.Errorf("name = %q, want Token", s.Name)
	}
	if len(s.Variants) != 3 {
		t.Fatalf("got %d variants, want 3", len(s.Variants))
	}
	// Eof — bare.
	if got := s.Variants[0]; got.Name != "Eof" || len(got.Payload) != 0 {
		t.Errorf("variant[0] = %s payload=%v, want Eof []", got.Name, got.Payload)
	}
	// Ident(str) — single payload type.
	if got := s.Variants[1]; got.Name != "Ident" || len(got.Payload) != 1 {
		t.Fatalf("variant[1] = %s payload=%v, want Ident [str]", got.Name, got.Payload)
	}
	if pl := s.Variants[1].Payload[0]; pl == nil || pl.Kind != TypeRefNamed || pl.Name != "str" {
		t.Errorf("variant[1] payload[0] = %v, want TypeRefNamed{str}", pl)
	}
	// Number(int, int) — two payload positions.
	if got := s.Variants[2]; got.Name != "Number" || len(got.Payload) != 2 {
		t.Fatalf("variant[2] = %s payload=%v, want Number [int, int]", got.Name, got.Payload)
	}
	for i := 0; i < 2; i++ {
		pl := s.Variants[2].Payload[i]
		if pl == nil || pl.Kind != TypeRefNamed || pl.Name != "int" {
			t.Errorf("variant[2] payload[%d] = %v, want TypeRefNamed{int}", i, pl)
		}
	}
}

// TestParseEnumDeclAllBareVariantsRegression confirms the v0.2 bare-only
// shape still parses identically — Payload stays nil/empty for every
// variant. This guards the "Unit 2 is a strict superset of v0.2" claim.
func TestParseEnumDeclAllBareVariantsRegression(t *testing.T) {
	prog := parseProgramSrc(t, "enum Color { Red, Green, Blue }\n")
	s := expectOne[*EnumDecl](t, prog)
	if len(s.Variants) != 3 {
		t.Fatalf("got %d variants, want 3", len(s.Variants))
	}
	for i, v := range s.Variants {
		if len(v.Payload) != 0 {
			t.Errorf("variant[%d] (%s) has %d payload entries, want 0", i, v.Name, len(v.Payload))
		}
	}
}

// TestParseEnumDeclCompoundPayloadType covers a payload type that is itself
// compound (`list[int]`). The parser delegates to parseTypeRef so list /
// tuple compound types come along for free.
func TestParseEnumDeclCompoundPayloadType(t *testing.T) {
	prog := parseProgramSrc(t, "enum Wrapper { Items(list[int]) }\n")
	s := expectOne[*EnumDecl](t, prog)
	if len(s.Variants) != 1 || len(s.Variants[0].Payload) != 1 {
		t.Fatalf("variants = %+v", s.Variants)
	}
	pl := s.Variants[0].Payload[0]
	if pl == nil || pl.Kind != TypeRefList || pl.Element == nil || pl.Element.Name != "int" {
		t.Errorf("payload[0] = %v, want TypeRefList<int>", pl)
	}
}

// TestParseEnumDeclTrailingCommaAfterVariants — the v0.2 rule allowing a
// trailing comma after the LAST variant continues to work; payload types
// reject trailing commas inside the parens (covered separately).
func TestParseEnumDeclTrailingCommaAfterVariants(t *testing.T) {
	prog := parseProgramSrc(t, "enum E { A, B(int), }\n")
	s := expectOne[*EnumDecl](t, prog)
	if len(s.Variants) != 2 {
		t.Errorf("got %d variants, want 2", len(s.Variants))
	}
}

// ---------------------------------------------------------------------------
// Match patterns binding enum payloads.
// ---------------------------------------------------------------------------

// TestParseMatchEnumPayloadBind covers the canonical pattern shape: bare
// variant, single-bind payload, and multi-bind payload all in one match.
func TestParseMatchEnumPayloadBind(t *testing.T) {
	src := `match t {
Token.Eof => nop
Token.Ident(name) => print name
Token.Number(value, base) => print value
}
`
	prog := parseProgramSrc(t, src)
	m := expectOne[*MatchStmt](t, prog)
	if len(m.Arms) != 3 {
		t.Fatalf("got %d arms, want 3", len(m.Arms))
	}
	// arm 0 — bare.
	ep0, ok := m.Arms[0].Pattern.(*EnumPat)
	if !ok || ep0.VariantName != "Eof" || len(ep0.Payload) != 0 {
		t.Errorf("arm[0] = %#v, want Token.Eof bare", m.Arms[0].Pattern)
	}
	// arm 1 — single bind.
	ep1, ok := m.Arms[1].Pattern.(*EnumPat)
	if !ok || ep1.VariantName != "Ident" || len(ep1.Payload) != 1 {
		t.Fatalf("arm[1] = %#v, want Token.Ident(name)", m.Arms[1].Pattern)
	}
	bp, ok := ep1.Payload[0].(*BindPat)
	if !ok || bp.Name != "name" {
		t.Errorf("arm[1] payload[0] = %#v, want BindPat{name}", ep1.Payload[0])
	}
	// arm 2 — two binds.
	ep2, ok := m.Arms[2].Pattern.(*EnumPat)
	if !ok || ep2.VariantName != "Number" || len(ep2.Payload) != 2 {
		t.Fatalf("arm[2] = %#v, want Token.Number(value, base)", m.Arms[2].Pattern)
	}
	for i, want := range []string{"value", "base"} {
		bp, ok := ep2.Payload[i].(*BindPat)
		if !ok || bp.Name != want {
			t.Errorf("arm[2] payload[%d] = %#v, want BindPat{%s}", i, ep2.Payload[i], want)
		}
	}
}

// TestParseMatchEnumPayloadLiteralAndWildcard covers a literal pattern and a
// wildcard nested inside a payload. Payload positions accept the full
// pattern grammar — every existing pattern shape.
func TestParseMatchEnumPayloadLiteralAndWildcard(t *testing.T) {
	src := `match t {
Token.Number(0, _) => nop
_ => nop
}
`
	prog := parseProgramSrc(t, src)
	m := expectOne[*MatchStmt](t, prog)
	if len(m.Arms) != 2 {
		t.Fatalf("got %d arms, want 2", len(m.Arms))
	}
	ep, ok := m.Arms[0].Pattern.(*EnumPat)
	if !ok || ep.VariantName != "Number" || len(ep.Payload) != 2 {
		t.Fatalf("arm[0] = %#v, want Token.Number(0, _)", m.Arms[0].Pattern)
	}
	// position 0 — literal 0.
	lp, ok := ep.Payload[0].(*LitPat)
	if !ok {
		t.Fatalf("payload[0] = %T, want *LitPat", ep.Payload[0])
	}
	il, ok := lp.Lit.(*IntLit)
	if !ok || il.Text != "0" {
		t.Errorf("payload[0] literal = %#v, want IntLit{0}", lp.Lit)
	}
	// position 1 — wildcard.
	if _, ok := ep.Payload[1].(*WildcardPat); !ok {
		t.Errorf("payload[1] = %T, want *WildcardPat", ep.Payload[1])
	}
}

// TestParseMatchEnumPayloadWildcardOnly covers the all-wildcard payload
// case: discard the entire payload while still matching the variant.
func TestParseMatchEnumPayloadWildcardOnly(t *testing.T) {
	src := `match t {
Token.Ident(_) => nop
_ => nop
}
`
	prog := parseProgramSrc(t, src)
	m := expectOne[*MatchStmt](t, prog)
	ep, ok := m.Arms[0].Pattern.(*EnumPat)
	if !ok || ep.VariantName != "Ident" || len(ep.Payload) != 1 {
		t.Fatalf("arm[0] = %#v, want Token.Ident(_)", m.Arms[0].Pattern)
	}
	if _, ok := ep.Payload[0].(*WildcardPat); !ok {
		t.Errorf("payload[0] = %T, want *WildcardPat", ep.Payload[0])
	}
}

// TestParseMatchEnumBareVariantRegression confirms a payload-less enum
// pattern still parses identically — Payload is nil/empty.
func TestParseMatchEnumBareVariantRegression(t *testing.T) {
	src := `match c {
Color.Red => nop
_ => nop
}
`
	prog := parseProgramSrc(t, src)
	m := expectOne[*MatchStmt](t, prog)
	ep, ok := m.Arms[0].Pattern.(*EnumPat)
	if !ok || ep.TypeName != "Color" || ep.VariantName != "Red" {
		t.Fatalf("arm[0] = %#v, want Color.Red", m.Arms[0].Pattern)
	}
	if len(ep.Payload) != 0 {
		t.Errorf("payload = %v, want empty", ep.Payload)
	}
}

// ---------------------------------------------------------------------------
// Negative cases.
// ---------------------------------------------------------------------------

// TestParseEnumDeclEmptyPayloadParens — `V()` is rejected; bare variants
// must drop the parentheses entirely.
func TestParseEnumDeclEmptyPayloadParens(t *testing.T) {
	expectParseErr(t,
		"enum E { V() }\n",
		"empty parentheses are not allowed; use the bare variant name")
}

// TestParseEnumDeclTrailingCommaInPayload — payload type lists do not allow
// a trailing comma. (Variant lists DO allow a trailing comma; the rules are
// separate.)
func TestParseEnumDeclTrailingCommaInPayload(t *testing.T) {
	expectParseErr(t,
		"enum E { V(int,) }\n",
		"trailing comma not allowed in enum variant payload type list")
}

// TestParseEnumDeclMissingCommaBetweenTypes — `V(int int)` is missing the
// comma between the two type-refs. parseTypeRef reads `int` as the first
// type, then we look for `,` or `)` and find an unexpected IDENT.
func TestParseEnumDeclMissingCommaBetweenTypes(t *testing.T) {
	expectParseErr(t,
		"enum E { V(int int) }\n",
		"to close enum variant payload")
}

// TestParseEnumDeclEmptyPayloadInPattern — `Token.Ident()` in a match arm is
// rejected; a payload-less variant pattern drops the parentheses.
func TestParseEnumDeclEmptyPayloadInPattern(t *testing.T) {
	src := `match t {
Token.Ident() => nop
_ => nop
}
`
	expectParseErr(t, src,
		"empty parentheses are not allowed; use the bare variant name")
}

// TestParseMatchEnumPayloadTrailingComma — a trailing comma inside a
// pattern payload is rejected too, mirroring the variant-decl rule.
func TestParseMatchEnumPayloadTrailingComma(t *testing.T) {
	src := `match t {
Token.Number(a,) => nop
_ => nop
}
`
	expectParseErr(t, src,
		"trailing comma not allowed in enum variant pattern")
}
