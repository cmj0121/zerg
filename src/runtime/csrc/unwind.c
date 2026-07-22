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
	g_tls.handler = frame;
}

void zrt_handler_pop(zrt_frame *frame) {
	g_tls.handler = frame->prev;
}

_Noreturn void zrt_abort(const char *msg) {
	zrt_report(msg);
	if (g_tls.handler != NULL) {
		zrt_unwind_to(g_tls.handler->mark);
		longjmp(g_tls.handler->buf, 1);
	}
	/* no handler installed (e.g. abort before program entry): last resort. Under the
	 * scheduler every coroutine installs a bottom handler in its trampoline, so a
	 * handler-less coroutine abort is contained there rather than reaching this. */
	exit(1);
}
