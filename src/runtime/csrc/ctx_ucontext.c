/*
 * ctx_ucontext.c - the portable floor for the zrt_ctx shim (Fork-A fallback).
 *
 * Implements the same two symbols as the per-arch .S files using POSIX ucontext,
 * so a host without a hand-written context switch can still run the scheduler. It
 * is deliberately the LAST resort: ucontext is deprecated on macOS and saves the
 * signal mask on every swap (a syscall-class cost), which is why arm64/amd64 use
 * asm instead. The driver selects this only for other hosted arches.
 *
 * zrt_ctx.slots is far smaller than a ucontext_t, so the real machine context
 * lives in a heap block (zrt_uctx) and slots[0] holds a pointer to it. The
 * scheduler's own g_sched_ctx is a zeroed zrt_ctx that was never zrt_ctx_init'd;
 * zrt_ctx_swap lazily allocates its save block on first use.
 */
#define _XOPEN_SOURCE 700

#include "zergrt.h"

#include <stdint.h>
#include <ucontext.h>

/* zrt_uctx is the heap-resident machine context plus the marshalled entry/arg a
 * freshly armed context starts with. */
typedef struct {
	ucontext_t uc;
	void (*entry)(void *);
	void *arg;
} zrt_uctx;

/* uctx_of returns the zrt_uctx a zrt_ctx points at, allocating a bare save block
 * the first time (used for the scheduler context, which is never zrt_ctx_init'd). */
static zrt_uctx *uctx_of(zrt_ctx *c) {
	zrt_uctx *u = (zrt_uctx *)c->slots[0];
	if (u == NULL) {
		u = (zrt_uctx *)zrt_alloc(sizeof(*u));
		u->entry = NULL;
		u->arg = NULL;
		c->slots[0] = u;
	}
	return u;
}

/* uctx_start is the makecontext landing pad. makecontext passes only int args, so
 * the zrt_uctx pointer is threaded through as two 32-bit halves and reassembled. */
static void uctx_start(unsigned hi, unsigned lo) {
	uintptr_t p = ((uintptr_t)hi << 32) | (uintptr_t)lo;
	zrt_uctx *u = (zrt_uctx *)p;
	u->entry(u->arg);
	/* the coroutine trampoline never returns; if it somehow does, halt this
	 * context by returning into its (unset) uc_link, which we leave NULL. */
}

void zrt_ctx_init(zrt_ctx *c, void *stack_base, size_t size, void (*entry)(void *), void *arg) {
	zrt_uctx *u = (zrt_uctx *)zrt_alloc(sizeof(*u));
	u->entry = entry;
	u->arg = arg;
	getcontext(&u->uc);
	u->uc.uc_stack.ss_sp = stack_base;
	u->uc.uc_stack.ss_size = size;
	u->uc.uc_link = NULL;
	uintptr_t p = (uintptr_t)u;
	makecontext(&u->uc, (void (*)(void))uctx_start, 2, (unsigned)(p >> 32), (unsigned)(p & 0xffffffffu));
	c->slots[0] = u;
}

void zrt_ctx_swap(zrt_ctx *from, zrt_ctx *to) {
	zrt_uctx *f = uctx_of(from);
	zrt_uctx *t = uctx_of(to);
	swapcontext(&f->uc, &t->uc);
}
