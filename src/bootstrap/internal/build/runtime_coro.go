package build

// v0.12 Unit 1 — coroutine primitive.
//
// The v0.7 build-side runtime spawns one OS thread per `spawn` via
// pthread_create. v0.12 retires that model in favour of an M:N green-thread
// scheduler: many coroutines share a smaller pool of OS threads, channel
// blocking parks the calling coroutine instead of an OS thread, and `spawn`
// becomes cheap (a malloc + ucontext setup).
//
// This unit lands the coroutine primitive only — no scheduler, no channel
// integration. The C source lives in coroRuntimeC and is consumed by a
// targeted unit test that builds a tiny driver program and asserts the
// round-trip yield/resume works. U2 will fold the primitive into the v0.12
// runtime emission path; U3+ rewrite channels / wait_group / select on top
// of the resulting park/unpark API.
//
// Implementation notes:
//
//   - Context switch via POSIX ucontext (`getcontext` / `makecontext` /
//     `swapcontext`). Marked deprecated on macOS but ships under
//     `_XOPEN_SOURCE 600`; we silence the warning and keep portability.
//     A future v0.13 unit may swap in per-arch inline-asm switches for
//     speed without touching callers.
//
//   - Fixed 256 KiB stack per coroutine. The bottom 4 KiB is mprotect'd
//     PROT_NONE so a stack overflow surfaces as SIGSEGV instead of silent
//     corruption of the next coroutine's stack. Growable stacks are out of
//     scope at v0.12.
//
//   - Coroutine states (CORO_*) gate transitions during U2+ scheduling.
//     U1 only exercises NEW -> RUNNING -> SUSPENDED -> RUNNING -> DONE.
//
//   - The "current coroutine" pointer is per-OS-thread. U1 runs on a single
//     OS thread (the main thread), so `_Thread_local` is sufficient and
//     stays sufficient when U2 introduces worker threads.
//
//   - `makecontext` only admits int args, not pointers. We split the
//     coroutine pointer across two ints in the entry stub and reassemble
//     in zerg_coro_entry. amd64 and arm64 both have 64-bit pointers; the
//     int width is at least 32 bits per C99, so two ints suffice.

