/*
 * sched.c - the Zerg runtime's M:N cooperative coroutine scheduler and `spawn`.
 *
 * Linked only when a program's Manifest reports Concurrency. M worker OS threads run
 * N stackful coroutines off ONE shared FIFO run queue, with the context switch hidden
 * behind the zrt_ctx shim (ctx_arm64.S / ctx_x86_64.S / ctx_ucontext.c) and the
 * threads behind the zrt_thread shim (thread_pthread.c / thread_win32.c /
 * thread_none.c). `main` runs as the first coroutine, and its end is the PROGRAM's end:
 * the moment main's coroutine finishes the scheduler stops handing out work, every
 * worker stands down, and the process exits with main's outcome.
 *
 * That lifetime is the language's (docs/code/coroutine.md, Termination & deadlock) and
 * it is why `spawn` is fire-and-forget in the strong sense: an unfinished coroutine is
 * ABANDONED where it stands rather than drained. Abandoning it means its stack is left
 * mapped — a worker may still be standing on that stack as the program winds down, and
 * unmapping it to tidy up would be a use-after-free for the sake of a page the exiting
 * process is about to give back anyway. The OS reclaims it.
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
 * TIME is the one thing a cooperative scheduler cannot leave to the coroutines: a
 * coroutine that waits for a deadline is neither runnable nor waiting on a counterparty,
 * so the scheduler holds those itself (zrt_sleep_ns and the timer queue below). Two
 * consequences run through the loop. An idle worker sleeps to the nearest deadline
 * rather than polling the clock, and a pending sleep suspends the deadlock detector,
 * because "every worker idle and coroutines remain" only means deadlock when none of
 * those coroutines is going to move on its own.
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

/* this worker's own fiber handle, so a switch back out of a coroutine can name where it
 * is going. NULL, and never read, outside a ThreadSanitizer build. */
static ZRT_THREAD_LOCAL void *t_sched_fiber;
static ZRT_THREAD_LOCAL zrt_coro *t_current;

/* SHARED, under g_lock: the one FIFO run queue every worker drains and the
 * bookkeeping that decides when the program is over. g_cond wakes a worker
 * that is waiting for work; g_idle counts the workers currently waiting on it, which
 * is what tells "everyone is idle and coroutines remain" (a deadlock) from "everyone
 * is idle because there is nothing to do this instant".
 *
 * g_done is the program's end, and only main's coroutine finishing sets it: a worker
 * that sees it stops taking work rather than draining what is left. There is no live
 * count beside it: main's coroutine outlives every other, so "main has finished" is
 * always the earlier end, and the deadlock criterion below counts what is RUNNING and
 * what is SLEEPING rather than what exists. */
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

/* main's coroutine, under g_lock. The scheduler needs to reach it by name for one
 * thing: a deadlock is an abort, an abort runs on the stack it belongs to, and the
 * scheduler has no stack of its own to raise on — so it hands the raise to a parked
 * coroutine and resumes it to die.
 *
 * WHICH coroutine, since every one of them is equally stuck? main. It is the only one
 * certain to be alive when the detector fires (its finishing is what ends the program),
 * it is certain to be parked (nothing is runnable, or this would not be a deadlock), and
 * a deadlock is a statement about the WHOLE program, so it belongs where the program's
 * outcome is decided: main's `guard`, and main's exit code. The alternative — raise on
 * some arbitrary member of the cycle — hands a program-wide failure to whichever
 * coroutine happens to be first in a list, and a `guard` in a coroutine that has nothing
 * to do with the deadlock would swallow it.
 *
 * That a `guard` in main can also catch it is the point rather than a hazard: the spec
 * asks for a catchable abort. What follows a catch is that the other coroutines are
 * still blocked, so the next time main waits the detector fires again — every detection
 * raises, one-shot is not on offer. A `guard` in a retry loop therefore turns a deadlock
 * into a livelock, and that is the honest trade: a livelock reports its reason on every
 * turn, where a detector that gave up after one raise would leave the program hanging
 * silently, which is the failure this whole mechanism exists to prevent. */
