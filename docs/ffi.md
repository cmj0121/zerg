# Zerg Foreign Function Interface (FFI)

How a Zerg package meets the **C ABI** — the one boundary where Zerg values become C values and back.
Because C is Zerg's **codegen target** (not just an escape hatch), FFI is a native concept: it builds
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
primitives, `list[T]` over an FFI-safe `T`, opaque foreign handles, a non-capturing top-level `fn`, and
any **non-recursive** `struct`/`enum` built transitively from those.

| Zerg                           | C representation                    | Notes                                           |
| ------------------------------ | ----------------------------------- | ----------------------------------------------- |
| `bool`                         | `bool` (`<stdbool.h>`)              |                                                 |
| `byte`                         | `uint8_t`                           | Zerg's char / a raw octet                       |
| `rune`                         | `int32_t`                           | a Unicode **code point** (a scalar, not UTF-8)  |
| `int`                          | `int64_t`                           | overflow still aborts inside Zerg (see Errors)  |
| `uint`                         | `uint64_t`                          | unsigned; overflow still aborts inside Zerg     |
| `float`                        | `double`                            | pure IEEE-754, unchanged                        |
| `str`                          | `const char*`                       | NUL-terminated UTF-8; copied in / borrowed out  |
| `list[T]` (FFI-safe `T`)       | `T*` **+** `size_t` length          | a fat value (pointer + length); copied in       |
| non-capturing top-level `fn`   | C function pointer                  | a `mut` parameter lowers to a pointer parameter |
| `struct` (all fields FFI-safe) | C `struct`, by value                | field-for-field; a `list`/`str` field is fat    |
| `enum` (FFI-safe payloads)     | tagged union `{ tag; union {…}; }`  | discriminant + payload (layout deferred, below) |
| `T?`                           | tagged union — **except** see below | pointer-shaped `T` → a nullable pointer         |
| opaque handle                  | opaque `typedef` (pointer-shaped)   | a foreign resource — never dereferenced by Zerg |

A **`T?` whose `T` is pointer-shaped** (an opaque handle, or a `fn`) does **not** grow a tag: `nil` is
the **null pointer** and the value is the bare pointer. Only a `T?` over a non-pointer `T` (e.g. `int?`)
needs the tagged form. That's what lets a handle out-parameter map to C's `T**` idiom (see the example).

**Not FFI-safe** — rejected in an `extern` signature and left off the exported header, always with a
diagnostic, never silently:

- **Generics and `spec` bounds** — a generic isn't one type until it's monomorphized, so it has no single C
  signature. Cross a **concrete** instance instead (a `pub` wrapper at a fixed type).
- **A `spec` used as a type** (existential) — heap-boxed with dynamic dispatch, so no stable layout.
  That's why **`Result[T]` is never FFI-safe**: its right side is always `Err`, the `Error` spec used
  as a type. No exceptions. Export uses `T?` (whose right side is the concrete `nil`) or
  `Either[T, C]` for a concrete, FFI-safe error `C` — no new rule, just the type mapping applied.
- **`chan` and coroutine handles** — runtime-managed, reference-counted, scheduler-bound; meaningless to C.
- **A capturing closure** — it is a scope-owned struct of captures (see Language Reference). Only a
  **non-capturing top-level `fn`** may cross, as a plain C function pointer (see Concurrency).
- **A recursive or self-referential type** — the compiler **auto-boxes** it (an inserted heap
  indirection, see Language Reference), so it has no **flat** (contiguous, non-boxed) C layout and would
  drag Zerg-owned heap across the boundary. Flatten it to an FFI-safe shape (an id or index) first.

**C's integer widths.** Zerg's `int`/`uint`/`byte`/`rune` are fixed (i64/u64/u8/i32) — `uint` maps to
`uint64_t` exactly — but C's **platform-width** `int`, `unsigned`, `long`, `size_t`, … still have **no
fixed Zerg counterpart** (`size_t` is not portably 64-bit). A `list`'s `size_t` length is
compiler-emitted, never a value you name — but an `extern` signature that must name a C `int` or
`size_t` needs a set of boundary-only C-width aliases (`c_int`, `c_uint`, `c_size`, …). That set is
**deferred** (see Open questions); until it lands, only Zerg's fixed widths cross.

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
shares nothing in Zerg's terms, and frees nothing. So a bare handle is **not** a new exception to the
memory model: it stays plain bits, and a struct containing one still deep-copies like any other (the
reference-counted values remain `chan` and `Ref[T]`, which a bare handle is neither). The one subtlety is
that the token **names a resource Zerg does not own**: the foreign
allocation lives entirely outside the memory model, so scope exit frees the token (trivially, being mere
bits) but never the resource, and `del` on a handle binding ends the name without releasing anything
foreign. The resource is released **only** by an explicit paired `extern` free.

