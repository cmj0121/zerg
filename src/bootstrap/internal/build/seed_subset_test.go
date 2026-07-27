package build

import (
	"strings"
	"testing"
)

// The seed builds exactly the subset src/compiler/*.zg is written in. A program
// outside that subset must be REJECTED, never quietly miscompiled — these pin the
// two spellings that used to slip through as valid-looking C.

// TestNamedFunctionAsValueRejected is the regression: a bare function name used as a
// value lowered to its own mangled symbol bound into a `void` local — `void zg_g =
// zg_f;` — with no diagnostic and exit 0, so the failure only surfaced later as cc
// noise about generated code the author never wrote.
func TestNamedFunctionAsValueRejected(t *testing.T) {
	src := "fn f(x: int) -> int {\n\treturn x + 1\n}\n" +
		"fn main() {\n\tg := f\n\tprint g(1)\n}\n"
	_, _, diags := Compile(src)
	if len(diags) == 0 {
		t.Fatalf("a function used as a value must be rejected, got no diagnostic")
	}
	if !strings.Contains(diags[0].Msg, "function used as a value") {
		t.Fatalf("unexpected diagnostic: %q", diags[0].Msg)
	}
}

// TestNamespaceFunctionAsValueRejected covers the Field spelling of the same thing:
// `mod.f` taken rather than called.
func TestNamespaceFunctionAsValueRejected(t *testing.T) {
	src := "import \"strconv\"\n\n" +
		"fn main() {\n\tg := strconv.to_string\n\tprint g(1, 10)\n}\n"
	_, _, diags := Compile(src)
	if len(diags) == 0 {
		t.Fatalf("a namespace function used as a value must be rejected, got no diagnostic")
	}
}

// TestClosureValueRejected is the sibling the seed already refused; it is pinned here
// so all three spellings of "function value" stay refused together.
func TestClosureValueRejected(t *testing.T) {
	src := "fn main() {\n\tf := fn(x: int) -> int {\n\t\treturn x + 1\n\t}\n\tprint f(1)\n}\n"
	_, _, diags := Compile(src)
	if len(diags) == 0 {
		t.Fatalf("a closure used as a value must be rejected, got no diagnostic")
	}
}
