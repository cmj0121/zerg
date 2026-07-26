package build

import (
	"strings"
	"testing"
)

// RUN-based tests for the built-in string conversions completed this pass: float(s) /
// uint(s) parsing (symmetric with int(s)) and str(scalar) rendering (the value's display).

// TestStrConvRuns exercises str(scalar) for each scalar and the float/uint string parses,
// including the guard-demoted failure paths.
func TestStrConvRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n"+
		"\tprint str(42)\n"+ // 42
		"\tprint str(3.5)\n"+ // 3.5
		"\tprint str(true)\n"+ // true
		"\tprint str(byte(65))\n"+ // 65 (byte renders as its integer, like print)
		"\tprint \"n=\" + str(7)\n"+ // n=7  (str in a concatenation)
		"\tprint uint(\"42\")\n"+ // 42
		"\tprint int(\"-17\")\n"+ // -17
		"\tprint float(\"2.5\") == 2.5\n"+ // true
		"\tprint guard { float(\"x\") } ?? -1.0\n"+ // -1  (malformed)
		"\tprint guard { uint(\"-1\") } ?? 0\n"+ // 0   (uint rejects a sign)
		"\tprint guard { int(\"bad\") } ?? -2\n"+ // -2
		"}\n")
	want := "42\n3.5\ntrue\n65\nn=7\n42\n-17\ntrue\n-1\n0\n-2\n"
	if got != want {
		t.Fatalf("string conversions: got %q, want %q", got, want)
	}
}

// TestStrConvLowering pins the emitted C: the parses go through the runtime and
// str(scalar) through the display helpers.
func TestStrConvLowering(t *testing.T) {
	code, _, diags := Compile("fn main() {\n" +
		"\tprint float(\"1.0\")\n\tprint uint(\"1\")\n\tprint str(42)\n\tprint str(1.5)\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{"zrt_parse_float(", "zrt_parse_uint(", "zrt_display_int(", "zrt_display_float("} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
}
