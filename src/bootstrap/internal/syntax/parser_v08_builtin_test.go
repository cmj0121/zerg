package syntax

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.8 Unit 1 — lexer + parser tests for the `__builtin` fn-decl marker.
//
// The marker lexes only when the file declares `# requires: v0.8` (or
// higher). Older requires-versions keep the bareword `__builtin` as a plain
// identifier so v0.0–v0.7 sources stay parseable. The keyword itself is also
// gated to embedded stdlib files via a parser flag the loader will set in
// Unit 2 — for tests we drive the flag directly via ParseWithOptions.
// ---------------------------------------------------------------------------

// lexAtMinor is the version-aware Lex shortcut used throughout the v0.8
// tests: it pretends the source carried a `# requires: v0.<minor>` line so
// the test inputs stay readable even when they exercise the lexer-only gate.
func lexAtMinor(t *testing.T, src string, minor int) []Token {
	t.Helper()
	tokens, _, err := lexWithVersion([]byte(src), 0, minor)
	if err != nil {
		t.Fatalf("lexWithVersion(%q, v0.%d): %v", src, minor, err)
	}
	return tokens
}

// parseStdlibSrc parses src as if it were one of the embedded `std/` modules
// at v0.8. Tests that need to exercise the user-file rejection set
// InStdlibFile = false themselves.
func parseStdlibSrc(t *testing.T, src string) (*Program, error) {
	t.Helper()
	tokens, _, err := lexWithVersion([]byte(src), 0, 8)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	return ParseWithOptions(tokens, ParseOptions{InStdlibFile: true})
}

// --- lexer gate ------------------------------------------------------------

func TestLexBuiltinIsIdentBeforeV08(t *testing.T) {
	// At v0.7 (and earlier) `__builtin` is just an identifier — the keyword
	// is reserved at v0.8. Pin both the bare-word and a hypothetical
	// `let __builtin := 1` shape so any future regression surfaces here.
	tokens := lexAtMinor(t, "__builtin\n", 7)
	if tokens[0].Kind != KindIdent || tokens[0].Value != "__builtin" {
		t.Errorf("token 0 = %v %q, want IDENT '__builtin'", tokens[0].Kind, tokens[0].Value)
	}
}

func TestLexBuiltinIsKeywordAtV08(t *testing.T) {
	tokens := lexAtMinor(t, "__builtin\n", 8)
	if tokens[0].Kind != KindBuiltin || tokens[0].Value != "__builtin" {
		t.Errorf("token 0 = %v %q, want KindBuiltin '__builtin'", tokens[0].Kind, tokens[0].Value)
	}
}

func TestLexBuiltinPrefixDoesNotPromote(t *testing.T) {
	// `__builtin_x` is a normal identifier at every version — only the bare
	// word `__builtin` promotes. Mirrors the every-other-keyword rule in
	// keywords.go.
	tokens := lexAtMinor(t, "__builtin_x __builtinX\n", 8)
	if tokens[0].Kind != KindIdent || tokens[0].Value != "__builtin_x" {
		t.Errorf("token 0 = %v %q, want IDENT '__builtin_x'", tokens[0].Kind, tokens[0].Value)
	}
	if tokens[1].Kind != KindIdent || tokens[1].Value != "__builtinX" {
		t.Errorf("token 1 = %v %q, want IDENT '__builtinX'", tokens[1].Kind, tokens[1].Value)
	}
}

func TestLexBuiltinViaRequiresMarker(t *testing.T) {
	// End-to-end: a real `# requires: v0.8` line in the source promotes the
	// keyword via the public Lex entry. This is the path the driver takes
	// for actual on-disk files.
	src := "# requires: v0.8\n__builtin\n"
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	// Find the __builtin token: the requires comment is stripped, so the
	// stream starts with NEWLINE then KindBuiltin.
	var got Kind
	for _, tok := range tokens {
		if tok.Value == "__builtin" {
			got = tok.Kind
			break
		}
	}
	if got != KindBuiltin {
		t.Errorf("__builtin token kind = %v, want KindBuiltin", got)
	}
}

func TestLexBuiltinViaRequiresMarkerV07(t *testing.T) {
	// Same shape as the v0.8 case but at v0.7: the bare word stays an IDENT.
	src := "# requires: v0.7\n__builtin\n"
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	var got Kind
	for _, tok := range tokens {
		if tok.Value == "__builtin" {
			got = tok.Kind
			break
		}
	}
	if got != KindIdent {
		t.Errorf("__builtin token kind = %v, want KindIdent", got)
	}
}

