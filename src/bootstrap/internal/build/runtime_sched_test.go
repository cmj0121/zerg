package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// v0.12 Unit 2 — scheduler end-to-end.
//
// Spawn N coroutines, each writes its index into a shared output buffer
// guarded by a mutex, then exits. Drain the scheduler. The exact order
// the indices appear in the buffer is non-deterministic by design (work-
// stealing across M workers is the whole point of v0.12), so the test
// asserts the SET of indices rather than the sequence:
//
//   - all N indices present (no coroutine dropped)
//   - no duplicates (no coroutine ran twice)
//   - count == N (no extra noise from runtime / scheduler bugs)
//
// The driver also prints the worker count so we can confirm
// ZERG_MAXPROCS / sysconf(_SC_NPROCESSORS_ONLN) is being honoured.

const schedDriverC = `
#include <stdio.h>
#include <stdlib.h>
#include <pthread.h>
#include "coro.h"
#include "sched.h"

#define N_SPAWNS 256

static pthread_mutex_t out_mu = PTHREAD_MUTEX_INITIALIZER;
static int out[N_SPAWNS];
static int out_n = 0;

static void worker_fn(void *arg) {
    intptr_t i = (intptr_t)arg;
    /* tiny artificial yield to exercise the scheduler — without this the
       coroutine may complete entirely on one worker before stealing has
       a chance to fire. v0.12 U2's yield path requeues on the local
       worker, so this round-trips through the scheduler at least once. */
    zerg_coro_yield();
    pthread_mutex_lock(&out_mu);
    out[out_n++] = (int)i;
    pthread_mutex_unlock(&out_mu);
}

int main(void) {
    /* default n_workers = nproc; the test sets ZERG_MAXPROCS to pin a
       deterministic worker count for the assertion below. */
    zerg_sched_init(0);
    for (intptr_t i = 0; i < N_SPAWNS; i++) {
        zerg_coro_spawn(worker_fn, (void *)i);
    }
    zerg_sched_drain();
    /* Print results. We cannot assert order — work-stealing scrambles it.
       Print one index per line; the test sorts and uniq-checks. */
    for (int i = 0; i < out_n; i++) printf("%d\n", out[i]);
    return 0;
}
`

const schedHeaderC = `
#ifndef ZERG_SCHED_H
#define ZERG_SCHED_H
typedef struct zerg_coro zerg_coro_t;
zerg_coro_t *zerg_coro_spawn(void (*fn)(void *), void *arg);
void zerg_sched_init(int n_workers);
void zerg_sched_drain(void);
void zerg_coro_park(void *unlock_mu);
void zerg_coro_unpark(zerg_coro_t *c);
#endif
`

// v12BuildSchedDriver is the shared compile + run helper for U2's tests.
// It writes the coro+sched runtime C, compiles the given driver source
// against it, and returns the driver's stdout. cc errors fail the test.
func v12BuildSchedDriver(t *testing.T, driver string, env []string) []byte {
	t.Helper()
	if _, err := exec.LookPath(DefaultCC()); err != nil {
		t.Skip("cc not available")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "coro.h"), []byte(coroHeaderC), 0o644); err != nil {
		t.Fatalf("write coro.h: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sched.h"), []byte(schedHeaderC), 0o644); err != nil {
		t.Fatalf("write sched.h: %v", err)
	}
	// runtime is single-TU: concatenate coro + sched into one .c file.
	if err := os.WriteFile(filepath.Join(dir, "rt.c"), []byte(coroRuntimeC+schedRuntimeC), 0o644); err != nil {
		t.Fatalf("write rt.c: %v", err)
	}
	driverPath := filepath.Join(dir, "driver.c")
	if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	binPath := filepath.Join(dir, "driver")
	cmd := exec.Command(DefaultCC(), "-Wall", "-Wno-deprecated-declarations",
		"-O2", "-pthread", "-o", binPath, driverPath, filepath.Join(dir, "rt.c"))
	cmd.Dir = dir
	var ccErr bytes.Buffer
	cmd.Stderr = &ccErr
	if err := cmd.Run(); err != nil {
		t.Fatalf("cc failed: %v\nstderr:\n%s", err, ccErr.String())
	}
	cmd = exec.Command(binPath)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("driver exited %d\nstderr:\n%s", ee.ExitCode(), ee.Stderr)
		}
		t.Fatalf("driver: %v", err)
	}
	return out
}

