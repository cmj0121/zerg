/*
 * map.c - the Zerg runtime built-in `map[K, V]` container (docs/code/collections.md).
 *
 * A map is a BY-VALUE header (`zrt_map`); only its storage is heap. The header is
 * embedded inline in whatever holds it (a local, a field, a list element, another
 * map's value) exactly like a list, so copying/dropping a map is the compiler's job
 * at every scope boundary — this file owns only the storage.
 *
 * The table is INSERTION-ORDERED open-addressing (docs/code/collections.md: map iterates
 * in insertion order):
 *   - `entries` is an insertion-order array of `entrysz`-byte records, each laid out
 *     `[ size_t hash | key (keysz) | val (valsz) ]`. Iteration walks it 0..len.
 *   - `buckets` is a linear-probe hash index of `nbuckets` slots, each holding a
 *     1-based entry index (0 = empty). A lookup hashes the key, probes buckets, and
 *     compares against the entry's stored hash then vt->eq. There is NO tombstone —
 *     removal is deferred (see docs/code/collections.md Deferred), so probing never has to
 *     skip a deleted slot.
 * Load factor 0.75 triggers a rehash that rebuilds `buckets` from `entries` in their
 * existing insertion order, so growth never reorders iteration.
 *
 * Key/val teardown is indirected through a per-instance vtable (`zrt_map_vt`): a POD
 * key or value carries NULL copy/drop (the memcpy fast path); a non-POD value (a Ref,
 * a nested list/map) carries copy/drop thunks the compiler emits. Built-in Hash is
 * provided for `int` and `str` keys only (this phase); a richer key needs user Hash.
 * Arch-neutral C — the size/hash/eq/copy/drop are runtime data, not monomorphized C.
 */
#include "zergrt.h"

#include <string.h>

/* entry field accessors: an entry is [ size_t hash | key | val ]. */
static size_t *zrt_map_ehash(zrt_map *m, size_t i) {
	return (size_t *)(m->entries + i * m->entrysz);
}

static void *zrt_map_ekey(zrt_map *m, size_t i) {
	return m->entries + i * m->entrysz + sizeof(size_t);
}

static void *zrt_map_eval(zrt_map *m, size_t i) {
	return m->entries + i * m->entrysz + sizeof(size_t) + m->keysz;
}

void zrt_map_init(zrt_map *m, size_t keysz, size_t valsz, const zrt_map_vt *vt) {
	m->entries = NULL;
	m->len = 0;
	m->cap = 0;
	m->buckets = NULL;
	m->nbuckets = 0;
	m->keysz = keysz;
	m->valsz = valsz;
	m->entrysz = sizeof(size_t) + keysz + valsz;
	m->vt = vt;
}

/* zrt_map_find probes the bucket index for `key` (hashed to h). It returns the entry
 * index (0-based) on a hit and sets *bslot to the bucket slot holding it; on a miss it
 * returns -1 and sets *bslot to the empty slot where an insert would land. Assumes
 * nbuckets != 0 (the callers ensure a table exists before probing). */
static int64_t zrt_map_find(zrt_map *m, const void *key, size_t h, size_t *bslot) {
	size_t idx = h % m->nbuckets;
	for (;;) {
		int64_t e = m->buckets[idx];
		if (e == 0) {
			*bslot = idx;
			return -1;
		}
		size_t ei = (size_t)(e - 1);
		if (*zrt_map_ehash(m, ei) == h && m->vt->eq(zrt_map_ekey(m, ei), key)) {
			*bslot = idx;
			return (int64_t)ei;
		}
		idx = (idx + 1) % m->nbuckets;
	}
}

/* zrt_map_rehash reallocates the bucket index to `newn` slots and rebuilds it from the
 * entries array IN INSERTION ORDER, so the iteration order (the entries array) is never
 * disturbed by a rehash. */
static void zrt_map_rehash(zrt_map *m, size_t newn) {
	zrt_free(m->buckets);
	m->buckets = (int64_t *)zrt_alloc(newn * sizeof(int64_t));
	memset(m->buckets, 0, newn * sizeof(int64_t));
	m->nbuckets = newn;
	for (size_t i = 0; i < m->len; i++) {
		size_t idx = *zrt_map_ehash(m, i) % newn;
		while (m->buckets[idx] != 0) {
			idx = (idx + 1) % newn;
		}
		m->buckets[idx] = (int64_t)(i + 1);
	}
}

/* zrt_map_grow_entries doubles the insertion-order array from a floor of 8. The move is
 * a raw bit-copy of the live prefix — never vt->copy — because the records are only
 * relocated, not logically duplicated. */
static void zrt_map_grow_entries(zrt_map *m) {
	size_t cap = m->cap == 0 ? 8 : m->cap * 2;
	uint8_t *e = (uint8_t *)zrt_alloc(cap * m->entrysz);
	if (m->len != 0) {
		memcpy(e, m->entries, m->len * m->entrysz);
	}
	zrt_free(m->entries);
	m->entries = e;
	m->cap = cap;
}

void *zrt_map_get(zrt_map *m, const void *key) {
	if (m->nbuckets == 0) {
		return NULL;
	}
	size_t bslot;
	int64_t ei = zrt_map_find(m, key, m->vt->hash(key), &bslot);
	if (ei < 0) {
		return NULL;
	}
	return zrt_map_eval(m, (size_t)ei);
}

void *zrt_map_index(zrt_map *m, const void *key) {
	void *v = zrt_map_get(m, key);
	if (v == NULL) {
		zrt_abort_kind(ZRT_ERR_KEY, "key not found");
	}
	return v;
}

