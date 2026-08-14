/*
 * memcount.c — a counting allocator, linked in place of the runtime's alloc.c.
 *
 * `scripts/mem-check.sh` links every runtime unit EXCEPT alloc.c and adds this one,
 * so these two functions ARE zrt_alloc/zrt_free for that build. The runtime that
 * ships is untouched: a counter in it would cost an atomic on every allocation for
 * a number only a gate ever reads.
 *
 * The precedent is src/runtime/runtime_test.go, which swaps the same unit out
 * (`coreUnitsExcept(cfiles, "alloc.c")`) so that alloc/free balance can stand in for
 * the LeakSanitizer macOS does not have.
 *
 * The counters are atomic because a worker thread frees what another thread
 * allocated — the same reason ref.c reaches for __atomic_fetch_add for a refcount.
 * `total` never decreases, so it is the floor that says the program allocated at all.
 */
#include "zergrt.h"

#include <stdio.h>
#include <stdlib.h>

static long g_total;
static long g_live;

void *zrt_alloc(size_t n) {
	void *p = malloc(n);
	if (p == NULL && n != 0) {
		zrt_abort("out of memory");
	}
	/* counted only when there is a pointer to give back, because zrt_free below only
	 * decrements for one — malloc(0) is allowed to answer NULL, and an increment with
	 * no matching decrement would read as a leak the program does not have. */
	if (p != NULL) {
		__atomic_fetch_add(&g_total, 1, __ATOMIC_RELAXED);
		__atomic_fetch_add(&g_live, 1, __ATOMIC_RELAXED);
	}
	return p;
}

void zrt_free(void *p) {
	if (p != NULL) {
		__atomic_fetch_sub(&g_live, 1, __ATOMIC_RELAXED);
	}
	free(p);
}

/*
 * stderr, not stdout: the case programs print their own answer and the script reads
 * both streams apart, so a counter line can never be mistaken for the program's output.
 */
static void memcount_report(void) {
	fprintf(stderr, "zrt-mem: total=%ld live=%ld\n",
		__atomic_load_n(&g_total, __ATOMIC_RELAXED),
		__atomic_load_n(&g_live, __ATOMIC_RELAXED));
}

__attribute__((constructor)) static void memcount_install(void) {
	atexit(memcount_report);
}
