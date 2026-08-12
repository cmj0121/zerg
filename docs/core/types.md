# Zerg Types

The primitive scalars, the `struct` / `enum` / tuple you declare, how a value is constructed, and
how one type converts to another. Part of the [Language Reference](../language.md). Also in
[繁體中文](types.zh-TW.md).

## Primitive Types

A small, fixed set — three integer widths (signed `int`, unsigned `uint`, and the `byte` octet). The
**fixed-width ladder** beyond them (`i8`, `i16`, `u32`, `f64`, …) is a set of **stdlib types, not new
syntax** — a type name is just an identifier, so a width like `u32` adds a stdlib type without touching
the grammar:

| Type    | Description                              |
| ------- | ---------------------------------------- |
| `bool`  | `true` / `false`                         |
| `byte`  | unsigned 8-bit — Zerg's char             |
| `rune`  | a single valid Unicode code point        |
| `int`   | signed 64-bit integer                    |
| `uint`  | unsigned 64-bit integer                  |
| `float` | IEEE-754 double (f64)                    |
| `str`   | immutable Unicode text (no embedded NUL) |

`nil` is not a type of its own — it is the placeholder value of a `T?`
([Null-safety & Errors](../code/errors.md)); the NUL-terminated in-memory form of a `str` is the C
boundary's business ([FFI](../runtime/ffi.md)), not a property of the type.

> **[not yet]** `zerg` has no part of the fixed-width ladder: `i8` … `i64`, `u8` … `u64`, `f32` and `f64`
> are specified as stdlib types and no stdlib declares one. It is refused **by name** — a width is an
> ordinary identifier rather than a keyword, so the refusal used to be _undefined function `i32`_, the
> message any misspelled call gets, and a reader was told their own name was unknown. The **seed** builds
> and runs them, which makes this the one chapter where the seed is the broader of the two.

- **Integer overflow and division by zero raise** (`OverflowError`, `DivideByZeroError`) — an
  **abort**, not a value (see [Null-safety & Errors](../code/errors.md)); `int`/`uint`/`byte`/`rune` never
  wrap (opt into roll-over with `+%`/`-%`/`*%`, below).
- **`float` is IEEE-754:** overflow → `±Inf`, invalid → `NaN`, neither raises; `NaN` is unequal to
  everything (including itself).
- **`str` iterates as `rune` and is not indexable** — convert it with **`bytearray(s)`** when you want
  raw bytes (or binary that may contain a NUL, which a `str` never holds), or **`runearray(s)`** for its
  code points. Each names the list it builds — `bytearray` **is** `list[byte]` and `runearray` **is**
  `list[rune]`, the same type under a shorter name, interchangeable with the spelled-out form everywhere
  and **not** a strong typedef (see [Collections](../code/collections.md)).
- **A `rune`'s values are not a range**, which makes it the one scalar whose bound is a
  **predicate**: a code point is `0..=0x10FFFF` **minus** the UTF-16 surrogates `0xD800..=0xDFFF`, which
  are not characters. So `rune(0xD800)` raises `OverflowError` even though the number fits the type's
  32 bits comfortably, and `r: rune = 0xD800` is the compile error the same rule makes of a known
  value. This is also why `rune` is not a width in the fixed-width ladder below: `i32` is a range and a
  `rune` is not.

### Integer operations

- **Bitwise** — `&`, `|`, `^`, `~` (and, or, xor, complement) and the shifts `<<`, `>>`, on `int`/`uint`/`byte`.
  `>>` is **arithmetic** on signed `int` (sign-extends) and **logical** on unsigned `uint`/`byte`
  (zero-fills) — the type's sign decides, so no separate logical-shift operator exists; a shift distance
  **outside the type width** — negative, or ≥ the width — raises (`OverflowError`). These desugar to
  specs (a user type may overload them — see [Built-in specs](specs.md)), and the bitwise **symbols**
  never collide with the logical **keywords** `not`/`and`/`or`.
- **Wrapping** — `+`, `-`, `*` raise on overflow; the **`%`-suffixed** `+%`, `-%`, `*%` (and unary `-%`)
  instead **wrap modulo 2^n** — for hashing, checksums, and bit-mixing where roll-over is the intent. The
  **checked** form is already `guard { a + b }` → `Result` (no `checked_*` API); **saturating** is deferred.
