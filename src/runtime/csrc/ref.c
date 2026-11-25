/*
 * ref.c - the Zerg runtime Ref[T] refcounted heap box.
 *
 * A Ref is one allocation laid out as `[ zrt_ref_hdr | payload... ]`; a Zerg
 * `Ref[T]` value is a pointer to the header. The referent is fixed at
 * construction, so there are no cycles to collect (the MVP would otherwise leak
 * them).
 *
 * The refcount is ATOMIC. It was a plain `size_t++` while the scheduler was
 * single-threaded. It has to be atomic because `spawn` hands a `Ref[T]` to a
 * coroutine a DIFFERENT worker thread may run, so two threads reach the same
 * header: a non-atomic increment loses one of two concurrent retains, and the
 * racing pair is UB besides. The exposure is wider than `Ref[T]` alone — since
 * S2 every managed `str` counts through zrt_str_retain/release (fmt.c), which
 * defer to these two.
 *
 * The count is spelled with the raw `__atomic_*` builtins rather than the
 * `zrt_atomic_*` wrappers list.c uses, because those take an `int64_t *` and
 * this count is a `size_t` whose immortal sentinel is SIZE_MAX — which would
 * read back as -1 through them.
 */
#include <string.h>  /* memcpy, memset */

#include "zergrt.h"

/* dyn_drop is what a dyn cell puts in its header, and it exists because of WHERE the header
 * contract hands a drop its pointer: `zrt_release` calls `hdr.drop(zrt_ref_payload(h))`, the
 * word after the ref header, which on a dyn cell is `vt` and not the payload. Recovering the
 * header from that address is the whole of it; the payload's own teardown is in the table. */
static void dyn_drop(void *after_ref_hdr) {
	zrt_dyn_hdr *h = (zrt_dyn_hdr *)((char *)after_ref_hdr - offsetof(zrt_dyn_hdr, vt));
	const zrt_elem_vt *vt = (const zrt_elem_vt *)h->vt;
	if (vt != NULL && vt->drop != NULL) {
		vt->drop(zrt_dyn_payload(h));
	}
}

void *zrt_dyn_alloc(size_t payload_sz, const void *vt) {
	zrt_dyn_hdr *h = (zrt_dyn_hdr *)zrt_alloc(sizeof(zrt_dyn_hdr) + payload_sz);
	h->hdr.rc = 1;
	h->hdr.drop = dyn_drop;
	h->vt = vt;
	h->size = payload_sz;
	return h;
}

void *zrt_dyn_copy(const void *cell) {
	if (cell == NULL) {
		return NULL;
	}
	const zrt_dyn_hdr *s = (const zrt_dyn_hdr *)cell;
	void *d = zrt_dyn_alloc(s->size, s->vt);
	const zrt_elem_vt *vt = (const zrt_elem_vt *)s->vt;
	if (vt != NULL && vt->copy != NULL) {
		vt->copy(zrt_dyn_payload(d), zrt_dyn_payload(cell));
	} else {
		memcpy(zrt_dyn_payload(d), zrt_dyn_payload(cell), s->size);
	}
	return d;
}

void zrt_dyn_free(void *cell) {
	zrt_free(cell);
}

const void *zrt_dyn_vt(const void *cell) {
	return ((const zrt_dyn_hdr *)cell)->vt;
}

void *zrt_dyn_payload(const void *cell) {
	return (void *)((const char *)cell + sizeof(zrt_dyn_hdr));
}

void *zrt_ref_alloc(size_t payload_sz, zrt_drop_fn drop) {
	zrt_ref_hdr *h = (zrt_ref_hdr *)zrt_alloc(sizeof(zrt_ref_hdr) + payload_sz);
	h->rc = 1;
	h->drop = drop;
	return h;
}

/* The sentinel test is an atomic LOAD, not a plain read: on a live cell the same word is
 * being incremented and decremented by other threads, so reading it with `==` is the very
 * race this file exists to remove. Relaxed is enough — what it reads is either the immortal
 * sentinel, which is never written, or a live count whose exact value is not acted on.
 *
 * The load cannot be folded into the RMW that follows it. A speculative increment would wrap
 * SIZE_MAX to 0, and a concurrent release then frees a string literal; the sound fold is a
 * CAS retry loop, which is worse than a load and an add. It costs ~0.3ns/pair uncontended
 * (the line is already local and the branch overlaps it), and it saves the whole atomic on
 * every literal, which is the common case in a program full of them. */
static inline bool rc_immortal(const zrt_ref_hdr *h) {
	return __atomic_load_n(&h->rc, __ATOMIC_RELAXED) == ZRT_RC_IMMORTAL;
}

