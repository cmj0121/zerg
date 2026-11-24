/*
 * unwind.c - the Zerg runtime abort/unwind mechanism and cleanup(defer) stack.
 *
 * There is a single teardown mechanism: one LIFO cleanup stack that holds every
 * pending deferred action - both `defer` thunks and Ref releases - so they run
 * in reverse construction order, interleaved, on every exit path. The normal
 * path calls zrt_unwind_to(mark); the abort path (zrt_abort) unwinds the same
 * stack up to the innermost handler and longjmps to it. Because both consume the
 * one stack, each pending action runs exactly once per path.
 *
 * State is gathered into one zrt_tls bundle (the cleanup stack + the abort
 * handler chain). A single-threaded program keeps one process-global bundle, so
 * this is exactly the Phase 1d behaviour; the 1e scheduler gives each coroutine
 * its own bundle and swaps the "current" one (zrt_tls_save / zrt_tls_load) around
 * every context switch. Moving the state into a switchable bundle is a source-level
 * refactor only: every zrt_ function below still operates on "the current bundle",
 * so no emitted C changes. The backend only reaches this file when a program uses
 * runtime teardown; value-only programs never touch it.
 */

/* pthread_key_create / pthread_setspecific (the Darwin probe mirror) are POSIX, not ISO C:
 * ask for the POSIX.1-2008 surface before any header, as sys.c does. */
#ifndef _POSIX_C_SOURCE
#define _POSIX_C_SOURCE 200809L
#endif

#include "zergrt.h"

#include <stdlib.h>
#if defined(__APPLE__)
#include <pthread.h>
#endif

/* the current unwind bundle: the one cleanup stack (grown on demand) and the
 * innermost abort handler. For a non-concurrent program this is the whole program's
 * state; under the scheduler it is whichever coroutine is currently running. */
/* PER-WORKER. This is the bundle of whichever coroutine this thread is running right
 * now — the scheduler loads it before a switch in and snapshots it back after. With one
 * worker that is one global, exactly as before; with M workers, a process-global would
 * mean two coroutines sharing one cleanup stack and one abort handler, and each would
 * unwind through the other's defers. The bundle belongs to the coroutine (zrt_coro.tls);
 * this is only the slot for the one currently mounted on this thread. */
static ZRT_THREAD_LOCAL zrt_tls g_tls;

/* The crash flag is true only while a coroutine unwinds an UNHANDLED crash (its target
 * handler is its outermost one). chan.c reads it through zrt_crash_active as that unwind
 * runs the coroutine's sender releases, so a channel auto-closed by a crashing last
 * sender carries a crash Err (Fork-C) rather than a clean end.
 *
 * It lives in g_tls, the bundle the scheduler saves and loads around every switch, and
 * NOT beside it as a per-worker flag — which is what it was, with a comment arguing that
 * per-worker was per-coroutine enough. It is not: a coroutine MIGRATES, and the unwind
 * that sets this runs cleanups that wake other coroutines and can hand the CPU over. When
 * that happened the crash flag stayed on the worker the crash started on, the releases
 * that ran afterwards saw it clear, and the channel closed with StopIteration — so a
 * consumer's `for v in ch` ended cleanly and the statement after it, the one the crash
 * was supposed to make unreachable, ran.
 *
 * conc_crash, one run in a hundred and twenty, printed `1 2 99`. */
bool zrt_crash_active(void) {
	return g_tls.crashing;
}

void zrt_tls_save(zrt_tls *out) {
	*out = g_tls;
}

void zrt_tls_load(const zrt_tls *in) {
	zrt_pend_free_local();
	g_tls = *in;
	zrt_stack_limit_put(in->stack_lim);
}

/* THE PROBE'S LIMIT (#480). The bundle owns it — a coroutine migrates, and its limit with
 * it — and ZRT_PROBE reads a per-thread mirror of the mounted one, so every write goes
 * through zrt_stack_limit_put and the two cannot drift. Darwin's mirror is a pthread TSD
 * slot, read off the thread register (zergrt.h). */
