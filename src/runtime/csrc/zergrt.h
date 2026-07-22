/*
 * zergrt.h - the sole public header of the Zerg C runtime (Phase 1d core).
 *
 * The Zerg compiler's emitted C translation unit includes this one header, and
 * only this one, when its Manifest reports that the program needs the runtime.
 * A value-only program (int/float/bool/str, no Ref, no defer/del/with) neither
 * includes nor links the runtime: its emitted C is byte-identical to Phase 0.
 *
 * Every runtime symbol carries the `zrt_` prefix so it cannot collide with the
 * `zg_` names the backend emits. The runtime is hosted (libc) for the MVP; the
 * allocator wrapper and the (non-atomic, single-threaded) refcount are the two
 * seams a later phase re-targets (freestanding allocator; atomic refcount for
 * the 1e scheduler) without touching emitted code.
 */
#ifndef ZERGRT_H
#define ZERGRT_H

#include <stddef.h>  /* size_t */
#include <stdint.h>  /* int32_t */
#include <stdbool.h> /* bool */
#include <setjmp.h>  /* jmp_buf */

/* --- allocator wrapper (alloc.c) ----------------------------------------- */

/* zrt_alloc returns n bytes of zero-uninitialised heap, aborting on OOM. It is
 * a thin wrapper over malloc so a later phase can swap the platform allocator
 * in one place without the backend re-emitting anything. */
void *zrt_alloc(size_t n);

/* zrt_free releases a block previously returned by zrt_alloc. */
void zrt_free(void *p);

/* --- Ref[T]: refcounted heap box (ref.c) --------------------------------- */

/* zrt_drop_fn runs when a Ref's refcount reaches zero, before the block is
 * freed. It receives the payload pointer (not the header). NULL means the
 * payload is plain memory with nothing to tear down. */
typedef void (*zrt_drop_fn)(void *payload);

/* zrt_ref_hdr is the header that immediately precedes a Ref's inline payload in
 * one allocation: `[ zrt_ref_hdr | payload... ]`. A Zerg `Ref[T]` value is, in
 * C, a `void*` pointing at the header. The layout (rc width, drop signature,
 * inline payload) is a build-contract commitment the backend depends on. */
typedef struct {
	size_t      rc;   /* strong refcount; the last holder frees the block */
	zrt_drop_fn drop; /* run at rc==0 before free (NULL = nothing to drop) */
} zrt_ref_hdr;

/* zrt_ref_alloc allocates a Ref holding payload_sz bytes with refcount 1 and
 * the given drop function, and returns the header pointer. */
void *zrt_ref_alloc(size_t payload_sz, zrt_drop_fn drop);

/* zrt_retain increments a Ref's refcount (copying a Ref value). */
void zrt_retain(void *ref);

/* zrt_ref_copy retains a Ref and returns it, so copying a Ref value is a single
 * expression 'x = zrt_ref_copy(y)' the backend can drop into any copy site. */
void *zrt_ref_copy(void *ref);

/* zrt_release decrements a Ref's refcount; at zero it runs the drop function on
 * the payload and frees the block. Dropping a Ref value calls this. */
void zrt_release(void *ref);

/* zrt_ref_payload returns the payload pointer for a Ref header (header + 1). */
void *zrt_ref_payload(void *ref);

/* --- abort / unwind + cleanup(defer) stack (unwind.c) -------------------- */

/* zrt_cleanup is one pending deferred action on the cleanup stack: a thunk and
 * its captured environment. Both `defer` thunks and Ref releases are pushed as
 * cleanups, so they interleave on one LIFO timeline (reverse construction
 * order) exactly as the memory model specifies. */
typedef struct {
	void (*fn)(void *env);
	void *env;
} zrt_cleanup;

/* zrt_frame is an abort handler: a setjmp landing pad plus the cleanup-stack
 * height to unwind to on abort. Frames form a stack via `prev`. The setjmp
 * itself must be evaluated in the frame owner's own activation (see zrt_run),
 * so this struct is populated by zrt_handler_push but the buf is armed by the
 * caller's setjmp. */
