# Zerg Values & Memory

How a value is owned, copied, and freed — scope ownership, copy-by-value, `mut`, `del` / `defer`,
and the `Ref[T]` escape hatch. Part of the [Language Reference](language.md). Also in
[繁體中文](memory.zh-TW.md).

No garbage collector, no pointer syntax. Every value is **scope-owned** (freed at scope exit) and
passed **by value**. Copy-by-value is the semantics; the compiler elides copies when safe:

- **Single flow** — an immutable value may pass by-ref invisibly; a mutable one falls back to a copy.
- **Across coroutines** — always copied: no shared mutable state, no data races; propagating a change
  back is the caller's job (e.g. via a channel).
- **Extract / return** — unwrap (`?`, `!`), `match`, and `return` copy out; the source is never
  invalidated. Move is only a silent optimization when the source is dead afterward.

Recursive and self-referential types need no pointer — declare the field directly (e.g. `Node?` →
`Node`) and the compiler auto-inserts the heap indirection; such values stay scope-owned and
copy-by-value like any other.

**A `struct`'s layout is its declaration.** Fields sit in **declaration order**, the value is laid out
**inline** in its owner (no indirection beyond the recursive auto-boxing above), and the compiler **never
reorders** them — so a Zerg `struct` _is_ a C `struct`, field for field, at natural alignment with standard
padding. This falls out of transpiling to C, and it is what makes a struct **FFI-ready by default** (see
[FFI](ffi.md)): there is no separate "optimized" layout to opt out of, so Zerg needs no `repr(C)` marker. (A
sum type's payload is likewise inline; only the exact C encoding of its discriminant is a deferred FFI
detail.) Tighter control — dropping padding (**packed**) or forcing a wider **alignment**, for wire formats
and memory-mapped hardware — is a niche knob, **deferred** until a concrete need.

Mutability belongs to the **instance** — the binding — not the type or any field: `mut x := …` makes
the whole constructed instance mutable (every field), a plain `x := …` keeps it immutable; a field
carries only visibility (`pub` or private). Zerg has no general reference; code shares storage only
through:

- **Mutable-ref parameter** (`mut &` param) — the one explicit by-ref path: the callee mutates the
  caller's (`mut`) variable in place. It is confined to the call — value positions (field, `return`,
  channel send) copy its current value, it can only pass onward to another `mut &` param, and it cannot
  cross a `spawn`. **Two `mut &` arguments never share storage** — a guarantee the callee relies on:
  static aliasing (`f(x, x)`) is a **compile error**, and where the compiler cannot prove it
  (`f(xs[i], xs[j])` with `i == j` at runtime) the call **aborts** (`AliasError`). A check is
  inserted only where `mut &` arguments could dynamically alias.
- **Channels** — shared by ref across coroutines, for communication only.

**Evaluation order is left-to-right.** Function arguments, operator operands, and the elements of a
`list`/`map` literal, or a `set(...)` constructor, evaluate **in source order**, deterministically — unlike C, whose
argument-evaluation order is unspecified. So side effects (a `mut &` argument, an abort) sequence
predictably; the `and` / `or` short-circuit is this rule with the right operand skipped ([Built-in specs](specs.md)).

**Reference-counted values** are scope-owning's one exception: a value whose type implements **`Ref`** —
the built-in **`chan`**, or a stdlib **`Ref[T]`** box — is shared **by reference**, not copied. The
runtime counts holders and frees it at the **last** holder's scope exit; everything else stays pure
scope-owned, no GC/refcount. Copying a value refcount-bumps any `Ref` value it (transitively) contains
and deep-copies the rest; a `Ref` value is shared, never duplicated.

**Refcounting is cycle-complete by construction**, so it needs no cycle collector and no weak
reference: a `Ref[T]`'s referent is **fixed when the box is built** (to point elsewhere, build a new
`Ref`), and with values immutable by default and constructed bottom-up there is no way to make an
existing `Ref` point back at a later one — a reference cycle can never form, so the last-holder free is
always complete. (The lone pathological case — a `chan` buffering a reference to itself — is a
programmer error, not a checked one.)

## `Ref[T]` — a resource that outlives its scope

Most cleanup is just memory, which scope exit frees automatically. A **resource whose release is not that
automatic free** — a foreign handle (see [FFI](ffi.md)), anything that must be closed **exactly once** — and
that must **escape the scope that opened it** (returned, stored in a field, sent over a channel) is held
in a **`Ref[T]`**: a reference-counted box carrying the value and a `drop` action. Because it copies
**by-ref**, every copy names **one** resource, and `drop` runs **once**, at the last holder's scope exit
(or an explicit `del`). This is the guarantee a bare copy-by-value handle cannot give — two copies of a
plain handle would each try to free the one resource. Reach for `Ref[T]` **only when the resource
escapes**; a resource confined to one scope wants `defer` (below).

