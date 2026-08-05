/*
 * fmt.c - the Zerg runtime's text-rendering surface (Phase 1f, U3 Format).
 *
 * The built-in `display()` and per-type `Format` (`:spec`) implementations the
 * compiler lowers an f-string onto, plus the string concatenation that joins a
 * lowered f-string's parts. Every returned string is heap-allocated through
 * zrt_alloc and NUL-terminated (leaked for the MVP, like every other box); the
 * compiler only ever reads them, never frees them.
 *
 * The spec grammar mirrors Python's `[[fill]align][sign][#][0][width][.prec][type]`.
 * What each field means is the type's own: numbers read sign / base / zero-pad /
 * width / precision; strings read width / align / precision (truncation). A spec the
 * MVP does not model is ignored rather than rejected — the f-string desugars at
 * compile time, so there is no runtime error channel.
 */
#include "zergrt.h"

#include <stdbool.h>
#include <stdio.h>
#include <string.h>

/* str_alloc returns the payload of a fresh rc=1 string cell holding n bytes: a managed
 * str value (S2) IS this payload pointer, its `[zrt_ref_hdr | bytes]` header one step
 * behind it. Every heap string this file produces goes through here so the compiler can
 * refcount and free it, in place of the old never-freed zrt_alloc. */
static char *str_alloc(size_t n) { return (char *)zrt_ref_payload(zrt_ref_alloc(n, NULL)); }

/* zrt_str_retain / zrt_str_release: the const char*-typed refcount wrappers (S2). They
 * recover the cell header from the payload pointer and defer to zrt_retain/zrt_release,
 * which no-op on an immortal (literal) cell. */
const char *zrt_str_retain(const char *s) {
	if (s != NULL) {
		zrt_retain((zrt_ref_hdr *)s - 1);
	}
	return s;
}

void zrt_str_release(const char *s) {
	if (s != NULL) {
		zrt_release((zrt_ref_hdr *)s - 1);
	}
}

/* zrt_str_elem_vt is the list element vtable for a `list[str]` of managed cells: a copy
 * retains each element, a drop releases it. zrt_os_args uses it so the args list owns and
 * frees its cells; a str-managed program's own list[str] uses the compiler-emitted vtable,
 * which is identical (retain/release). */
static void str_elem_copy(void *dst, const void *src) {
	*(const char **)dst = zrt_str_retain(*(const char *const *)src);
}
static void str_elem_drop(void *elem) { zrt_str_release(*(const char **)elem); }
const zrt_elem_vt zrt_str_elem_vt = {str_elem_copy, str_elem_drop};

/* dup_n copies n bytes of s into a fresh NUL-terminated string cell. */
static char *dup_n(const char *s, size_t n) {
	char *p = str_alloc(n + 1);
	if (n > 0) {
		memcpy(p, s, n);
	}
	p[n] = '\0';
	return p;
}

const char *zrt_str_concat(const char *a, const char *b) {
	if (a == NULL) {
		a = "";
	}
	if (b == NULL) {
		b = "";
	}
	size_t la = strlen(a), lb = strlen(b);
	char  *p = str_alloc(la + lb + 1);
	memcpy(p, a, la);
	memcpy(p + la, b, lb);
	p[la + lb] = '\0';
	return p;
}

/* zrt_str_dup COPIES a C string into a managed cell, and is what an `Err`'s message is read
 * through. `zrt_str_retain` cannot serve there: it recovers a cell header from the byte
 * BEFORE the payload, and a zrt_err's msg is very often not a payload at all — every abort
 * the runtime raises itself passes a plain string literal ("IndexError: index out of range"
 * in list.c, and its like in str.c, map.c, sys.c and conv.c). Retaining one of those reads
 * eight bytes of .rodata as a refcount and writes them back, which is a bus error on a good
 * day and silent corruption on a bad one.
 *
 * Copying costs an allocation per read and needs no invariant about where the message came
 * from, which is the property worth paying for: the caller owns what it is handed, whoever
 * raised it. */
const char *zrt_str_dup(const char *s) {
	size_t n;
	char  *p;
	if (s == NULL) {
		s = "";
	}
	n = strlen(s);
	p = str_alloc(n + 1);
	memcpy(p, s, n);
	p[n] = '\0';
	return p;
}

/* --- display() -------------------------------------------------------------- */

const char *zrt_display_int(int64_t v) {
	char buf[32];
	int  n = snprintf(buf, sizeof(buf), "%lld", (long long)v);
	return dup_n(buf, n < 0 ? 0 : (size_t)n);
}

const char *zrt_display_uint(uint64_t v) {
	char buf[32];
	int  n = snprintf(buf, sizeof(buf), "%llu", (unsigned long long)v);
	return dup_n(buf, n < 0 ? 0 : (size_t)n);
}

