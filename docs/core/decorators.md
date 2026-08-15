# Zerg Decorators

A **decorator** is a `#[…]` prefix on a statement — a directive to the compiler. The set is **fixed and
compiler-owned**: users cannot define new ones (Zerg has **no macros**), so nothing outside this page can
rewrite your code. Because the set is closed, an **unknown or misspelled decorator is a compile error** — it
is never silently ignored. Each decorator binds to the statement that follows it. Part of the
[Language Reference](../language.md). Also in [繁體中文](decorators.zh-TW.md).

## Shape

Three rules hold for every decorator, whatever it names.

- **It leads a statement**, and a declaration is one — so `#[derive(Eq)]` above a `struct` and
  `#[allow(L103)]` above a binding are the same form (`statement`, `decorated-decl`,
  [`GRAMMAR`](../../GRAMMAR) group 1). **Which** decorator is legal where is a **semantic** rule, and a
  short one: `#[derive]`, `#[obj]` and `#[test]` are about a **declaration**, and above a plain statement
  each is refused by name — _E612 `#[derive(Eq)]` applies to the `struct`, `enum` or `spec` that follows
  it, and a statement is not one_. `#[allow(…)]` is the one that belongs on a statement.
- **One decorator per item.** An item that wants several writes the **comma list** — `#[allow(L601), test]`
  — and stacking one over another is a compile error: _E613 a second decorator on one item — an item takes
  ONE decorator, so merge them into its comma list_. Two spellings for one thing is what `zerg fmt` exists
  to remove, and it cannot remove one once both are legal.
- **It stands on its own line.** A decorator is an item of the statement list like any other, so a
  separator divides it from what it leads; `#[derive(Eq)] struct P` on one line is not a form.

## The set

`#[derive]`, `#[obj]`, `#[test]`, `#[fixture]` and `#[allow]` are the decorators the compiler reads. Every
other one — `#[sealed]`, the layout directives — is **[not yet]** and refused by name (Reserved, below).

- **`#[derive(Spec, …)]`** — on a `struct` / `enum`. Generates the canonical impl of each named blessed spec
  from the type's **structure**. The blessed set is **`Eq`** — built, generating a correct `==` / `!=` on a
  `struct` and on a fieldless `enum` — together with **`Ord`**, **`Hash`**, **`Encode`** and **`Decode`**,
  each specified here and **[not yet]**: naming one is a clean refusal, _E436 NotImplemented:
  `#[derive(Ord)]` — this compiler derives `Eq`; `Ord`, `Hash`, `Encode` and `Decode` are specified and
  unbuilt_. `Eq` on a **payload** `enum` is **[not yet]** by a code of its own, _E438 … it carries a payload
  (`A`), and this compiler derives equality for a fieldless enum_. There is **no auto-derived `Object`**.
  A user spec can never be derived **on a struct** — `E437` — while on an **`enum`** any spec may be,
  because the generated impl is delegation to the payload rather than a reading of structure. See
  **[Derive & Default Behavior](derive.md)**.
- **`#[obj]`** — on a `spec`, no arguments. Generates a companion **struct of function values** and a
  **generic wrap**, which is how a heterogeneous collection is written in a language where a spec is a bound
  and never a type. A `mut fn`, a method taking `This`, and anything that is not a spec are refused by name.
  See **[Specs & Generics](specs.md)**.
- **`#[test]`** — on a `fn`. Marks the function as a test case, run by `zerg test` and by nothing else. It
  **returns nothing** and takes a **`testing.Context`** (by type), the **fixtures** it needs (by name), or no
  parameter at all; a failing assertion or an abort inside it fails the test (see
  [Modules, Packages & Programs](../runtime/package.md) on where tests live). A declared return type is
  **refused** by `zerg test`, with a place: the driver calls a test as a statement, so the value would be
  dropped, and a reader who thinks it is the verdict has to be told it is not. It may be written
  **anywhere**, and `zerg test` **discovers it wherever it is** — a directory whose only `#[test]` sits in an
  ordinary module file is still a test package. Written outside a `*_test.zg` it is legal and it **ships**:
  it is compiled into the binary like any other function and nothing in the **program** calls it, so
  `zerg lint` warns about it (**L601**, see [fmt & lint](../tooling/fmt.md)). Both, not one — the linter says
  where a test ought to live, and the runner runs what is written.
