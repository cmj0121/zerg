package build

import (
	"sort"
	"strconv"
	"strings"
	"testing"
)

// v0.12 Unit 4 — select runtime end-to-end.
//
// Compiles a driver program that uses zerg_select against two chans.
// The driver also defines per-chan _ready probe fns matching what U6
// cgen will template per element-type.

const selectChanReadyC = `
static int zerg_chan_int64_t_ready(void *p, int kind) {
    zerg_chan_int64_t *ch = (zerg_chan_int64_t *)p;
    pthread_mutex_lock(&ch->mu);
    int r = 0;
    int64_t slots = ch->cap > 0 ? ch->cap : 1;
    if (kind == 0) {
        /* recv ready: buffer non-empty OR a parked sender OR closed. */
        r = (ch->count > 0) || (ch->send_head != 0) || ch->closed;
    } else if (kind == 1) {
        /* send ready: buffer has room AND not closed, OR a parked receiver. */
        r = ((ch->count < slots) && !ch->closed) || (ch->recv_head != 0);
    }
    pthread_mutex_unlock(&ch->mu);
    return r;
}
`

// v12BuildSelectDriver pulls in (coro + sched + chan + select) plus the
// per-element-type ready probe selectChanReadyC (matching what U6 cgen
// templates per chan element type).
func v12BuildSelectDriver(t *testing.T, driver string, env []string) ([]byte, int) {
	t.Helper()
	prog := coroRuntimeC + schedRuntimeC + chanRuntimeC + selectRuntimeC +
		selectChanReadyC + "\n" + driver
	return v12CompileAndRun(t, prog, env)
}

// TestV12SelectFanIn drives two producers each into its own chan, with
// one consumer that fan-ins via select. Asserts every value emitted by
// either producer was received exactly once.
func TestV12SelectFanIn(t *testing.T) {
	driver := `
#include <stdio.h>
#include <stdlib.h>
#include <pthread.h>

#define N 32
static zerg_chan_int64_t *cha;
static zerg_chan_int64_t *chb;

static pthread_mutex_t out_mu = PTHREAD_MUTEX_INITIALIZER;
static long long out[2*N];
static int out_n = 0;

static void prod_a(void *arg) {
    (void)arg;
    for (long long i = 0; i < N; i++) zerg_chan_int64_t_send(cha, i);
    zerg_chan_int64_t_close(cha);
}

static void prod_b(void *arg) {
    (void)arg;
    for (long long i = 0; i < N; i++) zerg_chan_int64_t_send(chb, 100 + i);
    zerg_chan_int64_t_close(chb);
}

static void consumer(void *arg) {
    (void)arg;
    int closed_a = 0, closed_b = 0;
    while (!closed_a || !closed_b) {
        zerg_select_case cases[2];
        cases[0].kind = 0; cases[0].chan = cha; cases[0].ready = zerg_chan_int64_t_ready;
        cases[1].kind = 0; cases[1].chan = chb; cases[1].ready = zerg_chan_int64_t_ready;
        int idx = zerg_select(cases, 2, 0, 0);
        zerg_chan_int64_t *ch = (idx == 0) ? cha : chb;
        zerg_opt_int64_t r = zerg_chan_int64_t_recv(ch);
        if (r.tag != 0) {
            /* Closed — the ready probe surfaces closed as recv-ready.
               Mark this side done; pick from the other on subsequent
               iterations. We can't simply drop this case from the
               array because the v0.7 surface is a fresh array per call;
               instead we re-shape below by simply ignoring the closed
               side until both are closed. */
            if (idx == 0) closed_a = 1; else closed_b = 1;
            /* Patch: if closed, we want subsequent selects to NOT pick
               this side. The simplest patch is to mark its kind = 2
               (default) so it is skipped — but then we'd need
               has_default etc. Instead, reuse the loop: re-send a
               degenerate case using a dummy chan. To keep this simple,
               we just spin on the open side until it also closes. */
            if (closed_a && !closed_b) {
                while (1) {
                    zerg_opt_int64_t r2 = zerg_chan_int64_t_recv(chb);
                    if (r2.tag != 0) { closed_b = 1; break; }
                    pthread_mutex_lock(&out_mu);
                    out[out_n++] = r2.payload.p0.a0;
                    pthread_mutex_unlock(&out_mu);
                }
            } else if (closed_b && !closed_a) {
                while (1) {
                    zerg_opt_int64_t r2 = zerg_chan_int64_t_recv(cha);
                    if (r2.tag != 0) { closed_a = 1; break; }
                    pthread_mutex_lock(&out_mu);
                    out[out_n++] = r2.payload.p0.a0;
                    pthread_mutex_unlock(&out_mu);
                }
            }
            continue;
        }
        pthread_mutex_lock(&out_mu);
        out[out_n++] = r.payload.p0.a0;
        pthread_mutex_unlock(&out_mu);
    }
}

int main(void) {
    zerg_sched_init(0);
    cha = zerg_chan_int64_t_make(0);
    chb = zerg_chan_int64_t_make(0);
    zerg_coro_spawn(consumer, 0);
    zerg_coro_spawn(prod_a, 0);
    zerg_coro_spawn(prod_b, 0);
    zerg_sched_drain();
    for (int i = 0; i < out_n; i++) printf("%lld\n", out[i]);
    return 0;
}
`
	out, code := v12BuildSelectDriver(t, driver, []string{"ZERG_MAXPROCS=4"})
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
	want := make([]int, 0, 64)
	for i := 0; i < 32; i++ {
		want = append(want, i)
	}
	for i := 0; i < 32; i++ {
		want = append(want, 100+i)
	}
	for i, v := range got {
		if v != want[i] {
			t.Fatalf("at sorted position %d: got %d, want %d", i, v, want[i])
		}
	}
}

// TestV12SelectDefault confirms the default arm fires when no case is
// ready, and that subsequent calls also see the default until a case
// becomes ready.
func TestV12SelectDefault(t *testing.T) {
	driver := `
#include <stdio.h>

static zerg_chan_int64_t *ch;

static void worker(void *arg) {
    (void)arg;
    /* No producer yet — recv with a default arm must take the default. */
    zerg_select_case cases[2];
    cases[0].kind = 0; cases[0].chan = ch; cases[0].ready = zerg_chan_int64_t_ready;
    cases[1].kind = 2; cases[1].chan = 0; cases[1].ready = 0;
    int idx = zerg_select(cases, 2, 1, 1);
    printf("first=%d\n", idx);

    /* Push something so the chan becomes ready. */
    zerg_chan_int64_t_send(ch, 42);

    cases[0].kind = 0; cases[0].chan = ch; cases[0].ready = zerg_chan_int64_t_ready;
    cases[1].kind = 2; cases[1].chan = 0; cases[1].ready = 0;
    idx = zerg_select(cases, 2, 1, 1);
    printf("second=%d\n", idx);
    if (idx == 0) {
        zerg_opt_int64_t r = zerg_chan_int64_t_recv(ch);
        printf("recv tag=%d val=%lld\n", r.tag, (long long)r.payload.p0.a0);
    }
}

int main(void) {
    zerg_sched_init(0);
    ch = zerg_chan_int64_t_make(1);
    zerg_coro_spawn(worker, 0);
    zerg_sched_drain();
    return 0;
}
`
	out, code := v12BuildSelectDriver(t, driver, []string{"ZERG_MAXPROCS=2"})
	if code != 0 {
		t.Fatalf("driver exited %d\noutput:\n%s", code, out)
	}
	got := strings.TrimRight(string(out), "\n")
	want := "first=1\nsecond=0\nrecv tag=0 val=42"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
