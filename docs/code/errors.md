# Zerg Null-safety & Errors

The two failure tiers — recoverable values and aborts — the operators that bridge them
(`?` `??` `?.` `!` `raise` `guard`), and how errors are handled by type. Part of the
[Language Reference](../language.md). Also in [繁體中文](errors.zh-TW.md).

Failure comes in **two tiers**, with exactly one bridge each way. **Recoverable failure is a value** —
absence and expected errors are ordinary values of a sum type, never a magic null; this is the tier
you work in day to day. **A bug is an abort** — overflow, division by zero, a wrong `!`, or an explicit
`raise` _raise_ and unwind the stack (see _Aborts_ below); they appear in no signature and can't be
inspected or resumed. Both tiers carry the same `Err` (the `Error` spec), so they meet cleanly at the
bridges: **`raise` (and `!`) lift a value into an abort, `guard` demotes an abort back into a value.**

The value tier is one sum type; by convention the **left** is the value, the **right** is what gets
propagated:

- **`Either[X, Y]`** — an `X` or a `Y`; the sides must differ (`Either[T, T]` is rejected), and an
  injection that could reach both sides is a compile error (construct the variant explicitly).
- **`Result[T]`** = `Either[T, Err]`, where `Err` is the `Error` spec (any implementing type).
- **`T?`** = `Either[T, nil]`; **`nil`** is its placeholder value.

**`?` — propagate.** `x?` unwraps the left value, or early-returns the right from the enclosing
function (sugar for that early return), so the function must share the same right type. There's no
implicit bridge between `T?` and `Result`: convert first with `opt.ok_or(err)` or `res.ok()`.

```text
fn load() -> Result[Config] {
    txt := read_file(path)?     # Result[str]; an Err early-returns
    return parse(txt)           # parse -> Result[Config]
}
```

> **[deviation]** `?` is defined on any `Either[X, Y]` — unwrap the `Left`, early-return the `Right`
> unchanged — and the two compilers cover different halves of it. The **seed** threads `Result[T]`:
> on a general `Either` the right payload is **dropped** (a silent miscompile), and `?` on a
> **`Result[nil]`** reaches the backend as a `void`-typed binding (a fail-loud sema gap). The shipped
> **`zerg`** threads **`T?`** instead — the absence early-returns, and the enclosing function must
> answer a `T?` to carry it — and refuses the `Result` half **by name**, because `Result[T]` does not
> survive in a signature there yet. The intended behavior threads the `Right` value unchanged in
> every case.

**`??` — default.** `a ?? b` yields `a`'s left value if present, else `b` (right discarded); it
short-circuits, chains right-to-left, and works on any `Either`.

**`?.` — optional chain (`T?` only).** `a?.b` reads `.b` when `a` has a value, else short-circuits
the chain to `nil` in place (unlike `?`, never returns from the function); use on any non-`T?` type
is a compile error. A field that is **itself optional flattens** — the chain answers that field's
type rather than a nested `T??`, which is not a type the language can write. **[implemented]**, in
both compilers.

**`!` — force-unwrap (value → abort).** `x!` unwraps the left value or **raises** on an absent optional —
the deliberate "I know it's set" hatch, a crossing from the value tier into an abort. (Logical negation is
the keyword `not`, so postfix `!` is free.)

> **[not yet]** `UnwrapError` as a distinct, `is`-testable error **kind** is not built; today the abort
> fires with a generic message and is not one of the six taxonomy kinds below.

```text
port := lookup("PORT") ?? 8080
name := env("NAME") ?? env("USER") ?? "anon"
addr := config?.server?.host ?? "localhost"
```

**`raise` — abort with any `Err` (value → abort).** `raise e` escalates an `Err` into an abort carrying
it — the general producer-side crossing that `!` specialises. Keep the **value tier** (`Result` / `Either`
in the signature) for **expected, recoverable** failure; `raise` is for the **unrecoverable** — a broken
invariant, a failed assertion, a "can't happen" — so it enters no signature and is caught only by `guard`.
A **`raise e from cause`** form records `cause` as `e`'s `unwrap()` — a **nested** abort that wraps a
lower-level `Err` in a higher-level one without losing it, feeding the same cause chain every `Error`
exposes; a bare `raise e` carries `e` unchanged.

