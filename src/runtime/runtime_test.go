package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
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

// buildConcurrent compiles a C driver against the core runtime plus the concurrency
// units and returns the binary. Every scheduler test opens with the same six lines, and
// spelling them out each time buries what the test is actually about.
func buildConcurrent(t *testing.T, name, src string) string {
	t.Helper()
	return buildUnits(t, name, src, true)
}

// buildCore is buildConcurrent without the scheduler: a driver that runs on the one thread
// it started on links the core runtime and nothing else.
func buildCore(t *testing.T, name, src string) string {
	t.Helper()
	return buildUnits(t, name, src, false)
}

func buildUnits(t *testing.T, name, src string, conc bool) string {
	t.Helper()
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	dir := t.TempDir()
	cfiles, err := Materialize(dir)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if conc {
		cfiles = append(cfiles, ConcurrencyCUnits(dir, HostArch())...)
	}
	driver := filepath.Join(dir, name+".c")
	if err := os.WriteFile(driver, []byte(src), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	bin := filepath.Join(dir, name+".bin")
	args := append([]string{"-std=c11", "-I", dir, "-o", bin, driver}, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s", err, out)
	}
	return bin
}

// runBounded runs bin under a deadline and returns its STDOUT and exit code; stderr is
// dropped, since an abort's diagnostic goes there and would interleave with the assertions.
//
// The deadline is the point of this helper. Everything the scheduler tests guard against —
// a lost wake-up, a deadlock the detector stopped catching, a timer that never fires, a
// waiter left dangling in a queue — presents as a program that simply never finishes, and
// a test that hangs eats the whole suite's budget without naming what broke.
//
// `workers` is the ZRT_WORKERS value: "1" forces the single-worker path, which is how a
// bug in the scheduler's logic is told apart from a race between workers. Every test here
// runs both ways, because each has caught a failure the other did not.
func runBounded(t *testing.T, bin, workers string, d time.Duration) (string, int) {
	t.Helper()
	out, _, code := runBoundedStreams(t, bin, workers, d)
	return out, code
}

// runBoundedStreams is runBounded with the abort diagnostic KEPT. The `Kind: text` line
// the abort contract makes normative lands on stderr, so the one test that reads it needs
// the stream every other test here deliberately drops.
func runBoundedStreams(t *testing.T, bin, workers string, d time.Duration) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	if workers != "" {
		cmd.Env = append(os.Environ(), "ZRT_WORKERS="+workers)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("%s (ZRT_WORKERS=%q) did not finish within %s; output so far %q",
			filepath.Base(bin), workers, d, stdout.String())
	}
	var exit *exec.ExitError
	switch {
	case err == nil:
		return stdout.String(), stderr.String(), 0
	case errors.As(err, &exit):
		return stdout.String(), stderr.String(), exit.ExitCode()
	default:
		t.Fatalf("run %s: %v", bin, err)
		return "", "", 0
	}
}

