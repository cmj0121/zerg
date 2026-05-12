package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.13 Unit 4 — cgen lowering tests for inline asm.
//
// The build half of TestE2EV13Corpus already covers end-to-end emit +
// assemble + execute. The tests here pin two cgen-specific contracts that
// the corpus alone cannot pin reliably:
//
//   1. PLAN pin 7's full caller-saved arm64 clobber set appears in every
//      emitted __asm__ volatile. A regression that drops a register from
//      the list would not surface in the corpus until a real program
//      happens to read that register across an asm boundary — too late.
//      The test asserts every register name from the spec is present in
//      the emit.
//
//   2. `${name}` interpolation lowers to `%N` operand placeholders with
//      the matching `"r"(expr)` input operand. The expr form depends on
//      type: byte → uint64_t cast of the binding, list[byte] → uintptr_t
//      cast of `.data`. Both shapes are checked.
// ---------------------------------------------------------------------------

// emitV13Asm builds the entry file at v0.13 and returns the emitted C
// source. Mirrors emitFromFile from cgen_v08_test.go but emits the
// requires-marker as part of the source so the lexer admits asm.
func emitV13Asm(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.zg")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatalf("write main.zg: %v", err)
	}
	out, err := emitFromFile(t, entry)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return out
}

func TestV13CgenAsmClobberSet(t *testing.T) {
	// Minimal asm-bearing program: a single empty asm block is enough
	// because the clobber list is constant — every block emits the same
	// set, regardless of body content or interp count.
	out := emitV13Asm(t, `# requires: v0.13
asm {
	nop
}
`)
	// PLAN pin 7's full set. Order in the emit follows the spec ordering
	// so a side-by-side diff stays readable; the test checks every name
	// is present rather than asserting the exact order.
	want := []string{`"memory"`, `"cc"`}
	for i := 0; i <= 17; i++ {
		want = append(want, `"x`+itoa(i)+`"`)
	}
	want = append(want, `"x30"`)
	for i := 0; i <= 7; i++ {
		want = append(want, `"v`+itoa(i)+`"`)
	}
	for i := 16; i <= 31; i++ {
		want = append(want, `"v`+itoa(i)+`"`)
	}
	for _, name := range want {
		if !strings.Contains(out, name) {
			t.Errorf("clobber list missing %s in emit:\n%s", name, out)
		}
	}
	// x29 is the user-preserve frame pointer (pin 8). It must NOT appear
	// in the clobber list — a regression that adds it would silently
	// break stack unwinding inside debuggers without any other signal.
	if strings.Contains(out, `"x29"`) {
		t.Errorf("clobber list erroneously contains x29 (user-preserve fp); emit:\n%s", out)
	}
}

func TestV13CgenAsmInterpListByteLowering(t *testing.T) {
	// `${msg}` with msg: list[byte] should produce a `%0` placeholder
	// plus an input operand reading msg.data cast to uintptr_t. The
	// uintptr_t cast widens to a register-sized type so the "r"
	// constraint can pick any GPR width without partial-write surprises.
	out := emitV13Asm(t, `# requires: v0.13
msg: list[byte] = ['h', 'i']
asm {
	mov x1, ${msg}
}
`)
	if !strings.Contains(out, "mov x1, %0") {
		t.Errorf("expected 'mov x1, %%0' in asm template; got:\n%s", out)
	}
	if !strings.Contains(out, `"r"(((uintptr_t)z_msg.data))`) {
		t.Errorf("expected list[byte] operand 'r'(((uintptr_t)z_msg.data)); got:\n%s", out)
	}
}

func TestV13CgenAsmInterpByteLowering(t *testing.T) {
	out := emitV13Asm(t, `# requires: v0.13
b := 'A'
asm {
	mov x0, ${b}
}
`)
	if !strings.Contains(out, "mov x0, %0") {
		t.Errorf("expected 'mov x0, %%0' in asm template; got:\n%s", out)
	}
	if !strings.Contains(out, `"r"(((uint64_t)z_b))`) {
		t.Errorf("expected byte operand 'r'(((uint64_t)z_b)); got:\n%s", out)
	}
}

