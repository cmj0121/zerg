package build

import "testing"

// RUN-based tests for the pure-Zerg transcendentals in the stdlib `math` module
// (src/stdlib/math.zg). Zerg is zero-external-dependency, so these are numerical
// algorithms over the primitive float ops — no libm binding. Output is f-string
// formatted for a deterministic decimal rendering.

// TestMathSqrtRuns exercises math.sqrt (Newton's method): an exact square, an
// irrational, zero, and the negative-domain error demoted by guard.
func TestMathSqrtRuns(t *testing.T) {
	got := runProgramRT(t, "import \"math\"\n"+
		"fn main() -> Result[nil] {\n"+
		"\tprint f\"{math.sqrt(9.0):.4f}\"\n"+
		"\tprint f\"{math.sqrt(2.0):.6f}\"\n"+
		"\tprint f\"{math.sqrt(0.0):.1f}\"\n"+
		"\tprint f\"{guard { math.sqrt(-4.0) } ?? -1.0:.1f}\"\n"+
		"\treturn nil\n}\n")
	if want := "3.0000\n1.414214\n0.0\n-1.0\n"; got != want {
		t.Fatalf("math.sqrt: got %q, want %q", got, want)
	}
}

// TestMathPowRuns exercises math.pow (exponentiation by squaring): a large positive
// exponent, exponent zero, a negative (reciprocal) exponent, and the zero-base /
// negative-exponent domain error demoted by guard.
func TestMathPowRuns(t *testing.T) {
	got := runProgramRT(t, "import \"math\"\n"+
		"fn main() -> Result[nil] {\n"+
		"\tprint f\"{math.pow(2.0, 10):.1f}\"\n"+
		"\tprint f\"{math.pow(3.0, 0):.1f}\"\n"+
		"\tprint f\"{math.pow(2.0, -3):.4f}\"\n"+
		"\tprint f\"{guard { math.pow(0.0, -1) } ?? -1.0:.1f}\"\n"+
		"\treturn nil\n}\n")
	if want := "1024.0\n1.0\n0.1250\n-1.0\n"; got != want {
		t.Fatalf("math.pow: got %q, want %q", got, want)
	}
}

// TestMathConstantsRuns exercises the pi/e constant functions (functions pending a
// module-level value-constant surface in the grammar).
func TestMathConstantsRuns(t *testing.T) {
	got := runProgramRT(t, "import \"math\"\n"+
		"fn main() -> Result[nil] {\n"+
		"\tprint f\"{math.pi():.5f}\"\n"+
		"\tprint f\"{math.e():.5f}\"\n"+
		"\treturn nil\n}\n")
	if want := "3.14159\n2.71828\n"; got != want {
		t.Fatalf("math constants: got %q, want %q", got, want)
	}
}

// TestMathRoundingRuns covers the rounding family trunc/floor/ceil/round over positive
// and negative inputs, the half-away-from-zero rule, and an already-integral value.
//
// The four answer an `int` now, because `int(x)` on a float is refused and a verb that
// gave back a float would leave the caller holding that refusal. The goldens did not move
// with the return type: an integral float printed without a decimal, so the digits were
// always what came out.
func TestMathRoundingRuns(t *testing.T) {
	got := runProgramRT(t, "import \"math\"\n"+
		"fn main() {\n"+
		"\tprint math.trunc(2.7)\n"+ // 2
		"\tprint math.trunc(-2.7)\n"+ // -2
		"\tprint math.floor(2.7)\n"+ // 2
		"\tprint math.floor(-2.1)\n"+ // -3
		"\tprint math.ceil(2.1)\n"+ // 3
		"\tprint math.ceil(-2.7)\n"+ // -2
		"\tprint math.round(2.5)\n"+ // 3
		"\tprint math.round(-2.5)\n"+ // -3
		"\tprint math.round(2.4)\n"+ // 2
		"\tprint math.round(-2.4)\n"+ // -2
		"\tprint math.floor(5.0)\n"+ // 5 (already integral)
		"}\n")
	if want := "2\n-2\n2\n-3\n3\n-2\n3\n-3\n2\n-2\n5\n"; got != want {
		t.Fatalf("math rounding: got %q, want %q", got, want)
	}
}

// TestMathRoundingOverflowRaises pins the contract the int return decides: a magnitude no
// `int` holds raises OverflowError rather than coming back unchanged, which is what the old
// float return did with anything past 2^52. It is `guard`-demotable like every other
// conversion that can raise, and the value just below the boundary still converts — so the
// test says where the line is and not only that there is one.
func TestMathRoundingOverflowRaises(t *testing.T) {
	got := runProgramRT(t, "import \"math\"\n"+
		"fn main() {\n"+
		"\tprint math.trunc(4503599627370496.0)\n"+ // 2^52, integral and in range
		"\tprint guard { math.trunc(1e30) } ?? 0\n"+
		"\tprint guard { math.floor(0.0 - 1e30) } ?? 0\n"+
		"}\n")
	if want := "4503599627370496\n0\n0\n"; got != want {
		t.Fatalf("math rounding overflow: got %q, want %q", got, want)
	}
}