typedef struct zrt_frame {
	jmp_buf           buf;
	size_t            mark;
	struct zrt_frame *prev;
	/* catches marks a `guard` handler that DEMOTES an abort to a Result value: it
	 * takes the in-flight Err without reporting it to stderr (the program handles the
	 * error). A root/reporting handler (zrt_run, a coroutine trampoline) leaves it
	 * false, so an uncaught abort still prints its diagnostic. */
	bool              catches;
} zrt_frame;

/* zrt_tls is the whole of the abort/unwind machinery's mutable state gathered into
 * one switchable bundle: the cleanup(defer) stack and the innermost abort handler.
 * A single-threaded program (Phase 1d) keeps one process-global zrt_tls and the
 * behaviour is exactly as before. The 1e scheduler gives every coroutine its own
 * zrt_tls and swaps the "current" bundle around each context switch (zrt_tls_save /
 * zrt_tls_load), so a coroutine's defers/drops and its abort handler act on its own
 * stack. Moving this state does NOT change any emitted C: the cleanup-stack API
 * (zrt_scope_mark / zrt_defer / zrt_unwind_to / zrt_handler_push / …) is unchanged;
 * only where it reads its state from moved. */
/* zrt_err is the Zerg runtime's minimal error value (Decision D): a message and an
 * optional chained cause. It is INTERNAL — never FFI-frozen — so a later phase may
 * add fields without breaking any ABI (a Result is never FFI-safe). `raise e`,
 * `x!`, and an abort carry one; a surrounding `guard`/`?` reads it back. */
typedef struct zrt_err {
	const char     *msg;
	struct zrt_err *cause;
} zrt_err;

typedef struct {
	zrt_cleanup *stack; /* the cleanup(defer) stack (was the file-static g_stack) */
	size_t       len;   /* its live height (was g_len) */
	size_t       cap;   /* its allocated capacity (was g_cap) */
	zrt_frame   *handler; /* the innermost abort handler (was g_handler) */
	zrt_err      taken;   /* the in-flight Err an abort/raise carries; a `guard` reads
	                       * it with zrt_taken_err on the abort landing (Decision D). */
} zrt_tls;

/* zrt_tls_save snapshots the current unwind state into out; zrt_tls_load makes in
 * the current unwind state. The scheduler brackets each zrt_ctx_swap with these so
 * the cleanup stack and handler chain follow the running coroutine. Non-concurrent
 * programs never call them. */
void zrt_tls_save(zrt_tls *out);
void zrt_tls_load(const zrt_tls *in);

/* zrt_tls_free releases a coroutine-owned cleanup stack's backing buffer (grown by
 * zrt_defer) when the coroutine is reclaimed, so a finished coroutine leaks nothing.
 * The process-global (main) zrt_tls is never freed. */
void zrt_tls_free(zrt_tls *t);

/* zrt_scope_mark records the current cleanup-stack height, to unwind back to on
 * scope exit. */
size_t zrt_scope_mark(void);

/* zrt_defer pushes a deferred action (a `defer` thunk or a Ref release) onto
 * the cleanup stack. */
void zrt_defer(void (*fn)(void *env), void *env);

/* zrt_unwind_to runs and pops cleanups LIFO down to a mark taken earlier by
 * zrt_scope_mark. This is the normal (non-abort) scope-exit path. */
void zrt_unwind_to(size_t mark);

/* zrt_handler_push links a frame as the innermost abort handler, recording the
 * current cleanup-stack height in frame->mark. The caller must then arm
 * frame->buf with setjmp in its own activation. A plain handler REPORTS an abort's
 * message before landing (the reporting root: zrt_run, a coroutine trampoline). */
void zrt_handler_push(zrt_frame *frame);

