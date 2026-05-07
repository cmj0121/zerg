package build

// v0.12 Unit 4 — select on park/unpark (yield-loop variant).
//
// Replaces v0.7's polling loop (usleep(50us) between probes) with a
// cooperative yield: when no case is ready and there's no default arm,
// the calling coroutine yields back to its worker so the worker can run
// other coroutines (including the producer / consumer that will make a
// case ready). The yield path goes through the U2 scheduler, so a select
// no longer pins the OS worker.
//
// This is the simplest correct rewrite. A wait-queue-based atomic select
// (Go-style: register on every chan, single-winner unpark) is a v0.13
// optimisation; the yield-loop already gets us off-thread parking with
// minimal complexity.
//
// Surface stays bytewise compatible with v0.7's emit:
//   - struct zerg_select_case { int kind; void *chan; int (*ready)(...); }
//   - int zerg_select(cases, n, has_default, default_idx) -> chosen idx
// so U6's cgen swap is body-only.

const selectRuntimeC = `
typedef struct {
    int kind;                                  /* 0=recv, 1=send, 2=default */
    void *chan;                                /* NULL for default */
    int (*ready)(void *chan, int kind);        /* probes — emitted per chan elem-type */
} zerg_select_case;

static int zerg_select(zerg_select_case *cases, int n_cases, int has_default,
                       int default_idx) {
    for (;;) {
        for (int i = 0; i < n_cases; i++) {
            if (cases[i].kind == 2) continue;  /* default arm — skip in probe */
            if (cases[i].ready(cases[i].chan, cases[i].kind)) return i;
        }
        if (has_default) return default_idx;
        /* No case ready and no default — yield to the scheduler. The
           worker will run other coroutines (which will eventually make
           a case ready) and pick this coro back up via the local queue
           or work-stealing path. */
        zerg_coro_yield();
    }
}
`

// SelectRuntimeC exposes the U4 select source so the targeted test can
// compile a driver against it. U6 will fold it into the v0.12 prelude
// emit path, replacing v0.7's usleep-based polling variant in
// runtime.go.
func SelectRuntimeC() string { return selectRuntimeC }
