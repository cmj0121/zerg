# Zerg Foreign Function Interface (FFI)

How a Zerg package meets the **C ABI** — the one boundary where Zerg values become C values and back.
Because C is Zerg's **codegen target** (not merely an escape hatch), FFI is a native concept: it builds
directly on the type, memory, error, and visibility models in the [Language Reference](language.md) and
the public-surface rules in [Modules, Packages & Programs](package.md). Also in [繁體中文](ffi.zh-TW.md).

## Two edges, one contract

FFI has two directions, and they deliberately reuse machinery you already have rather than adding a new
surface for each:

| Edge       | Direction | How it is expressed                                                              |
| ---------- | --------- | -------------------------------------------------------------------------------- |
| **export** | Zerg → C  | **no new syntax** — a package's public surface _is_ its C ABI, emitted on demand |
| **import** | C → Zerg  | an **`extern`** block names the foreign C symbols Zerg may call                  |

Both edges share **one** definition of which values may cross (FFI-safe types), **one** rule for who
owns memory at the boundary, and **one** treatment of errors and concurrency. `extern` names the import
direction only; it never appears on the export side.

## FFI-safe types

Only values with a **fixed, C-representable layout known at the boundary** may cross. Formally, a
declaration is **FFI-safe** when every type it mentions is FFI-safe; the FFI-safe types are the
primitives, `list[byte]`, opaque foreign handles, and any **non-recursive** `struct`/`enum` built
transitively from those.

| Zerg                           | C representation                   | Notes                                            |
| ------------------------------ | ---------------------------------- | ------------------------------------------------ |
| `bool`                         | `bool` (`<stdbool.h>`)             |                                                  |
| `byte`                         | `uint8_t`                          | Zerg's char / a raw octet                        |
| `rune`                         | `int32_t`                          | a Unicode **code point** (a scalar, not UTF-8)   |
| `int`                          | `int64_t`                          | overflow still aborts inside Zerg (see Errors)   |
| `float`                        | `double`                           | pure IEEE-754, unchanged                         |
| `str`                          | `const char*`                      | immutable, NUL-terminated UTF-8; a borrowed view |
| `list[byte]`                   | `uint8_t*` **+** `size_t` length   | raw/binary bytes; unlike `str`, may hold a NUL   |
| `struct` (all fields FFI-safe) | C `struct`, by value               | field-for-field layout                           |
| `enum` (FFI-safe payloads)     | tagged union `{ tag; union {…}; }` | `T?` included when `T` is FFI-safe               |
| opaque handle                  | opaque `typedef` (pointer-shaped)  | a foreign resource — never dereferenced by Zerg  |

**Not FFI-safe** — rejected in an `extern` signature and left off the exported header, always with a
diagnostic, never silently:

- **Generics and `spec` bounds** — a generic is not one type until monomorphized, so it has no single C
  signature. Cross a **concrete** instance instead (a `pub` wrapper at a fixed type).
- **A `spec` used as a type** (existential) — heap-boxed with dynamic dispatch, so no stable layout.
  This is why **`Result[T]` is generally not FFI-safe**: its right side is `Err`, the `Error` spec used
  as a type. Export uses `T?` (whose right side is the concrete `nil`) or `Either[T, C]` for a concrete,
  FFI-safe error `C` — no new rule, just the type mapping applied.
- **`chan` and coroutine handles** — runtime-managed, reference-counted, scheduler-bound; meaningless to C.
- **A capturing closure** — it is a scope-owned struct of captures (see Language Reference). Only a
  **non-capturing top-level `fn`** may cross, as a plain C function pointer (see Concurrency).
- **A recursive or self-referential type** — the compiler **auto-boxes** it (an inserted heap
  indirection, see Language Reference), so it has no flat C layout and would drag Zerg-owned heap across
  the boundary. Flatten it to an FFI-safe shape (e.g. an id or an index) first.

## Opaque handles — foreign resources without a pointer

Zerg has **no pointer surface** and is **safe by default**, so FFI must not smuggle a dereferenceable
raw pointer into the language. It doesn't. A foreign resource crosses as an **opaque handle**: a named
type, declared in an `extern` block with **no body**, that Zerg can hold but never inspect.

