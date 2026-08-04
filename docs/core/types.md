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
- **`//` always yields an `int`** — `a // b` is that same Euclidean division, spelled so the reader
  sees an integer whatever the operands are. On two integers it _is_ `/`: the language has **one**
  integer division, and a second rule for negative divisors would be a trap rather than a feature. On
  two `float`s it divides as a double and lands in `int` through the same range check `int(x)` is, so
  `7.5 // 2.0` is `3` and `-7.5 // 2.0` is `-4` with no `int(...)` round trip. `/` is unchanged and
  stays type-driven — `int / int` is an `int`, `float / float` is a `float` — because an already-typed
  `int` value never becomes a `float` implicitly (see "Numeric literals" below). `//` does **not**
  open a comment — a Zerg comment starts with `#`.

> **[deviation]** The correction that makes `/` and `%` Euclidean is emitted **unconditionally**, not
> elided when both operands are provably non-negative — the "zero overhead in the common case" above is
> the intended codegen, not today's. The semantics are unaffected: it is a cost, not a wrong answer.

### Typed positions

Several rules below are about **a value meeting a declared type** — what fits, what a literal adopts,
what converts. They all need the same answer to one question, so it is given once, here, and each of
them refers to it: **a typed position** is a place where the language already knows what type is wanted.

They are these, and the list is **exhaustive**:

| position                      | example                       |
| ----------------------------- | ----------------------------- |
| a typed binding               | `x: byte = e`                 |
| an assignment                 | `x = e` where `x` is declared |
| a `return`                    | `return e`                    |
| an argument                   | `f(e)`                        |
| a parameter's default         | `fn f(x: byte = e)`           |
| a struct literal's field      | `P(e)`                        |
| an enum variant's payload     | `Shape.Line(e)`               |
| a list literal's element      | `xs: list[byte] = [e]`        |
| a map literal's key and value | `{e: e}`                      |
| a map write                   | `m[k] = e`                    |
| a container method's argument | `xs.append(e)`                |
| a channel send                | `ch <- e`, and a `select` arm |
| a `??` fallback               | `x ?? e`                      |
| the other operand             | `a + e`                       |

A position is **structural**, not syntactic: it is what the expression IS to the construct around it, not
how it is written. **Grouping parentheses are not a position** — `(e)` is the same position `e` was —
which is what keeps a rule stated over positions from being defeated by typing more brackets.

**A position takes at most one conversion.** Where a rule below converts a value to reach the declared
type, it does so in one step per position; a value that crosses two positions may be converted at each.

**A carrier does not end a position — it moves it one level in.** Where the declared type is a `T?`, a
`Result[T]` or an `Either[X, Y]`, what meets a value is the **payload**, and the payload is the same
position: `x: int? = e` puts `e` at the binding's position against `int`, and `return Left(e)` puts it at
the `return`'s. Every rule below reads `T` there, never the wrapper.

> **[deviation]** The list is the contract; the compiler reached it incrementally, and each position it
> had not yet been told about was a value lowered into a type it did not fit — silently. The list exists
> because that kept happening: it was written as four examples in a parenthesis, and the four grew to
> fourteen one miscompile at a time. A new syntactic form owes an answer to "is this a typed position",
> and that answer belongs here rather than in whichever rule notices first.
>
> The carrier sentence is the same story from inside: the compiler fitted the WRAPPER and then lowered
> its payload by another route, which no rule was attached to. `x: float? = i` for an `int` value printed
> `5`, and `Left(i)` for `i = 300` into a `Result[byte]` truncated in silence — the same two mistakes the
> same rules already refuse one level up.

### Numeric literals

A numeric literal is **untyped** — it adopts the type its context demands, at any **typed position**
above — checked **at compile time**.
Unconstrained, an integer literal defaults to `int` and a fractional/exponent literal (`1.0`, `1e3`) to
`float`; the non-decimal bases `0x…` / `0o…` / `0b…` are ordinary integer literals.

