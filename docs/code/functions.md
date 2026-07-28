# Zerg Functions & Closures

Functions as first-class values, what their type does and does not track, default parameters and
named arguments, and closure capture. Part of the [Language Reference](../language.md). Also in
[繁體中文](functions.zh-TW.md).

A function is a **first-class value**: it has a type, and can be passed as an argument, returned,
stored in a field, and bound to a variable. This holds **across modules** too — a function named through
another module is an ordinary value: `f := other.helper` binds it, then `f(x)` calls it, exactly as for a
local function. Binding a **bare top-level function** as a value, and doing so **across modules**, are both
**[implemented]**. A **generic** function is **not first-class until instantiated**: the un-instantiated
generic name is not itself a value — it becomes one only once its type arguments are fixed at a use site.
A function type is written `fn(P...) -> R`; a parameter's
`mut &` is **part of the type**, so `fn(mut &int) -> bool` and `fn(int) -> bool` are distinct types (they
differ in calling convention — mutable-reference vs copy). Visibility is **not** part of the type: `pub`
exports a top-level function's **name**, never travels with the value, and is meaningless on an
anonymous function.

**A function's type is its input/output contract, and only that.** It reveals its parameters — with `mut &`
marking the one argument-level effect, a mutable reference that writes back to the caller — and its result, where a
recoverable failure shows as a `Result` / `Either`. It tracks **no other effect**: whether a function does
I/O, reads ambient state (clock, randomness, `env`), or may **abort** never appears in a signature. I/O is
visible only through a file's `import`; an abort, being possible in nearly every expression (an overflow, a
bad index), would be noise on every signature. Effects beyond argument mutation and recoverable error are
untracked **by design** — Zerg is procedural-first here — not by omission.

The mutability of the binding that _holds_ a function is the ordinary per-instance axis — `mut f := …`
is rebindable, `f := …` is not — and is orthogonal to everything above.

## Default parameters & named arguments

Zerg has **no overloading** — one name is one function — so the flexibility overloading usually buys comes
from the **call site** instead: **default parameters** and **named arguments**, together the sanctioned way
to say "this input is optional."

```text
fn greet(name: str, greeting: str = "Hello", loud: bool = false) -> str { … }

greet("Sam")                 # greeting = "Hello", loud = false
greet("Sam", loud: true)     # greeting defaults; loud given by name
greet("Sam", "Hi", true)     # all positional
```

- A parameter may declare a **default** — the expression the call uses when the argument is omitted. It is
  **evaluated at the call site each time** it is used, never once at definition, so there is no
  shared-mutable-default trap; it is an ordinary expression and may read earlier parameters (evaluation is
  left-to-right). A parameter with no default stays **mandatory**.

  > **[deviation]** Today only a **self-contained simple constant** default (a literal such as `443` or
  > `"Hello"`) is lowered correctly. A **non-trivial** default expression — one built from an operator or a
  > call, e.g. `greeting: str = "a" + "b"` — is currently **mishandled** rather than evaluated per call as
  > specified. Keep defaults to simple constants until this is fixed.

- A **named argument** passes a parameter by its name (`loud: true`) — which is what lets you **skip a
  defaulted parameter** in the middle. The rule is the usual one: positional arguments fill left-to-right,
  any parameter may instead be given by name, a defaulted one may be omitted, and **once you name an argument
  the rest must be named too** (no positional after a name).

Because a parameter can be selected by name, **the name is part of the function's contract** — renaming it
breaks callers, exactly as changing a type would. Yet neither defaults nor names ride in the _type_:
`fn(str, str, bool) -> str` is the type, defaults live in the declaration and names in the parameter list —
consistent with a type being the input/output contract and only that. Both are **call-site sugar**: across
the C ABI (see [FFI](../runtime/ffi.md)) an exported function is all-positional with no defaults.

A **variadic** parameter is deliberately **not** offered — pass a `list[T]` explicitly (`sum(xs: list[int])`,
called `sum([1, 2, 3])`). This keeps the call model and the C ABI flat, and matches the no-variadics stance
already taken for formatting; `print` stays a built-in construct, not a user-definable variadic.

**Closures capture by the same rule as `spawn`: only immutable values and channels, copied in.** Capturing
an **immutable** value — a plain scalar, or a **non-POD** value (a `list` / `map` / `str`, a `Ref`, or a
boxed value) — is **[not yet]**, along with the rest of closures; capturing a **`mut`** binding is
**[not yet]** too — snapshot it into an
immutable binding first (`snap := n`). Capture is **by copy** in meaning — a captured channel is
refcount-bumped, and a **non-POD immutable value** is **retained into the closure's refcounted environment**
rather than eagerly deep-cloned, a plain scalar simply copied — so a closure that escapes its defining scope
carries its own captures and can never dangle. Because every capture is immutable, retaining versus cloning
is unobservable. Equivalently:

> A closure is a scope-owned struct whose fields are its captures.

So copy, free, and channel-refcount all fall out of the existing memory rules with nothing
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

Two classic closure hazards are ruled out by construction. A plain `for x in xs` variable is a **fresh
immutable binding each iteration** (a copy of that element), and a capture copies the value — so a closure
capturing it keeps **its own iteration's value**, no shared-loop-var bug and no snapshot needed (a
`for mut x`, the in-place form, is `mut` and so, like any `mut`, uncapturable — snapshot it first):

```text
for x in xs {
    spawn fn() { handle(x) }       # each coroutine gets its own iteration's value
}
```

And because captures are always immutable copies, "captured the variable or the value?" has no
observable answer — the captured value can never change, so the question disappears.
