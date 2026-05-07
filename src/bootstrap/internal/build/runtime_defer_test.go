package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// v0.12 Unit 5 — defer + main-coroutine end-to-end.
//
// The main-coroutine pattern (C main spawns a "main coro" running the
// user's main body, then drains) lets even top-level defers run via
// the same per-coroutine machinery. The tests below build a driver
// that exercises:
//
//   - LIFO ordering of multiple defers in the same coroutine
//   - defers running after a normal return AND on a coroutine spawned
//     by main (i.e. not just main itself)
//   - per-coroutine isolation: one coro's defer cannot run another's
//   - defer-from-main works once main is wrapped as a coroutine

func v12BuildDeferDriver(t *testing.T, driver string, env []string) ([]byte, int) {
	t.Helper()
	if _, err := exec.LookPath(DefaultCC()); err != nil {
		t.Skip("cc not available")
	}
	dir := t.TempDir()
	prog := coroRuntimeC + schedRuntimeC + deferRuntimeC + "\n" + driver
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

// TestV12DeferLIFOOrder confirms multiple defers in the same coroutine
// run in LIFO order on the way out.
func TestV12DeferLIFOOrder(t *testing.T) {
	driver := `
#include <stdio.h>

static void say_a(void *arg) { (void)arg; printf("a\n"); }
static void say_b(void *arg) { (void)arg; printf("b\n"); }
static void say_c(void *arg) { (void)arg; printf("c\n"); }

static void worker(void *arg) {
    (void)arg;
    zerg_coro_defer(say_a, 0);
    zerg_coro_defer(say_b, 0);
    zerg_coro_defer(say_c, 0);
    printf("body\n");
    /* expect: body, c, b, a */
}

int main(void) {
    zerg_sched_init(0);
    zerg_coro_spawn(worker, 0);
    zerg_sched_drain();
    return 0;
}
`
	out, code := v12BuildDeferDriver(t, driver, []string{"ZERG_GOMAXPROCS=1"})
	if code != 0 {
		t.Fatalf("driver exited %d\noutput:\n%s", code, out)
	}
	got := strings.TrimRight(string(out), "\n")
	want := "body\nc\nb\na"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestV12DeferIsolatedPerCoro confirms one coro's defer doesn't fire on
// another coro's exit. Two coros each register a unique-tagged defer;
// driver collects via a mutex-protected output buffer and asserts both
// defers (and only those) ran.
func TestV12DeferIsolatedPerCoro(t *testing.T) {
	driver := `
#include <stdio.h>
#include <pthread.h>

static pthread_mutex_t out_mu = PTHREAD_MUTEX_INITIALIZER;
static char out[256];
static int out_n = 0;

static void emit(void *arg) {
    char tag = (char)(intptr_t)arg;
    pthread_mutex_lock(&out_mu);
    out[out_n++] = tag;
    pthread_mutex_unlock(&out_mu);
}

static void coro_x(void *arg) {
    (void)arg;
    zerg_coro_defer(emit, (void *)(intptr_t)'X');
}

static void coro_y(void *arg) {
    (void)arg;
    zerg_coro_defer(emit, (void *)(intptr_t)'Y');
}

int main(void) {
    zerg_sched_init(0);
    zerg_coro_spawn(coro_x, 0);
    zerg_coro_spawn(coro_y, 0);
    zerg_sched_drain();
    /* Sort the two-char output for determinism (order between X/Y is
       scheduler-dependent). */
    if (out_n != 2) { fprintf(stderr, "out_n=%d\n", out_n); return 1; }
    if (out[0] > out[1]) { char t = out[0]; out[0] = out[1]; out[1] = t; }
    printf("%c%c\n", out[0], out[1]);
    return 0;
}
`
	out, code := v12BuildDeferDriver(t, driver, []string{"ZERG_GOMAXPROCS=2"})
	if code != 0 {
		t.Fatalf("driver exited %d\noutput:\n%s", code, out)
	}
	got := strings.TrimRight(string(out), "\n")
	if got != "XY" {
		t.Fatalf("got %q, want %q", got, "XY")
	}
}

// TestV12DeferMainCoroutine wraps the user main body in a coroutine so
// top-level defers also run via the per-coro mechanism. Confirms the
// defer fires before the OS process exits.
func TestV12DeferMainCoroutine(t *testing.T) {
	driver := `
#include <stdio.h>

static void final_msg(void *arg) {
    (void)arg;
    printf("main-defer-fired\n");
}

static void user_main(void *arg) {
    (void)arg;
    /* This is the user's main body, wrapped in a coroutine. */
    zerg_coro_defer(final_msg, 0);
    printf("user-main-body\n");
}

int main(void) {
    zerg_sched_init(0);
    zerg_coro_spawn(user_main, 0);
    zerg_sched_drain();
    /* C main returns AFTER the main coro has run all its defers. */
    return 0;
}
`
	out, code := v12BuildDeferDriver(t, driver, []string{"ZERG_GOMAXPROCS=1"})
	if code != 0 {
		t.Fatalf("driver exited %d\noutput:\n%s", code, out)
	}
	got := strings.TrimRight(string(out), "\n")
	want := "user-main-body\nmain-defer-fired"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
