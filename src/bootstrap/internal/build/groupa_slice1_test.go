package build

import "testing"

// --- SLICE 1: A1 break-if / continue-if ---------------------------------------

// TestBreakContinueIf covers A1: a `break if cond` / `continue if cond` must guard
// the jump (and its loop teardown) on the condition, not jump unconditionally. Before
// the fix the emitter ignored n.Cond and printed a bare `break;`/`continue;`, so this
// loop broke on the first iteration (or hung, depending on ordering). Now it runs to
// completion: i=1 prints, i=2 continues, i=3 prints, i=4 breaks.
func TestBreakContinueIf(t *testing.T) {
	got := runProgram(t, "fn main() {\n"+
		"\tmut i := 0\n"+
		"\tfor {\n"+
		"\t\ti = i + 1\n"+
		"\t\tcontinue if i == 2\n"+
		"\t\tbreak if i > 3\n"+
		"\t\tprint i\n"+
		"\t}\n"+
		"}\n")
	if got != "1\n3\n" {
		t.Fatalf("break-if/continue-if loop = %q, want %q", got, "1\n3\n")
	}
}
