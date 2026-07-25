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
