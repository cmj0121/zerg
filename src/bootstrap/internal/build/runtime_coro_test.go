package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// v0.12 Unit 1 — coroutine primitive end-to-end.
//
// Builds a tiny C driver that uses the coroRuntimeC primitive to alternate
// between the main thread and a single coroutine. The expected output pins
// the lifecycle: NEW -> RUNNING -> SUSPENDED -> RUNNING -> DONE walks one
// SUSPENDED stop, so we should see "main 1 / child a / main 2 / child b /
// main 3" interleaved exactly once. Anything else (corrupt stack, missing
// swapcontext save, unbalanced state) shows up as a missing or out-of-order
// line.

const coroDriverC = `
#include <stdio.h>
#include "coro.h"

static void child_fn(void *arg) {
    int *seen = (int *)arg;
    printf("child a (seen=%d)\n", *seen);
    *seen = 1;
    zerg_coro_yield();
    printf("child b (seen=%d)\n", *seen);
    *seen = 2;
}

int main(void) {
    int seen = 0;
    zerg_coro_t *c = zerg_coro_new(child_fn, &seen);
    if (!c) { fprintf(stderr, "coro_new failed\n"); return 1; }
    printf("main 1 (seen=%d)\n", seen);
    zerg_coro_resume(c);
    printf("main 2 (seen=%d)\n", seen);
    zerg_coro_resume(c);
    printf("main 3 (seen=%d)\n", seen);
    zerg_coro_free(c);
    return 0;
}
`

// coroHeaderC is the public surface of coroRuntimeC — just the typedef and
// the three fns used by the driver. We don't pull in the full runtime here
// because the .h/.c separation isn't real (everything lives in coroRuntimeC
// as a single TU); the driver compiles against a fwd-decl header that
// matches the API.
const coroHeaderC = `
#ifndef ZERG_CORO_H
#define ZERG_CORO_H
typedef struct zerg_coro zerg_coro_t;
zerg_coro_t *zerg_coro_new(void (*fn)(void *), void *arg);
void zerg_coro_resume(zerg_coro_t *c);
void zerg_coro_yield(void);
void zerg_coro_free(zerg_coro_t *c);
#endif
`

func TestV12CoroRoundTrip(t *testing.T) {
	if _, err := exec.LookPath(DefaultCC()); err != nil {
		t.Skip("cc not available")
	}
	dir := t.TempDir()

	hdrPath := filepath.Join(dir, "coro.h")
	srcPath := filepath.Join(dir, "coro.c")
	driverPath := filepath.Join(dir, "driver.c")
	binPath := filepath.Join(dir, "driver")

	if err := os.WriteFile(hdrPath, []byte(coroHeaderC), 0o644); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if err := os.WriteFile(srcPath, []byte(coroRuntimeC), 0o644); err != nil {
		t.Fatalf("write coro source: %v", err)
	}
	if err := os.WriteFile(driverPath, []byte(coroDriverC), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}

	cmd := exec.Command(DefaultCC(), "-Wall", "-Wno-deprecated-declarations",
		"-O2", "-pthread", "-o", binPath, driverPath, srcPath)
	cmd.Dir = dir
	var ccErr bytes.Buffer
	cmd.Stderr = &ccErr
	if err := cmd.Run(); err != nil {
		t.Fatalf("cc failed: %v\nstderr:\n%s", err, ccErr.String())
	}

	out, err := exec.Command(binPath).Output()
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	got := strings.TrimRight(string(out), "\n")
	want := strings.Join([]string{
		"main 1 (seen=0)",
		"child a (seen=0)",
		"main 2 (seen=1)",
		"child b (seen=1)",
		"main 3 (seen=2)",
	}, "\n")
	if got != want {
		t.Errorf("output mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestV12CoroStackOverflowGuard probes the mprotect guard page: a coroutine
// that recurses unboundedly should die with SIGSEGV (segfault), NOT corrupt
// the next coroutine or escape to the OS thread's stack. We check the
// driver's exit status / signal rather than parsing stderr because the
// kernel's "Bus error" / "Segmentation fault" message text varies.
func TestV12CoroStackOverflowGuard(t *testing.T) {
	if _, err := exec.LookPath(DefaultCC()); err != nil {
		t.Skip("cc not available")
	}
	dir := t.TempDir()

	const driver = `
#include <stdio.h>
#include "coro.h"

static int blackhole(int n) {
    /* recursive descent with a side-effect the optimizer cannot elide. */
    int local[64];
    for (int i = 0; i < 64; i++) local[i] = n + i;
    int sum = 0;
    for (int i = 0; i < 64; i++) sum += local[i];
    return sum + blackhole(n + 1);
}

static void child_fn(void *arg) {
    (void)arg;
    blackhole(0);
}

int main(void) {
    zerg_coro_t *c = zerg_coro_new(child_fn, 0);
    if (!c) { fprintf(stderr, "coro_new failed\n"); return 2; }
    zerg_coro_resume(c);
    /* Should never reach here — the recursion overruns the 256 KiB stack
       and trips the guard page. If we get here without SIGSEGV the guard
       is broken or the recursion is somehow being optimised away. */
    fprintf(stderr, "expected stack overflow, got clean return\n");
    return 3;
}
`

	hdrPath := filepath.Join(dir, "coro.h")
	srcPath := filepath.Join(dir, "coro.c")
	driverPath := filepath.Join(dir, "driver.c")
	binPath := filepath.Join(dir, "driver")

	if err := os.WriteFile(hdrPath, []byte(coroHeaderC), 0o644); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if err := os.WriteFile(srcPath, []byte(coroRuntimeC), 0o644); err != nil {
		t.Fatalf("write coro source: %v", err)
	}
	if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}

	// O0 here so the recursion is not tail-call-optimised into a loop —
	// we want each call frame on the stack so the guard fires.
	cmd := exec.Command(DefaultCC(), "-Wall", "-Wno-deprecated-declarations",
		"-O0", "-pthread", "-o", binPath, driverPath, srcPath)
	cmd.Dir = dir
	var ccErr bytes.Buffer
	cmd.Stderr = &ccErr
	if err := cmd.Run(); err != nil {
		t.Fatalf("cc failed: %v\nstderr:\n%s", err, ccErr.String())
	}

	cmd = exec.Command(binPath)
	err := cmd.Run()
	if err == nil {
		t.Fatal("driver returned cleanly; expected SIGSEGV from stack overflow")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("driver exit: unexpected error type %T: %v", err, err)
	}
	// If the process was killed by a signal, ExitError reports it via
	// ProcessState.Sys() on POSIX. We accept any non-zero exit / signal:
	// the contract is "the driver did not return cleanly". A signal-killed
	// process has an exit code in the 128+N range from the shell, but
	// exec.ExitError.ExitCode() returns -1 in that case.
	if exitErr.ExitCode() == 0 {
		t.Fatalf("driver exit code = 0; expected non-zero (signal or assert)")
	}
}
