package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/build"
	runtime "github.com/cmj0121/zerg/src/runtime"
)

// buildConcurrent compiles src through the full pipeline and links it against the
// real runtime plus the scheduler and the host's context switch, returning the built
// binary path (skipping when no C compiler is available). It is the Phase 1e slice-C1
// observation harness: running the binary shows a spawned coroutine actually runs and
// the program completes.
func buildConcurrent(t *testing.T, src string) string {
	t.Helper()
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	code, manifest, diags := build.Compile(src)
	if len(diags) != 0 {
		t.Fatalf("compile diagnostics: %v", diags)
	}
	if !manifest.Concurrency {
		t.Fatalf("a spawn program must report Concurrency\n%s", code)
	}
	dir := t.TempDir()
	cfiles, err := runtime.Materialize(dir)
	if err != nil {
		t.Fatalf("materialize runtime: %v", err)
	}
	cfiles = append(cfiles, runtime.ConcurrencyCUnits(dir, runtime.HostArch())...)
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

// TestSpawnRunsCoroutine checks a fire-and-forget `spawn` actually runs: main runs
// first (no yield point), then the scheduler drains the run queue so the spawned
// coroutine runs to completion before the program exits.
func TestSpawnRunsCoroutine(t *testing.T) {
	const src = "fn worker() {\n print 42\n}\n" +
		"fn main() {\n print 1\n spawn worker()\n print 2\n}"
	bin := buildConcurrent(t, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "1\n2\n42\n" {
		t.Fatalf("stdout = %q, want %q (main runs, then the spawned coroutine)", stdout, "1\n2\n42\n")
	}
}

// TestSpawnMarshalsArguments checks a `spawn f(args)` evaluates and hands its
// arguments to the coroutine: two spawns with distinct int args each print their own
// captured value, in run-queue (FIFO) order after main.
func TestSpawnMarshalsArguments(t *testing.T) {
	const src = "fn greet(n: int) {\n print n\n}\n" +
		"fn main() {\n print 0\n spawn greet(7)\n spawn greet(8)\n}"
	bin := buildConcurrent(t, src)
	stdout, stderr, err := run(t, bin)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if stdout != "0\n7\n8\n" {
		t.Fatalf("stdout = %q, want %q (main, then FIFO-spawned coroutines)", stdout, "0\n7\n8\n")
	}
}