- A literal that **does not fit** its required type is a **compile error** (`byte = 300`, `uint = -1`, an
  `int` literal past i64) — never a runtime overflow. This is the **constant** half of
  [`Into`](#into--the-conversion-that-happens-on-its-own): the target's type is known, the value is
  known, so the answer is known now.

- **A typed `float` context accepts an untyped integer literal**: `x: float = 1` is legal, and so is
  `x: float = i` for an `int` value — the first adopts, the second converts, and `int → float` is one of
  the built-in `Into`s. They differ in **when** the answer is reached, not in whether it is allowed: the
  literal is settled at compile time, the value at run time. A fractional or exponent literal (`1.0`,
  `1e3`) is a `float` from the start and never an `int`.

- **A literal adopts where a value converts, and the two are worth telling apart.** `b: byte = 5` writes
  a byte with no conversion at all; `b: byte = n` for an `int` value writes the conversion, which may
  raise. So `b: byte = 300` is a **compile error** — the constant is known not to fit — while
  `b: byte = n` with `n == 300` is an **`OverflowError`** at run time. Same rule, two moments.

  Every one of the conversions is a **lint** finding (`L5xx`), because the reader of `xs: list[byte] =
[1, 2]` should be able to see bytes on the page rather than infer them from the declaration.

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

### `Into` — the conversion that happens on its own

`T(x)` is the conversion you **write**. `Into` is the one that happens **where the target type is
already known** — at a [typed position](#typed-positions) — and it is nothing more than the compiler
writing `.into()` for you:

```text
x: float = 1.5 + 1          # what you write
x: float = 1.5 + 1.into()   # what it means
```

**`Into` is an ordinary spec**, so a type opts in by implementing it, and the built-in types already do:

```text
spec Into[T] {
    fn into() -> T
}
```

- **It may raise, and the caller handles it.** Narrowing loses values — `int → byte` cannot always
  succeed — so the built-in numeric impls raise `OverflowError`, the same failure and the same name
  `byte(300)` already raises. A user impl raises whatever fits its own reason; `ValueError` is the
  natural one, and `OverflowError` is a kind of it.
- **One step, never chained** — `X → Y` and `Y → Z` do not give you `X → Z`. Write two steps, or
  declare `X → Z` yourself.
- **One step per position.** A value crossing two positions may convert at each, which is what makes
  `demo: Z = x + y` legal for `x: X`, `y: Y`: `x` reaches `Y` at the operand position, and the sum
  reaches `Z` at the binding. It is two positions, not a chain.
- **`.into()` needs a target.** `x := 1.into()` is a compile error — there is nothing there to say
  which `Into` was meant. Written by hand, it is legal exactly where the compiler would have written
  it.

**What an expression's type is, is decided by its operands alone** — never by what it is being assigned
to. So the two operands of an operator agree first, and only then does the result meet the declared
type:

- Operands of the **same** type stay that type; nothing is converted.
- **Different** types take the **largest type both reach in one step** — largest meaning the one whose
  values include the other's, which is also the direction that cannot fail. `1.5 + 1` is a `float`
  because `int → float` exists and `float → int` does not.
- If there is no such type, or two that neither contains, the expression's type is **undetermined** — a
  compile error, and the conversion has to be written.

Because the target is never pushed down, `demo: Z = x + (y + z)` can be an error while
`demo: Z = x + y + z` is not: the parentheses change which operands meet first, and each meeting is
resolved on its own.

The built-in impls are these, and no others — a pair that is absent is one you convert with `T(x)`:

| from   | to      | can raise | note                                                |
| ------ | ------- | --------- | --------------------------------------------------- |
| `byte` | `int`   | no        | every byte is an int                                |
| `rune` | `int`   | no        | every code point is an int                          |
| `int`  | `float` | no        | never fails; may lose precision, and `L5xx` says so |
| `int`  | `byte`  | yes       | out of range → `OverflowError`                      |
| `int`  | `rune`  | yes       | not a code point → `OverflowError`                  |
| `int`  | `uint`  | yes       | negative → `OverflowError`                          |
| `uint` | `int`   | yes       | past the signed maximum → `OverflowError`           |

**`int` and `uint` do not mix**, and that falls out rather than being a rule of its own: both
directions exist, but neither type's values contain the other's, so `i + u` has no largest type and is
undetermined. Cast one side — `int(u) + i` or `u + uint(i)`.

There is no `float → int`: dropping a fraction is a decision, so it is written (`int(x)`, or `//` for
the division that yields one). There is no `byte → float` either — that would be `byte → int → float`,
which is the chain the one-step rule forbids.

**A conversion the compiler can carry out is carried out.** `x: byte = 300` is well-formed — `int → byte`
exists, so it type-checks — and then fails as a **constant**: the value is known, the conversion is known
to raise, and it is reported at compile time rather than left to run. Reachability does not enter into
it; `if false { b: byte = 300 }` is the same error.

**Every implicit conversion is a lint finding** (`L5xx`), literals included — so `1.5 + 1` is reported
and `1.5 + 1.0` is not. It is advisory, not a rule of the language: the point is that `1` and `1.0`
should mean different types on the page, so a reader never has to infer a literal's type from its
surroundings.

This is also how a value, an `Err`, or `nil` flows into an `Either` at a typed position without explicit
wrapping (see [Null-safety & Errors](../code/errors.md)) — still a build of the target value, never a
reinterpret.