Wrap the handle in a **`Ref[sqlite3]`** — the reference-counted resource box (Language Reference) whose
`drop` is the paired `extern` free — inside a **newtype you own** (a single-field `struct`, the pattern
from [package.md](package.md), **without** the auto-cast that would re-expose the box). Its **private
field makes it opaque** outside its module, so it offers only safe methods, and `Ref[T]` makes the close
**exact**: copied, returned, or sent across `spawn`, every `Db` names one connection that closes **once**,
at the last holder's scope exit:

```text
struct Db { h: Ref[sqlite3] }                          # private + refcounted ⇒ opaque, closed once

pub fn open(path: str) -> Db? {
    mut h: sqlite3? = nil                              # a mut handle out-parameter: C's sqlite3**
    if sqlite3_open(path, h) != 0 { return nil }       # map C's status by hand — no magic
    return Db{ h: Ref(h!, sqlite3_close) }             # the box's drop is the paired free
}
```

The `mut sqlite3?` out-parameter works because a handle is a **scalar token written back by value** — the
ordinary `mut`-parameter path (Language Reference), nothing new. A C function that fills a caller's
**byte buffer in place** is a different mechanism (it writes _through_ a pointer, not a value copy-back);
that write-back protocol is deferred (see Open questions).

**The `Ref[sqlite3]` makes the close exact.** Copying a `Db` refcount-bumps the box instead of duplicating
a bare token, and the `drop` — the paired `sqlite3_close` — runs **once**, when the last `Db` leaves scope
(or is `del`-ed). This is the guarantee a bare `struct Db { h: sqlite3 }` can't give, where two copies
would each try to close one connection; the private field keeps the raw token from escaping, so the
guarantee holds through ordinary "safe" code. `Ref[T]` is the home for a resource that **escapes its
scope** — a handle opened and closed within a single scope wants `defer` instead (Language Reference).

## Ownership & lifetime at the boundary

The rule that keeps **scope-owned** intact: the compiler's automatic free applies **only to
Zerg-allocated storage**. Foreign storage lives outside the memory model; Zerg never frees it implicitly,
and never retains a Zerg buffer on C's behalf. Character and element buffers follow Zerg's "across the
boundary, copy" ethos — **copied into Zerg, borrowed out to C**:

- **`str` / `list[T]` passed _into_ C** (an argument) — C receives a **borrowed, read-only view** valid
  only for the duration of the call. C must not free it, write through it, or retain the pointer past
  return. Zerg keeps ownership and frees at scope exit as usual.
- **`str` / `list[T]` coming _out_ of C** (a return, or a field of a returned `struct`) — the bytes are
  **copied into a fresh Zerg-owned value** at the boundary, so C's buffer need only be valid at return
  time and Zerg frees **only its own copy**, never C's. An inbound `str` is accepted only when C
  guarantees **valid UTF-8 with no embedded NUL** (the `str` invariant); otherwise take it as
  `list[byte]`.
- **Plain scalar values returned from C** — a `struct` of scalars, an `int`, a `bool`: a copy Zerg now
  owns outright, freed by the ordinary scope rule.
- **A buffer or resource C allocated that _Zerg must later free_** — does **not** come back as
  `str`/`list` (that would leak C's original after Zerg copies it). It comes back as an **opaque handle**,
  paired with an explicit `extern` free the wrapper calls as a `Ref[T]`'s `drop` (see Opaque handles).
  There's no implicit free of foreign memory — ever.

## Exporting a package (Zerg → C)

There's **no export keyword**. A package's C ABI is exactly its **package-public surface** — the `pub`
declarations re-exported on the **root module** (see [package.md](package.md), "Visibility"). Any such
`pub` declaration is a candidate C entry point; the boundary adds nothing to say so.

A normal build produces a program from an entry `main`. A **library build** — a compile option, not a
source change — instead emits, in the same pass:

1. a C library exposing the **FFI-safe subset** of the root public surface under stable symbols, and
2. a matching **`.h` header** — include-guarded, holding the opaque `typedef`s, the `struct`/`enum`
   layouts, and the function prototypes.

A `pub` **method** exports too: it lowers to a C function whose **first parameter is the receiver** — a
by-value `this` becomes the struct by value, a `mut this` becomes a pointer to it (in-place) — so the
recommended handle-wrapper methods reach C as ordinary functions. A `pub` root declaration that is
**not** FFI-safe is **reported and left out** of the header rather than silently dropped: a package may
legitimately offer a richer API to Zerg dependents than it can to C, and the diagnostic keeps the C ABI
honest about what actually crosses. A Zerg function that returns nothing maps to C `void`.

**Symbol names are stable and unmangled.** C's symbol space is flat and a stable ABI forbids mangling,
so an exported name is deterministic and collision-free — conceptually the package name prefixed onto the
declaration name (a method also carrying its type, e.g. `zg_<pkg>_<name>` / `zg_<pkg>_<Type>_<method>`).
A clash on the flat exported surface is a compile error in library mode. (The exact scheme, and any
per-declaration link-name override, are open questions — see below.)

## Importing C (`extern`)

An `extern "C"` block is the sole doorway for a foreign symbol into Zerg. Each item names a C function,
opaque type, or symbol **verbatim** — no mangling is applied, the linker name is taken as written. An
`extern` signature is type-checked as FFI-safe like any boundary declaration.

`extern` is **raw**: it mirrors the C contract exactly and carries none of C's error conventions into
Zerg. A C function that signals failure by `errno`, a return code, or a `NULL` result returns those raw
to Zerg; the **thin, hand-written wrapper** is what maps them into the null-safety model —
`res.ok_or(err)`, an early `return nil`, or a constructed `Either`. Nothing's automatic, and that's
exactly what keeps the mapping explicit and auditable.

## Errors across the boundary

**An abort never crosses.** An abort (`OverflowError`, `DivideByZeroError`, `UnwrapError`, any _raised_
error) is a Zerg stack unwind that runs scope cleanup (see Language Reference); a C frame has no such
cleanup and Zerg does not own C's unwind path. So when an **exported** `pub` function (or a Zerg callback
C invokes) aborts, the unwind **traps at the boundary** — it **terminates the process** rather than
tearing through the C caller's frames (there is no Zerg `main` stack to stop at when C is the caller). To
hand a C caller a failure it can read, **demote the abort to a value at the edge with `guard`**, so the
exported function returns an FFI-safe result instead of unwinding:

