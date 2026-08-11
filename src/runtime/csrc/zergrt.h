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
 * allocator wrapper is the seam a later phase re-targets (a freestanding
 * allocator) without touching emitted code.
 */
#ifndef ZERGRT_H
#define ZERGRT_H

#include <stddef.h>  /* size_t */
#include <stdint.h>  /* int32_t */
#include <stdbool.h> /* bool */
#include <setjmp.h>  /* jmp_buf */

/* --- compile-time assertions --------------------------------------------- */

/* ZRT_STATIC_ASSERT states an invariant the build must hold rather than the test
 * suite. `_Static_assert` is C11, and this runtime is compiled under C99 too —
 * where clang and gcc both still accept the keyword, but only as an extension, so
 * a conforming C99 toolchain is entitled to reject it. The fallback declares an
 * array whose length is negative when the condition is false, which says the same
 * thing in C89 onward; the message rides along in the arm that can carry one. */
#define ZRT_SA_CAT_(a, b) a##b
#define ZRT_SA_CAT(a, b) ZRT_SA_CAT_(a, b)
#if defined(__STDC_VERSION__) && __STDC_VERSION__ >= 201112L
#define ZRT_STATIC_ASSERT(cond, msg) _Static_assert(cond, msg)
#else
#define ZRT_STATIC_ASSERT(cond, msg) \
	typedef char ZRT_SA_CAT(zrt_static_assert_, __LINE__)[(cond) ? 1 : -1]
#endif

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
	size_t      rc;   /* strong refcount; the last holder frees the block. See below. */
	zrt_drop_fn drop; /* run at rc==0 before free (NULL = nothing to drop) */
} zrt_ref_hdr;

/* `rc` is a PLAIN size_t — deliberately not `_Atomic` — and is accessed only through the
 * `__atomic_*` builtins, in ref.c. The distinction is a layout contract and not a style: the
 * BACKEND writes this struct, brace-initializing an immortal cell as
 * `static struct { zrt_ref_hdr h; char b[N]; } = {{ZRT_RC_IMMORTAL, NULL}, …}` (both
 * emitters do), and `_Atomic` would change the type those initializers must satisfy and the
 * dialects this header compiles under. Code outside ref.c that touches `rc` must use the
 * builtins too — fmt.c reaches this header as `(zrt_ref_hdr *)s - 1` and does exactly that,
 * by calling zrt_retain/zrt_release rather than the field. */

/* ZRT_RC_IMMORTAL is the sentinel refcount of a cell that must never be freed: a
 * cell in static storage, whose count is therefore never written by anyone — which is what
 * makes reading it with a RELAXED atomic load sound (ref.c). It is: a
 * value in static storage (a string literal, a constant result such as the "true"/
 * "false" of zrt_display_bool). zrt_retain/zrt_release short-circuit on it, so such a
 * cell can be bound, copied, and "released" at scope exit without ever touching its
 * backing memory. A heap cell keeps a normal count and frees at zero. This is the one
 * mechanism that lets literals and heap strings share one `const char*` ABI (S2). */
#define ZRT_RC_IMMORTAL ((size_t)SIZE_MAX)

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

/* --- list[T]: by-value growable sequence (list.c) ------------------------ */

/* zrt_elem_vt is a list instance's per-element teardown vtable: how to deep-copy
 * one element (dst <- src) and how to drop one. A POD element type carries a NULL
 * vtable (or NULL copy/drop), which selects the raw memcpy fast path. The compiler
 * emits one static vtable per distinct list element type. */
typedef struct {
	void (*copy)(void *dst, const void *src);
	void (*drop)(void *elem);
} zrt_elem_vt;

/* zrt_list is a list's BY-VALUE header: the elements live in one heap buffer `data`
 * of `cap` slots each `elemsz` bytes, `len` of them live, and `vt` teaches the
 * runtime how to copy/drop an element. The header itself is embedded inline in its
 * holder (a local/field/element), so copying/dropping the header is the compiler's
 * job; this runtime owns only the buffer. The layout is INTERNAL (never FFI-frozen).
 *
 * The BUFFER is copy-on-write and holds an atomic refcount just before `data` (see
 * list.c). The HEADER is never shared between holders — that is what makes the rest
 * sound — so every field here may be read and written without synchronization. */
typedef struct {
	uint8_t          *data;
	size_t            len;
	size_t            cap;
	size_t            elemsz;
	const zrt_elem_vt *vt;
} zrt_list;

/* zrt_list_init sets l to an empty list of elemsz-byte elements with the given
 * element vtable (NULL for a POD element). No buffer is allocated until the first
 * push. */
void zrt_list_init(zrt_list *l, size_t elemsz, const zrt_elem_vt *vt);

/* zrt_list_push appends a copy of *elem (elemsz bytes, a raw memcpy — the caller has
 * already produced an owned element) to l, growing the buffer 0->8 then doubling. A
 * grow relocates the live prefix with a bit-move, not vt->copy. */
void zrt_list_push(zrt_list *l, const void *elem);

/* zrt_list_at returns a pointer to element i's slot to be WRITTEN through, aborting
 * ("index out of range") when i is past the end — the `xs[i]` force path (IndexError).
 * It unshares first, so the pointer names storage this list alone owns.
 *
 * The WRITE one keeps the plain name on purpose. Copy-on-write needs a caller to say
 * which it wants, and a caller that does not say must get the safe answer: the Go seed
 * shares this runtime, has no notion of the split, and would otherwise have started
 * writing into buffers its own `zrt_list_copy` had just shared. The cost of the safe
 * default is a duplicate on a read; the cost of the other one is silent aliasing in a
 * client nobody remembered to update. */
void *zrt_list_at(zrt_list *l, size_t i);

/* zrt_list_at_ref is the same slot for READING, and does not unshare — so nothing may be
 * written through it. It is the whole point of copy-on-write: a read that unshared would
 * duplicate every buffer anybody ever indexed, which is the cost this exists to avoid. */
void *zrt_list_at_ref(zrt_list *l, size_t i);

/* zrt_list_set overwrites element i: it drops the old element (vt->drop) then
 * memcpys *elem in. Aborts on a bad index, like zrt_list_at. Unshares. */
void zrt_list_set(zrt_list *l, size_t i, const void *elem);

/* zrt_list_len returns the live element count. */
size_t zrt_list_len(const zrt_list *l);

/* zrt_list_get returns element i's slot pointer, or NULL when i is out of range (no
 * abort) — the checked `.get(i)` path. */
void *zrt_list_get(zrt_list *l, size_t i);

/* zrt_list_copy gives dst its own view of src's elements. It is the value-semantics
 * copy the compiler inserts wherever a list is bound/passed/returned by value, and it
 * is O(1): the buffer is SHARED and its refcount incremented, and the elements are
 * duplicated later, by whichever holder writes first (zrt_list_unshare). Nothing
 * observable changes — a write through one name is never seen through another. */
void zrt_list_copy(zrt_list *dst, const zrt_list *src);

/* zrt_list_slice fills dst with src's elements [lo, hi), each duplicated the way a copy
 * duplicates one. Aborts (IndexError) when the range is not within src. It is a function
 * rather than a loop the compiler emits because the duplication rule — vt->copy, or a
 * memcpy for a POD — is the one this file already states twice. */
void zrt_list_slice(zrt_list *dst, const zrt_list *src, size_t lo, size_t hi);

/* zrt_list_unshare gives l a buffer no other holder has, so a caller may write into it
 * directly. Every mutating entry point above calls it; it is exported for the compiler
 * to reach a slot it writes through by other means. A sole owner pays one atomic load. */
void zrt_list_unshare(zrt_list *l);

/* zrt_list_drop releases this holder's reference to the buffer, leaving an empty
 * header. The holder that brings the refcount to zero drops every live element
 * (vt->drop) and frees. It is the scope-exit teardown the compiler schedules. */
void zrt_list_drop(zrt_list *l);

/* --- map[K, V]: by-value insertion-ordered hash table (map.c) ------------ */

/* zrt_map_vt is a map instance's per-key/value teardown+hash vtable: how to hash and
 * compare a key, and how to deep-copy / drop one key and one value. A POD key/value
 * carries NULL copy/drop (the memcpy fast path). Built-in `hash`/`eq` are provided for
 * int (zrt_hash_int/zrt_eq_int) and str (zrt_hash_str/zrt_eq_str) keys; the compiler
 * emits one static vtable per distinct map instance. */
