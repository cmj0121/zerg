package build

import "testing"

// --- SLICE 5: A5 default params + field defaults (FORK-A5 tail-constant) -------

// TestDefaultParamRuns covers A5: a call omitting a trailing defaulted parameter
// backfills the default constant. `f(1)` must pass b=10, so the body `return b`
// yields 10 (before the fix the emitter passed too few C arguments).
func TestDefaultParamRuns(t *testing.T) {
	got := runProgram(t, "fn f(a: int, b: int = 10) -> int {\n\treturn b\n}\n"+
		"fn main() { print f(1) }\n")
	if got != "10\n" {
		t.Fatalf("default-param call = %q, want %q", got, "10\n")
	}
}

// TestDefaultParamOverridden guards that an explicitly supplied argument still wins
// over the default (no spurious backfill of a provided parameter).
func TestDefaultParamOverridden(t *testing.T) {
	got := runProgram(t, "fn f(a: int, b: int = 10) -> int {\n\treturn a + b\n}\n"+
		"fn main() {\n\tprint f(1)\n\tprint f(1, 5)\n}\n")
	if got != "11\n6\n" {
		t.Fatalf("default-param override = %q, want %q", got, "11\n6\n")
	}
}

// TestFieldDefaultRuns covers A5: a struct construction omitting a trailing defaulted
// field backfills its default constant. `S(x: 5)` must set y=100.
func TestFieldDefaultRuns(t *testing.T) {
	got := runProgram(t, "struct S { pub x: int; pub y: int = 100 }\n"+
		"fn main() {\n\ts := S(x: 5)\n\tprint s.x\n\tprint s.y\n}\n")
	if got != "5\n100\n" {
		t.Fatalf("field-default construction = %q, want %q", got, "5\n100\n")
	}
}
