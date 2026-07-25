package build

import (
	"strings"
	"testing"
)

// RUN-based tests for string scanning (docs/collections.md): a str is not indexable, so
// you bridge to a list[byte] (raw octets) or list[rune] (code points), scan/index that,
// and build a str back with str(...), which validates the str invariant and raises on
// violation. A str also iterates as its runes. Each program is compiled, linked, and
// executed for exact stdout.

// TestStrToBytesRuns covers str -> list[byte]: length, index, and the round-trip back
// through str(bytes).
func TestStrToBytesRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n"+
		"\ts := \"hello\"\n"+
		"\tbytes := list[byte](s)\n"+
		"\tprint bytes.len()\n"+
		"\tprint bytes[0]\n"+
		"\tprint bytes[4]\n"+
		"\tprint str(bytes) == s\n}\n")
	if want := "5\n104\n111\ntrue\n"; got != want {
		t.Fatalf("str->bytes: got %q, want %q", got, want)
	}
}

// TestStrToRunesRuns covers str -> list[rune] over multi-byte UTF-8: the code points and
// the round-trip back through str(runes).
func TestStrToRunesRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n"+
		"\ts := \"a\u00e9\u4e2d\"\n"+ // a, é (U+00E9), 中 (U+4E2D)
		"\trunes := list[rune](s)\n"+
		"\tprint runes.len()\n"+
		"\tprint runes[0]\n\tprint runes[1]\n\tprint runes[2]\n"+
		"\tprint list[byte](s).len()\n"+
		"\tprint str(runes) == s\n}\n")
	if want := "3\n97\n233\n20013\n6\ntrue\n"; got != want {
		t.Fatalf("str->runes: got %q, want %q", got, want)
	}
}

// TestStrFromBytesValidates covers the checked conversion: invalid UTF-8 raises
// EncodingError, and guard demotes it to a Result so a default takes over.
func TestStrFromBytesValidates(t *testing.T) {
	out := runProgramRTAbort(t, "fn main() {\n\tprint str([byte(0xFF)])\n}\n")
	if !strings.Contains(out, "EncodingError") {
		t.Fatalf("str of invalid UTF-8 should raise EncodingError, got:\n%s", out)
	}
	got := runProgramRT(t, "fn main() {\n"+
		"\tbad := guard { str([byte(0xFF)]) } ?? \"fallback\"\n"+
		"\tprint bad\n"+
		"\tok := guard { str([byte(65), byte(66)]) } ?? \"x\"\n"+
		"\tprint ok\n}\n")
	if want := "fallback\nAB\n"; got != want {
		t.Fatalf("guarded str(bytes): got %q, want %q", got, want)
	}
}

// TestStrForInRuns covers `for c in s`: the loop variable binds each rune, over
// multi-byte UTF-8, with break/continue.
func TestStrForInRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n"+
		"\tfor c in \"a\u00e9\u4e2d\" {\n\t\tprint c\n\t}\n"+
		"\tfor c in \"abcde\" {\n\t\tif c == 99 { continue }\n\t\tif c == 101 { break }\n\t\tprint c\n\t}\n}\n")
	if want := "97\n233\n20013\n97\n98\n100\n"; got != want {
		t.Fatalf("for c in s: got %q, want %q", got, want)
	}
}

// TestStrScanLexerRuns is the load-bearing use case: scan source text by indexing a
// list[byte] (with a range loop and byte classification) and materialize a token with
// str — the docs' lexer pattern, and the reason self-hosting needs this.
func TestStrScanLexerRuns(t *testing.T) {
	got := runProgramRT(t, "fn is_digit(b: byte) -> bool {\n\treturn b >= 48 and b <= 57\n}\n"+
		"fn main() {\n"+
		"\tbytes := list[byte](\"ab12cd3\")\n"+
		"\tmut digits := 0\n\tmut letters := 0\n"+
		"\tfor i in 0..bytes.len() {\n"+
		"\t\tif is_digit(bytes[i]) {\n\t\t\tdigits = digits + 1\n\t\t} else {\n\t\t\tletters = letters + 1\n\t\t}\n\t}\n"+
		"\tprint digits\n\tprint letters\n"+
		"\tprint str([bytes[0], bytes[1]])\n}\n")
	if want := "3\n4\nab\n"; got != want {
		t.Fatalf("mini lexer: got %q, want %q", got, want)
	}
}

// TestStrScanLowering pins the emitted C: the bridge and iteration go through the
// runtime str helpers.
func TestStrScanLowering(t *testing.T) {
	code, _, diags := Compile("fn main() {\n" +
		"\tbytes := list[byte](\"hi\")\n" +
		"\tprint str(bytes)\n" +
		"\tfor c in \"hi\" { print c }\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{
		"zrt_str_bytes(",
		"zrt_str_from_bytes(",
		"zrt_str_runes(",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
}

// TestStrBridgeRejected keeps the surface honest: `str(x)` builds from a byte/rune list
// or a scalar only (not a list[int]); `list[byte]/[rune]` bridge only from a str; and a
// str is not indexable.
func TestStrBridgeRejected(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"str is not indexable", "fn main() {\n\tprint \"hi\"[0]\n}\n"},
		{"str from a list[int]", "fn main() {\n\tprint str([1, 2, 3])\n}\n"},
		{"list[byte] from a non-str", "fn main() {\n\tprint list[byte](42)\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, diags := Compile(tc.src); len(diags) == 0 {
				t.Fatalf("expected a diagnostic for %s", tc.name)
			}
		})
	}
}