typedef struct {
	size_t (*hash)(const void *key);
	bool   (*eq)(const void *a, const void *b);
	void   (*key_copy)(void *dst, const void *src);
	void   (*key_drop)(void *key);
	void   (*val_copy)(void *dst, const void *src);
	void   (*val_drop)(void *val);
} zrt_map_vt;

/* zrt_map is a map's BY-VALUE header. `entries` is an insertion-order array of `cap`
 * records each `entrysz = sizeof(size_t)+keysz+valsz` bytes, laid out
 * `[ hash | key | val ]`, `len` of them live — walked in order for iteration. `buckets`
 * is a linear-probe hash index of `nbuckets` slots, each a 1-based entry index (0 =
 * empty). The header is embedded inline in its holder, so copying/dropping it is the
 * compiler's job; this runtime owns only the two heap buffers. Layout is INTERNAL
 * (never FFI-frozen). */
typedef struct {
	uint8_t          *entries;
	size_t            len;
	size_t            cap;
	int64_t          *buckets;
	size_t            nbuckets;
	size_t            keysz;
	size_t            valsz;
	size_t            entrysz;
	const zrt_map_vt *vt;
} zrt_map;

/* zrt_map_init sets m to an empty map of keysz/valsz-byte keys/values with the given
 * vtable. No storage is allocated until the first insert. */
void zrt_map_init(zrt_map *m, size_t keysz, size_t valsz, const zrt_map_vt *vt);

/* zrt_map_get returns a pointer to the value stored under key, or NULL on a miss — the
 * checked `.get(k)` path. */
void *zrt_map_get(zrt_map *m, const void *key);

/* zrt_map_index returns the value pointer for key, aborting ("key not found", a
 * KeyError) on a miss — the force path `m[k]`. */
void *zrt_map_index(zrt_map *m, const void *key);

/* zrt_map_has reports whether key is present — the `k in m` membership test. */
bool zrt_map_has(zrt_map *m, const void *key);

/* zrt_map_set inserts or updates key -> val (both already copied in by the emitter).
 * On a hit it drops the old value, stores the new one, and drops the surplus incoming
 * key; on a miss it appends a new insertion-order entry, rehashing the bucket index
 * when the 0.75 load factor is crossed. */
void zrt_map_set(zrt_map *m, const void *key, const void *val);

/* zrt_map_len returns the live entry count. */
size_t zrt_map_len(const zrt_map *m);

/* zrt_map_key_at / zrt_map_val_at return the key/value slot of the i-th entry in
 * INSERTION order (0..len-1) — the walk a `for k in m` iterates. */
void *zrt_map_key_at(zrt_map *m, size_t i);
void *zrt_map_val_at(zrt_map *m, size_t i);

/* zrt_map_copy deep-copies src into dst: fresh storage, the entry bits bit-copied, then
 * per-entry vt->key_copy / vt->val_copy for any non-POD side. It is the value-semantics
 * copy the compiler inserts wherever a map is bound/passed/returned by value. */
void zrt_map_copy(zrt_map *dst, const zrt_map *src);

/* zrt_map_drop drops every live key/value (vt->key_drop / vt->val_drop) and frees both
 * buffers, leaving an empty header. It is the scope-exit teardown for a map. */
void zrt_map_drop(zrt_map *m);

/* zrt_hash_int / zrt_eq_int are the built-in Hash for an int key (a splitmix64 mix of
 * the int64 value; equality by value). The key slot holds an int64_t. */
size_t zrt_hash_int(const void *key);
bool   zrt_eq_int(const void *a, const void *b);

/* zrt_hash_str / zrt_eq_str are the built-in Hash for a str key (FNV-1a over the bytes
 * to the NUL; equality by strcmp). The key slot holds a `const char *`. */
size_t zrt_hash_str(const void *key);
bool   zrt_eq_str(const void *a, const void *b);

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
/* zrt_err_kind is the FIXED, built-in error taxonomy (docs/code/errors.md, GRAMMAR group
 * 8). Users choose from these named kinds but cannot define their own this phase. The
 * kind lets a `guard`/`?`-recovered Err be distinguished at the language surface (the
 * `is <Kind>` test lowers to `err.kind == <this>`), so a runtime abort ("ValueError:
 * …", "IOError: …") and a `raise ValueError("…")` reify to the same nameable kind.
 * These integer values are MIRRORED by the compiler (internal/sema/builtins.go); keep
 * the two in lockstep. ZRT_ERR_NONE is the untyped/generic Err a bare abort carries. */
enum {
	ZRT_ERR_NONE           = 0,
	ZRT_ERR_VALUE          = 1, /* ValueError */
	ZRT_ERR_OVERFLOW       = 2, /* OverflowError */
	ZRT_ERR_IO             = 3, /* IOError */
	ZRT_ERR_ENCODING       = 4, /* EncodingError */
	ZRT_ERR_INDEX          = 5, /* IndexError */
	ZRT_ERR_KEY            = 6, /* KeyError */
	ZRT_ERR_DEADLOCK       = 7, /* DeadlockError (docs/code/coroutine.md) */
	ZRT_ERR_SEND_ON_CLOSED = 8, /* SendOnClosedError */
	ZRT_ERR_STOP_ITERATION = 9, /* StopIteration: the end-of-stream sentinel, not a failure */
	ZRT_ERR_DIVZERO        = 10, /* DivideByZeroError (docs/core/types.md) */
};

typedef struct zrt_err {
	const char     *msg;
	struct zrt_err *cause;
	int             kind; /* a zrt_err_kind (ZRT_ERR_*); ZRT_ERR_NONE for a generic Err */
} zrt_err;

