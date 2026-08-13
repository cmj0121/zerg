# Zerg Foreign Function Interface (FFI)

How a Zerg package meets the **C ABI** — the one boundary where Zerg values become C values and back.
Because C is Zerg's **codegen target** (not just an escape hatch), FFI is a native concept: it builds
directly on the type, memory, error, and visibility models in the [Language Reference](../language.md) and
the public-surface rules in [Modules, Packages & Programs](package.md). Also in [繁體中文](ffi.zh-TW.md).

> **[not yet]** **Neither edge is built, so this chapter is a design rather than a description.** The
> `unsafe` context a foreign call sits inside is where it stops: the **block-expression** form is refused by name,
> with a place (`E224`), and so is a standalone **`unsafe fn`** (`E264`) and the **`unsafe fn` TYPE** the bindings
> above are spelled with (`E488`) — which takes the import edge with it. There is no `ffi` module in the shipped
> standard library, so `import "ffi"` fails at the import itself — _E502 cannot resolve import `ffi` under any source
> root_ — rather than later, at the `unsafe` the binding would have needed. The module-level **group** is the shape
> that IS built, for its `mut` bindings; what its `fn` may do inside is still refused, one operation at a time. On the
> export edge a `--emit lib` build writes an object and **no header**, and nothing reports which `pub` declarations
> would have been left out of one. `sizeof` / `alignof`, which this chapter calls a stdlib facility and
> [Built-ins](builtins.md) calls a built-in, exist in neither place.
>
> One thing here is not merely unbuilt but wrong today, and it is marked where it appears below: a
> `handle`-typed binding escapes to `cc` against generated C. (The group's caller rule used to be the
> second: a `fn` declared inside a module-level `unsafe { … }` group was callable from safe code with no
> diagnostic. It is enforced now, and reported as `E387`.)

## Two edges, one contract

FFI has two directions, and they deliberately reuse machinery you already have rather than adding a new
surface for each:

| Edge       | Direction | How it is expressed                                                              |
| ---------- | --------- | -------------------------------------------------------------------------------- |
| **export** | Zerg → C  | **no new syntax** — a package's public surface _is_ its C ABI, emitted on demand |
| **import** | C → Zerg  | **no new syntax** — the **stdlib** binds a foreign C symbol as an `unsafe fn`    |

Both edges share **one** definition of which values may cross (FFI-safe types), **one** rule for who
owns memory at the boundary, and **one** treatment of errors and concurrency. **Neither edge is
grammar**: export rides the `pub` surface, and import is a **stdlib facility** — there is no `extern`
keyword — whose foreign calls are **`unsafe`** and sit inside an `unsafe` context (below).

## FFI-safe types

Only values with a **fixed, C-representable layout known at the boundary** may cross. Formally, a
declaration is **FFI-safe** when every type it mentions is FFI-safe; the FFI-safe types are the
primitives, `list[T]` over an FFI-safe `T`, opaque foreign handles, a non-capturing top-level `fn`, and
any **non-recursive** `struct`/`enum` built transitively from those.

| Zerg                           | C representation                  | Notes                                          |
| ------------------------------ | --------------------------------- | ---------------------------------------------- |
| `bool`                         | `bool` (`<stdbool.h>`)            |                                                |
| `byte`                         | `uint8_t`                         | Zerg's char / a raw octet                      |
| `rune`                         | `int32_t`                         | a Unicode **code point** (a scalar, not UTF-8) |
| `int`                          | `int64_t`                         | overflow still aborts inside Zerg (see Errors) |
| `uint`                         | `uint64_t`                        | unsigned; overflow still aborts inside Zerg    |
| `float`                        | `double`                          | pure IEEE-754, unchanged                       |
| `str`                          | `const char*`                     | NUL-terminated UTF-8; copied in / borrowed out |
| `list[T]` (FFI-safe `T`)       | `T*` **+** `size_t` length        | a fat value (pointer + length); copied in      |
| non-capturing top-level `fn`   | C function pointer                | a `mut &` parameter lowers to a pointer        |
| `struct` (all fields FFI-safe) | C `struct`, by value              | field-for-field; a `list`/`str` field is fat   |
| `enum` (FFI-safe payloads)     | tagged union `{ tag; union … }`   | discriminant + payload (layout deferred below) |
| `T?`                           | tagged union — except see below   | pointer-shaped `T` → a nullable pointer        |
| opaque handle                  | opaque `typedef` (pointer-shaped) | a foreign resource, never dereferenced by Zerg |