const coroRuntimeC = `
#define _XOPEN_SOURCE 600
/* _DARWIN_C_SOURCE re-exposes MAP_ANON on macOS (hidden under strict
   _XOPEN_SOURCE); _DEFAULT_SOURCE plays the same role on glibc. Linux
   spells the constant MAP_ANONYMOUS while macOS uses MAP_ANON, so we
   feature-test below and standardise on MAP_ANONYMOUS in the call site. */
#define _DARWIN_C_SOURCE
#define _DEFAULT_SOURCE

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <ucontext.h>
#include <unistd.h>

#if !defined(MAP_ANONYMOUS) && defined(MAP_ANON)
#define MAP_ANONYMOUS MAP_ANON
#endif

#ifdef __APPLE__
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
#endif

/* zerg_coro_state_t tracks the lifecycle stages we want to assert against
   in U2+. U1 only walks NEW -> RUNNING -> SUSPENDED -> RUNNING -> DONE. */
typedef enum {
    ZERG_CORO_NEW = 0,
    ZERG_CORO_RUNNABLE = 1,
    ZERG_CORO_RUNNING = 2,
    ZERG_CORO_SUSPENDED = 3,
    ZERG_CORO_DONE = 4,
} zerg_coro_state_t;

typedef struct zerg_coro {
    ucontext_t ctx;          /* save area for swapcontext */
    ucontext_t *caller;      /* set on resume; yield/park swap back through it */
    void *stack_base;        /* mmap'd block, includes guard page */
    size_t stack_size;       /* total mmap'd size (guard + usable) */
    void (*fn)(void *);      /* user entry */
    void *arg;               /* opaque arg passed to fn */
    zerg_coro_state_t state;
    /* parked is set by zerg_coro_park (true) and cleared by yield (false)
       so the scheduler worker can distinguish "yielded — requeue locally"
       from "parked on a wait queue — leave alone, the unparker will
       requeue". Both paths swapcontext back through caller; only the bit
       differs. */
    int parked;
    uint64_t id;             /* monotonic id for diagnostics */
} zerg_coro_t;

/* ZERG_CORO_STACK_USABLE is the bytes available to user code; the actual
   mmap is one page larger to host a PROT_NONE guard at the bottom. 256 KiB
   chosen as the v0.12 default — large enough for the v0.7 corpus's deepest
   recursion (composite-clone + match dispatch ~ a few KiB), small enough
   that 10000 coroutines fit in 2.5 GiB of address space (not residency). */
#define ZERG_CORO_STACK_USABLE (256 * 1024)

/* zerg_current_coro is exported so callers in other TUs (the scheduler in
   schedRuntimeC, U3+ wait queues, and unit-test drivers) can reference it
   without going through an accessor. _Thread_local because each OS-thread
   worker has its own current-coroutine. */
_Thread_local zerg_coro_t *zerg_current_coro = 0;
static uint64_t zerg_coro_next_id = 1;

/* zerg_coro_entry is the C entry stub makecontext jumps to. It receives the
   coroutine pointer split across two ints (high/low halves) because
   makecontext only admits int args. We rejoin and dispatch the user fn,
   then mark the coroutine DONE and swap back to whichever context resumed
   us — c->caller is set by zerg_coro_resume / the scheduler worker before
   the swap-in, so the same pointer drives the swap-out. */
static void zerg_coro_entry(unsigned int hi, unsigned int lo) {
    uintptr_t packed = ((uintptr_t)hi << 32) | (uintptr_t)lo;
    zerg_coro_t *c = (zerg_coro_t *)packed;
    c->fn(c->arg);
    c->state = ZERG_CORO_DONE;
    swapcontext(&c->ctx, c->caller);
}

/* zerg_coro_alloc_stack mmaps stack memory and protects the bottom page so
   stack overflow surfaces as SIGSEGV instead of silent neighbour-corruption.
   Returns NULL on failure (out of memory / mmap rejected). */
static void *zerg_coro_alloc_stack(size_t *out_size) {
    long page = sysconf(_SC_PAGESIZE);
    if (page <= 0) page = 4096;
    size_t total = (size_t)page + ZERG_CORO_STACK_USABLE;
    void *base = mmap(0, total, PROT_READ | PROT_WRITE,
                      MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (base == MAP_FAILED) return 0;
    if (mprotect(base, (size_t)page, PROT_NONE) != 0) {
        munmap(base, total);
        return 0;
    }
    *out_size = total;
    return base;
}

zerg_coro_t *zerg_coro_new(void (*fn)(void *), void *arg) {
    zerg_coro_t *c = (zerg_coro_t *)calloc(1, sizeof(*c));
    if (!c) return 0;
    size_t sz = 0;
    void *base = zerg_coro_alloc_stack(&sz);
    if (!base) { free(c); return 0; }
    c->stack_base = base;
    c->stack_size = sz;
    c->fn = fn;
    c->arg = arg;
    c->state = ZERG_CORO_NEW;
    c->id = __atomic_fetch_add(&zerg_coro_next_id, 1, __ATOMIC_RELAXED);
    if (getcontext(&c->ctx) != 0) {
        munmap(base, sz);
        free(c);
        return 0;
    }
    long page = sysconf(_SC_PAGESIZE);
    if (page <= 0) page = 4096;
    c->ctx.uc_stack.ss_sp = (char *)base + page; /* skip guard page */
    c->ctx.uc_stack.ss_size = sz - (size_t)page;
    c->ctx.uc_link = 0;
    uintptr_t packed = (uintptr_t)c;
    unsigned int hi = (unsigned int)(packed >> 32);
    unsigned int lo = (unsigned int)(packed & 0xffffffffu);
    makecontext(&c->ctx, (void (*)(void))zerg_coro_entry, 2, hi, lo);
    return c;
}

/* zerg_coro_resume jumps into a coroutine. Returns when the coroutine
   yields (state SUSPENDED) or finishes (state DONE). Marks the coroutine
   RUNNING for the duration; the caller's context lives in a stack-local
   ucontext_t whose address is stashed in c->caller — yield/park reads
   that pointer to swap straight back. */
void zerg_coro_resume(zerg_coro_t *c) {
    ucontext_t local;
    zerg_coro_t *prev = zerg_current_coro;
    zerg_current_coro = c;
    c->caller = &local;
    c->state = ZERG_CORO_RUNNING;
    swapcontext(&local, &c->ctx);
    zerg_current_coro = prev;
}

/* zerg_coro_yield gives control back to whoever called zerg_coro_resume
   (or to the scheduler worker if running under a worker pool) on this
   coroutine. May only be called from a coroutine context; callers without
   a current coroutine are a programming error and abort. */
void zerg_coro_yield(void) {
    zerg_coro_t *c = zerg_current_coro;
    if (!c) {
        fprintf(stderr, "zerg: zerg_coro_yield called outside a coroutine\n");
        abort();
    }
    c->state = ZERG_CORO_SUSPENDED;
    c->parked = 0;
    swapcontext(&c->ctx, c->caller);
}

void zerg_coro_free(zerg_coro_t *c) {
    if (!c) return;
    if (c->stack_base) munmap(c->stack_base, c->stack_size);
    free(c);
}

#ifdef __APPLE__
#pragma clang diagnostic pop
#endif
`

// CoroRuntimeC exposes the U1 C source so the targeted test can compile a
// driver program that links against it. U2+ folds the source into the
// emitted runtime via the normal cgen prelude path; this exporter exists
// solely so the unit-1 test does not have to re-derive the source.
func CoroRuntimeC() string { return coroRuntimeC }