**The built-in error taxonomy.** This phase ships a **fixed set of six** error kinds **[implemented]** —
**`ValueError`**, **`OverflowError`**, **`IOError`**, **`EncodingError`**, **`IndexError`**, **`KeyError`**
— and you **choose from these**; **defining your own** error type (a `struct` / `enum` implementing the **`Error`** spec —
`message() -> str`, `unwrap() -> Err?`, `code() -> byte?`, see [Built-in specs](../core/specs.md)) is **not yet
supported**. Each kind is a full `Err`: construct one with a message (`raise ValueError("bad input")`), let it
sit in a `Result`'s right **and** be `raise`d, read `err.message()`, test it with `err is ValueError`, and have
`guard` reify it back as `Right(err)` with message, cause, and code intact. The runtime's **own intrinsic
failures raise the matching kind**, so library and runtime errors share one vocabulary: a failed integer parse
is a `ValueError` (out of range → `OverflowError`), a checked narrowing conversion an `OverflowError`, an I/O
failure an `IOError`, a `str` bridge over invalid UTF-8 an `EncodingError`, an out-of-bounds index an
`IndexError`, a missing `map` key a `KeyError`. A `Result` / `Either` carries them and `?` / `??` / `guard`
thread them unchanged; a `match` on a concrete `Either[T, Kind]` distinguishes the kind. The abort
contract itself — the message written to stderr, exit status 1, the `Kind: message` line — is
[Conformance](../conformance.md).

**Aborts.** An abort — a built-in runtime fault or any `Err` you `raise` — marks a **bug**, not an
expected failure. Of the fault names this chapter uses, nine reify as `is`-testable **kinds** today:
`ValueError`, `OverflowError`, `IOError`, `EncodingError`, `IndexError`, `KeyError`, plus the three the
concurrency chapter names — `SendOnClosedError`, `DeadlockError` and `StopIteration` (**[implemented]**).
The rest cannot be **named** at the surface yet: `UnwrapError`, `DivideByZeroError`, `MatchError` and
`AliasError` are **[not yet]** — writing `err is AliasError` is a clean, named compile error in **both**
compilers, the name not being one of the nine — and the abort carries no distinct reified kind for them,
only a generic message.

**`StopIteration` is testable but not constructible.** It is the one name a program may put on the right
of `is` and may **not** call: `raise StopIteration("…")` is a compile error in **both** compilers. A
channel's clean close carries it as a **kind** rather than a message, which is what lets a consumer tell a
clean end from a crash without comparing strings; a sender able to raise the sentinel would defeat exactly
that (see [Concurrency](coroutine.md)). `StackOverflowError` is a **[deviation]** (see below). An abort is **not
catchable as control flow**: no `try`/`catch`,
no inspecting _which_ abort fired, no resuming the failed expression. Semantically it is a **stack
unwind that runs scope cleanup** — every scope from the raise point to where it stops **runs its
`defer`s** and is freed in order, its `Ref` values (channels and `Ref[T]`) refcount-decremented, exactly
like a normal scope exit; never a bare `abort()`. An unwind that reaches the top of its stack crashes
that stack: the main stack ends the program, a coroutine's stack ends only that coroutine (`spawn` is
fire-and-forget — see [Concurrency](coroutine.md)).

An abort that fires **while another is already unwinding** — a `defer`, or a `Ref` `drop`, that itself
aborts — never abandons that unwind: the remaining `defer`s still run, so **cleanup is never skipped**. The
two errors combine by the **same nesting as `raise e from cause`** — the later abort propagates with the one
already in flight recorded as its `unwrap()` cause — so neither is lost and the consumer reads the whole
chain. No error silently wins, and there is no separate _suppressed_ slot to consult.

A **`StackOverflowError`** is Zerg's own safety net, not the OS's: the runtime **owns every stack** — the
main one and each coroutine's — and **checks call depth itself**, raising this abort (a clean unwind that
runs `defer`s) the instant a call would exceed the stack, so runaway recursion **never** becomes a C
stack smash. Zerg does **not** optimize tail calls — `for` is the loop, so a bounded stack is enough —
which makes an unbounded recursion a definite `StackOverflowError`, never a silent hang.

> **[deviation]** The bootstrap does **not** yet own or depth-check the stack: a stack overflow is an
> unrecoverable `SIGSEGV` / stack-smash that terminates the process **without** running `defer`s, not a
> clean `StackOverflowError` unwind (see [Conformance](../conformance.md), the runtime-abort deviation). The
> intended safety net stands; it is not built this phase.

