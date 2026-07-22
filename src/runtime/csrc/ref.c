/*
 * ref.c - the Zerg runtime Ref[T] refcounted heap box.
 *
 * A Ref is one allocation laid out as `[ zrt_ref_hdr | payload... ]`; a Zerg
 * `Ref[T]` value is a pointer to the header. The refcount is a non-atomic
 * size_t (single-threaded MVP, per the design's Fork-2); the atomic swap for
 * the 1e scheduler is confined to zrt_retain/zrt_release so emitted code never
 * changes. The referent is fixed at construction, so there are no cycles to
 * collect (the MVP would otherwise leak them).
 */
#include "zergrt.h"

void *zrt_ref_alloc(size_t payload_sz, zrt_drop_fn drop) {
	zrt_ref_hdr *h = (zrt_ref_hdr *)zrt_alloc(sizeof(zrt_ref_hdr) + payload_sz);
	h->rc = 1;
	h->drop = drop;
	return h;
}

void zrt_retain(void *ref) {
	if (ref == NULL) {
		return;
	}
	((zrt_ref_hdr *)ref)->rc++;
}

void *zrt_ref_copy(void *ref) {
	zrt_retain(ref);
	return ref;
}

void zrt_release(void *ref) {
	zrt_ref_hdr *h;
	if (ref == NULL) {
		return;
	}
	h = (zrt_ref_hdr *)ref;
	if (--h->rc == 0) {
		if (h->drop != NULL) {
			h->drop(zrt_ref_payload(h));
		}
		zrt_free(h);
	}
}

void *zrt_ref_payload(void *ref) {
	return (void *)((zrt_ref_hdr *)ref + 1);
}