#if defined(__APPLE__) && (defined(__aarch64__) || defined(__x86_64__))
uintptr_t zrt_stack_key;

/* A key left at 0 would name TSD slot 0, which holds pthread_self — never NULL — and every
 * probed call would read as an overflow. So no key is no program. */
__attribute__((constructor)) static void zrt_stack_key_init(void) {
	pthread_key_t k;
	if (pthread_key_create(&k, NULL) != 0) {
		zrt_abort("cannot create the stack-probe key");
	}
	zrt_stack_key = (uintptr_t)k;
}

void zrt_stack_limit_put(char *lim) {
	g_tls.stack_lim = lim;
	(void)pthread_setspecific((pthread_key_t)zrt_stack_key, lim);
}
#else
ZRT_THREAD_LOCAL char *zrt_stack_lim_tls;

void zrt_stack_limit_put(char *lim) {
	g_tls.stack_lim = lim;
	zrt_stack_lim_tls = lim;
}
#endif

/* never inlined nor analysed across (GCC's noipa; clang has only noinline): a caller that
 * crossed a park must get THIS worker's bundle, not a TLS address computed before the switch */
#if defined(__clang__)
__attribute__((noinline))
#elif defined(__GNUC__) && __GNUC__ >= 8
__attribute__((noipa))
#elif defined(__GNUC__)
__attribute__((noinline))
#endif
zrt_tls *zrt_tls_cur(void) {
	return &g_tls;
}

static void pend_reset(zrt_tls *t) {
	t->pend = NULL;
	t->plen = 0;
	t->pcap = 0;
	t->pbase = 0;
	t->dactive = false;
}

void zrt_tls_detach(void) {
	pend_reset(&g_tls);
	zrt_stack_limit_put(NULL); /* a worker's native stack never probes */
}

void zrt_pend_free_local(void) {
	free(g_tls.pend);
	pend_reset(&g_tls);
}

void zrt_tls_free(zrt_tls *t) {
	/* an Err a crash left in flight, which no guard took: the coroutine reported it and
	 * ended, and the bundle is the last thing holding it */
	zrt_err_drop(t->taken);
	t->taken = (zrt_err){0};
	free(t->stack);
	t->stack = NULL;
	t->len = 0;
	t->cap = 0;
	t->handler = NULL;
	free(t->pend);
	pend_reset(t);
}

size_t zrt_scope_mark(void) {
	return g_tls.len;
}

void *zrt_grow(void *p, size_t *cap, size_t elsz) {
	size_t n = *cap == 0 ? 16 : *cap * 2;
	void *grown = realloc(p, n * elsz);
	if (grown != NULL) {
		*cap = n;
	}
	return grown;
}

void zrt_defer(void (*fn)(void *env), void *env) {
	if (g_tls.len == g_tls.cap) {
		zrt_cleanup *grown = (zrt_cleanup *)zrt_grow(g_tls.stack, &g_tls.cap, sizeof(zrt_cleanup));
		if (grown == NULL) {
			zrt_abort("out of memory (cleanup stack)");
		}
		g_tls.stack = grown;
	}
	g_tls.stack[g_tls.len].fn = fn;
	g_tls.stack[g_tls.len].env = env;
	g_tls.len++;
}

void zrt_unwind_to(size_t mark) {
	while (g_tls.len > mark) {
		zrt_cleanup c = g_tls.stack[--g_tls.len];
		c.fn(c.env);
	}
}

void zrt_forget(size_t n) {
	g_tls.len -= n;
}

/* the cleanup a disarmed entry holds: it keeps the slot's place on the stack and gives
 * nothing back, because zrt_undefer already did */
static void zrt_disarmed(void *env) {
	(void)env;
}

