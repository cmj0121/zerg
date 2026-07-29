package build

import (
	"strings"
	"testing"
)

// TestIfBindPresentRuns covers the `if x := opt { … }` binding head (GRAMMAR group 6):
// the block runs — with x bound to the unwrapped value — only when the optional is
// present (Some), and is skipped when it is absent (None). An else-branch does not see x.
func TestIfBindPresentRuns(t *testing.T) {
	src := "fn find(k: int) -> int? {\n" +
		"\treturn 42 if k == 1\n" +
		"\treturn nil\n" +
		"}\n" +
		"fn main() {\n" +
		"\tif x := find(1) {\n\t\tprint x\n\t}\n" +
		"\tif y := find(2) {\n\t\tprint y\n\t} else {\n\t\tprint -1\n\t}\n" +
		"}\n"
	if got := runProgramRT(t, src); got != "42\n-1\n" {
		t.Fatalf("if-bind statement = %q, want %q", got, "42\n-1\n")
	}
}

// TestIfBindExpressionRuns covers the expression form `y := if x := f() { x } else { 0 }`:
// the if-expression yields the unwrapped value on the present branch and the else value
// otherwise.
func TestIfBindExpressionRuns(t *testing.T) {
	src := "fn find(k: int) -> int? {\n" +
		"\treturn 7 if k == 1\n" +
		"\treturn nil\n" +
		"}\n" +
		"fn main() {\n" +
		"\ta := if x := find(1) { x } else { 0 }\n" +
		"\tb := if x := find(2) { x } else { 0 }\n" +
		"\tprint a\n\tprint b\n" +
		"}\n"
	if got := runProgramRT(t, src); got != "7\n0\n" {
		t.Fatalf("if-bind expression = %q, want %q", got, "7\n0\n")
	}
}

// TestIfBindNonOptionalRejected covers the type-check: an `if x :=` head over a value that
// carries no presence — neither an optional nor a Result — is a clean error (the block only
// runs on the present case, and a plain int has no absent one).
func TestIfBindNonOptionalRejected(t *testing.T) {
	src := "fn main() {\n\tif x := 5 {\n\t\tprint x\n\t}\n}\n"
	code, _, diags := Compile(src)
	if len(diags) == 0 || code != "" {
		t.Fatalf("a non-optional if-bind head should be rejected, got code=%q diags=%v", code, diags)
	}
	joined := ""
	for _, d := range diags {
		joined += d.Msg
		if strings.Contains(d.Msg, "internal:") {
			t.Fatalf("must be a clean gate, got internal error: %v", diags)
		}
	}
	if !strings.Contains(joined, "requires an optional or a Result value") {
		t.Fatalf("expected the presence-carrier diagnostic, got: %v", diags)
	}
}
