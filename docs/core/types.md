# Zerg Types

The primitive scalars, the `struct` / `enum` / tuple you declare, how a value is constructed, and
how one type converts to another. Part of the [Language Reference](../language.md). Also in
[繁體中文](types.zh-TW.md).

## Primitive Types

A small, fixed set — three integer widths (signed `int`, unsigned `uint`, and the `byte` octet). The
**fixed-width ladder** beyond them (`i8`, `i16`, `u32`, `f64`, …) is a set of **stdlib types, not new
syntax** — a type name is just an identifier, so a width like `u32` adds a stdlib type without touching
the grammar:

| Type    | Description                                          |
| ------- | ---------------------------------------------------- |
| `bool`  | `true` / `false`                                     |
| `byte`  | unsigned 8-bit — Zerg's char                         |
| `rune`  | a single valid Unicode code point                    |
| `int`   | signed 64-bit integer                                |
| `uint`  | unsigned 64-bit integer                              |
| `float` | IEEE-754 double (f64)                                |
| `str`   | immutable, null-terminated Unicode (no embedded NUL) |
| `nil`   | the placeholder value of `T?`                        |

- **Integer overflow and division by zero raise** (`OverflowError`, `DivideByZeroError`) — an
  **abort**, not a value (see [Null-safety & Errors](../code/errors.md)); `int`/`uint`/`byte`/`rune` never
  wrap (opt into roll-over with `+%`/`-%`/`*%`, below).
- **`float` is IEEE-754:** overflow → `±Inf`, invalid → `NaN`, neither raises; `NaN` is unequal to
  everything (including itself).
- **`str` iterates as `rune` and is not indexable** — convert it to `list[byte]` (see
  [Collections](../code/collections.md)) when you want raw bytes (or
  binary that may contain a NUL, which a `str` never holds).

### Integer operations

- **Bitwise** — `&`, `|`, `^`, `~` (and, or, xor, complement) and the shifts `<<`, `>>`, on `int`/`uint`/`byte`.
  `>>` is **arithmetic** on signed `int` (sign-extends) and **logical** on unsigned `uint`/`byte`
  (zero-fills) — the type's sign decides, so no separate logical-shift operator exists; a shift by **≥ the
  type width** raises (`OverflowError`). These desugar to specs (a user type may overload them — see
  [Built-in specs](specs.md)), and the bitwise **symbols** never collide with the logical **keywords**
  `not`/`and`/`or`.
- **Wrapping** — `+`, `-`, `*` raise on overflow; the **`%`-suffixed** `+%`, `-%`, `*%` (and unary `-%`)
  instead **wrap modulo 2^n** — for hashing, checksums, and bit-mixing where roll-over is the intent. The
  **checked** form is already `guard { a + b }` → `Result` (no `checked_*` API); **saturating** is deferred.