### `mut` is for owned fields — a handle's state is an effect

`mut` (and a `mut fn` method) track a change to a value's **own Zerg-owned fields**. The state _behind_ a
`Ref[T]` — a foreign handle's internal state: an OS file's position, a socket, a database cursor — is **not
part of the Zerg value's bytes**. It belongs to the resource, reached through a handle that copy-by-ref
never duplicates and construction fixes in place. Touching it is an untracked **effect** (like any I/O),
**not a mutation** — so a method that advances it needs no `mut`, and its receiver may be **immutable**: an
immutable `File` can still `read()` and advance its cursor, exactly as a C `const FILE*` can `fread`.

This is a real modelling choice. Put mutable state you **own** in a plain field and change it with a
`mut fn` on a `mut` binding — tracked, and subject to the no-aliasing guarantee above. Put a resource whose
internal state the **foreign side owns** behind a `Ref[T]` — an immutable handle with effectful methods,
needing no `mut`. The dividing question mirrors the `defer`-vs-`Ref[T]` one: **are you changing your own
bytes, or reaching state a handle owns?** Zerg has **no interior mutability** for owned fields — the same
"immutable by default, a `Ref`'s referent fixed at construction" that keeps refcounting cycle-free also
keeps an immutable binding honestly immutable.

## Re-declaration & shadowing

A name may be **re-declared** — in the same block or a nested one — and the new binding may differ in
type and mutability from the old. Re-declaration is **declare-del-declare**: the right-hand side is
evaluated first (so it may read the _old_ binding), then the old binding is `del`-ed (see below), then
the name is bound afresh.

```text
x := read()          # immutable
x := parse(x)        # RHS reads the old x; the old x is del-ed; the name rebinds to the new value
mut x := x           # shadow again — now mutable, seeded from the previous copy
```

Because the old binding is dead the instant the RHS finishes, `x := transform(x)` needs no copy — the
source is provably dead, so the move optimization applies and the old storage is reused.

## `del` — explicit early release

`del name` **revokes that name's access to its storage** before the scope ends. Freeing the storage is
only a _consequence_: it happens when the revoked access was the **owning** one and no other holder
remains; otherwise `del` merely ends this name's (or this borrow's) access early and the owner keeps
the storage.

| `del` target                          | Own? | Effect                                                                      |
| ------------------------------------- | ---- | --------------------------------------------------------------------------- |
| local, by-value param, captured copy  | yes  | last access → **storage freed**                                             |
| `mut &` param (borrows caller's var)  | no   | ends this call's borrow → **not freed**; caller keeps it                    |
| captured value, inside a closure body | no   | ends **this invocation's** access only; next call still has it              |
| channel, `Ref[T]`                     | ref  | drops a holder (refcount--); last holder runs **`drop`** (a channel closes) |

`del` can never dangle: revoking a borrow cannot free storage another name owns, and Zerg's existing
rules already stop an owner from outliving-then-freeing under a live borrower (a `mut &` parameter is
confined to its call; an escaping closure owns copies of its captures). The compiler knows statically
whether each `del` frees or merely revokes — only `Ref` values (channels and `Ref[T]`) carry a runtime
refcount.

`del` is **flow-consistent**: once a name is `del`-ed on any path, it is treated as dead on _every_
subsequent path (no runtime drop flags). A `del` inside one arm of an `if` therefore makes the name
unusable after the merge, symmetrically with the other arms.

`del ch` is also the direct way to **close a channel early** — it drops your hold on `ch` now, which
closes the channel if you were its last sender, without wrapping it in a tighter block.

## `defer` — cleanup at block exit

`defer expr` schedules `expr` to run when the enclosing **block** exits — on **every** path out,
**including an abort unwind**. It is the procedural tool for an effect bound to a scope — release a lock,
flush a buffer, close a scope-local resource — needing no type at all:

```text
{
    lock.acquire()
    defer lock.release()     # runs on every exit — normal, early return, or an abort inside risky()
    risky()
}
```

Several `defer`s in a block run **last-scheduled-first**, interleaved with scope-owned frees and `Ref`
drops in reverse construction order, so teardown mirrors setup. Three constructs share one axis — _when_
cleanup fires: `del` revokes a name **now**; `defer` fires at **this block's** exit; a `Ref[T]` drop fires
at the **last holder's** exit. The dividing line is a single question — does the resource escape its
scope? **No → `defer`; yes → `Ref[T]`.**
