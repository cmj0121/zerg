package build

import (
	"strings"
	"testing"
)

// RUN-based tests for the compile-time built-ins sizeof[T] / alignof[T] — a type's byte
// size / alignment as a uint, the one built-in that genuinely needs compiler knowledge.

// TestSizeofRuns covers primitives and a struct that is NEVER otherwise instantiated (so
// the mono pass must still emit its C type), and that the result is an ordinary uint.
func TestSizeofRuns(t *testing.T) {
	got := runProgramRT(t, "struct Point { pub x: int; pub y: int }\n"+
		"fn main() {\n"+
		"\tprint sizeof[int]\n"+ // 8
		"\tprint sizeof[byte]\n"+ // 1
		"\tprint sizeof[float]\n"+ // 8
		"\tprint sizeof[Point]\n"+ // 16 — an un-instantiated struct
		"\tprint alignof[int]\n"+ // 8
		"\tprint alignof[byte]\n"+ // 1
		"\tprint sizeof[int] + sizeof[byte]\n"+ // 9 — usable in arithmetic
		"}\n")
	if want := "8\n1\n8\n16\n8\n1\n9\n"; got != want {
		t.Fatalf("sizeof/alignof: got %q, want %q", got, want)
	}
}

// TestSizeofLowering pins the emitted C: sizeof[T] / alignof[T] lower to C's sizeof /
// _Alignof of the mapped C type.
func TestSizeofLowering(t *testing.T) {
	code, _, diags := Compile("fn main() {\n\tprint sizeof[int]\n\tprint alignof[float]\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{"sizeof(int64_t)", "_Alignof(double)"} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
}

// TestSizeofShadowed keeps the built-in overridable: a user binding named `sizeof` wins,
// so `sizeof[i]` is then an ordinary index, not the built-in.
func TestSizeofShadowed(t *testing.T) {
	got := runProgramRT(t, "fn main() {\n"+
		"\tsizeof := [10, 20, 30]\n"+
		"\tprint sizeof[1]\n"+ // an index into the user's list, not the built-in
		"}\n")
	if want := "20\n"; got != want {
		t.Fatalf("shadowed sizeof: got %q, want %q", got, want)
	}
}