A **`T?` whose `T` is pointer-shaped** (an opaque handle, or a `fn`) does **not** grow a tag: `nil` is
the **null pointer** and the value is the bare pointer. Only a `T?` over a non-pointer `T` (e.g. `int?`)
needs the tagged form. That's what lets a handle out-parameter map to C's `T**` idiom (see the example).

**Not FFI-safe** — rejected in a foreign binding's signature and left off the exported header, always
with a diagnostic, never silently:

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
compiler-emitted, never a value you name — but a foreign binding that must name a C `int` or `size_t`
needs a set of boundary-only C-width aliases (`c_int`, `c_uint`, `c_size`, …). That set is **deferred**
(see Open questions); until it lands, only Zerg's fixed widths cross.

## Opaque handles — foreign resources without a pointer

Zerg has **no safe pointer surface** and is **safe by default**, so FFI must not smuggle a dereferenceable
raw pointer into the language. It doesn't. A foreign resource crosses as an **opaque handle** — the
stdlib's pointer-shaped **`handle`** token, which Zerg can hold but never inspect. There is **no bodyless
type declaration** for it (a bare `type sqlite3` does not parse — `type` is a strong typedef and needs a
right-hand side); the raw token is the stdlib `handle`, and a named resource is a `Ref[handle]` wrapped
in a newtype you own (the same **foreign-handle pattern** as `File = Ref[handle]` in
[Process & I/O](io.md)).

The stdlib binds each foreign symbol as an **`unsafe fn`** whose signature you supply — the linker name
taken **verbatim**, no mangling:

```text
import "ffi"

sqlite3_open  := ffi.symbol[unsafe fn(path: str, mut &db: handle?) -> int]("sqlite3_open")
sqlite3_close := ffi.symbol[unsafe fn(db: handle) -> int]("sqlite3_close")
```

(The exact stdlib API is a stdlib detail; what the **language** fixes is that the result is an `unsafe
fn`, callable only inside `unsafe`.) A handle **can** be stored in a binding or field, copied, passed to
other foreign calls, and `del`-ed. It **cannot** be dereferenced, indexed, arithmetic'd, or built from a
constructor (it has no fields) — it arrives only as a foreign call's return or out-parameter.

A handle is an **opaque token copied by value like any primitive** — duplicating it copies the bits,
shares nothing in Zerg's terms, and frees nothing. So a bare handle is **not** a new exception to the
memory model: it stays plain bits, and a struct containing one still deep-copies like any other (the
reference-counted values remain `chan` and `Ref[T]`, which a bare handle is neither). The one subtlety is
that the token **names a resource Zerg does not own**: the foreign
allocation lives entirely outside the memory model, so scope exit frees the token (trivially, being mere
bits) but never the resource, and `del` on a handle binding ends the name without releasing anything
foreign. The resource is released **only** by an explicit paired **foreign free**.

Wrap the raw `handle` in a **`Ref[handle]`** — the reference-counted resource box (Language Reference)
whose `drop` is the paired foreign free — inside a **newtype you own** (a single-field `struct`, the
pattern from [package.md](package.md), **without** the accessor that would re-expose the box). Its
**private field makes it opaque** outside its module, so it offers only safe methods, and `Ref[T]` makes
the close **exact**: copied, returned, or sent across `spawn`, every `Db` names one connection that closes
**once**, at the last holder's scope exit:

```text
struct Db { h: Ref[handle] }                           # private + refcounted ⇒ opaque, closed once

pub fn open(path: str) -> Db? {
    mut raw: handle? = nil                             # a mut handle out-parameter: C's sqlite3**
    status := unsafe { sqlite3_open(path, raw) }       # a foreign call is UNSAFE — only inside `unsafe`
    if status != 0 { return nil }                      # map C's status by hand — no magic
    return Db(h: Ref(raw!, sqlite3_close))             # construction is a CALL; the box's drop is the paired free
}
```

`unsafe { … }` is a **block-expression** here: it yields the call's `int`, which the next line inspects.
The `mut &handle?` out-parameter works because a handle is a **scalar token written back by value** — the
ordinary `mut &`-parameter path (Language Reference), nothing new; note the `mut &` marker sits **before**
the name in the signature. A C function that fills a caller's **byte buffer in place** is a different
mechanism (it writes _through_ a pointer, not a value copy-back); that write-back protocol is deferred
(see Open questions).

