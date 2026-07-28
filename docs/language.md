# Zerg Language Reference

The detailed semantics behind the design principles in the [README](../README.md). This page is the
**front matter and map** of the specification: it states the reading conventions, then indexes every
chapter and summarizes what each decides. Each summary links to its full reference. Also in
[繁體中文](language.zh-TW.md).

## How to read this specification

The `docs/` chapters are **normative for semantics**; the root [`GRAMMAR`](../GRAMMAR) file is normative
for **syntax**. Zerg is specified as a whole, while the Phase-1 bootstrap implements a subset, so every
feature carries a **status marker** that flags the gap between the language and the current compiler:

| Marker                       | Meaning                                                           |
| ---------------------------- | ----------------------------------------------------------------- |
| **[implemented]**            | The bootstrap compiler implements this as specified.              |
| **[not yet: Phase N]**       | Specified, not yet built; using it is a clean compile error.      |
| **[implementation-defined]** | The spec does not pin this; a conforming implementation chooses.  |
| **[deviation]**              | The bootstrap's current behavior does not match the spec (a bug). |

The **[Conformance](conformance.md)** chapter defines these markers, what "conforming" means, and the
observable contracts (diagnostics, runtime abort, undefined vs implementation-defined behavior) every
other chapter relies on. Read it first.

## Chapters

### Reading this reference

| Chapter                       | Covers                                                           |
| ----------------------------- | ---------------------------------------------------------------- |
| [Conformance](conformance.md) | reading conventions, status markers, diagnostics/abort contracts |

### The type system

| Chapter                                | Covers                                                            |
| -------------------------------------- | ----------------------------------------------------------------- |
| [Types](types.md)                      | primitives, `struct`, `enum`, tuples, strong-typedefs, conversion |
| [Values & Memory](memory.md)           | scope ownership, `mut &`, `del` / `defer`, `Ref[T]`               |
| [Specs & Generics](specs.md)           | `spec` as bound / conformance / type; generics; the `is` test     |
| [Derive & Default Behavior](derive.md) | structural derivation vs spec default methods                     |
| [Decorators](decorators.md)            | the fixed, compiler-owned `#[…]` directive set                    |

### Writing code

| Chapter                                            | Covers                                                      |
| -------------------------------------------------- | ----------------------------------------------------------- |
| [Control Flow & Pattern Matching](control-flow.md) | `if`, `for`, `match`, and patterns                          |
| [Functions & Closures](functions.md)               | first-class functions, defaults, named args, closures       |
| [Null-safety & Errors](errors.md)                  | `Result[T]` / `T?`, `?` `??` `?.` `!` `raise` `guard`       |
| [Collections](collections.md)                      | `list`, `map`, `set`, the fixed-size `[T; N]` array         |
| [Coroutines & Channels](coroutine.md)              | `spawn`, channels, `select`, scheduling                     |
| [Patterns & Idioms](patterns.md)                   | closures, pipelines, builders — the Zerg way, no new syntax |

### The surface

| Chapter                         | Covers                                              |
| ------------------------------- | --------------------------------------------------- |
| [Syntax Sugar](syntax-sugar.md) | every surface form and the core it desugars to      |
| [Grammar](grammar.md)           | the formal surface grammar (companion to `GRAMMAR`) |

### Programs, and the world outside them

| Chapter                                    | Covers                                                          |
| ------------------------------------------ | --------------------------------------------------------------- |
| [Modules, Packages & Programs](package.md) | organization, visibility, coherence, program start              |
| [Process & I/O](io.md)                     | streams, files, stdio, processes — the `io` package             |
| [Formatting & Text](format.md)             | `display` / `debug` rendering, `f"…"`, `print`                  |
| [Built-in Functions](builtins.md)          | the fixed no-import functions — `Ref`, conversions, error kinds |
| [Standard Library](stdlib.md)              | the bundled `import` packages — io, fs, os, time, math, rand, … |
| [FFI](ffi.md)                              | the C ABI boundary — `pub` export, unsafe foreign import        |

### Tooling

| Chapter                            | Covers                                                    |
| ---------------------------------- | --------------------------------------------------------- |
| [Formatter & Linter Rules](fmt.md) | every rule `zerg fmt` and `zerg lint` apply, and its code |

## Types

The scalar primitives every program starts from — `bool`, `byte`, `rune`, `int`, `uint`, `float`,
`str` — and the **product** (`struct`) and **sum** (`enum`) types, tuples, and strong-typedefs you
build on them: how a type is declared, constructed, and converted (always by re-construction, never a
reinterpret). See **[Types](types.md)**.

## Specs & Generics

How Zerg abstracts over behavior. A **`spec`** is the one mechanism — a nominal interface that serves
as a generic **bound**, a **conformance** a type declares, and a **type** in its own right (a
heap-boxed, dynamically dispatched existential). Covers the built-in specs (`Eq`, `Ord`, `Hash`, `Error`,
the operators — there is **no auto-implemented `Object` spec** and no implicit `==`: equality and ordering
are **opt-in** via `derive(Eq)` / `derive(Ord)` or a hand-written impl), the iteration protocol, and the
`is` type test (`x is T` on an existential is **[implemented]**; a general `x is T` on an arbitrary value
is **[not yet]**). See **[Specs & Generics](specs.md)**.

