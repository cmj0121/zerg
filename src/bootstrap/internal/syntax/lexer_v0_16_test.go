package syntax

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.16 lexer tests — bare-identifier string interpolation.
//
// Non-interpolated strings still emit a single KindString. Interpolated
// strings expand to a structured sequence: KindInterpStart, alternating
// KindInterpLit / KindInterpVar (with empty Lits sandwiching any Var that
// would otherwise open or close the string), KindInterpEnd. Escape forms
// `\{` and `\}` produce literal braces and do NOT open / close an interp
// slot. Unescaped `}` outside a slot is a lex error.
// ---------------------------------------------------------------------------

// --- back-compat: non-interpolated strings still single-token ---------------

func TestLexNonInterpolatedStringStillSingleToken(t *testing.T) {
	tokens, err := Lex([]byte(`"hello world"`))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if tokens[0].Kind != KindString {
		t.Fatalf("token 0 kind = %v, want KindString", tokens[0].Kind)
	}
	if tokens[0].Value != "hello world" {
		t.Errorf("token 0 value = %q, want %q", tokens[0].Value, "hello world")
	}
	for _, tk := range tokens {
		switch tk.Kind {
		case KindInterpStart, KindInterpLit, KindInterpVar, KindInterpEnd:
			t.Errorf("unexpected interp token %v in non-interpolated string", tk.Kind)
		}
	}
}

func TestLexEscapedBraceStillSingleToken(t *testing.T) {
	tokens, err := Lex([]byte(`"\{ literal \} braces"`))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if tokens[0].Kind != KindString {
		t.Fatalf("token 0 kind = %v, want KindString", tokens[0].Kind)
	}
	if got, want := tokens[0].Value, "{ literal } braces"; got != want {
		t.Errorf("value = %q, want %q", got, want)
	}
}

// --- structured emission ----------------------------------------------------

func TestLexInterpolatedStringEmitsStructuredTokens(t *testing.T) {
	tokens, err := Lex([]byte(`"hi {name}, n is {n}"`))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	// Expect: Start, Lit("hi "), Var("name"), Lit(", n is "), Var("n"), Lit(""), End, EOF.
	want := []struct {
		kind  Kind
		value string
	}{
		{KindInterpStart, ""},
		{KindInterpLit, "hi "},
		{KindInterpVar, "name"},
		{KindInterpLit, ", n is "},
		{KindInterpVar, "n"},
		{KindInterpLit, ""},
		{KindInterpEnd, ""},
		{KindEOF, ""},
	}
	if len(tokens) != len(want) {
		t.Fatalf("got %d tokens, want %d: %#v", len(tokens), len(want), tokens)
	}
	for i, w := range want {
		if tokens[i].Kind != w.kind {
			t.Errorf("token %d kind = %v, want %v", i, tokens[i].Kind, w.kind)
		}
		if w.kind == KindInterpLit || w.kind == KindInterpVar {
			if tokens[i].Value != w.value {
				t.Errorf("token %d value = %q, want %q", i, tokens[i].Value, w.value)
			}
		}
	}
}

func TestLexInterpolatedStringLeadingVarHasEmptyLit(t *testing.T) {
	// "{n} stuff" — a Var that opens the string must be sandwiched by an
	// empty Lit so the parser sees uniform alternation.
	tokens, err := Lex([]byte(`"{n} stuff"`))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	want := []Kind{KindInterpStart, KindInterpLit, KindInterpVar, KindInterpLit, KindInterpEnd, KindEOF}
	if len(tokens) != len(want) {
		t.Fatalf("got %d tokens, want %d: %#v", len(tokens), len(want), tokens)
	}
	for i, w := range want {
		if tokens[i].Kind != w {
			t.Errorf("token %d kind = %v, want %v", i, tokens[i].Kind, w)
		}
	}
	if tokens[1].Value != "" {
		t.Errorf("token 1 (leading Lit) value = %q, want empty", tokens[1].Value)
	}
}

