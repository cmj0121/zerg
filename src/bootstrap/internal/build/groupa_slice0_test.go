package build

import (
	"strings"
	"testing"
)

// --- SLICE 0: literal lowering (A6/A7) + gate diagnostics (A8/A7b) -------------
//
// These are RUN-based tests (compile AND execute, asserting stdout + a clean
// exit) for the literal forms the emitter now lowers, and diagnostic tests for
// the constructs it now fails cleanly on instead of silently lowering to "0".

// TestRuneLiteralRuns covers A6: a rune literal 'A' lowers to its DECODED int32_t
// code point (65), never the surface lexeme, and prints via the signed display.
func TestRuneLiteralRuns(t *testing.T) {
	got := runProgram(t, "fn main() {\n print 'A'\n print '0'\n}")
	if got != "65\n48\n" {
		t.Fatalf("rune literal print = %q, want %q", got, "65\n48\n")
	}
}

// TestByteLiteralRuns covers A6: a byte literal b'z' lowers to its decoded octet
// (122) and prints via the (signed) integer display.
func TestByteLiteralRuns(t *testing.T) {
	got := runProgram(t, "fn main() {\n print b'z'\n print b'A'\n}")
	if got != "122\n65\n" {
		t.Fatalf("byte literal print = %q, want %q", got, "122\n65\n")
	}
}

// TestRawStringLiteralRuns covers A7: a raw string r"a\nb" processes NO escapes,
// so its content is the four characters a \ n b — printing it yields a single line
// with a literal backslash-n, not a real newline.
func TestRawStringLiteralRuns(t *testing.T) {
	got := runProgram(t, "fn main() {\n print r\"a\\nb\"\n}")
	if got != "a\\nb\n" {
		t.Fatalf("raw string print = %q, want %q", got, "a\\nb\n")
	}
}

// TestListLiteralValueGate covers A8: a general list[T] value has no runtime yet,
// so a list-literal used as a value must fail cleanly rather than lower to a broken
// C initializer. (A fixed-array [int;N] initializer is unaffected — see the
// value-generic emit test.)
func TestListLiteralValueGate(t *testing.T) {
	code, _, diags := Compile("fn main() {\n x := [1, 2, 3]\n print x[0]\n}")
	if len(diags) == 0 || code != "" {
		t.Fatalf("a list value should be gated, got code=%q diags=%v", code, diags)
	}
	if !strings.Contains(diags[0].Msg, "list value is not yet supported") {
		t.Fatalf("expected the list-value gate diagnostic, got: %v", diags)
	}
}

// TestFillFormGate covers A8: the fill form [v; N] is gated cleanly.
func TestFillFormGate(t *testing.T) {
	code, _, diags := Compile("fn main() {\n x := [0; 3]\n print x[0]\n}")
	if len(diags) == 0 || code != "" {
		t.Fatalf("a fill-form list should be gated, got code=%q diags=%v", code, diags)
	}
	if !strings.Contains(diags[0].Msg, "fill-form list literal is not yet supported") {
		t.Fatalf("expected the fill-form gate diagnostic, got: %v", diags)
	}
}

// TestRangeValueGate covers A8: a range value 0..5 is gated cleanly.
func TestRangeValueGate(t *testing.T) {
	code, _, diags := Compile("fn main() {\n r := 0..5\n print 0\n}")
	if len(diags) == 0 || code != "" {
		t.Fatalf("a range value should be gated, got code=%q diags=%v", code, diags)
	}
	if !strings.Contains(diags[0].Msg, "range value is not yet supported") {
		t.Fatalf("expected the range-value gate diagnostic, got: %v", diags)
	}
}

// TestCommandLiteralGate covers A8: a backtick command literal is gated cleanly
// (it is typed as Str by sema but has no execution lowering).
func TestCommandLiteralGate(t *testing.T) {
	code, _, diags := Compile("fn main() {\n s := `echo hi`\n print s\n}")
	if len(diags) == 0 || code != "" {
		t.Fatalf("a command literal should be gated, got code=%q diags=%v", code, diags)
	}
	if !strings.Contains(diags[0].Msg, "command literal is not yet supported") {
		t.Fatalf("expected the command-literal gate diagnostic, got: %v", diags)
	}
}
