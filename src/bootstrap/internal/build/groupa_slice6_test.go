package build

import (
	"strings"
	"testing"
)

// --- SLICE 6: A9 strong typedef `type X = Y` (newtype) ------------------------

// TestNewtypeConversionsRun covers A9 (revised): a non-generic `type Id = int` is a
// DISTINCT nominal type (a newtype) that lowers to its underlying int. Both explicit
// conversion directions work — `Id(x)` constructs one and `int(id)` extracts it —
// and the value round-trips through a parameter and return of type Id.
func TestNewtypeConversionsRun(t *testing.T) {
	got := runProgram(t, "type Id = int\n"+
		"fn f(a: Id) -> Id {\n\treturn a\n}\n"+
		"fn main() {\n\tx: Id = Id(5)\n\tprint int(f(x))\n\tprint int(Id(42))\n}\n")
	if got != "5\n42\n" {
		t.Fatalf("newtype program = %q, want %q", got, "5\n42\n")
	}
}

// TestNewtypeDistinctRejected pins the identity difference: an int is not an Id and an
// Id is not an int, so a bare assignment either way is a type error — a value only
// crosses the boundary through an explicit conversion.
func TestNewtypeDistinctRejected(t *testing.T) {
	for _, src := range []string{
		"type Id = int\nfn main() {\n\tx: Id = 5\n\tprint int(x)\n}\n",
		"type Id = int\nfn main() {\n\ty: int = Id(5)\n\tprint y\n}\n",
	} {
		if _, _, diags := Compile(src); len(diags) == 0 {
			t.Fatalf("expected a distinct-type diagnostic for: %s", src)
		}
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
