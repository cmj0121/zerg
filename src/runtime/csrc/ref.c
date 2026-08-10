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
#include "zergrt.h"

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
		if (h->drop != NULL) {
			h->drop(zrt_ref_payload(h));
		}
		zrt_free(h);
	}
}

void *zrt_ref_payload(void *ref) {
	return (void *)((zrt_ref_hdr *)ref + 1);
}