const char *zrt_display_float(double v) {
	char buf[32];
	int  n = snprintf(buf, sizeof(buf), "%g", v);
	return dup_n(buf, n < 0 ? 0 : (size_t)n);
}

/* zrt_display_bool renders the constant text "true"/"false". Under S2 these are not C
 * literals but IMMORTAL string cells, so a managed program can treat every str value —
 * including this constant result — uniformly as a cell (retain/release are no-ops on it). */
const char *zrt_display_bool(bool v) {
	static struct {
		zrt_ref_hdr h;
		char        b[6];
	} t = {{ZRT_RC_IMMORTAL, NULL}, "true"}, f = {{ZRT_RC_IMMORTAL, NULL}, "false"};
	return (const char *)zrt_ref_payload(v ? (void *)&t : (void *)&f);
}

/* --- spec parsing ----------------------------------------------------------- */

typedef struct {
	char fill;  /* pad char, default ' ' (or '0' when zero is set)            */
	char align; /* '<' '>' '^' '=' or 0 when unset                           */
	char sign;  /* '+' '-' ' ' ; '-' (negatives only) is the default          */
	bool alt;   /* '#' — the 0b/0o/0x base prefix                            */
	bool zero;  /* '0' — zero-pad between sign/prefix and the digits          */
	long width;
	long prec; /* -1 when unset */
	char type; /* 0 when unset  */
} fmt_spec;

/* parse_spec reads a `[[fill]align][sign][#][0][width][.prec][type]` spec. An empty
 * or NULL spec yields the all-defaults form. Unknown trailing bytes are tolerated. */
static void parse_spec(const char *s, fmt_spec *f) {
	f->fill = ' ';
	f->align = 0;
	f->sign = '-';
	f->alt = false;
	f->zero = false;
	f->width = 0;
	f->prec = -1;
	f->type = 0;
	if (s == NULL) {
		return;
	}
	size_t i = 0, n = strlen(s);
	/* [[fill]align] — a fill char is present only when the 2nd byte is an align. */
	if (n >= 2 && (s[1] == '<' || s[1] == '>' || s[1] == '^' || s[1] == '=')) {
		f->fill = s[0];
		f->align = s[1];
		i = 2;
	} else if (n >= 1 && (s[0] == '<' || s[0] == '>' || s[0] == '^' || s[0] == '=')) {
		f->align = s[0];
		i = 1;
	}
	if (i < n && (s[i] == '+' || s[i] == '-' || s[i] == ' ')) {
		f->sign = s[i++];
	}
	if (i < n && s[i] == '#') {
		f->alt = true;
		i++;
	}
	if (i < n && s[i] == '0') {
		f->zero = true;
		if (f->align == 0) {
			f->align = '=';
			f->fill = '0';
		}
		i++;
	}
	while (i < n && s[i] >= '0' && s[i] <= '9') {
		f->width = f->width * 10 + (s[i++] - '0');
	}
	if (i < n && s[i] == '.') {
		i++;
		f->prec = 0;
		while (i < n && s[i] >= '0' && s[i] <= '9') {
			f->prec = f->prec * 10 + (s[i++] - '0');
		}
	}
	if (i < n) {
		f->type = s[i];
	}
}

/* pad_field pads body to the spec width with fill/align, defaulting the alignment to
 * align_default (numbers right-align, strings left-align). '=' falls back to a right
 * pad here; the numeric formatter handles true sign-aware '=' padding itself. */
static const char *pad_field(const char *body, char align_default, const fmt_spec *f) {
	size_t len = strlen(body);
	if (f->width <= 0 || (size_t)f->width <= len) {
		return dup_n(body, len);
	}
	size_t total = (size_t)f->width, padn = total - len, left = 0, right = 0;
	char   align = f->align ? f->align : align_default;
	char  *p = str_alloc(total + 1);
	if (align == '<') {
		right = padn;
	} else if (align == '^') {
		left = padn / 2;
		right = padn - left;
	} else {
		left = padn; /* '>' and '=' fall back to a right-justified field */
	}
	memset(p, f->fill, left);
	memcpy(p + left, body, len);
	memset(p + left + len, f->fill, right);
	p[total] = '\0';
	return p;
}

/* --- format() : numbers ----------------------------------------------------- */

