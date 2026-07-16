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
- **`str` iterates as `rune` and is not indexable** — convert to `list[byte]` (see
  [Collections](collections.md)) for raw bytes (and for
  binary that may contain a NUL, which a `str` never holds).

## Types

Declare your own **product types** (`struct`) and **sum types** (`enum`), each generic over `[...]`.

**Visibility (`pub`)** — every declaration (a type, a field, a function) is **private to its module
by default**; prefix it with `pub` to export it for use elsewhere. Mutability is a separate axis and
is **not** declared here: it belongs to the **instance** (the binding; see Values & Memory), never to
a field or type. What a module and a package are — and how visibility, coherence, and the program
entry point work across them — is the [Modules, Packages & Programs](package.md) reference.

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
(see Null-safety). An `enum`'s **variants share the type's visibility** — a `pub enum` exposes every
variant, to construct and to `match`; there is no per-variant privacy.

## Pattern matching

`match` is an **expression**: it tries a value against **arms** (`pattern -> result`), runs the first
that fits, and yields its result. Every arm yields the **same type**, so a `match` is a value usable at a
`:=`, a `return`, or an argument — arms that yield `nil` read as a plain statement. Coverage is
**advised, not forced**: a `match` that misses a case is a **warning** (a linter may enforce it), not a
compile error — so **adding an `enum` variant never breaks a dependent's `match`**. A trailing **`_`**
covers the rest; a value that reaches a `match` no arm covers **aborts** at runtime (`MatchError`), and a
**redundant** arm (one an earlier arm already covers) is a warning too.

A **pattern** is one of: a **variant with a payload binding** (`Left(v)`) — bound **by copy**, like
`?`/`return`, the source never invalidated; a **literal** (`0`, `"y"`, `true`) — matched by `equal`; a
**nested** pattern (`Left(Some(v))`); an **or-pattern** (`A | B ->`, its alternatives binding the same
names at the same types); or the **wildcard `_`**, matching anything and binding nothing.

```text
msg := match ev {
    Click(p)           -> render(p)
    Key(k) | Scroll(k) -> handle(k)
    _                  -> nil
}
```

`match` never inspects an existential's real type — a spec used as a type erases it one-way, with no
downcast — it destructures variants and compares values, nothing more. **Struct-field destructuring** and
**guard conditions** (`Left(v) if v > 0`) are deferred.

## Specs & Generics

Behavior comes in two tiers. A type may define **inherent methods** — its own behavior, usable only
when you hold the concrete type. **Abstraction**, however, always goes through a **`spec`**: a named
interface of behavior (method signatures only — **never fields**). Satisfaction is **nominal**: a type
must explicitly declare it implements a `spec`, and there is **one canonical implementation per
(type, spec)** pair.

A `spec` is the sole mechanism for abstracting over behavior, so it plays three roles — the **bound**
on a generic parameter, the interface a type **conforms** to, and (below) a **type** in its own right.
The built-in behaviors are specs too, not compiler magic: `Err` is the `Error` spec, and equality,
ordering, hashing, iteration, and opt-in casts are ordinary stdlib specs. A type's inherent methods
need not belong to any spec; **only what a spec guarantees is ever abstractable**.

**A spec bound is the complete interface to a generic type.** In code generic over `T`, the only
operations available on a `T` value are the methods its spec bound declares — its fields and any
inherent methods are invisible. Hence:

- The **empty `spec`** is a valid bound, satisfied by every type, but it guarantees **no** behavior:
  such a `T` supports only the structural operations every value has from the memory model — copy it,
  `del` it, pass it, store it, or send it over a channel — not a single method.
- **`Object`** is the top `spec`, implemented automatically by every type. It provides a minimal,
  **auto-derived** method set — `equal`, `copy`, `debug`, … — generated structurally, field by field
  (a contained channel is refcount-bumped, matching the copy rule). A type may **explicitly override**
  any of them (e.g. an order-insensitive `equal`); otherwise it inherits the derived version. Because
  every type implements `Object`, bounding `T: Object` never narrows which types are accepted — it
  only unlocks those methods.

