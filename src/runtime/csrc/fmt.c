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
 * width / precision; strings read width / align / precision (truncation).
 *
 * A SPEC IS TEXT THE PROGRAM WROTE, and this file used to treat it as trusted — the
 * float path built the pattern it handed snprintf out of the spec's own bytes. So every
 * field is BOUNDED here and every type letter is one of a fixed set, per rendering;
 * docs/runtime/format.md is where the set and the reasons live. What is outside either
 * ends the run by name (ValueError), the way an index or a key does — "ignored rather
 * than rejected" was written when this file had no error channel, and zrt_abort_kind is
 * one. The compiler is expected to refuse a bad spec before a program ever runs; this is
 * the floor under that, not a substitute.
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
 * the runtime raises itself passes a plain string literal ("index out of range"
 * in list.c, and its like in str.c, map.c, sys.c and conv.c). Retaining one of those reads
 * eight bytes of .rodata as a refcount and writes them back, which is a bus error on a good
 * day and silent corruption on a bad one.
 *
 * Copying costs an allocation per read and needs no invariant about where the message came
 * from, which is the property worth paying for: the caller owns what it is handed, whoever
 * raised it. */
const char *zrt_str_dup(const char *s) {
	if (s == NULL) {
		s = "";
	}
	return dup_n(s, strlen(s));
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

/* The bounds. A width is a field a caller is asking to be allocated, and a precision is
 * digits a caller is asking to be produced, so both are a size somebody else chose. The
 * numbers are not sacred — they are large enough that no rendering a person writes meets
 * them, and small enough that meeting one is a mistake rather than a memory request.
 * ZRT_FMT_BODY is sized for the widest thing the float path can produce inside them. */
#define ZRT_FMT_MAX_WIDTH 4096
#define ZRT_FMT_MAX_PREC  100
/* a DBL_MAX in `%f` is 309 integer digits, plus a sign, a point and the NUL */
#define ZRT_FMT_BODY (312 + ZRT_FMT_MAX_PREC)

/* spec_reject ends the run naming the spec that could not be rendered. A spec is written
 * in the source, so this is deterministic for a given program: the same run always ends
 * the same way, which is what makes it a diagnosis rather than a crash. */
_Noreturn static void spec_reject(const char *why) {
	zrt_abort_kind(ZRT_ERR_VALUE, why);
}

/* spec_digits reads a run of digits into a bounded value. It stops accumulating at the
 * limit rather than wrapping, so the refusal below is reached with a number to name and
 * the accumulation itself is never undefined. */
static long spec_digits(const char *s, size_t *i, size_t n, long limit, const char *why) {
	long v = 0;
	while (*i < n && s[*i] >= '0' && s[*i] <= '9') {
		if (v <= limit) {
			v = v * 10 + (s[(*i)] - '0');
		}
		(*i)++;
	}
	if (v > limit) {
		spec_reject(why);
	}
	return v;
}


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
	f->width = spec_digits(s, &i, n, ZRT_FMT_MAX_WIDTH,
	                       "a format spec's width is past what this runtime pads to");
	if (i < n && s[i] == '.') {
		i++;
		f->prec = spec_digits(s, &i, n, ZRT_FMT_MAX_WIDTH,
		                      "a format spec's precision is past what this runtime produces");
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
	/* The integer conversions, named rather than defaulted. This path builds its digits by
	 * hand, so an unknown letter was never a memory hazard the way the float path's was —
	 * it was a silent one, rendering `{n:q}` as though the `q` had not been written. */
	if (f.type != 0 && strchr("boxXcd", f.type) == NULL) {
		spec_reject("an int renders as `b`, `o`, `x`, `X`, `c` or `d`, and a format spec asked for another");
	}

	/* `c` RENDERS A CODE POINT, and it used to render the low byte of one: `{300:c}` came
	 * back as a comma, `{0:c}` as an empty string, and `{-1:c}` as a byte that is not UTF-8
	 * at all. A conversion that silently answers about a different value is the shape this
	 * project's standing rule forbids, and it is the same encoder `str(runes)` uses. */
	if (f.type == 'c') {
		char cb[5];
		int  len = zrt_utf8_encode(v, cb);
		if (len == 0) {
			spec_reject("`c` renders a code point, and this is not one a str can hold");
		}
		cb[len] = '\0';
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
	/* the same closed set the signed path names — this branch renders through the display
	 * fallback rather than the base machinery, so an unknown letter was dropped in silence
	 * here while its twin three lines up refused it. */
	if (f.type != 0 && strchr("boxXcd", f.type) == NULL) {
		spec_reject("an int renders as `b`, `o`, `x`, `X`, `c` or `d`, and a format spec asked for another");
	}
	const char *body = zrt_display_uint(v); /* a fresh cell; pad_field copies, so free it here */
	const char *out = pad_field(body, '>', &f);
	zrt_str_release(body);
	return out;
}

const char *zrt_fmt_float(double v, const char *spec) {
	fmt_spec f;
	parse_spec(spec, &f);
	char type = f.type ? f.type : 'g';
	/* THE TYPE LETTER IS A CONVERSION, so it is switched on rather than SPLICED INTO a
	 * pattern. Both halves of that sentence matter. Bounding the letter to this set is what
	 * stops `%s` reading the double as a pointer and `%n` writing through it; writing one
	 * literal per case is what leaves no format string in this file built out of anything a
	 * program supplied — the precision rides in as an argument, where a number belongs.
	 *
	 * The sign is not in the pattern either. `%+` and `% ` differ from the default only in
	 * what they put before a non-negative number, and putting it there by hand keeps the
	 * count of literals at one per conversion instead of one per conversion per sign. */
	/* THE FLOAT'S OWN BOUND, tighter than the shared one and for a different reason: this is
	 * fractional DIGITS, and the buffer below is sized for them. A `str`'s precision is a
	 * truncation and an int ignores it, so neither wants this number — checking it in
	 * parse_spec made `f"{s:.200}"`, a legal 200-character truncation, an error. */
	if (f.prec > ZRT_FMT_MAX_PREC) {
		spec_reject("a float renders at most a hundred fractional digits, and a format spec asked for more");
	}
	long prec = f.prec >= 0 ? f.prec : 6;
	char body[ZRT_FMT_BODY];
	char digits[ZRT_FMT_BODY];
	switch (type) {
	case 'e':
		snprintf(digits, sizeof(digits), "%.*e", (int)prec, v);
		break;
	case 'E':
		snprintf(digits, sizeof(digits), "%.*E", (int)prec, v);
		break;
	case 'f':
		snprintf(digits, sizeof(digits), "%.*f", (int)prec, v);
		break;
	case 'F':
		snprintf(digits, sizeof(digits), "%.*F", (int)prec, v);
		break;
	case 'g':
		snprintf(digits, sizeof(digits), "%.*g", (int)prec, v);
		break;
	case 'G':
		snprintf(digits, sizeof(digits), "%.*G", (int)prec, v);
		break;
	default:
		spec_reject("a float renders as `e`, `f` or `g` (in either case), and a format spec asked for another");
	}
	if ((f.sign == '+' || f.sign == ' ') && digits[0] != '-') {
		snprintf(body, sizeof(body), "%c%s", f.sign, digits);
	} else {
		snprintf(body, sizeof(body), "%s", digits);
	}
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
	if (f.type != 0 && f.type != 's') {
		spec_reject("a str renders as `s`, and a format spec asked for another");
	}
	size_t len = strlen(s);
	if (f.prec >= 0 && (size_t)f.prec < len) {
		len = (size_t)f.prec; /* precision truncates a string */
	}
	char       *body = dup_n(s, len);
	const char *out = pad_field(body, '<', &f); /* pad_field always returns a fresh cell */
	zrt_str_release(body);                       /* so the dup_n intermediate is ours to free */
	return out;
}