- **`int` and `uint` are two types and never mix** — `int + uint` is a compile error, and not a special
  case: an operator's operands must already be one type, whatever the pair (below). This pair is the one
  worth naming because C's answer is the trap — there the signed operand converts to unsigned, so
  `-1 < 1u` is false. Cast one side: `int(u) + i`.
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
  `7.5 // 2.0` is `3` and `-7.5 // 2.0` is `-4` with no `int(...)` round trip (`7 // 2.0` is the same —
  the untyped `7` adopts `float` from its operand). `/` is unchanged and
  stays type-driven — `int / int` is an `int`, `float / float` is a `float` — because an already-typed
  `int` value never becomes a `float` implicitly (see "Numeric literals" below). `//` does **not**
  open a comment — a Zerg comment starts with `#`.

> **[not yet]** The bitwise operators do not desugar to anything a user type can implement. No `BitAnd`,
> `BitOr`, `BitXor`, `Not`, `Shl` or `Shr` spec is declared anywhere, so naming one reports
> _error: no spec named `BitAnd`_ — the ordinary message for a spec nobody wrote — and `&` on a composite has
> no route to a user body. The operators themselves are built in on `int` / `uint` / `byte` and work as
> specified; what is missing is the overload the desugaring exists to allow (see [Specs & Generics](specs.md)).
>
> **[deviation]** The correction that makes `/` and `%` Euclidean is emitted **unconditionally**, not
> elided when both operands are provably non-negative — the "zero overhead in the common case" above is
> the intended codegen, not today's. The semantics are unaffected: it is a cost, not a wrong answer.

### Typed positions

Several rules below are about **a value meeting a declared type** — what fits, what a literal adopts,
what is refused. They all need the same answer to one question, so it is given once, here, and each of
them refers to it: **a typed position** is a place where the language already knows what type is wanted.

They are these, and the list is **exhaustive**:

| position                      | example                       |
| ----------------------------- | ----------------------------- |
| a typed binding               | `x: byte = e`                 |
| an assignment                 | `x = e` where `x` is declared |
| a `return`                    | `return e`                    |
| an argument                   | `f(e)`                        |
| a parameter's default         | `fn f(x: byte = e)`           |
| a field's default             | `struct P { x: byte = e }`    |
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

**A position may wrap a value; it never converts one.** What a position builds around a value is a
carrier (next) or a spec's box ([Specs & Generics](specs.md)) — the value inside keeps its type. One
that does not fit its position is refused, and the fix is the **written** conversion, `T(x)`
(Type Conversion, below).

**A carrier does not end a position — it moves it one level in.** Where the declared type is a `T?`, a
`Result[T]` or an `Either[X, Y]`, what meets a value is the **payload**, and the payload is the same
position: `x: int? = e` puts `e` at the binding's position against `int`, and `return Either.Left(e)` puts it at
the `return`'s. Every rule below reads `T` there, never the wrapper.

The list grew one silent miscompile at a time — each position the compiler had not yet been told about
lowered a value into a type it did not fit, the carrier cases included (`x: float? = i` printed `5`;
`Left(300)` into a `Result[byte]` truncated in silence). The list is the contract: a new syntactic form
owes an answer to "is this a typed position", and the answer belongs here. Each position now refuses a
value of another type, and names the place it refused.

**A literal that does not fit is refused at every one of them, the other operand included.** `b: byte = 1`
followed by `b + 300` is a compile error: `300` is not a value of `byte`, so it does not adopt, and what
is left is a `byte` beside an `int` — two types, no expression. It used to retype the whole expression to
`int` and write `301`, which made the operand slot the one position where a literal escaped the range it
was supposed to be measured against.

### Where a type flows in — the four carve-outs

Types are worked out **bottom-up**: an expression's type comes from its parts. Four carve-outs, and
**only** these four, let a declared type flow the other way — into an expression that cannot speak for
itself:

- **(a) a literal's type.** An **untyped literal** adopts the type of the position it lands in, and is
  computed in it (Numeric literals, below).
- **(b) a composite's type.** A **composite literal with nothing to speak for it** — `[]`, `{:}`, and the
  fill form `[v; N]` choosing between a `list` and a `[T; N]` array.
- **(c) a closure's parameter types.** A closure's **omitted `: type`**, taken from the function type it
  is checked against; one that never meets an expected type is an error.
- **(d) a carrier's payload type.** A value, an `Err` or `nil` entering a `T?`, `Result[T]` or
  `Either[X, Y]` — the payload is the same position, one level in.