```text
extern "C" {
    type sqlite3                                       # opaque — no fields, pointer-shaped, never dereferenced
    fn sqlite3_open(path: str, db: mut sqlite3?) -> int
    fn sqlite3_close(db: sqlite3) -> int
}
```

A handle **can** be stored in a binding or field, copied, passed to other `extern` calls, and `del`-ed.
It **cannot** be dereferenced, indexed, arithmetic'd, or built from a struct literal (it has no fields) —
it arrives only as an `extern` return or out-parameter.

A handle is an **opaque token copied by value like any primitive** — duplicating it copies the bits,
shares nothing in Zerg's terms, and frees nothing. So it is **not** a new exception to the memory model:
`chan` stays the sole reference-counted value, and a struct containing a handle still deep-copies like
any other. The one subtlety is that the token **names a resource Zerg does not own**: the foreign
allocation lives entirely outside the memory model, so scope exit frees the token (trivially, being mere
bits) but never the resource, and `del` on a handle binding ends the name without releasing anything
foreign. The resource is released **only** by an explicit paired `extern` free.

That leaves exactly one gap the language cannot close statically — several live tokens may name a
resource C has already freed — and it is a **foreign** correctness concern, not a Zerg aliasing
violation, since Zerg attaches no ownership to the token. It is precisely why a raw handle should be
**wrapped in a newtype you own** (a single-field `struct`, per the newtype guidance in
[package.md](package.md)) that exposes only safe methods and a `del`/close, so the bare handle never
escapes into ordinary code:

```text
struct Db { h: sqlite3 }                               # private field ⇒ opaque outside its module

pub fn open(path: str) -> Db? {
    mut h: sqlite3? = nil
    if sqlite3_open(path, h) != 0 { return nil }       # map C's status by hand — no magic
    return Db{ h: h! }
}
```

## Ownership & lifetime at the boundary

The rule that keeps **scope-owned** intact: the compiler's automatic free applies **only to
Zerg-allocated storage**. Foreign storage lives outside the memory model and is released by an explicit
`extern` free — Zerg never frees it implicitly, and never retains a Zerg buffer on C's behalf.

- **`str` / `list[byte]` passed into C** — C receives a **borrowed, read-only view** valid only for the
  duration of the call. C must not free it, write through it, or retain the pointer past return. Zerg
  keeps ownership and frees at scope exit as usual.
- **Plain values returned from C** — a `struct` by value, an `int`, a `bool`: a copy Zerg now owns
  outright, freed by the ordinary scope rule.
