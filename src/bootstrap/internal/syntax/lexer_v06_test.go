package syntax

import (
	"testing"
)

// ---------------------------------------------------------------------------
// v0.6 Unit 1 — lexer tests for the new tokens.
// ---------------------------------------------------------------------------

func TestLexNilKeyword(t *testing.T) {
	tokens, err := Lex([]byte("nil"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if tokens[0].Kind != KindNil {
		t.Errorf("kind = %v, want KindNil", tokens[0].Kind)
	}
	if tokens[0].Value != "nil" {
		t.Errorf("value = %q, want nil", tokens[0].Value)
	}
}

func TestLexNilIsNotIdent(t *testing.T) {
	// `nilly` and `nil_x` are still identifiers — only the bare word `nil`
	// promotes to the keyword. Mirrors how the v0.1 keyword table works.
	tokens, err := Lex([]byte("nilly nil_x"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if tokens[0].Kind != KindIdent || tokens[0].Value != "nilly" {
		t.Errorf("token 0 = %v %q, want IDENT 'nilly'", tokens[0].Kind, tokens[0].Value)
	}
	if tokens[1].Kind != KindIdent || tokens[1].Value != "nil_x" {
		t.Errorf("token 1 = %v %q, want IDENT 'nil_x'", tokens[1].Kind, tokens[1].Value)
	}
}

func TestLexQuestionFamily(t *testing.T) {
	// Each input should split into exactly one token of the expected kind.
	cases := []struct {
		src  string
		want Kind
	}{
		{"?", KindQuestion},
		{"??", KindCoalesce},
		{"?.", KindSafeDot},
	}
	for _, c := range cases {
		c := c
		t.Run(c.src, func(t *testing.T) {
			tokens, err := Lex([]byte(c.src))
			if err != nil {
				t.Fatalf("Lex(%q): %v", c.src, err)
			}
			if len(tokens) != 2 {
				t.Fatalf("token count = %d, want 2: %#v", len(tokens), tokens)
			}
			if tokens[0].Kind != c.want {
				t.Errorf("kind = %v, want %v", tokens[0].Kind, c.want)
			}
		})
	}
}

func TestLexQuestionLongestMatch(t *testing.T) {
	// `???` should split as `??` then `?` — longest-match prefers `??`.
	got := kindsOf(t, "???")
	want := []Kind{KindCoalesce, KindQuestion}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("token %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestLexNullableTypeShape(t *testing.T) {
	// `let x: int? = nil` lexes to LET IDENT COLON IDENT QUESTION ASSIGN NIL.
	got := kindsOf(t, "let x: int? = nil")
	want := []Kind{
		KindLet, KindIdent, KindColon, KindIdent, KindQuestion, KindAssign, KindNil,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("token %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestLexSafeNavShape(t *testing.T) {
	// `obj?.field` lexes to IDENT SAFEDOT IDENT — the `?.` fuses.
	got := kindsOf(t, "obj?.field")
	want := []Kind{KindIdent, KindSafeDot, KindIdent}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("token %d = %v, want %v", i, got[i], want[i])
		}
	}
}
