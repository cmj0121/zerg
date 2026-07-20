# Zerg Decorators

A **decorator** is a `#[…]` prefix on a declaration — a directive to the compiler. The set is **fixed and
compiler-owned**: users cannot define new ones (Zerg has **no macros**), so nothing outside this page can
rewrite your code. Each decorator binds to the declaration that follows it. Part of the
[Language Reference](language.md). Also in [繁體中文](decorators.zh-TW.md).

## The set

- **`#[derive(Spec, …)]`** — on a `struct` / `enum`. Generates the canonical impl of each named blessed spec
  from the type's **structure**: `Object` (always derived) plus opt-in `Ord`, `Hash`, `Encode`, `Decode`. A
  user spec can never be derived (`#[derive(MySpec)]` is a compile error). See
  **[Derive & Default Behavior](derive.md)**.
- **`#[dyn]`** — on a generic `fn`. Compiles the generic to **one shared witness-table body** instead of
  monomorphizing per type argument — trading zero-cost for smaller code, and letting the compiler cap
  instantiation bloat. See **[Grammar](grammar.md)** (group 7).
- **`#[sealed]`** — on a `struct`. Demotes the default field-wise `T(…)` constructor to **module-private**, so
  external code must build through a public custom constructor (a named associated `fn`); the module itself
  still builds with `T(…)` internally. Pairs with private, defaulted fields to enforce an invariant.

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

The set grows only as the compiler gains directives — memory **layout** control (`#[repr]`, `#[packed]`,
`#[align]`), **logging** / instrumentation, and **FFI** are the likely next entries. Until a decorator is
listed here it is not valid syntax.
