package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/build"
	runtime "github.com/cmj0121/zerg/src/runtime"
)

// buildResult compiles src, links it against the materialized runtime, and returns
// the built binary path (skipping when no C compiler is available). It is the Phase
// 1f U0 harness: the program constructs and consumes general Result[T] values, so it
// must report NeedsRuntime and link the Err-carrying abort/guard machinery.
func buildResult(t *testing.T, src string) string {
	t.Helper()
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	code, manifest, diags := build.Compile(src)
	if len(diags) != 0 {
		t.Fatalf("compile diagnostics: %v", diags)
	}
	if !manifest.NeedsResult || !manifest.NeedsRuntime {
		t.Fatalf("a Result-carrier program must set NeedsResult+NeedsRuntime\n%s", code)
	}
	dir := t.TempDir()
	cfiles, err := runtime.Materialize(dir)
	if err != nil {
		t.Fatalf("materialize runtime: %v", err)
	}
	cpath := filepath.Join(dir, "prog.c")
	if err := os.WriteFile(cpath, []byte(code), 0o644); err != nil {
		t.Fatalf("write C: %v", err)
	}
	bin := filepath.Join(dir, "prog.bin")
	args := append([]string{"-std=c11", "-I", dir, "-o", bin, cpath}, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s\n--- generated C ---\n%s", err, out, code)
	}
	return bin
}

// TestResultOkForceCoalesceGuard is the U0 end-to-end demo: a fn returning
// Result[int] with `return 5` (Ok), a fn that raises, a caller using `?` to
// propagate, `??` with a default, `!` force-unwrap, and `guard { }` catching a raise
// into a Result. Every outcome is observed through stdout.
func TestResultOkForceCoalesceGuard(t *testing.T) {
	const src = "fn ok_val(n: int) -> Result[int] {\n" +
		"  return n\n" +
		"}\n" +
		"fn risky(n: int) -> int {\n" +
		"  raise \"kaboom\"\n" +
		"  return n\n" +
		"}\n" +
		"fn chain_err() -> Result[int] {\n" +
		"  v := (guard { risky(1) })?\n" +
		"  return v + 1\n" +
		"}\n" +
		"fn chain_ok() -> Result[int] {\n" +
		"  v := (guard { 10 })?\n" +
		"  return v + 5\n" +
		"}\n" +
		"fn main() -> Result[nil] {\n" +
		"  print ok_val(5)!\n" + // 5   : force-unwrap an Ok
		"  print (ok_val(7) ?? -1)\n" + // 7   : ?? on a Left
		"  print (chain_err() ?? -1)\n" + // -1  : chain_err returned a Right (propagated by ?)
		"  print chain_ok()!\n" + // 15  : ? unwrapped a Left, then +5
		"  g := guard { risky(9) }\n" + // guard catches a raise -> Right
		"  print (g ?? -2)\n" + // -2
		"  h := guard { 99 }\n" + // guard on a normal completion -> Left(99)
		"  print (h ?? -2)\n" + // 99
		"}"
	bin := buildResult(t, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "5\n7\n-1\n15\n-2\n99\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "5\n7\n-1\n15\n-2\n99\n")
	}
	// a guard-caught raise is handled, so it prints NO diagnostic to stderr.
	if strings.Contains(stderr, "kaboom") {
		t.Fatalf("a guard-caught raise must not report to stderr; got %q", stderr)
	}
}

// TestResultTryPropagatesEarly checks `?` early-returns the enclosing function's
// Right when the operand is a Right: use_err never runs its tail, so the caller's
// `??` sees the propagated default.
func TestResultTryPropagatesEarly(t *testing.T) {
	const src = "fn rr() -> int {\n  raise \"e\"\n  return 0\n}\n" +
		"fn make_err() -> Result[int] {\n" +
		"  return (guard { rr() })?\n" +
		"}\n" +
		"fn use_err() -> Result[int] {\n" +
		"  v := make_err()?\n" + // make_err is a Right -> propagate, tail unreached
		"  return v + 1000\n" +
		"}\n" +
		"fn main() -> Result[nil] {\n" +
		"  print (use_err() ?? 42)\n" + // 42, not 1000-something
		"}"
	bin := buildResult(t, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "42\n" {
		t.Fatalf("stdout = %q, want %q (early propagation)", stdout, "42\n")
	}
}

// TestResultCoalesceDiverges checks the `??` default may itself DIVERGE: here it
// `return`s the whole Right, so a caller sees the propagated error.
func TestResultCoalesceDiverges(t *testing.T) {
	const src = "fn doubled(r: Result[int]) -> Result[int] {\n" +
		"  v := r ?? return r\n" + // r is Right -> return r; else use v
		"  return v * 2\n" +
		"}\n" +
		"fn main() -> Result[nil] {\n" +
		"  print (doubled(ok()) ?? -1)\n" + // 20
		"  print (doubled(bad()) ?? -1)\n" + // -1 (diverged return of the Right)
		"}\n" +
		"fn rr() -> int {\n  raise \"x\"\n  return 0\n}\n" +
		"fn ok() -> Result[int] {\n  return 10\n}\n" +
		"fn bad() -> Result[int] {\n  return (guard { rr() })?\n}"
	bin := buildResult(t, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "20\n-1\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "20\n-1\n")
	}
}

// TestResultFallthroughReturnCarrier is the regression for the synthesized
// fall-through return of a carrier-returning function: a Result[T] fn whose body
// only diverges (a `raise`, no explicit return) must still emit a type-correct
// trailing return — a zeroed carrier, not the scalar `0`. The guard catches the
// raise, so the program prints -1.
func TestResultFallthroughReturnCarrier(t *testing.T) {
	const src = "fn boom() -> Result[int] { raise \"bad\" }\n" +
		"fn main() {\n" +
		"  g := guard { boom()! }\n" +
		"  print g ?? -1\n" +
		"}"
	bin := buildResult(t, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "-1\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "-1\n")
	}
}

// TestResultForceRaisesOnErr checks `!` on a Right aborts (UnwrapError): an uncaught
// force-unwrap of a Right value exits non-zero and reports.
func TestResultForceRaisesOnErr(t *testing.T) {
	const src = "fn rr() -> int {\n  raise \"boom\"\n  return 0\n}\n" +
		"fn bad() -> Result[int] {\n" +
		"  return (guard { rr() })?\n" +
		"}\n" +
		"fn main() -> Result[nil] {\n" +
		"  print bad()!\n" + // force-unwrap a Right -> raise UnwrapError, uncaught
		"}"
	bin := buildResult(t, src)
	stdout, stderr, err := run(t, bin)
	if err == nil {
		t.Fatalf("force-unwrap of a Right must exit non-zero; stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stderr, "force-unwrap of an Err value") {
		t.Fatalf("stderr = %q, want the UnwrapError diagnostic", stderr)
	}
}
