package build

import (
	"strings"
	"testing"
)

// --- SLICE 2: A12 guard{void} runtime + A11 void-try binding ------------------

// TestGuardVoidRuns covers A12: a `guard { void_fn() }` yields a Result[nil] spelled
// zrt_result_nil, but the runtime-header trigger previously scanned only signatures,
// so the header was omitted and the C failed to compile ("undeclared zrt_result_nil").
// With ExprTypes/BindTypes now scanned for Result[nil], the header is included and the
// program links and runs: noop() prints 1, the guard completes normally, then 2.
func TestGuardVoidRuns(t *testing.T) {
	got := runProgramRT(t, "fn noop() {\n\tprint 1\n}\n"+
		"fn main() {\n\tg := guard { noop() }\n\tprint 2\n}\n")
	if got != "1\n2\n" {
		t.Fatalf("guard{void} program = %q, want %q", got, "1\n2\n")
	}
}

// TestVoidTryBindDiagnostic covers A11: `y := boom()?` where boom yields Result[nil]
// unwraps to a nil/void value with no C storage, so an inferred binding to it must be
// rejected with a clean diagnostic rather than emit a `void zg_y`.
func TestVoidTryBindDiagnostic(t *testing.T) {
	src := "fn boom() -> Result[nil] {\n\traise \"x\"\n}\n" +
		"fn main() -> Result[nil] {\n\ty := boom()?\n\tprint 0\n}\n"
	code, _, diags := Compile(src)
	if len(diags) == 0 || code != "" {
		t.Fatalf("a void-try binding should be rejected, got code=%q diags=%v", code, diags)
	}
	if !strings.Contains(diags[0].Msg, "cannot bind a value of type nil") {
		t.Fatalf("expected the void-try-bind diagnostic, got: %v", diags)
	}
}

// TestBareVoidTryRuns is the A11 regression guard: a bare `boom()?` statement (no
// binding) must still propagate — the gate targets only the inferred binding form.
func TestBareVoidTryRuns(t *testing.T) {
	got := runProgramRT(t, "fn boom() -> Result[nil] {\n\tprint 1\n}\n"+
		"fn use() -> Result[nil] {\n\tboom()?\n\tprint 2\n}\n"+
		"fn main() {\n\tuse()!\n\tprint 3\n}\n")
	if got != "1\n2\n3\n" {
		t.Fatalf("bare void-try program = %q, want %q", got, "1\n2\n3\n")
	}
}
