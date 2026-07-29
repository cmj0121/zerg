/*
 * sched.c - the Zerg runtime's M:N cooperative coroutine scheduler and `spawn`.
 *
 * Linked only when a program's Manifest reports Concurrency. M worker OS threads run
 * N stackful coroutines off ONE shared FIFO run queue, with the context switch hidden
 * behind the zrt_ctx shim (ctx_arm64.S / ctx_x86_64.S / ctx_ucontext.c) and the
 * threads behind the zrt_thread shim (thread_pthread.c / thread_win32.c /
 * thread_none.c). `main` runs as the first coroutine; the program exits with main's
 * outcome once nothing is runnable, so a fire-and-forget `spawn` still gets to run.
 *
 * Cooperative, not preemptive: a coroutine yields at a channel operation or an
 * explicit zrt_yield, and a worker cannot take it away between those points. What M:N
 * adds is that two coroutines which are both runnable can occupy two CPUs at once.
 *
 * A coroutine MIGRATES: it is pulled off the shared queue by whichever worker is free,
 * so it may resume on a different thread than it parked on. That is why every piece of
 * "who is running here" state below is thread-local, and why zrt_tls_load/save must
 * bracket the swap rather than sit anywhere outside it — the bundle belongs to the
 * coroutine, not to the worker.
 *
 * With one worker (thread_none.c, or a single-CPU host) this is exactly the old N:1
 * scheduler: the same queue, the same order, the same output. Only the number of
 * threads draining the queue changes.
 *
 * Each coroutine carries its OWN unwind state (cleanup stack + abort handler) in
 * its zrt_coro.tls; the scheduler swaps the "current" bundle (zrt_tls_save /
 * zrt_tls_load) around every switch, so a coroutine's defers/drops and its abort
 * handler act on its own stack. A coroutine that aborts with no user handler is
 * caught by a bottom handler its trampoline installs, so a crash is contained to
 * that coroutine and does not tear down the whole process.
 */
#include "zergrt.h"

#include <stdlib.h>
#include <sys/mman.h>
#include <unistd.h>

#if !defined(MAP_ANONYMOUS) && defined(MAP_ANON)
#define MAP_ANONYMOUS MAP_ANON
#endif

/* --- scheduler state --------------------------------------------------------- */

/* PER-WORKER. Each worker has its own scheduler context to swap back to and its own
 * notion of what it is currently running; a coroutine that parks on one worker and
 * resumes on another must find the resuming worker's context, not the one it left. */
static ZRT_THREAD_LOCAL zrt_ctx t_sched_ctx;

static ZRT_THREAD_LOCAL zrt_coro *t_current;

/* SHARED, under g_lock: the one FIFO run queue every worker drains, the live count,
 * and the bookkeeping that decides when the program is over. g_cond wakes a worker
 * that is waiting for work; g_idle counts the workers currently waiting on it, which
 * is what tells "everyone is idle and coroutines remain" (a deadlock) from "everyone
 * is idle and none remain" (the program finished). */
static zrt_mutex g_lock;
static zrt_cond g_cond;
static zrt_coro *g_runq_head;
static zrt_coro *g_runq_tail;
static size_t g_idle;
static size_t g_workers;
static bool g_done;

/* main's outcome, mapped to the process exit code; pessimistically 1 so an
 * aborting main (whose thunk never records success) exits non-zero, as zrt_run. */
static int g_exit_code;

/* the number of live (not-yet-reclaimed) coroutines, under g_lock. When every worker
 * is idle while this is non-zero, every remaining coroutine is parked on a channel
 * with nothing left to wake it — a deadlock (§1.4). Zero means the program finished. */
static size_t g_live;

/* the number of coroutines executing on some worker right now. A worker that finds the
 * queue empty while this is non-zero must NOT call deadlock: the running coroutines can
 * still enqueue or wake others. It is the difference between "no work available at this
 * instant" and "no work will ever be available again". */
static size_t g_live_running;

/* runq_push / runq_pop assume g_lock is HELD. They are the only two places the queue
 * is touched, which is what keeps the locking discipline reviewable in one screen. */
