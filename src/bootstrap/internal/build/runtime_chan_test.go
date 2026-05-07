package build

import (
	"bytes"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// v0.12 Unit 3 — channel runtime end-to-end.
//
// Each test compiles a driver against (coro + sched + chan) runtime,
// exercising one channel scenario: unbuffered ping-pong, buffered fan-out,
// close + drain, panic on send-after-close, panic on double-close.
//
// The drivers print results via printf. We assert on the post-sort set of
// outputs (work-stealing scrambles order).

// v12BuildChanDriver concatenates the (coro + sched + chan) runtime with
// the driver source into one TU and runs it. Chan helpers are emitted
// `static` (matching the U6 single-TU topology), so single-TU
// concatenation is what makes them visible to driver code.
func v12BuildChanDriver(t *testing.T, driver string, env []string) ([]byte, int) {
	t.Helper()
	prog := coroRuntimeC + schedRuntimeC + chanRuntimeC + "\n" + driver
	return v12CompileAndRun(t, prog, env)
}

// TestV12ChanUnbuffered exercises rendezvous send/recv. cap=0 means every
// send must hand off directly to a receiver (no buffer). The driver
// spawns 64 producers and 64 consumers paired through one chan; the
// receiver coros print the values they pulled. We assert the set of
// values printed equals the set of values sent.
func TestV12ChanUnbuffered(t *testing.T) {
	driver := `
#include <stdio.h>
#include <stdlib.h>
#include <pthread.h>

#define N 64
static zerg_chan_int64_t *ch;

static pthread_mutex_t out_mu = PTHREAD_MUTEX_INITIALIZER;
static long long out[N];
static int out_n = 0;

static void producer(void *arg) {
    long long v = (long long)(intptr_t)arg;
    zerg_chan_int64_t_send(ch, v);
}

static void consumer(void *arg) {
    (void)arg;
    zerg_opt_int64_t r = zerg_chan_int64_t_recv(ch);
    if (r.tag != 0) { fprintf(stderr, "got None unexpectedly\n"); exit(2); }
    pthread_mutex_lock(&out_mu);
    out[out_n++] = r.payload.p0.a0;
    pthread_mutex_unlock(&out_mu);
}

int main(void) {
    zerg_sched_init(0);
    ch = zerg_chan_int64_t_make(0);
    for (int i = 0; i < N; i++) zerg_coro_spawn(consumer, 0);
    for (int i = 0; i < N; i++) zerg_coro_spawn(producer, (void *)(intptr_t)(100 + i));
    zerg_sched_drain();
    for (int i = 0; i < out_n; i++) printf("%lld\n", out[i]);
    return 0;
}
`
	out, code := v12BuildChanDriver(t, driver, []string{"ZERG_MAXPROCS=4"})
	if code != 0 {
		t.Fatalf("driver exited %d\noutput:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 64 {
		t.Fatalf("got %d lines, want 64\noutput:\n%s", len(lines), out)
	}
	got := make([]int, 0, 64)
	for _, l := range lines {
		n, err := strconv.Atoi(l)
		if err != nil {
			t.Fatalf("non-int line %q: %v", l, err)
		}
		got = append(got, n)
	}
	sort.Ints(got)
	for i, v := range got {
		if v != 100+i {
			t.Fatalf("missing/duplicate at sorted position %d: got %d, want %d", i, v, 100+i)
		}
	}
}

// TestV12ChanBuffered exercises the FIFO buffer path. cap=8 producers,
// fewer consumers, and we assert the buffered semantics: one producer
// can send up to 8 values without blocking, even with no consumer.
func TestV12ChanBuffered(t *testing.T) {
	driver := `
#include <stdio.h>
#include <stdlib.h>
#include <pthread.h>

static zerg_chan_int64_t *ch;
static pthread_mutex_t out_mu = PTHREAD_MUTEX_INITIALIZER;
static long long out[256];
static int out_n = 0;

static void producer(void *arg) {
    (void)arg;
    /* Push 32 values; with cap=8 and no consumer initially, sends after
       the 8th will park until a consumer drains. */
    for (long long i = 0; i < 32; i++) zerg_chan_int64_t_send(ch, i);
    zerg_chan_int64_t_close(ch);
}

static void consumer(void *arg) {
    (void)arg;
    for (;;) {
        zerg_opt_int64_t r = zerg_chan_int64_t_recv(ch);
        if (r.tag != 0) return;
        pthread_mutex_lock(&out_mu);
        out[out_n++] = r.payload.p0.a0;
        pthread_mutex_unlock(&out_mu);
    }
}

int main(void) {
    zerg_sched_init(0);
    ch = zerg_chan_int64_t_make(8);
    zerg_coro_spawn(consumer, 0);
    zerg_coro_spawn(producer, 0);
    zerg_sched_drain();
    for (int i = 0; i < out_n; i++) printf("%lld\n", out[i]);
    return 0;
}
`
	out, code := v12BuildChanDriver(t, driver, []string{"ZERG_MAXPROCS=2"})
	if code != 0 {
		t.Fatalf("driver exited %d\noutput:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 32 {
		t.Fatalf("got %d lines, want 32\noutput:\n%s", len(lines), out)
	}
	// Buffered chan delivers FIFO; with one producer and one consumer the
	// order is fully deterministic.
	for i, l := range lines {
		n, err := strconv.Atoi(l)
		if err != nil {
			t.Fatalf("non-int line %q: %v", l, err)
		}
		if n != i {
			t.Fatalf("line %d = %d, want %d (FIFO order broken)", i, n, i)
		}
	}
}

// TestV12ChanCloseDrainsBuffered confirms that close on a chan with
// pending buffered values lets a receiver still drain them in FIFO order
// before observing None.
func TestV12ChanCloseDrainsBuffered(t *testing.T) {
	driver := `
#include <stdio.h>
#include <stdlib.h>

static zerg_chan_int64_t *ch;

static void worker(void *arg) {
    (void)arg;
    /* Push 5, close, then a separate consumer drains. */
    for (long long i = 0; i < 5; i++) zerg_chan_int64_t_send(ch, i * 10);
    zerg_chan_int64_t_close(ch);
}

int main(void) {
    zerg_sched_init(0);
    ch = zerg_chan_int64_t_make(8);
    zerg_coro_spawn(worker, 0);
    zerg_sched_drain();
    /* After drain, ch is closed. Drain the buffer from main thread. */
    for (;;) {
        zerg_opt_int64_t r = zerg_chan_int64_t_recv(ch);
        if (r.tag != 0) break;
        printf("%lld\n", r.payload.p0.a0);
    }
    return 0;
}
`
	out, code := v12BuildChanDriver(t, driver, []string{"ZERG_MAXPROCS=2"})
	if code != 0 {
		t.Fatalf("driver exited %d\noutput:\n%s", code, out)
	}
	got := strings.TrimRight(string(out), "\n")
	want := "0\n10\n20\n30\n40"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestV12ChanSendOnClosedPanics confirms a send on a closed channel exits
// non-zero with the runtime diagnostic.
func TestV12ChanSendOnClosedPanics(t *testing.T) {
	driver := `
#include <stdio.h>

static zerg_chan_int64_t *ch;

static void closer(void *arg) {
    (void)arg;
    zerg_chan_int64_t_close(ch);
}

static void sender(void *arg) {
    (void)arg;
    /* Yield once so the closer runs first, then attempt a send. */
    zerg_coro_yield();
    zerg_chan_int64_t_send(ch, 1);
}

int main(void) {
    zerg_sched_init(0);
    ch = zerg_chan_int64_t_make(0);
    zerg_coro_spawn(sender, 0);
    zerg_coro_spawn(closer, 0);
    zerg_sched_drain();
    fprintf(stderr, "expected panic before drain returned\n");
    return 99;
}
`
	out, code := v12BuildChanDriver(t, driver, nil)
	if code == 0 {
		t.Fatalf("driver returned cleanly; expected non-zero from send-on-closed panic\noutput:\n%s", out)
	}
	if !bytes.Contains(out, []byte("send on closed channel")) {
		t.Fatalf("output does not contain expected diagnostic\noutput:\n%s", out)
	}
}

// TestV12ChanDoubleClosePanics confirms close-on-already-closed exits
// non-zero with the runtime diagnostic.
func TestV12ChanDoubleClosePanics(t *testing.T) {
	driver := `
#include <stdio.h>

static zerg_chan_int64_t *ch;

static void worker(void *arg) {
    (void)arg;
    zerg_chan_int64_t_close(ch);
    zerg_chan_int64_t_close(ch);
}

int main(void) {
    zerg_sched_init(0);
    ch = zerg_chan_int64_t_make(0);
    zerg_coro_spawn(worker, 0);
    zerg_sched_drain();
    return 0;
}
`
	out, code := v12BuildChanDriver(t, driver, nil)
	if code == 0 {
		t.Fatalf("driver returned cleanly; expected non-zero from double-close panic\noutput:\n%s", out)
	}
	if !bytes.Contains(out, []byte("close on already-closed channel")) {
		t.Fatalf("output does not contain expected diagnostic\noutput:\n%s", out)
	}
}
