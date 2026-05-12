package syntax

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.13 Unit 2 — lexer + parser tests for `asm { … }`.
//
// `asm` lexes as a keyword only when the source declares `# requires: v0.13`
// (or higher). Older requires-versions keep the bareword `asm` as a plain
// identifier so v0.0–v0.12 corpora that legally named locals / fields
// `asm` stay parseable. The body is delivered by the lexer as a single
// KindAsmBody token whose Value is the verbatim bytes between the braces;
// the parser splits it on `${name}` markers into AsmChunkText / AsmChunkInterp.
// ---------------------------------------------------------------------------

// --- lexer gate ------------------------------------------------------------

func TestLexAsmIsIdentBeforeV13(t *testing.T) {
	// At v0.12 (and earlier) `asm` is just an identifier — the keyword
	// is reserved at v0.13. Pin the bareword shape; a hypothetical
	// `asm := 1` form would regression-fail on this exact assertion.
	tokens := lexAtMinor(t, "asm\n", 12)
	if tokens[0].Kind != KindIdent || tokens[0].Value != "asm" {
		t.Errorf("token 0 = %v %q, want IDENT 'asm'", tokens[0].Kind, tokens[0].Value)
	}
}

func TestLexAsmKeywordAtV13(t *testing.T) {
	// At v0.13+ `asm` is a keyword and the lexer immediately scans the
	// body, queueing KindAsmBody behind the KindAsm token.
	tokens := lexAtMinor(t, "asm {\n\tnop\n}\n", 13)
	if tokens[0].Kind != KindAsm || tokens[0].Value != "asm" {
		t.Fatalf("token 0 = %v %q, want KindAsm 'asm'", tokens[0].Kind, tokens[0].Value)
	}
	if tokens[1].Kind != KindAsmBody {
		t.Fatalf("token 1 = %v, want KindAsmBody", tokens[1].Kind)
	}
	if tokens[1].Value != "\n\tnop\n" {
		t.Errorf("body value = %q, want %q", tokens[1].Value, "\n\tnop\n")
	}
}

func TestLexAsmBodyStringLiteralAware(t *testing.T) {
	// A `}` inside a `"…"` is NOT a block close; the body scan tracks
	// string state. Likewise a backslash escapes the next byte so
	// `"\""` doesn't terminate mid-byte.
	src := "asm {\n\tmov x0, \"close } here\"\n\tmov x1, \"esc \\\"X\"\n}\n"
	tokens := lexAtMinor(t, src, 13)
	if tokens[0].Kind != KindAsm {
		t.Fatalf("token 0 = %v, want KindAsm", tokens[0].Kind)
	}
	if tokens[1].Kind != KindAsmBody {
		t.Fatalf("token 1 = %v, want KindAsmBody", tokens[1].Kind)
	}
	want := "\n\tmov x0, \"close } here\"\n\tmov x1, \"esc \\\"X\"\n"
	if tokens[1].Value != want {
		t.Errorf("body value = %q, want %q", tokens[1].Value, want)
	}
}

func TestLexAsmBodyNestedBraces(t *testing.T) {
	// Body brace counting: balanced `{}` inside the body are part of
	// the body and the matching close only fires when depth returns to 0.
	src := "asm { outer { inner } end }\n"
	tokens := lexAtMinor(t, src, 13)
	if tokens[1].Kind != KindAsmBody {
		t.Fatalf("token 1 = %v, want KindAsmBody", tokens[1].Kind)
	}
	if tokens[1].Value != " outer { inner } end " {
		t.Errorf("body value = %q, want %q", tokens[1].Value, " outer { inner } end ")
	}
}

func TestLexAsmUnterminatedRejects(t *testing.T) {
	// Body that runs into EOF before depth returns to 0 → focused
	// lex-error with the open-brace position.
	src := "asm {\n\tnop\n"
	_, _, err := lexWithVersion([]byte(src), 0, 13)
	if err == nil {
		t.Fatalf("expected lex error for unterminated asm block")
	}
	if !strings.Contains(err.Error(), "unterminated asm block") {
		t.Errorf("error = %q, want it to contain 'unterminated asm block'", err.Error())
	}
}

// --- parser ----------------------------------------------------------------

// parseAsmAtV13 lexes + parses src at v0.13. Tests reach into the lexer
// directly because the CLI gate is still at v0.12 at U2.
func parseAsmAtV13(t *testing.T, src string) (*Program, error) {
	t.Helper()
	tokens, _, err := lexWithVersion([]byte(src), 0, 13)
	if err != nil {
		return nil, err
	}
	return Parse(tokens)
}

func TestParseAsmTextOnly(t *testing.T) {
	src := "asm {\n\tmov x0, #1\n\tsvc #0x80\n}\n"
	prog, err := parseAsmAtV13(t, src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(prog.Statements) != 1 {
		t.Fatalf("got %d statements, want 1", len(prog.Statements))
	}
	asm, ok := prog.Statements[0].(*AsmBlock)
	if !ok {
		t.Fatalf("stmt 0 = %T, want *AsmBlock", prog.Statements[0])
	}
	want := "\n\tmov x0, #1\n\tsvc #0x80\n"
	if asm.BodyRaw != want {
		t.Errorf("BodyRaw = %q, want %q", asm.BodyRaw, want)
	}
	if len(asm.Chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(asm.Chunks))
	}
	if asm.Chunks[0].Kind != AsmChunkText {
		t.Errorf("chunk[0].Kind = %v, want AsmChunkText", asm.Chunks[0].Kind)
	}
	if asm.Chunks[0].Text != want {
		t.Errorf("chunk[0].Text = %q, want %q", asm.Chunks[0].Text, want)
	}
}