// workerModes are the two scheduler shapes every concurrency test is run under: the
// machine's real worker count, and one worker.
var workerModes = []string{"", "1"}

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
	for _, want := range []string{"alloc.c", "ref.c", "list.c", "map.c", "unwind.c", "entry.c", "sys.c", "fmt.c", "conv.c", "str.c"} {
		if !got[want] {
			t.Errorf("materialized C files missing %s (got %v)", want, cfiles)
		}
	}
	if len(cfiles) != len(got) || len(got) != 10 {
		t.Errorf("expected 10 C units, got %v", cfiles)
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

// mapSanFlags are the sanitizer flags the map memory tests compile under: ASan +
// UBSan with no recover, so any heap error or UB aborts the run. macOS has no
// LeakSanitizer, so the driver's own counting allocator asserts alloc/free balance.
var mapSanFlags = []string{"-fsanitize=address,undefined", "-fno-sanitize-recover=all"}

// coreUnitsExcept returns the materialized core C units with the named file removed, so
// a test can substitute its own translation unit (here: a counting allocator in place
// of alloc.c).
func coreUnitsExcept(cfiles []string, drop string) []string {
	out := make([]string, 0, len(cfiles))
	for _, c := range cfiles {
		if filepath.Base(c) != drop {
			out = append(out, c)
		}
	}
	return out
}

// TestMapCompilesAndRuns drives map.c under ASan+UBSan with a COUNTING allocator
// (alloc.c is swapped out for the driver's zrt_alloc/zrt_free) so alloc/free balance
// stands in for the LeakSanitizer macOS lacks. It exercises every memory path the
// design calls out: a map[str,int] that grows past the 0.75 load factor (copy + drop),
// a map[int, Ref[int]] value (copy/drop through the val vtable), a nested map value, a
// key-miss abort (KeyError caught by a guard handler), and insertion-order iteration.
func TestMapCompilesAndRuns(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	dir := t.TempDir()
	cfiles, err := Materialize(dir)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	units := coreUnitsExcept(cfiles, "alloc.c") // driver supplies a counting zrt_alloc/zrt_free

	driver := filepath.Join(dir, "map_smoke.c")
	if err := os.WriteFile(driver, []byte(mapSmokeC), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	bin := filepath.Join(dir, "map_smoke.bin")
	args := append(append([]string{"-std=c11", "-I", dir, "-o", bin, driver}, mapSanFlags...), units...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s", err, out)
	}

	// Capture stdout only: the key-miss path may report on stderr under a plain handler,
	// though the guard handler here suppresses it.
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("map smoke run failed: %v\n%s", err, out)
	}
	want := "str-order=a=1,bb=2,ccc=3,dddd=4,eeeee=5,ffffff=6,ggggggg=7,hhhhhhhh=8,iiiiiiiii=9,jjjjjjjjjj=10\n" +
		"str-get=3/miss\n" +
		"str-update=99\n" +
		"copy-indep=1/1000\n" +
		"ref-sum=60\n" +
		"nested=2:3\n" +
		"key-miss-abort=1\n" +
		"live=0\n"
	if string(out) != want {
		t.Fatalf("map smoke output = %q, want %q", out, want)
	}
}

// mapSmokeC drives map.c through every memory path under a counting allocator. It builds
// vtables by hand exactly as the emitter would: int/str keys use the built-in Hash, a
// Ref value routes copy/drop through zrt_ref_copy/zrt_release, a nested map value through
// zrt_map_copy/zrt_map_drop.
const mapSmokeC = `
#include "zergrt.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* counting allocator: alloc.c is not linked, so these ARE zrt_alloc/zrt_free. */
static long g_live;
void *zrt_alloc(size_t n) { void *p = malloc(n); if (p != NULL) { g_live++; } return p; }
void zrt_free(void *p) { if (p != NULL) { g_live--; free(p); } }

static const zrt_map_vt vt_str_int = { zrt_hash_str, zrt_eq_str, NULL, NULL, NULL, NULL };
static const zrt_map_vt vt_int_int = { zrt_hash_int, zrt_eq_int, NULL, NULL, NULL, NULL };

static void ref_val_copy(void *dst, const void *src) { *(void **)dst = zrt_ref_copy(*(void *const *)src); }
static void ref_val_drop(void *v) { zrt_release(*(void **)v); }
static const zrt_map_vt vt_int_ref = { zrt_hash_int, zrt_eq_int, NULL, NULL, ref_val_copy, ref_val_drop };

static void map_val_copy(void *dst, const void *src) { zrt_map_copy((zrt_map *)dst, (const zrt_map *)src); }
static void map_val_drop(void *v) { zrt_map_drop((zrt_map *)v); }
static const zrt_map_vt vt_int_map = { zrt_hash_int, zrt_eq_int, NULL, NULL, map_val_copy, map_val_drop };

int main(void) {
    /* --- map[str,int]: insert 10 keys (grows past load factor 0.75 from nbuckets 8),
     * prove INSERTION-order iteration, get hit/miss, and an update in place. --- */
    static const char *keys[10] = {"a","bb","ccc","dddd","eeeee","ffffff","ggggggg","hhhhhhhh","iiiiiiiii","jjjjjjjjjj"};
    zrt_map m;
    zrt_map_init(&m, sizeof(const char *), sizeof(int64_t), &vt_str_int);
    for (int i = 0; i < 10; i++) {
        const char *k = keys[i]; int64_t v = i + 1;
        zrt_map_set(&m, &k, &v);
    }
    printf("str-order=");
    for (size_t i = 0; i < zrt_map_len(&m); i++) {
        const char *k = *(const char **)zrt_map_key_at(&m, i);
        int64_t v = *(int64_t *)zrt_map_val_at(&m, i);
        printf("%s%s=%lld", i ? "," : "", k, (long long)v);
    }
    printf("\n");

    const char *q = "ccc";
    int64_t *hit = (int64_t *)zrt_map_get(&m, &q);
    const char *qm = "zzz";
    void *miss = zrt_map_get(&m, &qm);
    printf("str-get=%lld/%s\n", (long long)(hit ? *hit : -1), miss == NULL ? "miss" : "hit");

    /* update in place (hit path: old val dropped [POD no-op], new stored, surplus key dropped). */
    const char *ku = "eeeee"; int64_t nv = 99;
    zrt_map_set(&m, &ku, &nv);
    printf("str-update=%lld\n", (long long)*(int64_t *)zrt_map_index(&m, &ku));

    /* --- deep copy independence: copy m, mutate the copy, original unchanged. --- */
    zrt_map c;
    zrt_map_copy(&c, &m);
    const char *ka = "a"; int64_t big = 1000;
    zrt_map_set(&c, &ka, &big);
    printf("copy-indep=%lld/%lld\n",
        (long long)*(int64_t *)zrt_map_index(&m, &ka),
        (long long)*(int64_t *)zrt_map_index(&c, &ka));
    zrt_map_drop(&c);
    zrt_map_drop(&m);

    /* --- map[int, Ref[int]]: value copy/drop routes through the Ref refcount. --- */
    zrt_map rm;
    zrt_map_init(&rm, sizeof(int64_t), sizeof(void *), &vt_int_ref);
    for (int i = 0; i < 3; i++) {
        int64_t k = i;
        void *box = zrt_ref_alloc(sizeof(int64_t), NULL);
        *(int64_t *)zrt_ref_payload(box) = (i + 1) * 10; /* 10,20,30 */
        zrt_map_set(&rm, &k, &box); /* box ownership moves into the entry */
    }
    zrt_map rc;
    zrt_map_copy(&rc, &rm); /* val_copy retains each box */
    int64_t sum = 0;
    for (size_t i = 0; i < zrt_map_len(&rc); i++) {
        void *box = *(void **)zrt_map_val_at(&rc, i);
        sum += *(int64_t *)zrt_ref_payload(box);
    }
    printf("ref-sum=%lld\n", (long long)sum); /* 60 */
    zrt_map_drop(&rc);
    zrt_map_drop(&rm);

    /* --- nested map value map[int, map[int,int]]: copy/drop through the val vtable. --- */
    zrt_map outer;
    zrt_map_init(&outer, sizeof(int64_t), sizeof(zrt_map), &vt_int_map);
    for (int i = 0; i < 2; i++) {
        zrt_map inner;
        zrt_map_init(&inner, sizeof(int64_t), sizeof(int64_t), &vt_int_int);
        for (int j = 0; j < i + 2; j++) { int64_t k = j, v = j; zrt_map_set(&inner, &k, &v); }
        int64_t ok = i;
        zrt_map_set(&outer, &ok, &inner); /* inner moves into the entry */
    }
    int64_t o0 = 0, o1 = 1;
    zrt_map *in0 = (zrt_map *)zrt_map_index(&outer, &o0);
    zrt_map *in1 = (zrt_map *)zrt_map_index(&outer, &o1);
    printf("nested=%zu:%zu\n", zrt_map_len(in0), zrt_map_len(in1)); /* 2:3 */
    zrt_map_drop(&outer);

    /* --- key-miss abort: m[k] on a miss raises KeyError; a guard handler catches it. --- */
    zrt_map km;
    zrt_map_init(&km, sizeof(int64_t), sizeof(int64_t), &vt_int_int);
    int64_t only = 7, onlyv = 7;
    zrt_map_set(&km, &only, &onlyv);
    int aborted = 0;
    zrt_frame fr;
    zrt_handler_push_catch(&fr);
    if (setjmp(fr.buf) == 0) {
        int64_t bad = 8;
        zrt_map_index(&km, &bad); /* longjmps to fr */
    } else {
        aborted = 1;
    }
    zrt_handler_pop(&fr);
    zrt_map_drop(&km);
    printf("key-miss-abort=%d\n", aborted);

    printf("live=%ld\n", g_live);
    return 0;
}
`

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
// nil-main entry shim. It asserts the spawned coroutines actually run, each on its own
// stack, and that main can observe them finishing.
//
// It counts rather than naming who printed what. Two coroutines that both become
// runnable can occupy two workers at once, so the order their output reaches the pipe
// in is not a property of the scheduler — asserting it would make this test fail on a
// machine with more cores rather than on a bug.
func TestSchedulerCompilesAndRuns(t *testing.T) {
	bin := buildConcurrent(t, "sched_smoke", schedSmokeC)

	want := "main\nran=2 sum=3\n"
	for _, w := range workerModes {
		out, code := runBounded(t, bin, w, 20*time.Second)
		if out != want || code != 0 {
			t.Fatalf("ZRT_WORKERS=%q: scheduler smoke = %q exit=%d, want %q exit=0", w, out, code, want)
		}
	}
}

// TestChannelsCompileAndRun is the slice-C2 channel smoke: it links the runtime with
// the scheduler and channels and drives all four behaviours the design calls out — a
// buffered producer, an unbuffered rendezvous, auto-close on the last sender leaving
// (recv -> Right/StopIteration), and a crashing last sender closing its channel with a
// crash Err (recv -> Right/Err). Each producer runs as a coroutine that parks/rendezvous
// with main, proving park/wake across a real context switch.
func TestChannelsCompileAndRun(t *testing.T) {
	bin := buildConcurrent(t, "chan_smoke", chanSmokeC)

	want := "A=1\nA=2\nA-closed err=0\n" +
		"B=10\nB=20\nB-closed err=0\n" +
		"C=1 r=0\nC-crash r=1 err=boom kind=3\n"
	for _, w := range workerModes {
		out, code := runBounded(t, bin, w, 20*time.Second)
		if out != want || code != 0 {
			t.Fatalf("ZRT_WORKERS=%q: channel smoke = %q exit=%d, want %q exit=0", w, out, code, want)
		}
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
    zrt_abort_kind(ZRT_ERR_IO, "boom"); /* unhandled: closes ch with THIS Err, kind and all */
}

static void prog_main(void) {
    long out;

    /* A: buffered (cap 2). main is a receive-only holder; producer is the sole sender. */
    zrt_chan *a = zrt_chan_new(sizeof(long), 2);
    zrt_chan_copy(a);
    zrt_spawn(producer_buffered, a);
    while (zrt_chan_recv(a, &out) == 0) { printf("A=%ld\n", out); }
    printf("A-closed err=%d\n", zrt_chan_close_err(a).kind != ZRT_ERR_STOP_ITERATION);
    zrt_chan_release(a);

    /* B: unbuffered rendezvous (cap 0). */
    zrt_chan *b = zrt_chan_new(sizeof(long), 0);
    zrt_chan_copy(b);
    zrt_spawn(producer_rendezvous, b);
    while (zrt_chan_recv(b, &out) == 0) { printf("B=%ld\n", out); }
    printf("B-closed err=%d\n", zrt_chan_close_err(b).kind != ZRT_ERR_STOP_ITERATION);
    zrt_chan_release(b);

    /* C: crashing last sender closes with a crash Err. */
    zrt_chan *c = zrt_chan_new(sizeof(long), 0);
    zrt_chan_copy(c);
    zrt_spawn(producer_crash, c);
    int r1 = zrt_chan_recv(c, &out);
    printf("C=%ld r=%d\n", out, r1);
    int r2 = zrt_chan_recv(c, &out);
    printf("C-crash r=%d err=%s kind=%d\n", r2, zrt_chan_close_err(c).msg, zrt_chan_close_err(c).kind);
    zrt_chan_release(c);
}

int main(void) {
    return zrt_sched_main_nil(prog_main);
}
`

// schedSmokeC spawns two coroutines from a nil main and waits for both through a
// channel. Waiting is not ceremony: main returning ends the program, so a coroutine
// that must finish has to be driven to a channel-observed completion — this is the
// shape every concurrent Zerg program now has to take.
const schedSmokeC = `
#include "zergrt.h"
#include <stdio.h>

typedef struct { zrt_chan *ch; long id; } co_env;

static void co_send(void *env) {
    co_env *e = (co_env *)env;
    zrt_chan_send(e->ch, &e->id);
    zrt_chan_sender_release(e->ch);
    zrt_free(e);
}

static void spawn_sender(zrt_chan *ch, long id) {
    co_env *e = (co_env *)zrt_alloc(sizeof(co_env));
    e->ch = zrt_chan_sender_copy(ch);
    e->id = id;
    zrt_spawn(co_send, e);
}

static void prog_main(void) {
    printf("main\n");
    zrt_chan *ch = zrt_chan_new(sizeof(long), 2);
    spawn_sender(ch, 1);
    spawn_sender(ch, 2);
    zrt_chan_copy(ch);
    zrt_chan_sender_release(ch); /* main keeps a receive-only hold */

    long v, ran = 0, sum = 0;
    while (zrt_chan_recv(ch, &v) == 0) { ran++; sum += v; }
    printf("ran=%ld sum=%ld\n", ran, sum);
    zrt_chan_release(ch);
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

// TestConcurrencyStress hammers the M:N scheduler with far more coroutines than
// workers, all contending on a few channels, and checks the ONE thing a race would
// disturb: the arithmetic. Every producer sends a known set of values and every
// consumer adds what it receives, so the total is fixed no matter which worker ran
// which coroutine or in what order — a lost wake-up, a doubly-queued coroutine, or a
// hand-off delivered twice all show up as a wrong sum or a hang.
//
// It is deliberately run many times: a race that survives one pass is common, and one
// that survives thirty is rarer. This is evidence, not proof.
//
// And it is run with one worker as well as with the machine's own count. That mode used
// to be untested here, and it was hiding a livelock: with several channels closing at
// different times, `select` kept re-scanning instead of parking, and the coroutines that
// would have unstuck it were sitting runnable behind the one worker that never yielded.
// Several workers hid it, because another worker ran them.
func TestConcurrencyStress(t *testing.T) {
	bin := buildConcurrent(t, "stress", stressC)

	const runs = 30
	want := "sum=49500 closed=10\n"
	for _, w := range workerModes {
		for i := 0; i < runs; i++ {
			out, code := runBounded(t, bin, w, 30*time.Second)
			if out != want || code != 0 {
				t.Fatalf("stress run %d (ZRT_WORKERS=%q) = %q exit=%d, want %q exit=0", i, w, out, code, want)
			}
		}
	}
}

// stressC: 10 producers over 4 channels, each sending 0..99, and one consumer draining
// them all through a select until every channel has closed. 10*(0+..+99) = 49500.
const stressC = `
#include "zergrt.h"
#include <stdio.h>

#define NCHAN 4
#define NPROD 10
#define NVALS 100

static zrt_chan *chans[NCHAN];
static long total;

typedef struct { zrt_chan *ch; } prod_env;

static void producer(void *env) {
    prod_env *e = (prod_env *)env;
    for (long v = 0; v < NVALS; v++) {
        zrt_chan_send(e->ch, &v);
    }
    zrt_chan_sender_release(e->ch);   /* this producer's handle */
    zrt_free(e);
}

static void prog(void) {
    for (int i = 0; i < NCHAN; i++) {
        chans[i] = zrt_chan_new(sizeof(long), 8);
    }
    for (int p = 0; p < NPROD; p++) {
        prod_env *e = (prod_env *)zrt_alloc(sizeof(prod_env));
        e->ch = zrt_chan_sender_copy(chans[p % NCHAN]);
        zrt_spawn(producer, e);
    }
    /* main gives up its own sender on each channel, keeping the holder */
    for (int i = 0; i < NCHAN; i++) {
        zrt_chan_copy(chans[i]);
        zrt_chan_sender_release(chans[i]);
    }

    long vals[NCHAN];
    for (;;) {
        zrt_sel_case cs[NCHAN];
        for (int i = 0; i < NCHAN; i++) {
            cs[i].op = ZRT_SEL_RECV;
            cs[i].ch = chans[i];
            cs[i].val = &vals[i];
        }
        int pick = zrt_select(cs, NCHAN, false, true);
        if (pick == ZRT_SEL_DONE) {
            break;
        }
        /* an arm that fires has a VALUE: a clean close drops the arm instead of firing it */
        total += vals[pick];
    }
    for (int i = 0; i < NCHAN; i++) {
        zrt_chan_release(chans[i]);
    }
    printf("sum=%ld closed=%d\n", total, NPROD);
}

int main(void) { return zrt_sched_main_nil(prog); }
`

// TestMainReturnEndsProgram pins the program lifetime: main's coroutine finishing ends
// the program, and a `spawn` still in flight is abandoned where it stands rather than
// drained (docs/code/coroutine.md, Termination & deadlock). The worker here is parked on
// a channel main holds the only sender for, so under the old drain-to-empty scheduler
// this program could not terminate at all — which is why the case is worth a test rather
// than a comment.
//
// It asserts the two things that do not depend on which worker ran what: the exit code is
// main's, and the sentinel only reachable by outliving main never prints. It deliberately
// does NOT assert that the worker failed to start; a coroutine already on a CPU runs to
// its next scheduling point, and that point is a property of the machine, not the design.
func TestMainReturnEndsProgram(t *testing.T) {
	bin := buildConcurrent(t, "lifetime", lifetimeC)
	for _, w := range workerModes {
		out, code := runBounded(t, bin, w, 20*time.Second)
		if code != 7 {
			t.Errorf("ZRT_WORKERS=%q: exit = %d, want main's 7", w, code)
		}
		if !strings.Contains(out, "main-end\n") {
			t.Errorf("ZRT_WORKERS=%q: main did not run to its end: %q", w, out)
		}
		if strings.Contains(out, "worker-end") {
			t.Errorf("ZRT_WORKERS=%q: an abandoned coroutine outlived main: %q", w, out)
		}
	}
}

// lifetimeC: the worker announces itself on `ready` so main knows it is really running,
// then parks forever on `never`. main takes the announcement and returns 7.
const lifetimeC = `
#include "zergrt.h"
#include <stdio.h>

typedef struct { zrt_chan *ready; zrt_chan *never; } wenv;

static void worker(void *env) {
    wenv *e = (wenv *)env;
    long v = 1;
    zrt_chan_send(e->ready, &v);
    zrt_chan_sender_release(e->ready);
    zrt_chan_recv(e->never, &v);   /* main holds the only sender: this never returns */
    printf("worker-end\n");        /* reachable only by outliving main */
}

static int64_t prog_main(void) {
    zrt_chan *ready = zrt_chan_new(sizeof(long), 0);
    zrt_chan *never = zrt_chan_new(sizeof(long), 0);
    wenv *e = (wenv *)zrt_alloc(sizeof(wenv));
    e->ready = zrt_chan_sender_copy(ready);
    e->never = zrt_chan_copy(never);   /* receive-only: adds no sender */
    zrt_spawn(worker, e);

    long v;
    zrt_chan_recv(ready, &v);      /* the worker is live and about to park */
    printf("main-end\n");
    return 7;
}

int main(void) { return zrt_sched_main_int(prog_main); }
`

// TestDeadlockIsACleanAbort covers DeadlockError as the spec writes it: an abort like any
// other, so it unwinds, runs the pending `defer`s, and a `guard` catches it with the
// DeadlockError kind rather than the process dying uncatchably where it stood.
//
// The second half is the part that would otherwise only show up as memory corruption. The
// scheduler resumes a PARKED coroutine to carry the raise, and that coroutine's waiter is
// a struct on the stack the abort is about to unwind — so the raise must take it back out
// of the channel's queue first. A dangling waiter is invisible until something hands off
// to it, which is why the driver goes on to send one value: it lands in the SECOND
// receive's variable, or it lands in a dead frame and the receive never completes.
func TestDeadlockIsACleanAbort(t *testing.T) {
	t.Run("caught", func(t *testing.T) {
		bin := buildConcurrent(t, "deadlock_caught", deadlockCaughtC)
		want := "caught kind=7\nsecond-recv r=0 v=42\ndefer ran\n"
		for _, w := range workerModes {
			out, code := runBounded(t, bin, w, 20*time.Second)
			if out != want || code != 0 {
				t.Errorf("ZRT_WORKERS=%q: out=%q exit=%d, want %q exit=0", w, out, code, want)
			}
		}
	})
	t.Run("uncaught", func(t *testing.T) {
		bin := buildConcurrent(t, "deadlock_fatal", deadlockFatalC)
		want := "before\ndefer ran\n"
		for _, w := range workerModes {
			out, code := runBounded(t, bin, w, 20*time.Second)
			if out != want || code != 1 {
				t.Errorf("ZRT_WORKERS=%q: out=%q exit=%d, want %q exit=1", w, out, code, want)
			}
		}
	})
}

// deadlockCaughtC: main receives on a channel it holds the only sender for, so nothing can
// ever hand off — the detector resumes main to raise, a guard catches it, and the program
// carries on. The receive AFTER the catch is the regression: it uses a different target
// variable from the first, so a waiter left behind by the raise would take the value into
// the dead frame and this receive would hang instead of answering 42.
const deadlockCaughtC = `
#include "zergrt.h"
#include <stdio.h>
#include <setjmp.h>

static void say_defer(void *env) { (void)env; printf("defer ran\n"); }

static void sender(void *env) {
    zrt_chan *ch = (zrt_chan *)env;
    long v = 42;
    zrt_chan_send(ch, &v);
    zrt_chan_sender_release(ch);
}

static void prog(void) {
    zrt_chan *ch = zrt_chan_new(sizeof(long), 0);
    zrt_defer(say_defer, NULL);

    long first = 0;
    zrt_frame f;
    zrt_handler_push_catch(&f);
    if (setjmp(f.buf) == 0) {
        zrt_chan_recv(ch, &first);   /* nobody can ever send: the detector raises here */
        zrt_handler_pop(&f);
        printf("no raise (WRONG)\n");
        return;
    }
    zrt_handler_pop(&f);
    printf("caught kind=%d\n", zrt_taken_err().kind);

    long second = 0;
    zrt_spawn(sender, zrt_chan_sender_copy(ch));
    int r = zrt_chan_recv(ch, &second);
    printf("second-recv r=%d v=%ld\n", r, second);
    zrt_chan_release(ch);
}

int main(void) { return zrt_sched_main_nil(prog); }
`

// deadlockFatalC: the same deadlock with no guard over it. The abort still unwinds — the
// defer runs — and main dying ends the program with main's non-zero outcome, which is the
// exit code the old hard-exit produced, now reached the clean way. A second coroutine is
// parked alongside so the deadlock is a real cycle rather than a lone coroutine.
const deadlockFatalC = `
#include "zergrt.h"
#include <stdio.h>

static void say_defer(void *env) { (void)env; printf("defer ran\n"); }

static void sleeper(void *env) {
    zrt_chan *ch = (zrt_chan *)env;
    long v;
    zrt_chan_recv(ch, &v);
    printf("sleeper woke (WRONG)\n");
}

static void prog(void) {
    zrt_chan *other = zrt_chan_new(sizeof(long), 0);
    zrt_spawn(sleeper, zrt_chan_sender_copy(other));

    zrt_chan *ch = zrt_chan_new(sizeof(long), 0);
    zrt_defer(say_defer, NULL);
    printf("before\n");
    fflush(stdout);

    long v;
    zrt_chan_recv(ch, &v);
    printf("after (WRONG)\n");
}

int main(void) { return zrt_sched_main_nil(prog); }
`

// TestBuiltinAbortsNameTheirKind pins the message shape the abort contract makes normative
// (docs/conformance.md, "Runtime abort contract"): a built-in error's message is
// `Kind: text`, where the text is the runtime's to word but the `Kind:` prefix is not. Every
// other zrt_abort_kind site was written that way from the start; the two the concurrency
// work added were not, and a taxonomy error whose own message will not say which kind it is
// makes the conformance claim false for the reader who only ever sees stderr.
func TestBuiltinAbortsNameTheirKind(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		prefix string
	}{
		{"send_on_closed", sendOnClosedC, "SendOnClosedError: "},
		{"deadlock", deadlockFatalC, "DeadlockError: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin := buildConcurrent(t, tc.name, tc.src)
			for _, w := range workerModes {
				_, errOut, code := runBoundedStreams(t, bin, w, 20*time.Second)
				if code != 1 {
					t.Errorf("ZRT_WORKERS=%q: exit=%d, want 1", w, code)
				}
				// The prefix is the assertion, not the whole line: the text after it is
				// explicitly not normative, and a second detection can add a line below.
				if !strings.HasPrefix(errOut, tc.prefix) {
					t.Errorf("ZRT_WORKERS=%q: stderr=%q, want it to start %q", w, errOut, tc.prefix)
				}
				if strings.TrimSpace(strings.TrimPrefix(errOut, tc.prefix)) == "" {
					t.Errorf("ZRT_WORKERS=%q: stderr=%q is a kind with no text", w, errOut)
				}
			}
		})
	}
}

// TestRaisedErrRendersItsKind pins the other half of that contract: the `Kind: ` prefix is
// a property of the abort LINE and not of the message, so an Err a program built and raised
// itself reports exactly as an intrinsic one does.
//
// The two paths used to differ, and the difference was invisible from here because every
// runtime literal spelled the prefix into itself — `zrt_abort_kind(ZRT_ERR_INDEX,
// "IndexError: index out of range")`. A program's own `raise ValueError("bad input")` built
// the same kind with a message that had no prefix in it, so it reached stderr as `bad
// input` and docs/conformance.md's claim held only for errors the runtime raised itself.
//
// The untyped case is the reason the prefix cannot simply always be there: a bare
// `raise "…"` builds an Err with NO kind, and there is nothing to name.
func TestRaisedErrRendersItsKind(t *testing.T) {
	cases := []struct {
		name string
		stmt string
		want string
	}{
		{"constructed", `zrt_raise_err(zrt_err_new_kind(ZRT_ERR_VALUE, "bad input"));`, "ValueError: bad input\n"},
		{"intrinsic", `zrt_abort_kind(ZRT_ERR_INDEX, "index out of range");`, "IndexError: index out of range\n"},
		{"untyped", `zrt_abort("boom");`, "boom\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin := buildCore(t, "raise_"+tc.name, fmt.Sprintf(abortLineC, tc.stmt))
			_, errOut, code := runBoundedStreams(t, bin, "", 20*time.Second)
			if code != 1 {
				t.Errorf("exit=%d, want 1", code)
			}
			// The WHOLE line, unlike the concurrency cases above: these messages are
			// this test's own, so there is no non-normative text to leave room for.
			if errOut != tc.want {
				t.Errorf("stderr=%q, want %q", errOut, tc.want)
			}
		})
	}
}

