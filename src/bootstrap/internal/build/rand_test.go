package build

import (
	"strings"
	"testing"
)

// RUN-based tests for the stdlib `rand` module (src/stdlib/rand.zg): a pure-Zerg
// xorshift64* generator whose `uint` state advances IN PLACE through a `mut &` reference.
// Deterministic, so the tests assert reproducibility, in-place advance, and range.

// TestRandRuns checks that next advances the state in place (two draws differ), that the
// same seed reproduces its first value, and that below stays in range over many draws.
func TestRandRuns(t *testing.T) {
	got := runProgramRT(t, "import \"rand\"\n"+
		"fn main() {\n"+
		"\tmut g := rand.seed(42)\n"+
		"\ta := rand.next(g)\n"+
		"\tb := rand.next(g)\n"+
		"\tprint a != b\n"+ // g advanced in place
		"\tmut h := rand.seed(42)\n"+
		"\tprint rand.next(h) == a\n"+ // determinism
		"\tmut s := rand.seed(7)\n"+
		"\tmut ok := true\n"+
		"\tmut i := 0\n"+
		"\tfor i < 20000 {\n"+
		"\t\td := rand.below(s, 6)\n"+ // advances s each call
		"\t\tif d < 0 { ok = false }\n"+
		"\t\tif d >= 6 { ok = false }\n"+
		"\t\ti = i + 1\n"+
		"\t}\n"+
		"\tprint ok\n"+ // every draw in [0, 6)
		"}\n")
	if want := "true\ntrue\ntrue\n"; got != want {
		t.Fatalf("rand: got %q, want %q", got, want)
	}
}

// TestRandLowering checks that next's `mut &` state is passed BY ADDRESS through the
// bundled-namespace call — the fix that lets stdlib functions take `mut &` parameters.
func TestRandLowering(t *testing.T) {
	code, _, diags := Compile("import \"rand\"\n" +
		"fn main() {\n\tmut g := rand.seed(1)\n\tprint rand.next(g)\n\tprint rand.below(g, 10)\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !strings.Contains(code, "zg_rand__next(&") {
		t.Fatalf("rand.next's mut& state should pass by address:\n%s", code)
	}
	for _, want := range []string{"zg_rand__seed(", "zg_rand__next(", "zg_rand__below("} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
}
