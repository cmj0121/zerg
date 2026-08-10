# Zerg Functions & Closures

Functions as first-class values, what their type does and does not track, default parameters and
named arguments, and closure capture. Part of the [Language Reference](../language.md). Also in
[繁體中文](functions.zh-TW.md).

A function is a **first-class value**: it has a type, and can be passed as an argument, returned,
stored in a field, and bound to a variable. This holds **across modules** too — a function named through
another module is an ordinary value: `f := other.helper` binds it, then `f(x)` calls it, exactly as for a
local function. A **generic** function is **not first-class until instantiated**: the un-instantiated
generic name is not itself a value — it becomes one only once its type arguments are **inferred** at a
use site.
A function type is written `fn(P...) -> R`; a parameter's
`mut &` is **part of the type**, so `fn(mut &int) -> bool` and `fn(int) -> bool` are distinct types (they
differ in calling convention — mutable-reference vs copy). Visibility is **not** part of the type: `pub`
exports a top-level function's **name**, never travels with the value, and is meaningless on an
anonymous function.

> **[not yet]** The value stops at the module boundary. A function named through another module is a **call
> target only**: `text.make(1)` compiles, and `f := text.make` written above it reports _module `text` has no
> `make`_ — the member lookup that resolves a qualified name lives on the call path, and the bare-name path
> never learned it. So a cross-module function can be called, but not bound, passed, or stored.
>
> **[not yet]** Two forms that share the indexed-callee shape are still unbuilt: a call through a function
> VALUE held in a container, `fs[0](x)`, and an optional method call, `p?.m(…)`. The third one left the
> language: an explicit type argument at a use site — `id[int](7)` — is no longer a form, since a postfix
> bracket is always an index ([Grammar](../surface/grammar.md)). The compiler still accepts it, which is the
> refusal that is unbuilt rather than the form.
>
> **[not yet]** The `mut &` distinction is real in the language and cannot be written down. A function
> **type** carrying it is rejected by the parser: `f: fn(mut &int) = bump` reports _expected `,`, found `&`_,
> though `GRAMMAR` derives `param-type ::= ( 'mut' '&' )? type`. The parameter-type parser reads a plain type
> and never the `mut &` prefix that production allows, so `fn(mut &int) -> bool` has no spelling and the two
> types stay distinct only on paper.

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

  > **[not yet]** A default that **reads an earlier parameter** — `fn g(a: int, b: int = a * 2)` — is the one
  > shape that is not built. The default is materialised at the **call site**, where the callee's parameter
  > names are not in scope, so the call reports _error: undefined name `a`_ instead of evaluating `a * 2`.
  > Every other default is lowered as specified, a bare constant and a computed expression alike:
  > `b: int = 1 + 2`, `b: int = side()` and `greeting: str = "a" + "b"` all evaluate at the call, each time.

- A **named argument** passes a parameter by its name (`loud: true`) — which is what lets you **skip a
  defaulted parameter** in the middle. The rule is the usual one: positional arguments fill left-to-right,
  any parameter may instead be given by name, a defaulted one may be omitted, and **once you name an argument
  the rest must be named too** (no positional after a name).

  > **[not yet]** Named arguments are not built at all. `greet("Sam", loud: true)` reports _NotImplemented:
  > the named argument `loud:` — this compiler binds arguments by position only_, and the rest of the
  > mechanism goes with it: there is no way to skip a defaulted parameter in the middle, and the "once you
  > name an argument the rest must be named too" rule has nothing left to govern. A call fills its parameters
  > left to right, and a defaulted one can only be dropped off the **end** of the argument list.

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