// abortLineC aborts once, with no handler anywhere: the last-resort arm of the abort core,
// which is the shape a program that raises out of main has.
const abortLineC = `
#include "zergrt.h"

int main(void) {
    %s
    return 0;
}
`

// sendOnClosedC: main gives up the only send end, which closes the channel cleanly, then
// sends on the receive handle it kept. Nothing can take that value and nothing ever will,
// so the send is the dead letter the abort is for. The receive handle is also what keeps
// the channel alive across the release — sender_release drops the refcount too, and the
// last drop frees it.
const sendOnClosedC = `
#include "zergrt.h"
#include <stdio.h>

static void prog(void) {
    zrt_chan *ch = zrt_chan_new(sizeof(long), 1);
    zrt_chan *rx = zrt_chan_copy(ch);   /* a receive handle: refcount only, not a sender */
    zrt_chan_sender_release(ch);        /* the last sender leaves: the channel closes clean */
    printf("before\n");
    fflush(stdout);

    long v = 1;
    zrt_chan_send(rx, &v);
    printf("after (WRONG)\n");
}

int main(void) { return zrt_sched_main_nil(prog); }
`

// TestSelectResolvesClosedChannels pins how `select` ends when the channels it watches
// stop producing. The three answers follow the language's own split between an ABSENCE and
// a FAILURE (docs/code/coroutine.md, the `select` section):
//
//   - every watched receive channel closed CLEANLY, with no `close` arm and no `_`: each
//     arm is an absence, so each is dropped from the wait, and a select with nothing left
//     to wait for is waiting for something that cannot happen -> DeadlockError;
//   - a channel closed by a CRASH is a failure, so it is RAISED carrying the producer's own
//     Err, message and kind and all. It is never handed to an arm, which is what makes it
//     impossible for a receiver to run over it without noticing;
//   - a `_` arm does NOT absorb an exhausted select: "nothing ready yet" would be a lie
//     once nothing can ever be ready, and a loop around that lie spins. The clean-close
//     case therefore still raises, `_` or no `_`.
func TestSelectResolvesClosedChannels(t *testing.T) {
	bin := buildConcurrent(t, "select_closed", selectClosedC)
	want := "all-clean: kind=7\ncrash: msg=producer died kind=3\ndefault: kind=7\n"
	for _, w := range workerModes {
		out, code := runBounded(t, bin, w, 20*time.Second)
		if out != want || code != 0 {
			t.Errorf("ZRT_WORKERS=%q: out=%q exit=%d, want %q exit=0", w, out, code, want)
		}
	}
}