- **A buffer or resource allocated by C** — comes back as an **opaque handle** (or as `str`/`list[byte]`
  only when C guarantees the buffer's lifetime), paired with an explicit `extern` free the wrapper is
  responsible for calling. There is no implicit free of foreign memory — ever.

## Exporting a package (Zerg → C)

There is **no export keyword**. A package's C ABI is exactly its **package-public surface** — the `pub`
declarations re-exported on the **root module** (see [package.md](package.md), "Visibility"). Any such
`pub` declaration is a candidate C entry point; the boundary adds nothing to say so.

A normal build produces a program from an entry `main`. A **library build** — a compile option, not a
source change — instead emits, in the same pass:

1. a C library exposing the **FFI-safe subset** of the root public surface under stable symbols, and
2. a matching **`.h` header** — include-guarded, holding the opaque `typedef`s, the `struct`/`enum`
   layouts, and the function prototypes.

The header is nearly free because C is already the codegen target. A `pub` root declaration that is
**not** FFI-safe is **reported and left out** of the header rather than silently dropped: a package may
legitimately offer a richer API to Zerg dependents than it can to C, and the diagnostic keeps the C ABI
honest about what actually crosses.

**Symbol names are stable and unmangled.** C's symbol space is flat and a stable ABI forbids mangling,
so an exported name is deterministic and collision-free — conceptually the package name prefixed onto
the declaration name (e.g. `zg_<pkg>_<name>`). A clash on the flat exported surface is a compile error
in library mode. (The exact scheme, and any per-declaration link-name override, are open questions —
see below.)

## Importing C (`extern`)

An `extern "C"` block is the sole doorway for a foreign symbol into Zerg. Each item names a C
function, opaque type, or symbol **verbatim** — no mangling is applied, the linker name is taken as
written. An `extern` signature is type-checked as FFI-safe like any boundary declaration.

`extern` is **raw**: it mirrors the C contract exactly and carries none of C's error conventions into
Zerg. A C function that signals failure by `errno`, a return code, or a `NULL` result returns those raw
to Zerg; the **thin, hand-written wrapper** is what maps them into the null-safety model —
`res.ok_or(err)`, an early `return nil`, or a constructed `Either`. Nothing is automatic, which is
exactly what keeps the mapping explicit and auditable.

## Errors across the boundary

**An abort never crosses.** An abort (`OverflowError`, `DivideByZeroError`, `UnwrapError`, any _raised_
error) is a Zerg stack unwind that runs scope cleanup (see Language Reference); a C frame has no such
cleanup and Zerg does not own C's unwind path. So when an **exported** `pub` function aborts, the unwind
**stops and traps at the boundary** — it ends the process (or the calling stack) rather than tearing
through the C caller's frames. To hand a C caller a failure it can read, **demote the abort to a value
at the edge with `guard`**, so the exported function returns an FFI-safe result instead of unwinding:

```text
pub fn parse_port(s: str) -> int? {
    return guard { to_int(s) }.ok()                    # an overflow inside becomes nil, not a trap
}
```

In the other direction, a **`Result`/`Either`/`T?` value** crosses as an ordinary tagged union when its
payloads are FFI-safe (recall `Result[T]` usually is not — use `T?` or a concrete error type); the C
caller reads `.tag` to tell the sides apart. Expected failure is data both sides can inspect; a bug is
an abort that never leaves Zerg.

## Concurrency across the boundary

Zerg's concurrency — `spawn` on the M:N scheduler, and `chan` — is **runtime-internal and does not
cross**. `chan` and coroutine handles are not FFI-safe and may not appear in an `extern` signature or on
the exported surface; results and completion still travel by channel **inside** Zerg only.

- **A blocking `extern` call blocks its OS thread.** The scheduler multiplexes many coroutines onto few
  OS threads; a C call that blocks (a syscall, a sleep) parks the whole underlying thread, so coroutines
  sharing it do not advance. Prefer non-blocking C APIs, or treat a long blocking call as
  thread-occupying. How the runtime sizes or grows that thread pool is an implementation detail (TBD).
- **Callbacks (C → Zerg)** are allowed only as a **non-capturing, FFI-safe top-level `fn`** handed over
  as a plain C function pointer. Such a callback runs on **whatever thread C invokes it from** — not a
  Zerg-scheduled coroutine — so it must not assume a scheduler context; `spawn`/`chan` from inside a
  foreign-invoked callback is constrained (TBD). A callback is a Zerg stack of its own, so an abort
  inside it **traps at the boundary** exactly as in an exported function — `guard` it to return a value.

## Consistency with the rest of the language

FFI adds no exception to the existing models — it mostly falls out of them:

- **Coherence & the orphan rule** are untouched: specs and existentials are not FFI-safe, so `(type,
spec)` implementations simply never appear at the C boundary. FFI trades in concrete data and
  functions, not abstraction.
- **`main`** is unaffected — a library build has no `main`; its `Result[nil]` exit model concerns
  programs, not exported libraries.
- **The prelude & std** are not involved in the mechanism: opaque handles and `extern` are language-level,
  built into the toolchain like the primitive keywords. std may still offer convenience helpers (e.g. a
  `str` ⇄ C-string bridge), but they ride the same rules above.
- **Testing** treats `extern` and exported code as ordinary code under the usual visibility rules; a
  black-box test may link the generated header, exactly as a C dependent would.

## Open questions

Deferred for a later design pass — none blocks the model above:

- The exact **stable-symbol scheme**, and whether a per-declaration **link-name override** is offered.
- Library mode on a non-FFI-safe public declaration: **skip-with-diagnostic** (current lean) vs. a hard
  error.
- The scheduler's policy for **blocking `extern` calls** (thread-pool growth) — a runtime detail.
- A **write-back protocol** for a `list[byte]` a C function fills (a mutable out-buffer).
- **Callback semantics** when foreign code invokes a Zerg `fn` off the scheduler (may it `spawn`?).
- Whether `extern` will ever name **ABIs other than `"C"`**; only `"C"` is defined today.
