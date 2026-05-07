package build

// v0.12 Unit 2 — scheduler.
//
// Builds on the U1 coroutine primitive: an M:N scheduler that distributes
// coroutines across a worker-thread pool with per-worker FIFO runqueues
// and work-stealing on local-empty. U3+ rewrites the channel / wait_group
// / select runtimes to park on the scheduler instead of condvar-blocking
// the worker thread.
//
// Design pins:
//
//   - Workers are pthread'd OS threads. Default count = ZERG_GOMAXPROCS env
//     var, fallback to sysconf(_SC_NPROCESSORS_ONLN). The OS main thread
//     does NOT become a worker; it acts as the user-program driver and
//     either runs user code inline (v0.7-compatible mode) or invokes
//     zerg_sched_drain() to wait for all spawned coroutines (v0.12 cgen
//     integration in U6).
//
//   - Each worker holds a per-worker runqueue protected by its own mutex.
//     Pop: dequeue from local. Empty: steal half from a random victim.
//     All empty: wait on a global "any-work" condvar; new spawn / unpark
//     signals it.
//
//   - The global atomic counter `live_coros` tracks coroutines that have
//     been created but not yet completed. Workers exit when live_coros
//     reaches zero AND the scheduler has been signalled to drain.
//
//   - park / unpark drive U3+'s channel + wg + select rewrites:
//       zerg_coro_park()   — current coroutine is being parked by some
//                            wait queue; save its ucontext, return to the
//                            worker's scheduler loop. The wait queue keeps
//                            the coroutine pointer; nothing else does.
//       zerg_coro_unpark(c)— enqueue c on a worker's runqueue (originating
//                            worker preferred for cache locality).
//
//   - Coroutine entry stub (zerg_coro_entry from U1) marks the coroutine
//     DONE on fn return and swaps back to the worker's scheduler context;
//     the scheduler decrements live_coros, frees the coroutine, and loops.

