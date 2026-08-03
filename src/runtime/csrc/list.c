/*
 * list.c - the Zerg runtime built-in `list[T]` container.
 *
 * A list is a BY-VALUE header (`zrt_list`); only its element buffer is heap. The
 * header is embedded inline in whatever holds it (a local, a struct field, another
 * list's element) exactly like a tuple carrier, so copying/dropping a list is the
 * compiler's job at every scope boundary — this file only owns the buffer.
 *
 * The buffer is COPY-ON-WRITE. `zrt_list_copy` shares it and bumps a refcount kept in
 * a header immediately before `data`; the elements are duplicated only when somebody
 * is about to WRITE and is not the sole owner. Value semantics are unchanged — no
 * program can observe a write through another name — but the cost moves from every
 * copy to the copies that are followed by a write, and a list passed to a function
 * that only reads it costs an increment.
 *
 * The refcount is ATOMIC. A `spawn` or a channel send hands a list to another
 * coroutine, and coroutines migrate between worker threads, so two threads can hold
 * the same buffer and release it at once. The HEADER is never shared — that is what
 * "no shared mutable Zerg state" forbids — which is what makes the rest safe: a
 * buffer with rc == 1 has exactly one header pointing at it, so its owner can write
 * in place without asking anyone.
 *
 * Element teardown is indirected through a per-instance vtable (`zrt_elem_vt`): a
 * POD element carries a NULL vtable (or NULL copy/drop), taking the memcpy fast
 * path, while a non-POD element (a Ref, a struct-of-Ref, a nested list) carries
 * copy/drop thunks the compiler emits. This keeps list.c arch-neutral and generic
 * over the element type — the size/copy/drop are runtime data, not monomorphized C.
 */
#include "zergrt.h"

#include <stddef.h>
#include <string.h>

/* The buffer's refcount header sits immediately BEFORE `data`, so sharing costs no
 * second allocation and `data` stays the element pointer every reader already uses.
 *
 * `data` must stay suitably aligned for any element type, and the prefix is what decides
 * that. The union with max_align_t is not enough on its own: `max_align_t` is 8 bytes on
 * Apple silicon and 16 on x86-64 Linux, so the same source gives a different guarantee per
 * target — and the one platform where it is weaker is the one this is developed on, which
 * is the worst way to find out. The assertion says what is actually required. Every
 * element type today (scalars, a list or map header, structs of those) needs 8, so the
 * floor is 8 and it is checked rather than assumed. */
typedef union {
	int64_t     rc;
	max_align_t align;
} zrt_buf_hdr;

_Static_assert(sizeof(zrt_buf_hdr) >= 8 && sizeof(zrt_buf_hdr) % 8 == 0,
               "a list buffer's prefix must keep `data` 8-byte aligned");

static zrt_buf_hdr *buf_hdr(const zrt_list *l) {
	return (zrt_buf_hdr *)(void *)(l->data - sizeof(zrt_buf_hdr));
}

/* buf_alloc returns the element pointer of a fresh buffer of `n` slots, rc 1. */
static uint8_t *buf_alloc(size_t n, size_t elemsz) {
	zrt_buf_hdr *h = (zrt_buf_hdr *)zrt_alloc(sizeof(zrt_buf_hdr) + n * elemsz);
	h->rc = 1;
	return (uint8_t *)(void *)(h + 1);
}

/* buf_release drops one reference; the holder that brings it to zero drops every live
 * element and frees the buffer.
 *
 * Whether to drop the elements is decided by the RESULT of the decrement, never by a
 * load before it. Reading `rc == 1` first and acting on it loses a race that costs
 * real memory: two holders both read 2, both conclude they are not last, both skip the
 * elements, and the second free releases a buffer whose Refs and nested lists nobody
 * ever released. After the decrement returns 1 no other header names this buffer, so
 * walking it is safe. */
static void buf_release(zrt_list *l) {
	if (l->data == NULL) {
		return;
	}
	zrt_buf_hdr *h = buf_hdr(l);
	if (zrt_atomic_add(&h->rc, -1) != 1) {
		return;
	}
	if (l->vt != NULL && l->vt->drop != NULL) {
		for (size_t i = 0; i < l->len; i++) {
			l->vt->drop(l->data + i * l->elemsz);
		}
	}
	zrt_free(h);
}

void zrt_list_init(zrt_list *l, size_t elemsz, const zrt_elem_vt *vt) {
	l->data = NULL;
	l->len = 0;
	l->cap = 0;
	l->elemsz = elemsz;
	l->vt = vt;
}

/* zrt_list_unshare gives l a buffer nobody else holds, so the caller may write into
 * it. A sole owner (or an empty list) is already there and pays nothing.
 *
 * The duplicate is a LOGICAL copy — vt->copy per element, so a nested list or a Ref
 * element gets its own reference — which is precisely what zrt_list_copy used to do
 * eagerly at every copy site.
 *
 * Two coroutines can reach here on the same buffer at once, and both may copy: each
 * reads rc > 1, each allocates, each releases, and the second release frees the
 * original. One wasted duplicate on a race nobody can observe is the cost of not
 * holding a lock; the outcome is the same either way. Reading rc here is sound where
 * ACTING on a read of it in buf_release is not: a 1 means this header is the only one
 * left, and no other holder can appear — a header is never shared. */