/* zrt_handler_push_catch is zrt_handler_push for a `guard` handler: it marks the
 * frame `catches`, so an abort demoted to a Result value prints no diagnostic (the
 * program is handling the error; the message rides in the Err value instead). */
void zrt_handler_push_catch(zrt_frame *frame);

/* zrt_handler_pop unlinks the innermost abort handler. */
void zrt_handler_pop(zrt_frame *frame);

/* zrt_abort unwinds cleanups up to the innermost handler (running every pending
 * defer/drop along the way) and longjmps to it; with no handler it reports the
 * message and exits non-zero. This is the single exit that `raise`, `x!`, an
 * alias violation and OOM all funnel through. */
_Noreturn void zrt_abort(const char *msg);

/* --- Err value + carrying abort (unwind.c, Decision D) -------------------- */

/* zrt_err_new builds an Err with the given message and no cause. */
zrt_err zrt_err_new(const char *msg);

/* zrt_err_with_cause builds an Err chained to a cause: `raise e from c` records c
 * so a handler can walk the chain. The cause is copied to the heap (the caller's
 * cause value is a stack temporary), leaked for the MVP like every other box. */
zrt_err zrt_err_with_cause(const char *msg, zrt_err cause);

/* zrt_raise_err aborts carrying an Err VALUE: it stashes e (so a `guard`/`?` reads
 * it back with zrt_taken_err), reports e.msg, then unwinds and longjmps exactly as
 * zrt_abort. `raise e`, `x!`, and the propagate paths funnel through here. */
_Noreturn void zrt_raise_err(zrt_err e);

/* zrt_taken_err returns the Err the current abort/raise carried, read on a `guard`
 * setjmp!=0 landing. It is an empty Err (msg NULL) when nothing was stashed. */
zrt_err zrt_taken_err(void);

/* --- minimal sys surface (sys.c) ----------------------------------------- */

/* zrt_report writes a diagnostic line to stderr. The MVP sys surface is just
 * abort-message output; the stream primitives below are the io module's leaves. */
void zrt_report(const char *msg);

/* --- io streams (sys.c, Phase 1f) ---------------------------------------------
 *
 * The minimal byte-level standard-stream surface the stdlib `io` module lowers
 * onto (through the compiler write intrinsics for now, the FFI binder later). The
 * standard fds are 0=stdin, 1=stdout, 2=stderr. These ride in the always-linked
 * sys.c, so a program that `import "io"` links them via NeedsRuntime; a program
 * that never imports io never calls them. This is the one spot a freestanding
 * backend swaps libc read/write for a platform console. */

/* zrt_write writes up to n bytes of buf to fd, retrying short writes, and returns
 * the total written or -1 on error (errno semantics). */
long zrt_write(int fd, const uint8_t *buf, size_t n);

/* zrt_read reads up to n bytes from fd into buf and returns the count (0 at end of
 * input, never a blocking wait beyond one read) or -1 on error. */
long zrt_read(int fd, uint8_t *buf, size_t n);

/* zrt_write_str writes the NUL-terminated string s to fd (its strlen bytes); a NULL
 * s writes nothing. Best-effort convenience over zrt_write. */
long zrt_write_str(int fd, const char *s);

/* zrt_write_int writes the decimal text of v to fd. Best-effort convenience over
 * zrt_write. */
long zrt_write_int(int fd, int64_t v);

/* --- Result[nil] + program entry (entry.c) ------------------------------- */

/* zrt_result_nil is the C encoding of Zerg's `Result[nil]` at the program-entry
 * position: a tag where 0 is Ok and non-zero is Err. The backend spells a
 * `fn main() -> Result[nil]` return in this type; general Result[T] operator
 * lowering is a later phase. */
typedef struct {
	int32_t tag;
} zrt_result_nil;

/* zrt_result_ok is the Ok value of Result[nil]. */
static inline zrt_result_nil zrt_result_ok(void) {
	zrt_result_nil r;
	r.tag = 0;
	return r;
}