A `spec` may also be used **as a type**, not only a bound: a spec-typed value holds any implementing
type — heap-boxed, single-owner, scope-owned, and **dynamically dispatched** (the method to run is
picked at runtime from the value's real type). This is **one-way** — once boxed, the concrete type is
hidden and cannot be recovered (no downcast).

Concrete-bound generics are **monomorphized** in the emitted C — the compiler emits a separate
specialized version for each concrete type — while a spec used as a type is the one place codegen uses
dynamic dispatch.

An **implementation** (a type satisfying a spec) carries no visibility marker of its own: coherence
requires a `(type, spec)` pair to resolve to the same implementation everywhere, so an implementation
can be neither hidden nor duplicated — it is in effect exactly where both its type and its spec are
visible. Implementations are written for a **concrete or generic type** (`list[T]` may implement
`Iterator`); a blanket implementation conditioned on a bound — one covering every type that satisfies
some spec — is not offered, keeping resolution decidable. The lone "every type" case is `Object`, which
the compiler auto-derives rather than the user writing.

Because specs are nominal, two independently declared specs may share a method name. A type can still
implement both and be used as either one on its own — the ambiguity exists only where a single value
must satisfy **both at once** (a `T: X + Y` bound, or a value typed as `X + Y`). Zerg rejects that combination
at compile time rather than adding fully-qualified call syntax to disambiguate; to share one method
across specs, have them obtain it from one shared spec. Where a spec may be implemented across package
boundaries, and how coherence stays globally unique, is the
[Modules, Packages & Programs](package.md) reference.

### Built-in specs

Most are **opt-in** — a type gains one by implementing it — except the set `Object` **auto-derives for
every type** (each overridable):

| `Object` method | drives          | notes                                                  |
| --------------- | --------------- | ------------------------------------------------------ |
| `copy`          | copy-by-value   | forced by the memory model — never absent              |
| `equal`         | `==` / `!=`     | **structural**; a channel or `fn` compares by identity |
| `debug`         | logging, stderr | a developer-facing rendering                           |

Zerg has **no instance-identity test** (no `is`): under copy-by-value distinct values are distinct
instances and there is no aliasing, so identity would be meaningful only for a channel — too narrow to
earn a keyword. Equality is always the **structural** `equal`.

**Opt-in** — implement the spec to gain the capability; a generic bound gates on it:

- **`Ord`** — `<` `<=` `>` `>=`, sort, min/max: a **total** order consistent with `equal`;
  `float` does **not** implement it.
- **`Hash`** — `map` / `set` keys, with `equal ⇒ same hash`; `float` does **not** implement it.
- **`Iterator`** / **`Iterable`** — the iteration protocol (**Iteration**, below).
- **`Error`** (`Err`) — the error tier: `message() -> str`, `unwrap() -> Err?` (the underlying cause,
  `nil` if none), and `code() -> byte?` (an optional small code).
- **`Add` / `Sub` / `Mul` / `Div` / …** — the value operators (`+ - * / %`, indexing, …): operator
  overloading, below.
- **the cast spec** — an opt-in auto-cast: single-step, at an explicit target (see Type Casts).

**Operators desugar to specs**, so a user type may overload the value operators by implementing the
matching one — `==` / `<` already route through `equal` / `Ord`. An overload must mean the
**conventional** thing (a `+` that is not addition is abuse, against `small and crisp`). The null-safety
operators (`?`, `??`, `?.`, `!`) and logical `not` are fixed constructs — never
overloadable. `float` sits out `Ord` and `Hash` because its `NaN` breaks both a total order and the
`equal ⇒ hash` law, so a `float` is never a sorted-collection element or a key.

**Iteration.** An **`Iterator[T]`** has `next() -> Result[T]` — `Left(v)` for the next element, or
`Right(StopIteration)` at the end (**`StopIteration`** is a built-in `Err`). An **`Iterable[T]`** has
`iter()`, producing a fresh `Iterator[T]`. `loop x in X` requires `X: Iterable`: it binds `x` to each
`Left`, **exits cleanly on `Right(StopIteration)`**, and **raises any other `Right(err)`** — a mid-stream
failure is never silently swallowed (drive `next()` by hand and `guard` to inspect it). Since `<-ch`
already yields `Result[T]`, **a channel is an `Iterator`**: `loop v in ch` drains it, ending on a clean
close and re-raising a producer crash. An `Iterator` is trivially `Iterable`, so **lazy adapters**
(`map`, `filter`, `take`, `zip`, …) are ordinary stdlib iterators that chain. `loop mut x in X` binds each
element as an in-place `mut` — only when `X` is `mut`.

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

### Re-declaration & shadowing

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

### `del` — explicit early release

`del name` **revokes that name's access to its storage** before the scope ends. Freeing the storage is
only a _consequence_: it happens when the revoked access was the **owning** one and no other holder
remains; otherwise `del` merely ends this name's (or this borrow's) access early and the owner keeps
the storage.

