package token

import (
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	cases := map[string]Kind{
		"fn": Fn, "match": Match, "return": Return, "nil": Nil,
		"foobar": Ident, "_": Ident, "x1": Ident,
	}
	for word, want := range cases {
		if got := Lookup(word); got != want {
			t.Errorf("Lookup(%q) = %v, want %v", word, got, want)
		}
	}
}

func TestKindString(t *testing.T) {
	cases := map[Kind]string{
		EOF: "EOF", Semi: ";", Ident: "IDENT", Int: "INT",
		Fn: "fn", FatArrow: "=>", Arrow: "->", Walrus: ":=", LBrace: "{",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int(k), got, want)
		}
	}
	if got := Kind(9999).String(); !strings.HasPrefix(got, "Kind(") {
		t.Errorf("unknown kind String() = %q, want Kind(...)", got)
	}
}

func TestEndsItem(t *testing.T) {
	for _, k := range []Kind{Ident, Int, Float, Str, RParen, RBrack, RBrace, Nil, True, Nop, Return} {
		if !k.EndsItem() {
			t.Errorf("%v.EndsItem() = false, want true", k)
		}
	}
	for _, k := range []Kind{Plus, Fn, Semi, LParen, Comma, Arrow, FatArrow} {
		if k.EndsItem() {
			t.Errorf("%v.EndsItem() = true, want false", k)
		}
	}
}

func TestTokenString(t *testing.T) {
	cases := []struct {
		tok  Token
		want string
	}{
		{Token{Kind: Int, Lexeme: "42"}, "INT(42)"},
		{Token{Kind: Ident, Lexeme: "x"}, "IDENT(x)"},
		{Token{Kind: Str, Str: "hi"}, `STR("hi")`},
		{Token{Kind: Semi}, ";"},
		{Token{Kind: EOF}, "EOF"},
		{Token{Kind: Plus}, "+"},
	}
	for _, c := range cases {
		if got := c.tok.String(); got != c.want {
			t.Errorf("Token{%v}.String() = %q, want %q", c.tok.Kind, got, c.want)
		}
	}
}

func TestPosString(t *testing.T) {
	if got := (Pos{Offset: 10, Line: 3, Col: 5}).String(); got != "3:5" {
		t.Errorf("Pos.String() = %q, want 3:5", got)
	}
}