// selectClosedC drives the three closure answers in turn. `quiet` ends normally so its
// channel closes clean; `crasher` aborts with a kind, so its channel closes carrying that
// very Err.
const selectClosedC = `
#include "zergrt.h"
#include <stdio.h>
#include <setjmp.h>

static void chan_sender_drop(void *slot) {
    zrt_chan **s = (zrt_chan **)slot;
    if (*s != NULL) { zrt_chan_sender_release(*s); }
}

static void quiet(void *env) { zrt_chan_sender_release((zrt_chan *)env); }

static void crasher(void *env) {
    zrt_chan *ch = (zrt_chan *)env;
    zrt_defer(chan_sender_drop, &ch);   /* released on the crash unwind, closing ch */
    zrt_abort_kind(ZRT_ERR_IO, "producer died");
}

/* hand the channel to a coroutine and give up main's own sender, so the coroutine
 * leaving is what closes it. */
static zrt_chan *closed_by(void (*body)(void *)) {
    zrt_chan *ch = zrt_chan_new(sizeof(long), 0);
    zrt_spawn(body, zrt_chan_sender_copy(ch));
    zrt_chan_copy(ch);
    zrt_chan_sender_release(ch);
    return ch;
}

/* wait_shut blocks until ch is closed. Each case below is about how a select resolves
 * once every arm has shut, and without this the select can run while one producer has
 * not been scheduled yet — it would then answer the arm that HAS closed, correctly, and
 * the test would be asserting a different question than the one it means to ask. A
 * receive on a channel with no values does exactly this wait: it returns only on close. */
static void wait_shut(zrt_chan *ch) {
    long v;
    zrt_chan_recv(ch, &v);
}

static void case_all_clean(void) {
    zrt_chan *a = closed_by(quiet), *b = closed_by(quiet);
    wait_shut(a); wait_shut(b);
    long v;
    zrt_sel_case cs[2] = {{ZRT_SEL_RECV, a, &v}, {ZRT_SEL_RECV, b, &v}};
    zrt_frame f;
    zrt_handler_push_catch(&f);
    if (setjmp(f.buf) == 0) {
        int pick = zrt_select(cs, 2, false, false);   /* no close arm, no _ */
        zrt_handler_pop(&f);
        printf("all-clean: no raise, pick=%d (WRONG)\n", pick);
    } else {
        zrt_handler_pop(&f);
        printf("all-clean: kind=%d\n", zrt_taken_err().kind);
    }
    zrt_chan_release(a); zrt_chan_release(b);
}

/* the crash is RAISED, so it is caught rather than returned */
static void case_crash(void) {
    zrt_chan *a = closed_by(quiet), *b = closed_by(crasher);
    wait_shut(a); wait_shut(b);
    long v;
    zrt_sel_case cs[2] = {{ZRT_SEL_RECV, a, &v}, {ZRT_SEL_RECV, b, &v}};
    zrt_frame f;
    zrt_handler_push_catch(&f);
    if (setjmp(f.buf) == 0) {
        int pick = zrt_select(cs, 2, false, false);
        zrt_handler_pop(&f);
        printf("crash: no raise, pick=%d (WRONG)\n", pick);
    } else {
        zrt_handler_pop(&f);
        zrt_err e = zrt_taken_err();
        printf("crash: msg=%s kind=%d\n", e.msg, e.kind);
    }
    zrt_chan_release(a); zrt_chan_release(b);
}

/* the non-blocking arm is about "nothing ready YET", so it does not answer an
 * exhausted select: once nothing can be ready, that answer would be a lie */
static void case_default(void) {
    zrt_chan *a = closed_by(quiet);
    wait_shut(a);
    long v;
    zrt_sel_case cs[1] = {{ZRT_SEL_RECV, a, &v}};
    zrt_frame f;
    zrt_handler_push_catch(&f);
    if (setjmp(f.buf) == 0) {
        int pick = zrt_select(cs, 1, true, false);
        zrt_handler_pop(&f);
        printf("default: no raise, pick=%d (WRONG)\n", pick);
    } else {
        zrt_handler_pop(&f);
        printf("default: kind=%d\n", zrt_taken_err().kind);
    }
    zrt_chan_release(a);
}

static void prog(void) { case_all_clean(); case_crash(); case_default(); }
int main(void) { return zrt_sched_main_nil(prog); }
`

