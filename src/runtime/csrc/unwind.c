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
 * State is process-global and non-atomic: the MVP is single-threaded (the 1e
 * scheduler will make this per-coroutine). The backend only reaches this file
 * when a program actually uses runtime teardown; value-only programs never
 * touch it.
 */
#include "zergrt.h"

#include <stdlib.h>

/* the one cleanup stack, grown on demand */
static zrt_cleanup *g_stack;
static size_t       g_len;
static size_t       g_cap;

/* the innermost abort handler, or NULL at the outermost (pre-entry) level */
static zrt_frame *g_handler;

size_t zrt_scope_mark(void) {
	return g_len;
}

void zrt_defer(void (*fn)(void *env), void *env) {
	if (g_len == g_cap) {
		size_t cap = g_cap == 0 ? 16 : g_cap * 2;
		zrt_cleanup *grown = (zrt_cleanup *)realloc(g_stack, cap * sizeof(zrt_cleanup));
		if (grown == NULL) {
			zrt_abort("out of memory (cleanup stack)");
		}
		g_stack = grown;
		g_cap = cap;
	}
	g_stack[g_len].fn = fn;
	g_stack[g_len].env = env;
	g_len++;
}

void zrt_unwind_to(size_t mark) {
	while (g_len > mark) {
		zrt_cleanup c = g_stack[--g_len];
		c.fn(c.env);
	}
}

void zrt_handler_push(zrt_frame *frame) {
	frame->mark = g_len;
	frame->prev = g_handler;
	g_handler = frame;
}

void zrt_handler_pop(zrt_frame *frame) {
	g_handler = frame->prev;
}

_Noreturn void zrt_abort(const char *msg) {
	zrt_report(msg);
	if (g_handler != NULL) {
		zrt_unwind_to(g_handler->mark);
		longjmp(g_handler->buf, 1);
	}
	/* no handler installed (e.g. abort before program entry): last resort */
	exit(1);
}
