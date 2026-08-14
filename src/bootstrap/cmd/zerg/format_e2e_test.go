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

// The two below are `AssertionError` end to end through the SEED — build it, link it, run it.
//
// THREE TESTS OF THE `testing` MODULE STOOD HERE and could not be kept. They exercised
// `assert_eq`, `assert` and `assert_ne`, which are gone: the assertion is the `assert`
// KEYWORD now, and a keyword is exactly what the seed does not have. What is left of the
// module is `assert_raises`, which the seed cannot call either — its argument is a generic
// `Result[T]` and the seed answers _cannot use Either[int, Err] as Either[T, Err]_ at every
// call site, which was true before this change too.
//
// So what a SEED test can still reach of the same subject is the ERROR KIND. That is the one
// piece of `assert` the seed carries, deliberately: the numbering is an ABI shared with the
// runtime, and these two are what hold the seed's table, the emitter's constant and
// `zrt_err_kindname` to each other on a running program rather than by reading three files.

// TestAssertionErrorKindRoundTrips checks that a raised AssertionError comes back as one:
// the kind survives the raise, the guard and the reification, and it is not some other kind.
func TestAssertionErrorKindRoundTrips(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	if !asanAvailable(t, cc) {
		t.Skip("toolchain cannot link the sanitizer runtime")
	}
	const src = "fn boom() -> Result[int] {\n" +
		"  raise AssertionError(\"a claim that did not hold\")\n" +
		"}\n" +
		"fn main() -> Result[nil] {\n" +
		"  match guard { boom()! } {\n" +
		"    Either.Left(v) => { print v }\n" +
		"    Either.Right(e) => {\n" +
		"      print e is AssertionError\n" +
		"      print e is ValueError\n" +
		"      print e.message()\n" +
		"    }\n" +
		"  }\n" +
		"}"
	bin := buildASan(t, cc, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	want := "true\nfalse\na claim that did not hold\n"
	if stdout != want {
		t.Fatalf("AssertionError round-trip = %q, want %q", stdout, want)
	}
}

// TestAssertionErrorAbortsWithItsName checks the other end of the mirror: an uncaught one
// exits non-zero and the RUNTIME names it. That name is built in zrt_err_kindname, from the
// number this package's table hands out — so a kind added here and not there would print
// nothing in front of its message, silently, which is the failure this asserts against.
func TestAssertionErrorAbortsWithItsName(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	if !asanAvailable(t, cc) {
		t.Skip("toolchain cannot link the sanitizer runtime")
	}
	const src = "fn main() -> Result[nil] {\n" +
		"  raise AssertionError(\"a claim that did not hold\")\n" +
		"  print \"unreached\"\n" +
		"}"
	bin := buildASan(t, cc, src)
	stdout, stderr, err := run(t, bin)
	if err == nil {
		t.Fatalf("an uncaught AssertionError must exit non-zero; stdout=%q", stdout)
	}
	if strings.Contains(stdout, "unreached") {
		t.Fatalf("the statement after the raise must not run; stdout=%q", stdout)
	}
	if !strings.Contains(stderr, "AssertionError: a claim that did not hold") {
		t.Fatalf("stderr = %q, want the kind's own name in front of the message", stderr)
	}
}
