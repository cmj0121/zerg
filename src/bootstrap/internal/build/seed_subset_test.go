package build

import "testing"

// The seed builds exactly the subset src/compiler/*.zg is written in. A program
// outside that subset must be REJECTED, never quietly miscompiled — these pin the
// spellings that used to slip through as valid-looking C.
//
// A NAMED function used as a value is no longer one of them: it lowers (fnvalue_test.go).
// A closure still is, and the difference is capture — a named function is one symbol,
// while a closure is a symbol plus the environment it closed over.

// TestClosureValueRejected pins the one spelling of "function value" that stays refused.
func TestClosureValueRejected(t *testing.T) {
	src := "fn main() {\n\tf := fn(x: int) -> int {\n\t\treturn x + 1\n\t}\n\tprint f(1)\n}\n"
	_, _, diags := Compile(src)
	if len(diags) == 0 {
		t.Fatalf("a closure used as a value must be rejected, got no diagnostic")
	}
}
