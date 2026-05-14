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
#include <stdarg.h>
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

/* Per-type to-str helpers. The format strings MUST stay in sync with the
   matching zerg_print_* helpers — interp output must agree with the
   printed form byte-for-byte, and the parity tests assert on this. */
static zerg_str zerg_str_from_fmt(const char *fmt, ...) {
    char buf[64];
    va_list ap;
    va_start(ap, fmt);
    int n = vsnprintf(buf, sizeof buf, fmt, ap);
    va_end(ap);
    if (n < 0) n = 0;
    char *p = (char *)malloc((size_t)n + 1);
    memcpy(p, buf, (size_t)n);
    p[n] = 0;
    return (zerg_str){p, (size_t)n};
}

static zerg_str zerg_int_to_str(int64_t x)   { return zerg_str_from_fmt("%lld", (long long)x); }
static zerg_str zerg_float_to_str(double x)  { return zerg_str_from_fmt("%.17g", x); }
static zerg_str zerg_byte_to_str(uint8_t x)  { return zerg_str_from_fmt("%hhu", x); }
static zerg_str zerg_rune_to_str(int32_t x)  { return zerg_str_from_fmt("%d", (int)x); }
static zerg_str zerg_bool_to_str(_Bool x)    { return x ? (zerg_str){"true", 4} : (zerg_str){"false", 5}; }

/* zerg_panic writes "zerg: runtime: <msg>\n" to stderr and exits with
   code 1. Backs the v0.14 panic(msg: str) builtin; pure-Zerg stdlib
   modules use it for non-recoverable contract violations (e.g.
   split's documented panic on empty separator). _Noreturn lets
   surrounding cgen-generated expressions reason about the
   divergent control flow. */