/* zrt_result_is_err reports whether a Result[nil] is the Err case. */
static inline bool zrt_result_is_err(zrt_result_nil r) {
	return r.tag != 0;
}

/* zrt_main_fn is the shape of a `fn main() -> Result[nil]` after lowering. */
typedef zrt_result_nil (*zrt_main_fn)(void);

/* zrt_run is the C entry shim for a `fn main() -> Result[nil]` program: it
 * installs the root abort handler (its setjmp lives in this function's own
 * activation, so the longjmp target stays valid), runs main under a root scope,
 * unwinds top-level defers, and maps the outcome to a process exit code (0 Ok,
 * 1 Err or abort). Value-only programs do not use this shim. */
int zrt_run(zrt_main_fn fn);

/* --- concurrency: coroutine scheduler + spawn (sched.c, ctx_<arch>) ----------
 *
 * Everything below is linked ONLY when a program's Manifest reports Concurrency
 * (it uses `spawn` / a channel). A program without concurrency neither links
 * sched.c nor the per-arch context switch, so these declarations are unused and
 * its emitted C is byte-identical to the non-concurrent path. The scheduler is
 * N:1 cooperative: one OS thread, a FIFO run queue of stackful coroutines, and a
 * context switch hidden behind the zrt_ctx shim (ctx_arm64.S / ctx_x86_64.S, or
 * ctx_ucontext.c as a portable floor). */

/* zrt_ctx is an opaque saved machine context: the callee-saved registers, the
 * stack pointer, and the resume point. Its storage must be large enough for every
 * backend; the arm64 layout uses the first 21 slots (x19-x30, sp, d8-d15). The
 * per-arch .S files know these offsets — do not reorder without updating them. */
typedef struct zrt_ctx {
	void *slots[24];
} zrt_ctx;

/* zrt_ctx_init prepares c so that the first zrt_ctx_swap into it begins executing
 * entry(arg) on the stack [stack_base, stack_base+size). The stack grows down from
 * the high end; the caller places a guard page at the low end. */
void zrt_ctx_init(zrt_ctx *c, void *stack_base, size_t size, void (*entry)(void *), void *arg);

/* zrt_ctx_swap saves the current execution point into *from and resumes the one
 * saved in *to; when control later returns to *from it continues as if this call
 * returned. It saves only callee-saved state (no signal mask), so it is cheap. */
void zrt_ctx_swap(zrt_ctx *from, zrt_ctx *to);

/* zrt_coro_state is a coroutine's scheduling state. RUNNABLE sits on the run queue;
 * BLOCKED is parked on a channel wait queue (1e channels, C2); DONE has finished its
 * thunk and awaits reclamation by the scheduler. */
typedef enum {
	ZRT_CORO_RUNNABLE,
	ZRT_CORO_BLOCKED,
	ZRT_CORO_DONE,
} zrt_coro_state;

/* zrt_coro is one stackful coroutine: its saved context, its own fixed stack (with a
 * guard page), its scheduling state, the thunk+env `spawn` marshalled, and its
 * private unwind state (its cleanup stack + abort handler chain). qnext threads it on
 * the run queue. */
typedef struct zrt_coro {
	zrt_ctx          ctx;
	void            *stack;      /* mmap base (guard page + usable stack) */
	size_t           stack_size; /* total mapped size, incl. the guard page */
	zrt_coro_state   state;
	void           (*thunk)(void *env); /* the marshalled call (spawn trampoline body) */
	void            *env;               /* heap-owned argument environment; thunk frees it */
	zrt_tls          tls;               /* this coroutine's own cleanup stack + handler */
	struct zrt_coro *qnext;             /* intrusive run-queue link */
} zrt_coro;

/* ZRT_CORO_STACK is the fixed per-coroutine stack size (Fork-B: fixed size + guard
 * page, not growable). Kept in one place so a later phase can retune it or move to a
 * growable stack without the backend re-emitting anything. */
#define ZRT_CORO_STACK ((size_t)(256 * 1024))