func TestV13CgenAsmTextOnlyHasNoOperands(t *testing.T) {
	// A pure-text body emits no input or output operands; both sections
	// are just the colon delimiter followed by a newline.
	out := emitV13Asm(t, `# requires: v0.13
asm {
	svc #0x80
}
`)
	if !strings.Contains(out, ": /* outputs */\n") {
		t.Errorf("expected empty output section; got:\n%s", out)
	}
	if !strings.Contains(out, ": /* inputs */\n") {
		t.Errorf("expected empty input section; got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// v0.14 — int input operand + mut write-back output operand.
// ---------------------------------------------------------------------------

func TestV14CgenAsmInterpIntInputLowering(t *testing.T) {
	// An immutable int binding lowers as an input operand cast to
	// int64_t (the C type Zerg's `int` maps to). The cast widens to
	// register-size so the "r" constraint can pick any GPR width.
	out := emitV13Asm(t, `# requires: v0.14
n := 5
asm {
	mov x0, ${n}
}
`)
	if !strings.Contains(out, "mov x0, %0") {
		t.Errorf("expected 'mov x0, %%0' in asm template; got:\n%s", out)
	}
	if !strings.Contains(out, `"r"(((int64_t)z_n))`) {
		t.Errorf("expected int input operand 'r'(((int64_t)z_n)); got:\n%s", out)
	}
}

func TestV14CgenAsmMutIntOutputLowering(t *testing.T) {
	// A `mut int` binding lowers as a "+r" inout operand. The C operand
	// expression is the raw lvalue `z_<name>` (no cast wrapper) so GCC
	// can emit load/store around the asm body.
	out := emitV13Asm(t, `# requires: v0.14
mut x: int = 0
asm {
	mov ${x}, #42
}
`)
	if !strings.Contains(out, "mov %0, #42") {
		t.Errorf("expected 'mov %%0, #42' (output placeholder = 0); got:\n%s", out)
	}
	if !strings.Contains(out, `"+r"(z_x)`) {
		t.Errorf("expected mut int output operand '+r'(z_x); got:\n%s", out)
	}
}

func TestV14CgenAsmMixedOutputAndInputNumbering(t *testing.T) {
	// GCC numbers operands outputs-first, inputs-second. With one output
	// `${x}` and one input `${n}`, the output gets %0 and the input gets
	// %1 — independent of source order.
	out := emitV13Asm(t, `# requires: v0.14
mut x: int = 0
n := 7
asm {
	mov ${x}, ${n}
}
`)
	if !strings.Contains(out, "mov %0, %1") {
		t.Errorf("expected 'mov %%0, %%1' (output then input); got:\n%s", out)
	}
	if !strings.Contains(out, `"+r"(z_x)`) {
		t.Errorf("expected output operand '+r'(z_x); got:\n%s", out)
	}
	if !strings.Contains(out, `"r"(((int64_t)z_n))`) {
		t.Errorf("expected input operand 'r'(((int64_t)z_n)); got:\n%s", out)
	}
}

func TestV13CgenAsmPercentEscapedInTemplate(t *testing.T) {
	// `%` is the GCC inline-asm operand-substitution prefix. The body
	// emit must double every literal `%` so it passes through to the
	// assembler unchanged. Use a likely real-world case: arm64 `w` (32-bit
	// register specifier) syntax is `%w0`, so users will probably write
	// `mov w0, %w0` inside asm bodies once register-typed operands ship.
	// For now, pin the rule by including a stray `%` in the body.
	out := emitV13Asm(t, `# requires: v0.13
asm {
	mov w0, %w0
}
`)
	// Look for `mov w0, %%w0` in the emitted template (the double-% is
	// inside a C string literal that itself escapes; cQuote does not
	// re-escape `%`, so the literal substring in the C source is `%%`).
	if !strings.Contains(out, `mov w0, %%w0`) {
		t.Errorf("expected '%%' doubled in asm template; got:\n%s", out)
	}
}

// itoa avoids dragging in strconv just for the clobber-list builder.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
