package build

import (
	"strings"
	"testing"
)

// RUN-based tests for the primitive conversion `T(x)` (docs/core/types.md, "Type
// Conversion"): a re-construction of x's value as a T, checked so that a narrowing
// conversion which does not fit raises OverflowError.

// TestConvScalarsRun covers the conversions that always succeed: widening, the
// documented truncation of a float toward zero, and the bool re-construction.
func TestConvScalarsRun(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n"+
		"\tprint int(3.7)\n"+
		"\tprint int(-3.7)\n"+
		"\tprint byte(65)\n"+
		"\tprint uint(5)\n"+
		"\tprint rune(65)\n"+
		"\tb: byte = 65\n"+
		"\tprint int(b)\n"+
		"\tprint bool(8)\n"+
		"\tprint bool(0)\n"+
		"\tprint int(true)\n"+
		"}\n")
	if want := "3\n-3\n65\n5\n65\n65\ntrue\nfalse\n1\n"; got != want {
		t.Fatalf("scalar conversions: got %q, want %q", got, want)
	}
}

// TestConvFloatTruncationRuns pins the float -> integer rule: the fractional part is
// dropped and only the TRUNCATED value is range-checked, so 255.7 converts to 255
// even though 255.7 itself is past the byte maximum.
func TestConvFloatTruncationRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n"+
		"\tprint byte(255.7)\n"+
		"\tprint byte(0.9)\n"+
		"\tprint byte(-0.5)\n"+
		"\tprint byte(255.0)\n"+
		"}\n")
	if want := "255\n0\n0\n255\n"; got != want {
		t.Fatalf("float truncation: got %q, want %q", got, want)
	}
}

// TestConvMaskToTruncateRuns covers the documented way to truncate to the low bits
// instead of raising: mask first, so the value always fits.
func TestConvMaskToTruncateRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n\ty: int = 300\n\tprint byte(y & 0xFF)\n}\n")
	if want := "44\n"; got != want {
		t.Fatalf("mask-then-convert: got %q, want %q", got, want)
	}
}

// TestConvOverflowAborts covers the raising half: a value past the target's range is
// an OverflowError abort, for an integer narrowing, a negative into an unsigned, and
// a float whose integer part does not fit.
func TestConvOverflowAborts(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"fn main() {\n\tn := 300\n\tprint byte(n)\n}\n", "integer conversion out of range"},
		{"fn main() {\n\tn := -5\n\tprint uint(n)\n}\n", "integer conversion out of range"},
		{"fn main() {\n\tprint byte(256.0)\n}\n", "float conversion out of range"},
		{"fn main() {\n\tprint int(1e30)\n}\n", "float conversion out of range"},
	} {
		out := runProgramRTAbort(t, tc.src)
		if !strings.Contains(out, "OverflowError") || !strings.Contains(out, tc.want) {
			t.Fatalf("expected an OverflowError mentioning %q, got:\n%s", tc.want, out)
		}
	}
}

// TestConvGuardDemotesRuns covers the checked form: `guard { byte(x) }` turns the
// abort into a Result, so `??` supplies a default instead of the program dying.
func TestConvGuardDemotesRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n"+
		"\tn := 300\n"+
		"\tbad := guard { byte(n) } ?? 7\n"+
		"\tprint bad\n"+
		"\tgood := guard { byte(42) } ?? 7\n"+
		"\tprint good\n"+
		"}\n")
	if want := "7\n42\n"; got != want {
		t.Fatalf("guarded conversion: got %q, want %q", got, want)
	}
}