const char *zrt_fmt_int(int64_t v, const char *spec) {
	fmt_spec f;
	parse_spec(spec, &f);

	if (f.type == 'c') {
		char cb[2] = {(char)v, '\0'};
		return pad_field(cb, '<', &f);
	}

	int         base = 10, upper = 0;
	const char *pfx = "";
	switch (f.type) {
	case 'b':
		base = 2;
		pfx = "0b";
		break;
	case 'o':
		base = 8;
		pfx = "0o";
		break;
	case 'x':
		base = 16;
		pfx = "0x";
		break;
	case 'X':
		base = 16;
		upper = 1;
		pfx = "0X";
		break;
	default:
		base = 10;
		break;
	}

	/* magnitude as unsigned (safe for INT64_MIN), then reversed digits. */
	char     sgn = 0;
	uint64_t mag;
	if (v < 0) {
		sgn = '-';
		mag = (uint64_t)(-(v + 1)) + 1;
	} else {
		mag = (uint64_t)v;
		if (f.sign == '+' || f.sign == ' ') {
			sgn = f.sign;
		}
	}
	const char *hex = upper ? "0123456789ABCDEF" : "0123456789abcdef";
	char        digits[72];
	int         di = 0;
	if (mag == 0) {
		digits[di++] = '0';
	}
	while (mag > 0) {
		digits[di++] = hex[mag % (uint64_t)base];
		mag /= (uint64_t)base;
	}

	/* the sign/prefix leader, then the magnitude digits in order. */
	char lead[8];
	int  li = 0;
	if (sgn) {
		lead[li++] = sgn;
	}
	if (f.alt && base != 10) {
		lead[li++] = pfx[0];
		lead[li++] = pfx[1];
	}
	lead[li] = '\0';

	char body[96];
	int  bi = 0;
	for (int k = 0; k < li; k++) {
		body[bi++] = lead[k];
	}
	for (int k = di - 1; k >= 0; k--) {
		body[bi++] = digits[k];
	}
	body[bi] = '\0';

	/* '=' (or an implicit zero-pad) puts the fill BETWEEN the leader and digits. */
	if ((f.align == '=' || (f.zero && f.align == 0)) && f.width > (long)bi) {
		size_t total = (size_t)f.width, padn = total - (size_t)bi;
		char  *p = str_alloc(total + 1);
		memcpy(p, lead, (size_t)li);
		memset(p + li, f.fill, padn);
		/* copy the reversed digits in order after the fill */
		int off = li + (int)padn;
		for (int k = di - 1; k >= 0; k--) {
			p[off++] = digits[k];
		}
		p[total] = '\0';
		return p;
	}
	return pad_field(body, '>', &f);
}

const char *zrt_fmt_uint(uint64_t v, const char *spec) {
	/* the common non-negative case reuses the signed path; a value above INT64_MAX
	 * is outside the MVP's demo surface and renders via the display fallback. */
	if (v <= (uint64_t)INT64_MAX) {
		return zrt_fmt_int((int64_t)v, spec);
	}
	fmt_spec f;
	parse_spec(spec, &f);
	const char *body = zrt_display_uint(v); /* a fresh cell; pad_field copies, so free it here */
	const char *out = pad_field(body, '>', &f);
	zrt_str_release(body);
	return out;
}

const char *zrt_fmt_float(double v, const char *spec) {
	fmt_spec f;
	parse_spec(spec, &f);
	char type = f.type ? f.type : 'g';
	long prec = f.prec >= 0 ? f.prec : 6;
	char pat[16];
	char body[64];
	if (f.sign == '+' || f.sign == ' ') {
		snprintf(pat, sizeof(pat), "%%%c.%ld%c", f.sign, prec, type);
	} else {
		snprintf(pat, sizeof(pat), "%%.%ld%c", prec, type);
	}
	snprintf(body, sizeof(body), pat, v);
	if ((f.align == '=' || (f.zero && f.align == 0)) && f.width > 0) {
		/* zero-pad after an optional leading sign. */
		size_t len = strlen(body);
		if ((size_t)f.width > len) {
			size_t total = (size_t)f.width, padn = total - len;
			char  *p = str_alloc(total + 1);
			size_t lead = (body[0] == '-' || body[0] == '+' || body[0] == ' ') ? 1 : 0;
			memcpy(p, body, lead);
			memset(p + lead, f.fill, padn);
			memcpy(p + lead + padn, body + lead, len - lead);
			p[total] = '\0';
			return p;
		}
	}
	return pad_field(body, '>', &f);
}

/* --- format() : strings ----------------------------------------------------- */

const char *zrt_fmt_str(const char *s, const char *spec) {
	if (s == NULL) {
		s = "";
	}
	fmt_spec f;
	parse_spec(spec, &f);
	size_t len = strlen(s);
	if (f.prec >= 0 && (size_t)f.prec < len) {
		len = (size_t)f.prec; /* precision truncates a string */
	}
	char       *body = dup_n(s, len);
	const char *out = pad_field(body, '<', &f); /* pad_field always returns a fresh cell */
	zrt_str_release(body);                       /* so the dup_n intermediate is ours to free */
	return out;
}
