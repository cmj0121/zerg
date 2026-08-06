# Zerg Syntax Sugar

Zerg keeps a **small core** and layers a few convenient surface forms on top — each is **sugar** that
desugars to that core, so there is nothing new to learn semantically. This page collects them; each topic's
full treatment is in the [Language Reference](../language.md). Also in [繁體中文](syntax-sugar.zh-TW.md).

## Landed sugar

| Sugar                                    | Desugars to                                                             |
| ---------------------------------------- | ----------------------------------------------------------------------- |
| `break if c` / `continue if c`           | `if c { break }` / `if c { continue }`                                  |
| `raise e if c`                           | `if c { raise e }` — the same postfix guard, on the fourth diverge      |
| `if x := e { … }`                        | a one-arm `match` on `e` — the block runs only when `x` is present      |
| `with e as y { … }`                      | `{ y := e; defer y's Scoped teardown; … }` (runs on every exit)         |
| `f"…{x}…"`                               | compile-time `str` concatenation, each hole `x.display()`               |
| `f"{x!r}"` / `f"{x=}"`                   | `f"{x.debug()}"` / the source text `x=` then the value                  |
| `f"{x:spec}"`                            | `x.format(spec)` through the `Format` protocol                          |
| `a + b`, `a == b`, `a[i]`, `-a`, …       | the operator's spec method — `a.add(b)`, `a.equal(b)`, …                |
| `for x in it { … }`                      | the iteration protocol on `it` (a `StopIteration`-terminated loop)      |
| `x..y` / `x..=y` / `x..`                 | `range(x, y)` / `range(x, y + 1)` / an open range (all builtin)         |
| `v in r`                                 | `r.contains(v)` — membership (Range by `Ord`, else by iteration)        |
| `lo..hi ->` (match arm)                  | a `_ if _ in lo..hi` arm — matches by containment, not `equal`          |
| `xs[k]`                                  | `Indexable[type of k].index(k)` — element, slice (`Range`), or map key  |
| `(a, b) := e` / `P{x, y} := e`           | destructuring a product/tuple return, each part bound **by copy**       |
| `f(x: 1)` (named) / `p: T = e` (default) | positional rewrite at the call; a default `e` is evaluated per call     |
| `print x`                                | a best-effort write of `x.display()` and a newline to stdout            |
| `e?`                                     | unwrap the `Left`, else early-return the `Right` from the function      |
| `a ?? b` / `a?.m` / `e!`                 | default; optional chain to `nil`; force-unwrap or raise `UnwrapError`   |
| `del ch`                                 | revoke the name **and** drop this holder (to end a stream: `close(ch)`) |

**Status.** Every row above works except inside an f-string hole, where only the plain `{x}` form does.
A **conversion** (`!r` / `!s` / `!a`), a **format spec** (`{x:.2f}`), and the self-documenting `f"{x=}"`
are each **[not yet]** and refused by name. A **composite** hole (a `struct`, `list`, or `map`) is
rejected too, so structural rendering is **[not yet]** — see
[Formatting & Text](../runtime/format.md). The interpolating command literal `` f`…` `` (grammar, not listed here) is
likewise **[not yet]**. Each desugaring above is otherwise exactly as written.

## Undoing it

`zerg desugar` rewrites the sugar in this table back into the core it desugars to, and
`make desugar` builds and runs both forms to check that they are the same program — because
the compiler lowers each surface form directly, so the core form a row here NAMES goes down a
different path in the emitter, and nothing compared the two.

Three rows are undone today: the postfix guard, the while-`for`, and the range-`for`. The rest
decline, and each decline is a measured reason rather than a gap — `for x in xs` needs the type
of `xs`, and a range arm's core form does not currently build. See
[Desugar Rules](../tooling/desugar.md).

## What is deliberately **not** sugar

To keep the core honest, some look-alikes are their own thing, not rewrites:

- **`type X = Y`** is a **strong typedef** — a new, distinct type, not a transparent alias.
- **`+%` / `-%` / `*%`** are **distinct wrapping operators**, not a modifier on `+` / `-` / `*`.
- **`#[derive(X)]`** is a compiler **code generator** keyed on a blessed spec — it reads the type's structure
  to emit the impl. It is **not** sugar for an empty `impl` (see [Derive & Default Behavior](../core/derive.md)).
- **`#[…]` decorators** are a **fixed, compiler-owned** set of directives, not user-definable macros —
  Zerg has **no macros**, so no user sugar can rewrite your code.
