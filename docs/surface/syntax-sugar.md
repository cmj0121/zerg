# Zerg Syntax Sugar

Zerg keeps a **small core** and layers a few convenient surface forms on top — each is **sugar** that
desugars to that core, so there is nothing new to learn semantically. This page collects them; each topic's
full treatment is in the [Language Reference](../language.md). Also in [繁體中文](syntax-sugar.zh-TW.md).

## Landed sugar

| Sugar                              | Desugars to                                                             |
| ---------------------------------- | ----------------------------------------------------------------------- |
| `break if c` / `continue if c`     | `if c { break }` / `if c { continue }`                                  |
| `raise e if c`                     | `if c { raise e }` — the same postfix guard, on the fourth diverge      |
| `if x := e { … }`                  | a one-arm `match` on `e` — the block runs only when `x` is present      |
| `with e as y { … }`                | `{ y := e; … }` (the block's own exits already cover it)                |
| `f"…{x}…"`                         | compile-time `str` concatenation, each hole `x.display()`               |
| `f"{x!r}"` / `f"{x=}"`             | `f"{x.debug()}"` / the source text `x=` then the value                  |
| `f"{x:spec}"`                      | `x.format(spec)` through the `Format` protocol                          |
| `a + b`, `a == b`, `a[i]`, `-a`, … | the operator's spec method — `a.add(b)`, `a.eq(b)`, …                   |
| `for x in it { … }`                | the iteration protocol on `it` (a `StopIteration`-terminated loop)      |
| `x..y` / `x..=y` / `x..`           | `range(x, y)` / `range(x, y + 1)` / an open range (all builtin)         |
| `v in r`                           | `r.contains(v)` — membership (Range by `Ord`, else by iteration)        |
| `lo..hi =>` (match arm)            | a `_ if _ in lo..hi` arm — matches by containment, not `equal`          |
| `xs[k]`                            | `Indexable[type of k].index(k)` — element, slice (`Range`), or map key  |
| `p: T = e` (default)               | the argument a call omits; the default `e` is evaluated per call        |
| `print x`                          | a best-effort write of `x.display()` and a newline to stdout            |
| `e?`                               | unwrap the `Left`, else early-return the `Right` from the function      |
| `a ?? b` / `a?.m` / `e!`           | default; optional chain to `nil`; force-unwrap or raise `UnwrapError`   |
| `del ch`                           | revoke the name **and** drop this holder (to end a stream: `close(ch)`) |
| `assert c`                         | operand temporaries, then `raise AssertionError(<message>) if not (c)`  |

**Status.** Every row above works, `del ch` and the operator rows on a user-defined type apart. An
f-string hole reads all three of its tails — a **conversion** (`!r` / `!s` / `!a`), a **format spec**
(`{x:.2f}`) and the self-documenting `f"{x=}"` — and a **composite** hole renders structurally, `f"{p}"`
being `P(x: 1)`, through the same generator `print` and `str(x)` reach; see
[Formatting & Text](../runtime/format.md). What the DESUGARED spelling of a spec would be is still a gap:
`x.format(spec)` written out is _E9107 NotImplemented: the method `format` on a int_, because the `Format`
protocol this row names is not a declaration any type carries yet — so `zerg desugar` writes out every
hole but that one ([Desugar](../tooling/desugar.md)).

**`del ch`** is **[not yet]**: _E9066 NotImplemented: `del ch` on a CHANNEL_, which points at `close(ch)`
and at the release the binding's scope already performs. And the **operator** row desugars only where the
operator is compiler-owned: no operator `spec` is declared, so `impl Add for P` is _E3013 no spec named
`Add`_ and `P(1) + P(2)` is _E3043_ — see [Specs & Generics](../core/specs.md). `==` is the exception, via
`#[derive(Eq)]` or a hand-written `impl Eq`. Both command literals desugar to `os.command(argv)` — a child
process and its three streams, with the interpolating `` f`…` `` splicing each hole in as ONE argument
([Process & I/O](../runtime/io.md)). Each desugaring above is otherwise exactly as written.

**Sugar the grammar has and this table does not.** One rewrite the grammar derives is **[not yet]**, and so
is absent above rather than listed as landed: a **named argument** `f(x: 1)` (`E9010`), arguments binding by
position only. A default parameter is the half of that row that did land, and is above. The whole list of
forms in this state, with the gate that holds it, is
[What is specified and not built](grammar.md#what-is-specified-and-not-built).

## Undoing it

`zerg desugar` rewrites the sugar in this table back into the core it desugars to, and
`make desugar` builds and runs both forms to check that they are the same program — because
the compiler lowers each surface form directly, so the core form a row here NAMES goes down a
different path in the emitter, and nothing compared the two.

Four rows are undone today: the postfix guard, the while-`for`, the range-`for`, and `assert`. The
rest decline, and each decline is a measured reason rather than a gap — `for x in xs` needs the type
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