static void runq_push(zrt_coro *co) {
	co->qnext = NULL;
	if (g_runq_tail != NULL) {
		g_runq_tail->qnext = co;
	} else {
		g_runq_head = co;
	}
	g_runq_tail = co;
}

static zrt_coro *runq_pop(void) {
	zrt_coro *co = g_runq_head;
	if (co != NULL) {
		g_runq_head = co->qnext;
		if (g_runq_head == NULL) {
			g_runq_tail = NULL;
		}
		co->qnext = NULL;
	}
	return co;
}

/* --- coroutine stacks (fixed size + guard page) ------------------------------ */

/* stack_alloc maps a fresh coroutine stack of `size` usable bytes plus a low-end
 * guard page: the stack grows down from the high end, so an overflow runs into the
 * PROT_NONE guard and faults at once (Fork-B). Returns the mapping base; the whole
 * mapped range (guard + usable) is handed to zrt_ctx_init, whose sp starts at the
 * high end. */
static void *stack_alloc(size_t size, size_t *total_out) {
	long pg = sysconf(_SC_PAGESIZE);
	size_t page = (pg > 0) ? (size_t)pg : 4096;
	size_t total = size + page; /* one guard page below the usable stack */
	void *base = mmap(NULL, total, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	if (base == MAP_FAILED) {
		zrt_abort("out of memory (coroutine stack)");
	}
	if (mprotect(base, page, PROT_NONE) != 0) {
		zrt_abort("cannot guard coroutine stack");
	}
	*total_out = total;
	return base;
}

/* --- the spawned coroutine's entry ------------------------------------------- */

/* coro_trampoline is where every coroutine begins, on its own stack. It installs a
 * bottom abort handler in this coroutine's (already current) unwind bundle, runs the
 * marshalled thunk, and unwinds its cleanup stack on the normal path. If the thunk
 * aborts with no inner handler, the longjmp lands here (the else branch): the crash
 * is contained to this coroutine - reported, and the coroutine simply finishes -
 * rather than exiting the process. Either way it marks DONE and swaps back to the
 * scheduler, which reclaims the stack. */
static void coro_trampoline(void *arg) {
	zrt_coro *co = (zrt_coro *)arg;
	zrt_frame frame;
	zrt_handler_push(&frame);
	if (setjmp(frame.buf) == 0) {
		co->thunk(co->env);
		zrt_unwind_to(frame.mark); /* run pending defers/drops on the normal path */
		zrt_handler_pop(&frame);
	} else {
		/* aborted with no user handler: zrt_abort already ran cleanups to frame.mark
		 * and reported its message; contain the crash to this coroutine. */
		zrt_handler_pop(&frame);
	}
	co->state = ZRT_CORO_DONE;
	zrt_ctx_swap(&co->ctx, &t_sched_ctx); /* back to THIS worker's loop; never returns */
}

/* --- spawn ------------------------------------------------------------------- */

void zrt_spawn(void (*thunk)(void *env), void *env) {
	zrt_coro *co = (zrt_coro *)zrt_alloc(sizeof(*co));
	size_t total = 0;
	co->stack = stack_alloc(ZRT_CORO_STACK, &total);
	co->stack_size = total;
	co->state = ZRT_CORO_RUNNABLE;
	co->woken = false; /* zrt_alloc is malloc, not calloc: an unset flag is a stray wake */
	co->thunk = thunk;
	co->env = env;
	co->tls.stack = NULL;
	co->tls.len = 0;
	co->tls.cap = 0;
	co->tls.handler = NULL;
	co->qnext = NULL;
	zrt_ctx_init(&co->ctx, co->stack, co->stack_size, coro_trampoline, co);
	zrt_mutex_lock(&g_lock);
	g_live++;
	runq_push(co);
	zrt_cond_signal(&g_cond); /* a worker may be waiting for exactly this */
	zrt_mutex_unlock(&g_lock);
}

void zrt_yield(void) {
	zrt_coro *co = t_current;
	if (co == NULL) {
		return; /* not inside a coroutine */
	}
	co->state = ZRT_CORO_RUNNABLE;
	zrt_ctx_swap(&co->ctx, &t_sched_ctx);
}

/* --- park / wake (channel blocking primitives, used by chan.c) --------------- */

zrt_coro *zrt_sched_current(void) {
	return t_current;
}

void zrt_sched_park_unlock(zrt_mutex *m) {
	zrt_coro *co = t_current;
	if (co == NULL) {
		zrt_mutex_unlock(m);
		return; /* not inside a coroutine: cannot block */
	}
	co->park_lock = m;
	co->state = ZRT_CORO_PARKING;
	zrt_ctx_swap(&co->ctx, &t_sched_ctx);
	/* Resumed by some worker — not necessarily the one we left. */
}

void zrt_sched_park(void) {
	zrt_coro *co = t_current;
	if (co == NULL) {
		return; /* not inside a coroutine: cannot block */
	}
	co->park_lock = NULL;
	co->state = ZRT_CORO_PARKING;
	zrt_ctx_swap(&co->ctx, &t_sched_ctx);
	/* Resumed. A wake re-enqueued us and SOME worker swapped us back in — not
	 * necessarily the one we parked on, which is why nothing here may cache a worker. */
}

void zrt_sched_wake(zrt_coro *co) {
	/* Idempotent: only a still-parked coroutine is re-enqueued. A select parks on
	 * several channels at once, so more than one counterparty (or a close) may try to
	 * wake it; waking only a BLOCKED coroutine keeps it on the run queue exactly once.
	 *
	 * The test and the transition must be ATOMIC together, which is why they are under
	 * g_lock rather than a bare read: with two workers, two counterparties can reach
	 * the test at the same moment and both find BLOCKED, and the coroutine would be
	 * enqueued twice — run twice, freed twice. This is the one place M:N turns a
	 * previously harmless check-then-act into a real race, and chan.c calls it while
	 * holding the channel's own lock, so the two locks are always taken in that order:
	 * channel first, scheduler second. */
	zrt_mutex_lock(&g_lock);
	if (co->state == ZRT_CORO_BLOCKED) {
		co->state = ZRT_CORO_RUNNABLE;
		runq_push(co);
		zrt_cond_signal(&g_cond);
	} else if (co->state == ZRT_CORO_RUNNING || co->state == ZRT_CORO_PARKING) {
		/* Not parked yet, so it cannot be queued: queueing a coroutine that is on a CPU
		 * would let a second worker run it on its own stack while it is still on this
		 * one. The wake is REMEMBERED instead, and the worker completing the park honours
		 * it rather than marking the coroutine blocked.
		 *
		 * The flag is a park token, in the sense Go's note and Rust's thread::park use:
		 * it does not name a channel or a reason, it only says "the next park must not
		 * block, because the news you are about to wait for has already arrived". That is
		 * why a stray one is harmless — every parking caller here (both channel ops and
		 * select) re-scans in a loop and parks again if nothing was actually ready.
		 *
		 * Only select reaches this. A plain send or recv hands its one channel lock to
		 * the scheduler, so a counterparty cannot even find the waiter until the switch
		 * is done; a select pushes a waiter onto every channel it watches and can hold
		 * only one of those locks at a time, so from the first push onward it is visible
		 * to a counterparty while still RUNNING. Missing that wake is not a wrong answer,
		 * it is a hang — the select parks on a hand-off that has already happened. */
		co->woken = true;
	}
	zrt_mutex_unlock(&g_lock);
}

/* --- the scheduler loop ------------------------------------------------------ */

static void sched_init(void) {
	zrt_mutex_init(&g_lock);
	zrt_cond_init(&g_cond);
	zrt_chan_select_init();
	g_runq_head = NULL;
	g_runq_tail = NULL;
	t_current = NULL;
	g_live = 0;
	g_live_running = 0;
	g_idle = 0;
	g_workers = 0;
	g_done = false;
	g_exit_code = 1; /* pessimistic: main's thunk overwrites this on success */
}

/* sched_run pops and runs coroutines until nothing is runnable. Each switch is
 * bracketed by the current coroutine's unwind bundle: load it before swapping in,
 * snapshot it back on return. A DONE coroutine is reclaimed (its stack unmapped and
 * its cleanup buffer freed); a coroutine that yielded is re-enqueued. */
static void sched_run(void) {
	zrt_mutex_lock(&g_lock);
	for (;;) {
		zrt_coro *co = runq_pop();
		if (co == NULL) {
			if (g_done) {
				break; /* another worker finished the program */
			}
			if (g_live == 0) {
				/* nothing runnable and nothing left alive: the program is over. Every
				 * other worker is either idle or about to be, so wake them all to see
				 * g_done rather than waiting for work that will never come. */
				g_done = true;
				zrt_cond_broadcast(&g_cond);
				break;
			}
			/* Coroutines remain but none is runnable HERE. With one worker that is a
			 * deadlock outright; with several it only means the others still hold work,
			 * so this worker sleeps until someone enqueues or the program ends. It is a
			 * deadlock exactly when every worker is idle at once — nobody is running, so
			 * nobody can ever wake anybody. */
			g_idle++;
			if (g_idle == g_workers && g_live_running == 0) {
				zrt_report("all coroutines blocked (deadlock)");
				exit(1);
			}
			zrt_cond_wait(&g_cond, &g_lock);
			g_idle--;
			continue;
		}
		g_live_running++;
		/* under g_lock, before any other thread can observe it: from here until it parks
		 * or finishes, a wake must be remembered rather than queued or discarded. */
		co->state = ZRT_CORO_RUNNING;
		zrt_mutex_unlock(&g_lock);

		/* OUTSIDE the lock: this is the only part that runs user code, and holding the
		 * scheduler lock across it would serialise the whole program — the M in M:N is
		 * precisely that several workers are in here at once. The unwind bundle brackets
		 * the swap because it belongs to the COROUTINE: it parked on some worker and may
		 * resume on this one. */
		t_current = co;
		zrt_tls_load(&co->tls);
		zrt_ctx_swap(&t_sched_ctx, &co->ctx); /* run it until it yields, parks, or finishes */
		zrt_tls_save(&co->tls);
		t_current = NULL;

		zrt_mutex_lock(&g_lock);
		g_live_running--;
		if (co->state == ZRT_CORO_PARKING) {
			/* the switch is complete, so the coroutine may now be woken. Both halves
			 * happen under g_lock, which is the lock zrt_sched_wake takes — so a wake
			 * cannot land between them and be missed. */
			if (co->park_lock != NULL) {
				zrt_mutex_unlock(co->park_lock);
				co->park_lock = NULL;
			}
			if (co->woken) {
				/* a wake arrived while it was still switching out: honour it now */
				co->woken = false;
				co->state = ZRT_CORO_RUNNABLE;
			} else {
				co->state = ZRT_CORO_BLOCKED;
			}
		}
		switch (co->state) {
		case ZRT_CORO_DONE:
			zrt_tls_free(&co->tls);
			munmap(co->stack, co->stack_size);
			zrt_free(co);
			g_live--;
			if (g_live == 0) {
				zrt_cond_broadcast(&g_cond); /* the last one: let every worker finish */
			}
			break;
		case ZRT_CORO_RUNNING: /* swapped out without saying why: treat as a yield */
		case ZRT_CORO_RUNNABLE:
			co->state = ZRT_CORO_RUNNABLE;
			runq_push(co); /* voluntarily yielded: back on the queue */
			zrt_cond_signal(&g_cond);
			break;
		case ZRT_CORO_PARKING:
		case ZRT_CORO_BLOCKED:
			break; /* parked on a channel wait queue; a wake re-enqueues it */
		}
	}
	zrt_mutex_unlock(&g_lock);
}

/* worker_main is what every extra OS thread runs: the same loop the calling thread
 * runs, so there is no "main worker" special case beyond who starts the others. */
static void worker_main(void *arg) {
	(void)arg;
	sched_run();
}

/* ZRT_MAX_WORKERS caps the pool. The work here is coroutines, not CPU-saturating
 * kernels, and every extra worker is another contender for the one run-queue lock —
 * past a point that costs more than it buys. */
#define ZRT_MAX_WORKERS ((size_t)16)

/* sched_drain runs the program: it starts the extra workers, joins the loop itself,
 * and waits for them. With no thread support, or one CPU, g_workers is 1 and this is
 * exactly the old single-threaded drain — the same queue in the same order.
 *
 * The CALLING thread is a worker, not a supervisor. That keeps the M = 1 case honest
 * (no thread is created at all) and means a host that fails to create threads simply
 * runs with fewer, rather than failing to run. */
static void sched_drain(void) {
	static zrt_thread workers[ZRT_MAX_WORKERS];
	size_t want = 1;
	/* ZRT_WORKERS=1 forces the single-worker path on a host that has threads. It is how
	 * a concurrency failure is told apart: if it survives with one worker it is a bug in
	 * the scheduler's logic, and if it only appears with several it is a race. */
	if (zrt_thread_supported() && getenv("ZRT_WORKERS") == NULL) {
		want = zrt_cpu_count();
		if (want > ZRT_MAX_WORKERS) {
			want = ZRT_MAX_WORKERS;
		}
	}

	/* g_workers must be right BEFORE any worker can observe it: it is what the idle
	 * count is compared against to call a deadlock. Setting it after starting threads
	 * would let an early worker see a smaller pool and declare deadlock wrongly. */
	zrt_mutex_lock(&g_lock);
	g_workers = want;
	zrt_mutex_unlock(&g_lock);

	size_t started = 0;
	for (size_t i = 0; i + 1 < want; i++) {
		if (!zrt_thread_start(&workers[started], worker_main, NULL)) {
			break; /* the host refused: run with the workers we have */
		}
		started++;
	}
	if (started + 1 < want) {
		/* fewer threads than planned: correct the count the deadlock test uses */
		zrt_mutex_lock(&g_lock);
		g_workers = started + 1;
		zrt_cond_broadcast(&g_cond);
		zrt_mutex_unlock(&g_lock);
	}

	sched_run(); /* the calling thread is a worker too */

	for (size_t i = 0; i < started; i++) {
		zrt_thread_join(&workers[i]);
	}
	zrt_mutex_destroy(&g_lock);
	zrt_cond_destroy(&g_cond);
}

/* --- program-entry shims (one per main return shape) ------------------------- */

/* main's function pointer is stashed in a global so a fixed-signature thunk can run
 * it as coroutine 0 and record the exit code. Only one is used per program. */
static zrt_main_fn g_main_result;
static void (*g_main_nil)(void);
static int64_t (*g_main_int)(void);

static void main_thunk_result(void *env) {
	(void)env;
	zrt_result_nil r = g_main_result();
	g_exit_code = zrt_result_is_err(r) ? 1 : 0;
}

static void main_thunk_nil(void *env) {
	(void)env;
	g_main_nil();
	g_exit_code = 0;
}

static void main_thunk_int(void *env) {
	(void)env;
	g_exit_code = (int)g_main_int();
}

int zrt_sched_main(zrt_main_fn fn) {
	sched_init();
	g_main_result = fn;
	zrt_spawn(main_thunk_result, NULL);
	sched_drain();
	return g_exit_code;
}

int zrt_sched_main_nil(void (*fn)(void)) {
	sched_init();
	g_main_nil = fn;
	zrt_spawn(main_thunk_nil, NULL);
	sched_drain();
	return g_exit_code;
}

int zrt_sched_main_int(int64_t (*fn)(void)) {
	sched_init();
	g_main_int = fn;
	zrt_spawn(main_thunk_int, NULL);
	sched_drain();
	return g_exit_code;
}

/* the test-driver body pointer, run as coroutine 0 by zrt_sched_run. */
static void (*g_run_body)(void);

static void run_body_thunk(void *env) {
	(void)env;
	g_run_body();
}

void zrt_sched_run(void (*fn)(void)) {
	sched_init();
	g_run_body = fn;
	zrt_spawn(run_body_thunk, NULL);
	sched_drain();
}
