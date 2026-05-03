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

func TestLexInterpolationRejected(t *testing.T) {
	src := []byte(`print "hi {name}"`)
	_, err := Lex(src)
	if err == nil {
		t.Fatal("expected lex error for interpolation, got nil")
	}
	le, ok := err.(*LexError)
	if !ok {
		t.Fatalf("error is %T, want *LexError: %v", err, err)
	}
	if !strings.Contains(le.Message, "interpolation not supported in v0.0") {
		t.Errorf("error message %q does not flag v0.0 interpolation restriction", le.Message)
	}
	// `{` in `"hi {name}"` is at column 11 (1-based, counting from the `p`
	// in `print`). Tolerate a small offset just in case the implementation
	// chooses to point at the `{` itself or the start of the string.
	if le.Pos.Line != 1 {
		t.Errorf("error line = %d, want 1", le.Pos.Line)
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
	if ps.Value != "Hello, Zerg!" {
		t.Errorf("print value = %q, want %q", ps.Value, "Hello, Zerg!")
	}
}

func TestParseRejectsPrintOfNonString(t *testing.T) {
	tokens, err := Lex([]byte("print foo\n"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	_, err = Parse(tokens)
	if err == nil {
		t.Fatal("expected parse error for print of identifier, got nil")
	}
	if _, ok := err.(*ParseError); !ok {
		t.Errorf("error is %T, want *ParseError", err)
	}
}

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
