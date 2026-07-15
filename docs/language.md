# Zerg Language Reference

Detailed semantics behind the design principles in the [README](../README.md). Also in
[繁體中文](language.zh-TW.md).

## Primitive Types

A small, fixed set — there is **no fixed-width integer ladder** (`i8`, `i16`, … do not exist):

| Type    | Description                                          |
| ------- | ---------------------------------------------------- |
| `bool`  | `true` / `false`                                     |
| `byte`  | unsigned 8-bit — Zerg's char                         |
| `rune`  | a single valid Unicode code point                    |
| `int`   | signed 64-bit integer                                |
| `float` | IEEE-754 double (f64)                                |
| `str`   | immutable, null-terminated Unicode (no embedded NUL) |
| `nil`   | the placeholder value of `T?`                        |

- **Integer overflow and division by zero raise** (`OverflowError`, `DivideByZeroError`) — an
  **abort**, not a value (see Null-safety & Errors); `int`/`byte`/`rune` never wrap.
- **`float` is IEEE-754:** overflow → `±Inf`, invalid → `NaN`, neither raises; `NaN` is unequal to
  everything (including itself).
- **`str` iterates as `rune` and is not indexable** — convert to `list[byte]` for raw bytes (and for
  binary that may contain a NUL, which a `str` never holds).

## Types

Declare your own **product types** (`struct`) and **sum types** (`enum`), each generic over `[...]`.

**Visibility (`pub`)** — every declaration (a type, a field, a function) is **private to its module
by default**; prefix it with `pub` to export it for use elsewhere. Mutability is a separate axis and
is **not** declared here: it belongs to the **instance** (the binding; see Values & Memory), never to
a field or type.

```text
struct Node {
    value: int,
    next:  Node?,           # self-referential — auto-boxed (see Values & Memory)
}

enum Either[X, Y] {         # generic sum type
    Left(X),
    Right(Y),
}
```

`Either`, `Result[T]`, and `T?` are not special — they are ordinary stdlib types built on `enum`
(see Null-safety).

## Specs & Generics

Generic type parameters are bounded by a **`spec`** — a named interface of what a type must provide.
Satisfaction is **nominal**: a type must explicitly declare it implements a `spec`.

- An **empty `spec`** is satisfied by every type — this expresses an unconstrained generic.
- **`Object`** is the built-in top `spec`: the minimal set every type, primitive or user, supports
  (details TBD).
- A `spec` may be used **as a type**, not only a bound: a spec-typed value holds any implementing
  type — heap-boxed, single-owner, scope-owned, dispatched **dynamically**.

Concrete-bound generics are monomorphized in the emitted C; a spec used as a type is the one place
codegen uses dynamic dispatch. `Err` is the `Error` spec, so any type implementing `Error` can be a
`Result`'s error side (see Null-safety).

## Type Casts

No type converts implicitly **by default** — an `int` is not a `bool`; cast with a constructor-style
call (`bool(8)`, `int(c)`). Primitive conversions are **compiler built-in**; a user type cannot add
an auto-cast to a primitive.

A **user type** may opt in to an **auto-cast** to another type, kept decidable by two rules:

- **Single step only** — never chained (`X → Y`, `Y → Z` ⇏ `X → Z`); one step to one explicit
  target, so no ambiguous multi-path choice arises.
- **Only where the target type is explicit** — at a typed binding (`x: X = y`), a `return`, or a
  typed parameter; never an inferred `:=`.

This is how a value, an `Err`, or `nil` flows into an `Either` at a typed binding or return without
explicit wrapping (see Null-safety).

## Values & Memory

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

Mutability belongs to the **instance** — the binding — not the type or any field: `mut x := …` makes
the whole constructed instance mutable (every field), a plain `x := …` keeps it immutable; a field
carries only visibility (`pub` or private). Zerg has no general reference; code shares storage only
through:

- **Mutable-ref parameter** (`mut` param) — the one explicit by-ref path: the callee mutates the
  caller's (`mut`) variable in place. It is confined to the call — value positions (field, `return`,
  channel send) copy its current value, it can only pass onward to another `mut` param, and it cannot
  cross a `spawn`. The same storage may not be a `mut` argument twice: static aliasing (`f(x, x)`) is
  a compile error; runtime index aliasing is the caller's job.
- **Channels** — shared by ref across coroutines, for communication only.

A channel is the **sole exception** to scope-owning: inherently shared across coroutines, the runtime
**reference-counts** it and frees it when its last holder's scope exits — everything else is pure
scope-owned, no GC/refcount. Copying a value refcount-bumps any channel it (transitively) contains
and deep-copies the rest; a channel is shared, never duplicated.

## Concurrency