/* Searched from the top: the binding a `del` names is in the current scope or one around
 * it, so its entry is near the top, and the innermost live entry for an address is its own. */
void zrt_undefer(void *env) {
	size_t i = g_tls.len;
	while (i > 0) {
		i--;
		zrt_cleanup *c = &g_tls.stack[i];
		if (c->env == env && c->fn != zrt_disarmed) {
			void (*fn)(void *) = c->fn;
			c->fn = zrt_disarmed;
			fn(env);
			return;
		}
	}
}

void zrt_defer_release(void *p) {
	zrt_release(*(void **)p);
}

void zrt_defer_dyn_free(void *p) {
	zrt_dyn_free(*(void **)p);
}

void zrt_handler_push(zrt_frame *frame) {
	frame->mark = g_tls.len;
	frame->prev = g_tls.handler;
	frame->catches = false;
	frame->pmark = g_tls.plen;
	frame->pbase = g_tls.pbase;
	frame->dactive = g_tls.dactive;
	frame->stack_lim = g_tls.stack_lim;
	frame->overflow_unwind = g_tls.overflow_unwind;
	g_tls.handler = frame;
}

void zrt_handler_push_catch(zrt_frame *frame) {
	zrt_handler_push(frame);
	frame->catches = true;
}

void zrt_handler_pop(zrt_frame *frame) {
	g_tls.handler = frame->prev;
}

/* zrt_err_kindname is the taxonomy's number -> NAME direction, and the one place the
 * `Kind: ` prefix of the abort line (docs/conformance.md) is built. A generic Err — kind
 * NONE, what a bare `raise "…"` carries — has no name to say, so it reports verbatim.
 *
 * The prefix belongs to the RENDERING and not to the message. It used to be spelled into
 * each of the runtime's own literals ("IndexError: index out of range"), which made the
 * shape of the line a property of who raised the error: a `raise ValueError("bad input")`
 * a program wrote itself carried the kind in its value and printed without it, and the
 * same kind from the runtime printed with it. Rendering here gives both one shape, and
 * leaves message() answering the bare text either way. */
static const char *zrt_err_kindname(int kind) {
	switch (kind) {
	case ZRT_ERR_VALUE: return "ValueError";
	case ZRT_ERR_OVERFLOW: return "OverflowError";
	case ZRT_ERR_IO: return "IOError";
	case ZRT_ERR_ENCODING: return "EncodingError";
	case ZRT_ERR_INDEX: return "IndexError";
	case ZRT_ERR_KEY: return "KeyError";
	case ZRT_ERR_DEADLOCK: return "DeadlockError";
	case ZRT_ERR_SEND_ON_CLOSED: return "SendOnClosedError";
	case ZRT_ERR_STOP_ITERATION: return "StopIteration";
	case ZRT_ERR_DIVZERO: return "DivideByZeroError";
	case ZRT_ERR_ASSERTION: return "AssertionError";
	case ZRT_ERR_ALIAS: return "AliasError";
	case ZRT_ERR_STACK_OVERFLOW: return "StackOverflowError";
	default: return NULL;
	}
}

/* zrt_stack_land gives the landing scope the probe state it had at push (#480): an
 * overflow's lowered limit and its flag end exactly where its unwind does. Driven by the
 * frame, never by the Err's kind — a guard inside the unwind keeps the flag it saw. */
static void zrt_stack_land(const zrt_frame *h) {
	zrt_tls *t = zrt_tls_cur();
	t->overflow_unwind = h->overflow_unwind;
	zrt_stack_limit_put(h->stack_lim);
}

/* land_drain finishes, before the longjmp, every release the abort interrupted above the
 * handler's floor: a drain loop the longjmp is about to skip would otherwise leave them
 * owed, and a list whose first element's drop aborted would never drop the rest. Then the
 * scope the handler was pushed in gets its own drain state and probe state back — the
 * probe state last, so a drop an overflow queued still drains while overflow_unwind is
 * set. A further abort inside this drain lands on the same handler and resumes the entries
 * it left. */