// TestTimerParksAndWakes covers the timer leaf the stdlib's `after`/`ticker` are built on.
// The driver is `after(d)` written in C — a coroutine that sleeps and then sends — because
// that is the whole shape of the feature: the runtime owns only the sleep, and a timer is
// a channel like any other.
//
// Three assertions, one per requirement. The select fires on the timer arm rather than
// raising, which proves a pending sleep suspends the deadlock detector — the arm it waits
// beside can never produce, so without that suspension this program would abort instead of
// waiting. The elapsed time is at least the requested one, so the sleep is a deadline and
// not a yield. And the CPU burned is a small fraction of the time waited, which is the
// no-busy-wait requirement stated as something a test can actually see.
func TestTimerParksAndWakes(t *testing.T) {
	bin := buildConcurrent(t, "timer", timerC)
	want := "pick=1 waited-full=1 cpu-under-tenth=1\n"
	for _, w := range workerModes {
		out, code := runBounded(t, bin, w, 30*time.Second)
		if out != want || code != 0 {
			t.Errorf("ZRT_WORKERS=%q: out=%q exit=%d, want %q exit=0", w, out, code, want)
		}
	}
}

const timerC = `
#include "zergrt.h"
#include <stdio.h>
#include <sys/resource.h>

#define SLEEP_MS 200
#define SLEEP_NS ((int64_t)SLEEP_MS * 1000000)

typedef struct { zrt_chan *ch; int64_t ns; } tenv;

/* stdlib after(d), in C: sleep the duration, then hand one value to the channel and let
 * go of it. Unit 5 writes this in Zerg over the same leaf. */
static void after_body(void *env) {
    tenv *e = (tenv *)env;
    zrt_sleep_ns(e->ns);
    long v = 1;
    zrt_chan_send(e->ch, &v);
    zrt_chan_sender_release(e->ch);
    zrt_free(e);
}

static long cpu_ms(void) {
    struct rusage r;
    getrusage(RUSAGE_SELF, &r);
    return (r.ru_utime.tv_sec + r.ru_stime.tv_sec) * 1000
         + (r.ru_utime.tv_usec + r.ru_stime.tv_usec) / 1000;
}

static void prog(void) {
    zrt_chan *timer = zrt_chan_new(sizeof(long), 1);
    zrt_chan *work = zrt_chan_new(sizeof(long), 1);  /* main holds its sender: never fires */
    tenv *e = (tenv *)zrt_alloc(sizeof(tenv));
    e->ch = zrt_chan_sender_copy(timer);
    e->ns = SLEEP_NS;
    zrt_spawn(after_body, e);
    zrt_chan_copy(timer);
    zrt_chan_sender_release(timer);

    int64_t t0 = zrt_time_mono();
    long c0 = cpu_ms();
    long v;
    zrt_sel_case cs[2] = {{ZRT_SEL_RECV, work, &v, 0}, {ZRT_SEL_RECV, timer, &v, 0}};
    int pick = zrt_select(cs, 2, false, false);   /* no done, no _: a deadlock would raise */
    long waited = (long)((zrt_time_mono() - t0) / 1000000);
    long burned = cpu_ms() - c0;

    /* the clock is allowed a little slack either way; the CPU budget is a tenth of the
     * wait, which a spin would blow through by two orders of magnitude. */
    printf("pick=%d waited-full=%d cpu-under-tenth=%d\n",
           pick, waited + 5 >= SLEEP_MS, burned < SLEEP_MS / 10);
    zrt_chan_release(timer);
    zrt_chan_release(work);
}

int main(void) { return zrt_sched_main_nil(prog); }
`

