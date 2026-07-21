package lexer

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// kinds lexes src and returns the token kinds, dropping the trailing EOF.
func kinds(t *testing.T, src string) []token.Kind {
	t.Helper()
	toks, diags := Tokenize(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics for %q: %v", src, diags)
	}
	var out []token.Kind
	for _, tok := range toks[:len(toks)-1] {
		out = append(out, tok.Kind)
	}
	return out
}

func eq(t *testing.T, src string, want ...token.Kind) {
	t.Helper()
	got := kinds(t, src)
	if len(got) != len(want) {
		t.Fatalf("%q: got %v, want %v", src, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%q: token %d got %v, want %v (full %v)", src, i, got[i], want[i], got)
		}
	}
}

func TestKeywordsAndIdents(t *testing.T) {
	eq(t, "fn main", token.Fn, token.Ident)
	eq(t, "return nop", token.Return, token.Nop)
	eq(t, "mut x", token.Mut, token.Ident)
	// `_` and prefix-looking identifiers are ordinary identifiers.
	eq(t, "_ radius back foo", token.Ident, token.Ident, token.Ident, token.Ident)
}

func TestIntLiterals(t *testing.T) {
	for _, src := range []string{"0", "42", "1_000_000", "0xDE_AD", "0o755", "0b1010"} {
		got := kinds(t, src)
		if len(got) != 1 || got[0] != token.Int {
			t.Fatalf("%q: got %v, want [INT]", src, got)
		}
	}
}

func TestFloatLiterals(t *testing.T) {
	for _, src := range []string{"3.14", "6.022e23", "1.0", "2e10", "1_0.5"} {
		got := kinds(t, src)
		if len(got) != 1 || got[0] != token.Float {
			t.Fatalf("%q: got %v, want [FLOAT]", src, got)
		}
	}
}

func TestDotIsNotFloat(t *testing.T) {
	// `a.0` is field/tuple access, not a float; `1..10` is a range.
	eq(t, "a.0", token.Ident, token.Dot, token.Int)
	eq(t, "1..10", token.Int, token.DotDot, token.Int)
	eq(t, "0..=9", token.Int, token.DotDotEq, token.Int)
}

func TestStringEscapes(t *testing.T) {
	toks, diags := Tokenize(`"a\tb\n\u{41}"`)
	if len(diags) != 0 {
		t.Fatalf("diags: %v", diags)
	}
	if toks[0].Kind != token.Str || toks[0].Str != "a\tb\nA" {
		t.Fatalf("got kind=%v str=%q", toks[0].Kind, toks[0].Str)
	}
}

func TestRawStringAndRuneAndByte(t *testing.T) {
	toks, diags := Tokenize(`r"C:\tmp" 'a' b'\x41'`)
	if len(diags) != 0 {
		t.Fatalf("diags: %v", diags)
	}
	if toks[0].Kind != token.RawStr || toks[0].Str != `C:\tmp` {
		t.Fatalf("raw: kind=%v str=%q", toks[0].Kind, toks[0].Str)
	}
	if toks[1].Kind != token.Rune || toks[1].Str != "a" {
		t.Fatalf("rune: kind=%v str=%q", toks[1].Kind, toks[1].Str)
	}
	if toks[2].Kind != token.Byte || toks[2].Str != "A" {
		t.Fatalf("byte: kind=%v str=%q", toks[2].Kind, toks[2].Str)
	}
}

func TestOperators(t *testing.T) {
	eq(t, "a +% b *% c", token.Ident, token.PlusMod, token.Ident, token.StarMod, token.Ident)
	eq(t, "x <= y >> z", token.Ident, token.Le, token.Ident, token.Shr, token.Ident)
	eq(t, "p ?? q ?. r", token.Ident, token.Coalesce, token.Ident, token.OptDot, token.Ident)
	eq(t, "a := b", token.Ident, token.Walrus, token.Ident)
	eq(t, "n: int = 0", token.Ident, token.Colon, token.Ident, token.Assign, token.Int)
	eq(t, "-> <-", token.Arrow, token.LArrow)
	eq(t, "a => b", token.Ident, token.FatArrow, token.Ident)
}

