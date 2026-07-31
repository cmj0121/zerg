/*
 * unwind.c - the Zerg runtime abort/unwind mechanism and cleanup(defer) stack.
 *
 * There is a single teardown mechanism: one LIFO cleanup stack that holds every
 * pending deferred action - both `defer` thunks and Ref releases - so they run
 * in reverse construction order, interleaved, on every exit path. The normal
 * path calls zrt_unwind_to(mark); the abort path (zrt_abort) unwinds the same
 * stack up to the innermost handler and longjmps to it. Because both consume the
 * one stack, each pending action runs exactly once per path.
 *
 * State is gathered into one zrt_tls bundle (the cleanup stack + the abort
 * handler chain). A single-threaded program keeps one process-global bundle, so
 * this is exactly the Phase 1d behaviour; the 1e scheduler gives each coroutine
 * its own bundle and swaps the "current" one (zrt_tls_save / zrt_tls_load) around
 * every context switch. Moving the state into a switchable bundle is a source-level
 * refactor only: every zrt_ function below still operates on "the current bundle",
 * so no emitted C changes. The backend only reaches this file when a program uses
 * runtime teardown; value-only programs never touch it.
 */
#include "zergrt.h"

#include <stdlib.h>

/* the current unwind bundle: the one cleanup stack (grown on demand) and the
 * innermost abort handler. For a non-concurrent program this is the whole program's
 * state; under the scheduler it is whichever coroutine is currently running. */
/* PER-WORKER. This is the bundle of whichever coroutine this thread is running right
 * now — the scheduler loads it before a switch in and snapshots it back after. With one
 * worker that is one global, exactly as before; with M workers, a process-global would
 * mean two coroutines sharing one cleanup stack and one abort handler, and each would
 * unwind through the other's defers. The bundle belongs to the coroutine (zrt_coro.tls);
 * this is only the slot for the one currently mounted on this thread. */
static ZRT_THREAD_LOCAL zrt_tls g_tls;

/* The crash flag is true only while a coroutine unwinds an UNHANDLED crash (its target
 * handler is its outermost one). chan.c reads it through zrt_crash_active as that unwind
 * runs the coroutine's sender releases, so a channel auto-closed by a crashing last
 * sender carries a crash Err (Fork-C) rather than a clean end.
 *
 * It lives in g_tls, the bundle the scheduler saves and loads around every switch, and
 * NOT beside it as a per-worker flag — which is what it was, with a comment arguing that
 * per-worker was per-coroutine enough. It is not: a coroutine MIGRATES, and the unwind
 * that sets this runs cleanups that wake other coroutines and can hand the CPU over. When
 * that happened the crash flag stayed on the worker the crash started on, the releases
 * that ran afterwards saw it clear, and the channel closed with StopIteration — so a
 * consumer's `for v in ch` ended cleanly and the statement after it, the one the crash
 * was supposed to make unreachable, ran.
 *
 * conc_crash, one run in a hundred and twenty, printed `1 2 99`. */
bool zrt_crash_active(void) {
	return g_tls.crashing;
}

void zrt_tls_save(zrt_tls *out) {
	*out = g_tls;
}

void zrt_tls_load(const zrt_tls *in) {
	g_tls = *in;
}

void zrt_tls_free(zrt_tls *t) {
	free(t->stack);
	t->stack = NULL;
	t->len = 0;
	t->cap = 0;
	t->handler = NULL;
}

size_t zrt_scope_mark(void) {
	return g_tls.len;
}

void zrt_defer(void (*fn)(void *env), void *env) {
	if (g_tls.len == g_tls.cap) {
		size_t cap = g_tls.cap == 0 ? 16 : g_tls.cap * 2;
		zrt_cleanup *grown = (zrt_cleanup *)realloc(g_tls.stack, cap * sizeof(zrt_cleanup));
		if (grown == NULL) {
			zrt_abort("out of memory (cleanup stack)");
		}
		g_tls.stack = grown;
		g_tls.cap = cap;
	}
	g_tls.stack[g_tls.len].fn = fn;
	g_tls.stack[g_tls.len].env = env;
	g_tls.len++;
}

