package build

// runtimeC is the inline `zerg.h` runtime: the small set of helpers every
// generated v0.1 program needs, emitted once at the top of the produced .c
// file as a block of static functions. Keeping it inline (rather than a
// sibling header) avoids any include-path coordination with the C compiler
// invocation and means a single .c file is the entire artifact we hand to cc.
//
// Why a Go string constant rather than a //go:embed file: the runtime is
// short, has no syntax of its own, and lives next to the codegen that depends
// on it. Editing the two together is the common path; a separate .c file would
// drift out of sync without a build-time check.
//
// v0.1 known limitation: zerg_str_concat allocates and never frees. Programs
// in the v0.1 corpus are short-running, so the OS reclaims at exit. v0.2+
// adds an arena once we have a measurable workload.
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

/* zerg_str_concat allocates a fresh buffer holding a||b. v0.1 leaks; see the
   note at the top of runtime.go. */
static zerg_str zerg_str_concat(zerg_str a, zerg_str b) {
    size_t n = a.len + b.len;
    char *p = (char *)malloc(n == 0 ? 1 : n);
    if (a.len) memcpy(p, a.data, a.len);
    if (b.len) memcpy(p + a.len, b.data, b.len);
    return (zerg_str){p, n};
}

/* The four print helpers mirror PLAN.md's print format table: %lld for int,
   %g for float (matches Go's strconv.FormatFloat(x, 'g', -1, 64) for finite
   inputs), "true"/"false" for bool, raw bytes plus '\n' for str. */
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
`
