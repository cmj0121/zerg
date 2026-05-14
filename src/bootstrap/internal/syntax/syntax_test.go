package syntax

import (
	"strings"
	"testing"
)

// Lock in the tenth-man-driven invariants for v0.0 syntax.

func TestLexShebangIsComment(t *testing.T) {
	src := []byte("#! /usr/bin/env zerg\nnop\n")
	tokens, err := Lex(src)
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	// Expect: NEWLINE (after shebang), nop, NEWLINE, EOF.
	// The shebang itself must NOT emit a special token kind — it's a comment.
	for _, tok := range tokens {
		if tok.Kind == KindIdent && strings.HasPrefix(tok.Value, "!") {
			t.Errorf("shebang leaked into IDENT stream: %+v", tok)
		}
	}
	// Find the nop token and assert it's on line 2, column 1.
	var nopTok *Token
	for i := range tokens {
		if tokens[i].Kind == KindNop {
			nopTok = &tokens[i]
			break
		}
	}
	if nopTok == nil {
		t.Fatal("did not find KindNop in token stream")
	}
	if nopTok.Pos.Line != 2 || nopTok.Pos.Column != 1 {
		t.Errorf("nop position = %s, want 2:1", nopTok.Pos)
	}
}

func TestLexInterpolationProducesStructuredTokens(t *testing.T) {
	// v0.16 promoted `{ident}` inside a string from a hard rejection to the
	// load-bearing interp feature. The lexer now emits the structured
	// Start / Lit / Var / Lit / End sequence around the surrounding `print`.
	src := []byte(`print "hi {name}"`)
	tokens, err := Lex(src)
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	wantKinds := []Kind{
		KindPrint,
		KindInterpStart,
		KindInterpLit,
		KindInterpVar,
		KindInterpLit,
		KindInterpEnd,
		KindEOF,
	}
	if len(tokens) != len(wantKinds) {
		t.Fatalf("got %d tokens, want %d: %#v", len(tokens), len(wantKinds), tokens)
	}
	for i, w := range wantKinds {
		if tokens[i].Kind != w {
			t.Errorf("token %d kind = %v, want %v", i, tokens[i].Kind, w)
		}
	}
	if tokens[2].Value != "hi " {
		t.Errorf("leading Lit value = %q, want %q", tokens[2].Value, "hi ")
	}
	if tokens[3].Value != "name" {
		t.Errorf("Var value = %q, want %q", tokens[3].Value, "name")
	}
}

func TestLexStringEscapes(t *testing.T) {
	src := []byte(`print "a\nb\tc\\d\"e"`)
	tokens, err := Lex(src)
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	var strTok *Token
	for i := range tokens {
		if tokens[i].Kind == KindString {
			strTok = &tokens[i]
			break
		}
	}
	if strTok == nil {
		t.Fatal("no KindString in token stream")
	}
	want := "a\nb\tc\\d\"e"
	if strTok.Value != want {
		t.Errorf("string value = %q, want %q", strTok.Value, want)
	}
}