| `del` target                          | Own? | Effect                                                         |
| ------------------------------------- | ---- | -------------------------------------------------------------- |
| local, by-value param, captured copy  | yes  | last access → **storage freed**                                |
| `mut` param (borrows caller's var)    | no   | ends this call's borrow → **not freed**; caller keeps it       |
| captured value, inside a closure body | no   | ends **this invocation's** access only; next call still has it |
| channel                               | ref  | drops a holder (refcount--); last sender → **closes**          |

`del` can never dangle: revoking a borrow cannot free storage another name owns, and Zerg's existing
rules already stop an owner from outliving-then-freeing under a live borrower (a `mut` parameter is
confined to its call; an escaping closure owns copies of its captures). The compiler knows statically
whether each `del` frees or merely revokes — only channels carry a runtime refcount.

`del` is **flow-consistent**: once a name is `del`-ed on any path, it is treated as dead on _every_
subsequent path (no runtime drop flags). A `del` inside one arm of an `if` therefore makes the name
unusable after the merge, symmetrically with the other arms.

`del ch` is also the direct way to **close a channel early** — it drops your hold on `ch` now, which
closes the channel if you were its last sender, without wrapping it in a tighter block.

## Construction & encapsulation

The one primitive for building a value is the **struct literal** — it names every field, so it is
usable only where every field is visible. A "constructor" is not a separate feature: it is an ordinary
(usually `pub`) associated function that returns a literal. A type with any **private field** is
therefore **opaque** outside its module — a struct literal cannot name the private field, so outsiders
build it only through such a `pub` function, which runs inside the type's module and can establish the
type's invariant at the moment of construction.

Field visibility is a **single knob covering read and write together** — a `pub` field is readable
and, given a `mut` binding, writable; a private field is neither. There is no separate "public read,
private write" axis; finer control is expressed with methods.

Copy-by-value reframes what a writable `pub` field means: writing one only ever changes the holder's
**own copy**, never anyone else's value (there is no aliasing). So a `pub` mutable field is not a
shared-mutation hazard. The reason to keep a field private is not to stop others changing your value —
copy already does — but to **protect an invariant**: only the type's own methods may change the field,
so every value of the type stays valid. A plain data type with no invariant can expose its fields
freely; a type that must stay valid keeps them private and offers:

- **read** — a `pub` getter method returning a copy of the field (cheap under copy-by-value);
- **change** — a `pub` mutator method taking `mut self`, which re-establishes the invariant.

To build a value that is an existing one with a few fields changed, use a **functional update** —
`Foo{ ..base, age: 2 }` produces a **new** value, leaving `base` untouched — for a type whose fields
are visible, or a `with`-style method returning a new instance for an opaque type. Zerg has **no
mutating builder or cascade**: it would work only for public-field types (where the literal already
says everything), could not touch a private field, and would drag a value through invalid intermediate
states — against immutable-by-default and valid-at-construction.

## Functions & Closures

A function is a **first-class value**: it has a type, and can be passed as an argument, returned,
stored in a field, and bound to a variable. A function type is written `fn(P...) -> R`; a parameter's
`mut` is **part of the type**, so `fn(mut int) -> bool` and `fn(int) -> bool` are distinct types (they
differ in calling convention — by-ref-in-place vs copy). Visibility is **not** part of the type: `pub`
exports a top-level function's **name**, never travels with the value, and is meaningless on an
anonymous function.

The mutability of the binding that _holds_ a function is the ordinary per-instance axis — `mut f := …`
is rebindable, `f := …` is not — and is orthogonal to everything above.

**Closures capture by the same rule as `spawn`: only immutable values and channels, copied in.** A
`mut` variable cannot be captured; snapshot it into an immutable binding first (`snap := n`). Capture
is by copy — a captured channel is refcount-bumped, everything else deep-copied — so a closure that
escapes its defining scope carries its own copies and can never dangle. Equivalently:

> A closure is a scope-owned struct whose fields are its captures.

Copy, free, and channel-refcount therefore all fall out of the existing memory rules with nothing
added; a captured send-capable channel end counts as a holder, so a live closure keeps that channel's
send side open (the send-coverage invariant in [Coroutines & Channels](coroutine.md)).

Capturing an immutable value is not the same as being unable to use `mut`: **local** mutation inside a
closure body is unrestricted — you just cannot mutate _captured_ state.

```text
base := load_cfg()                 # immutable
apply := fn(req: Request) -> Reply {
    mut acc := base                # local mutable working copy, seeded from the capture
    acc = merge(acc, req)          # mutating the local, not the capture — fine
    return build(acc)
}
```

Two classic closure hazards are ruled out by construction. A loop variable is `mut`, so it **cannot**
be captured — each iteration must snapshot, giving every closure its own value (no shared-loop-var
bug):

```text
loop i in 0..n {
    snap := i                      # required — i is mut and uncapturable
    spawn fn() { handle(snap) }    # each coroutine gets its own 0, 1, 2, …
}
```

And because captures are always immutable copies, "captured the variable or the value?" has no
observable answer — the captured value can never change, so the question disappears.

## Concurrency

Zerg is concurrent through **coroutines and channels only**: `spawn` (Go's `go`) on an **M:N
scheduler**, fire-and-forget with no join/handle, capturing **only immutable values and channels**.
Channels are the sole reference-counted, by-ref conduit — payloads copied, **auto-closed** when their
last sender leaves, received as **`Result[T]`** (`Right` = closed, carrying a crash `Err` or the
`StopIteration` sentinel), and multiplexed with **`select`**.

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
deliberate "I know it's set" hatch, a crossing from the value tier into an abort. (Logical negation is
the keyword `not`, so postfix `!` is free.)

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

**Aborts.** An abort — a built-in (`OverflowError`, `DivideByZeroError`, `UnwrapError`, …) or any `Err`
you `raise` —
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

`guard` is the sole way back from the abort tier, mirroring `raise`/`!` as the ways in — once guarded, an
abort is an ordinary `Result` handled by the same `?` / `??` / `match`, with no separate handler and
no `recover` construct. It carries **no special meaning in a coroutine**: a coroutine body wrapped in
`guard` is just a function producing `Result[T]`, and reports it by sending over a channel like any
other value.