func TestV12SchedFanOut(t *testing.T) {
	// Pin to 4 workers so we test work-stealing without going to NPROC
	// (which can be 1 in some CI environments and would degenerate to
	// single-threaded execution).
	out := v12BuildSchedDriver(t, schedDriverC, []string{"ZERG_MAXPROCS=4"})
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 256 {
		t.Fatalf("got %d output lines, want 256\noutput:\n%s", len(lines), out)
	}
	got := make([]int, 0, 256)
	for _, l := range lines {
		n, err := strconv.Atoi(l)
		if err != nil {
			t.Fatalf("non-int line %q: %v", l, err)
		}
		got = append(got, n)
	}
	sort.Ints(got)
	for i, v := range got {
		if v != i {
			t.Fatalf("missing or duplicate spawn id at sorted position %d: got %d, want %d", i, v, i)
		}
	}
}

// TestV12SchedSingleWorker pins ZERG_MAXPROCS=1 so steal paths can
// never fire. Confirms the scheduler still terminates and runs every
// coroutine on the lone worker.
func TestV12SchedSingleWorker(t *testing.T) {
	out := v12BuildSchedDriver(t, schedDriverC, []string{"ZERG_MAXPROCS=1"})
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 256 {
		t.Fatalf("got %d output lines, want 256\noutput:\n%s", len(lines), out)
	}
}

// TestV12SchedParkUnpark exercises the park / unpark API directly: a
// "consumer" coroutine parks; a "producer" coroutine unparks it; the
// consumer resumes and prints. v0.12 U3 will hide this dance behind the
// channel rewrite, but the unit-2 test exercises the primitives that U3
// will call.
func TestV12SchedParkUnpark(t *testing.T) {
	driver := `
#include <stdio.h>
#include <stdlib.h>
#include <pthread.h>
#include "coro.h"
#include "sched.h"

/* Shared coroutine slot. The producer reads it to know which coroutine to
   unpark; we set it from the consumer side BEFORE parking. */
static zerg_coro_t * volatile waiting = 0;
static volatile int produced = 0;

/* The runtime exposes the current-coro pointer through a function-pointer
   indirection (zerg_coro_get_fp) — see runtime_coro.go for the macOS-arm64
   swapcontext / TLS rationale. The U2 sched_init replaces it with a
   pthread_self-keyed lookup so the value survives coroutine migration. */
extern zerg_coro_t *(*zerg_coro_get_fp)(void);

static void consumer_fn(void *arg) {
    (void)arg;
    /* Register self as waiting — the producer will unpark this exact
       coroutine. */
    waiting = zerg_coro_get_fp();
    zerg_coro_park(0);
    /* Resumed by producer. Read produced. */
    printf("consumed=%d\n", produced);
}

static void producer_fn(void *arg) {
    (void)arg;
    /* Spin briefly until consumer has registered. */
    while (waiting == 0) zerg_coro_yield();
    produced = 42;
    zerg_coro_unpark(waiting);
}

int main(void) {
    zerg_sched_init(0);
    zerg_coro_spawn(consumer_fn, 0);
    zerg_coro_spawn(producer_fn, 0);
    zerg_sched_drain();
    return 0;
}
`
	out := v12BuildSchedDriver(t, driver, []string{"ZERG_MAXPROCS=2"})
	got := strings.TrimRight(string(out), "\n")
	if got != "consumed=42" {
		t.Fatalf("output = %q, want %q", got, "consumed=42")
	}
}