- **Mixed `int`/`uint` is never implicit** — `int + uint` is a compile error (no implicit conversion,
  which also sidesteps C's signed/unsigned comparison traps); cast one side (`int(u) + i`).
- **Division & remainder** — `/` and `%` follow the **Euclidean** definition: the remainder is **always
  non-negative** (`0 ≤ a % b < |b|`) and `a == (a / b) * b + a % b` holds for every sign, so `a % n` is a
  valid index or bucket for any `b`. This is the canonical mathematical `div`/`mod`, not C's
  sign-of-dividend truncation; the compiler emits the small correction only when an operand may be
  negative and **elides it when both are non-negative** (the common case, zero overhead). `a / 0` and
  `a % 0` raise `DivideByZeroError`, and `INT_MIN / -1` overflows (`OverflowError`); truncating and
  flooring variants are stdlib (deferred).

> **[deviation]** The correction that makes `/` and `%` Euclidean is emitted **unconditionally**, not
> elided when both operands are provably non-negative — the "zero overhead in the common case" above is
> the intended codegen, not today's. The semantics are unaffected: it is a cost, not a wrong answer.

### Numeric literals

A numeric literal is **untyped** — it adopts the type its context demands (a typed binding `x: uint = 5`,
a typed parameter, a `return`, or the other operand of a typed expression), checked **at compile time**.
Unconstrained, an integer literal defaults to `int` and a fractional/exponent literal (`1.0`, `1e3`) to
`float`; the non-decimal bases `0x…` / `0o…` / `0b…` are ordinary integer literals.

- A literal that **does not fit** its required type is a **compile error** (`byte = 300`, `uint = -1`, an
  `int` literal past i64) — never a runtime overflow.

  > **[deviation]** The fit-check today covers only the **fixed-width ladder** (`i8` / `u16` / … ). The
  > **named** primitives `int` / `uint` / `byte` / `rune` are **not** range-checked, so `byte = 300` and
  > `uint = -1` are currently **accepted** (an `int` literal past i64 is still rejected). The rule stands
  > as specified; the bootstrap does not yet enforce it for the named primitives.

- **A typed `float` context accepts an untyped integer literal**: `x: float = 1` is legal — the `1` adopts
  `float` like any untyped literal adopting its context. What never happens implicitly is an
  already-typed **`int` value** becoming a `float`; that needs `float(i)` (no silent int→float, which
  could lose precision). A fractional or exponent literal (`1.0`, `1e3`) is a `float` from the start and
  never an `int`.

## User-Defined Types

Declare your own **product types** (`struct`) and **sum types** (`enum`), each generic over `[...]`.

**Visibility (`pub`)** — every declaration (a type, a field, a function) is **private to its module
by default**; prefix it with `pub` to export it for use elsewhere. Mutability is a separate axis and
is **not** declared here: it belongs to the **instance** (the binding; see [Values & Memory](memory.md)), never to
a field or type. What a module and a package are — and how visibility, coherence, and the program
entry point work across them — is the [Modules, Packages & Programs](../runtime/package.md) reference.

```text
struct Node {
    value: int
    next:  Node?            # self-referential — auto-boxed (see Values & Memory)
}

enum Either[X, Y] {         # generic sum type
    Left(X)
    Right(Y)
}
```

**Recursive and self-referential types** work directly — a `struct Node { next: Node? }`, an
`enum Expr { Num(int); Add(Expr, Expr) }` — with **no pointer**: the compiler auto-boxes the self-referential
slot behind a refcounted cell, so such a value copies **by reference** (refcount-shared), not by deep clone.
Its MVP limits (a `mut`-built cycle leaks; a long chain frees in O(depth)) are the [Values & Memory](memory.md)
reference.

`Either`, `Result[T]`, and `T?` aren't special — they're ordinary stdlib types built on `enum`
(see [Null-safety & Errors](../code/errors.md)). An `enum`'s **variants share the type's visibility** — a
`pub enum` exposes every variant, to construct and to `match`; there is no per-variant privacy.

An `enum`'s **discriminant behaves differently for a fieldless enum than for a payload one** — the split
turns on whether _every_ variant is fieldless. A **fieldless** `enum` may give a variant an explicit
`= <discriminant>` — a **compile-time-constant integer**, distinct across variants (an unspecified one is
the previous `+ 1`, counting from `0`) — making it a **C-style integer enum**: `variant = <int>`. Such an
enum has a **native, C-compatible integer repr** (backing `int` by one default rule, no annotation needed);
the **enum name is a value namespace** — `Color.Green` names the variant and `Color.of(n)` reverses a number
— with `int(v)` **reading** the discriminant and `E.of(n) -> E?` **reversing** it (an unknown `n` yielding
`nil`, never a wrong variant). A specific width is the opt-in layout decorator `#[repr]` (**[not yet]** —
reserved and rejected loudly today, see [Decorators](decorators.md)); the serialized/wire form is the
`Encode` / `Decode` impl (**[not yet]**), never a decorator.

A **payload** `enum` (any variant carries fields) keeps its **tag opaque and match-only** — no `= 5` is
allowed, and you `match` on the variant, never on a tag. To bind such a variant to a specific integer,
write an **explicit conversion**: a `match` from the variant to the number, and one back that **validates**.
This is _convert by re-construction, never reinterpret_ again — the number is _built_ from the variant, not
the tag's bytes reread — and it absorbs the non-contiguous values, aliases, and versioning a baked-in value
cannot.

A **tuple** — `(int, str)`, its fields reached positionally as `.0`, `.1` — is nothing but an **anonymous
`struct`**: the same product type, spelled without a name for a transient positional bundle (a multiple
return, a `divmod -> (int, int)`). Being anonymous it is the language's **one structurally typed** form —
`(int, str)` is the same type wherever written, while every named `struct` and `enum` stays **nominal**.
It rides the whole product machinery — copy-by-value, and the compiler's structural `Eq` / `Ord` /
`Hash` / … derivation (see [Specs & Generics](specs.md)) — but, with no name to attach one to, carries **no inherent
methods and no `spec` impl of its own**: reach for a named `struct` the moment a value wants behavior, a
nominal identity, or field names worth reading. A tuple result is **first-class** — stored, passed, or
destructured — so multiple return needs no separate mechanism ([Pattern matching](../code/control-flow.md)).

**`type X = Y`** defines a **new, distinct type** — not a transparent alias. `X` takes on `Y`'s
representation and implementation (its fields or variants, and its `spec` impls, now with `This` = `X`), yet
is a **separate identity**: `X` and `Y` are **different types even when structurally identical**, and there
is **no cast** between them — you convert by **re-construction** (`X(y)` / `Y(x)`), like any conversion. A
**monomorphic** `type X = Y` **lowers to `Y`** at runtime — the distinctness is **compile-time only**, so a
`Celsius = int` costs nothing (no box, no wrapper) yet a `Celsius` is never an `int` without an explicit
`int(c)` / `Celsius(x)`. A **generic** alias `type X[T] = …` is **not yet supported** this phase (parsed, but
rejected). This is the **strong-typedef** tool — a `UserId` that behaves like an `int` but can never be passed
where a plain `int` or a `ProductId` is wanted — and it is distinct from the single-field-struct **newtype**,
which _wraps_ a value behind a new field and fresh impls rather than reusing the whole shape. The prelude's
**`Result[T]`** and **`T?`** are the compiler-provided, generic form of exactly this over `Either` (built in,
not something you can yet spell yourself with a generic `type`), which is why they are distinct from each other
and need an explicit `ok_or` / `ok` to cross.

> **[deviation]** The bootstrap implements `type X = Y` only for a **scalar** underlying `Y`, and the new
> type does **not** inherit `Y`'s arithmetic or `spec` impls — a `Celsius = int` will not accept `+`
> without an explicit `int(c)`, contrary to the inheritance rule above — while `type Name = str` is
> currently **rejected**. The intended semantics (a fresh identity reusing `Y`'s whole representation and
> impls) stand; the bootstrap covers only the scalar, impl-less case this phase.

## Construction & encapsulation

The one primitive for building a value is the **struct literal** — it names every field, so it is
usable only where every field is visible. A "constructor" is not a separate feature: it is an ordinary
(usually `pub`) associated function that returns a literal. A type with any **private field** is
therefore **opaque** outside its module — a struct literal cannot name the private field, so outsiders
build it only through such a `pub` function, which runs inside the type's module and can establish the
type's invariant at the moment of construction.

Field visibility is a **single knob covering read and write together** — a `pub` field is readable
and, given a `mut` binding, writable; a private field is neither. There is no separate "public read,
private write" axis; finer control is expressed with methods.

Copy-by-value reframes what a writable `pub` field means: writing one only ever changes the holder's
**own copy**, never anyone else's value (there is no aliasing). So a `pub` mutable field is not a
shared-mutation hazard. The reason to keep a field private is not to stop others changing your value —
copy already does — but to **protect an invariant**: only the type's own methods may change the field,
so every value of the type stays valid. A plain data type with no invariant can expose its fields
freely; a type that must stay valid keeps them private and offers:

- **read** — a `pub` getter method returning a copy of the field (cheap under copy-by-value);
- **change** — a `pub mut fn` mutator (its receiver is `This`, mutated in place — `this` is not a
  parameter), which re-establishes the invariant.

To build a value that is an existing one with a few fields changed, call the constructor with the new
field and copy the rest explicitly — `Foo(age: 2, name: base.name)` produces a **new** value, leaving
`base` untouched — for a type whose fields are visible, or a `with`-style method returning a new instance
for an opaque type. A spread / `..base` shorthand that would copy the untouched fields for you is **not
yet in the grammar** — it is a separate open design question, not sugar you can write today. Zerg has
**no mutating builder or cascade**: it would work only for public-field types (where the constructor call
already says everything), could not touch a private field, and would drag a value through invalid
intermediate states — against immutable-by-default and valid-at-construction.

## Type Conversion

Zerg **converts by re-construction, never by reinterpretation** — a conversion `T(x)` _builds a new `T`_
from `x`'s value, the way a constructor does; there is **no C-style cast** that views one type's bytes as
another (a `reinterpret`), and none is offered. The three type operations stay cleanly apart: **build** a
new value (`T(x)`, here), **test** an existential's identity (`x is T` → `bool`, [Type tests](specs.md) —
**[not yet]** for non-error types this phase; only the error taxonomy is `is`-testable today), and
**never** reinterpret one type's storage as another.