// TestRefcountIsAtomic hammers Ref headers from real pthreads and asserts the counts they
// land on. A `spawn` hands a `Ref[T]` — and, since S2, every managed `str` — to a coroutine
// another worker thread may run, so two threads reach the same header.
//
// It asserts the COUNT rather than waiting for a sanitizer to catch the use-after-free a
// lost update eventually causes: a wrong number is deterministic where a timing window is
// not. The shape is many SHORT races rather than one long one, which is what makes it a
// gate — against the non-atomic count, ten cells of two thousand rounds each detects the bug
// 40 times out of 40, while a single cell of two hundred thousand missed it 3 times in 40.
// A probe that passes on broken code one run in thirteen is not one.
func TestRefcountIsAtomic(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	dir := t.TempDir()
	cfiles, err := Materialize(dir)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}

	driver := filepath.Join(dir, "rc_race.c")
	if err := os.WriteFile(driver, []byte(refcountRaceC), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	bin := filepath.Join(dir, "rc_race.bin")
	args := append([]string{"-std=c11", "-I", dir, "-pthread", "-o", bin, driver}, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s", err, out)
	}

	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("rc race run failed: %v\n%s", err, out)
	}
	const want = "retain-balance=ok\nimmortal=ok\ndrop-once=ok\n"
	if string(out) != want {
		t.Fatalf("refcount race = %q, want %q — a lost update means the count is not atomic", out, want)
	}
}

// refcountRaceC races THREADS threads over CELLS cells, each thread balancing every retain
// with a release, so a correct count returns to exactly 1 and no drop has run. It then
// checks the two things the fast path must keep: an immortal cell is never counted or
// freed, and the last release drops each cell exactly once.
const refcountRaceC = `
#include "zergrt.h"
#include <pthread.h>
#include <stdio.h>

#define THREADS 4
#define CELLS   10
#define ROUNDS  2000

static int g_drops;
static void count_drop(void *payload) { (void)payload; g_drops++; }

static void *hammer(void *arg) {
    void *ref = arg;
    for (int i = 0; i < ROUNDS; i++) {
        zrt_retain(ref);
        zrt_release(ref);
    }
    return NULL;
}

/* Shaped like the cell the BACKEND emits for a string literal — both emitters write a
 * struct of a zrt_ref_hdr followed by the bytes, brace-initialized with the sentinel — so
 * what is hammered here is the layout that actually exists, not a header on its own. */
static struct { zrt_ref_hdr h; char b[8]; } g_immortal = {{ZRT_RC_IMMORTAL, NULL}, {0}};

int main(void) {
    pthread_t th[THREADS];

    /* Many short races rather than one long one: each cell is a fresh window, and a lost
     * update anywhere leaves that cell off 1. */
    void *cells[CELLS];
    for (int c = 0; c < CELLS; c++) { cells[c] = zrt_ref_alloc(sizeof(int), count_drop); }
    for (int c = 0; c < CELLS; c++) {
        for (int i = 0; i < THREADS; i++) { pthread_create(&th[i], NULL, hammer, cells[c]); }
        for (int i = 0; i < THREADS; i++) { pthread_join(th[i], NULL); }
    }

    int balanced = (g_drops == 0);
    for (int c = 0; c < CELLS; c++) { balanced = balanced && ((zrt_ref_hdr *)cells[c])->rc == 1; }
    printf("retain-balance=%s\n", balanced ? "ok" : "LOST");

    for (int i = 0; i < THREADS; i++) { pthread_create(&th[i], NULL, hammer, &g_immortal); }
    for (int i = 0; i < THREADS; i++) { pthread_join(th[i], NULL); }
    printf("immortal=%s\n", g_immortal.h.rc == ZRT_RC_IMMORTAL ? "ok" : "TOUCHED");

    for (int c = 0; c < CELLS; c++) { zrt_release(cells[c]); }
    printf("drop-once=%s\n", g_drops == CELLS ? "ok" : "WRONG");
    return 0;
}
`

// TestFormatSpecIsBounded drives the `:spec` formatters with SPECS A PROGRAM CAN WRITE and
// asserts the runtime answers rather than dies. `zrt_fmt_float` builds a printf pattern out
// of the spec's own bytes — the precision digits and the trailing type letter — and then
// hands it a `double`, so a spec is a format string a caller controls:
//
//	f"{x:.6s}"   a double read as a `char *`      — a wild pointer dereference
//	f"{x:.6n}"   %n, which WRITES through its argument — a write-what-where primitive
//
// Both are reachable from ordinary source; both crashed. The digits are the other half:
// width and precision accumulate into a `long` with no bound, so twenty of them are signed
// overflow, and a precision wide enough truncates the pattern buffer into a conversion that
// is no longer one.
//
// It runs the hostile specs as SEPARATE PROCESSES because the answer to one is an abort:
// what the gate pins is that the abort is the runtime's own, by name, and not a signal —
// the same distinction scripts/reject-check.sh draws between a diagnostic and a crash. The
// valid specs run together and their output is pinned exactly, because a bound that also
// broke formatting would pass a test that only asked about the crash.
func TestFormatSpecIsBounded(t *testing.T) {
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	dir := t.TempDir()
	cfiles, err := Materialize(dir)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}

	driver := filepath.Join(dir, "fmt_spec.c")
	if err := os.WriteFile(driver, []byte(fmtSpecC), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	bin := filepath.Join(dir, "fmt_spec.bin")
	args := append(append([]string{"-std=c11", "-I", dir, "-o", bin, driver}, mapSanFlags...), cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s", err, out)
	}

	out, err := exec.Command(bin, "valid").Output()
	if err != nil {
		t.Fatalf("valid specs run failed: %v\n%s", err, out)
	}
	const want = "f-2=1.50\n" +
		"e-3=+1.500e+00\n" +
		"zero=00001.50\n" +
		"right=    1.50\n" +
		"int-hex=0xff\n" +
		"str-pad=  abc\n" +
		"char-ascii=A\n" +
		"char-wide=Ĭ\n" +
		"str-trunc=abcde\n"
	if string(out) != want {
		t.Fatalf("valid specs = %q, want %q — a bound that breaks formatting is not one", out, want)
	}

	// Each of these is a spec a program can write, and each must end the run the way every
	// other runtime refusal does: a ValueError on stderr and an ordinary exit status. A
	// SIGNAL here is the bug this test exists for.
	for _, spec := range []string{"type-s", "type-n", "wide-prec", "huge-width", "huge-prec", "char-past-max", "char-surrogate", "char-nul", "char-negative", "float-prec"} {
		cmd := exec.Command(bin, spec)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			t.Errorf("%s: the runtime accepted a spec it cannot render", spec)
			continue
		}
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Errorf("%s: %v", spec, err)
			continue
		}
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			t.Errorf("%s: died of signal %v instead of refusing by name\n%s", spec, ws.Signal(), stderr.String())
			continue
		}
		if !strings.Contains(stderr.String(), "ValueError") {
			t.Errorf("%s: wanted a ValueError, got: %s", spec, stderr.String())
		}
	}
}