func TestLexInterpolatedStringAdjacentVars(t *testing.T) {
	// "{a}{b}" — adjacent vars must be separated by an empty Lit so the
	// alternation invariant holds.
	tokens, err := Lex([]byte(`"{a}{b}"`))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	want := []Kind{
		KindInterpStart,
		KindInterpLit,
		KindInterpVar,
		KindInterpLit,
		KindInterpVar,
		KindInterpLit,
		KindInterpEnd,
		KindEOF,
	}
	if len(tokens) != len(want) {
		t.Fatalf("got %d tokens, want %d: %#v", len(tokens), len(want), tokens)
	}
	for i, w := range want {
		if tokens[i].Kind != w {
			t.Errorf("token %d kind = %v, want %v", i, tokens[i].Kind, w)
		}
	}
}

func TestLexInterpolatedStringEscapesPreserved(t *testing.T) {
	tokens, err := Lex([]byte(`"a\nb\t{x}c\{d\}"`))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if tokens[1].Kind != KindInterpLit || tokens[1].Value != "a\nb\t" {
		t.Errorf("first Lit = %v %q, want KindInterpLit %q", tokens[1].Kind, tokens[1].Value, "a\nb\t")
	}
	if tokens[2].Kind != KindInterpVar || tokens[2].Value != "x" {
		t.Errorf("Var = %v %q, want KindInterpVar %q", tokens[2].Kind, tokens[2].Value, "x")
	}
	if tokens[3].Kind != KindInterpLit || tokens[3].Value != "c{d}" {
		t.Errorf("trailing Lit = %v %q, want KindInterpLit %q", tokens[3].Kind, tokens[3].Value, "c{d}")
	}
}

// --- rejection cases --------------------------------------------------------

func TestLexInterpRejectsEmptySlot(t *testing.T) {
	_, err := Lex([]byte(`"{}"`))
	if err == nil {
		t.Fatalf("expected lex error, got nil")
	}
	if !strings.Contains(err.Error(), "empty interpolation") {
		t.Errorf("error = %v, want substring 'empty interpolation'", err)
	}
}

func TestLexInterpRejectsWhitespace(t *testing.T) {
	_, err := Lex([]byte(`"{ x }"`))
	if err == nil {
		t.Fatalf("expected lex error, got nil")
	}
	if !strings.Contains(err.Error(), "interpolation must contain a single identifier") {
		t.Errorf("error = %v, want substring 'single identifier'", err)
	}
}

func TestLexInterpRejectsNonIdent(t *testing.T) {
	_, err := Lex([]byte(`"{1n}"`))
	if err == nil {
		t.Fatalf("expected lex error, got nil")
	}
	if !strings.Contains(err.Error(), "single identifier") {
		t.Errorf("error = %v, want substring 'single identifier'", err)
	}
}

func TestLexInterpRejectsExpression(t *testing.T) {
	_, err := Lex([]byte(`"{a + b}"`))
	if err == nil {
		t.Fatalf("expected lex error, got nil")
	}
	if !strings.Contains(err.Error(), "single identifier") {
		t.Errorf("error = %v, want substring 'single identifier'", err)
	}
}

func TestLexInterpRejectsUnterminatedSlot(t *testing.T) {
	_, err := Lex([]byte(`"oops {n`))
	if err == nil {
		t.Fatalf("expected lex error, got nil")
	}
	// May surface as 'unterminated interpolation' OR 'unterminated string literal' —
	// the former is preferred but both forms describe the same failure.
	msg := err.Error()
	if !strings.Contains(msg, "unterminated") {
		t.Errorf("error = %v, want substring 'unterminated'", err)
	}
}

func TestLexRejectsBareCloseBraceInString(t *testing.T) {
	_, err := Lex([]byte(`"bad } here"`))
	if err == nil {
		t.Fatalf("expected lex error, got nil")
	}
	if !strings.Contains(err.Error(), "use \\}") {
		t.Errorf("error = %v, want substring 'use \\\\}'", err)
	}
}
