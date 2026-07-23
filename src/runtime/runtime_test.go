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
	for _, want := range []string{"alloc.c", "ref.c", "list.c", "unwind.c", "entry.c", "sys.c", "fmt.c"} {
		if !got[want] {
			t.Errorf("materialized C files missing %s (got %v)", want, cfiles)
		}
	}
	if len(cfiles) != len(got) || len(got) != 7 {
		t.Errorf("expected 7 C units, got %v", cfiles)
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

// TestChannelsCompileAndRun is the slice-C2 channel smoke: it links the runtime with
// the scheduler and channels and drives all four behaviours the design calls out — a
// buffered producer, an unbuffered rendezvous, auto-close on the last sender leaving
// (recv -> Right/StopIteration), and a crashing last sender closing its channel with a
// crash Err (recv -> Right/Err). Each producer runs as a coroutine that parks/rendezvous
// with main, proving park/wake across a real context switch.
func TestChannelsCompileAndRun(t *testing.T) {
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

	driver := filepath.Join(dir, "chan_smoke.c")
	if err := os.WriteFile(driver, []byte(chanSmokeC), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	bin := filepath.Join(dir, "chan_smoke.bin")
	args := append([]string{"-std=c11", "-I", dir, "-o", bin, driver}, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s", err, out)
	}

	// Capture stdout only: a crashing coroutine reports "boom" on stderr (expected).
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("channel smoke run failed: %v\n%s", err, out)
	}
	want := "A=1\nA=2\nA-closed err=0\n" +
		"B=10\nB=20\nB-closed err=0\n" +
		"C=1 r=0\nC-crash r=1 err=coroutine crashed\n"
	if string(out) != want {
		t.Fatalf("channel smoke output = %q, want %q", out, want)
	}
}

// chanSmokeC drives channels through their four moving parts. Each producer coroutine
// receives the channel as its env; a sender registers its release on the cleanup stack
// (as the compiler does for a channel parameter) so a crash auto-closes it. main holds a
// receive-only copy (rc only) so the object survives the sender's close until main reads
// the Right and releases it.
const chanSmokeC = `
#include "zergrt.h"
#include <stdio.h>

static void chan_sender_drop(void *slot) {
    zrt_chan **s = (zrt_chan **)slot;
    if (*s != NULL) { zrt_chan_sender_release(*s); }
}

static void producer_buffered(void *env) {
    zrt_chan *ch = (zrt_chan *)env;
    long v = 1; zrt_chan_send(ch, &v);
    v = 2;     zrt_chan_send(ch, &v);
    zrt_chan_sender_release(ch);
}

static void producer_rendezvous(void *env) {
    zrt_chan *ch = (zrt_chan *)env;
    long v = 10; zrt_chan_send(ch, &v);
    v = 20;      zrt_chan_send(ch, &v);
    zrt_chan_sender_release(ch);
}

static void producer_crash(void *env) {
    zrt_chan *ch = (zrt_chan *)env;
    zrt_defer(chan_sender_drop, &ch); /* released on every exit, incl. the crash unwind */
    long v = 1; zrt_chan_send(ch, &v);
    zrt_abort("boom");                /* unhandled: closes ch with a crash Err */
}

static void prog_main(void) {
    long out;

    /* A: buffered (cap 2). main is a receive-only holder; producer is the sole sender. */
    zrt_chan *a = zrt_chan_new(sizeof(long), 2);
    zrt_chan_copy(a);
    zrt_spawn(producer_buffered, a);
    while (zrt_chan_recv(a, &out) == 0) { printf("A=%ld\n", out); }
    printf("A-closed err=%d\n", zrt_chan_err(a) != NULL);
    zrt_chan_release(a);

    /* B: unbuffered rendezvous (cap 0). */
    zrt_chan *b = zrt_chan_new(sizeof(long), 0);
    zrt_chan_copy(b);
    zrt_spawn(producer_rendezvous, b);
    while (zrt_chan_recv(b, &out) == 0) { printf("B=%ld\n", out); }
    printf("B-closed err=%d\n", zrt_chan_err(b) != NULL);
    zrt_chan_release(b);

    /* C: crashing last sender closes with a crash Err. */
    zrt_chan *c = zrt_chan_new(sizeof(long), 0);
    zrt_chan_copy(c);
    zrt_spawn(producer_crash, c);
    int r1 = zrt_chan_recv(c, &out);
    printf("C=%ld r=%d\n", out, r1);
    int r2 = zrt_chan_recv(c, &out);
    printf("C-crash r=%d err=%s\n", r2, zrt_chan_err(c));
    zrt_chan_release(c);
}

int main(void) {
    return zrt_sched_main_nil(prog_main);
}
`

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