void zrt_unwind_to(size_t mark) {
	while (g_tls.len > mark) {
		zrt_cleanup c = g_tls.stack[--g_tls.len];
		c.fn(c.env);
	}
}

void zrt_handler_push(zrt_frame *frame) {
	frame->mark = g_tls.len;
	frame->prev = g_tls.handler;
	frame->catches = false;
	g_tls.handler = frame;
}

void zrt_handler_push_catch(zrt_frame *frame) {
	zrt_handler_push(frame);
	frame->catches = true;
}

void zrt_handler_pop(zrt_frame *frame) {
	g_tls.handler = frame->prev;
}

/* zrt_unwind_abort is the shared abort core: report the message, then unwind to the
 * innermost handler and longjmp (or exit when none). Both the string zrt_abort and
 * the Err-carrying zrt_raise_err funnel through it; the only difference is whether
 * an Err value was stashed in g_tls.taken first (Decision D). */
static _Noreturn void zrt_unwind_abort(const char *msg) {
	zrt_frame *h = g_tls.handler;
	/* A `guard` handler demotes this abort to a Result value, so it is not an error
	 * the program failed to handle: land on it WITHOUT reporting the message (the Err
	 * value carries it). Only a reporting handler (or none) prints the diagnostic. */
	if (h != NULL && h->catches) {
		zrt_unwind_to(h->mark);
		longjmp(h->buf, 1);
	}
	zrt_report(msg);
	if (h != NULL) {
		/* An unhandled coroutine crash unwinds to the coroutine's outermost handler
		 * (the trampoline's, whose prev is NULL); mark it so the sender releases run
		 * during this unwind close their channels with a crash Err. A handled abort
		 * (an inner user handler, prev != NULL) is not a crash and leaves the flag off. */
		g_tls.crashing = (h->prev == NULL);
		zrt_unwind_to(h->mark);
		g_tls.crashing = false;
		longjmp(h->buf, 1);
	}
	/* no handler installed (e.g. abort before program entry): last resort. Under the
	 * scheduler every coroutine installs a bottom handler in its trampoline, so a
	 * handler-less coroutine abort is contained there rather than reaching this. */
	exit(1);
}

_Noreturn void zrt_abort(const char *msg) {
	/* A string abort still stashes an Err so a surrounding `guard` recovers a value
	 * (with the message) rather than nothing; behaviour is otherwise unchanged. */
	g_tls.taken = zrt_err_new(msg);
	zrt_unwind_abort(msg);
}

_Noreturn void zrt_abort_kind(int kind, const char *msg) {
	/* Like zrt_abort, but the stashed Err carries a built-in KIND so a guard-recovered
	 * intrinsic error is distinguishable (`err is ValueError`, etc.). */
	g_tls.taken = zrt_err_new_kind(kind, msg);
	zrt_unwind_abort(msg);
}

zrt_err zrt_err_new(const char *msg) {
	return zrt_err_new_kind(ZRT_ERR_NONE, msg);
}

zrt_err zrt_err_new_kind(int kind, const char *msg) {
	zrt_err e;
	e.msg = msg;
	e.cause = NULL;
	e.kind = kind;
	return e;
}

zrt_err zrt_err_with_cause(const char *msg, zrt_err cause) {
	return zrt_err_chain(zrt_err_new(msg), cause);
}

zrt_err zrt_err_chain(zrt_err e, zrt_err cause) {
	e.cause = (zrt_err *)zrt_alloc(sizeof(zrt_err));
	*e.cause = cause;
	return e;
}

_Noreturn void zrt_raise_err(zrt_err e) {
	g_tls.taken = e;
	zrt_unwind_abort(e.msg);
}

zrt_err zrt_taken_err(void) {
	return g_tls.taken;
}