bool zrt_map_has(zrt_map *m, const void *key) {
	return zrt_map_get(m, key) != NULL;
}

void zrt_map_set(zrt_map *m, const void *key, const void *val) {
	size_t h = m->vt->hash(key);
	if (m->nbuckets == 0) {
		zrt_map_rehash(m, 8);
	}
	size_t bslot;
	int64_t ei = zrt_map_find(m, key, h, &bslot);
	if (ei >= 0) {
		/* hit: drop the old value, store the new one, and drop the surplus incoming key
		 * (both key and val were copied in by the emitter; the existing key stays). */
		void *v = zrt_map_eval(m, (size_t)ei);
		if (m->vt->val_drop != NULL) {
			m->vt->val_drop(v);
		}
		memcpy(v, val, m->valsz);
		if (m->vt->key_drop != NULL) {
			m->vt->key_drop((void *)key);
		}
		return;
	}
	/* miss: append a new entry. Rehash the bucket index first if this insert would push
	 * past the 0.75 load factor, then re-probe for the (now different) empty slot. */
	if ((m->len + 1) * 4 > m->nbuckets * 3) {
		zrt_map_rehash(m, m->nbuckets * 2);
		zrt_map_find(m, key, h, &bslot);
	}
	if (m->len == m->cap) {
		zrt_map_grow_entries(m);
	}
	size_t i = m->len;
	*zrt_map_ehash(m, i) = h;
	memcpy(zrt_map_ekey(m, i), key, m->keysz);
	memcpy(zrt_map_eval(m, i), val, m->valsz);
	m->buckets[bslot] = (int64_t)(i + 1);
	m->len++;
}

size_t zrt_map_len(const zrt_map *m) {
	return m->len;
}

void *zrt_map_key_at(zrt_map *m, size_t i) {
	return zrt_map_ekey(m, i);
}

void *zrt_map_val_at(zrt_map *m, size_t i) {
	return zrt_map_eval(m, i);
}

void zrt_map_copy(zrt_map *dst, const zrt_map *src) {
	dst->len = src->len;
	dst->cap = src->len;
	dst->keysz = src->keysz;
	dst->valsz = src->valsz;
	dst->entrysz = src->entrysz;
	dst->vt = src->vt;
	dst->nbuckets = src->nbuckets;
	if (src->len == 0) {
		dst->entries = NULL;
		dst->cap = 0;
	} else {
		dst->entries = (uint8_t *)zrt_alloc(src->len * src->entrysz);
		memcpy(dst->entries, src->entries, src->len * src->entrysz);
		/* deep-copy any non-POD key/val over the shallow bit-copy (a POD side is NULL and
		 * keeps the memcpy'd bits). A src value's shallow header (e.g. a nested list that
		 * aliases src's buffer) is overwritten by the fresh deep copy — never dropped, it
		 * was never owned. */
		if (dst->vt->key_copy != NULL || dst->vt->val_copy != NULL) {
			for (size_t i = 0; i < src->len; i++) {
				if (dst->vt->key_copy != NULL) {
					dst->vt->key_copy(zrt_map_ekey(dst, i), zrt_map_ekey((zrt_map *)src, i));
				}
				if (dst->vt->val_copy != NULL) {
					dst->vt->val_copy(zrt_map_eval(dst, i), zrt_map_eval((zrt_map *)src, i));
				}
			}
		}
	}
	if (src->nbuckets == 0) {
		dst->buckets = NULL;
	} else {
		dst->buckets = (int64_t *)zrt_alloc(src->nbuckets * sizeof(int64_t));
		memcpy(dst->buckets, src->buckets, src->nbuckets * sizeof(int64_t));
	}
}

void zrt_map_drop(zrt_map *m) {
	if (m->vt->key_drop != NULL || m->vt->val_drop != NULL) {
		for (size_t i = 0; i < m->len; i++) {
			if (m->vt->key_drop != NULL) {
				m->vt->key_drop(zrt_map_ekey(m, i));
			}
			if (m->vt->val_drop != NULL) {
				m->vt->val_drop(zrt_map_eval(m, i));
			}
		}
	}
	zrt_free(m->entries);
	zrt_free(m->buckets);
	m->entries = NULL;
	m->buckets = NULL;
	m->len = 0;
	m->cap = 0;
	m->nbuckets = 0;
}

/* --- built-in Hash for int and str keys (docs/code/collections.md) ----------------- */

size_t zrt_hash_int(const void *key) {
	/* splitmix64 finalizer: a strong bit-mix so a linear-probe table stays well spread. */
	uint64_t x = (uint64_t)(*(const int64_t *)key);
	x += 0x9E3779B97F4A7C15ull;
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9ull;
	x = (x ^ (x >> 27)) * 0x94D049BB133111EBull;
	x = x ^ (x >> 31);
	return (size_t)x;
}

bool zrt_eq_int(const void *a, const void *b) {
	return *(const int64_t *)a == *(const int64_t *)b;
}

size_t zrt_hash_str(const void *key) {
	/* FNV-1a over the NUL-terminated bytes; the key slot holds a `const char *`. */
	const char *s = *(const char *const *)key;
	uint64_t h = 1469598103934665603ull;
	if (s != NULL) {
		for (; *s != '\0'; s++) {
			h ^= (uint8_t)*s;
			h *= 1099511628211ull;
		}
	}
	return (size_t)h;
}

bool zrt_eq_str(const void *a, const void *b) {
	const char *x = *(const char *const *)a;
	const char *y = *(const char *const *)b;
	if (x == y) {
		return true;
	}
	if (x == NULL || y == NULL) {
		return false;
	}
	return strcmp(x, y) == 0;
}
