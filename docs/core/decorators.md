# Zerg Decorators

A **decorator** is a `#[…]` prefix on a declaration — a directive to the compiler. The set is **fixed and
compiler-owned**: users cannot define new ones (Zerg has **no macros**), so nothing outside this page can
rewrite your code. Because the set is closed, an **unknown or misspelled decorator is a compile error** — it
is never silently ignored. Each decorator binds to the declaration that follows it. Part of the
[Language Reference](../language.md). Also in [繁體中文](decorators.zh-TW.md).

> **[not yet]** `#[sealed]` is refused with a code of its own — _E496 NotImplemented: the decorator
> `#[sealed]` — it is a reserved decorator … and this compiler does not build it, so the constructor stays
> public rather than being sealed in silence_. The word is the right one; what is missing is the behaviour,
> which is exactly what a reader who wrote it needs to be told.

## The set

`#[derive]`, `#[obj]` and `#[test]` are the decorators the compiler reads. Every other one —
`#[sealed]`, the layout directives — is **[not yet]** and refused by name.

- **`#[derive(Spec, …)]`** — on a `struct` / `enum`. Generates the canonical impl of each named blessed spec
  from the type's **structure**. The blessed set is **`Eq`** — built, generating a correct `==` / `!=` on a
  `struct` and on a fieldless `enum` (on a **payload** `enum` it is **[not yet]**) — together with **`Ord`**,
  **`Hash`**, **`Encode`** and **`Decode`**, each specified here and **[not yet]**: naming one is a clean
  refusal, _NotImplemented: `#[derive(Ord)]` — this compiler derives `Eq`; `Ord`, `Hash`, `Encode` and
  `Decode` are specified and unbuilt_. There is **no auto-derived `Object`**. A user spec can never be
  derived **on a struct** (`#[derive(MySpec)]` there is a compile error); on an **`enum`** any spec may be,
  because the generated impl is delegation to the payload rather than a reading of structure. See
  **[Derive & Default Behavior](derive.md)**.
- **`#[obj]`** — on a `spec`, no arguments. Generates a companion **struct of function values** and a
  **generic wrap**, which is how a heterogeneous collection is written in a language where a spec is a bound
  and never a type. A `mut fn`, a method taking `This`, and anything that is not a spec are refused by name.
  See **[Specs & Generics](specs.md)**.
- **`#[test]`** — on a `fn`. Marks the function as a test case, compiled and run **only in a test build** and
  excluded from a normal one. The function takes no parameters; a failing assertion or an abort inside it
  fails the test (see [Modules, Packages & Programs](../runtime/package.md) on where tests live).

## Recognized but not yet supported

Four more decorator names are **recognized** by the compiler but **rejected loudly** this phase — using one
is a "not yet supported" **compile error**, never a silent no-op:

- **`#[sealed]`** — on a `struct`. _Intended_ to demote the default field-wise `T(…)` constructor to
  **module-private**, so external code must build through a public custom constructor (a named associated
  `fn`) while the module still builds with `T(…)` internally — pairing with private, defaulted fields to
  enforce an invariant. **[not yet]**
- **`#[repr]`** / **`#[packed]`** / **`#[align]`** — the memory-**layout** decorators. Reserved for
  controlling in-memory width, padding, and alignment against an external ABI (see _Kept rare_ and
  [Values & Memory](memory.md)). **[not yet]**

> **[not yet]** `#[repr]` is still a reserved name with no rule of its own: it falls into the
> unknown-decorator arm and gets _E217 … this compiler reads `#[derive(…)]`, `#[obj]` and `#[test]`, and no
> other_, the same sentence a misspelled `#[frobnicate]` gets. It is refused, so nothing is silently
> dropped; what is lost is the distinction between a name awaiting implementation and a typo —
> `#[sealed]`, which had the same problem, now has `E496`.
>
> `#[test]` is **read** now, by both compilers, and `zerg test` runs what it marks (see
> [Modules, Packages & Programs](../runtime/package.md) for how far that command goes). One
> **[deviation]** is left in it: the seed strips a `#[test]` function before its checker runs, so the body
> is never type-checked there, while `zerg` checks it like any other. A test that does not compile is a
> compile error under `zerg` and silence under `zerg0` — recorded in `src/bootstrap/README.md`.

## Not a macro

A decorator only selects a **compiler-provided** behavior — it never runs user code at compile time and
cannot expand into arbitrary source. That is why the set is closed: the guarantee that no directive can
silently rewrite your program holds precisely because you cannot add one.

## Kept rare

Decorators are meant to be reached for **seldom**. Two everyday concerns are **not** their job: the
**serialized / wire form** of a value is customized by hand-writing its `Encode` / `Decode` **spec impl** (a
`#[repr]` controls in-memory width, never the bytes on a wire); and **memory layout** follows one predictable
default (declaration order, natural alignment) — you add a layout decorator only to **deviate** for an
external ABI. On the default path, day to day, you write no decorators at all.

## Reserved

The set grows only as the compiler gains directives. The layout decorators (`#[repr]`, `#[packed]`,
`#[align]`) and `#[sealed]` are already **reserved names** — recognized and rejected loudly (above) until
implemented — and **logging** / instrumentation and **FFI** are the likely next entries. Any name **not**
listed on this page is not a reserved decorator at all: it is a **compile error**, so a typo can never pass
as a directive the compiler silently drops.
