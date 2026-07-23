/*
 * list.c - the Zerg runtime built-in `list[T]` container.
 *
 * A list is a BY-VALUE header (`zrt_list`); only its element buffer is heap. The
 * header is embedded inline in whatever holds it (a local, a struct field, another
 * list's element) exactly like a tuple carrier, so copying/dropping a list is the
 * compiler's job at every scope boundary — this file only owns the buffer.
 *
 * Element teardown is indirected through a per-instance vtable (`zrt_elem_vt`): a
 * POD element carries a NULL vtable (or NULL copy/drop), taking the memcpy fast
 * path, while a non-POD element (a Ref, a struct-of-Ref, a nested list) carries
 * copy/drop thunks the compiler emits. This keeps list.c arch-neutral and generic
 * over the element type — the size/copy/drop are runtime data, not monomorphized C.
 */
#include "zergrt.h"

#include <string.h>

void zrt_list_init(zrt_list *l, size_t elemsz, const zrt_elem_vt *vt) {
	l->data = NULL;
	l->len = 0;
	l->cap = 0;
	l->elemsz = elemsz;
	l->vt = vt;
}

/* grow reallocates the buffer to hold at least `want` elements, doubling from a
 * floor of 8. The move is a raw bit-copy of the live prefix — never vt->copy —
 * because the elements are only relocated, not logically duplicated. */
static void zrt_list_grow(zrt_list *l, size_t want) {
	size_t cap = l->cap == 0 ? 8 : l->cap;
	while (cap < want) {
		cap *= 2;
	}
	uint8_t *data = (uint8_t *)zrt_alloc(cap * l->elemsz);
	if (l->len != 0) {
		memcpy(data, l->data, l->len * l->elemsz);
	}
	zrt_free(l->data);
	l->data = data;
	l->cap = cap;
}

void zrt_list_push(zrt_list *l, const void *elem) {
	if (l->len == l->cap) {
		zrt_list_grow(l, l->len + 1);
	}
	memcpy(l->data + l->len * l->elemsz, elem, l->elemsz);
	l->len++;
}

void *zrt_list_at(zrt_list *l, size_t i) {
	if (i >= l->len) {
		zrt_abort("index out of range");
	}
	return l->data + i * l->elemsz;
}

void zrt_list_set(zrt_list *l, size_t i, const void *elem) {
	if (i >= l->len) {
		zrt_abort("index out of range");
	}
	uint8_t *slot = l->data + i * l->elemsz;
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

void zrt_list_copy(zrt_list *dst, const zrt_list *src) {
	dst->len = src->len;
	dst->cap = src->len;
	dst->elemsz = src->elemsz;
	dst->vt = src->vt;
	if (src->len == 0) {
		dst->data = NULL;
		return;
	}
	dst->data = (uint8_t *)zrt_alloc(src->len * src->elemsz);
	if (src->vt == NULL || src->vt->copy == NULL) {
		memcpy(dst->data, src->data, src->len * src->elemsz);
		return;
	}
	for (size_t i = 0; i < src->len; i++) {
		src->vt->copy(dst->data + i * src->elemsz, src->data + i * src->elemsz);
	}
}

void zrt_list_drop(zrt_list *l) {
	if (l->vt != NULL && l->vt->drop != NULL) {
		for (size_t i = 0; i < l->len; i++) {
			l->vt->drop(l->data + i * l->elemsz);
		}
	}
	zrt_free(l->data);
	l->data = NULL;
	l->len = 0;
	l->cap = 0;
}