func TestASI(t *testing.T) {
	// a newline after a value-ending token inserts one Semi; blank lines collapse.
	eq(t, "a\nb", token.Ident, token.Semi, token.Ident)
	eq(t, "a\n\n\nb", token.Ident, token.Semi, token.Ident)
	// no insertion after an operator that cannot end an item.
	eq(t, "a +\nb", token.Ident, token.Plus, token.Ident)
	// no insertion inside an unclosed '(' or '[' ...
	eq(t, "(a\nb)", token.LParen, token.Ident, token.Ident, token.RParen)
	eq(t, "[a\nb]", token.LBrack, token.Ident, token.Ident, token.RBrack)
	// ... but a '{' block DOES separate statements at newlines.
	eq(t, "{a\nb}", token.LBrace, token.Ident, token.Semi, token.Ident, token.RBrace)
	// a literal ';' is a Semi too.
	eq(t, "a; b", token.Ident, token.Semi, token.Ident)
}

func TestComments(t *testing.T) {
	eq(t, "a # trailing\nb", token.Ident, token.Semi, token.Ident)
	eq(t, "# whole line\na", token.Ident)
	eq(t, "## doc comment\nfn", token.Fn)
}

func TestDiagnostics(t *testing.T) {
	cases := []string{
		`"unterminated`,
		"f\"unterminated", // f-string with no closing quote
		"`unterminated",   // command literal with no closing backtick
		`0xZZ`,
	}
	for _, src := range cases {
		_, diags := Tokenize(src)
		if len(diags) == 0 {
			t.Fatalf("%q: expected a diagnostic, got none", src)
		}
	}
}

// TestInterpTokenizes proves the group 3/5 interpolation surface now lexes
// cleanly into the structured token stream (DESIGN-1a §4).
func TestInterpTokenizes(t *testing.T) {
	eq(t, `f"a{x}b"`,
		token.FStrBegin, token.LBrace, token.Ident, token.RBrace, token.FStrText, token.FStrEnd)
	eq(t, `f"{x!r:>3}"`,
		token.FStrBegin, token.LBrace, token.Ident, token.Conv, token.FmtSpec, token.RBrace, token.FStrEnd)
	eq(t, "`ls -l`", token.Cmd)
	eq(t, "f`echo {x}`",
		token.FCmdBegin, token.LBrace, token.Ident, token.RBrace, token.FCmdEnd)
	eq(t, `#[derive(Eq)]`,
		token.Hash, token.Ident, token.LParen, token.Ident, token.RParen, token.RBrack)
}

func TestTripleString(t *testing.T) {
	toks, diags := Tokenize("\"\"\"line1\nline2\"\"\"")
	if len(diags) != 0 {
		t.Fatalf("diags: %v", diags)
	}
	if toks[0].Kind != token.Str || toks[0].Str != "line1\nline2" {
		t.Fatalf("triple string: kind=%v str=%q", toks[0].Kind, toks[0].Str)
	}
}

func TestMoreEscapes(t *testing.T) {
	toks, diags := Tokenize(`'\u{263A}' b'\x7F' "\r\0end"`)
	if len(diags) != 0 {
		t.Fatalf("diags: %v", diags)
	}
	if toks[0].Kind != token.Rune || toks[0].Str != "☺" {
		t.Fatalf("rune escape: %q", toks[0].Str)
	}
	if toks[1].Kind != token.Byte || toks[1].Str != "\x7f" {
		t.Fatalf("byte escape: %q", toks[1].Str)
	}
	if toks[2].Kind != token.Str || toks[2].Str != "\r\x00end" {
		t.Fatalf("str escapes: %q", toks[2].Str)
	}
}

func TestBadCharLiterals(t *testing.T) {
	for _, src := range []string{`'\q'`, `"\z"`, `''`, `b''`, `'ab'`, `@`, `'\u{}'`, `b'\xZZ'`} {
		if _, diags := Tokenize(src); len(diags) == 0 {
			t.Errorf("%q: expected a diagnostic", src)
		}
	}
}

func TestDiagnosticsMethod(t *testing.T) {
	l := New("@")
	l.Next()
	if len(l.Diagnostics()) == 0 {
		t.Fatalf("Diagnostics() should report the illegal character")
	}
}

func TestSpans(t *testing.T) {
	toks, _ := Tokenize("fn\n  x")
	// 'fn' cannot end an item, so no ';' is inserted; 'x' is on line 2, column 3.
	x := toks[1]
	if x.Kind != token.Ident || x.Span.Start.Line != 2 || x.Span.Start.Col != 3 {
		t.Fatalf("got %v at %s", x.Kind, x.Span.Start)
	}
}