void zrt_retain(void *ref) {
	if (ref == NULL) {
		return;
	}
	zrt_ref_hdr *h = (zrt_ref_hdr *)ref;
	/* An immortal cell (a string literal / constant result) is never counted. */
	if (rc_immortal(h)) {
		return;
	}
	/* RELAXED, and the asymmetry with the release below is measured rather than assumed. A
	 * caller of zrt_retain already holds a reference, so the increment orders nothing — and
	 * on arm64 the acquire/release bit is the difference between `ldadd` and `ldaddal`, worth
	 * 8.1ns -> 4.5ns per retain+release pair, which every managed `str` copy pays. On x86-64
	 * both spellings emit the same `lock incq`, so nothing is lost there. */
	__atomic_fetch_add(&h->rc, (size_t)1, __ATOMIC_RELAXED);
}

void *zrt_ref_copy(void *ref) {
	zrt_retain(ref);
	return ref;
}

static void release_cell(zrt_ref_hdr *h);

void zrt_release(void *ref) {
	if (ref == NULL) {
		return;
	}
	zrt_ref_hdr *h = (zrt_ref_hdr *)ref;
	/* An immortal cell (a string literal / constant result) is never freed. */
	if (rc_immortal(h)) {
		return;
	}
	/* The decrement's RESULT decides, never a second read: two threads dropping the last two
	 * holders would both see a zero if they re-read, and both free. Exactly one of them gets
	 * 1 back from the exchange, and that one owns the teardown.
	 *
	 * SEQ_CST here and not the textbook acq_rel, because on arm64 the two emit the identical
	 * `ldaddal` — the weakening is free to state and worth nothing to take. The release must
	 * carry the ordering the retain does not: it is what makes the drop see every write the
	 * other holders made. */
	if (__atomic_fetch_sub(&h->rc, (size_t)1, __ATOMIC_SEQ_CST) == 1) {
		if (h->drop == NULL) {
			zrt_free(h);
			return;
		}
		release_cell(h);
	}
}

/* --- the drain (#479) -----------------------------------------------------------------
 *
 * A release used to run its drop in place, and the drop released the value's children in
 * place too, so tearing down a chain a million links long nested a million C frames and
 * overflowed the stack. Every release that has drop work now pushes an entry onto the
 * mounted bundle's pending array and one loop per scope runs them, one step at a time: a
 * CELL step runs the cell's drop, a LIST/MAP step drops one element of a dead buffer and
 * advances its cursor. Whatever a step released lands above its entry and is drained before
 * the entry's next step, so the order is the depth-first one of the recursion it replaces,
 * a child is freed before its parent, and the array grows with the value's DEPTH, not its
 * width.
 *
 * The bundle is re-read after every step: a drop can park, and the coroutine come back on
 * another worker. Entries are named by index, never by a pointer kept across a step,
 * because a step's own pushes may move the array. */

static zrt_pending *pend_push(zrt_tls *t) {
	if (t->plen == t->pcap) {
		zrt_pending *grown = (zrt_pending *)zrt_grow(t->pend, &t->pcap, sizeof(zrt_pending));
		if (grown == NULL) {
			zrt_abort("out of memory (pending releases)");
		}
		t->pend = grown;
	}
	zrt_pending *e = &t->pend[t->plen++];
	memset(e, 0, sizeof(*e));
	return e;
}

void zrt_pend_cell(zrt_ref_hdr *h) {
	zrt_pending *e = pend_push(zrt_tls_cur());
	e->kind = ZRT_PEND_CELL;
	e->block = h;
}

void zrt_pend_list(void *hdr, uint8_t *data, size_t len, size_t elemsz, const zrt_elem_vt *vt) {
	zrt_pending *e = pend_push(zrt_tls_cur());
	e->kind = ZRT_PEND_LIST;
	e->block = hdr;
	e->data = data;
	e->len = len;
	e->stride = elemsz;
	e->vt = vt;
}

void zrt_pend_map(void *block, uint8_t *entries, int64_t *buckets, size_t len, size_t entrysz,
                  size_t koff, size_t voff, const zrt_map_vt *vt) {
	zrt_pending *e = pend_push(zrt_tls_cur());
	e->kind = ZRT_PEND_MAP;
	e->block = block;
	e->data = entries;
	e->len = len;
	e->stride = entrysz;
	e->koff = koff;
	e->voff = voff;
	e->vt = vt;
	e->buckets = buckets;
}

/* pend_settle puts what one step pushed, pend[top..plen), into release order: the first
 * child the drop released is the next one popped. */
