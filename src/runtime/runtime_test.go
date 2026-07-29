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

// TestConcurrencyStress hammers the M:N scheduler with far more coroutines than
// workers, all contending on a few channels, and checks the ONE thing a race would
// disturb: the arithmetic. Every producer sends a known set of values and every
// consumer adds what it receives, so the total is fixed no matter which worker ran
// which coroutine or in what order — a lost wake-up, a doubly-queued coroutine, or a
// hand-off delivered twice all show up as a wrong sum or a hang.
//
// It is deliberately run many times: a race that survives one pass is common, and one
// that survives thirty is rarer. This is evidence, not proof.
func TestConcurrencyStress(t *testing.T) {
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

	driver := filepath.Join(dir, "stress.c")
	if err := os.WriteFile(driver, []byte(stressC), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	bin := filepath.Join(dir, "stress.bin")
	args := append([]string{"-std=c11", "-I", dir, "-o", bin, driver}, cfiles...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("cc failed: %v\n%s", err, out)
	}

	const runs = 30
	want := "sum=49500 closed=10\n"
	for i := 0; i < runs; i++ {
		out, err := exec.Command(bin).CombinedOutput()
		if err != nil {
			t.Fatalf("stress run %d failed: %v\n%s", i, err, out)
		}
		if string(out) != want {
			t.Fatalf("stress run %d = %q, want %q", i, out, want)
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
static int closed_seen;

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
            cs[i].closed = 0;
        }
        int pick = zrt_select(cs, NCHAN, false, true);
        if (pick == ZRT_SEL_DONE) {
            break;
        }
        if (cs[pick].closed) {
            closed_seen++;
            continue;
        }
        total += vals[pick];
    }
    for (int i = 0; i < NCHAN; i++) {
        zrt_chan_release(chans[i]);
    }
    printf("sum=%ld closed=%d\n", total, NPROD);
}

int main(void) { return zrt_sched_main_nil(prog); }
`
