package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAsmAddCompileAndRun is the Phase 1h U3 end-to-end oracle: an inline `asm`
// integer add inside an `unsafe { }` block-expression must lower to a GCC
// extended-asm statement, compile, and run — computing 3 + 4 = 7 in a register and
// observing the result.
func TestAsmAddCompileAndRun(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	src := "fn add_asm(a: int, b: int) -> int {\n" +
		"\tr: int = 0\n" +
		"\tunsafe {\n" +
		"\t\tasm(\"add %0, %1, %2\", out(\"=r\") r, in(\"r\") a, in(\"r\") b)\n" +
		"\t}\n" +
		"\treturn r\n" +
		"}\n" +
		"fn main() {\n" +
		"\tprint add_asm(3, 4)\n" +
		"}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if got := compileAndRun(t, cc, code); got != "7\n" {
		t.Fatalf("asm add: got %q, want %q", got, "7\n")
	}
}

// TestAsmLoweringShape checks the U3 lowering shape: an `asm` lowers to
// `__asm__ __volatile__(<template> : <outputs> : <inputs> : <clobbers>)` wrapped in a
// statement-expression yielding 0, with the template and constraint strings passed
// through verbatim and operands mapped output-then-input.
func TestAsmLoweringShape(t *testing.T) {
	src := "fn add_asm(a: int, b: int) -> int {\n" +
		"\tr: int = 0\n" +
		"\tunsafe {\n" +
		"\t\tasm(\"add %0, %1, %2\", out(\"=r\") r, in(\"r\") a, in(\"r\") b)\n" +
		"\t}\n" +
		"\treturn r\n" +
		"}\n" +
		"fn main() { print add_asm(1, 2) }\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	want := "({ __asm__ __volatile__(\"add %0, %1, %2\" : \"=r\"(zg_r) : \"r\"(zg_a), \"r\"(zg_b) : ); 0; })"
	if !strings.Contains(code, want) {
		t.Fatalf("emitted C missing extended-asm shape %q:\n%s", want, code)
	}
}

// TestAsmClobberLowering checks that `clobber(...)` registers land in the fourth
// extended-asm slot as a comma-separated list of verbatim string literals.
func TestAsmClobberLowering(t *testing.T) {
	src := "fn touch() {\n" +
		"\tunsafe {\n" +
		"\t\tr: int = 0\n" +
		"\t\tasm(\"mov %0, #1\", out(\"=r\") r, clobber(\"x9\", \"x10\"))\n" +
		"\t}\n" +
		"}\n" +
		"fn main() { touch() }\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	want := "\"=r\"(zg_r) :  : \"x9\", \"x10\""
	if !strings.Contains(code, want) {
		t.Fatalf("emitted C missing clobber slot %q:\n%s", want, code)
	}
}

// TestAsmRejectedInSafeCode is the U3 trust-boundary diagnostic: inline asm is a raw
// operation, so using it outside an `unsafe { }` context is rejected cleanly.
func TestAsmRejectedInSafeCode(t *testing.T) {
	src := "fn add_asm(a: int, b: int) -> int {\n" +
		"\tr: int = 0\n" +
		"\tasm(\"add %0, %1, %2\", out(\"=r\") r, in(\"r\") a, in(\"r\") b)\n" +
		"\treturn r\n" +
		"}\n" +
		"fn main() { print add_asm(1, 2) }\n"
	_, _, diags := Compile(src)
	if len(diags) == 0 {
		t.Fatalf("inline asm outside `unsafe` should be rejected")
	}
	if !strings.Contains(diags[0].Msg, "inline asm is unsafe") {
		t.Fatalf("asm diagnostic should point at the unsafe boundary, got: %v", diags[0].Msg)
	}
}

// TestUnsafeConformanceCorpus is the Phase 1h Slice D conformance corpus: one build
// (testdata/1h_unsafe) that exercises every raw operation — addr/store/load/offset, a
// pointer-cast round trip, a null test, a mutable global mutated by an unsafe group
// fn, and an inline-asm add — and must compile and run with the expected output.
func TestUnsafeConformanceCorpus(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	entry := filepath.Join("testdata", "1h_unsafe", "main.zg")
	src, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	code, _, diags := Compile(string(src))
	if len(diags) != 0 {
		t.Fatalf("unsafe conformance corpus should compile, got diagnostics: %v", diags)
	}
	const want = "20\n20\n20\nfalse\n1\n2\n7\n"
	if got := compileAndRun(t, cc, code); got != want {
		t.Fatalf("unsafe conformance corpus run: got %q, want %q", got, want)
	}
}