// --- parser shape ----------------------------------------------------------

func TestParseBuiltinFnDeclShape(t *testing.T) {
	src := "fn read_file(path: string) -> int __builtin io_read_file\n"
	prog, err := parseStdlibSrc(t, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fn := expectOne[*FnDecl](t, prog)
	if fn.Name != "read_file" {
		t.Errorf("name = %q, want read_file", fn.Name)
	}
	if fn.BuiltinName != "io_read_file" {
		t.Errorf("BuiltinName = %q, want io_read_file", fn.BuiltinName)
	}
	if fn.Body != nil {
		t.Errorf("Body = %v, want nil for __builtin fn", fn.Body)
	}
	if fn.BuiltinNamePos == (Position{}) {
		t.Errorf("BuiltinNamePos is zero, want a real position")
	}
	// Sanity: the recorded position points at the bareword, which sits on
	// line 1 after the `__builtin` keyword.
	if fn.BuiltinNamePos.Line != 1 {
		t.Errorf("BuiltinNamePos.Line = %d, want 1", fn.BuiltinNamePos.Line)
	}
}

func TestParseBuiltinFnDeclNoReturnType(t *testing.T) {
	// The marker is allowed even without an explicit return type — the fn
	// just declares a void-returning host primitive.
	src := "fn touch() __builtin io_touch\n"
	prog, err := parseStdlibSrc(t, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fn := expectOne[*FnDecl](t, prog)
	if fn.BuiltinName != "io_touch" {
		t.Errorf("BuiltinName = %q, want io_touch", fn.BuiltinName)
	}
	if fn.Return != nil {
		t.Errorf("Return = %v, want nil", fn.Return)
	}
}

// --- parser rejects --------------------------------------------------------

func TestParseBuiltinFnDeclRejectsBody(t *testing.T) {
	src := "fn foo() -> int __builtin foo_impl { return 1 }\n"
	tokens, _, err := lexWithVersion([]byte(src), 0, 8)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	_, err = ParseWithOptions(tokens, ParseOptions{InStdlibFile: true})
	if err == nil {
		t.Fatalf("expected parse error for body after __builtin")
	}
	if !strings.Contains(err.Error(), "__builtin") {
		t.Errorf("error %q does not mention __builtin", err.Error())
	}
}

func TestParseBuiltinFnDeclRejectsStringName(t *testing.T) {
	// The marker takes a bareword IDENT — a string literal is rejected so
	// typo-driven host-resolution lookups fail at typeck rather than later.
	src := "fn foo() -> int __builtin \"foo_impl\"\n"
	tokens, _, err := lexWithVersion([]byte(src), 0, 8)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	_, err = ParseWithOptions(tokens, ParseOptions{InStdlibFile: true})
	if err == nil {
		t.Fatalf("expected parse error for string-literal builtin name")
	}
}

func TestParseBuiltinFnDeclRejectsMissingName(t *testing.T) {
	src := "fn foo() -> int __builtin\n"
	tokens, _, err := lexWithVersion([]byte(src), 0, 8)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	_, err = ParseWithOptions(tokens, ParseOptions{InStdlibFile: true})
	if err == nil {
		t.Fatalf("expected parse error for missing builtin name")
	}
}

func TestParseBuiltinKeywordRejectedInUserFile(t *testing.T) {
	// User-loaded files (InStdlibFile = false) reject the keyword with the
	// focused diagnostic. The lexer still promotes the word — it's the
	// parser's job to gate access.
	src := "fn foo() -> int __builtin foo_impl\n"
	tokens, _, err := lexWithVersion([]byte(src), 0, 8)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	_, err = ParseWithOptions(tokens, ParseOptions{InStdlibFile: false})
	if err == nil {
		t.Fatalf("expected parse error in user file")
	}
	if !strings.Contains(err.Error(), "stdlib") {
		t.Errorf("error %q does not mention stdlib", err.Error())
	}
}

// --- regression: v0.7 corpora keep parsing ---------------------------------

func TestParseBuiltinDoesNotAffectV07Sources(t *testing.T) {
	// A v0.7 source that happens to contain the literal `__builtin` as an
	// identifier (e.g. as a fn name) keeps parsing. We don't ship such a
	// corpus, but the gate must hold for backwards compat.
	src := "# requires: v0.7\nlet __builtin := 1\n"
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if _, err := Parse(tokens); err != nil {
		t.Fatalf("Parse: %v", err)
	}
}
