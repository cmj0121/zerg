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
static zrt_tls g_tls;

/* g_crashing is true only while zrt_abort unwinds an UNHANDLED coroutine crash (its
 * target handler is the coroutine's outermost one). chan.c reads it through
 * zrt_crash_active as the crash unwind runs the coroutine's sender releases, so a
 * channel auto-closed by a crashing last sender carries a crash Err (Fork-C). It is
 * self-scoped: each zrt_abort sets it for the duration of that unwind and clears it. */
static bool g_crashing;

bool zrt_crash_active(void) {
	return g_crashing;
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
		g_crashing = (h->prev == NULL);
		zrt_unwind_to(h->mark);
		g_crashing = false;
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

zrt_err zrt_err_new(const char *msg) {
	zrt_err e;
	e.msg = msg;
	e.cause = NULL;
	return e;
}

zrt_err zrt_err_with_cause(const char *msg, zrt_err cause) {
	zrt_err e;
	e.msg = msg;
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