typedef struct {
	zrt_cleanup *stack; /* the cleanup(defer) stack (was the file-static g_stack) */
	size_t       len;   /* its live height (was g_len) */
	size_t       cap;   /* its allocated capacity (was g_cap) */
	zrt_frame   *handler; /* the innermost abort handler (was g_handler) */
	zrt_err      taken;   /* the in-flight Err an abort/raise carries; a `guard` reads
	                       * it with zrt_taken_err on the abort landing (Decision D). */

	/* crashing is true only while THIS coroutine unwinds an unhandled crash, which is
	 * what makes a channel auto-closed by its last sender carry a crash Err rather than
	 * a clean end. It lives in the bundle, not beside it: a coroutine MIGRATES, and the
	 * unwind that sets this runs cleanups that can hand the CPU over, so a flag held per
	 * WORKER is left behind on the thread the crash started on. */
	bool         crashing;
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

/* zrt_abort_kind is zrt_abort carrying a built-in error KIND (a ZRT_ERR_* value), so a
 * guard-recovered intrinsic abort (int-parse ValueError, a checked-conversion
 * OverflowError, io IOError, the str bridge's EncodingError, a bounds IndexError/
 * KeyError) reifies to the matching nameable kind. `zrt_abort(msg)` is the ZRT_ERR_NONE
 * case. */
_Noreturn void zrt_abort_kind(int kind, const char *msg);

/* --- Err value + carrying abort (unwind.c, Decision D) -------------------- */

/* zrt_err_new builds an Err with the given message, no cause, and the generic kind
 * (ZRT_ERR_NONE). */
zrt_err zrt_err_new(const char *msg);

/* zrt_err_new_kind builds an Err with a built-in KIND (a ZRT_ERR_* value) and message,
 * no cause — the value a `raise ValueError("…")` carries. */
zrt_err zrt_err_new_kind(int kind, const char *msg);

/* zrt_err_chain attaches a cause to an Err ALREADY BUILT: `raise e from c` records c
 * so a handler can walk the chain, and e keeps everything else it is — its KIND above
 * all. Chaining used to go through a message-only constructor, so `raise IOError("x")
 * from c` came out kind-less and an `e is IOError` on the far side answered false. The
 * cause is copied to the heap (the caller's is a stack temporary), leaked for the MVP
 * like every other box. */
zrt_err zrt_err_chain(zrt_err e, zrt_err cause);

/* zrt_err_in answers `e in Kind` — membership in that kind's subtree of the built-in taxonomy:
 * an OverflowError and an EncodingError are both kinds of ValueError (docs/code/errors.md).
 * `is` is identity and needs no call. */
int zrt_err_in(zrt_err e, int kind);

/* zrt_err_with_cause is zrt_err_chain over a bare message — the kind-less Err a
 * `raise "…" from c` carries. */
zrt_err zrt_err_with_cause(const char *msg, zrt_err cause);

/* zrt_raise_err aborts carrying an Err VALUE: it stashes e (so a `guard`/`?` reads
 * it back with zrt_taken_err), reports e.msg, then unwinds and longjmps exactly as
 * zrt_abort. `raise e`, `x!`, and the propagate paths funnel through here. */
_Noreturn void zrt_raise_err(zrt_err e);

/* zrt_taken_err returns the Err the current abort/raise carried, read on a `guard`
 * setjmp!=0 landing. It is an empty Err (msg NULL) when nothing was stashed. */
zrt_err zrt_taken_err(void);

/* --- checked integer ARITHMETIC (docs/core/types.md) -----------------------------
 *
 * `+`, `-`, `*` RAISE on overflow; `/` and `%` raise on a zero divisor and follow the
 * EUCLIDEAN definition (the remainder is never negative); a shift by a distance outside
 * the type's width raises. The `%`-suffixed `+%`, `-%`, `*%` are the wrapping forms and go
 * on emitting the bare C operator — that pairing is the whole point of having two.
 *
 * Every one of these was the plain C operator, which is not merely a different answer: a
 * signed overflow, a division by zero and a shift past the width are all UNDEFINED
 * BEHAVIOUR in C. `+` and `+%` emitted identical code, so a program that chose the checked
 * form got the wrapping one.
 *
 * `static inline` in the header, over `__builtin_*_overflow`, so a check costs an
 * add-and-branch-on-overflow rather than a call — the compiler builds itself with these,
 * and an out-of-line call per arithmetic operator would be felt.
 *
 * There is no fallback for a host without the builtins, because there is no such host
 * here: sys.c and thread_pthread.c already use __atomic_* unguarded, so a compiler that
 * cannot build these cannot build the runtime at all. A guarded second spelling of the
 * overflow rule would be code no configuration ever compiles.
 */

/* One message per fault, named once: each helper below is `static inline` in a header, so
 * a repeated literal is re-emitted per translation unit as well as re-typed per edit. */
#define ZRT_MSG_ADD_OVERFLOW "OverflowError: integer addition overflowed"
#define ZRT_MSG_SUB_OVERFLOW "OverflowError: integer subtraction overflowed"
#define ZRT_MSG_MUL_OVERFLOW "OverflowError: integer multiplication overflowed"
#define ZRT_MSG_NEG_OVERFLOW "OverflowError: integer negation overflowed"
#define ZRT_MSG_DIV_OVERFLOW "OverflowError: integer division overflowed"
#define ZRT_MSG_SHIFT_WIDTH  "OverflowError: shift distance outside the type width"
#define ZRT_MSG_DIV_ZERO     "DivideByZeroError: division by zero"
#define ZRT_MSG_MOD_ZERO     "DivideByZeroError: remainder by zero"

static inline int64_t zrt_add_i64(int64_t a, int64_t b) {
	int64_t r;
	if (__builtin_add_overflow(a, b, &r)) {
		zrt_abort_kind(ZRT_ERR_OVERFLOW, ZRT_MSG_ADD_OVERFLOW);
	}
	return r;
}

static inline int64_t zrt_sub_i64(int64_t a, int64_t b) {
	int64_t r;
	if (__builtin_sub_overflow(a, b, &r)) {
		zrt_abort_kind(ZRT_ERR_OVERFLOW, ZRT_MSG_SUB_OVERFLOW);
	}
	return r;
}

static inline int64_t zrt_mul_i64(int64_t a, int64_t b) {
	int64_t r;
	if (__builtin_mul_overflow(a, b, &r)) {
		zrt_abort_kind(ZRT_ERR_OVERFLOW, ZRT_MSG_MUL_OVERFLOW);
	}
	return r;
}

/* `/` and `%` are EUCLIDEAN (docs/core/types.md): the remainder is never negative, so
 * `0 <= a % b < |b|` and `a == (a / b) * b + a % b` holds for every sign — which is what
 * makes `a % n` a valid index or bucket whatever the sign of `a`.
 *
 * C truncates toward zero instead, so it answered `-7 % 3 == -1` where the language says
 * `2`, and `-7 / 3 == -2` where the language says `-3`. That difference never announces
 * itself: it turns a modulo into a negative index.
 *
 * The correction is one branch on a remainder that C already computed, and it is skipped
 * whenever the remainder came out non-negative — the common case, which is every loop over
 * a non-negative counter.
 *
 * A zero divisor and `INT64_MIN / -1` are both undefined in C, and both are checked before
 * the division rather than after. */
static inline int64_t zrt_div_i64(int64_t a, int64_t b) {
	if (b == 0) {
		zrt_abort_kind(ZRT_ERR_DIVZERO, ZRT_MSG_DIV_ZERO);
	}
	if (a == INT64_MIN && b == -1) {
		zrt_abort_kind(ZRT_ERR_OVERFLOW, ZRT_MSG_DIV_OVERFLOW);
	}
	int64_t q = a / b;
	if (a % b < 0) {
		q = (b > 0) ? q - 1 : q + 1;
	}
	return q;
}

static inline int64_t zrt_mod_i64(int64_t a, int64_t b) {
	if (b == 0) {
		zrt_abort_kind(ZRT_ERR_DIVZERO, ZRT_MSG_MOD_ZERO);
	}
	if (a == INT64_MIN && b == -1) {
		return 0; /* the quotient overflows; the remainder is exactly zero */
	}
	int64_t r = a % b;
	if (r < 0) {
		r += (b > 0) ? b : -b;
	}
	return r;
}

/* A shift by a distance OUTSIDE the type's width raises (docs/core/types.md). In C both
 * ends are undefined: `1 << 64` on a 64-bit type, and any negative distance.
 *
 * The width is passed by the caller because it is the SOURCE type's, not the helper's: a
 * `byte` shifts within 8 even though the value travels here as an i64.
 *
 * `>>` is arithmetic on a signed operand and logical on an unsigned one — the type's sign
 * decides, which is why there is no separate logical-shift operator. C already does exactly
 * that for the C type the value arrives in, so the shift itself is left to it. */
static inline int64_t zrt_shl_i64(int64_t a, int64_t n, int64_t width) {
	if (n < 0 || n >= width) {
		zrt_abort_kind(ZRT_ERR_OVERFLOW, ZRT_MSG_SHIFT_WIDTH);
	}
	/* the shift is done on the UNSIGNED bit pattern: a left shift that moves bits into or
	 * past the sign is undefined on a signed operand in C, and wrapping is what a bit-level
	 * operator is for once the distance itself has been checked */
	return (int64_t)((uint64_t)a << (uint64_t)n);
}

static inline int64_t zrt_shr_i64(int64_t a, int64_t n, int64_t width) {
	if (n < 0 || n >= width) {
		zrt_abort_kind(ZRT_ERR_OVERFLOW, ZRT_MSG_SHIFT_WIDTH);
	}
	return a >> n;
}

/* The WRAPPING three — `+%`, `-%`, `*%`. They are the operators whose overflow is the point,
 * and a plain C `+` on two signed operands is UNDEFINED on exactly that input: the compiler
 * is entitled to assume it never happens, and UBSan reports it. The arithmetic is done on the
 * unsigned bit pattern, where wrapping is defined as modulo 2^64, and cast back — which is
 * what `+%` means. The conversion back is implementation-defined rather than undefined, and
 * every target this compiler emits for is two's complement. */
static inline int64_t zrt_wrap_add(int64_t a, int64_t b) {
	return (int64_t)((uint64_t)a + (uint64_t)b);
}

static inline int64_t zrt_wrap_sub(int64_t a, int64_t b) {
	return (int64_t)((uint64_t)a - (uint64_t)b);
}

static inline int64_t zrt_wrap_mul(int64_t a, int64_t b) {
	return (int64_t)((uint64_t)a * (uint64_t)b);
}

/* unary `-`. Its one overflow is the type's minimum, which has no positive counterpart. */
static inline int64_t zrt_neg_i64(int64_t a) {
	if (a == INT64_MIN) {
		zrt_abort_kind(ZRT_ERR_OVERFLOW, ZRT_MSG_NEG_OVERFLOW);
	}
	return -a;
}

/* An UNSIGNED type has no negative values at all, so ZERO is the only one it can negate.
 * A narrow unsigned reaches that answer through the i64 helper and the narrowing check
 * after it; `uint` cannot, because its top half is not int64's — which left it taking C's
 * wrap, the very thing `-%` exists to ask for explicitly. */
static inline uint64_t zrt_neg_u64(uint64_t a) {
	if (a != 0) {
		zrt_abort_kind(ZRT_ERR_OVERFLOW, ZRT_MSG_NEG_OVERFLOW);
	}
	return 0;
}

/* The same seven for an UNSIGNED 64-bit operand. `uint` is not int64's range, so
 * routing it through the signed helpers would raise on values that are perfectly
 * representable; and unsigned division is already Euclidean, because neither operand
 * can be negative. What is left is the zero divisor and the wrap. */

static inline uint64_t zrt_add_u64(uint64_t a, uint64_t b) {
	uint64_t r;
	if (__builtin_add_overflow(a, b, &r)) {
		zrt_abort_kind(ZRT_ERR_OVERFLOW, ZRT_MSG_ADD_OVERFLOW);
	}
	return r;
}

static inline uint64_t zrt_sub_u64(uint64_t a, uint64_t b) {
	uint64_t r;
	if (__builtin_sub_overflow(a, b, &r)) {
		zrt_abort_kind(ZRT_ERR_OVERFLOW, ZRT_MSG_SUB_OVERFLOW);
	}
	return r;
}

static inline uint64_t zrt_mul_u64(uint64_t a, uint64_t b) {
	uint64_t r;
	if (__builtin_mul_overflow(a, b, &r)) {
		zrt_abort_kind(ZRT_ERR_OVERFLOW, ZRT_MSG_MUL_OVERFLOW);
	}
	return r;
}

static inline uint64_t zrt_div_u64(uint64_t a, uint64_t b) {
	if (b == 0) {
		zrt_abort_kind(ZRT_ERR_DIVZERO, ZRT_MSG_DIV_ZERO);
	}
	return a / b;
}

static inline uint64_t zrt_mod_u64(uint64_t a, uint64_t b) {
	if (b == 0) {
		zrt_abort_kind(ZRT_ERR_DIVZERO, ZRT_MSG_MOD_ZERO);
	}
	return a % b;
}

static inline uint64_t zrt_shl_u64(uint64_t a, uint64_t n) {
	if (n >= 64) {
		zrt_abort_kind(ZRT_ERR_OVERFLOW, ZRT_MSG_SHIFT_WIDTH);
	}
	return a << n;
}

static inline uint64_t zrt_shr_u64(uint64_t a, uint64_t n) {
	if (n >= 64) {
		zrt_abort_kind(ZRT_ERR_OVERFLOW, ZRT_MSG_SHIFT_WIDTH);
	}
	return a >> n;
}

/* --- checked primitive conversions (conv.c, docs/core/types.md) ------------------
 *
 * `T(x)` converts by re-construction; a narrowing conversion whose value does not
 * fit the target raises OverflowError. Each helper aborts through zrt_abort, so
 * `guard { byte(x) }` catches it and yields a Result. The compiler passes the
 * target's bounds and calls one of these ONLY when the source range is not provably
 * inside the target range — a widening conversion stays a plain C cast. */
int64_t  zrt_conv_i_from_i(int64_t v, int64_t lo, int64_t hi);
uint64_t zrt_conv_u_from_i(int64_t v, uint64_t hi);
int64_t  zrt_conv_i_from_u(uint64_t v, int64_t hi);
uint64_t zrt_conv_u_from_u(uint64_t v, uint64_t hi);

/* float -> integer truncates toward zero and raises when the TRUNCATED value is out
 * of range, or the input is NaN/+-Inf. lo/hi are the target's bounds as doubles; the
 * test is over the open interval (lo-1, hi+1), so 255.7 converts to 255 while 256.0
 * still raises. */
int64_t  zrt_conv_i_from_f(double v, double lo, double hi);
uint64_t zrt_conv_u_from_f(double v, double hi);

/* A `rune` is A SINGLE VALID UNICODE CODE POINT (docs/core/types.md), and that is not a
 * range: U+D800..U+DFFF are surrogates, which exist only inside UTF-16 and are not
 * characters. So it is the one scalar whose bound a (lo, hi) pair cannot state, and the
 * one that needs a predicate of its own.
 *
 * It is declared here rather than left inside str.c because THREE callers need the same
 * answer: the UTF-8 validator, the encoder, and `rune(n)`. It was written twice and
 * reachable from neither the type system nor a program. */
static inline int zrt_is_rune(int64_t cp) {
	return cp >= 0 && cp <= 0x10FFFF && !(cp >= 0xD800 && cp <= 0xDFFF);
}

int32_t zrt_conv_rune(int64_t v);

/* The UTF-8 encoder, shared for the same reason zrt_is_rune is: `str(runes)` builds a
 * whole string out of it and `{n:c}` builds one character, and two copies of "which code
 * points a str can hold" is two answers waiting to disagree. zrt_utf8_len answers 0 for a
 * code point no str can hold — a surrogate, out of range, or U+0000, whose byte is a NUL —
 * and zrt_utf8_encode then writes nothing. */
int zrt_utf8_len(int64_t cp);
int zrt_utf8_encode(int64_t cp, char *out);

/* `a // b` is floor division whose result is always an `int` (docs/core/types.md). For
 * two integers that IS zrt_div_i64 — the language has one integer division and `//` is
 * the spelling that says so. The float form is the one that needs a helper: it divides
 * as a double, applies the same Euclidean correction, and lands in int64 through the
 * same range check `int(x)` is, so a quotient that does not fit raises rather than
 * being undefined. NaN and the infinities fail those comparisons and abort on it too. */

static inline int64_t zrt_floordiv_f(double a, double b) {
	if (b == 0.0) {
		zrt_abort_kind(ZRT_ERR_DIVZERO, ZRT_MSG_DIV_ZERO);
	}
	/* zrt_conv_i_from_f truncates toward zero and range-checks, which is exactly the
	 * quotient C's `/` would give; the correction below turns that into the Euclidean
	 * one. Doing it AFTER the conversion keeps the arithmetic in int64, where a step of
	 * one is exact — a double near the top of the range cannot represent `f - 1`. */
	int64_t t = zrt_conv_i_from_f(a / b, -9223372036854775808.0, 9223372036854775807.0);
	if (a - (double)t * b < 0.0) {
		t = (b > 0.0) ? zrt_sub_i64(t, 1) : zrt_add_i64(t, 1);
	}
	return t;
}

/* --- str <-> list bridge (str.c, docs/code/collections.md) ----------------------
 *
 * A str bridges to a list[byte] (raw octets) or list[rune] (code points) for scanning
 * and editing; going TO a str validates the str invariant (valid UTF-8, no NUL) and
 * raises EncodingError on violation, going FROM a str never fails. */
zrt_list zrt_str_bytes(const char *s);
zrt_list zrt_str_runes(const char *s);
/* zrt_str_dup copies a C string into a managed cell. It is how an `Err`'s message is read:
 * a zrt_err's msg may be a managed payload, an immortal literal cell, or a plain string
 * literal the runtime raised with, and only a copy is correct for all three. */
const char *zrt_str_dup(const char *s);

const char *zrt_str_from_bytes(zrt_list bytes);
const char *zrt_str_from_runes(zrt_list runes);

/* zrt_parse_int / zrt_parse_uint / zrt_parse_float are `int(s)` / `uint(s)` / `float(s)`
 * for a str: checked parses that raise ValueError on a malformed string and OverflowError
 * out of range (str.c). */
int64_t  zrt_parse_int(const char *s);
uint64_t zrt_parse_uint(const char *s);
double   zrt_parse_float(const char *s);

/* Whole-file read floor (sys.c): thin syscall leaves the stdlib `io.read_file` drives
 * from pure Zerg. zrt_open returns an fd (IOError on a failed open); zrt_read_fd reads
 * one up-to-4096-byte list[byte] chunk (empty = end of input, IOError on error);
 * zrt_close closes the fd. */
int64_t zrt_open(const char *path);
zrt_list zrt_read_fd(int64_t fd);
int64_t  zrt_close(int64_t fd);

/* Clock leaves the stdlib `time` module lowers onto (sys.c): zrt_time_unix is wall-clock
 * seconds since the Unix epoch; zrt_time_mono is a monotonic nanosecond reading, valid
 * only as a difference. */
int64_t zrt_time_unix(void);
int64_t zrt_time_mono(void);

/* Process & platform leaves the stdlib `os` module lowers onto (sys.c). zrt_platform /
 * zrt_arch return the compile-time TARGET OS / arch as a fresh str cell; zrt_getenv
 * returns a COPY of an env var's value ("" when unset) and zrt_has_env tells set from
 * unset; zrt_exit terminates the process (does not return). */
const char *zrt_platform(void);
const char *zrt_arch(void);
/* zrt_exe_path is the running executable's path, or "" when the host will not say — how a
 * toolchain finds the files it was installed beside. See sys.c for why argv[0] is not it. */
const char *zrt_exe_path(void);
const char *zrt_getenv(const char *key);
bool        zrt_has_env(const char *key);
void        zrt_exit(int64_t code);

/* Filesystem-write leaves (sys.c): the stdlib `io.write_file` drives zrt_open_write (an
 * fd, IOError on fail), zrt_write_bytes (write all of a borrowed list[byte], IOError on
 * fail), and zrt_close from pure Zerg; the `fs` module uses zrt_exists / zrt_remove. */
int64_t zrt_open_write(const char *path);
void    zrt_write_bytes(int64_t fd, zrt_list bytes);
bool    zrt_exists(const char *path);
void    zrt_remove(const char *path);
int64_t zrt_exec(zrt_list argv);

/* zrt_proc_spawn starts a child and returns its pid without waiting; zrt_proc_wait
 * collects one. (The coroutine zrt_spawn above is a different thing entirely.)
 * Together they are zrt_exec taken apart, so a caller can have several children running
 * — which is how a build compiles its units in parallel. */
int64_t zrt_proc_spawn(zrt_list argv);
int64_t zrt_proc_wait(int64_t pid);

/* zrt_mkdir creates a directory and any missing parents (`mkdir -p`), reporting whether
 * it exists afterwards. Creating one that is already there is success. */
bool zrt_mkdir(const char *path);

/* zrt_listdir returns the sorted entry names directly under path as a list[str] (empty
 * when path is not a readable directory). Sorted because a compiler that resolves a
 * directory module through it must emit the same C on every machine. */
zrt_list zrt_listdir(const char *path);

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

/* --- Atomic[int] cell operations (sys.c, Phase 1f U2) ----------------------
 *
 * The stdlib `atomic` module lowers onto these: an Atomic[int] is a `Ref[int]`
 * box (a shared, refcounted heap cell) whose int64 payload these functions read
 * and write with sequential-consistency ordering. Under the 1e N:1 cooperative
 * scheduler there is no preemption, so atomicity holds even without hardware
 * fences; the SC ops keep the API correct for a future M:N scheduler with no
 * change to the Zerg surface. Each takes the payload pointer (from
 * zrt_ref_payload) so a copy of the box — shared across `spawn` — names the same
 * cell.
 *
 * THE RULE FOR EVERY COUNT IN THIS RUNTIME, since there are three of them and they do not
 * share a mechanism: a count two threads can reach is either ATOMIC, or guarded by a lock
 * named where the count is declared. There is no third option, and a plain `++` on a shared
 * count is the bug this rule exists to keep out — it survived in ref.c for as long as it did
 * because no gate looked. The three:
 *
 *   - the Ref header's `rc` (ref.c) — atomic, `__atomic_*` on a plain size_t, see above
 *   - the copy-on-write list buffer's `rc` (list.c) — atomic, `zrt_atomic_add` on an int64_t
 *   - a channel's `rc` (chan.c) — plain, and correct: every access is under `ch->lock`, which
 *     it must be anyway because the decision to free is made inside the lock and acted on
 *     outside it (chan_free destroys the lock it would be holding)
 *
 * They are deliberately not unified. The widths differ, only one has a sentinel, and only
 * list.c reads its count for a decision other than freeing (`rc == 1` is its COW fast path).
 * A shared primitive would be adopted by one of the three. */

/* zrt_atomic_load returns *p read with sequential-consistency ordering. */
int64_t zrt_atomic_load(int64_t *p);

/* zrt_atomic_store writes v to *p (SC) and returns the value stored. */
int64_t zrt_atomic_store(int64_t *p, int64_t v);

/* zrt_atomic_swap stores v into *p (SC) and returns the previous value. */
int64_t zrt_atomic_swap(int64_t *p, int64_t v);

/* zrt_atomic_add adds n to *p (SC) and returns the previous value. */
int64_t zrt_atomic_add(int64_t *p, int64_t n);

/* zrt_atomic_cas compares *p with expect and, if equal, stores desired (SC),
 * returning true on success and false otherwise. */
bool zrt_atomic_cas(int64_t *p, int64_t expect, int64_t desired);

/* --- text rendering: display() / Format / f-string join (fmt.c, Phase 1f) ---
 *
 * The built-in `display()` and per-type `Format` (`:spec`) impls the compiler lowers
 * an f-string onto, plus the concatenation that joins the lowered parts. Every result
 * is a fresh heap string (leaked for the MVP); the compiler only reads them. The spec
 * mirrors Python's `[[fill]align][sign][#][0][width][.prec][type]`; a field the MVP
 * does not model is ignored (there is no runtime error channel — the desugar is at
 * compile time). */

/* zrt_str_concat returns a fresh heap string holding a followed by b (a NULL operand
 * is the empty string). It joins the parts of a lowered f-string. */
const char *zrt_str_concat(const char *a, const char *b);

/* zrt_str_retain / zrt_str_release are the `const char*`-typed refcount wrappers for a
 * MANAGED str value (S2): a managed str IS the payload of a `[zrt_ref_hdr | bytes,'\0']`
 * cell, so the header is recovered by `((zrt_ref_hdr*)p) - 1`. retain bumps the count and
 * returns the same pointer (so a copy site is one expression); release drops it, freeing
 * the cell at zero. A string LITERAL is an immortal cell, so both are no-ops on it. These
 * are emitted only for a program the compiler marks str-managed; an unmanaged program
 * keeps `str` a bare `const char*` and never calls them. */
const char *zrt_str_retain(const char *s);
void        zrt_str_release(const char *s);

/* zrt_str_elem_vt is the list-element vtable for a `list[str]` whose elements are managed
 * str cells: copy retains, drop releases. zrt_os_args builds the command-line args list
 * with it so the list owns and frees its argv cells. Declared after zrt_elem_vt. */
extern const zrt_elem_vt zrt_str_elem_vt;

/* zrt_display_* render a value's human `display()` text (the f-string `{x}` default
 * and the `!s` conversion). A `str` displays as itself, so it has no entry here. */
const char *zrt_display_int(int64_t v);
const char *zrt_display_uint(uint64_t v);
const char *zrt_display_float(double v);
const char *zrt_display_bool(bool v);

/* zrt_fmt_* render a value under a `:spec` (the f-string `{x:spec}` hole). Numbers
 * read sign / base (d/b/o/x/X/c) / '#' prefix / zero-pad / width / (float) precision;
 * a string reads width / align / precision (truncation). */
const char *zrt_fmt_int(int64_t v, const char *spec);
const char *zrt_fmt_uint(uint64_t v, const char *spec);
const char *zrt_fmt_float(double v, const char *spec);
const char *zrt_fmt_str(const char *s, const char *spec);

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

/* zrt_main_fn is the shape of a `fn main() -> Result[nil]` after lowering;
 * zrt_main_args_fn is its `fn main(args: list[str]) -> Result[nil]` counterpart. */
typedef zrt_result_nil (*zrt_main_fn)(void);
typedef zrt_result_nil (*zrt_main_args_fn)(zrt_list);

/* zrt_run is the C entry shim for a `fn main() -> Result[nil]` program: it
 * installs the root abort handler (its setjmp lives in this function's own
 * activation, so the longjmp target stays valid), runs main under a root scope,
 * unwinds top-level defers, and maps the outcome to a process exit code (0 Ok,
 * 1 Err or abort). Value-only programs do not use this shim. */
int zrt_run(zrt_main_fn fn);

/* zrt_run_args is zrt_run for a main that takes the command-line args list. */
int zrt_run_args(zrt_main_args_fn fn, zrt_list args);

/* zrt_main_run_nil / _int / _args are zrt_run for the three entry shapes that answer no
 * Result — `fn main()`, `fn main() -> int` and `fn main(args)`. Each arms the same root
 * handler, so an uncaught abort unwinds the top-level cleanup stack (running every pending
 * `defer`) and answers 1; the _int form carries main's own value out when it returns
 * normally. The function they take is the emitter's entry WRAPPER, which is what puts the
 * top-level constant initialization and every `init()` inside the handler too. */
int zrt_main_run_nil(void (*fn)(void));
int zrt_main_run_int(int64_t (*fn)(void));
int zrt_main_run_args(void (*fn)(zrt_list), zrt_list args);
int zrt_main_run_args_int(int64_t (*fn)(zrt_list), zrt_list args);

/* zrt_os_args builds the `list[str]` a `fn main(args: list[str])` receives from the C
 * entry's argc/argv, skipping the program name (argv[0]). */
zrt_list zrt_os_args(int argc, char **argv);

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

/* ZRT_THREAD_LOCAL marks a variable each OS thread gets its own copy of — the
 * scheduler's "which coroutine is running here" and "where do I swap back to" are
 * per-worker once there is more than one worker. With thread_none.c there is only
 * ever one thread, so the qualifier is empty and the variable is an ordinary global,
 * which is exactly what it was before M:N. */
#if defined(_MSC_VER)
#define ZRT_THREAD_LOCAL __declspec(thread)
#elif defined(__STDC_VERSION__) && __STDC_VERSION__ >= 201112L && !defined(__STDC_NO_THREADS__)
#define ZRT_THREAD_LOCAL _Thread_local
#elif defined(__GNUC__) || defined(__clang__)
#define ZRT_THREAD_LOCAL __thread
#else
#define ZRT_THREAD_LOCAL /* single-threaded host: an ordinary global */
#endif

typedef struct zrt_thread { void *slots[4]; } zrt_thread;
typedef struct zrt_mutex  { void *slots[10]; } zrt_mutex;
typedef struct zrt_cond   { void *slots[10]; } zrt_cond;

/* zrt_coro_state is a coroutine's scheduling state. RUNNABLE sits on the run queue;
 * BLOCKED is parked on a channel wait queue (1e channels, C2); DONE has finished its
 * thunk and awaits reclamation by the scheduler. */
typedef enum {
	ZRT_CORO_RUNNABLE,
	/* RUNNING is "on a CPU right now", and it is distinct from RUNNABLE because a wake
	 * has to tell the two apart. Waking a RUNNABLE coroutine is a no-op — it is already
	 * queued and will run. Waking a RUNNING one cannot be, and cannot queue it either:
	 * the wake has to be REMEMBERED, so that the park this coroutine is on its way to
	 * does not block on news that already arrived.
	 *
	 * A select is what makes that window reachable. It pushes a waiter onto every
	 * channel it watches, and can only hold one channel lock at a time, so the moment
	 * the first waiter is visible a counterparty can claim it — while the select is
	 * still RUNNING, several statements away from parking. */
	ZRT_CORO_RUNNING,
	/* PARKING is the instant between "I have decided to block" and "I am off the CPU".
	 * It exists only because M > 1: a coroutine that announced BLOCKED while still
	 * running could be woken, queued, and picked up by a second worker before it
	 * finished switching away — the same coroutine on two stacks at once. A parking
	 * coroutine is invisible to zrt_sched_wake; the worker that swapped it out is what
	 * turns it into BLOCKED, under the scheduler lock, once the switch is complete. */
	ZRT_CORO_PARKING,
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
	zrt_mutex       *park_lock;          /* released by the worker AFTER the switch out */
	bool             woken;              /* a wake arrived while PARKING; see sched.c */
	bool             is_main;           /* coroutine 0: its end is the program's end */
	bool             deadlocked;        /* resumed to raise DeadlockError; see sched.c */
	int64_t          deadline;          /* zrt_sleep_ns wake time (zrt_time_mono); 0 = none */
	void            *tsan_fiber;        /* ZRT_TSAN only; NULL otherwise. See below. */
	/* ZRT_ASAN only, and NULL/0 otherwise. asan_fake is where AddressSanitizer parks this
	 * coroutine's fake stack while it is suspended, and asan_sched_* is the stack of the
	 * WORKER that last resumed it — which is where this coroutine switches back to, so it
	 * is learned on every resume and kept here rather than in a thread-local. See below. */
	void            *asan_fake;
	const void      *asan_sched_stack;
	size_t           asan_sched_stack_size;
	struct zrt_coro *qnext;             /* intrusive run-queue link */
} zrt_coro;

/* --- ThreadSanitizer -----------------------------------------------------------
 *
 * TSan tracks happens-before per THREAD, and a coroutine is not one: zrt_ctx_swap
 * moves the machine to another stack behind its back, so it attributes one
 * coroutine's accesses to whichever coroutine ran there before. Built naively, a
 * TSan binary of this runtime does not merely report noise — it takes a fatal
 * signal walking a stack it has the wrong shadow for.
 *
 * TSan answers this with the fiber API: a fiber is a shadow-state handle, and
 * announcing a switch moves the tracking with the machine. Every context switch
 * this scheduler makes is bracketed by one, which is what makes a TSan build both
 * possible and meaningful — and a fiber may be resumed on a different thread than
 * it parked on, which is exactly what M:N requires.
 *
 * All of it compiles to nothing when TSan is off, so the non-instrumented build
 * carries no field it does not use and no call it does not make.
 *
 * The probe is NESTED rather than one '||' expression, because the two spellings
 * cannot share a line. GCC before 14 has no '__has_feature' at all, and a '#if'
 * expression is PARSED whole before any operator short-circuits: an undefined
 * identifier becomes 0, so '__has_feature(thread_sanitizer)' reads as '0(...)' and
 * the preprocessor stops at "missing binary operator before token (" — on every
 * translation unit, TSan or not. Guarding the inner '#if' with an outer one is
 * what keeps the clang spelling out of a preprocessor that cannot read it. */
#if defined(__has_feature)
#if __has_feature(thread_sanitizer)
#define ZRT_TSAN 1
#endif
#endif
#if defined(__SANITIZE_THREAD__)
#define ZRT_TSAN 1
#endif

#ifdef ZRT_TSAN
void *__tsan_get_current_fiber(void);
void *__tsan_create_fiber(unsigned flags);
void  __tsan_destroy_fiber(void *fiber);
void  __tsan_switch_to_fiber(void *fiber, unsigned flags);
#define ZRT_TSAN_FIBER_SELF()      __tsan_get_current_fiber()
#define ZRT_TSAN_FIBER_NEW()       __tsan_create_fiber(0)
#define ZRT_TSAN_FIBER_FREE(f)     __tsan_destroy_fiber(f)
#define ZRT_TSAN_FIBER_SWITCH(f)   __tsan_switch_to_fiber((f), 0)
#else
#define ZRT_TSAN_FIBER_SELF()      NULL
#define ZRT_TSAN_FIBER_NEW()       NULL
#define ZRT_TSAN_FIBER_FREE(f)     ((void)(f))
#define ZRT_TSAN_FIBER_SWITCH(f)   ((void)(f))
#endif

/* --- AddressSanitizer --------------------------------------------------------
 *
 * ASan has the same blind spot TSan has, and it is the more dangerous of the two
 * because it is not a reporting problem — it CRASHES the program it is checking.
 *
 * ASan keeps two things per THREAD which this scheduler moves per COROUTINE. The
 * first is the thread's stack bounds, used to decide how much shadow to clean when
 * a function that never returns is about to unwind: with a coroutine on an mmap'd
 * stack those bounds name someone else's memory, and ASan says so —
 *
 *     WARNING: ASan is ignoring requested __asan_handle_no_return: ... size: -37670944
 *
 * The second is the FAKE STACK, and it is the one that kills. With use-after-return
 * detection on (the default in the compilers CI runs), an instrumented frame does not
 * live on the stack at all: ASan allocates it from a per-thread arena megabytes away,
 * so `zrt_waiter w` in zrt_chan_recv — a local, on the parked coroutine's own stack by
 * the design in chan.c — is really in the fake stack of whichever WORKER first ran that
 * frame. That arena is unmapped when its thread exits. A coroutine parked on a channel
 * therefore had its waiter torn out from under it by an unrelated worker standing down,
 * and the next walk of that wait queue read a `zrt_waiter` out of a hole in the address
 * space: SEGV in wq_pop, no shadow to explain it, one run in several hundred, and only
 * ever with several workers. That is the CI failure this annotation closes, and it was
 * never a bug in the channel or in the stack lifetime it was hunted as.
 *
 * The fiber API answers both: a switch announces the stack it is going to, and the
 * fake stack travels with the COROUTINE instead of staying with the thread. The
 * bounds of the worker to switch back to are learned from the finishing half (it
 * reports the stack that resumed us) and kept in zrt_coro rather than in a thread-
 * local, so nothing here reads thread-local state after a switch — the one place a
 * migrating coroutine cannot trust a cached TLS base.
 *
 * A coroutine that is ENDING passes NULL as its save slot, which is how ASan is told
 * to destroy that fake stack rather than leave it for a coroutine that will never
 * resume.
 *
 * All of it compiles to nothing when ASan is off. The '__has_feature' guard is nested
 * for the reason the TSan probe above gives. */
#if defined(__has_feature)
#if __has_feature(address_sanitizer)
#define ZRT_ASAN 1
#endif
#endif
#if defined(__SANITIZE_ADDRESS__)
#define ZRT_ASAN 1
#endif

#ifdef ZRT_ASAN
void __sanitizer_start_switch_fiber(void **fake_stack_save, const void *bottom, size_t size);
void __sanitizer_finish_switch_fiber(void *fake_stack_save, const void **bottom_old, size_t *size_old);
void __lsan_register_root_region(const void *p, size_t size);
void __lsan_unregister_root_region(const void *p, size_t size);
/* a live coroutine's stack is a ROOT, exactly as a thread's stack is one */
#define ZRT_LSAN_ROOT_ADD(p, n) __lsan_register_root_region((p), (n))
#define ZRT_LSAN_ROOT_DEL(p, n) __lsan_unregister_root_region((p), (n))
/* leaving this stack for [bottom, bottom+size); `save` is NULL when it is not coming back */
#define ZRT_ASAN_SWITCH_TO(save, bottom, size) __sanitizer_start_switch_fiber((save), (bottom), (size))
/* arrived: restore this fiber's fake stack and read back the stack we came FROM */
#define ZRT_ASAN_SWITCH_DONE(save, obottom, osize) __sanitizer_finish_switch_fiber((save), (obottom), (osize))
#else
#define ZRT_ASAN_SWITCH_TO(save, bottom, size)     ((void)(save), (void)(bottom), (void)(size))
#define ZRT_ASAN_SWITCH_DONE(save, obottom, osize) ((void)(save), (void)(obottom), (void)(osize))
#define ZRT_LSAN_ROOT_ADD(p, n)                    ((void)(p), (void)(n))
#define ZRT_LSAN_ROOT_DEL(p, n)                    ((void)(p), (void)(n))
#endif

/* --- threads: the M of M:N ---------------------------------------------------
 *
 * zrt_thread / zrt_mutex / zrt_cond are a SHIM over the host's threading, in exactly
 * the shape zrt_ctx is a shim over the host's context switch: an opaque slot array
 * big enough for every backend, and one implementation per platform selected at
 * build time (thread_pthread.c, thread_win32.c, or thread_none.c as the floor).
 *
 * thread_none.c is not a stub — it is the SEMANTICS the other two must match with
 * M = 1. On a host without threads (a freestanding target, a wasm build) the
 * scheduler runs every coroutine on the calling thread, which is precisely what it
 * did before M:N existed. A program's OUTPUT must not depend on which backend it
 * got; only whether two coroutines can occupy two CPUs at once.
 *
 * The slot counts are sized for the largest known layout: pthread_mutex_t is 64
 * bytes on macOS and 40 on glibc, pthread_cond_t 48, and a Win32 CRITICAL_SECTION
 * 40 — so 10 pointers (80 bytes) clears all of them. A backend static_asserts its
 * own type fits.
 */

/* zrt_thread_supported reports whether this build can actually run more than one
 * OS thread. The scheduler asks once, to decide how many workers to start. */
bool zrt_thread_supported(void);

/* zrt_cpu_count is the host's usable parallelism, or 1 when it cannot be told. */
size_t zrt_cpu_count(void);

/* zrt_thread_start runs fn(arg) on a new OS thread; false means the thread could not
 * be created and the caller must run the work itself. zrt_thread_join waits for one
 * started thread. */
bool zrt_thread_start(zrt_thread *t, void (*fn)(void *arg), void *arg);
void zrt_thread_join(zrt_thread *t);

/* The mutex is NOT recursive: the scheduler and the channels each take one lock at a
 * time and never re-enter. zrt_cond_wait atomically releases m and blocks until a
 * signal or broadcast, then re-acquires it. */
void zrt_mutex_init(zrt_mutex *m);
void zrt_mutex_destroy(zrt_mutex *m);
void zrt_mutex_lock(zrt_mutex *m);
void zrt_mutex_unlock(zrt_mutex *m);

void zrt_cond_init(zrt_cond *c);
void zrt_cond_destroy(zrt_cond *c);
void zrt_cond_wait(zrt_cond *c, zrt_mutex *m);
void zrt_cond_signal(zrt_cond *c);
void zrt_cond_broadcast(zrt_cond *c);

/* zrt_cond_timedwait is zrt_cond_wait that also gives up after ns nanoseconds. It is
 * what makes a timer cost nothing while it is pending: an idle worker with a deadline
 * ahead of it SLEEPS until then instead of spinning on the clock, and any enqueue in the
 * meantime still wakes it early through the ordinary signal. A spurious or early return
 * is allowed — every caller re-checks — and ns <= 0 returns at once. On thread_none.c,
 * where no other thread exists to signal, this is the one wait that can still end, which
 * is why timers work with M = 1 exactly as they do with M workers. */
void zrt_cond_timedwait(zrt_cond *c, zrt_mutex *m, int64_t ns);

/* zrt_atomic_claim atomically moves *flag from false to true, answering whether THIS
 * call was the one that moved it. It exists for select: one select parks a waiter on
 * every channel it watches, all sharing a single "already fired" flag, and each of
 * those channels is guarded by its OWN lock — so two workers can reach the flag under
 * two different locks at the same instant. No lock can order that; only an atomic can.
 * With no threads it is a plain read-then-write, which is what it always was. */
bool zrt_atomic_claim(bool *flag);

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

/* zrt_sleep_ns parks the running coroutine until at least ns nanoseconds of monotonic
 * time have passed, then makes it runnable again. It is the runtime's whole TIMER
 * surface — deliberately, because a timer in Zerg is not a primitive but a channel
 * (docs/code/coroutine.md, Timers & cancellation): the stdlib's `after(d)` is a
 * coroutine that sleeps and then sends, and `ticker(d)` is that in a loop, both written
 * in Zerg over this one leaf. Nothing here knows what a duration is or how to format
 * one; the policy lives above.
 *
 * Two properties the scheduler owes it. A pending sleep costs no CPU: the worker that
 * runs out of work sleeps to the nearest deadline (zrt_cond_timedwait) rather than
 * looking at the clock in a loop. And a sleeping coroutine is NOT a deadlock — it is
 * going to make progress on its own — so the detector stands down while any sleep is
 * pending, which is what lets a `select` on a timeout arm wait without being killed for
 * waiting.
 *
 * A sleep is never interrupted: ns <= 0 returns at once, and there is no cancel. A
 * coroutine sleeping when main returns is abandoned like any other. A no-op outside a
 * coroutine. */
void zrt_sleep_ns(int64_t ns);

/* zrt_sched_main / _nil / _int are the concurrency program-entry shims, one per
 * `main` return shape. Each starts the scheduler, runs main as the first coroutine,
 * and returns main's outcome as the process exit code (an aborting main yields 1, as
 * zrt_run does). The backend selects one by main's type when the Manifest reports
 * Concurrency.
 *
 * MAIN'S END IS THE PROGRAM'S END (docs/code/coroutine.md, Termination & deadlock):
 * once main's coroutine finishes — by returning or by crashing — the scheduler stops
 * handing out work and every worker stands down. A `spawn` that has not finished is
 * abandoned where it stands, so a coroutine that must complete has to be driven to a
 * channel-observed completion; there is no join. */
int zrt_sched_main(zrt_main_fn fn);
int zrt_sched_main_nil(void (*fn)(void));
int zrt_sched_main_int(int64_t (*fn)(void));

/* zrt_sched_run starts the scheduler and runs fn as the first coroutine (coroutine 0),
 * with the same lifetime the _main shims have: fn's end ends the program. Unlike them
 * it maps no return value to an exit code — the `zerg test` driver (Phase 1i) uses it
 * so a `#[test]` that `spawn`s or uses a channel runs under the scheduler like a normal
 * program, then reads its own pass/fail tally for the exit code. Linked only into a
 * concurrent test binary. */
void zrt_sched_run(void (*fn)(void));

/* zrt_sched_current returns the running coroutine, or NULL when the scheduler loop
 * itself is running (no coroutine is current). chan.c uses it to park the caller. */
zrt_coro *zrt_sched_current(void);

/* zrt_sched_park marks the current coroutine BLOCKED and returns control to the
 * scheduler; it returns once a zrt_sched_wake puts the coroutine back on the run
 * queue and the scheduler resumes it. A no-op outside a coroutine.
 *
 * IT ANSWERS NOTHING, and a caller must not read a returning park as "the thing I
 * parked for has happened". A park can end for a reason that is none of the caller's:
 * a wake meant for something else, or a token an earlier `select` left standing. Every
 * caller therefore re-tests its own condition in a loop — zrt_sleep_ns re-reads the
 * clock, the channel paths re-scan their queues.
 *
 * A waiter on a wait queue parks through zrt_sched_park_unlock, not through this. */
void zrt_sched_park(void);

/* zrt_sched_park_unlock is zrt_sched_park for a caller holding a lock that guards the
 * wait queue it just joined. Releasing that lock before switching away would open the
 * window PARKING exists to close, so the lock is handed to the scheduler: the worker
 * releases it once the coroutine is off the CPU and marked BLOCKED, both under the
 * scheduler lock. Every channel operation blocks through this, never through the bare
 * park.
 *
 * It answers HOW the park ended. False is the ordinary wake: a counterparty handed off,
 * or a close, or nothing at all (a stray token), and the caller re-scans as it always
 * did. True means the deadlock detector chose this coroutine to carry DeadlockError —
 * nothing woke it and nothing will. A caller that sees true owes two things, in order:
 * take its waiter back out of every queue it is on, because the abort is about to
 * unwind the stack that waiter lives on, and then call zrt_sched_deadlock. */
bool zrt_sched_park_unlock(zrt_mutex *m);

/* zrt_sched_deadlock raises DeadlockError on the CALLING coroutine's stack: a clean
 * abort that unwinds, runs the pending `defer`s, and is catchable by a `guard` like any
 * other (docs/code/coroutine.md, Termination & deadlock). Only a park site that has just
 * been told its park ended in a deadlock may call it, and only after unlinking itself
 * from every wait queue. */
_Noreturn void zrt_sched_deadlock(void);

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

/* zrt_defer_chan_release / _sender_release are the `void (*)(void *)` adapters a compiled
 * program registers a channel binding's release with. The ENV is always the ADDRESS of the
 * binding's storage, never its value, so the cleanup reads what the binding holds at unwind
 * time — a reassigned binding gives back what it ends up with.
 *
 * Only channels. Every other owning type is a list ELEMENT somewhere, so the emitter
 * already generates a thunk of this shape for it (`zg_elemdrop_<kind>`) and registers that. */
void zrt_defer_chan_release(void *p);
void zrt_defer_chan_sender_release(void *p);

/* zrt_chan_send copies *val (elemsz bytes) into ch: it hands off directly to a waiting
 * receiver, else buffers it when there is room, else PARKS the caller on the send queue
 * until a receiver takes it. Sending on a closed channel aborts (a dead-letter is a
 * program error). Send yields no value. */
void zrt_chan_send(zrt_chan *ch, const void *val);

/* zrt_chan_recv receives one element into *out. It returns 0 for a value (the Left of
 * Result[T]) and 1 when the channel is closed and drained (the Right). When empty and
 * open it PARKS the caller on the receive queue until a value arrives or the channel
 * closes. On a Right result zrt_chan_close_err reports the reason (see below). */
int zrt_chan_recv(zrt_chan *ch, void *out);

/* zrt_chan_close_err returns the Err a recv's Right carries — the whole Err, which is
 * the point: an ordinary close answers the StopIteration sentinel (kind
 * ZRT_ERR_STOP_ITERATION), and a close caused by a crashing last sender (Fork-C) answers
 * that coroutine's OWN Err, message, cause and kind intact, so the reason it died
 * reaches the receiver rather than a stand-in string. Reading `kind` is how the two are
 * told apart; nothing needs to match on a message. Valid immediately after a
 * zrt_chan_recv that returned 1, and this is what a `Result[T]`'s Right is built from. */
zrt_err zrt_chan_close_err(zrt_chan *ch);

/* zrt_chan_close is the EXPLICIT close behind Zerg's `close(ch)`: a flag on the CHANNEL,
 * not a give-up of the caller's own share. It touches neither rc nor senders, so every
 * handle keeps its reference and stays readable; it is idempotent, so first close wins for
 * the recorded reason; and it wakes any parked SENDER, which the auto-close path never has
 * to do because that one runs only when the last sender has already left. It does not
 * replace auto-close: a crashing producer never reaches a `close` call, and its unwind is
 * what carries the crash Err to the receiver. */
void zrt_chan_close(zrt_chan *ch);

/* zrt_crash_active reports whether the abort currently unwinding is an unhandled
 * coroutine crash (unwind.c sets it while running the crashing coroutine's cleanup
 * stack). zrt_chan_sender_release reads it, alongside zrt_taken_err for the Err itself,
 * so a channel auto-closed by a crashing last sender carries that crash rather than the
 * ordinary StopIteration. Always false on a normal path. */
bool zrt_crash_active(void);

/* --- concurrency: select (chan.c) -------------------------------------------
 *
 * `select { arm+ }` (GRAMMAR group 9) is the one multi-way wait. The backend lowers a
 * select to an array of case descriptors (one per recv/send arm; the `close` and `_` arms
 * are passed as the has_exit / has_default flags), one zrt_select call to pick and perform
 * a ready arm, then a switch on the returned index to run the chosen arm body. zrt_select
 * owns readiness, fairness, the non-blocking `_`, the exhausted select, and parking on
 * every watched channel at once.
 *
 * It divides a closed channel the way the language does. A CLEAN close is an ABSENCE: the
 * arm is dropped from the wait, never fires, and when every recv arm is gone the select is
 * exhausted. A CRASH close is a FAILURE: it is RAISED, carrying the producer's own Err, so
 * the reason cannot be lost by a receiver that was not looking for it. */

/* zrt_sel_op tags a select case's direction: a receive arm (`(id :=)? <-ch => …`) or a
 * send arm (`ch <- v => …`). */
typedef enum {
	ZRT_SEL_RECV,
	ZRT_SEL_SEND,
} zrt_sel_op;

/* zrt_sel_case is one recv/send arm's descriptor. `ch` is the arm's channel; `val` is the
 * receive target (recv) or the value to send (send).
 *
 * There is no `closed` output any more: an arm that fires has a VALUE, always. A clean
 * close drops the arm, and a crash close raises — neither reaches an arm body, so nothing
 * downstream has to ask whether the value it was handed is real. */
typedef struct {
	zrt_sel_op op;
	zrt_chan  *ch;
	void      *val;
} zrt_sel_case;

/* zrt_select scans the n cases for a ready one and performs it, returning its index
 * (0..n-1). A recv case is value-ready when its channel has a buffered value or a parked
 * sender; a send case is ready when its channel has room or a parked receiver (sending on
 * a closed channel aborts). Ties among ready cases are broken fairly by a rotating start
 * so no arm starves. When no case is value-ready it resolves in this order:
 *   - a recv channel closed by a CRASH -> RAISE its Err (the reason is never dropped);
 *   - every watched recv channel closed-and-drained, and there is at least one -> the
 *     select is exhausted: ZRT_SEL_DONE when has_exit (a `close` arm, or the `for select`
 *     that ends there), else DeadlockError, because waiting is now waiting for something
 *     that cannot happen;
 *   - has_default -> yield, then ZRT_SEL_DEFAULT (the non-blocking `_`, which never parks
 *     and therefore must not hold the one worker it is running on);
 *   - otherwise PARK on every case's channel at once and re-scan when any wakes it.
 * A cleanly closed recv arm is never value-ready and never fires: it is an absence, so it
 * leaves the wait instead of competing in it. That is what stops a finished producer from
 * starving a live one, and what lets `for select` end rather than spin. */
/* zrt_chan_select_init prepares select's own lock; the scheduler calls it at start. */
void zrt_chan_select_init(void);

int zrt_select(zrt_sel_case *cases, size_t n, bool has_default, bool has_exit);
#define ZRT_SEL_DEFAULT (-1)
#define ZRT_SEL_DONE (-2)

#endif /* ZERGRT_H */