static zrt_coro *g_main_co;

/* the number of coroutines executing on some worker right now. A worker that finds the
 * queue empty while this is non-zero must NOT call deadlock: the running coroutines can
 * still enqueue or wake others. It is the difference between "no work available at this
 * instant" and "no work will ever be available again". */
static size_t g_live_running;

/* the coroutines parked in zrt_sleep_ns, under g_lock, ordered by deadline so the head
 * is always the next one due. They are threaded on qnext, which they can borrow because
 * a sleeping coroutine is on no other queue — it is BLOCKED, so the run queue does not
 * hold it, and it never joined a channel's wait queue.
 *
 * A NON-EMPTY LIST IS READ FOR TWO THINGS: whether anything may be due, and whether the
 * deadlock detector should stand down. Every worker idle with coroutines left is a
 * deadlock only if none of those coroutines is going to move again by itself, and a
 * sleeping one is. */
static zrt_coro *g_timerq;

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

/* --- timers ------------------------------------------------------------------ */

/* timerq_insert files a sleeping coroutine by deadline; g_lock is HELD. A sorted insert
 * rather than a heap: the list is the set of sleeps pending at one instant, which is
 * small, and keeping the next deadline at the head is the only query the scheduler
 * makes of it. */
static void timerq_insert(zrt_coro *co) {
	zrt_coro **link = &g_timerq;
	while (*link != NULL && (*link)->deadline <= co->deadline) {
		link = &(*link)->qnext;
	}
	co->qnext = *link;
	*link = co;
}

/* timers_fire_due moves every coroutine whose deadline has passed back onto the run
 * queue; g_lock is HELD. Sorted order means it stops at the first one that is not due,
 * so this costs nothing on the overwhelmingly common pass where none is — and the empty
 * list is answered before the clock is read, since the scheduler loop calls this on every
 * turn and most programs never sleep at all. */
static void timers_fire_due(void) {
	if (g_timerq == NULL) {
		return;
	}
	int64_t now = zrt_time_mono();
	while (g_timerq != NULL && g_timerq->deadline <= now) {
		zrt_coro *co = g_timerq;
		g_timerq = co->qnext;
		co->state = ZRT_CORO_RUNNABLE;
		runq_push(co); /* clears qnext, which the timer queue was borrowing */
	}
}

