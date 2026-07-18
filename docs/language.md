# Zerg Language Reference

The detailed semantics behind the design principles in the [README](../README.md). This page is the
**map**: each section states what a topic decides and links to its full reference. Also in
[繁體中文](language.zh-TW.md).

## Types

The scalar primitives every program starts from — `bool`, `byte`, `rune`, `int`, `uint`, `float`,
`str` — and the **product** (`struct`) and **sum** (`enum`) types, tuples, and strong-typedefs you
build on them: how a type is declared, constructed, and converted (always by re-construction, never a
reinterpret). See **[Types](types.md)**.

## Specs & Generics

How Zerg abstracts over behavior. A **`spec`** is the one mechanism — a nominal interface that serves
as a generic **bound**, a **conformance** a type declares, and a **type** in its own right (a
heap-boxed, dynamically dispatched existential). Covers the built-in specs (`Object`, `Ord`, `Hash`,
`Error`, the operators), the iteration protocol, and the `is` type test. See
**[Specs & Generics](specs.md)**.

## Decorators & compiler-derived behavior

The compiler can **write an implementation for you** from a type's **structure**, requested with a
**decorator** on the type: `#[derive(Encode, Decode)]` on a `struct`/`enum` generates the canonical,
field-by-field (and variant-by-variant) impls. What it derives is a **fixed, compiler-owned set of blessed
specs** — `Object` (always derived) and, opt-in, `Ord`, `Hash`, `Encode`, `Decode`. A **user spec can never
be derived** (`#[derive(MySpec)]` is a compile error): generating from structure needs code that reads
fields, which only the compiler may do — there are **no macros**. For anything custom, hand-write
`impl X for Y`. Decorators are Zerg's one channel for such compiler directives, and it stays closed (users
cannot define new ones). See **[Derive & Default Behavior](derive.md)**.

## Control Flow & Pattern Matching

The three constructs, split by what they yield: **`if`** and **`for`** are statements that run for
effect, **`match`** is the expression that yields a value. Plus the patterns — variants, literals,
tuples, structs, or-patterns — that a `match` (or a `:=` binding) destructures. See
**[Control Flow & Pattern Matching](control-flow.md)**.

## Values & Memory

The ownership model with no GC: every value is **scope-owned** and passed **by value**, `mut` is the
one explicit by-ref path, `del` and `defer` control cleanup timing, and a **`Ref[T]`** (or a `chan`)
is the reference-counted exception for a resource that outlives its scope. See
**[Values & Memory](memory.md)**.

## Functions & Closures

A function is a **first-class value** whose type is its input/output contract and nothing more — no
effect tracking beyond argument mutation and recoverable error. Covers default parameters, named
arguments, and closures that capture only immutable values and channels, by copy. See
**[Functions & Closures](functions.md)**.

## Formatting & Text

How a value becomes text — the structurally **auto-derived `debug`** and the human-facing
**`display`** (both `Object` methods), `f"…"` interpolation, and the always-in-scope `print`
keyword. See **[Formatting & Text](format.md)**.

## Null-safety & Errors

Failure in **two tiers**: recoverable failure is an ordinary value (`Result[T]`, `T?`), a bug is an
**abort** that unwinds the stack. The operators `?` `??` `?.` `!` `raise` and `guard` bridge the
tiers, and `is` dispatches on an erased `Err`. See **[Null-safety & Errors](errors.md)**.

## Concurrency

Zerg is concurrent through **coroutines and channels only**: `spawn` (Go's `go`) on an **M:N
scheduler**, fire-and-forget with no join/handle, capturing **only immutable values and channels**.
Channels are the reference-counted, by-ref **conduit** (a `Ref` type built for communication;
`Ref[T]` is its resource-holding sibling — see [Values & Memory](memory.md)) — payloads copied,
**auto-closed** when their last sender leaves, received as **`Result[T]`** (`Right` = closed,
carrying a crash `Err` or the `StopIteration` sentinel), and multiplexed with **`select`**.

The full model — buffering, receive/close semantics, directional ends, `select`, and deadlock — is
the **[Coroutines & Channels](coroutine.md)** reference.

## Companion references

Built on the core language above:

- **[Grammar](grammar.md)** — the formal surface grammar (W3C-EBNF), the authoritative
  [`GRAMMAR`](../GRAMMAR) file, and the nvim syntax tooling.
- **[Syntax Sugar](syntax-sugar.md)** — every convenient surface form and the core it desugars to,
  collected in one table.
- **[Collections](collections.md)** — the built-in containers `list`, `map`, `set`, and the
  fixed-size `[T; N]` array; one canonical type per role.
- **[Derive & Default Behavior](derive.md)** — the two sources of "free" behavior: the compiler's
  structural derivation and a spec's default methods, and the firm line between them.
- **[Modules, Packages & Programs](package.md)** — how source is organized into modules and
  packages, how visibility and coherence hold across them, and where a program starts.
- **[FFI](ffi.md)** — the C ABI boundary: exporting Zerg through its `pub` surface and importing C
  with `extern`.
- **[Process & I/O](io.md)** — the checked I/O surface (streams, files, stdio, processes), imported
  as the `io` package.