```text
pub fn parse_port(s: str) -> int? {
    return guard { parse_int(s) }.ok()                 # an overflow inside becomes nil, not a trap
}
```

In the other direction, a **`Either`/`T?` value** crosses as an ordinary tagged union (or, for a
pointer-shaped `T?`, a nullable pointer) when its payloads are FFI-safe — recall `Result[T]` is not, so
export a concrete error type or `T?`. The C caller reads the **discriminant** to tell the sides apart.
Expected failure is data both sides can inspect; a bug is an abort that never leaves Zerg. (The concrete
C layout of that discriminant is a deferred detail — see Open questions.)

## Concurrency across the boundary

Zerg's concurrency — `spawn` on the M:N scheduler, and `chan` — is **runtime-internal and does not
cross**. `chan` and coroutine handles are not FFI-safe and may not appear in an `extern` signature or on
the exported surface; results and completion still travel by channel **inside** Zerg only.

- **A blocking `extern` call blocks its OS thread.** The scheduler multiplexes many coroutines onto few
  OS threads; a C call that blocks (a syscall, a sleep) parks the whole underlying thread, so coroutines
  sharing it do not advance. Prefer non-blocking C APIs, or treat a long blocking call as
  thread-occupying. How the runtime sizes or grows that thread pool is an implementation detail (TBD).
- **A `Ref[handle]` crossing coroutines** shares one foreign resource whose thread-safety Zerg cannot
  vouch for — it never sees the C-side state. Serialize it the ordinary way: give the handle to **one
  owner coroutine** and message it (the actor pattern, see [Coroutines & Channels](coroutine.md)), unless
  the C library is itself thread-safe.
- **Callbacks (C → Zerg)** are allowed only as a **non-capturing, FFI-safe top-level `fn`** handed over
  as a plain C function pointer. Such a callback runs on **whatever thread C invokes it from** — not a
  Zerg-scheduled coroutine — so it must not assume a scheduler context, and an abort inside it **traps at
  the boundary** exactly as in an exported function. Because it cannot capture and Zerg has no `void*`,
  it also cannot yet receive a Zerg context the way C's `void* userdata` callbacks expect — an open
  question below.

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

- **C-width integer aliases** (`c_int`, `c_uint`, `c_size`, `c_long`, …) so an `extern` signature can name
  C's platform-width integers, not just Zerg's fixed widths.
- The concrete **C layout of a tagged union** (discriminant type, variant values, member names,
  alignment) that a C caller reads as `.tag`.
- A **write-back protocol** for a `list[byte]` a C function fills in place (a mutable out-buffer) — the
  common `read`/`recv` shape, not yet expressible.
- **Callback context** — how a foreign-invoked callback reaches Zerg state without capture or a `void*`
  (e.g. by receiving an opaque handle as context), and whether it may `spawn`.
- The exact **stable-symbol scheme**, and whether a per-declaration **link-name override** is offered.
- Library mode on a non-FFI-safe public declaration: **skip-with-diagnostic** (current lean) vs. a hard
  error.
- The scheduler's policy for **blocking `extern` calls** (thread-pool growth) — a runtime detail.
- Whether `extern` will ever name **ABIs other than `"C"`**; only `"C"` is defined today.