const schedRuntimeC = `
/* ---------------- scheduler ----------------------------------------------
   Per-worker runqueue + work-stealing pool. Depends on coroRuntimeC for the
   coroutine primitive (zerg_coro_t, zerg_coro_new, ucontext entry stub).
   --------------------------------------------------------------------- */

#include <pthread.h>
#include <errno.h>

/* Per-worker runqueue: a fixed-capacity ring buffer keyed off (head, tail).
   Lock-free dequeues are a v0.13 enhancement; v0.12 protects each queue
   with its own mutex so the steal path can lock multiple queues without a
   global serialization point. Capacity = 1024 per worker; overflow falls
   back to a global linked list (rare path). */
#define ZERG_RQ_CAP 1024

typedef struct zerg_runqueue {
    pthread_mutex_t mu;
    zerg_coro_t *ring[ZERG_RQ_CAP];
    int head;
    int tail;
    int size;
} zerg_runqueue_t;

typedef struct zerg_worker {
    pthread_t thread;
    int id;
    zerg_runqueue_t rq;
} zerg_worker_t;

/* Global scheduler state. The worker pool is allocated at zerg_sched_init
   and torn down at zerg_sched_drain. live_coros tracks created-but-not-
   completed coroutines; sched_done flips when zerg_sched_drain is called
   AND live_coros has hit zero, signalling all workers to exit. */
static zerg_worker_t *zerg_workers = 0;
static int zerg_n_workers = 0;
/* The atomic counters use plain integer types so the __atomic_* builtins
   can address them; clang rejects __atomic_* against _Atomic-qualified
   types (those want C11 <stdatomic.h>'s atomic_* fns instead). The
   __atomic_* builtins provide atomicity via the memory order argument. */
static int64_t zerg_live_coros = 0;
static int zerg_sched_done = 0;

/* Global "any work" condvar — workers wait here when their local queue is
   empty AND every steal target was empty. A spawn or unpark broadcasts so
   sleeping workers re-probe. Spurious wakeups are harmless: the worker
   loop re-checks live_coros / sched_done and re-tries pop. */
static pthread_mutex_t zerg_sched_mu = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t zerg_sched_cv = PTHREAD_COND_INITIALIZER;

/* Per-thread "current worker" pointer so park/unpark can read the worker
   they belong to without an explicit arg threading. Set by the worker fn
   before its scheduler loop. */
static _Thread_local zerg_worker_t *zerg_current_worker = 0;
static _Thread_local ucontext_t zerg_worker_ctx;

static void zerg_sched_signal(void) {
    pthread_mutex_lock(&zerg_sched_mu);
    pthread_cond_broadcast(&zerg_sched_cv);
    pthread_mutex_unlock(&zerg_sched_mu);
}

/* zerg_rq_push enqueues a coroutine on the worker's local queue. Returns
   1 on success, 0 if the ring is full (caller falls back to push on a
   different worker; v0.12 keeps the fallback simple — wrap to the next
   worker linearly). */
static int zerg_rq_push(zerg_runqueue_t *rq, zerg_coro_t *c) {
    pthread_mutex_lock(&rq->mu);
    if (rq->size >= ZERG_RQ_CAP) {
        pthread_mutex_unlock(&rq->mu);
        return 0;
    }
    rq->ring[rq->tail] = c;
    rq->tail = (rq->tail + 1) % ZERG_RQ_CAP;
    rq->size++;
    pthread_mutex_unlock(&rq->mu);
    return 1;
}

static zerg_coro_t *zerg_rq_pop(zerg_runqueue_t *rq) {
    pthread_mutex_lock(&rq->mu);
    if (rq->size == 0) {
        pthread_mutex_unlock(&rq->mu);
        return 0;
    }
    zerg_coro_t *c = rq->ring[rq->head];
    rq->head = (rq->head + 1) % ZERG_RQ_CAP;
    rq->size--;
    pthread_mutex_unlock(&rq->mu);
    return c;
}

/* zerg_rq_steal moves up to half of the victim's queue into the thief's
   queue, returning one coroutine for the thief to run immediately.
   Returns 0 if the victim was empty. v0.12 steals from the head (FIFO)
   for simplicity; LIFO-from-back stealing is a v0.13 optimisation. */
static zerg_coro_t *zerg_rq_steal(zerg_runqueue_t *thief, zerg_runqueue_t *victim) {
    pthread_mutex_lock(&victim->mu);
    if (victim->size == 0) {
        pthread_mutex_unlock(&victim->mu);
        return 0;
    }
    int take = (victim->size + 1) / 2;
    zerg_coro_t *first = victim->ring[victim->head];
    victim->head = (victim->head + 1) % ZERG_RQ_CAP;
    victim->size--;
    take--;
    /* Move the rest into the thief's queue. We hold the victim's lock the
       entire time; lock the thief separately to maintain a stable lock
       order (victim then thief is fine because thief == self and won't
       contend with itself). */
    if (take > 0) {
        pthread_mutex_lock(&thief->mu);
        for (int i = 0; i < take && thief->size < ZERG_RQ_CAP; i++) {
            zerg_coro_t *c = victim->ring[victim->head];
            victim->head = (victim->head + 1) % ZERG_RQ_CAP;
            victim->size--;
            thief->ring[thief->tail] = c;
            thief->tail = (thief->tail + 1) % ZERG_RQ_CAP;
            thief->size++;
        }
        pthread_mutex_unlock(&thief->mu);
    }
    pthread_mutex_unlock(&victim->mu);
    return first;
}

/* zerg_sched_pick walks the worker pool starting at a random offset,
   stealing the first non-empty queue's work. Returns 0 only when every
   worker had an empty queue at the moment we checked — caller treats
   that as "park until signalled". */
static zerg_coro_t *zerg_sched_pick(zerg_worker_t *self) {
    /* Try local first. */
    zerg_coro_t *c = zerg_rq_pop(&self->rq);
    if (c) return c;
    if (zerg_n_workers <= 1) return 0;
    /* Random victim selection: simple linear walk from a thread-local
       cursor. Not random in the strict sense, but adequate at v0.12 — the
       walk-from-cursor avoids every worker piling onto worker 0 first. */
    static _Thread_local int cursor = 0;
    for (int i = 0; i < zerg_n_workers; i++) {
        cursor = (cursor + 1) % zerg_n_workers;
        if (&zerg_workers[cursor] == self) continue;
        c = zerg_rq_steal(&self->rq, &zerg_workers[cursor].rq);
        if (c) return c;
    }
    return 0;
}

/* zerg_worker_run executes one coroutine and returns to the scheduler
   loop. The coroutine's caller context is the worker thread's saved
   ucontext (zerg_worker_ctx). When the coroutine yields or finishes,
   control swaps back here. */
static void zerg_worker_run_one(zerg_worker_t *self, zerg_coro_t *c) {
    (void)self;
    zerg_coro_t *prev = zerg_current_coro;
    zerg_current_coro = c;
    c->caller = &zerg_worker_ctx;
    c->state = ZERG_CORO_RUNNING;
    /* The worker context is the resume target on yield / park / done. */
    swapcontext(&zerg_worker_ctx, &c->ctx);
    zerg_current_coro = prev;
    /* On return, c's state is either SUSPENDED (parked or yielded) or
       DONE. SUSPENDED + on no wait queue => yielded => requeue. SUSPENDED
       + on a wait queue => parked => waiter's job to unpark. DONE =>
       scheduler decrements live_coros and frees. v0.12 U2 doesn't have
       wait queues yet — every SUSPENDED is a yield, so we always requeue.
       U3 introduces a "is parked" bit. */
    if (c->state == ZERG_CORO_DONE) {
        zerg_coro_free(c);
        int64_t left = __atomic_sub_fetch(&zerg_live_coros, 1, __ATOMIC_ACQ_REL);
        if (left == 0) zerg_sched_signal();
    } else if (c->parked) {
        /* parked on a wait queue — the unparker is responsible for
           requeueing. Leave the coroutine alone and pick the next one. */
    } else {
        /* yielded — back to the local queue. */
        c->state = ZERG_CORO_RUNNABLE;
        if (!zerg_rq_push(&self->rq, c)) {
            /* overflow — try other queues linearly. */
            for (int i = 0; i < zerg_n_workers; i++) {
                if (zerg_rq_push(&zerg_workers[i].rq, c)) break;
            }
        }
    }
}

/* The pthread entry point for each worker. */
static void *zerg_worker_main(void *arg) {
    zerg_worker_t *self = (zerg_worker_t *)arg;
    zerg_current_worker = self;
    /* Save our scheduler context; coroutines swap into c->ctx and back to
       this saved context. We don't reuse swapcontext's incoming ctx for
       this — getcontext seeds the field with valid contents that
       swapcontext needs to resume. */
    getcontext(&zerg_worker_ctx);
    for (;;) {
        zerg_coro_t *c = zerg_sched_pick(self);
        if (c) {
            zerg_worker_run_one(self, c);
            continue;
        }
        /* No work — check shutdown. */
        if (__atomic_load_n(&zerg_sched_done, __ATOMIC_ACQUIRE) &&
            __atomic_load_n(&zerg_live_coros, __ATOMIC_ACQUIRE) == 0) {
            return 0;
        }
        /* Park on the global condvar with a short timeout so a missed
           signal does not deadlock us. */
        pthread_mutex_lock(&zerg_sched_mu);
        struct timespec ts;
        clock_gettime(CLOCK_REALTIME, &ts);
        ts.tv_nsec += 5 * 1000 * 1000; /* 5 ms */
        if (ts.tv_nsec >= 1000 * 1000 * 1000) {
            ts.tv_sec += 1;
            ts.tv_nsec -= 1000 * 1000 * 1000;
        }
        pthread_cond_timedwait(&zerg_sched_cv, &zerg_sched_mu, &ts);
        pthread_mutex_unlock(&zerg_sched_mu);
    }
}

/* zerg_sched_init initialises the worker pool. Call once at program
   start (the C main() does this before invoking any coroutine APIs).
   n_workers <= 0 selects the platform default (NPROC env var or
   _SC_NPROCESSORS_ONLN). */
void zerg_sched_init(int n_workers) {
    if (zerg_workers) return; /* idempotent */
    if (n_workers <= 0) {
        const char *env = getenv("ZERG_GOMAXPROCS");
        if (env && *env) n_workers = atoi(env);
    }
    if (n_workers <= 0) {
        long n = sysconf(_SC_NPROCESSORS_ONLN);
        n_workers = (n > 0) ? (int)n : 1;
    }
    if (n_workers > 256) n_workers = 256; /* sanity clamp */
    zerg_n_workers = n_workers;
    zerg_workers = (zerg_worker_t *)calloc((size_t)n_workers, sizeof(*zerg_workers));
    for (int i = 0; i < n_workers; i++) {
        zerg_workers[i].id = i;
        pthread_mutex_init(&zerg_workers[i].rq.mu, 0);
    }
    for (int i = 0; i < n_workers; i++) {
        pthread_create(&zerg_workers[i].thread, 0, zerg_worker_main, &zerg_workers[i]);
    }
}

/* zerg_coro_spawn allocates a coroutine running fn(arg) and enqueues it
   on a worker's runqueue. Pick policy at v0.12: round-robin via an
   atomic counter. Returns 0 on coro_new failure. */
static int zerg_spawn_cursor = 0;
zerg_coro_t *zerg_coro_spawn(void (*fn)(void *), void *arg) {
    zerg_coro_t *c = zerg_coro_new(fn, arg);
    if (!c) return 0;
    c->state = ZERG_CORO_RUNNABLE;
    __atomic_add_fetch(&zerg_live_coros, 1, __ATOMIC_ACQ_REL);
    int idx = __atomic_fetch_add(&zerg_spawn_cursor, 1, __ATOMIC_RELAXED) % zerg_n_workers;
    if (!zerg_rq_push(&zerg_workers[idx].rq, c)) {
        /* overflow — linear scan. */
        for (int i = 0; i < zerg_n_workers; i++) {
            if (zerg_rq_push(&zerg_workers[i].rq, c)) break;
        }
    }
    zerg_sched_signal();
    return c;
}

/* zerg_coro_park is called by U3+'s wait queues. Saves the current
   coroutine's context and yields back to the worker's scheduler loop;
   the worker loop sees state == SUSPENDED and (post-U3) checks a
   parked-bit to decide whether to requeue or trust a wait queue to
   unpark. v0.12 U2 only exposes the API; channels won't call it until
   U3 lands. */
void zerg_coro_park(void) {
    zerg_coro_t *c = zerg_current_coro;
    if (!c) {
        fprintf(stderr, "zerg: zerg_coro_park called outside a coroutine\n");
        abort();
    }
    c->state = ZERG_CORO_SUSPENDED;
    c->parked = 1;
    swapcontext(&c->ctx, c->caller);
}

/* zerg_coro_unpark moves a parked coroutine back to a worker's runqueue.
   v0.12 enqueues on the spawning worker (round-robin); U3 may pin to
   the originating worker for cache locality. */
void zerg_coro_unpark(zerg_coro_t *c) {
    if (!c) return;
    c->parked = 0;
    c->state = ZERG_CORO_RUNNABLE;
    int idx = __atomic_fetch_add(&zerg_spawn_cursor, 1, __ATOMIC_RELAXED) % zerg_n_workers;
    if (!zerg_rq_push(&zerg_workers[idx].rq, c)) {
        for (int i = 0; i < zerg_n_workers; i++) {
            if (zerg_rq_push(&zerg_workers[i].rq, c)) break;
        }
    }
    zerg_sched_signal();
}

/* zerg_sched_drain blocks until every spawned coroutine has finished, then
   shuts down the worker pool. The OS main thread calls this exactly once
   after running its top-level statements (which may spawn coroutines).
   v0.12 U6 wires this into the cgen-emitted main(). */
void zerg_sched_drain(void) {
    /* Wait for live_coros to hit zero. */
    pthread_mutex_lock(&zerg_sched_mu);
    while (__atomic_load_n(&zerg_live_coros, __ATOMIC_ACQUIRE) > 0) {
        pthread_cond_wait(&zerg_sched_cv, &zerg_sched_mu);
    }
    pthread_mutex_unlock(&zerg_sched_mu);
    __atomic_store_n(&zerg_sched_done, 1, __ATOMIC_RELEASE);
    zerg_sched_signal();
    for (int i = 0; i < zerg_n_workers; i++) {
        pthread_join(zerg_workers[i].thread, 0);
        pthread_mutex_destroy(&zerg_workers[i].rq.mu);
    }
    free(zerg_workers);
    zerg_workers = 0;
    zerg_n_workers = 0;
}
`

// SchedRuntimeC exposes the U2 C source so the targeted test can compile a
// driver against it (concatenated with coroRuntimeC). U6 will fold both
// into the v0.12 prelude emit path.
func SchedRuntimeC() string { return schedRuntimeC }
