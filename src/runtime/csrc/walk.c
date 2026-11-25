/*
 * walk.c - the Zerg runtime's walkers over deep values (#481).
 *
 * A derived `==` or `Hash` on a type that reaches itself was a recursion through the type's
 * own `eq` or `hash`, one C frame per level, so a value built deep enough to be worth
 * building overflowed the stack comparing or hashing it. Here the recursion is a loop over
 * an explicit stack of frames, each naming a STEP the compiler emitted for one type, the
 * operands it reads, and the part to resume at. The stack starts in the walker's own activation and moves to the
 * heap when it outgrows that, so a shallow compare allocates nothing.
 */

#include "zergrt.h"

#include <string.h>

void zrt_eqw_push(zrt_eqw *w, zrt_eq_step step, const void *a, const void *b) {
	if (w->len == w->cap) {
		size_t   nc = w->cap * 2;
		zrt_eqf *nf = (zrt_eqf *)zrt_alloc(nc * sizeof(zrt_eqf));
		memcpy(nf, w->f, w->len * sizeof(zrt_eqf));
		if (w->f != w->inl) {
			zrt_free(w->f);
		}
		w->f = nf;
		w->cap = nc;
	}
	zrt_eqf *e = &w->f[w->len++];
	e->step = step;
	e->a = a;
	e->b = b;
	e->at = 0;
}

/* eqw_free is the walk's give-back: the heap frames, when it grew any. It is the cleanup an
 * abort inside a held walk runs, and the normal end's last line. */
static void eqw_free(void *p) {
	zrt_eqw *w = (zrt_eqw *)p;
	if (w->f != w->inl) {
		zrt_free(w->f);
		w->f = w->inl;
	}
}

bool zrt_eq_walk(zrt_eq_step step, const void *a, const void *b, bool held) {
	zrt_eqw w;
	w.f = w.inl;
	w.len = 0;
	w.cap = sizeof w.inl / sizeof w.inl[0];
	w.held = held;
	if (held) {
		zrt_defer(eqw_free, &w);
	}
	zrt_eqw_push(&w, step, a, b);
	bool r = true;
	while (w.len > 0) {
		size_t k = w.len - 1;
		size_t at = w.f[k].at;
		int    s = w.f[k].step(&w, w.f[k].a, w.f[k].b, &at);
		/* BY INDEX: the step's push may have moved the array */
		w.f[k].at = at;
		if (s == ZRT_EQ_NO) {
			r = false;
			break;
		}
		if (s == ZRT_EQ_YES) {
			w.len--;
		}
	}
	if (held) {
		zrt_forget(1);
	}
	eqw_free(&w);
	return r;
}

int zrt_eqstep_list(zrt_eqw *w, zrt_eq_step elem, const zrt_list *a, const zrt_list *b, size_t *at) {
	if (*at == 0 && a->len != b->len) {
		return ZRT_EQ_NO;
	}
	if (*at == a->len) {
		return ZRT_EQ_YES;
	}
	size_t i = (*at)++;
	zrt_eqw_push(w, elem, zrt_list_at_ref((zrt_list *)a, i), zrt_list_at_ref((zrt_list *)b, i));
	return ZRT_EQ_PUSHED;
}

/* The lookup is the map's own (vt->eq on the key), the comparison `m[k]` makes; a map only
 * READS here, so the shared buffer a copy left behind is never unshared. */
int zrt_eqstep_map(zrt_eqw *w, zrt_eq_step val, const zrt_map *a, const zrt_map *b, size_t *at) {
	if (*at == 0 && zrt_map_len(a) != zrt_map_len(b)) {
		return ZRT_EQ_NO;
	}
	if (*at == zrt_map_len(a)) {
		return ZRT_EQ_YES;
	}
	size_t i = (*at)++;
	void  *pb = zrt_map_get((zrt_map *)b, zrt_map_key_at((zrt_map *)a, i));
	if (pb == NULL) {
		return ZRT_EQ_NO;
	}
	zrt_eqw_push(w, val, zrt_map_val_at((zrt_map *)a, i), pb);
	return ZRT_EQ_PUSHED;
}

void zrt_hw_push(zrt_hw *w, zrt_hash_step step, const void *x) {
	if (w->len == w->cap) {
		size_t  nc = w->cap * 2;
		zrt_hf *nf = (zrt_hf *)zrt_alloc(nc * sizeof(zrt_hf));
		memcpy(nf, w->f, w->len * sizeof(zrt_hf));
		if (w->f != w->inl) {
			zrt_free(w->f);
		}
		w->f = nf;
		w->cap = nc;
	}
	zrt_hf *e = &w->f[w->len++];
	e->step = step;
	e->x = x;
	e->acc = 0;
	e->at = 0;
}

static void hw_free(void *p) {
	zrt_hw *w = (zrt_hw *)p;
	if (w->f != w->inl) {
		zrt_free(w->f);
		w->f = w->inl;
	}
}

/* Post-order: a frame is DONE only once every part it pushed has been folded into its acc. */
uint64_t zrt_hash_walk(zrt_hash_step step, const void *x, bool held) {
	zrt_hw w;
	w.f = w.inl;
	w.len = 0;
	w.cap = sizeof w.inl / sizeof w.inl[0];
	w.held = held;
	if (held) {
		zrt_defer(hw_free, &w);
	}
	zrt_hw_push(&w, step, x);
	uint64_t r = 0;
	for (;;) {
		size_t   k = w.len - 1;
		size_t   at = w.f[k].at;
		uint64_t acc = w.f[k].acc;
		int      s = w.f[k].step(&w, w.f[k].x, &acc, &at);
		/* BY INDEX: the step's push may have moved the array */
		w.f[k].at = at;
		w.f[k].acc = acc;
		if (s == ZRT_H_PUSHED) {
			continue;
		}
		w.len--;
		if (w.len == 0) {
			r = acc;
			break;
		}
		w.f[k - 1].acc = (w.f[k - 1].acc ^ acc) * 1099511628211ull;
	}
	if (held) {
		zrt_forget(1);
	}
	hw_free(&w);
	return r;
}