void zrt_sleep_ns(int64_t ns) {
	zrt_coro *co = t_current;
	if (co == NULL || ns <= 0) {
		return; /* outside a coroutine there is nothing to park, and a past deadline is now */
	}
	int64_t deadline = zrt_time_mono() + ns;
	/* A LOOP, re-reading the clock, because a park is not a promise that the thing you
	 * parked for has happened — it is only a promise that you left the CPU. A wake token
	 * left standing by an earlier `select` (chan.c: the token is one-shot and survives to
	 * the coroutine's NEXT park) makes the very next park return at once, and a sleep that
	 * trusted one park returned before its deadline. Every other park site in this runtime
	 * already re-scans in a loop; a deadline is just the condition this one re-scans.
	 *
	 * The deadline is re-armed each turn: co->deadline is what tells the worker swapping
	 * us out to file us on the timer queue rather than leave us blocked forever, and the
	 * worker is what does the filing — for the reason PARKING exists at all, that until
	 * the switch is complete this coroutine is still on a CPU and a queue another worker
	 * can reach must not name it yet. */
	while (zrt_time_mono() < deadline) {
		co->deadline = deadline;
		zrt_sched_park();
	}
	co->deadline = 0;
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

/* spawn_coro is the body of zrt_spawn, with the one thing a `spawn` may not ask for:
 * whether this is coroutine 0. Only the program-entry shims below pass true, and they
 * do it before any worker exists, so the flag is set long before it can be read. */
static void spawn_coro(void (*thunk)(void *env), void *env, bool is_main) {
	zrt_coro *co = (zrt_coro *)zrt_alloc(sizeof(*co));
	size_t total = 0;
	co->stack = stack_alloc(ZRT_CORO_STACK, &total);
	co->stack_size = total;
	co->state = ZRT_CORO_RUNNABLE;
	co->woken = false; /* zrt_alloc is malloc, not calloc: an unset flag is a stray wake */
	co->is_main = is_main;
	co->deadlocked = false;
	co->deadline = 0;
	co->tsan_fiber = ZRT_TSAN_FIBER_NEW(); /* NULL, and free, unless this is a TSan build */
	co->thunk = thunk;
	co->env = env;
	co->tls.stack = NULL;
	co->tls.len = 0;
	co->tls.cap = 0;
	co->tls.handler = NULL;
	co->qnext = NULL;
	zrt_ctx_init(&co->ctx, co->stack, co->stack_size, coro_trampoline, co);
	zrt_mutex_lock(&g_lock);
	if (is_main) {
		g_main_co = co;
	}
	runq_push(co);
	zrt_cond_signal(&g_cond); /* a worker may be waiting for exactly this */
	zrt_mutex_unlock(&g_lock);
}

void zrt_spawn(void (*thunk)(void *env), void *env) {
	spawn_coro(thunk, env, false);
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

/* park_resumed is the tail both parks share: the coroutine is back on a CPU, and the
 * one question it has is why. `deadlocked` was written by the worker that queued it,
 * under g_lock, and this coroutine only runs again after being popped under that same
 * lock — so the flag is ordered without needing one of its own. It is consumed here:
 * the raise happens once, and a park later in the same coroutine starts clean. */
static bool park_resumed(zrt_coro *co) {
	bool dl = co->deadlocked;
	co->deadlocked = false;
	return dl;
}

bool zrt_sched_park_unlock(zrt_mutex *m) {
	zrt_coro *co = t_current;
	if (co == NULL) {
		zrt_mutex_unlock(m);
		return false; /* not inside a coroutine: cannot block */
	}
	co->park_lock = m;
	co->state = ZRT_CORO_PARKING;
	zrt_ctx_swap(&co->ctx, &t_sched_ctx);
	/* Resumed by some worker — not necessarily the one we left. */
	return park_resumed(co);
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
	 * necessarily the one we parked on, which is why nothing here may cache a worker.
	 *
	 * The deadlock flag is consumed rather than answered: the bare park is only reached
	 * from zrt_sleep_ns, and the detector stands down while any sleep is pending, so it
	 * cannot be set here. Consuming it keeps the one-shot rule true for the next park. */
	(void)park_resumed(co);
}

_Noreturn void zrt_sched_deadlock(void) {
	zrt_abort_kind(ZRT_ERR_DEADLOCK, "DeadlockError: all coroutines blocked (deadlock)");
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
	g_main_co = NULL;
	g_timerq = NULL;
	g_live_running = 0;
	g_idle = 0;
	g_workers = 0;
	g_done = false;
	g_exit_code = 1; /* pessimistic: main's thunk overwrites this on success */
}

/* sched_run pops and runs coroutines until main's coroutine ends the program. Each
 * switch is bracketed by the current coroutine's unwind bundle: load it before swapping
 * in, snapshot it back on return. A DONE coroutine is reclaimed (its stack unmapped and
 * its cleanup buffer freed); a coroutine that yielded is re-enqueued.
 *
 * g_done is tested before the queue rather than only when the queue runs dry, so main
 * finishing stops the other workers at their next scheduling point instead of letting
 * them work through whatever `spawn` left behind. */
static void sched_run(void) {
	t_sched_fiber = ZRT_TSAN_FIBER_SELF();
	zrt_mutex_lock(&g_lock);
	for (;;) {
		if (g_done) {
			break; /* main has finished: stand down, whatever is still queued */
		}
		timers_fire_due(); /* a due sleep is runnable work, so look before the queue */
		zrt_coro *co = runq_pop();
		if (co == NULL) {
			/* Coroutines remain but none is runnable HERE, which is three different
			 * situations and they are told apart below in this order: a sleep is pending
			 * and will become runnable by itself; every worker is idle with nothing running
			 * and nothing sleeping, so nobody can ever wake anybody and this is a deadlock;
			 * or the other workers still hold work, and this one waits for them. */
			g_idle++;
			if (g_timerq != NULL) {
				/* A sleep is pending, so this is not a deadlock however idle everyone is —
				 * that coroutine will become runnable on its own. Sleep to its deadline
				 * rather than to a signal: with one worker there is nobody to signal, and
				 * with several an enqueue still cuts the wait short. */
				zrt_cond_timedwait(&g_cond, &g_lock, g_timerq->deadline - zrt_time_mono());
				g_idle--;
				continue;
			}
			if (g_idle == g_workers && g_live_running == 0) {
				/* Hand the raise to main and put it back on the queue. Marking and queueing
				 * are both under g_lock and main is BLOCKED (the precondition above says so),
				 * so this is the same transition a wake makes — the park site main resumes in
				 * unlinks its waiter and calls zrt_sched_deadlock, which raises on main's own
				 * stack with main's own defers and abort handler.
				 *
				 * No cond_wait after it: the work just queued is this worker's own, and with
				 * one worker there is nobody left to signal it awake. */
				g_main_co->deadlocked = true;
				g_main_co->state = ZRT_CORO_RUNNABLE;
				runq_push(g_main_co);
				g_idle--;
				continue;
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
		/* the two announcements bracketing the switch are what let ThreadSanitizer follow
		 * a coroutine across stacks and across workers; both vanish in a normal build. */
		ZRT_TSAN_FIBER_SWITCH(co->tsan_fiber);
		zrt_ctx_swap(&t_sched_ctx, &co->ctx); /* run it until it yields, parks, or finishes */
		ZRT_TSAN_FIBER_SWITCH(t_sched_fiber);
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
		case ZRT_CORO_DONE: {
			bool was_main = co->is_main;
			zrt_tls_free(&co->tls);
			ZRT_TSAN_FIBER_FREE(co->tsan_fiber);
			munmap(co->stack, co->stack_size);
			zrt_free(co);
			if (was_main) {
				/* main is over, so the program is. Every other worker is either idle or
				 * between two coroutines, so broadcast rather than signal: each has to
				 * come back round and see g_done, and none of them is waiting for work
				 * any more. Whatever is still queued or parked stays where it is. */
				g_done = true;
				zrt_cond_broadcast(&g_cond);
			}
			break;
		}
		case ZRT_CORO_RUNNING: /* swapped out without saying why: treat as a yield */
		case ZRT_CORO_RUNNABLE:
			co->state = ZRT_CORO_RUNNABLE;
			runq_push(co); /* voluntarily yielded: back on the queue */
			zrt_cond_signal(&g_cond);
			break;
		case ZRT_CORO_PARKING:
		case ZRT_CORO_BLOCKED:
			if (co->deadline != 0) {
				/* a sleep, not a channel wait: nothing will ever wake it, so the scheduler
				 * itself has to, and files it by deadline now that the switch is complete. */
				timerq_insert(co);
			}
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
	spawn_coro(main_thunk_result, NULL, true);
	sched_drain();
	return g_exit_code;
}

int zrt_sched_main_nil(void (*fn)(void)) {
	sched_init();
	g_main_nil = fn;
	spawn_coro(main_thunk_nil, NULL, true);
	sched_drain();
	return g_exit_code;
}

int zrt_sched_main_int(int64_t (*fn)(void)) {
	sched_init();
	g_main_int = fn;
	spawn_coro(main_thunk_int, NULL, true);
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
	spawn_coro(run_body_thunk, NULL, true);
	sched_drain();
}