## Decorators & compiler-derived behavior

The compiler can **write an implementation for you** from a type's **structure**, requested with a
**decorator** on the type: `#[derive(Encode, Decode)]` on a `struct`/`enum` generates the canonical,
field-by-field (and variant-by-variant) impls. What it derives is a **fixed, compiler-owned set of blessed
specs** — all **opt-in**: `Eq`, `Ord`, `Hash`, `Encode`, `Decode` (there is no always-derived `Object`).
A **user spec can never be derived** (`#[derive(MySpec)]` is a compile error): generating from structure needs code that reads
fields, which only the compiler may do — there are **no macros**. For anything custom, hand-write
`impl X for Y`. Decorators are Zerg's one channel for such compiler directives, and it stays closed (users
cannot define new ones). `derive` is one of a small fixed set — `#[dyn]`, `#[sealed]`, and more — listed in
**[Decorators](decorators.md)**. See also **[Derive & Default Behavior](derive.md)**.

## Control Flow & Pattern Matching

The three constructs, split by what they yield: **`if`** and **`for`** are statements that run for
effect, **`match`** is the expression that yields a value. Plus the patterns — variants, literals,
tuples, structs, or-patterns — that a `match` (or a `:=` binding) destructures. See
**[Control Flow & Pattern Matching](control-flow.md)**.

## Values & Memory

The ownership model with no GC: every value is **scope-owned** and passed **by value**, `mut &` is the
one explicit by-ref path, `del` and `defer` control cleanup timing, and a **`Ref[T]`** (or a `chan`)
is the reference-counted exception for a resource that outlives its scope. See
**[Values & Memory](memory.md)**.

## Functions & Closures

A function is a **first-class value** whose type is its input/output contract and nothing more — no
effect tracking beyond argument mutation and recoverable error. Covers default parameters, named
arguments, and closures that capture only immutable values and channels, by copy. See
**[Functions & Closures](functions.md)**.

## Built-in functions

A small, **fixed** set of compiler-recognized functions need no `import` — the only free-function calls
the language itself provides. A user cannot add to the set.

- **`Ref(x)` / `deref(r)`** — construct and read the reference-counted box ([Values & Memory](memory.md)).
- **Primitive conversion `T(x)`** — `int` / `uint` / `float` / `bool` / `byte` / `rune` (and the
  fixed-width `i8`…`i64` / `u8`…`u64` / `f32` / `f64`): a **re-construction** with range checks, never a
  reinterpretation; `int("…")` additionally **parses** a decimal string ([Types](types.md)).
- **`str(bytes)` / `str(runes)`** and **`list[byte](s)` / `list[rune](s)`** — the str ⇄ list bridges,
  validating the `str` invariant ([Collections](collections.md)).
- **Error constructors** — the fixed `ValueError` / `OverflowError` / `IOError` / `EncodingError` /
  `IndexError` / `KeyError`, each building an `Err` of that kind ([Null-safety & Errors](errors.md)).
- **Raw-pointer builtins (`unsafe` only)** — `addr` / `ptr` / `ptr[T]` / `uint(p)`, and the pointer
  methods `.load` / `.store` / `.offset` ([Values & Memory](memory.md)).

Everything else that looks callable is **not** a built-in function: `print` / `raise` / `guard` / `spawn`
/ `defer` / `del` are **keywords**; `list.len()` / `map.get()` are **methods** on a built-in type; and
`math.sqrt` / `io.read_file` are **stdlib** functions reached with `import`. The per-function detail is in
**[Built-in Functions](builtins.md)**; the importable packages are in **[Standard Library](stdlib.md)**.

## Formatting & Text

How a value becomes text — the structural **`debug`** and the human-facing **`display`**, which are
**built-in value renderings** (not methods of any `Object` spec), `f"…"` interpolation, and the
always-in-scope `print` keyword. See **[Formatting & Text](format.md)**.

## Null-safety & Errors

Failure in **two tiers**: recoverable failure is an ordinary value (`Result[T]`, `T?`), a bug is an
**abort** that unwinds the stack. The operators `?` `??` `?.` `!` `raise` and `guard` bridge the
tiers, and `is` dispatches on an erased `Err`. See **[Null-safety & Errors](errors.md)**.

## Concurrency

Zerg is concurrent through **coroutines and channels only**: `spawn` (Go's `go`), fire-and-forget with
no join/handle, capturing **only immutable values and channels**. The intended scheduler is a preemptive
**M:N** one, but the bootstrap runs a cooperative **N:1** single thread today (**[deviation]** — a
CPU-bound coroutine that never parks starves the rest; see [Coroutines & Channels](coroutine.md)).
Channels are the reference-counted, by-ref **conduit** (a `Ref` type built for communication;
`Ref[T]` is its resource-holding sibling — see [Values & Memory](memory.md)) — payloads copied,
**auto-closed** when their last sender leaves, received as **`Result[T]`** (`Right` = closed,
carrying a crash `Err` or the `StopIteration` sentinel), and multiplexed with **`select`**.

The full model — buffering, receive/close semantics, directional ends, `select`, and deadlock — is
the **[Coroutines & Channels](coroutine.md)** reference.