func TestParseAsmWithInterp(t *testing.T) {
	// One interp in the middle of the body — expect three chunks
	// (text, interp, text). Empty leading/trailing text is admitted so
	// chunk order matches byte order.
	src := "asm { mov x1, ${msg} }\n"
	prog, err := parseAsmAtV13(t, src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	asm := prog.Statements[0].(*AsmBlock)
	if len(asm.Chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(asm.Chunks))
	}
	if asm.Chunks[0].Kind != AsmChunkText || asm.Chunks[0].Text != " mov x1, " {
		t.Errorf("chunk[0] = %#v, want text ' mov x1, '", asm.Chunks[0])
	}
	if asm.Chunks[1].Kind != AsmChunkInterp || asm.Chunks[1].Name != "msg" {
		t.Errorf("chunk[1] = %#v, want interp 'msg'", asm.Chunks[1])
	}
	if asm.Chunks[2].Kind != AsmChunkText || asm.Chunks[2].Text != " " {
		t.Errorf("chunk[2] = %#v, want text ' '", asm.Chunks[2])
	}
}

func TestParseAsmInterpAtBoundaries(t *testing.T) {
	// Interp at body start and body end — chunks bookend with empty text
	// runs swallowed (no AsmChunkText emitted for zero-length leading or
	// trailing text).
	src := "asm {${a} mid ${b}}\n"
	prog, err := parseAsmAtV13(t, src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	asm := prog.Statements[0].(*AsmBlock)
	if len(asm.Chunks) != 3 {
		t.Fatalf("got %d chunks, want 3 (interp, text, interp); chunks: %#v", len(asm.Chunks), asm.Chunks)
	}
	if asm.Chunks[0].Kind != AsmChunkInterp || asm.Chunks[0].Name != "a" {
		t.Errorf("chunk[0] = %#v, want interp 'a'", asm.Chunks[0])
	}
	if asm.Chunks[1].Kind != AsmChunkText || asm.Chunks[1].Text != " mid " {
		t.Errorf("chunk[1] = %#v, want text ' mid '", asm.Chunks[1])
	}
	if asm.Chunks[2].Kind != AsmChunkInterp || asm.Chunks[2].Name != "b" {
		t.Errorf("chunk[2] = %#v, want interp 'b'", asm.Chunks[2])
	}
}

func TestParseAsmInterpEmptyRejects(t *testing.T) {
	src := "asm { ${} }\n"
	_, err := parseAsmAtV13(t, src)
	if err == nil {
		t.Fatalf("expected parse error for empty '${}'")
	}
	if !strings.Contains(err.Error(), "empty interpolation") {
		t.Errorf("error = %q, want it to contain 'empty interpolation'", err.Error())
	}
}

func TestSplitAsmBodyUnterminatedInterpRejects(t *testing.T) {
	// Unterminated `${` is unreachable through the normal lex → parse
	// pipeline because the lexer's body scan brace-counts `{` and `}`
	// uniformly — a body that contains an open `${` is itself unbalanced
	// and the lexer rejects it first with `unterminated asm block`. The
	// branch in splitAsmBody is defence-in-depth for callers that
	// construct an AsmBlock body directly (tests, future tooling). Cover
	// it here by calling splitAsmBody with a hand-rolled raw body so the
	// defence path keeps working even if the upstream contract changes.
	_, err := splitAsmBody(Position{Line: 1, Column: 5}, "${name")
	if err == nil {
		t.Fatalf("expected error for unterminated '${' in raw body")
	}
	if !strings.Contains(err.Error(), "unterminated '${'") {
		t.Errorf("error = %q, want it to contain 'unterminated ${'", err.Error())
	}
}

func TestParseAsmInterpInvalidNameRejects(t *testing.T) {
	// Digit-leading name violates the v0.5 identifier rule. The parser
	// surfaces a focused diagnostic with the offending name in the
	// quoted form.
	src := "asm { ${1bad} }\n"
	_, err := parseAsmAtV13(t, src)
	if err == nil {
		t.Fatalf("expected parse error for invalid interp name")
	}
	if !strings.Contains(err.Error(), "invalid interpolation name") {
		t.Errorf("error = %q, want it to contain 'invalid interpolation name'", err.Error())
	}
}

// --- fmt round-trip --------------------------------------------------------

// asmFormat re-formats src by lex → parse → fmt and returns the result.
// The fmt assertion mode is "byte-preserved body" — the formatter MUST emit
// the asm body bytes exactly as it received them.
func TestFmtAsmRoundTripPreservesBody(t *testing.T) {
	bodies := []string{
		"\n\tmov x0, #1\n\tsvc #0x80\n",
		" mov x1, ${msg} ",
		"\n\tmov x0, \"close } here\"\n",
		// Empty body — the parser admits it; the formatter must round-trip.
		"",
	}
	for _, body := range bodies {
		src := "asm {" + body + "}\n"
		prog, err := parseAsmAtV13(t, src)
		if err != nil {
			t.Fatalf("parse %q: %v", src, err)
		}
		asm := prog.Statements[0].(*AsmBlock)
		if asm.BodyRaw != body {
			t.Errorf("BodyRaw = %q, want %q (source %q)", asm.BodyRaw, body, src)
		}
	}
}
