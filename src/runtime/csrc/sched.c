/*
 * sched.c - the Zerg runtime's N:1 cooperative coroutine scheduler and `spawn`.
 *
 * Linked only when a program's Manifest reports Concurrency. The model is one OS
 * thread, a FIFO run queue of stackful coroutines, and a context switch hidden
 * behind the zrt_ctx shim (ctx_arm64.S / ctx_x86_64.S / ctx_ucontext.c). `main`
 * runs as the first coroutine; the scheduler drains the run queue, so a
 * fire-and-forget `spawn` still gets to run (it is never joined, but not killed),
 * and the program exits with main's outcome once nothing is runnable.
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

/* the scheduler's own saved context: coroutines swap back here to yield/finish */
static zrt_ctx g_sched_ctx;

/* the currently running coroutine, or NULL while the scheduler loop runs */
static zrt_coro *g_current;

/* the FIFO run queue of RUNNABLE coroutines */
static zrt_coro *g_runq_head;
static zrt_coro *g_runq_tail;

/* main's outcome, mapped to the process exit code; pessimistically 1 so an
 * aborting main (whose thunk never records success) exits non-zero, as zrt_run. */
static int g_exit_code;

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
	zrt_ctx_swap(&co->ctx, &g_sched_ctx); /* back to the scheduler; never returns */
}

/* --- spawn ------------------------------------------------------------------- */

void zrt_spawn(void (*thunk)(void *env), void *env) {
	zrt_coro *co = (zrt_coro *)zrt_alloc(sizeof(*co));
	size_t total = 0;
	co->stack = stack_alloc(ZRT_CORO_STACK, &total);
	co->stack_size = total;
	co->state = ZRT_CORO_RUNNABLE;
	co->thunk = thunk;
	co->env = env;
	co->tls.stack = NULL;
	co->tls.len = 0;
	co->tls.cap = 0;
	co->tls.handler = NULL;
	co->qnext = NULL;
	zrt_ctx_init(&co->ctx, co->stack, co->stack_size, coro_trampoline, co);
	runq_push(co);
}

void zrt_yield(void) {
	zrt_coro *co = g_current;
	if (co == NULL) {
		return; /* not inside a coroutine */
	}
	co->state = ZRT_CORO_RUNNABLE;
	zrt_ctx_swap(&co->ctx, &g_sched_ctx);
}

/* --- the scheduler loop ------------------------------------------------------ */

static void sched_init(void) {
	g_runq_head = NULL;
	g_runq_tail = NULL;
	g_current = NULL;
	g_exit_code = 1; /* pessimistic: main's thunk overwrites this on success */
}

/* sched_run pops and runs coroutines until nothing is runnable. Each switch is
 * bracketed by the current coroutine's unwind bundle: load it before swapping in,
 * snapshot it back on return. A DONE coroutine is reclaimed (its stack unmapped and
 * its cleanup buffer freed); a coroutine that yielded is re-enqueued. */
static void sched_run(void) {
	for (;;) {
		zrt_coro *co = runq_pop();
		if (co == NULL) {
			return; /* run queue drained: the program is finished */
		}
		g_current = co;
		zrt_tls_load(&co->tls);               /* make this coroutine's unwind state current */
		zrt_ctx_swap(&g_sched_ctx, &co->ctx); /* run it until it yields or finishes */
		zrt_tls_save(&co->tls);               /* snapshot its unwind state back */
		g_current = NULL;
		if (co->state == ZRT_CORO_DONE) {
			zrt_tls_free(&co->tls);
			munmap(co->stack, co->stack_size);
			zrt_free(co);
		} else {
			runq_push(co); /* yielded: back on the queue */
		}
	}
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
	sched_run();
	return g_exit_code;
}

int zrt_sched_main_nil(void (*fn)(void)) {
	sched_init();
	g_main_nil = fn;
	zrt_spawn(main_thunk_nil, NULL);
	sched_run();
	return g_exit_code;
}

int zrt_sched_main_int(int64_t (*fn)(void)) {
	sched_init();
	g_main_int = fn;
	zrt_spawn(main_thunk_int, NULL);
	sched_run();
	return g_exit_code;
}
