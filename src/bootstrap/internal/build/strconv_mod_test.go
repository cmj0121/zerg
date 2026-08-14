package build

import (
	"strings"
	"testing"
)

// RUN-based tests for the `strconv` stdlib module (src/stdlib/strconv.zg) — base-aware
// numeric conversion reached with `import "strconv"`, the layer the decimal-only built-in
// conversions do not cover. Programs run under runProgramRTBalanced (exact stdout +
// alloc/free balance, since to_string builds heap strings).

// TestStrconvParse covers parse_int across bases and signs, parse_uint at the 64-bit
// ceiling, and parse_bool.
func TestStrconvParse(t *testing.T) {
	got := runProgramRTBalanced(t, "import \"strconv\"\n"+
		"fn main() {\n"+
		"\tprint strconv.parse_int(\"ff\", 16)\n"+ // 255
		"\tprint strconv.parse_int(\"-101\", 2)\n"+ // -5
		"\tprint strconv.parse_int(\"+777\", 8)\n"+ // 511
		"\tprint strconv.parse_int(\"zz\", 36)\n"+ // 1295
		"\tprint strconv.parse_uint(\"ffffffffffffffff\", 16)\n"+ // 18446744073709551615
		"\tprint strconv.parse_bool(\"true\")\n"+ // true
		"\tprint strconv.parse_bool(\"false\")\n"+ // false
		"}\n")
	if want := "255\n-5\n511\n1295\n18446744073709551615\ntrue\nfalse\n"; got != want {
		t.Fatalf("strconv parse: got %q, want %q", got, want)
	}
}

// TestStrconvToString covers rendering across bases including 0, a negative, and the
// INT_MIN edge (the whole value is never negated, so it does not overflow).
func TestStrconvToString(t *testing.T) {
	got := runProgramRTBalanced(t, "import \"strconv\"\n"+
		"fn main() {\n"+
		"\tprint strconv.to_string(255, 16)\n"+ // ff
		"\tprint strconv.to_string(-5, 2)\n"+ // -101
		"\tprint strconv.to_string(0, 10)\n"+ // 0
		"\tprint strconv.to_string(-9223372036854775807 - 1, 10)\n"+ // INT_MIN
		"}\n")
	if want := "ff\n-101\n0\n-9223372036854775808\n"; got != want {
		t.Fatalf("strconv to_string: got %q, want %q", got, want)
	}
}

// TestStrconvRoundTrip pins that to_string and parse_int invert each other for a
// negative value in a non-decimal base.
func TestStrconvRoundTrip(t *testing.T) {
	got := runProgramRTBalanced(t, "import \"strconv\"\n"+
		"fn main() {\n"+
		"\tprint strconv.parse_int(strconv.to_string(-12345, 16), 16)\n"+
		"}\n")
	if want := "-12345\n"; got != want {
		t.Fatalf("strconv round trip: got %q, want %q", got, want)
	}
}

// TestStrconvAborts pins the loud ValueError paths: a base out of range, a digit not
// valid for the base, an empty string, and a non-bool.
//
// The `ValueError: ` prefix is part of every want, because it is the part that was not
// true: this test named the kind and asserted only the text, and the raises threw a bare
// `str` — so each of these aborted with a message and no kind at all, and a caught one
// answered `false` to `e is ValueError`.
func TestStrconvAborts(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"fn main() { print strconv.parse_int(\"10\", 40) }", "ValueError: strconv: base out of range"},
		{"fn main() { print strconv.parse_int(\"1f\", 10) }", "ValueError: strconv.parse_int: invalid digit for base"},
		{"fn main() { print strconv.parse_int(\"\", 10) }", "ValueError: strconv.parse_int: empty string"},
		{"fn main() { print strconv.parse_bool(\"yes\") }", "ValueError: strconv.parse_bool: not a bool"},
	}
	for _, tc := range cases {
		out := runProgramRTAbort(t, "import \"strconv\"\n"+tc.src+"\n")
		if !strings.Contains(out, tc.want) {
			t.Fatalf("expected abort %q, got %q", tc.want, out)
		}
	}
}
