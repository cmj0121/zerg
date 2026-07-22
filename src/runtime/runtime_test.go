package runtime

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// findCC locates a C compiler, or "" when none is installed.
func findCC() string {
	for _, name := range []string{"cc", "clang", "gcc"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// TestEmbedShipsTree asserts the runtime tree is embedded and Materialize lays
// down the header plus exactly the C translation units the driver links.
func TestEmbedShipsTree(t *testing.T) {
	dir := t.TempDir()
	cfiles, err := Materialize(dir)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "zergrt.h")); err != nil {
		t.Fatalf("zergrt.h not materialized: %v", err)
	}
	got := map[string]bool{}
	for _, c := range cfiles {
		got[filepath.Base(c)] = true
	}
	for _, want := range []string{"alloc.c", "ref.c", "unwind.c", "entry.c", "sys.c"} {
		if !got[want] {
			t.Errorf("materialized C files missing %s (got %v)", want, cfiles)
		}
	}
	if len(cfiles) != len(got) || len(got) != 5 {
		t.Errorf("expected 5 C units, got %v", cfiles)
	}
}

// TestRuntimeCompilesAndRuns is the runtime's own smoke: it compiles the C tree
// with a small driver that exercises the Ref refcount (drop runs once, at the
// last release), the cleanup(defer) stack ordering, and the abort/unwind entry
// shim (an aborting main returns non-zero after running its pending defer).
func TestRuntimeCompilesAndRuns(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	dir := t.TempDir()
	cfiles, err := Materialize(dir)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}

	driver := filepath.Join(dir, "smoke.c")
	if err := os.WriteFile(driver, []byte(smokeC), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}

	bin := filepath.Join(dir, "smoke.bin")
	args := append([]string{"-std=c11", "-I", dir, "-o", bin, driver}, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s", err, out)
	}

	// Capture stdout only: zrt_abort reports "boom" on stderr (expected), which
	// would otherwise interleave with the buffered stdout assertions.
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("smoke run failed: %v\n%s", err, out)
	}
	want := "drops=1\ndefer-order=BA\nabort-run=1\n"
	if string(out) != want {
		t.Fatalf("smoke output = %q, want %q", out, want)
	}
}

// TestEmbedHasNoStrayFiles guards that only the intended sources are embedded.
func TestEmbedHasNoStrayFiles(t *testing.T) {
	var names []string
	_ = fs.WalkDir(Files, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names = append(names, p)
		}
		return nil
	})
	for _, n := range names {
		if !strings.HasSuffix(n, ".c") && !strings.HasSuffix(n, ".h") && !strings.HasSuffix(n, ".S") {
			t.Errorf("unexpected embedded file %q", n)
		}
	}
}

// TestSchedulerCompilesAndRuns is the concurrency smoke: it links the core runtime
// with the scheduler and the host's context switch, then drives zrt_spawn and the
// nil-main entry shim. It asserts the spawned coroutine actually runs (on its own
// stack) and that the run queue drains so fire-and-forget coroutines complete before
// the program exits.
func TestSchedulerCompilesAndRuns(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	dir := t.TempDir()
	cfiles, err := Materialize(dir)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	cfiles = append(cfiles, ConcurrencyCUnits(dir, HostArch())...)

	driver := filepath.Join(dir, "sched_smoke.c")
	if err := os.WriteFile(driver, []byte(schedSmokeC), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	bin := filepath.Join(dir, "sched_smoke.bin")
	args := append([]string{"-std=c11", "-I", dir, "-o", bin, driver}, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s", err, out)
	}

	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("scheduler smoke run failed: %v\n%s", err, out)
	}
	want := "main\nco-a\nco-b\n"
	if string(out) != want {
		t.Fatalf("scheduler smoke output = %q, want %q", out, want)
	}
}

// schedSmokeC spawns two coroutines from a nil main, then returns; the scheduler
// must still run both queued coroutines (fire-and-forget) before the program exits.
const schedSmokeC = `
#include "zergrt.h"
#include <stdio.h>

static void co_a(void *env) { (void)env; printf("co-a\n"); }
static void co_b(void *env) { (void)env; printf("co-b\n"); }

static void prog_main(void) {
    printf("main\n");
    zrt_spawn(co_a, NULL);
    zrt_spawn(co_b, NULL);
}

int main(void) {
    return zrt_sched_main_nil(prog_main);
}
`

// smokeC drives the runtime through its three moving parts and prints one line
// each so the Go test can assert observable behavior.
const smokeC = `
#include "zergrt.h"
#include <stdio.h>

static int g_drops;
static void count_drop(void *payload) { (void)payload; g_drops++; }

static char g_order[3];
static int  g_oi;
static void mark_a(void *env) { (void)env; g_order[g_oi++] = 'A'; }
static void mark_b(void *env) { (void)env; g_order[g_oi++] = 'B'; }

static int g_aborted_defer;
static void abort_defer(void *env) { (void)env; g_aborted_defer = 1; }

static zrt_result_nil aborting_main(void) {
    zrt_defer(abort_defer, NULL);
    zrt_abort("boom");           /* longjmps to zrt_run, running abort_defer */
    return zrt_result_ok();      /* unreached */
}

int main(void) {
    /* Ref refcount: copy (retain) then two releases; drop runs once at rc==0. */
    void *r = zrt_ref_alloc(sizeof(long), count_drop);
    *(long *)zrt_ref_payload(r) = 42;
    void *r2 = zrt_ref_copy(r);  /* retains and returns the same box */
    zrt_release(r);
    zrt_release(r2);
    printf("drops=%d\n", g_drops);

    /* cleanup(defer) stack runs LIFO: push A then B, unwind runs B then A. */
    size_t mark = zrt_scope_mark();
    zrt_defer(mark_a, NULL);
    zrt_defer(mark_b, NULL);
    zrt_unwind_to(mark);
    g_order[g_oi] = 0;
    printf("defer-order=%s\n", g_order);

    /* abort/unwind entry shim: aborting main returns non-zero, defer ran. */
    int rc = zrt_run(aborting_main);
    printf("abort-run=%d\n", g_aborted_defer);
    return rc == 0 ? 3 : 0;  /* expect non-zero rc from the aborting run */
}
`
