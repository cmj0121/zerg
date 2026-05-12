package syntax

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.13 Unit 3 — typeck binder + type rules for `${name}` interpolation.
//
// The parser-level shape lives in parser_v13_asm_test.go. Here we exercise
// the typeck stage: every `${name}` must resolve to an in-scope binding
// whose type is `byte` (lowered to an immediate operand by cgen U4) or
// `list[byte]` (lowered to a `.data` pointer to the first byte). Any other
// type rejects; an unknown name rejects with a focused diagnostic.
// ---------------------------------------------------------------------------

// checkAsmAtV13 runs lex → parse → Check on src at v0.13 and returns Check's
// error (nil for success). Used by every U3 typeck assertion.
func checkAsmAtV13(t *testing.T, src string) error {
	t.Helper()
	tokens, _, err := lexWithVersion([]byte(src), 0, 13)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	prog, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return Check(prog)
}

func TestCheckAsmInterpUnknownNameRejects(t *testing.T) {
	src := "asm { mov x0, ${missing} }\n"
	err := checkAsmAtV13(t, src)
	if err == nil {
		t.Fatalf("Check unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "asm interpolation '${missing}' references unknown name") {
		t.Errorf("error = %q, want unknown-name diagnostic", err.Error())
	}
}

func TestCheckAsmInterpByteAccepted(t *testing.T) {
	// `'A'` defaults to byte (the ASCII literal rule).
	src := "b := 'A'\nasm { mov x0, ${b} }\n"
	if err := checkAsmAtV13(t, src); err != nil {
		t.Fatalf("Check failed for byte binding: %v", err)
	}
}

func TestCheckAsmInterpListByteAccepted(t *testing.T) {
	src := "msg: list[byte] = ['h', 'i']\nasm { mov x1, ${msg} }\n"
	if err := checkAsmAtV13(t, src); err != nil {
		t.Fatalf("Check failed for list[byte] binding: %v", err)
	}
}

func TestCheckAsmInterpIntRejects(t *testing.T) {
	// int is the most common wrong-type case (bare numeric literal).
	src := "n := 5\nasm { mov x0, ${n} }\n"
	err := checkAsmAtV13(t, src)
	if err == nil {
		t.Fatalf("Check unexpectedly succeeded for int binding")
	}
	if !strings.Contains(err.Error(), "must be byte or list[byte], got int") {
		t.Errorf("error = %q, want byte-or-list[byte] diagnostic with 'got int'", err.Error())
	}
}

func TestCheckAsmInterpStrRejects(t *testing.T) {
	// str is what users will reach for first when they want a "byte
	// buffer"; the diagnostic must steer them to list[byte] explicitly.
	src := "s := \"hi\"\nasm { mov x1, ${s} }\n"
	err := checkAsmAtV13(t, src)
	if err == nil {
		t.Fatalf("Check unexpectedly succeeded for str binding")
	}
	if !strings.Contains(err.Error(), "must be byte or list[byte], got str") {
		t.Errorf("error = %q, want diagnostic with 'got str'", err.Error())
	}
}

func TestCheckAsmInterpListIntRejects(t *testing.T) {
	// list[int] is structurally close to list[byte] but rejects — the
	// cgen lowering is byte-pointer-specific and has no contract for
	// arbitrary list element types.
	src := "xs := [1, 2, 3]\nasm { mov x1, ${xs} }\n"
	err := checkAsmAtV13(t, src)
	if err == nil {
		t.Fatalf("Check unexpectedly succeeded for list[int] binding")
	}
	if !strings.Contains(err.Error(), "must be byte or list[byte], got list[int]") {
		t.Errorf("error = %q, want diagnostic with 'got list[int]'", err.Error())
	}
}

func TestCheckAsmInterpRuneRejects(t *testing.T) {
	// rune is the "lookalike" trap — same lexical literal as byte for
	// ASCII codepoints, but the type differs and cgen has no path.
	src := "r: rune = '漢'\nasm { mov x0, ${r} }\n"
	err := checkAsmAtV13(t, src)
	if err == nil {
		t.Fatalf("Check unexpectedly succeeded for rune binding")
	}
	if !strings.Contains(err.Error(), "must be byte or list[byte], got rune") {
		t.Errorf("error = %q, want diagnostic with 'got rune'", err.Error())
	}
}

func TestParseAsmDoubleDollarRejects(t *testing.T) {
	// `$$` is reserved at v0.13 — the splitter rejects with a focused
	// diagnostic anchored on the first `$`. Pin the rule here because
	// users will probably write `$$` thinking it produces a literal `$`
	// in the asm output.
	src := "asm { mov x0, $${a} }\n"
	_, err := parseAsmAtV13(t, src)
	if err == nil {
		t.Fatalf("parse unexpectedly succeeded for '$$'")
	}
	if !strings.Contains(err.Error(), "'$$' is reserved in asm body") {
		t.Errorf("error = %q, want '$$' reserved diagnostic", err.Error())
	}
}
