package main

import (
	"os/exec"
	"strings"
	"testing"
)

// The Phase 1f U3 (Format) + U6 (testing) end-to-end demos: an f-string with a
// `:spec` renders formatted text through the built-in Format impls, and the bundled
// `testing` module's assertions compile, pass, and make a failure observable. Both
// build through the ASan harness (buildASan / asanAvailable, shared with the other
// e2e tests) so the runtime string helpers are exercised under the sanitizer.

// TestFStrFormatSpec is the formatted-f-string demo: width, alignment, zero-pad, an
// alternate-form hex, a right-aligned string, an explicit sign, and a float
// precision — each a `{x:spec}` hole lowering to its type's Format impl.
func TestFStrFormatSpec(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	if !asanAvailable(t, cc) {
		t.Skip("toolchain cannot link the sanitizer runtime")
	}
	const src = "fn main() -> Result[nil] {\n" +
		"  n := 42\n" +
		"  s := \"hi\"\n" +
		"  pi := 3.14159\n" +
		"  print f\"[{n:5}]\"\n" + // right-align width 5 (numbers)
		"  print f\"[{n:<5}]\"\n" + // left-align
		"  print f\"[{n:05}]\"\n" + // zero-pad
		"  print f\"hex={n:#x}\"\n" + // alt-form hex
		"  print f\"[{s:>6}] {n} {s}\"\n" + // string right-align + plain holes
		"  print f\"{n:+}\"\n" + // explicit sign
		"  print f\"pi={pi:.2f}\"\n" + // float precision
		"}"
	bin := buildASan(t, cc, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	want := "[   42]\n[42   ]\n[00042]\nhex=0x2a\n[    hi] 42 hi\n+42\npi=3.14\n"
	if stdout != want {
		t.Fatalf("f-string format\n got: %q\nwant: %q", stdout, want)
	}
}

// TestTestingAssertPasses checks the bundled `testing` module: a satisfied
// assert_eq/assert is `nil` (Ok), so `!` unwraps it and the program runs on.
func TestTestingAssertPasses(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	if !asanAvailable(t, cc) {
		t.Skip("toolchain cannot link the sanitizer runtime")
	}
	const src = "import \"testing\"\n" +
		"fn main() -> Result[nil] {\n" +
		"  testing.assert_eq(2 + 2, 4)!\n" +
		"  testing.assert(2 < 3)!\n" +
		"  testing.assert_ne(1, 2)!\n" +
		"  print \"all passed\"\n" +
		"}"
	bin := buildASan(t, cc, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "all passed\n" {
		t.Fatalf("assert pass = %q, want %q", stdout, "all passed\n")
	}
}

// TestTestingAssertFailAborts checks a violated assertion is observable: an uncaught
// failing assert_eq raises, so the program exits non-zero and reports the message.
func TestTestingAssertFailAborts(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	if !asanAvailable(t, cc) {
		t.Skip("toolchain cannot link the sanitizer runtime")
	}
	const src = "import \"testing\"\n" +
		"fn main() -> Result[nil] {\n" +
		"  testing.assert_eq(1, 2)!\n" +
		"  print \"unreached\"\n" +
		"}"
	bin := buildASan(t, cc, src)
	stdout, stderr, err := run(t, bin)
	if err == nil {
		t.Fatalf("a failing assert must exit non-zero; stdout=%q", stdout)
	}
	if strings.Contains(stdout, "unreached") {
		t.Fatalf("the statement after a failing assert must not run; stdout=%q", stdout)
	}
	if !strings.Contains(stderr, "assert_eq failed") {
		t.Fatalf("stderr = %q, want the assert_eq failure message", stderr)
	}
}

// TestTestingAssertGuardCatches checks a failing assertion is recoverable as a value:
// wrapped in a `guard`, its raise is demoted to a Right, so the program continues.
func TestTestingAssertGuardCatches(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	if !asanAvailable(t, cc) {
		t.Skip("toolchain cannot link the sanitizer runtime")
	}
	const src = "import \"testing\"\n" +
		"fn checked() -> Result[int] {\n" +
		"  testing.assert_eq(1, 2)!\n" + // raises
		"  return 1\n" +
		"}\n" +
		"fn main() -> Result[nil] {\n" +
		"  g := guard { checked()! }\n" + // guard catches the raise
		"  print (g ?? -1)\n" + // -1: the caught failure
		"}"
	bin := buildASan(t, cc, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "-1\n" {
		t.Fatalf("guard-caught assert = %q, want %q", stdout, "-1\n")
	}
}
