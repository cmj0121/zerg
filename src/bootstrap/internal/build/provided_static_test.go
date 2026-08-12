package build

import "testing"

// RUN-based tests for a spec's PROVIDED (default) method resolved on a CONCRETE receiver
// through the STATIC (monomorphized) path (GRAMMAR group 7). An impl that omits a method
// the spec provides with a default body inherits that body; before the fix such a call
// reached cc as a null callee `0()`. The `#[dyn]` witness path (groupa_slice7 / FIX 3)
// already handled provided methods and stays unregressed.

// TestProvidedMethodStaticRuns: `impl Greet for Foo {}` (no override) inherits the spec's
// provided `hello`, callable on a concrete `Foo` value.
func TestProvidedMethodStaticRuns(t *testing.T) {
	got := runProgram(t, "spec Greet {\n"+
		"\tfn hello() -> str { return \"hi\" }\n"+
		"}\n"+
		"struct Foo {\n\tpub n: int\n}\n"+
		"impl Greet for Foo {}\n"+
		"fn main() {\n\tf := Foo(1)\n\tprint f.hello()\n}\n")
	if got != "hi\n" {
		t.Fatalf("provided default on concrete receiver = %q, want %q", got, "hi\n")
	}
}

// TestProvidedMethodOverrideWins: an impl that DOES override the provided method calls the
// override, not the spec default.
func TestProvidedMethodOverrideWins(t *testing.T) {
	got := runProgram(t, "spec Greet {\n"+
		"\tfn hello() -> str { return \"hi\" }\n"+
		"}\n"+
		"struct Bar {\n\tpub n: int\n}\n"+
		"impl Greet for Bar {\n\tfn hello() -> str { return \"override\" }\n}\n"+
		"fn main() {\n\tb := Bar(2)\n\tprint b.hello()\n}\n")
	if got != "override\n" {
		t.Fatalf("impl override should win = %q, want %q", got, "override\n")
	}
}

// TestProvidedMethodGenericBoundRuns: a generic function bounded by the spec (non-#[dyn],
// so monomorphized) resolves the provided default for its concrete type argument.
func TestProvidedMethodGenericBoundRuns(t *testing.T) {
	got := runProgram(t, "spec Greet {\n"+
		"\tfn hello() -> str { return \"hi\" }\n"+
		"}\n"+
		"struct Foo {\n\tpub n: int\n}\n"+
		"impl Greet for Foo {}\n"+
		"fn greet[T: Greet](x: T) -> str {\n\treturn x.hello()\n}\n"+
		"fn main() {\n\tprint greet(Foo(1))\n}\n")
	if got != "hi\n" {
		t.Fatalf("provided default via generic bound = %q, want %q", got, "hi\n")
	}
}

// TestProvidedMethodCallsProvidedStaticRuns: a provided default that calls ANOTHER
// provided default on `this` resolves both on the static path for a concrete receiver
// (the self-call dispatches to the second default specialized for the type).
func TestProvidedMethodCallsProvidedStaticRuns(t *testing.T) {
	got := runProgram(t, "spec Show {\n"+
		"\tfn value() -> int\n"+
		"\tfn twice() -> int { return this.value() * 2 }\n"+
		"\tfn quad() -> int { return this.twice() * 2 }\n"+
		"}\n"+
		"struct Wrap {\n\tpub n: int\n}\n"+
		"impl Show for Wrap {\n\tfn value() -> int { return this.n }\n}\n"+
		"fn main() {\n\tw := Wrap(5)\n\tprint w.quad()\n}\n")
	if got != "20\n" {
		t.Fatalf("provided->provided static dispatch = %q, want %q", got, "20\n")
	}
}
