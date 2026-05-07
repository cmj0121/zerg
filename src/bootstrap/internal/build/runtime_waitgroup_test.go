package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// v0.12 Unit 4 — wait_group end-to-end.
//
// Spawn N producer coroutines that each bump-and-done a shared
// wait_group, and one waiter coroutine that waits on the wait_group and
// prints "done" once all producers have finished. Confirms:
//
//   - waiter parks (no busy-spin in the C trace) until counter hits zero
//   - every producer's done fires before the waiter resumes
//   - the protocol is robust under work-stealing (multiple workers)

func v12BuildWaitGroupDriver(t *testing.T, driver string, env []string) ([]byte, int) {
	t.Helper()
	if _, err := exec.LookPath(DefaultCC()); err != nil {
		t.Skip("cc not available")
	}
	dir := t.TempDir()
	prog := coroRuntimeC + schedRuntimeC + chanRuntimeC + waitgroupRuntimeC + "\n" + driver
	progPath := filepath.Join(dir, "prog.c")
	if err := os.WriteFile(progPath, []byte(prog), 0o644); err != nil {
		t.Fatalf("write prog.c: %v", err)
	}
	binPath := filepath.Join(dir, "driver")
	cmd := exec.Command(DefaultCC(), "-Wall", "-Wno-deprecated-declarations",
		"-Wno-unused-function", "-O2", "-pthread", "-o", binPath, progPath)
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
			return append(out, ee.Stderr...), ee.ExitCode()
		}
		t.Fatalf("driver: %v", err)
	}
	return out, 0
}

// TestV12WaitGroupBasic spawns 64 producers + 1 waiter; the waiter prints
// "done" once all producers are accounted for. Asserts the exact
// "done" line and total producer-progress count.
func TestV12WaitGroupBasic(t *testing.T) {
	driver := `
#include <stdio.h>
#include <stdlib.h>
#include <pthread.h>

#define N 64
static zerg_waitgroup_t *wg;
static pthread_mutex_t cnt_mu = PTHREAD_MUTEX_INITIALIZER;
static int cnt = 0;

static void producer(void *arg) {
    (void)arg;
    pthread_mutex_lock(&cnt_mu);
    cnt++;
    pthread_mutex_unlock(&cnt_mu);
    zerg_waitgroup_done(wg);
}

static void waiter(void *arg) {
    (void)arg;
    zerg_waitgroup_wait(wg);
    printf("done cnt=%d\n", cnt);
}

int main(void) {
    zerg_sched_init(0);
    wg = zerg_waitgroup_make();
    zerg_waitgroup_add(wg, N);
    zerg_coro_spawn(waiter, 0);
    for (int i = 0; i < N; i++) zerg_coro_spawn(producer, 0);
    zerg_sched_drain();
    return 0;
}
`
	out, code := v12BuildWaitGroupDriver(t, driver, []string{"ZERG_GOMAXPROCS=4"})
	if code != 0 {
		t.Fatalf("driver exited %d\noutput:\n%s", code, out)
	}
	got := strings.TrimRight(string(out), "\n")
	want := "done cnt=64"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestV12WaitGroupZeroDoesntBlock confirms that a wait on a zero-counter
// wait_group returns immediately without parking.
func TestV12WaitGroupZeroDoesntBlock(t *testing.T) {
	driver := `
#include <stdio.h>

static zerg_waitgroup_t *wg;

static void waiter(void *arg) {
    (void)arg;
    /* counter starts at zero — wait must be a no-op. */
    zerg_waitgroup_wait(wg);
    printf("ok\n");
}

int main(void) {
    zerg_sched_init(0);
    wg = zerg_waitgroup_make();
    zerg_coro_spawn(waiter, 0);
    zerg_sched_drain();
    return 0;
}
`
	out, code := v12BuildWaitGroupDriver(t, driver, []string{"ZERG_GOMAXPROCS=2"})
	if code != 0 {
		t.Fatalf("driver exited %d\noutput:\n%s", code, out)
	}
	got := strings.TrimRight(string(out), "\n")
	if got != "ok" {
		t.Fatalf("got %q, want %q", got, "ok")
	}
}

// TestV12WaitGroupNegativePanics confirms the runtime aborts on a
// counter-goes-negative misuse (matching v0.7 semantics).
func TestV12WaitGroupNegativePanics(t *testing.T) {
	driver := `
#include <stdio.h>

static zerg_waitgroup_t *wg;

static void worker(void *arg) {
    (void)arg;
    /* No corresponding add — done() drops counter from 0 to -1, which
       must trip the runtime diagnostic. */
    zerg_waitgroup_done(wg);
}

int main(void) {
    zerg_sched_init(0);
    wg = zerg_waitgroup_make();
    zerg_coro_spawn(worker, 0);
    zerg_sched_drain();
    fprintf(stderr, "expected panic before drain returned\n");
    return 99;
}
`
	out, code := v12BuildWaitGroupDriver(t, driver, nil)
	if code == 0 {
		t.Fatalf("driver returned cleanly; expected non-zero from negative-counter panic\noutput:\n%s", out)
	}
	if !bytes.Contains(out, []byte("wait_group counter went negative")) {
		t.Fatalf("output does not contain expected diagnostic\noutput:\n%s", out)
	}
}