static void pend_settle(zrt_tls *t, size_t top) {
	size_t lo = top;
	size_t hi = t->plen;
	while (hi > lo + 1) {
		hi--;
		zrt_pending tmp = t->pend[lo];
		t->pend[lo] = t->pend[hi];
		t->pend[hi] = tmp;
		lo++;
	}
}

void zrt_drain_run(void) {
	zrt_tls *t = zrt_tls_cur();
	t->dactive = true;
	while (t->plen > t->pbase) {
		size_t k = t->plen - 1;
		size_t top = t->plen;
		zrt_pending *e = &t->pend[k];
		if (e->kind == ZRT_PEND_CELL) {
			zrt_ref_hdr *h = (zrt_ref_hdr *)e->block;
			if (e->dropped) {
				t->plen--;
				zrt_free(h);
			} else {
				e->dropped = true;
				h->drop(zrt_ref_payload(h));
				t = zrt_tls_cur();
				if (t->plen - top > 1) {
					pend_settle(t, top);
				}
			}
		} else if (e->next == e->len) {
			void *block = e->block;
			int64_t *buckets = e->buckets;
			t->plen--;
			zrt_free(block);
			if (e->kind == ZRT_PEND_MAP) {
				zrt_free(buckets);
			}
		} else {
			uint8_t *at = e->data + e->next++ * e->stride;
			if (e->kind == ZRT_PEND_LIST) {
				((const zrt_elem_vt *)e->vt)->drop(at);
			} else {
				const zrt_map_vt *vt = (const zrt_map_vt *)e->vt;
				size_t voff = e->voff;
				if (vt->key_drop != NULL) {
					vt->key_drop(at + e->koff);
				}
				if (vt->val_drop != NULL) {
					vt->val_drop(at + voff);
				}
			}
			t = zrt_tls_cur();
			if (t->plen - top > 1) {
				pend_settle(t, top);
			}
		}
	}
	t->dactive = false;
}

/* DURING A STACK OVERFLOW'S UNWIND (#486) a drain runs nothing: the unwind is still on the
 * deep stack, and an element or cell drop run there re-enters the chain that overflowed.
 * The entries the caller pushed stay above the handler's mark for land_drain's loop. */
void zrt_drain(void) {
	zrt_tls *t = zrt_tls_cur();
	if (t->overflow_unwind || t->dactive) {
		return;
	}
	zrt_drain_run();
}

/* release_cell is the drain's first step run in place (D6): outside any loop, with nothing
 * owed in this scope, the cell's drop runs directly and the loop starts only for what that
 * drop released. The cell sits in pend marked dropped while its drop runs, with dactive set,
 * exactly as the loop's own CELL step leaves it, so an abort's landing drain frees it — unless
 * an overflow stopped the drop at its action's entry, which un-marks it to run there (#487). */
static void release_cell(zrt_ref_hdr *h) {
	zrt_tls *t = zrt_tls_cur();
	/* DURING A STACK OVERFLOW'S UNWIND (#480) a cell's drop is QUEUED, not run: the unwind is
	 * still on the deep stack, and a user drop run there re-enters the chain that overflowed
	 * (drop -> release -> drop) and hits the probe a second time, which is the hard fault. The
	 * entry sits above the handler's mark, so land_drain's flat loop runs it, still on the deep
	 * stack but one drop deep, before zrt_stack_land restores the limit and the flag; a drop it
	 * runs that releases another cell queues that one too, and the loop takes it next. */
	if (t->overflow_unwind) {
		zrt_pend_cell(h);
		return;
	}
	if (t->dactive || t->plen != t->pbase) {
		zrt_pend_cell(h);
		zrt_drain();
		return;
	}
	zrt_pending *e = pend_push(t);
	e->kind = ZRT_PEND_CELL;
	e->block = h;
	e->dropped = true;
	size_t top = t->plen;
	t->dactive = true;
	h->drop(zrt_ref_payload(h));
	t = zrt_tls_cur();
	if (t->plen == top) {
		t->plen--;
		t->dactive = false;
		zrt_free(h);
		return;
	}
	if (t->plen - top > 1) {
		pend_settle(t, top);
	}
	t->dactive = false;
	zrt_drain();
}

zrt_drain_scope zrt_drain_open(void) {
	zrt_tls *t = zrt_tls_cur();
	zrt_drain_scope s = {t->pbase, t->dactive};
	t->pbase = t->plen;
	t->dactive = false;
	return s;
}

void zrt_drain_close(zrt_drain_scope s) {
	zrt_tls *t = zrt_tls_cur();
	t->pbase = s.pbase;
	t->dactive = s.dactive;
}

void *zrt_ref_payload(void *ref) {
	return (void *)((zrt_ref_hdr *)ref + 1);
}