static void land_drain(const zrt_frame *h) {
	g_tls.pbase = h->pmark;
	g_tls.dactive = false;
	zrt_drain();
	/* the drain can park and resume on another worker: re-read the bundle */
	zrt_tls *t = zrt_tls_cur();
	t->pbase = h->pbase;
	t->dactive = h->dactive;
	zrt_stack_land(h);
}

/* zrt_unwind_abort is the shared abort core: report the error, then unwind to the
 * innermost handler and longjmp (or exit when none). Both the string zrt_abort and
 * the Err-carrying zrt_raise_err funnel through it; the only difference is whether
 * an Err value was stashed in g_tls.taken first (Decision D). */
static _Noreturn void zrt_unwind_abort(zrt_err e, bool place) {
	zrt_frame *h = g_tls.handler;
	/* A `guard` handler demotes this abort to a Result value, so it is not an error
	 * the program failed to handle: land on it WITHOUT reporting the message (the Err
	 * value carries it). Only a reporting handler (or none) prints the diagnostic. */
	if (h != NULL && h->catches) {
		zrt_unwind_to(h->mark);
		land_drain(h);
		longjmp(h->buf, 1);
	}
	zrt_report_at(zrt_err_kindname(e.kind), e.msg, place ? zrt_site_here() : NULL);
	if (h != NULL) {
		/* An unhandled coroutine crash unwinds to the coroutine's outermost handler
		 * (the trampoline's, whose prev is NULL); mark it so the sender releases run
		 * during this unwind close their channels with a crash Err. A handled abort
		 * (an inner user handler, prev != NULL) is not a crash and leaves the flag off. */
		g_tls.crashing = (h->prev == NULL);
		zrt_unwind_to(h->mark);
		land_drain(h);
		zrt_tls_cur()->crashing = false;
		longjmp(h->buf, 1);
	}
	/* no handler installed (e.g. abort before program entry): last resort. Under the
	 * scheduler every coroutine installs a bottom handler in its trampoline, so a
	 * handler-less coroutine abort is contained there rather than reaching this. */
	exit(1);
}

_Noreturn void zrt_abort_cell(int kind, const char *msg) {
	/* The abort still stashes an Err so a surrounding `guard` recovers a value (with the
	 * message and the KIND, so `err is ValueError` holds) rather than nothing. The message
	 * is the immortal cell zrt_abort/zrt_abort_kind built around their literal, so the Err
	 * owns it like any other str and a reader may keep it (#405). */
	g_tls.taken = zrt_err_own(kind, msg);
	zrt_unwind_abort(g_tls.taken, true);
}

/* zrt_stack_overflow is ZRT_PROBE's hit (#480): the first one lowers the limit to FLOOR,
 * which is the room the unwind's defers and drops run in, and aborts like any other error.
 * A hit while that unwind runs has no reserve left to give, so it is the hard fault. */
_Noreturn void zrt_stack_overflow(void) {
	zrt_tls *t = zrt_tls_cur();
	if (t->overflow_unwind) {
		zrt_overflow_exit();
	}
	t->overflow_unwind = true;
	zrt_stack_limit_put(t->stack_lim - (ZRT_STACK_RESERVE - ZRT_STACK_FLOOR));
	zrt_abort_kind(ZRT_ERR_STACK_OVERFLOW, "stack overflow");
}

zrt_err zrt_err_new(const char *msg) {
	return zrt_err_new_kind(ZRT_ERR_NONE, msg);
}

zrt_err zrt_err_new_kind(int kind, const char *msg) {
	zrt_err e;
	e.msg = msg;
	e.cause = NULL;
	e.kind = kind;
	e.owned = 0;
	return e;
}

zrt_err zrt_err_own(int kind, const char *msg) {
	zrt_err e = zrt_err_new_kind(kind, msg);
	e.owned = 1;
	return e;
}

