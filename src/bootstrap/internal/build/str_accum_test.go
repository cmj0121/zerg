package build

import "testing"

// Regression tests for str accumulation through self-referential reassignment —
// `s = s + x`. The reassign lowering used to release the old cell BEFORE evaluating
// the RHS, so the concat then read a freed cell (a use-after-free that silently lost
// the accumulator: `s = s + "b"` twice printed "c", a loop printed only the last part).
// runProgramRTBalanced also asserts alloc/free balance, so a leak in the fixed order
// (retain-new -> release-old -> store) fails too.

// TestStrAccumReassign covers the direct case: two self-referential concats keep both.
func TestStrAccumReassign(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n"+
		"\tmut j := \"a\"\n"+
		"\tj = j + \"b\"\n"+
		"\tj = j + \"c\"\n"+
		"\tprint j\n"+
		"}\n")
	if want := "abc\n"; got != want {
		t.Fatalf("str accum reassign: got %q, want %q", got, want)
	}
}

// TestStrAccumLoopLiteral builds a str by concatenating a literal each iteration.
func TestStrAccumLoopLiteral(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n"+
		"\tmut j := \"\"\n"+
		"\tmut i := 0\n"+
		"\tfor i < 3 {\n"+
		"\t\tj = j + \"x\"\n"+
		"\t\ti = i + 1\n"+
		"\t}\n"+
		"\tprint j\n"+
		"}\n")
	if want := "xxx\n"; got != want {
		t.Fatalf("str accum loop (literal): got %q, want %q", got, want)
	}
}

// TestStrAccumLoopIndex accumulates from a list index each iteration (the join shape).
func TestStrAccumLoopIndex(t *testing.T) {
	got := runProgramRTBalanced(t, "fn main() {\n"+
		"\tmut j := \"\"\n"+
		"\tp := [\"a\", \"b\", \"c\"]\n"+
		"\tmut i := 0\n"+
		"\tfor i < 3 {\n"+
		"\t\tj = j + p[i]\n"+
		"\t\ti = i + 1\n"+
		"\t}\n"+
		"\tprint j\n"+
		"}\n")
	if want := "abc\n"; got != want {
		t.Fatalf("str accum loop (index): got %q, want %q", got, want)
	}
}
