/*
 * alloc.c - the Zerg runtime allocator wrapper.
 *
 * A thin layer over libc malloc/free. It exists as its own seam so a later
 * freestanding backend can swap the platform allocator here without the Zerg
 * backend re-emitting any code: everything above allocates through zrt_alloc.
 */
#include "zergrt.h"

#include <stdlib.h>

void *zrt_alloc(size_t n) {
	void *p = malloc(n);
	if (p == NULL && n != 0) {
		zrt_abort("out of memory");
	}
	return p;
}

void zrt_free(void *p) {
	free(p);
}
