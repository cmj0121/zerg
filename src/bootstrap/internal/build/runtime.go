package build

// runtimeC is the inline `zerg.h` runtime: the small set of helpers every
// generated v0.1 / v0.2 program needs, emitted once at the top of the
// produced .c file as a block of static functions. Keeping it inline (rather
// than a sibling header) avoids any include-path coordination with the C
// compiler invocation and means a single .c file is the entire artifact we
// hand to cc.
//
// Why a Go string constant rather than a //go:embed file: the runtime is
// short, has no syntax of its own, and lives next to the codegen that depends
// on it. Editing the two together is the common path; a separate .c file would
// drift out of sync without a build-time check.
//
// Known limitations: zerg_str_concat allocates and never frees; the per-shape
// list helpers also leak. v0.3+ adds an arena once we have a measurable
// workload.
const runtimeC = `#include <stdio.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <stdlib.h>
#include <math.h>

typedef struct { const char *data; size_t len; } zerg_str;

static inline zerg_str zerg_str_lit(const char *s, size_t n) { return (zerg_str){s, n}; }

static inline _Bool zerg_str_eq(zerg_str a, zerg_str b) {
    return a.len == b.len && (a.len == 0 || memcmp(a.data, b.data, a.len) == 0);
}

/* zerg_str_cmp returns <0, 0, >0 like memcmp/strcmp, with the length tiebreak
   that lets shorter prefixes order before longer strings sharing the prefix. */
static int zerg_str_cmp(zerg_str a, zerg_str b) {
    size_t n = a.len < b.len ? a.len : b.len;
    int c = n == 0 ? 0 : memcmp(a.data, b.data, n);
    if (c != 0) return c;
    if (a.len < b.len) return -1;
    if (a.len > b.len) return 1;
    return 0;
}

/* zerg_str_concat allocates a fresh buffer holding a||b. v0.1+ leaks; see the
   note at the top of runtime.go. */
static zerg_str zerg_str_concat(zerg_str a, zerg_str b) {
    size_t n = a.len + b.len;
    char *p = (char *)malloc(n == 0 ? 1 : n);
    if (a.len) memcpy(p, a.data, a.len);
    if (b.len) memcpy(p + a.len, b.data, b.len);
    return (zerg_str){p, n};
}

/* The five primitive print helpers mirror PLAN.md's print format table.
   v0.2 adds byte and rune; both print as decimal of the unsigned/codepoint
   value (PLAN line 155-156). */
static void zerg_print_int(int64_t x) { printf("%lld\n", (long long)x); }

static void zerg_print_float(double x) {
    char buf[32];
    snprintf(buf, sizeof buf, "%.17g", x);
    fputs(buf, stdout);
    putchar('\n');
}

static void zerg_print_bool(_Bool x) { puts(x ? "true" : "false"); }

static void zerg_print_str(zerg_str s) {
    if (s.len) fwrite(s.data, 1, s.len, stdout);
    putchar('\n');
}

static void zerg_print_byte(uint8_t x) { printf("%hhu\n", x); }

static void zerg_print_rune(int32_t x) { printf("%d\n", (int)x); }

/* zerg_str_write prints the raw bytes of s WITHOUT a trailing newline; used
   inside list/tuple/struct print helpers where surrounding punctuation is
   handled by the caller. */
static void zerg_str_write(zerg_str s) {
    if (s.len) fwrite(s.data, 1, s.len, stdout);
}

/* zerg_utf8_decode reads one codepoint starting at p, returns its width in
   bytes and writes the codepoint into *cp. Mirrors Go's []rune(s) decoding so
   string indexing is byte-for-byte compatible with the interpreter. The
   helper is conservative: a malformed lead byte returns width 1 + the byte
   itself as cp (treats it as a literal rune for resilience). */
static size_t zerg_utf8_decode(const unsigned char *p, size_t n, int32_t *cp) {
    if (n == 0) { *cp = 0; return 0; }
    unsigned char b0 = p[0];
    if (b0 < 0x80) { *cp = (int32_t)b0; return 1; }
    if ((b0 & 0xE0) == 0xC0 && n >= 2) {
        *cp = (int32_t)(((b0 & 0x1F) << 6) | (p[1] & 0x3F));
        return 2;
    }
    if ((b0 & 0xF0) == 0xE0 && n >= 3) {
        *cp = (int32_t)(((b0 & 0x0F) << 12) | ((p[1] & 0x3F) << 6) | (p[2] & 0x3F));
        return 3;
    }
    if ((b0 & 0xF8) == 0xF0 && n >= 4) {
        *cp = (int32_t)(((b0 & 0x07) << 18) | ((p[1] & 0x3F) << 12) | ((p[2] & 0x3F) << 6) | (p[3] & 0x3F));
        return 4;
    }
    *cp = (int32_t)b0;
    return 1;
}

/* zerg_str_runelen returns the codepoint count of s — same as Go's
   len([]rune(s)). Used to back the v0.2 len(str) builtin. */
static int64_t zerg_str_runelen(zerg_str s) {
    const unsigned char *p = (const unsigned char *)s.data;
    size_t i = 0;
    int64_t count = 0;
    while (i < s.len) {
        int32_t cp;
        size_t w = zerg_utf8_decode(p + i, s.len - i, &cp);
        if (w == 0) break;
        i += w;
        count++;
    }
    return count;
}

/* zerg_str_rune_at returns the i-th codepoint of s. Out-of-range exits with
   an error to stderr — same behaviour as the interpreter's runtime error. */
static int32_t zerg_str_rune_at(zerg_str s, int64_t i, const char *pos) {
    if (i < 0) {
        fprintf(stderr, "zerg: %s: string index %lld out of range\n", pos, (long long)i);
        exit(1);
    }
    const unsigned char *p = (const unsigned char *)s.data;
    size_t off = 0;
    int64_t k = 0;
    while (off < s.len) {
        int32_t cp;
        size_t w = zerg_utf8_decode(p + off, s.len - off, &cp);
        if (w == 0) break;
        if (k == i) return cp;
        off += w;
        k++;
    }
    fprintf(stderr, "zerg: %s: string index %lld out of range [0..%lld)\n",
            pos, (long long)i, (long long)k);
    exit(1);
}

/* zerg_index_check aborts with a clear stderr line when i is outside [0, n).
   Pos is the source position of the indexing site, supplied by the codegen. */
static void zerg_index_check(int64_t i, size_t n, const char *pos) {
    if (i < 0 || (size_t)i >= n) {
        fprintf(stderr, "zerg: %s: list index %lld out of range [0..%zu)\n",
                pos, (long long)i, n);
        exit(1);
    }
}

/* zerg_match_panic is the no-arm-matched runtime panic for match. Pos is
   "line:col" of the match expression. Mirrors run.go's
   "match: no arm matched at <pos>" diagnostic. */
__attribute__((noreturn))
static void zerg_match_panic(const char *pos) {
    fprintf(stderr, "match: no arm matched at %s\n", pos);
    exit(1);
}
`
