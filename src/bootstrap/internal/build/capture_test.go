package build

import (
	"strings"
	"testing"
)

// RUN-based tests for CAPTURING closures (docs/functions.md): a closure captures its
// free variables by copy — the fields of the scope-owned closure struct — so it can
// escape its defining scope and never dangle. Each program is compiled, linked, and
// executed for exact stdout.

// TestCapturingClosureRuns covers a closure that captures an enclosing immutable local
// by copy.
func TestCapturingClosureRuns(t *testing.T) {
	got := runProgramRT(t, "fn apply(f: fn(int) -> int, x: int) -> int {\n\treturn f(x)\n}\n"+
		"fn main() {\n"+
		"\tbase := 100\n"+
		"\tprint apply(fn(n: int) -> int { return n + base }, 5)\n}\n")
	if want := "105\n"; got != want {
		t.Fatalf("capturing closure: got %q, want %q", got, want)
	}
}

// TestCapturingClosureEscapesRuns is the definitive test: a closure that captures a
// local and ESCAPES its defining scope (returned from a factory) still works, because
// the capture is copied into a heap environment and can never dangle. Two instances
// keep independent captures.
func TestCapturingClosureEscapesRuns(t *testing.T) {
	got := runProgramRT(t, "fn make_adder(n: int) -> fn(int) -> int {\n"+
		"\treturn fn(x: int) -> int { return x + n }\n}\n"+
		"fn main() {\n"+
		"\tadd5 := make_adder(5)\n"+
		"\tadd10 := make_adder(10)\n"+
		"\tprint add5(100)\n\tprint add10(100)\n\tprint add5(0)\n}\n")
	if want := "105\n110\n5\n"; got != want {
		t.Fatalf("escaping closure: got %q, want %q", got, want)
	}
}

// TestCapturingMultipleRuns covers capturing several variables, and a str capture (a
// pointer, POD like the numbers).
func TestCapturingMultipleRuns(t *testing.T) {
	got := runProgramRT(t, "fn run(f: fn() -> int) -> int { return f() }\n"+
		"fn greet(f: fn() -> str) -> str { return f() }\n"+
		"fn main() {\n"+
		"\ta := 10\n\tb := 20\n\tc := 30\n"+
		"\tprint run(fn() -> int { return a + b + c })\n"+
		"\tprefix := \"hi, \"\n"+
		"\tprint greet(fn() -> str { return prefix + \"there\" })\n}\n")
	if want := "60\nhi, there\n"; got != want {
		t.Fatalf("multi/str capture: got %q, want %q", got, want)
	}
}

// TestCapturingWithLocalMutationRuns covers the docs example: a capture is immutable,
// but a local seeded from it can be mutated inside the body.
func TestCapturingWithLocalMutationRuns(t *testing.T) {
	got := runProgramRT(t, "fn run(f: fn(int) -> int) -> int { return f(3) }\n"+
		"fn main() {\n\tbase := 100\n"+
		"\tprint run(fn(req: int) -> int {\n\t\tmut acc := base\n\t\tacc = acc + req\n\t\treturn acc\n\t})\n}\n")
	if want := "103\n"; got != want {
		t.Fatalf("capture + local mutation: got %q, want %q", got, want)
	}
}

// TestCapturingClosureLowering pins the emitted C: a per-closure environment struct, a
// heap-allocated box filled with the captures, and a body that reads a capture through
// the env pointer.
func TestCapturingClosureLowering(t *testing.T) {
	code, _, diags := Compile("fn apply(f: fn(int) -> int, x: int) -> int { return f(x) }\n" +
		"fn main() {\n\tbase := 100\n\tprint apply(fn(n: int) -> int { return n + base }, 5)\n}\n")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{
		"} zg_env_lambda_0;",                   // the environment struct
		"zrt_alloc(sizeof(zg_env_lambda_0))",   // the heap box
		"_e->zg_base =",                        // a capture copied in
		"((zg_env_lambda_0 *)zg_env)->zg_base", // a body read through env
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("emitted C missing %q:\n%s", want, code)
		}
	}
}