static _Noreturn void zerg_panic(zerg_str msg) {
    fputs("zerg: runtime: ", stderr);
    if (msg.len) fwrite(msg.data, 1, msg.len, stderr);
    fputc('\n', stderr);
    exit(1);
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

// runtimeV04C is the v0.4 runtime extension: helpers for the
// NotImplemented vtable stub and any other spec-mechanics support that
// generated code calls. The exact diagnostic format is byte-identical to
// run.go's `not implemented: <Type>.<method> (declared in spec <Spec> at
// <pos>)` — Unit 8's parity corpus asserts on the substring.
const runtimeV04C = `/* zerg_not_implemented is the runtime stub for a vtable slot whose
   spec method has no impl override and no spec default body. Bytes-for-byte
   matched to run.go's diagnostic so parity tests pass on substring. */
__attribute__((noreturn))
static void zerg_not_implemented(const char *type_name, const char *method_name,
                                  const char *spec_name, const char *pos) {
    fprintf(stderr, "not implemented: %s.%s (declared in spec %s at %s)\n",
            type_name, method_name, spec_name, pos);
    exit(1);
}
`

// buildV12RuntimePreamble constructs the v0.12 concurrency-runtime preamble
// emitted whenever a program uses any v0.7 concurrency primitive (chan /
// spawn / defer / wait_group / select / anon-fn). It assembles:
//
//   - coroRuntimeC: ucontext-based coroutine primitive (U1)
//   - schedRuntimeC: M:N scheduler with park/unpark + shared wait-queue
//     primitive (U2 + U3 generic node moved here)
//   - waitgroupRuntimeC: wait_group on park/unpark (U4)
//   - selectRuntimeC: cooperative-yield select (U4)
//   - deferRuntimeC: per-coroutine defer push (U5)
//   - v07ShimsC: surface compatibility shims so cgen's existing emit
//     (zerg_spawn / zerg_main_wg_* / zerg_wait_group_t / zerg_defer_*)
//     keeps working bytewise while the runtime body is now M:N
//
// Replaces the old condvar-and-pthread-per-spawn runtime: spawn is now an
// M:N coroutine; select yields instead of usleep; defer is per-coroutine
// (the __thread defer stack from v0.7 is gone). See PLAN.md for the
// design pins and runtime_*.go for each unit's source + tests.
func buildV12RuntimePreamble() string {
	return coroRuntimeC + "\n" +
		schedRuntimeC + "\n" +
		waitgroupRuntimeC + "\n" +
		selectRuntimeC + "\n" +
		deferRuntimeC + "\n" +
		v07ShimsC
}

// v07ShimsC keeps the v0.7 emit surface (zerg_spawn, zerg_main_wg_*,
// zerg_wait_group_t, zerg_defer_push / drain, zerg_fn_value) while
// routing through the v0.12 internals. The wait_group struct keeps the
// v0.7 typedef name but the layout is the v0.12 zerg_waitgroup_t shape;
// the helpers delegate. zerg_main_wg_* is a synthetic global drain
// counter the cgen emits at the spawn / main boundary — the v0.12
// scheduler tracks live coroutines independently, so the shim helpers
// are free to be no-ops with zerg_main_wg_wait deferring to
// zerg_sched_drain. zerg_defer_push pushes onto the current
// coroutine's defer stack via zerg_coro_defer; the v0.7 zerg_defer_drain
// marker pattern is handled inside zerg_coro_defer's per-coroutine LIFO.
const v07ShimsC = `
/* ---------------- fn-value (v0.7 surface kept) --------------------------- */
typedef struct {
    void *fn;
    void *env;
} zerg_fn_value;

/* ---------------- defer shim (v0.7 surface) ----------------------------
   v0.7 emitted per-fn marker push/pop bracketing the user body:

       zerg_defer_rec *m = zerg_defer_top;
       ... user code with zerg_defer_push(fn, env) calls ...
       zerg_defer_drain(m);

   so each fn frame's defers ran at fn exit, not at thread exit. Under
   v0.12 the defer stack lives on the coroutine (zerg_coro_t.defer_head,
   nodes of type zerg_defer_node_t from coroRuntimeC). We keep the
   per-fn-frame semantics by reading the current coro's defer_head as
   the marker and draining back to it on fn exit.

   zerg_defer_rec is aliased to zerg_defer_node_t so the v0.7 struct
   name still names a valid type. zerg_defer_top is a macro reading the
   current coro's defer_head (NULL outside a coroutine — main is now
   wrapped in a top-level coro by cgen so this is reachable everywhere
   user code actually pushes defers). */
typedef zerg_defer_node_t zerg_defer_rec;

#define zerg_defer_top (zerg_current_coro ? zerg_current_coro->defer_head : 0)

static void zerg_defer_push(void (*fn)(void *), void *env) {
    zerg_coro_defer(fn, env);
}

/* zerg_defer_drain pops nodes from the current coroutine's defer stack
   in LIFO order until reaching the saved marker, executing each fn
   along the way. Outside a coroutine context (no current_coro) it is a
   no-op — there can be no pushes either, so the marker is also NULL.
   When the coroutine eventually finishes, zerg_coro_entry walks any
   leftover defers automatically. */
static void zerg_defer_drain(zerg_defer_rec *marker) {
    zerg_coro_t *c = zerg_current_coro;
    if (!c) return;
    while (c->defer_head != marker) {
        zerg_defer_node_t *r = c->defer_head;
        c->defer_head = r->next;
        r->fn(r->env);
        free(r);
    }
}

/* ---------------- wait_group shim (v0.7 surface) -----------------------
   Aliases the v0.7-named type to the v0.12 zerg_waitgroup_t and
   forwards every helper. cgen-emitted code constructs / adds / waits /
   dones on zerg_wait_group_t pointers; under the new runtime they are
   really zerg_waitgroup_t instances with park/unpark semantics. */
typedef zerg_waitgroup_t zerg_wait_group_t;

static zerg_wait_group_t *zerg_wait_group_make(void) {
    return zerg_waitgroup_make();
}

static void zerg_wait_group_add(zerg_wait_group_t *w, int64_t delta) {
    zerg_waitgroup_add(w, delta);
}

static void zerg_wait_group_done(zerg_wait_group_t *w) {
    zerg_waitgroup_done(w);
}

static void zerg_wait_group_wait(zerg_wait_group_t *w) {
    zerg_waitgroup_wait(w);
}

/* ---------------- main-thread drain shim ------------------------------
   v0.7 emitted zerg_main_wg_add(1) on every spawn entry and
   zerg_main_wg_done() on every spawn exit, then zerg_main_wg_wait() at
   the end of main to join all detached pthreads. v0.12 tracks live
   coroutines via the scheduler's live_coros counter, so the add/done
   shims are no-ops and zerg_main_wg_wait delegates to
   zerg_sched_drain. */
static void zerg_main_wg_add(int64_t delta) {
    (void)delta;
}

static void zerg_main_wg_done(void) {
}

static void zerg_main_wg_wait(void) {
    zerg_sched_drain();
}

/* ---------------- spawn shim (v0.7 surface) ----------------------------
   v0.7 emit calls zerg_spawn(fn, env) where fn is void *(void *). The
   v0.12 zerg_coro_spawn takes void(*)(void*); we wrap with a one-shot
   adapter that invokes fn and discards its return. The adapter env is
   heap-allocated and freed inside the trampoline. */
typedef struct {
    void *(*fn)(void *);
    void *env;
} zerg_v07_spawn_env_t;

static void zerg_v07_spawn_trampoline(void *p) {
    zerg_v07_spawn_env_t *e = (zerg_v07_spawn_env_t *)p;
    void *(*fn)(void *) = e->fn;
    void *env = e->env;
    free(e);
    fn(env);
}

static void zerg_spawn(void *(*fn)(void *), void *env) {
    zerg_v07_spawn_env_t *e =
        (zerg_v07_spawn_env_t *)malloc(sizeof(zerg_v07_spawn_env_t));
    e->fn = fn;
    e->env = env;
    zerg_coro_spawn(zerg_v07_spawn_trampoline, e);
}
`