- **`#[fixture]`** — on a `fn`, and it belongs in a `*_test.zg`. Marks the function as something `zerg test`
  **builds for the tests that name it**. It takes its tests as a **continuation**: one parameter of type
  `fn (T)`, identified by type, which is both where those tests run and the declaration of what the fixture
  **produces**. Every other parameter **names another fixture**. Teardown is `defer`, so the runner supplies
  nothing for it. It is read **wherever it is written**, exactly as a `#[test]` is — a fixture beside a test in
  an ordinary module file serves that test rather than being silently absent from it — and the same **L601**
  applies, since a `#[fixture]` outside a `*_test.zg` ships exactly as a `#[test]` does. See
  [Modules, Packages & Programs](../runtime/package.md).
- **`#[allow(Lxxx, …)]`** — on any **statement**, declarations included. Suppresses the named **lint**
  findings over that statement, and over its block when it has one: the scope is the size of the statement
  it leads, which is one rule rather than a choice between a line and a scope. It does not reach the next
  statement and cannot reach another file — there is deliberately **no file-level scope**.

  It names **`L` codes only**. An `E` code is a **compiler diagnostic** and `#[allow]` never suppresses one:
  a program able to switch a compiler check off would make bypassing one an official feature. A lint finding
  is advisory, so suppressing one is legitimate.

  It is the one decorator the **compiler reads and never uses**. The parser accepts the name and attributes
  no meaning to it — the catalogue of codes belongs to the linter, and a copy of it in the compiler would be
  the second place a language fact is written down. The linter therefore says two things about a suppression
  itself: **L106** (**info**) when it had nothing to suppress, and **L107** (**warning**) when it names a
  code no rule has. An `#[allow]` naming no code at all is refused outright — _E614_.

## Reserved, and what a reserved name actually gets

Four names are **specified and unbuilt**, and only one of them is a name the compiler knows:

- **`#[sealed]`** — on a `struct`. _Intended_ to demote the default field-wise `T(…)` constructor to
  **module-private**, so external code must build through a public custom constructor (a named associated
  `fn`) while the module still builds with `T(…)` internally — pairing with private, defaulted fields to
  enforce an invariant. **[not yet]**, with a code of its own: `E496`.
- **`#[repr]`** / **`#[packed]`** / **`#[align]`** — the memory-**layout** decorators, for in-memory
  width, padding and alignment against an external ABI (see _Kept rare_ and
  [Values & Memory](memory.md)). **[not yet]**

> **[not yet]** The layout three are **reserved on this page and nowhere in the compiler**. `#[repr]` has
> no rule of its own: it falls into the unknown-decorator arm and gets _E217 … this compiler reads
> `#[derive(…)]`, `#[obj]`, `#[test]`, `#[fixture]` and `#[allow(…)]`, and no other_ — the same sentence a
> misspelled `#[frobnicate]` gets. Nothing is silently dropped; what is lost is the distinction between a
> name awaiting implementation and a typo, which is exactly what `#[sealed]`'s `E496` bought back.
>
> **[deviation]** `#[test]` is read by both compilers, but the **seed strips a `#[test]` function before
> its checker runs**, so the body is never type-checked there while `zerg` checks it like any other. A test
> that does not compile is a compile error under `zerg` and silence under `zerg0` — recorded in
> `src/bootstrap/README.md`.

The set grows only as the compiler gains directives; **logging** / instrumentation and **FFI** are the
likely next entries. Any name **not** listed on this page is not a reserved decorator at all — it is a
compile error, so a typo can never pass as a directive the compiler silently drops.

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
