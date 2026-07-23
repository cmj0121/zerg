package build

import "testing"

// --- SLICE 7: A10 spec provided/default methods -------------------------------

// TestSpecProvidedMethodRuns covers A10: a spec supplies a default method body
// (`twice`) that an impl does NOT override, and it is called through a `#[dyn]`
// function. Before the fix the witness table skipped the provided slot (leaving the
// struct and the global misaligned, dispatching through a null slot); now the default
// body is built as an erased-receiver instance whose abstract `This` resolves to the
// concrete type, so `run(Wrap(5))` runs `this.value() * 2` = 10.
func TestSpecProvidedMethodRuns(t *testing.T) {
	got := runProgram(t, "spec Show {\n"+
		"\tfn value() -> int\n"+
		"\tfn twice() -> int {\n\t\treturn this.value() * 2\n\t}\n"+
		"}\n"+
		"struct Wrap {\n\tn: int\n}\n"+
		"impl Show for Wrap {\n\tfn value() -> int {\n\t\treturn this.n\n\t}\n}\n"+
		"#[dyn]\nfn run[T: Show](x: T) -> int {\n\treturn x.twice()\n}\n"+
		"fn main() {\n\tprint run(Wrap(5))\n}\n")
	if got != "10\n" {
		t.Fatalf("spec provided-method dispatch = %q, want %q", got, "10\n")
	}
}