// fmtSpecC formats one thing per argv selector, so the four specs that must end the run can
// each be a run of its own. The valid set is deliberately one of each shape the formatters
// take a different path for: a fixed float, a signed exponent, the sign-aware zero pad, a
// plain right pad, an integer with a base prefix, and a padded string.
const fmtSpecC = `
#include "zergrt.h"
#include <stdio.h>
#include <string.h>

static void say(const char *k, const char *v) { printf("%s=%s\n", k, v); zrt_str_release(v); }

int main(int argc, char **argv) {
    const char *what = argc > 1 ? argv[1] : "valid";
    if (strcmp(what, "valid") == 0) {
        say("f-2",     zrt_fmt_float(1.5, ".2f"));
        say("e-3",     zrt_fmt_float(1.5, "+.3e"));
        say("zero",    zrt_fmt_float(1.5, "08.2f"));
        say("right",   zrt_fmt_float(1.5, ">8.2f"));
        say("int-hex", zrt_fmt_int(255, "#x"));
        say("str-pad", zrt_fmt_str("abc", ">5"));
        say("char-ascii", zrt_fmt_int(65, "c"));
        say("char-wide", zrt_fmt_int(300, "c"));
        say("str-trunc", zrt_fmt_str("abcdefghij", ".5"));
        return 0;
    }
    if (strcmp(what, "type-s")     == 0) { say("out", zrt_fmt_float(1.5, ".6s")); }
    if (strcmp(what, "type-n")     == 0) { say("out", zrt_fmt_float(1.5, ".6n")); }
    if (strcmp(what, "wide-prec")  == 0) { say("out", zrt_fmt_float(1.5, ".99999999f")); }
    if (strcmp(what, "huge-width") == 0) { say("out", zrt_fmt_float(1.5, "99999999999999999999f")); }
    if (strcmp(what, "huge-prec")  == 0) { say("out", zrt_fmt_float(1.5, ".99999999999999999999f")); }
    if (strcmp(what, "char-past-max")  == 0) { say("out", zrt_fmt_int(1114112, "c")); }
    if (strcmp(what, "char-surrogate") == 0) { say("out", zrt_fmt_int(0xD800, "c")); }
    if (strcmp(what, "char-nul")       == 0) { say("out", zrt_fmt_int(0, "c")); }
    if (strcmp(what, "char-negative")  == 0) { say("out", zrt_fmt_int(-1, "c")); }
    if (strcmp(what, "float-prec")     == 0) { say("out", zrt_fmt_float(1.5, ".200f")); }
    return 0;
}
`