/* the cause is the PAYLOAD of a counted cell, so its header is the word pair before it —
 * the layout a str cell has, and recovered the same way */
static void *zrt_err_cause_cell(zrt_err *cause) {
	return (zrt_ref_hdr *)cause - 1;
}

zrt_err zrt_err_copy(zrt_err e) {
	if (e.owned && e.msg != NULL) {
		zrt_str_retain(e.msg);
	}
	if (e.cause != NULL) {
		zrt_retain(zrt_err_cause_cell(e.cause));
	}
	return e;
}

void zrt_err_drop(zrt_err e) {
	if (e.owned && e.msg != NULL) {
		zrt_str_release(e.msg);
	}
	if (e.cause != NULL) {
		zrt_release(zrt_err_cause_cell(e.cause));
	}
}

/* zrt_err_cause_drop is the cause cell's teardown: the Err in it is the cell's to give back */
static void zrt_err_cause_drop(void *payload) {
	zrt_err_drop(*(zrt_err *)payload);
}

zrt_err zrt_err_with_cause(const char *msg, zrt_err cause) {
	return zrt_err_chain(zrt_err_new(msg), cause);
}

/* zrt_err_parent is the built-in taxonomy, one link at a time (docs/code/errors.md). An
 * OverflowError and an EncodingError are both KINDS OF ValueError — a value that was not one
 * this could accept, said more precisely — so a handler may catch coarsely without the coarse
 * answer being a lie.
 *
 * Zerg has no inheritance and this is not it: what a link means is IS-A, not was-caused-by,
 * which is why it is a table here and not a second Err materialised under every overflow. The
 * distinction matters to a reader of unwrap(): a cause is another error, and a parent is the
 * same error described more generally. */
static int zrt_err_parent(int kind) {
	switch (kind) {
	case ZRT_ERR_OVERFLOW:
	case ZRT_ERR_ENCODING:
		return ZRT_ERR_VALUE;
	default:
		return ZRT_ERR_NONE;
	}
}

/* zrt_err_in answers `e in Kind` — is this error a member of that kind's subtree? It walks the
 * taxonomy upward and no further.
 *
 * THREE RELATIONS, THREE SPELLINGS, and that is the whole design. `is` asks identity and stays
 * a kind comparison; `in` asks the taxonomy and is this; unwrap() asks what an error was
 * raised FROM. One predicate answering two of them distinguishes neither — which is what
 * happened when `is` walked the cause chain and `raise IOError("outer") from inner` for a
 * ValueError `inner` stopped being tellable from an error that simply is a ValueError. */
int zrt_err_in(zrt_err e, int kind) {
	int k = e.kind;
	while (k != ZRT_ERR_NONE) {
		if (k == kind) {
			return 1;
		}
		k = zrt_err_parent(k);
	}
	return 0;
}

zrt_err zrt_err_chain(zrt_err e, zrt_err cause) {
	zrt_err *cell = (zrt_err *)zrt_ref_payload(zrt_ref_alloc(sizeof(zrt_err), zrt_err_cause_drop));
	*cell = cause;
	if (e.cause != NULL) {
		zrt_release(zrt_err_cause_cell(e.cause));
	}
	e.cause = cell;
	return e;
}

_Noreturn void zrt_raise_err(zrt_err e) {
	g_tls.taken = e;
	zrt_unwind_abort(e, e.kind != ZRT_ERR_NONE);
}

_Noreturn void zrt_raise_fault(zrt_err e) {
	g_tls.taken = e;
	zrt_unwind_abort(e, true);
}

zrt_err zrt_taken_err(void) {
	zrt_err e = g_tls.taken;
	g_tls.taken = (zrt_err){0};
	return e;
}

zrt_err zrt_taken_err_copy(void) {
	return zrt_err_copy(g_tls.taken);
}
