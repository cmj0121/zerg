package build

import "testing"

// --- named arguments ------------------------------------------------------------
//
// A named argument selects its parameter BY NAME (docs/code/functions.md): positional
// arguments fill left to right, any parameter may instead be given by name, a defaulted
// one may be omitted, and once an argument is named the rest must be named too.
//
// Sema bound them correctly from the day it was written, and the EMITTER read `n.Args`
// positionally and never looked at `a.Name` — so `f(b: 1, a: 5)` emitted `zg_f(1, 5)` and
// the arguments arrived swapped, with no diagnostic anywhere. It was a form the docs
// specify, no chapter marked it a deviation, and the answer was simply wrong whenever the
// names were not already in declaration order.

// TestNamedArgsReorder is the case that was wrong: the names are in the opposite order to
// the parameters, so a positional lowering computes 1-5 where the program says 5-1.
func TestNamedArgsReorder(t *testing.T) {
	got := runProgram(t, "fn f(a: int, b: int) -> int {\n\treturn a - b\n}\n"+
		"fn main() {\n\tprint f(b: 1, a: 5)\n\tprint f(5, 1)\n}\n")
	if got != "4\n4\n" {
		t.Fatalf("named-argument call = %q, want %q", got, "4\n4\n")
	}
}

// TestNamedArgSkipsADefault is what naming an argument is FOR — reaching past a defaulted
// parameter in the middle without restating it. `g(1, c: 9)` leaves b at its default.
func TestNamedArgSkipsADefault(t *testing.T) {
	got := runProgram(t, "fn g(a: int, b: int = 5, c: int = 7) -> int {\n\treturn a * 100 + b * 10 + c\n}\n"+
		"fn main() {\n\tprint g(1, c: 9)\n\tprint g(1)\n\tprint g(1, 2, 3)\n}\n")
	if got != "159\n157\n123\n" {
		t.Fatalf("named argument past a default = %q, want %q", got, "159\n157\n123\n")
	}
}

// TestNamedFieldsReorder is the same rule for a struct construction, which had the same
// positional loop: `P(y: 2, x: 1)` built `{2, 1}`.
func TestNamedFieldsReorder(t *testing.T) {
	got := runProgram(t, "struct P {\n\tx: int\n\ty: int\n}\n"+
		"fn main() {\n\tp := P(y: 2, x: 1)\n\tprint p.x\n\tprint p.y\n}\n")
	if got != "1\n2\n" {
		t.Fatalf("named-field construction = %q, want %q", got, "1\n2\n")
	}
}

// TestPositionalCallIsUnchanged pins the other half: a call that names nothing takes the
// original path, so every program that worked before still emits the same C.
func TestPositionalCallIsUnchanged(t *testing.T) {
	got := runProgram(t, "fn f(a: int, b: int) -> int {\n\treturn a - b\n}\n"+
		"fn main() { print f(9, 2) }\n")
	if got != "7\n" {
		t.Fatalf("positional call = %q, want %q", got, "7\n")
	}
}