Zerg is concurrent through **coroutines and channels only**: `spawn` (Go's `go`) on an **M:N
scheduler**, fire-and-forget with no join/handle, capturing **only immutable values and channels**.
Channels are the sole reference-counted, by-ref conduit — payloads copied, **auto-closed** when their
last sender leaves, received as **`Result[T]`** (`Right` = closed, carrying a crash `Err` or the
`Closed` sentinel), and multiplexed with **`select`**.

The full model — buffering, receive/close semantics, directional ends, `select`, and deadlock — is
the **[Coroutines & Channels](coroutine.md)** reference.

## Null-safety & Errors

Failure comes in **two tiers**, with exactly one bridge each way. **Recoverable failure is a value** —
absence and expected errors are ordinary values of a sum type, never a magic null; this is the tier
you work in day to day. **A bug is an abort** — overflow, division by zero, a wrong `!`, or an explicit
`raise` _raise_ and unwind the stack (see _Aborts_ below); they appear in no signature and cannot be
inspected or resumed. Both tiers carry the same `Err` (the `Error` spec), so they meet cleanly at the
bridges: **`raise` (and `!`) lift a value into an abort, `guard` demotes an abort back into a value.**

The value tier is one sum type; by convention the **left** is the value, the **right** is what gets
propagated:

- **`Either[X, Y]`** — an `X` or a `Y`; the sides must differ (`Either[T, T]` is rejected), and an
  injection that could reach both sides is a compile error (construct the variant explicitly).
- **`Result[T]`** = `Either[T, Err]`, where `Err` is the `Error` spec (any implementing type).
- **`T?`** = `Either[T, nil]`; **`nil`** is its placeholder value.

**`?` — propagate.** `x?` unwraps the left value, or early-returns the right from the enclosing
function (sugar for that early return), so the function must share the same right type. There is no
implicit bridge between `T?` and `Result`: convert first with `opt.ok_or(err)` or `res.ok()`.

```text
fn load() -> Result[Config] {
    txt := read_file(path)?     # Result[str]; an Err early-returns
    return parse(txt)           # parse -> Result[Config]
}
```

**`??` — default.** `a ?? b` yields `a`'s left value if present, else `b` (right discarded); it
short-circuits, chains right-to-left, and works on any `Either`.

**`?.` — optional chain (`T?` only).** `a?.b` reads `.b` when `a` has a value, else short-circuits
the chain to `nil` in place (unlike `?`, never returns from the function); use on any non-`T?` type
is a compile error.

**`!` — force-unwrap (value → abort).** `x!` unwraps the left value or **raises** `UnwrapError` — the
deliberate "I know it's set" hatch, a crossing from the value tier into an abort (`x!` is
unwrap-or-`raise UnwrapError`). (Logical negation is the keyword `not`, so postfix `!` is free.)

```text
port := lookup("PORT") ?? 8080
name := env("NAME") ?? env("USER") ?? "anon"
addr := config?.server?.host ?? "localhost"
```

**`raise` — abort with any `Err` (value → abort).** `raise e` escalates an `Err` into an abort carrying
it — the general producer-side crossing that `!` specialises. Keep the **value tier** (`Result` / `Either`
in the signature) for **expected, recoverable** failure; `raise` is for the **unrecoverable** — a broken
invariant, a failed assertion, a "can't happen" — so it enters no signature and is caught only by `guard`.

**Custom error types.** Any type implementing the **`Error`** spec (`message() -> str`, `unwrap() -> Err?`,
`code() -> byte?` — see Built-in specs) is an `Err`: it may sit in a `Result`'s right **and** be `raise`d,
and `guard` reifies it back as `Right(e)` with message, cause, and code intact. Use a `struct` for one
error, an `enum` for a family — the same value serves both tiers; the bridges convert.

**Aborts.** An abort — `OverflowError`, `DivideByZeroError`, `UnwrapError`, or any `Err` you `raise` —
marks a **bug**, not an expected failure. It is **not catchable as control flow**: no `try`/`catch`,
no inspecting _which_ abort fired, no resuming the failed expression. Semantically it is a **stack
unwind that runs scope cleanup** — every scope from the raise point to where it stops is freed in
order and its channels refcount-decremented, exactly like a normal scope exit; never a bare
`abort()`. An unwind that reaches the top of its stack crashes that stack: the main stack ends the
program, a coroutine's stack ends only that coroutine (`spawn` is fire-and-forget — see Concurrency).

**`guard` — demote an abort to a value (abort → value).** `guard { … }` runs a block and reifies any
abort inside it as an `Err`, so the expression is always a **`Result[T]`** (`T` = the block's value
type): a normal result `v` becomes `Left(v)`, an abort carrying `err` becomes `Right(err)`.

```text
n := guard { parse_int(untrusted) } ?? 0    # an overflow inside becomes Right(err); ?? then defaults

fn read_config(s: str) -> Result[Config] {
    return guard { risky_parse(s) }         # an abort inside demotes to Right(err)
}
```

The `Result` is **always flattened**: because a raised error is itself an `Err`, guarding a block
that already yields `Result[U]` still yields `Result[U]` — an internal abort and a returned
`Right(err)` collapse to the same `Right(err)`. `guard` catches only aborts on the **current** stack;
a coroutine `spawn`ed inside the block has its own stack and is untouched.

`guard` is the sole way back from the abort tier, mirroring `!` as the sole way in — once guarded, an
abort is an ordinary `Result` handled by the same `?` / `??` / `match`, with no separate handler and
no `recover` construct. It carries **no special meaning in a coroutine**: a coroutine body wrapped in
`guard` is just a function producing `Result[T]`, and reports it by sending over a channel like any
other value.