// TestRuntimeFormatsAreLiterals asserts that no printf-family call in the runtime takes a
// format string built at run time. That is the CLASS the float formatter's crash belonged
// to, rather than the crash itself: a pattern assembled from a spec's own bytes is a format
// string the program chose, and every such call is one `%n` away from a write primitive.
//
// It reads the sources rather than running anything, because a call that no input reaches
// today is still a call the next input might. The check is deliberately shape-based and
// strict — the format argument must OPEN with a quote — so the way to satisfy it is to write
// the literal at the call site, which is also the way to make it true.
func TestRuntimeFormatsAreLiterals(t *testing.T) {
	dir := t.TempDir()
	cfiles, err := Materialize(dir)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	// The map is the whole statement: which functions take a format, and which argument it
	// is. The regex finds candidates loosely — anything ending in `printf`, plus syslog —
	// and a name the map does not know is skipped, so adding one is one line in one place
	// rather than two lists to keep in step.
	call := regexp.MustCompile(`\b(\w*printf|syslog)\s*\(`)
	skip := map[string]int{
		"printf": 0, "vprintf": 0, "asprintf": 1, "vasprintf": 1,
		"snprintf": 2, "vsnprintf": 2, "sprintf": 1, "vsprintf": 1,
		"fprintf": 1, "vfprintf": 1, "dprintf": 1, "syslog": 1,
	}
	for _, c := range cfiles {
		src, err := os.ReadFile(c)
		if err != nil {
			t.Fatalf("read %s: %v", c, err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			m := call.FindStringSubmatchIndex(line)
			if m == nil || strings.HasPrefix(strings.TrimSpace(line), "*") || strings.HasPrefix(strings.TrimSpace(line), "/*") {
				continue
			}
			name := line[m[2]:m[3]]
			args := splitArgs(line[m[1]:])
			n, ok := skip[name]
			if !ok {
				continue // not a call that takes a format
			}
			if len(args) <= n {
				// A CALL THIS CANNOT READ IS A FINDING, not a pass. Skipping one silently is
				// how a gate comes to depend on how the sources happen to be wrapped: the
				// day a call spans two lines it stops being checked and nothing says so.
				t.Errorf("%s:%d: %s spans lines, so its format cannot be read here: %s",
					filepath.Base(c), i+1, name, strings.TrimSpace(line))
				continue
			}
			if !strings.HasPrefix(strings.TrimSpace(args[n]), `"`) {
				t.Errorf("%s:%d: %s takes a format that is not a literal: %s",
					filepath.Base(c), i+1, name, strings.TrimSpace(line))
			}
		}
	}
}

// splitArgs splits an argument list at top-level commas, ignoring the ones inside nested
// parentheses or inside a string literal. It stops at the closing paren of the call.
func splitArgs(s string) []string {
	var out []string
	depth, start, inStr := 0, 0, false
	for i := 0; i < len(s); i++ {
		switch {
		case inStr:
			if s[i] == '\\' {
				i++
			} else if s[i] == '"' {
				inStr = false
			}
		case s[i] == '"':
			inStr = true
		case s[i] == '(' || s[i] == '[':
			depth++
		case s[i] == ')' && depth == 0:
			return append(out, s[start:i])
		case s[i] == ')' || s[i] == ']':
			depth--
		case s[i] == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// buildCore compiles a C driver against the core runtime only — no scheduler — and
// returns the binary: the shape of a non-concurrent program, whose user code runs on
// main's native stack through the entry.c shims.
func buildCore(t *testing.T, name, src string) string {
	t.Helper()
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	dir := t.TempDir()
	cfiles, err := Materialize(dir)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	driver := filepath.Join(dir, name+".c")
	if err := os.WriteFile(driver, []byte(src), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	bin := filepath.Join(dir, name+".bin")
	args := append([]string{"-std=c11", "-I", dir, "-o", bin, driver}, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s", err, out)
	}
	return bin
}

// TestStackOverflowNamedOnMainStack pins the narrowed runtime-abort deviation
// (docs/conformance.md): a runaway recursion on main's native stack no longer dies as
// a bare signal. The fault is named — `StackOverflowError: stack overflow` on stderr —
// and the process exits 1, the message-then-status shape every other abort has. The
// pending `defer`s are still skipped; that half of the deviation stands, and nothing
// here asserts otherwise.
func TestStackOverflowNamedOnMainStack(t *testing.T) {
	bin := buildCore(t, "so_main", stackOverflowMainC)
	_, errOut, code := runBoundedStreams(t, bin, "", 20*time.Second)
	if code != 1 {
		t.Fatalf("main-stack overflow exit=%d, want 1 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "StackOverflowError: stack overflow") {
		t.Fatalf("main-stack overflow stderr = %q, want the StackOverflowError line", errOut)
	}
}

const stackOverflowMainC = `
#include "zergrt.h"

/* the volatile pad forces a real frame per call, so no compiler turns the
 * recursion into a loop that never grows the stack */
static void boom(void) {
    volatile char pad[512];
    pad[0] = 1;
    boom();
}

int main(void) { return zrt_main_run_nil(boom); }
`

// TestStackOverflowNamedInCoroutine is the guard-page half of the same contract: a
// runaway recursion inside a `spawn`ed coroutine runs into its stack's PROT_NONE
// guard page, and that fault too is named and exits 1 rather than dying by signal.
func TestStackOverflowNamedInCoroutine(t *testing.T) {
	bin := buildConcurrent(t, "so_coro", stackOverflowCoroC)
	for _, w := range workerModes {
		_, errOut, code := runBoundedStreams(t, bin, w, 20*time.Second)
		if code != 1 {
			t.Fatalf("ZRT_WORKERS=%q: coroutine overflow exit=%d, want 1 (stderr %q)", w, code, errOut)
		}
		if !strings.Contains(errOut, "StackOverflowError: stack overflow") {
			t.Fatalf("ZRT_WORKERS=%q: coroutine overflow stderr = %q, want the StackOverflowError line", w, errOut)
		}
	}
}

const stackOverflowCoroC = `
#include "zergrt.h"

static void boom(void) {
    volatile char pad[512];
    pad[0] = 1;
    boom();
}

static void spawned(void *env) {
    (void)env;
    boom();
}

static void prog(void) {
    /* main parks on a channel it is itself the only sender of, so the program can
     * only end the way the spawned coroutine ends it: no deadlock is declared
     * while the recursion is RUNNING, and the overflow's _exit(1) is the exit. */
    zrt_chan *never = zrt_chan_new(sizeof(long), 0);
    zrt_spawn(spawned, NULL);
    long v;
    zrt_chan_recv(never, &v);
}

int main(void) { return zrt_sched_main_nil(prog); }
`

// TestGenuineSegvUndisguised is the other edge of the naming: a wild pointer is NOT a
// stack overflow, and the handler must not dress it as one. It restores the default
// disposition and lets the fault re-fire, so the process still dies by the genuine
// signal — observable here as a signal death, with no StackOverflowError line.
func TestGenuineSegvUndisguised(t *testing.T) {
	bin := buildCore(t, "wild", wildPointerC)
	var stderr bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("wild-pointer run: expected a signal death, got err=%v", err)
	}
	ws, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		t.Fatalf("wild-pointer run: expected a signal death, got status %d", exit.ExitCode())
	}
	if sig := ws.Signal(); sig != syscall.SIGSEGV && sig != syscall.SIGBUS {
		t.Fatalf("wild-pointer run: died by %v, want SIGSEGV or SIGBUS", sig)
	}
	if strings.Contains(stderr.String(), "StackOverflowError") {
		t.Fatalf("wild-pointer death was disguised as a stack overflow: stderr %q", stderr.String())
	}
}

const wildPointerC = `
#include "zergrt.h"

/* address 16: inside the never-mapped NULL page, far from every stack bound */
static void wild(void) { *(volatile int *)16 = 7; }

int main(void) { return zrt_main_run_nil(wild); }
`

// TestUnclaimedFaultReachesThePreviousHandler pins the CHAINING: a fault the runtime
// does not claim must reach whoever held the signal before it, not the default
// disposition. The loss this guards against is invisible in a plain build — where the
// previous action IS the default — and shows only under a sanitizer, whose SEGV report
// (`SEGV on unknown address 0x10`, with the stack trace) simply vanished when the
// handler restored SIG_DFL instead of what it replaced. No gate here runs a sanitizer
// over a faulting program, so the driver stands in for one: it installs a handler of
// its own BEFORE the runtime's, and that handler is what must run.
func TestUnclaimedFaultReachesThePreviousHandler(t *testing.T) {
	bin := buildCore(t, "chained", chainedHandlerC)
	var stdout bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Stdout = &stdout
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("unclaimed fault did not reach the previous handler: err=%v stdout=%q", err, stdout.String())
	}
	if stdout.String() != "chained\n" {
		t.Fatalf("previous handler output = %q, want %q", stdout.String(), "chained\n")
	}
}

const chainedHandlerC = `
#include "zergrt.h"
#include <signal.h>
#include <string.h>
#include <unistd.h>

/* stands in for the sanitizer's handler: whatever held SIGSEGV before the runtime did */
static void previous(int sig) {
    (void)sig;
    (void)write(1, "chained\n", 8);
    _exit(3);
}

static void wild(void) { *(volatile int *)16 = 7; }

int main(void) {
    struct sigaction sa;
    memset(&sa, 0, sizeof(sa));
    sa.sa_handler = previous;
    sigemptyset(&sa.sa_mask);
    sigaction(SIGSEGV, &sa, NULL);
    sigaction(SIGBUS, &sa, NULL);
    return zrt_main_run_nil(wild); /* installs the runtime's over this one */
}
`

// TestNearMissBelowMainStackNotClaimed measures the WIDTH of the overflow window, which
// the two tests above cannot see: they only ask whether a real overflow is named and
// whether a fault at address 16 is left alone, and both stay true however much ground
// below main's stack the runtime claims. The first version of this handler claimed 64KB
// of it, so a wild write 8 pages under the bound was reported as a StackOverflowError —
// a genuine memory bug renamed AND stripped of its sanitizer diagnostic, with nothing on
// the board able to say so.
//
// The driver faults at a deliberate distance below the bound (32KB), on a page it maps
// PROT_NONE itself so the fault is certain and its address exact. That must die as the
// signal it is: the window is ONE page, and this is eight.
func TestNearMissBelowMainStackNotClaimed(t *testing.T) {
	bin := buildCore(t, "near_miss", nearMissC)
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if strings.Contains(stdout.String(), "skip") {
		t.Skip("host answered no stack bounds, or refused the fixed mapping")
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("near-miss run: expected a signal death, got err=%v (stderr %q)", err, stderr.String())
	}
	ws, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		t.Fatalf("near-miss run: expected a signal death, got status %d (stderr %q)", exit.ExitCode(), stderr.String())
	}
	if strings.Contains(stderr.String(), "StackOverflowError") {
		t.Fatalf("a fault 32KB below main's stack was claimed as an overflow: stderr %q", stderr.String())
	}
}

const nearMissC = `
#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE 1
#endif
#if defined(__APPLE__) && !defined(_DARWIN_C_SOURCE)
#define _DARWIN_C_SOURCE 1
#endif
#ifndef _DEFAULT_SOURCE
#define _DEFAULT_SOURCE 1
#endif
#include "zergrt.h"
#include <pthread.h>
#include <stdio.h>
#include <sys/mman.h>
#include <unistd.h>

#if !defined(MAP_ANONYMOUS) && defined(MAP_ANON)
#define MAP_ANONYMOUS MAP_ANON
#endif

/* the same low bound the runtime records for main, asked the same way */
static uintptr_t stack_lo(void) {
#if defined(__APPLE__)
    pthread_t self = pthread_self();
    uintptr_t hi = (uintptr_t)pthread_get_stackaddr_np(self);
    return hi - (uintptr_t)pthread_get_stacksize_np(self);
#elif defined(__linux__)
    pthread_attr_t attr;
    uintptr_t lo = 0;
    if (pthread_getattr_np(pthread_self(), &attr) == 0) {
        void *p = NULL; size_t n = 0;
        if (pthread_attr_getstack(&attr, &p, &n) == 0) { lo = (uintptr_t)p; }
        pthread_attr_destroy(&attr);
    }
    return lo;
#else
    return 0;
#endif
}

static void near_miss(void) {
    uintptr_t lo = stack_lo();
    long pg = sysconf(_SC_PAGESIZE);
    uintptr_t page = (pg > 0) ? (uintptr_t)pg : 4096;
    if (lo == 0) { printf("skip\n"); fflush(stdout); return; }
    uintptr_t at = (lo - 32768) & ~(page - 1);
    /* map it PROT_NONE ourselves so the fault is certain and lands exactly here */
    if (mmap((void *)at, page, PROT_NONE,
             MAP_PRIVATE | MAP_ANONYMOUS | MAP_FIXED, -1, 0) == MAP_FAILED) {
        printf("skip\n"); fflush(stdout); return;
    }
    *(volatile int *)at = 7;
}

int main(void) { return zrt_main_run_nil(near_miss); }
`