func TestLexKeywords(t *testing.T) {
	tokens, err := Lex([]byte("nop\nprint\n"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if tokens[0].Kind != KindNop {
		t.Errorf("token 0 kind = %v, want KindNop", tokens[0].Kind)
	}
	if tokens[2].Kind != KindPrint {
		t.Errorf("token 2 kind = %v, want KindPrint", tokens[2].Kind)
	}
}

func TestParseEmptyProgram(t *testing.T) {
	// Comments-only and blank-line-only files must parse to an empty program.
	src := []byte("#! /usr/bin/env zerg\n# just a comment\n\n")
	tokens, err := Lex(src)
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	prog, err := Parse(tokens)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(prog.Statements) != 0 {
		t.Errorf("expected 0 statements, got %d", len(prog.Statements))
	}
}

func TestParseHelloExample(t *testing.T) {
	src := []byte(`#! /usr/bin/env zerg
# the simplest Zerg program
print "Hello, Zerg!"
`)
	tokens, err := Lex(src)
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	prog, err := Parse(tokens)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(prog.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(prog.Statements))
	}
	ps, ok := prog.Statements[0].(*PrintStmt)
	if !ok {
		t.Fatalf("statement 0 is %T, want *PrintStmt", prog.Statements[0])
	}
	lit, ok := ps.Expr.(*StringLit)
	if !ok {
		t.Fatalf("PrintStmt.Expr is %T, want *StringLit", ps.Expr)
	}
	if lit.Value != "Hello, Zerg!" {
		t.Errorf("print value = %q, want %q", lit.Value, "Hello, Zerg!")
	}
}

// v0.1 generalised PrintStmt to take any expression. `print foo` now parses
// as PrintStmt{Expr: IdentExpr{Name: "foo"}}; the type checker is the layer
// that later catches references to undefined names.
func TestParsePrintOfIdentifier(t *testing.T) {
	tokens, err := Lex([]byte("print foo\n"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	prog, err := Parse(tokens)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(prog.Statements) != 1 {
		t.Fatalf("got %d statements, want 1", len(prog.Statements))
	}
	ps, ok := prog.Statements[0].(*PrintStmt)
	if !ok {
		t.Fatalf("statement 0 is %T, want *PrintStmt", prog.Statements[0])
	}
	id, ok := ps.Expr.(*IdentExpr)
	if !ok {
		t.Fatalf("PrintStmt.Expr is %T, want *IdentExpr", ps.Expr)
	}
	if id.Name != "foo" {
		t.Errorf("ident name = %q, want %q", id.Name, "foo")
	}
}

// At v0.1 a bare identifier is still an error in statement position —
// expression statements are restricted to function calls.
func TestParseRejectsBareIdentifier(t *testing.T) {
	tokens, err := Lex([]byte("xyzzy\n"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	_, err = Parse(tokens)
	if err == nil {
		t.Fatal("expected parse error for bare identifier, got nil")
	}
}

// ---------------------------------------------------------------------------
// v0.1 lexer tests.
//
// These exercise the new token kinds added for v0.1 (procedural core). The
// parser hasn't been extended yet, so the tests here only assert lexer
// output: kinds, values, and where applicable, source positions. Existing
// v0.0 tests above MUST continue to pass unchanged.
// ---------------------------------------------------------------------------

// kindsOf collects token kinds from a Lex result, omitting the trailing EOF.
// It's a tiny convenience for "did the lexer split this exactly the way I
// expect?" tests.
func kindsOf(t *testing.T, src string) []Kind {
	t.Helper()
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex(%q): %v", src, err)
	}
	out := make([]Kind, 0, len(tokens))
	for _, tk := range tokens {
		if tk.Kind == KindEOF {
			break
		}
		out = append(out, tk.Kind)
	}
	return out
}

func TestLexAllKeywords(t *testing.T) {
	cases := []struct {
		src  string
		want Kind
	}{
		{"let", KindLet},
		{"mut", KindMut},
		{"const", KindConst},
		{"fn", KindFn},
		{"return", KindReturn},
		{"if", KindIf},
		{"elif", KindElif},
		{"else", KindElse},
		{"for", KindFor},
		{"while", KindWhile},
		{"loop", KindLoop},
		{"break", KindBreak},
		{"continue", KindContinue},
		{"in", KindIn},
		{"and", KindAnd},
		{"or", KindOr},
		{"not", KindNot},
		{"xor", KindXor},
		{"true", KindTrue},
		{"false", KindFalse},
	}
	for _, c := range cases {
		c := c
		t.Run(c.src, func(t *testing.T) {
			tokens, err := Lex([]byte(c.src))
			if err != nil {
				t.Fatalf("Lex: %v", err)
			}
			if len(tokens) != 2 {
				t.Fatalf("got %d tokens, want 2 (keyword + EOF)", len(tokens))
			}
			if tokens[0].Kind != c.want {
				t.Errorf("kind = %v, want %v", tokens[0].Kind, c.want)
			}
			if tokens[0].Value != c.src {
				t.Errorf("value = %q, want %q", tokens[0].Value, c.src)
			}
		})
	}
}

func TestLexIdentifierIsNotKeyword(t *testing.T) {
	// A name that contains a keyword as a prefix must still be an identifier.
	tokens, err := Lex([]byte("letter mute lett continue_x"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	for i, want := range []string{"letter", "mute", "lett", "continue_x"} {
		if tokens[i].Kind != KindIdent {
			t.Errorf("token %d kind = %v, want KindIdent", i, tokens[i].Kind)
		}
		if tokens[i].Value != want {
			t.Errorf("token %d value = %q, want %q", i, tokens[i].Value, want)
		}
	}
}

func TestLexIntegerLiterals(t *testing.T) {
	cases := []struct {
		src       string
		wantValue string
	}{
		{"0", "0"},
		{"42", "42"},
		{"1_000", "1000"},
		{"1_000_000", "1000000"},
		{"0x0", "0x0"},
		{"0xff", "0xff"},
		{"0xFF", "0xFF"},
		{"0xDEAD_BEEF", "0xDEADBEEF"},
		{"0X10", "0X10"},
		{"0b0", "0b0"},
		{"0b1010", "0b1010"},
		{"0b1010_1010", "0b10101010"},
		{"0B1", "0B1"},
		{"0o0", "0o0"},
		{"0o755", "0o755"},
		{"0o7_5_5", "0o755"},
		{"0O17", "0O17"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.src, func(t *testing.T) {
			tokens, err := Lex([]byte(c.src))
			if err != nil {
				t.Fatalf("Lex(%q): %v", c.src, err)
			}
			if len(tokens) != 2 {
				t.Fatalf("token count = %d, want 2", len(tokens))
			}
			if tokens[0].Kind != KindInt {
				t.Errorf("kind = %v, want KindInt", tokens[0].Kind)
			}
			if tokens[0].Value != c.wantValue {
				t.Errorf("value = %q, want %q", tokens[0].Value, c.wantValue)
			}
		})
	}
}

func TestLexIntegerRejectsBadUnderscore(t *testing.T) {
	cases := []string{
		"0x_",       // empty digit run after prefix
		"0x_ff",     // leading separator after prefix
		"0b__1",     // doubled separator
		"0o7_",      // trailing separator
		"1_",        // trailing separator on decimal
		"1__0",      // doubled separator on decimal
		"0x",        // no digits at all
		"0b",
		"0o",
		"0xG",       // invalid hex digit; readDigitRun returns no-digit error
	}
	for _, src := range cases {
		src := src
		t.Run(src, func(t *testing.T) {
			_, err := Lex([]byte(src))
			if err == nil {
				t.Fatalf("expected lex error for %q, got nil", src)
			}
			if _, ok := err.(*LexError); !ok {
				t.Errorf("error is %T, want *LexError: %v", err, err)
			}
		})
	}
}

func TestLexFloatLiterals(t *testing.T) {
	cases := []struct {
		src       string
		wantValue string
	}{
		{"0.0", "0.0"},
		{"3.14", "3.14"},
		{"100.001", "100.001"},
		{"1_0.0_1", "10.01"},
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
			if tokens[0].Kind != KindFloat {
				t.Errorf("kind = %v, want KindFloat", tokens[0].Kind)
			}
			if tokens[0].Value != c.wantValue {
				t.Errorf("value = %q, want %q", tokens[0].Value, c.wantValue)
			}
		})
	}
}

func TestLexFloatTrailingDotIsError(t *testing.T) {
	// `1.` — digit-dot-non-digit — should be a lex error (we don't admit
	// trailing-dot floats at v0.1).
	_, err := Lex([]byte("1."))
	if err == nil {
		t.Fatal("expected lex error for `1.`, got nil")
	}
	if _, ok := err.(*LexError); !ok {
		t.Errorf("error is %T, want *LexError", err)
	}
}

func TestLexLeadingDotIsNotFloat(t *testing.T) {
	// `.5` must lex as DOT followed by INT (no leading-dot float at v0.1).
	tokens, err := Lex([]byte(".5"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if tokens[0].Kind != KindDot {
		t.Errorf("token 0 kind = %v, want KindDot", tokens[0].Kind)
	}
	if tokens[1].Kind != KindInt || tokens[1].Value != "5" {
		t.Errorf("token 1 = %v %q, want KindInt \"5\"", tokens[1].Kind, tokens[1].Value)
	}
}

func TestLexIntegerFollowedByRange(t *testing.T) {
	// `0..n`: 0 INT, .. RANGE, n IDENT — the dot must not be eaten by the
	// integer scanner.
	got := kindsOf(t, "0..n")
	want := []Kind{KindInt, KindRange, KindIdent}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("token %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestLexIntegerFollowedByRangeEq(t *testing.T) {
	got := kindsOf(t, "0..=10")
	want := []Kind{KindInt, KindRangeEq, KindInt}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("token %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestLexLongestMatchOperators(t *testing.T) {
	// Each input should split into exactly one operator token.
	cases := []struct {
		src  string
		want Kind
	}{
		{"==", KindEq},
		{"!=", KindNE},
		{"<=", KindLE},
		{">=", KindGE},
		{"<<", KindShl},
		{">>", KindShr},
		{"<<=", KindShlEq},
		{">>=", KindShrEq},
		{"+=", KindPlusEq},
		{"-=", KindMinusEq},
		{"*=", KindStarEq},
		{"/=", KindSlashEq},
		{"%=", KindPctEq},
		{"&=", KindAmpEq},
		{"|=", KindPipeEq},
		{"^=", KindCaretEq},
		{":=", KindWalrus},
		{"->", KindArrow},
		{"..", KindRange},
		{"..=", KindRangeEq},
		{"//", KindFloorDiv},
	}
	for _, c := range cases {
		c := c
		t.Run(c.src, func(t *testing.T) {
			tokens, err := Lex([]byte(c.src))
			if err != nil {
				t.Fatalf("Lex(%q): %v", c.src, err)
			}
			// Expect exactly one token + EOF.
			if len(tokens) != 2 {
				t.Fatalf("token count = %d, want 2: %#v", len(tokens), tokens)
			}
			if tokens[0].Kind != c.want {
				t.Errorf("kind = %v, want %v", tokens[0].Kind, c.want)
			}
		})
	}
}

func TestLexAllSingleCharOperators(t *testing.T) {
	// Each kind must appear once when we feed in one-char operators
	// separated by spaces. Order in `src` matches `want`.
	src := "+ - * / % & | ^ ~ < > = ! ( ) { } [ ] : , ."
	want := []Kind{
		KindPlus, KindMinus, KindStar, KindSlash, KindPercent,
		KindAmp, KindPipe, KindCaret, KindTilde,
		KindLT, KindGT, KindAssign, KindBang,
		KindLParen, KindRParen, KindLBrace, KindRBrace,
		KindLBracket, KindRBracket,
		KindColon, KindComma, KindDot,
	}
	got := kindsOf(t, src)
	if len(got) != len(want) {
		t.Fatalf("got %d kinds, want %d: got=%v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("token %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestLexLongestMatchAmbiguity(t *testing.T) {
	// Without the longest-match rule, `<<=` would lex as `<` `<=` or
	// `<<` `=`, and `..=` would lex as `..` `=`. Cement that they don't.
	t.Run("<<=", func(t *testing.T) {
		got := kindsOf(t, "<<=")
		if len(got) != 1 || got[0] != KindShlEq {
			t.Errorf("got %v, want [KindShlEq]", got)
		}
	})
	t.Run("..=", func(t *testing.T) {
		got := kindsOf(t, "..=")
		if len(got) != 1 || got[0] != KindRangeEq {
			t.Errorf("got %v, want [KindRangeEq]", got)
		}
	})
	t.Run("==", func(t *testing.T) {
		got := kindsOf(t, "==")
		if len(got) != 1 || got[0] != KindEq {
			t.Errorf("got %v, want [KindEq]", got)
		}
	})
	t.Run(">>", func(t *testing.T) {
		got := kindsOf(t, ">>")
		if len(got) != 1 || got[0] != KindShr {
			t.Errorf("got %v, want [KindShr]", got)
		}
	})
	// `: =` (with whitespace) is two tokens, not WALRUS.
	t.Run(": =", func(t *testing.T) {
		got := kindsOf(t, ": =")
		if len(got) != 2 || got[0] != KindColon || got[1] != KindAssign {
			t.Errorf("got %v, want [KindColon KindAssign]", got)
		}
	})
}

func TestLexLetStatementShape(t *testing.T) {
	// Sanity: a representative v0.1 statement lexes to the expected kinds.
	got := kindsOf(t, "let x: int = 0xff_ff")
	want := []Kind{
		KindLet, KindIdent, KindColon, KindIdent, KindAssign, KindInt,
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

func TestLexFnSignatureShape(t *testing.T) {
	got := kindsOf(t, "fn add(a: int, b: int) -> int")
	want := []Kind{
		KindFn, KindIdent, KindLParen,
		KindIdent, KindColon, KindIdent, KindComma,
		KindIdent, KindColon, KindIdent,
		KindRParen, KindArrow, KindIdent,
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

func TestLexStringAdmitsInterpolationAtV016(t *testing.T) {
	// v0.16 promoted `{ident}` from a hard lex rejection to the load-bearing
	// interpolation feature. The lexer now emits the Start / Lit / Var / Lit /
	// End sequence; non-interpolated strings still single-token through
	// KindString (TestLexNonInterpolatedStringStillSingleToken covers that).
	tokens, err := Lex([]byte(`"hi {x}"`))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if tokens[0].Kind != KindInterpStart {
		t.Errorf("token 0 kind = %v, want KindInterpStart", tokens[0].Kind)
	}
}

func TestLexNewlineAlwaysEmitted(t *testing.T) {
	// Even with brackets open, the lexer still emits NEWLINE — line-joining
	// is a parser concern at v0.1 (we keep v0.0 behaviour for now).
	got := kindsOf(t, "(\n)\n")
	want := []Kind{KindLParen, KindNewline, KindRParen, KindNewline}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("token %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestLexKindStringHumanReadable(t *testing.T) {
	// Spot-check that the new kinds have non-default String() forms.
	cases := []struct {
		k    Kind
		want string
	}{
		{KindLet, "'let'"},
		{KindRangeEq, "'..='"},
		{KindShlEq, "'<<='"},
		{KindFloat, "float literal"},
		{KindInt, "integer literal"},
		{KindFloorDiv, "'//'"},
		{KindRune, "rune literal"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("Kind(%d).String() = %q, want %q", c.k, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// v0.2 lexer tests — rune (single-quoted character) literals.
//
// The lexer emits KindRune for every well-formed rune literal and stores the
// Unicode code-point as a decimal string in Token.Value. The byte-vs-rune
// classification is typeck's job (Unit 2), so the lexer never errors on
// non-ASCII; it only errors when the literal is empty, multi-rune, or
// malformed.
// ---------------------------------------------------------------------------

func TestLexRuneLiterals(t *testing.T) {
	cases := []struct {
		src  string
		want string // decimal codepoint
	}{
		{`'A'`, "65"},
		{`'z'`, "122"},
		{`'0'`, "48"},
		{`' '`, "32"},
		{`'\n'`, "10"},
		{`'\t'`, "9"},
		{`'\r'`, "13"},
		{`'\\'`, "92"},
		{`'\''`, "39"},
		{`'\"'`, "34"},
		{`'\0'`, "0"},
		{`'漢'`, "28450"},
		{`'😀'`, "128512"},
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
			if tokens[0].Kind != KindRune {
				t.Errorf("kind = %v, want KindRune", tokens[0].Kind)
			}
			if tokens[0].Value != c.want {
				t.Errorf("value = %q, want %q", tokens[0].Value, c.want)
			}
		})
	}
}

func TestLexRuneRejectsBadInput(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantMsg string
	}{
		{"empty", `''`, "empty rune literal"},
		{"two ascii", `'AB'`, "rune literal must contain exactly one character"},
		{"ascii then multibyte", `'A漢'`, "rune literal must contain exactly one character"},
		{"unterminated eof", `'A`, "unterminated rune literal"},
		{"unterminated newline", "'\n", "unterminated rune literal"},
		{"unknown escape", `'\q'`, "unknown escape"},
		{"unterminated escape", `'\`, "unterminated escape"},
		{"open quote only", `'`, "unterminated rune literal"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			_, err := Lex([]byte(c.src))
			if err == nil {
				t.Fatalf("expected lex error for %q, got nil", c.src)
			}
			le, ok := err.(*LexError)
			if !ok {
				t.Fatalf("error is %T, want *LexError: %v", err, err)
			}
			if !strings.Contains(le.Message, c.wantMsg) {
				t.Errorf("message = %q, want it to contain %q", le.Message, c.wantMsg)
			}
		})
	}
}

func TestLexRunePosition(t *testing.T) {
	// The Pos must point at the opening quote, mirroring how strings work.
	tokens, err := Lex([]byte("  'A'"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if tokens[0].Kind != KindRune {
		t.Fatalf("kind = %v, want KindRune", tokens[0].Kind)
	}
	if tokens[0].Pos.Line != 1 || tokens[0].Pos.Column != 3 {
		t.Errorf("pos = %s, want 1:3", tokens[0].Pos)
	}
}

func TestLexRuneInsideStatementShape(t *testing.T) {
	// Ensure rune tokens compose with surrounding tokens correctly:
	// `let c := 'A'` → LET IDENT WALRUS RUNE.
	got := kindsOf(t, "let c := 'A'")
	want := []Kind{KindLet, KindIdent, KindWalrus, KindRune}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("token %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestLexCommentWithSingleQuoteIsStillComment(t *testing.T) {
	// A stray `'` inside a `# …` comment must not start a rune literal —
	// comments still consume to end of line.
	tokens, err := Lex([]byte("nop # don't lex me as a rune\nnop\n"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	// Expect: nop NEWLINE nop NEWLINE EOF.
	wantKinds := []Kind{KindNop, KindNewline, KindNop, KindNewline, KindEOF}
	if len(tokens) != len(wantKinds) {
		t.Fatalf("got %d tokens (%v), want %d", len(tokens), tokens, len(wantKinds))
	}
	for i, want := range wantKinds {
		if tokens[i].Kind != want {
			t.Errorf("token %d = %v, want %v", i, tokens[i].Kind, want)
		}
	}
}
