package build

import (
	"strings"
	"testing"
)

// --- SLICE 6: A9 transparent type alias `type X = Y` --------------------------

// TestTypeAliasRuns covers A9: a non-generic alias `type Id = int` is transparent —
// every use resolves to the underlying type, so a parameter, return, and annotated
// binding of type Id are all plain int. Before the fix the alias resolved to Unknown
// and lowered to a `void` C type.
func TestTypeAliasRuns(t *testing.T) {
	got := runProgram(t, "type Id = int\n"+
		"fn f(a: Id) -> Id {\n\treturn a\n}\n"+
		"fn main() {\n\tx: Id = 5\n\tprint f(x)\n}\n")
	if got != "5\n" {
		t.Fatalf("type-alias program = %q, want %q", got, "5\n")
	}
}

// TestGenericTypeAliasGate covers the FORK-A9 cut: a generic type alias is not yet
// supported, so USING one must fail cleanly.
func TestGenericTypeAliasGate(t *testing.T) {
	src := "type Box[T] = T\nfn f(a: Box[int]) -> int { return a }\nfn main() { print f(3) }\n"
	code, _, diags := Compile(src)
	if len(diags) == 0 || code != "" {
		t.Fatalf("a generic type alias should be gated, got code=%q diags=%v", code, diags)
	}
	if !strings.Contains(diags[0].Msg, "generic type alias is not yet supported") {
		t.Fatalf("expected the generic-alias gate diagnostic, got: %v", diags)
	}
}