A bare `struct Db { h: handle }` **can't** give that once-only close: two copies would each try to close
the one connection. The private field keeps the raw token from escaping, so the guarantee holds through
ordinary "safe" code. `Ref[T]` is the home for a resource that **escapes its scope** — a handle opened
and closed within a single scope wants `defer` instead (Language Reference).

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
  `str`/`list` (that would leak C's original after Zerg copies it) but as an **opaque handle** with an
  explicit paired **foreign free** (see Opaque handles). There's no implicit free of foreign memory — ever.

## Exporting a package (Zerg → C)

A package's C ABI is exactly its **package-public surface** — the `pub`
declarations re-exported on the **root module** (see [package.md](package.md), "Visibility"). Any such
`pub` declaration is a candidate C entry point; the boundary adds nothing to say so.

A normal build produces a program from an entry `main`. A **library build** — a compile option, not a
source change — instead emits, in the same pass:

1. a C library exposing the **FFI-safe subset** of the root public surface under stable symbols, and
2. a matching **`.h` header** — include-guarded, holding the opaque `typedef`s, the `struct`/`enum`
   layouts, and the function prototypes.

A `pub` **method** exports too: it lowers to a C function whose **first parameter is the receiver** — a
by-value `this` becomes the struct by value, a `mut &this` becomes a pointer to it (in-place) — so the
recommended handle-wrapper methods reach C as ordinary functions. A `pub` root declaration that is
**not** FFI-safe is **reported and left out** of the header rather than silently dropped: a package may
legitimately offer a richer API to Zerg dependents than it can to C, and the diagnostic keeps the C ABI
honest about what actually crosses. A Zerg function that returns nothing maps to C `void`.

**Symbol names are stable and unmangled.** C's symbol space is flat and a stable ABI forbids mangling,
so an exported name is deterministic and collision-free — conceptually the package name prefixed onto the
declaration name (a method also carrying its type, e.g. `zg_<pkg>_<name>` / `zg_<pkg>_<Type>_<method>`).
A clash on the flat exported surface is a compile error in library mode. (The exact scheme, and any
per-declaration link-name override, are open questions — see below.)

## Importing C — a stdlib facility

There is **no import block** in the grammar either. Binding a foreign C symbol — naming
`sqlite3_open` so Zerg may call it — is a **stdlib facility**: the stdlib resolves a linker symbol
**verbatim** (no mangling, the name taken as written) into an **`unsafe fn`**-typed callable whose
signature you supply, type-checked as FFI-safe like any boundary declaration.

> **The standard library does not use this to reach the OS.** Binding a foreign symbol is for a program
> that links a **third-party** C library (sqlite, a codec, …). Zerg itself is **zero-dependency, like Go**:
> its own standard library reaches the operating system only through the **self runtime** — the syscall
> floor in the C runtime — and never binds libc / libm here. So `io` and `math` are **pure Zerg over the
> runtime**, not FFI clients (`io.read_file` loops the runtime's syscall leaves; `math.sqrt` is a numerical
> algorithm). The runtime is the one floor; the FFI import above is how a **program** reaches beyond it.

**A foreign call is `unsafe`.** Calling such a binding is legal **only inside an `unsafe` context**. The
current unsafe model has three shapes: an **`unsafe { … }` block-expression** in a function body (it
yields the block's value, as in `open` above); a standalone **`unsafe fn`**, unsafe throughout its body
and callable only from unsafe; and a **module-level `unsafe { … }`** that **groups declarations** in an
unsafe context (a `fn` inside is an unsafe fn, a `mut` binding is a mutable global). There is **no
`unsafe mut` prefix**. Inside any of them the compiler makes no safety guarantee across the foreign call —
the thin wrapper you write is where you vouch. Group the raw bindings and their wrappers together:

The module-level group is **one context with a beginning and an end**, and both are checked:
`unsafe-item ::= decorated-decl | binding` ([`GRAMMAR`](../../GRAMMAR) group 12) derives no nested group,
so a group inside a group is refused, and a group left **unclosed** is refused at the `unsafe` that opened
it rather than swallowing the rest of the file. Neither is pedantry about braces: a missing `}` makes every
declaration below it read as being inside, which is exactly how a `mut` binding in safe code becomes a
mutable global with nothing said.

> **[not yet]** A standalone `unsafe fn` declaration is **refused by name, with a place**. Building it would
> read the `fn` as safe — nothing enforces the boundary the keyword marks — so until that check exists the
> form is turned away rather than silently disarmed. (It used to compile exactly that way:
> `unsafe fn g() -> int { return 2 }` then `print g()` compiled, and `g` was callable from ordinary safe
> code with no diagnostic at all.)

The group's own rule **is** enforced: a `fn` declared inside a module-level `unsafe { … }` group is an
unsafe fn, and naming it from safe code — calling it, or binding the bare name as a function value — is
rejected as `E387`, with a place. Its callers are the other declarations in the group, which is what the
group is for. Until the block-expression above is built, that is also the ONLY caller a program has: an
entry point is safe, so a group's `fn` is reachable from another group member and from nowhere else.

> **[deviation]** A `handle`-typed binding escapes to `cc`. `mut h: handle? = nil` produces no Zerg
> diagnostic and fails as `error: unknown type name 'zg_handle'` against the generated C — the one place in
> this chapter where a form breaks the standing contract by reaching the C compiler rather than being
> refused.

```text
unsafe {
    fn raw_open(path: str, mut &db: handle?) -> int { sqlite3_open(path, db) }
}
```

The binding is **raw**: it mirrors the C contract exactly and carries none of C's error conventions into
Zerg. A C function that signals failure by `errno`, a return code, or a `NULL` result returns those raw
to Zerg; the **thin, hand-written wrapper** — running the call inside `unsafe` — is what maps them into
the null-safety model: `res.ok_or(err)`, an early `return nil`, or a constructed `Either`. Nothing's
automatic, and that's exactly what keeps the mapping explicit and auditable.

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

Zerg's concurrency — `spawn` on the cooperative scheduler, and `chan` — is **runtime-internal and does not
cross**. `chan` and coroutine handles are not FFI-safe and may not appear in a foreign binding's signature
or on the exported surface; results and completion still travel by channel **inside** Zerg only.

- **A blocking foreign call blocks its OS thread.** The scheduler multiplexes many coroutines onto few
  OS threads; a C call that blocks (a syscall, a sleep) parks the whole underlying thread, so coroutines
  sharing it do not advance. Prefer non-blocking C APIs, or treat a long blocking call as
  thread-occupying. How the runtime sizes or grows that thread pool is an implementation detail (TBD).
- **A `Ref[handle]` crossing coroutines** shares one foreign resource whose thread-safety Zerg cannot
  vouch for — it never sees the C-side state. Serialize it the ordinary way: give the handle to **one
  owner coroutine** and message it (the actor pattern, see [Coroutines & Channels](../code/coroutine.md)), unless
  the C library is itself thread-safe.
- **Callbacks (C → Zerg)** are allowed only as a **non-capturing, FFI-safe top-level `fn`** handed over
  as a plain C function pointer. Such a callback runs on **whatever thread C invokes it from** — not a
  Zerg-scheduled coroutine — so it must not assume a scheduler context, and an abort inside it **traps at
  the boundary** exactly as in an exported function. Because it cannot capture and Zerg has no `void*`,
  it also cannot yet receive a Zerg context the way C's `void* userdata` callbacks expect — an open
  question below.

## Consistency with the rest of the language

FFI adds no exception to the existing models — it mostly falls out of them:

- **Coherence & the orphan rule** are untouched: specs and existentials are not FFI-safe, so a `(type,
spec)` implementation never appears at the C boundary.
- **`main`** is unaffected — a library build has no `main`.
- **The prelude & std** _are_ the import mechanism: the `handle` token and the symbol-binding facility
  are **stdlib**, riding on the language-level `unsafe` boundary and `Ref[T]`; std may also offer
  convenience helpers (e.g. a `str` ⇄ C-string bridge), under the same rules above.
- **Testing** treats foreign bindings and exported code as ordinary code under the usual visibility rules;
  a black-box test may link the generated header, exactly as a C dependent would.

## Open questions

Deferred for a later design pass — none blocks the model above:

- **C-width integer aliases** (`c_int`, `c_uint`, `c_size`, `c_long`, …) so a foreign binding can name
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
- The scheduler's policy for **blocking foreign calls** (thread-pool growth) — a runtime detail.
- Whether the import facility will ever bind **ABIs other than `"C"`**; only `"C"` is defined today.
- A compile-time **`sizeof` / `alignof`** — a type's size and alignment as a constant, now that the layout is
  fixed (see [Values & Memory](../core/memory.md)) — is a **stdlib** facility, deferred until a
  concrete need; it is not a core-language construct.