void zrt_list_unshare(zrt_list *l) {
	if (l->data == NULL || zrt_atomic_load(&buf_hdr(l)->rc) == 1) {
		return;
	}

	size_t n = l->len == 0 ? 1 : l->len;
	uint8_t *data = buf_alloc(n, l->elemsz);
	if (l->vt == NULL || l->vt->copy == NULL) {
		memcpy(data, l->data, l->len * l->elemsz);
	} else {
		for (size_t i = 0; i < l->len; i++) {
			l->vt->copy(data + i * l->elemsz, l->data + i * l->elemsz);
		}
	}
	buf_release(l);
	l->data = data;
	l->cap = n;
}

/* grow reallocates the buffer to hold at least `want` elements, doubling from a
 * floor of 8. The move is a raw bit-copy of the live prefix — never vt->copy —
 * because the elements are only relocated, not logically duplicated.
 *
 * It is reached only through a caller that has already unshared, so the old buffer is
 * this list's alone and freeing it is right. */
static void zrt_list_grow(zrt_list *l, size_t want) {
	size_t cap = l->cap == 0 ? 8 : l->cap;
	while (cap < want) {
		cap *= 2;
	}
	uint8_t *data = buf_alloc(cap, l->elemsz);
	if (l->len != 0) {
		memcpy(data, l->data, l->len * l->elemsz);
	}
	if (l->data != NULL) {
		zrt_free(buf_hdr(l));
	}
	l->data = data;
	l->cap = cap;
}

void zrt_list_push(zrt_list *l, const void *elem) {
	zrt_list_unshare(l);
	if (l->len == l->cap) {
		zrt_list_grow(l, l->len + 1);
	}
	memcpy(l->data + l->len * l->elemsz, elem, l->elemsz);
	l->len++;
}

void *zrt_list_at_ref(zrt_list *l, size_t i) {
	if (i >= l->len) {
		zrt_abort_kind(ZRT_ERR_INDEX, "IndexError: index out of range");
	}
	return l->data + i * l->elemsz;
}

void *zrt_list_at(zrt_list *l, size_t i) {
	/* the bound is checked BEFORE unsharing: `xs[9] = 1` on a two-element list is an
	 * IndexError, and duplicating a shared buffer on the way to reporting it would be
	 * work done for a program that is about to abort */
	void *slot = zrt_list_at_ref(l, i);
	if (l->data == NULL || zrt_atomic_load(&buf_hdr(l)->rc) == 1) {
		return slot;
	}
	zrt_list_unshare(l);
	return l->data + i * l->elemsz;
}

void zrt_list_set(zrt_list *l, size_t i, const void *elem) {
	uint8_t *slot = (uint8_t *)zrt_list_at(l, i);
	if (l->vt != NULL && l->vt->drop != NULL) {
		l->vt->drop(slot);
	}
	memcpy(slot, elem, l->elemsz);
}

size_t zrt_list_len(const zrt_list *l) {
	return l->len;
}

void *zrt_list_get(zrt_list *l, size_t i) {
	if (i >= l->len) {
		return NULL;
	}
	return l->data + i * l->elemsz;
}

/* zrt_list_slice builds a fresh list of src's elements [lo, hi), duplicating each one the
 * way a copy does — vt->copy, or a memcpy for a POD element.
 *
 * The emitter used to open-code this loop and push raw SLOTS, which is a memcpy whatever
 * the element type: `a[0..2]` on a `list[list[int]]` gave the slice inner headers naming
 * a's buffers, with no reference taken, so `b[0][0] = 99` was read back through `a`. The
 * loop belongs here, beside zrt_list_unshare, which is the same walk.
 *
 * A range outside the list is an IndexError, the same answer `xs[i]` gives. */
void zrt_list_slice(zrt_list *dst, const zrt_list *src, size_t lo, size_t hi) {
	if (hi > src->len || lo > hi) {
		zrt_abort_kind(ZRT_ERR_INDEX, "IndexError: slice out of range");
	}
	zrt_list_init(dst, src->elemsz, src->vt);
	for (size_t i = lo; i < hi; i++) {
		const uint8_t *slot = src->data + i * src->elemsz;
		if (src->vt == NULL || src->vt->copy == NULL) {
			zrt_list_push(dst, slot);
			continue;
		}
		if (dst->len == dst->cap) {
			zrt_list_grow(dst, dst->len + 1);
		}
		src->vt->copy(dst->data + dst->len * dst->elemsz, slot);
		dst->len++;
	}
}

void zrt_list_copy(zrt_list *dst, const zrt_list *src) {
	*dst = *src;
	if (src->data != NULL) {
		zrt_atomic_add(&buf_hdr(src)->rc, 1);
	}
}

void zrt_list_drop(zrt_list *l) {
	/* the elements are dropped by the LAST holder only: a shared buffer's elements are
	 * one set of values, however many headers name them */
	buf_release(l);
	l->data = NULL;
	l->len = 0;
	l->cap = 0;
}

/* zrt_defer_* is the `void (*)(void *)` shape the cleanup stack takes, and a release does
 * not have it: some take the value and some its address, and none take a `void *`. The
 * adapter lives beside the release it adapts rather than with zrt_defer, so a program that
 * links no channels does not pull chan.c in through the cleanup stack. The ENV is always
 * the ADDRESS of the binding's storage, so a cleanup reads what the binding holds at
 * unwind time — a reassigned binding gives back what it ends up with. */
void zrt_defer_list_drop(void *p) { zrt_list_drop((zrt_list *)p); }
