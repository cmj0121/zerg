/* str.c — the str <-> list[byte] / list[rune] bridge (docs/collections.md).
 *
 * A `str` is a distinct immutable primitive that is NOT indexable and holds no embedded
 * NUL; to scan or edit text you bridge through a `list[byte]` (raw octets) or a
 * `list[rune]` (code points). Going TO a str validates the str invariant — valid UTF-8,
 * no NUL — and raises on violation, so `str(bytes)` is a checked conversion a `guard`
 * can demote to a Result. Going FROM a str never fails: the invariant guarantees the
 * bytes decode.
 *
 * A str is a NUL-terminated UTF-8 `const char *`; a byte element is a `uint8_t`, a rune a
 * signed 32-bit code point.
 */

#include "zergrt.h"

#include <string.h>

/* str_alloc returns the payload of a fresh rc=1 string cell holding n bytes: a managed
 * str value (S2) IS this payload pointer. `str(bytes)` / `str(runes)` are heap producers,
 * so the str they build is a cell the compiler refcounts and frees, in place of the old
 * never-freed zrt_alloc. */
static char *str_alloc(size_t n) { return (char *)zrt_ref_payload(zrt_ref_alloc(n, NULL)); }

/* --- UTF-8 decode (str -> list) -------------------------------------------- */

/* seq_len returns the byte length of the UTF-8 sequence a lead byte opens (1..4). A str
 * is valid UTF-8 by invariant, so a lead byte is always well-formed here. */
static int seq_len(unsigned char c) {
	if (c < 0x80) {
		return 1;
	}
	if ((c >> 5) == 0x6) {
		return 2;
	}
	if ((c >> 4) == 0xE) {
		return 3;
	}
	return 4;
}

/* zrt_str_bytes returns the raw UTF-8 bytes of s as a list[byte]. The str holds no NUL,
 * so its length is strlen. */
zrt_list zrt_str_bytes(const char *s) {
	zrt_list l;
	zrt_list_init(&l, sizeof(uint8_t), NULL);
	if (s == NULL) {
		return l;
	}
	for (const unsigned char *p = (const unsigned char *)s; *p != 0; p++) {
		uint8_t b = *p;
		zrt_list_push(&l, &b);
	}
	return l;
}

/* zrt_str_runes returns the Unicode code points of s as a list[rune]. Valid UTF-8 by the
 * str invariant, so every sequence decodes. */
zrt_list zrt_str_runes(const char *s) {
	zrt_list l;
	zrt_list_init(&l, sizeof(int32_t), NULL);
	if (s == NULL) {
		return l;
	}
	const unsigned char *p = (const unsigned char *)s;
	while (*p != 0) {
		int n = seq_len(*p);
		int32_t cp;
		if (n == 1) {
			cp = *p;
		} else {
			cp = *p & (0x7F >> n);
			for (int k = 1; k < n; k++) {
				cp = (cp << 6) | (p[k] & 0x3F);
			}
		}
		zrt_list_push(&l, &cp);
		p += n;
	}
	return l;
}

/* --- UTF-8 validate / encode (list -> str) --------------------------------- */

/* utf8_ok reports whether n bytes are valid UTF-8 with no embedded NUL — the str
 * invariant. It rejects overlong encodings, surrogates, and code points past U+10FFFF,
 * so a str built from these bytes is well-formed. */
static int utf8_ok(const uint8_t *b, size_t n) {
	size_t i = 0;
	while (i < n) {
		uint8_t c = b[i];
		if (c == 0) {
			return 0; /* a str never holds an embedded NUL */
		}
		int len;
		uint32_t cp;
		uint32_t min;
		if (c < 0x80) {
			i++;
			continue;
		} else if ((c >> 5) == 0x6) {
			len = 2;
			cp = c & 0x1F;
			min = 0x80;
		} else if ((c >> 4) == 0xE) {
			len = 3;
			cp = c & 0x0F;
			min = 0x800;
		} else if ((c >> 3) == 0x1E) {
			len = 4;
			cp = c & 0x07;
			min = 0x10000;
		} else {
			return 0; /* not a valid lead byte */
		}
		if (i + (size_t)len > n) {
			return 0; /* truncated sequence */
		}
		for (int k = 1; k < len; k++) {
			uint8_t cc = b[i + (size_t)k];
			if ((cc >> 6) != 0x2) {
				return 0; /* bad continuation byte */
			}
			cp = (cp << 6) | (cc & 0x3F);
		}
		if (cp < min || cp > 0x10FFFF || (cp >= 0xD800 && cp <= 0xDFFF)) {
			return 0; /* overlong, out of range, or a surrogate */
		}
		i += (size_t)len;
	}
	return 1;
}