// TestConvLosslessEmitsPlainCast pins the optimization that keeps a widening
// conversion free: it must NOT call a checked helper, while a narrowing one must.
func TestConvLosslessEmitsPlainCast(t *testing.T) {
	code, manifest, diags := Compile("fn main() {\n\tb: byte = 65\n\tprint int(b)\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if strings.Contains(code, "zrt_conv_") {
		t.Fatalf("a widening byte->int must not call a checked helper:\n%s", code)
	}
	if manifest.NeedsRuntime {
		t.Fatalf("a program of only-lossless conversions should not need the runtime, got %+v", manifest)
	}

	code, manifest, diags = Compile("fn main() {\n\ty: int = 9\n\tprint byte(y)\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !strings.Contains(code, "zrt_conv_u_from_i(") {
		t.Fatalf("a narrowing int->byte must call the checked helper:\n%s", code)
	}
	if !manifest.NeedsRuntime {
		t.Fatalf("a checked conversion should need the runtime, got %+v", manifest)
	}
}

// TestConvFixedWidthRuns covers the fixed-width family sharing one code path with the
// named primitives.
func TestConvFixedWidthRuns(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n"+
		"\tprint i32(70000)\n"+
		"\tprint u16(65535)\n"+
		"\tprint i8(-128)\n"+
		"}\n")
	if want := "70000\n65535\n-128\n"; got != want {
		t.Fatalf("fixed-width conversions: got %q, want %q", got, want)
	}
}

// TestConvFixedWidthOverflowAborts checks the same bounds apply to the fixed widths.
func TestConvFixedWidthOverflowAborts(t *testing.T) {
	out := runProgramRTAbort(t, "fn main() {\n\tn := 128\n\tprint i8(n)\n}\n")
	if !strings.Contains(out, "OverflowError") {
		t.Fatalf("i8(128) should raise OverflowError, got:\n%s", out)
	}
}

// TestConvNonScalarRejected keeps the mechanism narrow: converting a CONTAINER is not a
// conversion (only a str's byte/rune list bridges, and only a scalar renders via
// str(scalar)). int(str)/uint(str)/float(str) ARE parses (runtime checks) and str(42) IS
// a scalar render — both are covered by their own tests, not here.
func TestConvNonScalarRejected(t *testing.T) {
	for _, src := range []string{
		"fn main() {\n\tprint str([1, 2, 3])\n}\n", // list[int] is neither a str-bridge nor a scalar
		"fn main() {\n\txs := [1, 2]\n\tprint int(xs)\n}\n",
		"fn main() {\n\tprint int(1, 2)\n}\n",
	} {
		if _, _, diags := Compile(src); len(diags) == 0 {
			t.Fatalf("expected a diagnostic for: %s", src)
		}
	}
}

// TestConvDoesNotMaskUserFunction checks the conversion never steals a call — asked of
// the only name it can still be asked of.
//
// It used to be asked of `fn byte`, and the answer it asserted was one compiler's. This
// seed resolved `byte(41)` to the user function and printed 42; the shipping compiler read
// the same callee as the conversion, printed 41, and left the declaration unreachable.
// Both accepted the program and NEITHER said anything — one source, two answers, no
// diagnostic. A prelude name is now refused at the declaration that takes it, in both
// compilers, which is what makes that question unaskable; reject-check.sh pins the refusal
// and TestConvUserFunctionRefusedOnAPreludeName pins it here.
//
// `map` is what is left to ask it of, and it is the whole of the difference between the
// function slot's set and the type slots' (parser.preludeCalleeRole): `map[...](...)` as a
// constructor is built by neither compiler, so no call can spell the name and the
// conversion machinery has nothing to steal.
func TestConvDoesNotMaskUserFunction(t *testing.T) {
	code, _, diags := Compile("fn map(n: int) -> int {\n\treturn n + 1\n}\n" +
		"fn main() {\n\tprint map(41)\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !strings.Contains(code, "zg_map(") {
		t.Fatalf("a user fn named map must keep its call:\n%s", code)
	}
}

// TestConvUserFunctionRefusedOnAPreludeName pins the other half: a function slot taking a
// name a CALL can spell is refused at the declaration, so the conversion and the user
// symbol can never both be candidates for one callee.
func TestConvUserFunctionRefusedOnAPreludeName(t *testing.T) {
	for _, name := range []string{"int", "byte", "str", "bytearray", "list", "Either", "Left", "Eq"} {
		src := "fn " + name + "(n: int) -> int {\n\treturn n + 1\n}\n" +
			"fn main() {\n\tprint 1\n}\n"
		_, _, diags := Compile(src)
		if len(diags) == 0 {
			t.Fatalf("fn %s: expected a refusal at the declaration", name)
		}
		if !strings.Contains(diags[0].Error(), "is a prelude name") {
			t.Fatalf("fn %s: got %v, want the prelude-name refusal", name, diags[0])
		}
	}
}
