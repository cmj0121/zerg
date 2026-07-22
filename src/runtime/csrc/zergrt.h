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
} zrt_frame;

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
 * frame->buf with setjmp in its own activation. */
void zrt_handler_push(zrt_frame *frame);

/* zrt_handler_pop unlinks the innermost abort handler. */
void zrt_handler_pop(zrt_frame *frame);

/* zrt_abort unwinds cleanups up to the innermost handler (running every pending
 * defer/drop along the way) and longjmps to it; with no handler it reports the
 * message and exits non-zero. This is the single exit that `raise`, `x!`, an
 * alias violation and OOM all funnel through. */
_Noreturn void zrt_abort(const char *msg);

/* --- minimal sys surface (sys.c) ----------------------------------------- */

/* zrt_report writes a diagnostic line to stderr. The MVP sys surface is just
 * abort-message output; real io is a later phase. */
void zrt_report(const char *msg);

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

#endif /* ZERGRT_H */