/* zrt_str_from_bytes builds a str from a list[byte], validating the str invariant and
 * raising EncodingError on violation. The bytes are copied into a fresh NUL-terminated
 * buffer the caller now owns; the source list is untouched. */
const char *zrt_str_from_bytes(zrt_list bytes) {
	const uint8_t *data = bytes.data;
	size_t n = bytes.len;
	if (!utf8_ok(data, n)) {
		zrt_abort_kind(ZRT_ERR_ENCODING, "EncodingError: bytes are not valid UTF-8 for a str");
	}
	char *out = str_alloc(n + 1);
	if (n > 0) {
		memcpy(out, data, n);
	}
	out[n] = 0;
	return out;
}

/* enc_len returns the UTF-8 byte length of a code point (1..4), or 0 when the code point
 * cannot be a str: a surrogate, past U+10FFFF, negative, or U+0000 (its byte is a NUL). */
static int enc_len(int32_t cp) {
	if (cp <= 0 || cp > 0x10FFFF || (cp >= 0xD800 && cp <= 0xDFFF)) {
		return 0;
	}
	if (cp < 0x80) {
		return 1;
	}
	if (cp < 0x800) {
		return 2;
	}
	if (cp < 0x10000) {
		return 3;
	}
	return 4;
}

/* --- number parsing (str -> int) ------------------------------------------- */

/* zrt_parse_int parses a decimal integer from s — an optional '+'/'-' then one or more
 * digits, nothing else — and raises ValueError on an empty string, a stray character, or
 * a value outside the int64 range. It is `int(s)` for a str argument (docs/types.md:
 * conversion is re-construction, checked). */
int64_t zrt_parse_int(const char *s) {
	if (s == NULL || *s == 0) {
		zrt_abort_kind(ZRT_ERR_VALUE, "ValueError: cannot parse an integer from an empty string");
	}
	const char *p = s;
	int neg = 0;
	if (*p == '+' || *p == '-') {
		neg = (*p == '-');
		p++;
	}
	if (*p == 0) {
		zrt_abort_kind(ZRT_ERR_VALUE, "ValueError: cannot parse an integer from a lone sign");
	}
	/* Accumulate magnitude as unsigned; the signed range is asymmetric, so the limit is
	 * 2^63 for a negative value and 2^63-1 for a non-negative one. */
	uint64_t limit = neg ? 9223372036854775808ULL : 9223372036854775807ULL;
	uint64_t acc = 0;
	for (; *p != 0; p++) {
		if (*p < '0' || *p > '9') {
			zrt_abort_kind(ZRT_ERR_VALUE, "ValueError: invalid digit while parsing an integer");
		}
		uint64_t d = (uint64_t)(*p - '0');
		if (acc > (limit - d) / 10) {
			zrt_abort_kind(ZRT_ERR_OVERFLOW, "OverflowError: integer literal out of range");
		}
		acc = acc * 10 + d;
	}
	if (neg) {
		return (acc == 9223372036854775808ULL) ? (-9223372036854775807LL - 1) : -(int64_t)acc;
	}
	return (int64_t)acc;
}

/* zrt_str_from_runes builds a str from a list[rune], encoding each code point to UTF-8
 * and raising EncodingError on an invalid one (a surrogate, out of range, or U+0000,
 * whose byte would be a NUL). */
const char *zrt_str_from_runes(zrt_list runes) {
	const int32_t *cps = (const int32_t *)runes.data;
	size_t n = runes.len;
	size_t total = 0;
	for (size_t i = 0; i < n; i++) {
		int len = enc_len(cps[i]);
		if (len == 0) {
			zrt_abort_kind(ZRT_ERR_ENCODING, "EncodingError: rune is not a valid code point for a str");
		}
		total += (size_t)len;
	}
	char *out = str_alloc(total + 1);
	size_t o = 0;
	for (size_t i = 0; i < n; i++) {
		uint32_t cp = (uint32_t)cps[i];
		if (cp < 0x80) {
			out[o++] = (char)cp;
		} else if (cp < 0x800) {
			out[o++] = (char)(0xC0 | (cp >> 6));
			out[o++] = (char)(0x80 | (cp & 0x3F));
		} else if (cp < 0x10000) {
			out[o++] = (char)(0xE0 | (cp >> 12));
			out[o++] = (char)(0x80 | ((cp >> 6) & 0x3F));
			out[o++] = (char)(0x80 | (cp & 0x3F));
		} else {
			out[o++] = (char)(0xF0 | (cp >> 18));
			out[o++] = (char)(0x80 | ((cp >> 12) & 0x3F));
			out[o++] = (char)(0x80 | ((cp >> 6) & 0x3F));
			out[o++] = (char)(0x80 | (cp & 0x3F));
		}
	}
	out[o] = 0;
	return out;
}