A **`DeadlockError`** — every coroutine blocked with no progress possible — is now the clean abort the spec
asks for: it unwinds, runs the pending `defer`s, and a `guard` catches it. It is raised on `main`'s
coroutine and re-raised on **every** detection rather than once, so a `guard` that retries unchanged turns
the deadlock into a livelock; see [Concurrency](coroutine.md) for why both are deliberate.

**`guard` — demote an abort to a value (abort → value).** `guard { … }` runs a block and reifies any
abort inside it as an `Err`, so the expression is always a **`Result[T]`** (`T` = the block's value
type): a normal result `v` becomes `Left(v)`, an abort carrying `err` becomes `Right(err)`.

```text
n := guard { parse_int(untrusted) } ?? 0    # an overflow inside becomes Right(err); ?? then defaults

fn read_config(s: str) -> Result[Config] {
    return guard { risky_parse(s) }         # an abort inside demotes to Right(err)
}
```

> **Two limits on what a guarded block may be, both loud.** A `return`, `break` or `continue`
> that LEAVES the block is refused in both compilers: the handler is pushed before the block and
> popped after it, so a jump in between takes the frame away and leaves the handler installed on
> it. And a block whose value is a name BOUND INSIDE it is refused, because C makes an automatic
> variable modified between `setjmp` and the landing pad indeterminate unless it is `volatile`
> (C99 7.13.2.1) — give the block a call or a literal as its value, which is the everyday shape
> anyway.
>
> **[not yet]** The `read_config` example above needs `Result[T]` to survive in a SIGNATURE, which
> the shipped `zerg` erases — the guard itself is fine, the return type is not. Until that lands,
> hand the `Result` straight to `??` / `match` at the call site instead of returning it.

The `Result` is **always flattened**: because a raised error is itself an `Err`, guarding a block
that already yields `Result[U]` still yields `Result[U]` — an internal abort and a returned
`Right(err)` collapse to the same `Right(err)`. `guard` catches only aborts on the **current** stack;
a coroutine `spawn`ed inside the block has its own stack and is untouched.

`guard` is the sole way back from the abort tier, mirroring `raise`/`!` as the ways in — once guarded, an
abort is an ordinary `Result` handled by the same `?` / `??` / `match`, with no separate handler and
no `recover` construct. It carries **no special meaning in a coroutine**: a coroutine body wrapped in
`guard` is just a function producing `Result[T]`, and reports it by sending over a channel like any
other value.

## Handling errors by type — `is`

Both tiers deliver the **same erased `Err`** — a value-tier `Right(err)` and a `guard`-reified abort are
indistinguishable — so one mechanism dispatches on either. To act on a **specific** error, test its type
with **`is`** ([Type tests](../core/specs.md)):

```text
match guard { work() } {
    Left(v)  => use(v)
    Right(e) => {
        if e is IOError { rebuild() }           # branch on the taxonomy kind
        else if e is OverflowError { alert(e) }  # a built-in abort, reified by guard
        else { report(e.message()) }            # everything else — a catch-all is required
    }
}
```

`is` yields only a `bool`, so a branch may use the **`Error` interface** (`message` / `code` / `unwrap`)
but **not the concrete type's own fields** — the value was erased and is never re-constructed. This phase `is`
is implemented **for the error taxonomy** (**[implemented]**) — the six built-in kinds and any built-in
abort a `guard` reifies; the general existential test `x is T` for a **non-error** type is **[not yet]**.
The set of errors reachable here is treated as **open** for coverage, so an `is`-chain can never be
exhaustive: a **catch-all is mandatory**. An unmatched error would abort like any uncovered `match` — but
`MatchError` is **[not yet]** a reified kind, and because the final `match` arm is always unconditional the
compiler never emits one today (the catch-all requirement is a static rule, not a runtime `MatchError`).

This splits error handling by whether you own a **closed** set. When you need an error's **kind** decided by
value, keep it concrete — an **`Either[T, ValueError]`** (never erased) whose right a `match` reads by value.
When you only recognize a few kinds, take the erased **`Result[T]`** and `is`-dispatch with a catch-all. So the
return type is a contract: an erased `Result` says "branch and use the `Error` interface"; a concrete `Either`
says "here is my exact error kind". (A user-defined error `enum` gathering several kinds into one closed sum
is deferred with user error types above.)