/* zrt_spawn allocates a coroutine (stack + guard page), arms it to run thunk(env) on
 * its own stack, and enqueues it on the run queue. Fire-and-forget: no handle, no
 * join. env is heap-owned and the thunk frees it. */
void zrt_spawn(void (*thunk)(void *env), void *env);

/* zrt_yield voluntarily returns to the scheduler, leaving the current coroutine
 * RUNNABLE so it resumes later. The only cooperative yield point this iteration
 * (channel send/recv add more in C2). A no-op when not inside a coroutine. */
void zrt_yield(void);

/* zrt_sched_main / _nil / _int are the concurrency program-entry shims, one per
 * `main` return shape. Each starts the scheduler, runs main as the first coroutine,
 * drains the run queue (so fire-and-forget coroutines get to run — they are never
 * joined but are not killed), and returns main's outcome as the process exit code
 * (an aborting main yields 1, as zrt_run does). The backend selects one by main's
 * type when the Manifest reports Concurrency. */
int zrt_sched_main(zrt_main_fn fn);
int zrt_sched_main_nil(void (*fn)(void));
int zrt_sched_main_int(int64_t (*fn)(void));

/* zrt_sched_current returns the running coroutine, or NULL when the scheduler loop
 * itself is running (no coroutine is current). chan.c uses it to park the caller. */
zrt_coro *zrt_sched_current(void);

/* zrt_sched_park marks the current coroutine BLOCKED and returns control to the
 * scheduler; it returns once a zrt_sched_wake puts the coroutine back on the run
 * queue and the scheduler resumes it. A no-op outside a coroutine. It is the single
 * blocking primitive the channel send/recv paths (C2) park on. */
void zrt_sched_park(void);

/* zrt_sched_wake marks a BLOCKED coroutine RUNNABLE and pushes it onto the run queue,
 * so the scheduler resumes it. The channel send/recv/close paths call it to hand a
 * parked counterparty back to the scheduler. */
void zrt_sched_wake(zrt_coro *co);

/* --- concurrency: channels (chan.c) -----------------------------------------
 *
 * A channel is a refcounted, coroutine-shared object with TWO independent counts
 * (Fork-D, distinct from the 1d zrt_ref_hdr): `rc` is the holder count (the last
 * holder frees the object), and `senders` is the count of send-capable handles
 * (when it reaches zero the channel AUTO-CLOSES). A bidirectional or send-only
 * handle bumps both counts; a receive-only handle bumps only rc. Closing merely
 * flips a flag and wakes parked receivers — freeing is still governed by rc, so
 * the object's lifetime (rc) and its channel semantics (senders/closed) are kept
 * separate. The whole struct lives in chan.c; emitted C sees only `zrt_chan *`. */
typedef struct zrt_chan zrt_chan;

/* zrt_chan_new allocates a channel carrying elemsz-byte elements with capacity cap
 * (0 = an unbuffered rendezvous). The new handle is bidirectional: rc = senders = 1. */
zrt_chan *zrt_chan_new(size_t elemsz, size_t cap);

/* zrt_chan_copy / zrt_chan_sender_copy copy a channel handle. A receive-only handle
 * bumps rc only (copy); a bidirectional or send-only handle also bumps senders
 * (sender_copy). Both return the same channel so a copy site is one expression. */
zrt_chan *zrt_chan_copy(zrt_chan *ch);
zrt_chan *zrt_chan_sender_copy(zrt_chan *ch);

/* zrt_chan_release / zrt_chan_sender_release drop a channel handle. sender_release
 * decrements senders first (auto-closing at zero, waking parked receivers); both then
 * decrement rc and free the channel when the last holder leaves. `del ch` lowers to
 * whichever matches the handle's direction. */
void zrt_chan_release(zrt_chan *ch);
void zrt_chan_sender_release(zrt_chan *ch);