A **value generic** is not a fifth: a function's `N` is inferred **from** an argument's type
(`fn sum[N: int](xs: [int; N])`), which runs the same direction as everything else here.

**Four, and only four**, is checked rather than asserted: `make layering` holds the seed's bidirectional
checker to exactly this list — every node kind it pushes a wanted type into, and no other — and holds
`zerg`'s inference family to taking no wanted type at all. A fifth carve-out cannot be added without the
gate naming it.

The rule above holds at every position: **a position wraps a value, it never converts one.** So
conversion is **not a fifth carve-out** — each of these decides a type the expression **does not have
yet**, where a conversion would change one it already has, and the fix for a value that does not fit
stays the written `T(x)` (Type Conversion, below).

### Numeric literals

A numeric literal is **untyped** — it adopts the type its **position** demands, at any of the typed
positions above — checked **at compile time**.
Unconstrained, an integer literal defaults to `int` and a fractional/exponent literal (`1.0`, `1e3`) to
`float`; the non-decimal bases `0x…` / `0o…` / `0b…` are ordinary integer literals.

- A literal that **does not fit** its required type is a **compile error** (`byte = 300`, `uint = -1`, an
  `int` literal past i64) — never a runtime overflow. It is the constant twin of the
  written conversion (`byte(300)`, [Type Conversion](#into--an-ordinary-conversion-spec)): the target's
  type is known, the value is known, so the answer is known now.

- **A typed `float` context accepts an untyped integer literal — and only a literal**: `x: float = 1`
  is legal (the literal adopts), while `x: float = i` for an `int` value is a **conversion** and is
  written — `x: float = float(i)`. A fractional or exponent literal (`1.0`, `1e3`) is a `float` from
  the start and never an `int`.

- **A literal adopts where a value converts, and the two are worth telling apart.** `b: byte = 5` writes
  a byte with no conversion at all, and `b: byte = 300` is a **compile error** — the constant is known
  not to fit. `b: byte = n` for an `int` value is a **conversion**, and a conversion is
  written: `b: byte = byte(n)`, which may raise `OverflowError` at run time. Adoption settles at
  compile time; a written conversion runs.

  An adoption away from the literal's default is a **lint** finding (`L502`), because the reader of
  `xs: list[byte] = [1, 2]` should be able to see bytes on the page rather than infer them from the
  declaration. The finding names each literal and hands over the spelling that shows it — `1.0` for a
  `float`, `byte(1)` where the type has no literal form of its own.

- **An expression of literals is a literal.** Nothing in `100 + 100` has a type of its own, so the whole
  of it adopts: `x: byte = 100 + 100` is byte arithmetic answering `200`. Each part is measured against
  the target **before** the operator runs — `x: byte = 300 - 100` is refused, naming the `300`, not the
  `200` it would come to — and the arithmetic that follows is the target's own, so `x: byte = 200 + 100`
  is refused too. A `float` target makes the operators float operators: **`x: float = 1 / 2` is `0.5`**,
  because both literals are floats before the `/` runs.

  A division by a constant `0` is a compile error wherever it is written, reachable or not — the same
  argument as a literal that does not fit, at the one operator that fails without any type being wrong.

- **The bound is the position's.** An integer literal is measured against `int` where nothing asks for
  anything else, and against `uint` where a `uint` position does — so `u: uint = 18446744073709551615` is
  that value and not an error, while `x := 18446744073709551615` and `int(18446744073709551615)` are still
  refused. A literal past **both** bounds is a compile error whatever the position: it is not a number this
  machine holds.

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

> **[not yet]** Neither declaration in that block compiles. A recursive `struct` is `E452` (below), and
> the **generic `enum`** is `E212 NotImplemented: a generic enum`Either[…]`— this compiler erases type
parameters, and a variant's payload names one`. A generic `struct` is `E215` for the same reason. The
> block shows the specified shapes; both of them wait on generic types.

**Recursive and self-referential types** work directly — a `struct Node { next: Node? }`, an
`enum Expr { Num(int); Add(Expr, Expr) }` — with **no pointer**: the compiler auto-boxes the self-referential
slot behind a refcounted cell, so such a value copies **by reference** (refcount-shared), not by deep clone.
What it does not do is free the chain, which is the [Values & Memory](memory.md) reference's own deviation.

> **[not yet]** A recursive **`struct`** cannot be declared. The `Node` written above is rejected with
> _`Node` is part of a cycle of by-value declarations — a type holding itself, however indirectly, has no
> size_: sizing runs over the declaration graph before any boxing decision is reached, so the self-referential
> slot never gets the cell the paragraph promises it. The recursive **`enum`** is the half that works, its
> payload being the slot the compiler boxes — which is why `Expr` builds and `Node` does not, and why the same
> example in [Values & Memory](memory.md) does not compile either.

`Either`, `Result[T]`, and `T?` aren't special — they're ordinary stdlib types built on `enum`
(see [Null-safety & Errors](../code/errors.md)). An `enum`'s **variants share the type's visibility** — a
`pub enum` exposes every variant, to construct and to `match`; there is no per-variant privacy.

An `enum`'s **discriminant behaves differently for a fieldless enum than for a payload one** — the split
turns on whether _every_ variant is fieldless. A **fieldless** `enum` may give a variant an explicit
`= <discriminant>` — a **compile-time-constant integer**, distinct across variants (an unspecified one is
the previous `+ 1`, counting from `0`) — making it a **C-style integer enum**: `variant = <int>`. It is the
same **compile-time constant** an array length is, so `A = BASE`, `A = 2 + 3` and `A = BASE * 2 - 1` are all
the form and a call is not; one that does not fold is an error **at the variant**. Such an
enum has a **native, C-compatible integer repr** (backing `int` by one default rule, no annotation needed);
the **enum name is a value namespace** — `Color.Green` names the variant and `Color.of(n)` reverses a number
— with `int(v)` **reading** the discriminant and `E.of(n) -> E?` **reversing** it (an unknown `n` yielding
`nil`, never a wrong variant).

> **[deviation]** The namespace is not the enum's. A **variant name belongs to the first enum that declares
> it**, program-wide: with `enum Colour { Red; Green }` ahead of `enum Signal { Red; Amber }`, the
> qualified `Signal.Red` — the spelling this paragraph tells a reader to use — is refused with _E457 `Red`
> is a variant of `Colour`, not of `Signal`_, a sentence that is false about the program it is reporting
> on, and with no place. So the second enum's variant is unreachable and the enum itself is unusable. The
> linter meanwhile still emits `L401` for the same pair and advises writing `Signal.Red`, which is the one
> spelling the compiler will not take.
> A specific width is the opt-in layout decorator `#[repr]` (**[not yet]** —
> reserved and rejected loudly today, see [Decorators](decorators.md)); the serialized/wire form is the
> `Encode` / `Decode` impl (**[not yet]**), never a decorator.

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
It rides the whole product machinery — copy-by-value, and the structural tier by one rule: **a named
type opts in; an unnamed form inherits its parts' opt-ins.** A tuple (and an array) has `Eq` / `Ord` /
`Hash` exactly when **every part** has it — no declaration needed, since an unnamed form has no
declaration point and its parts already opted in (see [Specs & Generics](specs.md)). What it cannot
carry is behavior of its **own** — no inherent methods, no hand-written `spec` impl: reach for a named
`struct` the moment a value wants behavior, a nominal identity, or field names worth reading. A tuple
result is **first-class** — stored, passed, or destructured — so multiple return needs no separate
mechanism ([Pattern matching](../code/control-flow.md)).

> **[not yet]** Neither of the two things this paragraph gives a tuple for free is built. `==` on a tuple
> is refused by name — the parts-inheritance rule above is specified and the derivation over an unnamed
> form is unbuilt (the shipped message still blames the missing declaration).
> **Destructuring** is refused a step earlier still, at the comma — `a, b := two()` reports _E205 expected
> a newline or `;` to separate statements, found `,`_, which names punctuation where it owes the form's
> name (the parenthesized `(a, b) := two()` does say it, as `E238`). Either way a tuple result is stored
> and passed as specified, and read back only through `.0` / `.1`.

**`type X = Y`** defines a **new, distinct type** — not a transparent alias. `X` takes on `Y`'s
representation and implementation (its fields or variants, and its `spec` impls, now with `This` = `X`), yet
is a **separate identity**: `X` and `Y` are **different types even when structurally identical**, and there
is **no cast** between them — you convert by **re-construction** (`X(y)` / `Y(x)`), like any conversion.
One inheritance is withheld on purpose: `X` does **not** take `Y`'s `Into` impls — what `X` is
convertible to is `X`'s own declaration to make. A
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
(usually `pub`) associated function that returns a literal, which runs inside the type's module and can
establish the type's invariant at the moment of construction (**[not yet]** — an associated function is
refused by name, _E424 `User.from_id(…)` is an associated function_, so the invariant-establishing
constructor this section reasons from is written as a free function today). A **private field is one an outsider never
names**: it must carry a default (below), so an outside construction leaves it off and the declaration
decides its value. Making the literal itself unavailable outside the module is what the `#[sealed]`
decorator is for — **[not yet]**, so today the literal is reachable wherever the type is.

> **[not yet]** The struct literal binds **by position only**, so the form that names a field does not exist:
> `P(a: 1, b: 2)` reports _NotImplemented: the named argument `a:` — this compiler binds arguments by position
> only_ (see [Functions & Closures](../code/functions.md)). `P(1, 2)` builds the same value, so construction
> itself is unaffected; what is missing is the spelling this section states its rules in terms of — "it names
> every field" is what makes a private field one an outsider cannot name, and `Foo(age: 2, name: base.name)`
> below is written in a form the compiler does not read.

### Field defaults

A field may declare a **default** — `h: int = 4` — and the default is what lets that field's
constructor argument be **omitted**: `Box(1)` on a `Box(w, h)` builds the same value `Box(1, 4)` does.
It is the rule a [function parameter's default](../code/functions.md) already follows, at the field-wise
constructor, and it follows it in both directions: the backfill runs from the end of the written
arguments, so a default makes **that** field optional and not the ones before it, and a default is
evaluated **per construction** rather than once at the declaration — an expression in it (a call, a sum
over module constants) runs again for every construction that omits the field.

There are **no zero values**. A non-optional field with no default is therefore **required** at
construction, and a construction short of one is an error naming the field. The **one implicit default**
is `nil` for a `T?` field, its natural absent state — a `T?` is omittable with no `=` written.

The two halves meet at visibility: a **non-`pub` field is module-private, and must carry a default**. The
field-wise constructor is public, so a required field is one every construction has to supply a value for
— and an outsider cannot supply a value for a field it may not read. A private field with no default is
rejected at the field's own declaration (`E482`), naming the field.

> **[not yet]** A default that **reads another field** — `struct P { pub a: int; pub b: int = a * 2 }` — is
> the one shape that is not built, and it is the same shape (and the same reason) as a parameter default
> reading an earlier parameter in [Functions & Closures](../code/functions.md). The default is materialised
> at the **construction**, where a field is not a name in scope, so `a` would resolve to whatever else
> carries that name. It reports _NotImplemented: the default on field `b` of `P` reads the field `a`_,
> with the field's place.

Field visibility is a **single knob covering read and write together** — a `pub` field is readable
and, given a `mut` binding, writable; a private field is neither (**[deviation]** — access across a
module boundary is not yet checked; see [Modules, Packages & Programs](../runtime/package.md)). There is
no separate "public read, private write" axis; finer control is expressed with methods.

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
(`bool(8)`, `int(c)`). `bool(x)` on a number answers `x != 0` — the question truthiness would have asked
is spelled, so it never enters a condition unwritten. Primitive conversions are **compiler built-in**; a
user type cannot add one to a primitive.

**Narrowing a primitive** can lose the value, so it is checked like arithmetic:

- An integer conversion whose value **does not fit** the target raises (`OverflowError`) — `byte(300)`,
  `uint(-5)`, `int(u)` for a `uint` past i64. The **checked** form is `guard { byte(x) }` → `Result`; to
  **truncate** to the low bits, mask first — `byte(x & 0xFF)` always fits, so it never raises. Saturating
  is deferred.
- **`float` → integer** drops the fractional part (`int(3.7)` is `3` — the intent, not a bug) but raises
  when the integer part is **out of range** or the float is `NaN` / `±Inf`.

### `Into` — an ordinary conversion spec

A conversion is **written**. `T(x)` converts between the scalars; a user type converts through a
constructor or a named function; and nothing converts on its own — **a position wraps a value; it never
converts one** (Typed positions, above). `Into` is not a mechanism the language runs for you: it is the
**spec that names "convertible"**, so generic code can ask for the capability —

```text
spec Into[T] {
    fn into() -> T
}
```

- **A type opts in** by implementing it. **No built-in type does**, and the two reasons are separate.
  Between numbers the conversion is written `T(x)` — that is the whole of the rule above, and an
  `.into()` beside it would need the position to say which target it meant, which
  [Type System](type-system.md) forbids in the same breath. And to text there is nothing to opt into:
  `display` is a built-in value **rendering** rather than a spec ([Format](../runtime/format.md)), so
  `str(x)` answers for every type — a generic that wants text needs no bound at all.
- **What is left is the conversion the language does not have**: `impl Into[Meters] for Feet`, called
  as the written `x.into()`. `into` on a built-in is refused by name, and says what to write instead.
- **Generic code bounds on it** — `fn f[T: Into[Meters]](x: T)` may call `x.into()`, the target fixed
  by the bound. The **arguments are part of the bound**: a type implementing `Into[Feet]` does not
  meet `Into[Meters]`.
- **One step, never chained** — `X → Y` and `Y → Z` do not give you `X → Z`. Write two steps, or
  declare `X → Z` yourself.

A **super-spec** carries its arguments too: `spec Ord: Eq[int]` says Ord extends `Eq` **at** `int`, so
what an `impl Ord` owes is `Eq`'s signatures with `int` where `Eq`'s own parameter stands. A bound's
arguments are MATCHED against an impl's; a super's are **substituted** into the named spec's
parameters, which is a different thing done in a different place.

**An operator's operands must already be one type.** An untyped literal adopts the other operand — the
_other operand_ position, above — so `1.5 + 1` is two `float`s. Two **typed** operands of different
types are a compile error, whatever the pair: `i + f` and `i + u` are the same mistake with the same
fix, a written cast on one side — `float(i) + f`, `int(u) + i`. One rule for every pair; nothing is
promoted, and no target is ever pushed down into an expression.

**The conversions `T(x)` accepts** are these, and no others. They are not `Into` impls and never were
one: `T(x)` is a built-in form, and this is the list of pairs it has an answer for.

| from   | to      | can raise | note                                      |
| ------ | ------- | --------- | ----------------------------------------- |
| `byte` | `int`   | no        | every byte is an int                      |
| `rune` | `int`   | no        | every code point is an int                |
| `int`  | `float` | no        | never fails; may lose precision past 2^53 |
| `int`  | `byte`  | yes       | out of range → `OverflowError`            |
| `int`  | `rune`  | yes       | not a code point → `OverflowError`        |
| `int`  | `uint`  | yes       | negative → `OverflowError`                |
| `uint` | `int`   | yes       | past the signed maximum → `OverflowError` |

`float → int` is absent: dropping a fraction is a decision, so it has its own spellings — `int(x)`,
or `//` for the division that lands there. `byte → float` is absent too: that would be
`byte → int → float`, and one step is what a conversion is — write the two.

**Any type to text is not in the table**, because it is not a conversion between types in this sense:
`str(x)` renders a value through `display`, which every type has.

**A conversion the compiler can carry out is carried out.** `byte(300)` is well-formed — and then fails
as a **constant**: the value is known, the conversion is known to raise, and it is reported at compile
time rather than left to run. Reachability does not enter into it; `if false { b := byte(300) }` is the
same error.

> **[deviation]** It does **not** hold through a generic call. `byte(id(300))` for `fn id[T](x: T) -> T` is
> the same known constant once monomorphized, and it compiles: the program builds and dies at run time with
> _OverflowError: integer conversion out of range_. The constant-folding runs before substitution, so the
> value the specialization makes known arrives after the only pass that would have refused it.

**An adoption away from the literal's default is a lint finding** (`L502`) — `1.5 + 1` is reported and
`1.5 + 1.0` is not. It is advisory, not a rule of the language: `1` and `1.0` should mean different
types on the page, so a reader never has to infer a literal's type from its surroundings.

> **[deviation]** A type may have **one** `Into` in this compiler, not several. A method is keyed by its
> NAME, so a second `impl Into[…] for X` collides with the first and is refused by name. Reaching
> several needs a spec method keyed by the spec **and its arguments**, which is the same thing the
> bound above needs — and what would let a written `x.into()` say which one it means.

A value, an `Err`, or `nil` entering an `Either` at a typed position is the **wrap** rule at work, not a
conversion (see [Null-safety & Errors](../code/errors.md)): the carrier is built around the value, which
keeps its type inside — still a build, never a reinterpret.