Conversion is **explicit by default** — an `int` isn't a `bool`; build one with a constructor-style call
(`bool(8)`, `int(c)`). Primitive conversions are **compiler built-in**; a user type cannot add one to a
primitive.

**Narrowing a primitive** can lose the value, so it is checked like arithmetic:

- An integer conversion whose value **does not fit** the target raises (`OverflowError`) — `byte(300)`,
  `uint(-5)`, `int(u)` for a `uint` past i64. The **checked** form is `guard { byte(x) }` → `Result`; to
  **truncate** to the low bits, mask first — `byte(x & 0xFF)` always fits, so it never raises. Saturating
  is deferred.
- **`float` → integer** drops the fractional part (`int(3.7)` is `3` — the intent, not a bug) but raises
  when the integer part is **out of range** or the float is `NaN` / `±Inf`.

A **user type** may opt in to an **automatic** re-construction to another type, kept decidable by two
rules:

- **Single step only** — never chained (`X → Y`, `Y → Z` ⇏ `X → Z`); one step to one explicit
  target, so no ambiguous multi-path choice arises.
- **Only where the target type is explicit** — at a typed binding (`x: X = y`), a `return`, or a
  typed parameter; never an inferred `:=`.

This is how a value, an `Err`, or `nil` flows into an `Either` at a typed binding or return without
explicit wrapping (see [Null-safety & Errors](../code/errors.md)) — still a build of the target value,
never a reinterpret.