/* zrt_chan_send copies *val (elemsz bytes) into ch: it hands off directly to a waiting
 * receiver, else buffers it when there is room, else PARKS the caller on the send queue
 * until a receiver takes it. Sending on a closed channel aborts (a dead-letter is a
 * program error). Send yields no value. */
void zrt_chan_send(zrt_chan *ch, const void *val);

/* zrt_chan_recv receives one element into *out. It returns 0 for a value (the Left of
 * Result[T]) and 1 when the channel is closed and drained (the Right). When empty and
 * open it PARKS the caller on the receive queue until a value arrives or the channel
 * closes. On a Right result zrt_chan_err reports the reason (see below). */
int zrt_chan_recv(zrt_chan *ch, void *out);

/* zrt_chan_err reports why a recv returned 1 (Right): NULL is the ordinary close
 * (StopIteration), a non-NULL string is the message of a sender coroutine that crashed
 * (Fork-C: an unhandled coroutine abort closes the channels it sends on with a crash
 * Err). Valid immediately after a zrt_chan_recv that returned 1. */
const char *zrt_chan_err(zrt_chan *ch);

/* zrt_crash_active reports whether the abort currently unwinding is an unhandled
 * coroutine crash (unwind.c sets it while running the crashing coroutine's cleanup
 * stack). zrt_chan_sender_release reads it so a channel auto-closed by a crashing last
 * sender carries a crash Err rather than the ordinary StopIteration. Always false on a
 * normal path. */
bool zrt_crash_active(void);

/* --- concurrency: select (chan.c) -------------------------------------------
 *
 * `select { arm+ }` (GRAMMAR group 9) is the one multi-way wait. The backend lowers a
 * select to an array of case descriptors (one per recv/send arm; the `done` and `_`
 * arms are passed as the has_done / has_default flags), one zrt_select call to pick and
 * perform a ready arm, then a switch on the returned index to run the chosen arm body.
 * zrt_select owns readiness, fairness, the non-blocking `_`, the all-closed `done`, and
 * parking on every watched channel at once. */

/* zrt_sel_op tags a select case's direction: a receive arm (`(id :=)? <-ch => …`) or a
 * send arm (`ch <- v => …`). */
typedef enum {
	ZRT_SEL_RECV,
	ZRT_SEL_SEND,
} zrt_sel_op;

/* zrt_sel_case is one recv/send arm's descriptor. `ch` is the arm's channel; `val` is
 * the receive target (recv) or the value to send (send). `closed` is an OUTPUT the recv
 * path sets to 1 when the chosen arm fired as the Right of Result[T] (a closed channel
 * with no `done` arm to absorb it), else 0. */
typedef struct {
	zrt_sel_op op;
	zrt_chan  *ch;
	void      *val;
	int        closed;
} zrt_sel_case;

/* zrt_select scans the n cases for a ready one and performs it, returning its index
 * (0..n-1). A recv case is value-ready when its channel has a buffered value or a parked
 * sender; a send case is ready when its channel has room or a parked receiver (sending
 * on a closed channel aborts). Ties among ready cases are broken fairly by a rotating
 * start so no arm starves. When no case is value-ready it resolves in this order:
 *   - has_done and every watched recv channel is closed-and-drained -> ZRT_SEL_DONE;
 *   - no `done` arm and some recv channel is closed -> that recv fires as Right (closed);
 *   - has_default -> ZRT_SEL_DEFAULT (the non-blocking `_`, never parks);
 *   - otherwise PARK on every case's channel at once and re-scan when any wakes it.
 * Note (refines DESIGN-1e §4.2): a closed-and-drained recv channel is NOT treated as
 * independently value-ready when a `done` arm is present — it routes to `done` — so a
 * `select`-loop over producers terminates via `done` once every producer's channel has
 * auto-closed rather than spinning on Right. */
int zrt_select(zrt_sel_case *cases, size_t n, bool has_default, bool has_done);
#define ZRT_SEL_DEFAULT (-1)
#define ZRT_SEL_DONE (-2)

#endif /* ZERGRT_H */
