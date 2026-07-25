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

// buildCounting compiles src, links it against the counting allocator, and returns
// the built binary path (skipping when no C compiler is available). It is the
// Phase 1d iteration-3 observation harness: running the binary reveals both the
// program's stdout and, on stderr at exit, the "ALLOCS=n LIVE=m" balance — so a
// test can assert a Ref is freed exactly once across every exit path.
func buildCounting(t *testing.T, src string) string {
	t.Helper()
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	code, manifest, diags := build.Compile(src)
	if len(diags) != 0 {
		t.Fatalf("compile diagnostics: %v", diags)
	}
	if !manifest.NeedsRuntime {
		t.Fatalf("a teardown program must need the runtime\n%s", code)
	}
	dir := t.TempDir()
	cfiles, err := runtime.Materialize(dir)
	if err != nil {
		t.Fatalf("materialize runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alloc.c"), []byte(countingAllocC), 0o644); err != nil {
		t.Fatalf("write instrumented allocator: %v", err)
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

// run executes bin and returns its stdout, stderr, and any run error.
func run(t *testing.T, bin string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command(bin)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	return out.String(), errb.String(), err
}

// TestDeferRunsLIFO checks a `defer f()` runs at block exit in last-in-first-out
// order: two defers scheduled A then B run B then A, after the block's own output.
func TestDeferRunsLIFO(t *testing.T) {
	const src = "fn one() {\n print 1\n}\n" +
		"fn two() {\n print 2\n}\n" +
		"fn main() {\n defer one()\n defer two()\n print 3\n}"
	bin := buildCounting(t, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "3\n2\n1\n" {
		t.Fatalf("defer order = %q, want %q", stdout, "3\n2\n1\n")
	}
}

// TestEarlyReturnReleasesRef is iter-2's fixed gap: a Ref live at an early return is
// released on that path (no leak), and released once on the fallthrough path too —
// the counting allocator shows two allocations, both freed (LIVE=0).
func TestEarlyReturnReleasesRef(t *testing.T) {
	const src = "fn work(flag: bool) {\n" +
		"  r := Ref(7)\n" +
		"  print deref(r)\n" +
		"  if flag {\n    return\n  }\n" +
		"  print deref(r)\n" +
		"}\n" +
		"fn main() {\n work(true)\n work(false)\n}"
	bin := buildCounting(t, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "7\n7\n7\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "7\n7\n7\n")
	}
	if !strings.Contains(stderr, "ALLOCS=2 LIVE=0") {
		t.Fatalf("balance = %q, want ALLOCS=2 LIVE=0 (each Ref freed once across early + normal exit)", stderr)
	}
}

// TestDeferRunsOnAbortPath checks a `defer` scheduled before an abort still runs: a
// `raise` in a Result[nil] main unwinds the cleanup stack (running the defer, which
// prints) and exits non-zero via the runtime entry shim.
func TestDeferRunsOnAbortPath(t *testing.T) {
	const src = "fn bye() {\n print 9\n}\n" +
		"fn main() -> Result[nil] {\n defer bye()\n raise \"boom\"\n}"
	bin := buildCounting(t, src)
	stdout, stderr, err := run(t, bin)
	if err == nil {
		t.Fatalf("aborting main must exit non-zero; stdout=%q stderr=%q", stdout, stderr)
	}
	if stdout != "9\n" {
		t.Fatalf("abort-path defer stdout = %q, want %q", stdout, "9\n")
	}
	if !strings.Contains(stderr, "boom") {
		t.Fatalf("abort message = %q, want it to contain %q", stderr, "boom")
	}
	if !strings.Contains(stderr, "LIVE=0") {
		t.Fatalf("balance = %q, want LIVE=0 on the abort path", stderr)
	}
}

// TestDelFreesOnceNoDoubleFree checks `del name` on the sole holder frees the box
// now and the scope exit does not free it again — the counting allocator shows one
// allocation, freed exactly once (a double free would show LIVE<0).
func TestDelFreesOnceNoDoubleFree(t *testing.T) {
	const src = "fn main() {\n r := Ref(7)\n print deref(r)\n del r\n}"
	bin := buildCounting(t, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "7\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "7\n")
	}
	if !strings.Contains(stderr, "ALLOCS=1 LIVE=0") {
		t.Fatalf("balance = %q, want ALLOCS=1 LIVE=0 (del frees once, scope skips)", stderr)
	}
}

// TestWithTeardownEveryExit checks `with r as f { }` runs its resource's Scoped
// teardown (a Ref's release) on every exit: on the fallthrough path and on an early
// return out of the block. Two resources are acquired, both freed (LIVE=0).
func TestWithTeardownEveryExit(t *testing.T) {
	const src = "fn use(flag: bool) {\n" +
		"  with Ref(7) as f {\n" +
		"    print deref(f)\n" +
		"    if flag {\n      return\n    }\n" +
		"    print deref(f)\n" +
		"  }\n" +
		"}\n" +
		"fn main() {\n use(true)\n use(false)\n}"
	bin := buildCounting(t, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "7\n7\n7\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "7\n7\n7\n")
	}
	if !strings.Contains(stderr, "ALLOCS=2 LIVE=0") {
		t.Fatalf("balance = %q, want ALLOCS=2 LIVE=0 (with teardown on early + normal exit)", stderr)
	}
}
