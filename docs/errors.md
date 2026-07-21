# Zerg Null-safety & Errors

The two failure tiers — recoverable values and aborts — the operators that bridge them
(`?` `??` `?.` `!` `raise` `guard`), and how errors are handled by type. Part of the
[Language Reference](language.md). Also in [繁體中文](errors.zh-TW.md).

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
A **`raise e from cause`** form records `cause` as `e`'s `unwrap()` — a **nested** abort that wraps a
lower-level `Err` in a higher-level one without losing it, feeding the same cause chain every `Error`
exposes; a bare `raise e` carries `e` unchanged.

**Custom error types.** Any type implementing the **`Error`** spec (`message() -> str`, `unwrap() -> Err?`,
`code() -> byte?` — see [Built-in specs](specs.md)) is an `Err`: it may sit in a `Result`'s right **and** be `raise`d,
and `guard` reifies it back as `Right(e)` with message, cause, and code intact. Use a `struct` for one
error, an `enum` for a family — the same value serves both tiers; the bridges convert.

**Aborts.** An abort — a built-in (`OverflowError`, `DivideByZeroError`, `UnwrapError`, `MatchError`,
`IndexError`, `KeyError`, `AliasError`, `StackOverflowError`, `SendOnClosedError`, `DeadlockError`) or
any `Err` you `raise` — marks a **bug**, not an expected failure. It is **not catchable as control flow**: no `try`/`catch`,
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

## Handling errors by type — `is`

Both tiers deliver the **same erased `Err`** — a value-tier `Right(err)` and a `guard`-reified abort are
indistinguishable — so one mechanism dispatches on either. To act on a **specific** error, test its type
with **`is`** ([Type tests](specs.md)):

```text
match guard { work() } {
    Left(v)  => use(v)
    Right(e) => {
        if e is NotFound { rebuild() }          # branch on the concrete type
        else if e is Overflow { alert(e) }      # a built-in abort, reified by guard
        else { report(e.message()) }            # everything else — a catch-all is required
    }
}
```

`is` yields only a `bool`, so a branch may use the **`Error` interface** (`message` / `code` / `unwrap`)
but **not the concrete type's own fields** — the value was erased and is never re-constructed. The set of
errors reachable here is **open** (any `raise`, any built-in abort, any library `Err`), so an `is`-chain
can never be exhaustive: a **catch-all is mandatory**, and an unmatched error aborts (`MatchError`) like
any uncovered `match`.

This splits error handling by whether you own a **closed** set. When you need an error's **data**, keep it
concrete — an **`Either[T, MyErrorEnum]`** (never erased) whose variants a `match` reads by value, with
payloads and coverage warnings. When the set is **open**, or you only recognize a few types, take the
erased **`Result[T]`** and `is`-dispatch with a catch-all. So the return type is a contract: an erased
`Result` says "branch and use the `Error` interface"; a concrete `Either` says "here is my full error
taxonomy, data and all".
