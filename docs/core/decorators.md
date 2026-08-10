# Zerg Decorators

A **decorator** is a `#[…]` prefix on a declaration — a directive to the compiler. The set is **fixed and
compiler-owned**: users cannot define new ones (Zerg has **no macros**), so nothing outside this page can
rewrite your code. Because the set is closed, an **unknown or misspelled decorator is a compile error** — it
is never silently ignored. Each decorator binds to the declaration that follows it. Part of the
[Language Reference](../language.md). Also in [繁體中文](decorators.zh-TW.md).

> **[deviation]** A `#[derive]` or `#[derive()]` with **no argument** is accepted and silently dropped, which
> is the one outcome this page says cannot happen. The argument list is read as a loop over the spec names
> inside the brackets, so an empty list is zero iterations and nothing is either generated or refused; and
> because the check that a decorator sits on the right kind of declaration is made **per named spec**, the
> bare form skips that too — `#[derive]` above a `fn` compiles, where `#[derive(Eq)]` above the same `fn` is
> correctly rejected with _`#[derive(Eq)]` has no declaration under it_. Nothing is miscompiled, but a
> directive is read and thrown away, which is what the closed set is supposed to rule out.

## The set

`#[derive]` is the only decorator the compiler reads. Every other one — `#[test]`, `#[sealed]`, the
layout directives — is **[not yet]** and refused by name.

- **`#[derive(Spec, …)]`** — on a `struct` / `enum`. Generates the canonical impl of each named blessed spec
  from the type's **structure**. The blessed set is **`Eq`** — built, generating a correct `==` / `!=` on a
  `struct` and on a fieldless `enum` (on a **payload** `enum` it is **[not yet]**) — together with **`Ord`**,
  **`Hash`**, **`Encode`** and **`Decode`**, each specified here and **[not yet]**: naming one is a clean
  refusal, _NotImplemented: `#[derive(Ord)]` — this compiler derives `Eq`; `Ord`, `Hash`, `Encode` and
  `Decode` are specified and unbuilt_. There is **no auto-derived `Object`**. A user spec can never be
  derived (`#[derive(MySpec)]` is a compile error). See **[Derive & Default Behavior](derive.md)**.
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

> **[deviation]** The compiler does not distinguish a **recognized** decorator from an **unknown** one. Every
> `#[…]` other than `#[derive]` falls into a single arm, so `#[sealed]`, `#[repr]`, `#[test]` and
> the misspelled `#[frobnicate]` all get the same sentence — _NotImplemented: the decorator `#[X]` — this
> compiler reads `#[derive(…)]` and no other_. Every one of them is refused, so nothing is silently dropped
> and nothing miscompiles; what is lost is the distinction this section and **Reserved** below are built on.
> A typo is reported as though it were a reserved name awaiting implementation, and the promise that an
> unknown decorator is an error the reader can tell apart from a not-yet-supported one is not kept.

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
