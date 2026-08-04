# Zerg Specs & Generics

How Zerg abstracts over behavior — the `spec` interface, generic bounds, spec-as-type existentials,
and the built-in specs every type gets. Part of the [Language Reference](../language.md). Also in
[繁體中文](specs.zh-TW.md).

Behavior comes in two tiers. A type may define **inherent methods** — its own behavior, usable only
when you hold the concrete type. **Abstraction**, however, always goes through a **`spec`**: a named
interface of behavior — method signatures (some carrying a **default body**, below), plus **associated
types** and **associated values** (both below), and **never fields**. Satisfaction is **nominal**: a type
must explicitly declare it implements a `spec`, and there is **one canonical implementation per
(type, spec)** pair — a **parameterized** spec counts its parameters into the pair, so `Indexable[int]`
and `Indexable[Range]` are distinct, each with its own canonical impl (Resolving a parameterized spec,
below).

A `spec` is the sole mechanism for abstracting over behavior, so it plays three roles — the **bound**
on a generic parameter, the interface a type **conforms** to, and (below) a **type** in its own right.
The built-in behaviors are specs too, not compiler magic: `Err` is the `Error` spec, and equality,
ordering, hashing, iteration, and opt-in conversions are ordinary stdlib specs. A type's inherent methods
need not belong to any spec; **only what a spec guarantees is ever abstractable**.

**A spec bound is the complete interface to a generic type.** In code generic over `T`, the only
operations available on a `T` value are the methods its spec bound declares — its fields and any
inherent methods are invisible. So:

- The **empty `spec`** is a valid bound, satisfied by every type, but it guarantees **no** behavior:
  such a `T` supports only the structural operations every value has from the memory model — copy it,
  `del` it, pass it, store it, or send it over a channel — not a single method.
- **Equality, ordering, and hashing are opt-in, never automatic.** There is **no auto-implemented
  `Object` spec** and no implicit `==`. A type gains structural equality (`==` / `!=`) only through
  **`#[derive(Eq)]`** or a hand-written `impl Eq`, a total order through `derive(Ord)`, and a hash through
  `derive(Hash)` (**[not yet]**); comparing two values of a type that has no `Eq` impl is a compile error.
  What _every_ value has, with no spec bound at all, are the **structural memory operations** the memory
  model guarantees — copy, `del`, pass, store, send over a channel — because those are properties of the
  representation, not behavior a spec abstracts. The compiler-owned **structural derivation** that backs
  `derive` (**[not yet]** for every trait in the blessed set) is the
  [Derive & Default Behavior](derive.md) reference.

A `spec` may also be used **as a type**, not only a bound: a spec-typed value holds any implementing
type — heap-boxed, single-owner, scope-owned, and **dynamically dispatched** (the method to run is
picked at runtime from the value's real type). Erasure is **one-way for the value** — once boxed, the
concrete value is hidden and **can never be recovered** (no downcast, no reinterpret; the only route to a
concrete type is to have kept it, never to un-erase one). Its **identity** is a separate matter: **`x is
T`** asks whether the boxed value's concrete type is `T` and yields a plain **`bool`** — a test that
reads the dispatch identity the box already carries, and **never recovers the value or reads its
structure** (Type tests, below).

On a boxed value, **unary** operations dispatch to the real type and work: its spec methods, plus `copy`
(producing an independent box — a contained `Ref` refcount-bumps) and `debug`, and the structural memory
ops (`del`, pass, store, send). The **binary same-type** operations — `Eq`'s `==` / `!=`, `Ord`
comparison, and so `Hash` keying — **do not**: their `other: This` operand is exactly the concrete type erasure
removes, and `is` tests only identity, never supplying it. Two boxed values are therefore **never
comparable by value**. Box a value to dynamically dispatch its spec's methods; to compare, sort, or key
it, keep the concrete type (a monomorphized `[T: S]` bound).

The same bar falls on two further member kinds: a spec's **associated functions** (`default() -> This`,
`zero()` — receiver-less, so a box gives nothing to dispatch _from_) and its **generic methods** (a vtable
holds one entry per type, not one per type-argument). Each needs a **named concrete type**, so each is a
compile error **on an existential** — never a ban on using the spec as a type, only on that call, exactly as
for the binary ops. So there is **no object-safety gate**: a spec is **always usable as a type**, and the box
offers precisely what dispatches through `this` alone — re-boxing a `This`-returning result as the same spec.

Concrete-bound generics are **monomorphized** in the emitted C — the compiler emits a separate
specialized version for each concrete type — while a spec used as a type is the one place codegen uses
dynamic dispatch. **Type arguments are solved from the call**, structurally: a `list[T]` parameter given a
`list[int]` decides `T`, so `max(a, b)` needs no `[int]` written. A parameter no argument mentions is a
compile error rather than a guess, and a **bound is checked at the instantiation** — that is where a
concrete type first exists to check it against. There is **no subtyping** between concrete types, so
generics are **invariant**: `list[Cat]` is not a `list[Animal]` — abstract over a family with a spec bound
(`[T: X]`), not subtype substitution.

> **[not yet]** A generic **`fn`** is built. A generic **`struct`** or **`enum`**, a generic **method**, and
> a bound naming more than one spec (`T: Eq + Ord`) are each refused by name.

An **implementation** (a type satisfying a spec) carries no visibility marker of its own: coherence
requires a `(type, spec)` pair — parameters included — to resolve to the same implementation everywhere,
so an implementation can be neither hidden nor duplicated — it is in effect exactly where both its type
and its spec are visible. Implementations are written for a **concrete or generic type** (`list[T]` may
implement `Iterator`); a blanket implementation conditioned on a bound — one covering every type that
satisfies some spec — is not offered, keeping resolution decidable. There is **no "every type"
implementation**: even equality is the per-type opt-in `Eq`, generated by `derive` where asked, never an
implicit blanket impl.

> **[deviation]** The intended coherence rule is **one impl per `(spec, type)` program-wide**, keyed by
> each concrete instantiation — so `impl X for list[int]` and `impl X for list[str]` are **distinct**,
> each resolvable — with the orphan rule enforced across packages. The bootstrap is **single-module**, and
> its coherence key **over-approximates**: it does not distinguish generic arguments, so `list[int]` and
> `list[str]` collide and a second instantiation is wrongly rejected as a duplicate impl. The
> per-instantiation rule stands as specified; the bootstrap does not yet enforce it precisely.

Because specs are nominal, two independently declared specs may share a method name. A type can still
implement both and be used as either one on its own — the ambiguity exists only where a single value
must satisfy **both at once** (a `T: X + Y` bound, a value typed as `X + Y`, or a bare `x.foo()` on a value
implementing both). Zerg rejects that at compile time rather than adding fully-qualified call syntax to
disambiguate — resolve it by narrowing the static context to one spec (a single-spec bound `[T: X]`, or a
spec-typed value); to share one method across specs, have them obtain it from one shared spec. Where a spec
may be implemented across package boundaries, and how coherence stays globally unique, is the
[Modules, Packages & Programs](../runtime/package.md) reference.

**A name resolved on a concrete value must name exactly one method** — the same anti-ambiguity rule, now at
a concrete call. An **inherent method may not share a name with any spec method the type implements**: a
compile error at the implementation. To give a type its own version of a spec method, **override** it
(dispatch stays canonical); inherent methods are for behavior _outside_ any spec, so a collision is a
mistake, not a priority to resolve.

**A spec may require a super-spec.** After the spec's name, `: Bound` names one or more specs every
implementer must **also** implement — `spec Ord: Eq` makes an `impl Ord` also require `impl Eq`, and a
`+` conjunction requires several (`spec Sorted: Ord + Hash`). A super-spec does two jobs: it is a
**precondition** (you cannot implement `Ord` without `Eq`), and it puts the super-spec's methods **in
scope on `This`** inside the spec's own default bodies — so `Ord`'s provided body may call `Eq`'s methods
on `this`. That is exactly the **cross-spec default-body reuse** a flat model would have to give up. A
super-spec is distinct from a **use-site** bound: needing several capabilities _at a call_ is still said
with a combined bound — `[T: Ord + Hash]`, the `+` reading as "and" — whereas a super-spec bakes a genuine
implementation dependency into the spec itself. Where a capability is merely shared, not depended on, it
can still be its own spec listed alongside the others in a bound.

## Resolving a parameterized spec

A **parameterized** spec folds its argument into an implementation's identity, so a type may implement
several at once — a `list` implements `Indexable[int]` (element access) and `Indexable[Range]` (slicing),
each keeping its own associated `Output`:

```text
spec Indexable[K] {
    type Output
    fn index(k: K) -> Output
}
impl[T] Indexable[int]   for list[T] { type Output = T;       fn index(i: int)   -> T       { … } }
impl[T] Indexable[Range] for list[T] { type Output = list[T]; fn index(r: Range) -> list[T] { … } }
```

Because the parameter `K` appears in the method signature, a use `xs[k]` **resolves statically on `k`'s
type** — the compiler picks the unique impl whose `K` matches, by the same machinery that types an untyped
literal. Three outcomes, with **no default fallback**:

- **exactly one** impl matches the argument type → resolved, no annotation;
- **none** → an ordinary type error;
- **two or more** (only when the argument is an untyped literal fitting several `K`s) → a **hard compile
  error demanding an annotation**. It is never demoted to a warning or picked by a default: unlike an
  uncovered `match` (whose fallback is a loud `MatchError`), a mis-resolved impl has no safe fallback — it
  would silently run the wrong code.

Distinct concrete parameters never overlap, so resolution is well-defined; the open question is only which
of several matches a use means. A concrete-bound generic names the parameter directly
(`[X: Indexable[int]]`) or binds it fresh (`[X: Indexable[K]]` binds `K`), so **bounds are never
ambiguous** — only a bare use on a value with several impls is. For a choice made at **run time** rather
than by the argument's type, use an `enum` instead.

## Type tests — `is`

An existential hides the value but not its **identity** — the `x is T` test introduced above. It is not a
downcast and adds no reinterpret to the language ([Type Conversion](types.md)). `T` must implement the spec
`x` is typed as, else the test is statically impossible and rejected; on a value whose concrete type is
**already known** (not an existential) the answer is a compile-time constant.

Because `is` never yields the concrete value, it drives **control flow, not data access**: you may branch
on "is this a `T`?", but to read a `T`'s own fields you must **already hold the concrete type** — one you
never boxed. It composes as an ordinary `bool` — in an `if`, under `not` / `and` / `or`, or as a `match`
guard — needing no new pattern form. Its main use is dispatching on an **erased error's** type (see
[Null-safety & Errors](../code/errors.md)). This phase, **that is the only implemented use** — `is` works on the
built-in error taxonomy, while the general existential test `x is T` for a
**non-error** type is **[not yet]**.

## Methods, `this` / `This`, and default bodies

A **method** is a function with a **receiver** — the instance it is called on, named **`this`**; the
receiver's own type is **`This`**. `This` names "the implementing type" wherever the concrete type is not
yet known — a same-type operand (`less(this, other: This) -> bool`) or the result of an **associated
function** (`default() -> This`, a constructor — which, having no receiver, has no `this`) — and resolves
to the concrete type in each implementation. A **parameterized** spec's parameter (`Indexable[K]`) is a
**separate**, freely-chosen type — fixed per impl and folded into the (type, spec) identity — and an
**associated type** (`type Item`, projected `This.Item`) is a third, implementer-chosen type; `This` alone
is the forced self-type, never a choice.

A spec's methods come in two kinds:

- **required** — a signature with no body; every implementer must supply it.
- **provided** — a signature **with a default body**, written in terms of the required (and other spec)
  methods on `this`, never fields. An implementer **inherits** it or **overrides** it with a specialized
  version (a faster `contains`, say); an override must still mean the conventional thing, and the
  `(type, spec)` implementation stays canonical either way.

So a spec with one required method can hand implementers many derived ones for free — `Iterator` derives
`map`, `filter`, `count`, … from `next` — and the `spec bound is the complete interface` rule then makes
**all** of them, required and provided, callable on a bounded `T`. These provided defaults are
**behavioral** — over methods, never fields; the separate **structural** tier, where the compiler reads
a type's shape to generate an impl, is the [Derive & Default Behavior](derive.md) reference.

A method or function may carry **its own type parameters**, stacked on the receiver's: `map[U](this, f:
fn(T) -> U)` adds `U` beside the spec's `T` and the receiver's `This`, each **monomorphized** per concrete
combination. A provided method may be generic too — that is exactly what lets an adapter change the
element type (`T` → `U`).

**Dispatch is uniform.** Every spec method, required or provided, resolves to the type's **canonical
implementation** — its override if it has one, else the default. So a default body that calls another spec
method reaches the type's override (a defaulted `count` built on `next` uses an overridden
`next`) — there is **no static-dispatch exception for defaults**. The mechanism is the one already
defined — a concrete-bound generic **monomorphizes** to the actual impl, a spec used as a type dispatches
through its **vtable** to the actual impl. This holds for a **direct call on a concrete value** as well
(**[not yet]** — a provided method is refused by name):
`c.provided()` runs the type's **override** if it has one, else the spec's **default
body** — with no `#[dyn]` and no boxing needed, so a provided method is not confined to the
dynamic-dispatch path.

## Associated types and values

A spec member is not only a method. A spec may declare an **associated type** — `type Item` (optionally
bounded, `type Item: Ord`) — a type each impl fills in with `type Item = int`. It is **functionally
determined by the implementer**: one `Item` per impl, never chosen per use. Reference it in type position
by **projection** — `I.Item`, chainable as `I.Item.Sub`.

This is what makes a single-output protocol like iteration **well-defined**: `for x in it` has **one**
element type because `Iterator.Item` is fixed per impl — not chosen per use, as a parameterized
`Iterable[T]` would allow (that alternative is deliberately rejected). The contrast is exact — an
**associated type** is one output per impl (`Iterator`), a **parameter** is one impl per argument type
(`Indexable[K]`, above).

A spec may also require an **associated value** — a compile-time value each impl supplies: `BITS: int`
requires it, and the impl provides `BITS := 32` (a **`val-bind`**). It is a folded constant, reached as
`T.BITS` in generic code — the spec-level form of a type constant (below). Unlike an associated
**function** (`fn max() -> This`, which _runs_ to produce a value), an associated **value** is _folded_ at
compile time; use the value form for a constant a spec guarantees, the function form for a computed one.

## Type constants

A **type constant** is a per-type compile-time value, declared inside the type's `impl` as a
**`val-bind`** — `NAME := <const-expr>` — and read as `Type.NAME` (`This.NAME` from inside the type). Its
initializer is a **constant expression** — a literal, another constant, or their folded arithmetic — that
the compiler **substitutes at compile time**, running no code and having **no side effect** (folding is
implicit and independent of the `const` keyword, which only marks a shadow-proof binding). Because
construction is an ordinary **call** — `Point(x: 0, y: 0)`, **never** brace syntax — a type can give
itself its own canonical values: `ORIGIN := Point(x: 0, y: 0)`, reached as `Point.ORIGIN`. Being a
compile-time constant, an `int`-typed one is usable **wherever a compile-time constant is** — including a
fixed-array size (`[byte; Buffer.SIZE]`, see [Collections](../code/collections.md)). Visibility is the ordinary
`pub` / private knob, as on a field or method.

A type constant is the **impl side** of the associated-value machinery (above): a plain `NAME := …` is a
`val-bind` no spec demanded, while a spec's `BITS: int` is the same `val-bind` a spec _requires_. So a
per-type _value_ that a spec must guarantee is an **associated value** — `MAX: This` required, each impl
supplying `MAX := …`, generic code reading `T.MAX` — **not** a receiver-less function.

## Built-in specs

Every built-in behavior is a spec, **gained by implementing (or deriving) it** — there is no
auto-implemented top spec, and none is implicit. The universal structural operations (`copy`, `del`, pass,
store, send over a channel) belong to the **memory model**, not to any spec bound
([Values & Memory](memory.md)); `copy` in particular is forced for every type and is never absent.
`debug` / `display` — the developer-facing and human-facing text renderings, `display` defaulting to
`debug` — belong to [Format](../runtime/format.md); their **structural auto-derivation** is **[not yet]**. Everything
else is a spec a type **opts into**, a generic bound gating on it:

- **`Eq`** — structural equality, driving `==` / `!=`, gained by `#[derive(Eq)]` or a hand-written
  `impl Eq`; a channel or `fn` field compares by identity. A type with **no `Eq` impl cannot be
  compared** — `==` on it is a compile error, never a silent structural default.

Zerg has **no instance-identity test** between two values: under copy-by-value distinct values are
distinct instances and there's no aliasing, so "same instance?" would be meaningful only for a channel —
too narrow to earn an operator. Equality, where a type opts into it, is the **structural** `Eq`. The
**`is`** keyword is a different question — the **type-identity** test `x is T` on an existential (Type
tests) — "what concrete type is boxed here?", never "are these two the same value?".

The remaining specs are likewise **opt-in** — implement (or derive) the spec to gain the capability; a
generic bound gates on it:

- **`Ord`** — a **total** order consistent with `Eq`, defined by the single required **`less`** (`<`) and
  requiring `Eq` as a super-spec (`spec Ord: Eq`); `<=` `>` `>=` and sort derive from it with `Eq`, and
  `min` / `max` / `clamp` are ordinary stdlib helpers over an `Ord` bound — there is **no three-way
  `Ordering`** value, only `less` and `Eq`. `str` orders **lexicographically by code point** (== byte
  order, its UTF-8 being valid — not locale collation, a separate stdlib concern); `float` opts out of
  both `Ord` and `Hash` (rationale below).
- **`Hash`** — `map` / `set` keys, with `equal ⇒ same hash`. `str`, being immutable, is a natural key.
  **[not yet]**
- **`Iterator`** / **`Iterable`** — the iteration protocol (**Iteration**, below).
- **`Error`** (`Err`) — the error tier: `message() -> str`, `unwrap() -> Err?` (the underlying cause,
  `nil` if none), and `code() -> byte?` (an optional small code).
- **`Add` / `Sub` / `Mul` / `Div` / … and the bitwise `BitAnd` / `BitOr` / `BitXor` / `Not` / `Shl` /
  `Shr`** — the value operators (`+ - * / %`, `& | ^ ~ << >>`, indexing, …): operator overloading, below.
  `str` implements `Add`, so `+` **concatenates** into a new string (see [Collections](../code/collections.md)).
- **the cast spec** — an opt-in auto-conversion: single-step, at an explicit target (see Type
  Conversion).

**`Ref` — copy-by-ref (sealed).** Unlike every spec above, implementing it adds no behavior — it changes
a value's **representation**. A `Ref` type is **reference-counted**: copying bumps a shared count instead
of deep-copying, and its `drop(this)` runs **once**, at the last holder's scope exit. The compiler
supplies the counting and the by-ref copy; only the `drop` body is written. `Ref` is **sealed** — its
sole implementers are the built-in **`chan`** (whose `drop` is close) and the stdlib **`Ref[T]`** resource
box (see [Values & Memory](memory.md)). Ordinary code **uses `Ref[T]`; it never implements `Ref`** — so "is this value
shared by reference?" always has a definite answer: only `chan` and `Ref[T]` are.

**Operators desugar to specs**, so a user type may overload the value operators by implementing the
matching one — `==` / `<` already route through `equal` / `Ord`. An overload must mean the
**conventional** thing (a `+` that is not addition is abuse, against `small and crisp`). The **logical
operators are keywords** — `not` (unary), and the **short-circuiting** `and` / `or` — over `bool` only,
yielding `bool` (no truthiness; cast with `bool(x)`): `and` skips its right operand when the left is
`false`, `or` when the left is `true`, and logical xor is just `a != b` (there is no `xor` keyword — it
cannot short-circuit, so it is an ordinary operation, not a keyword). These, and the null-safety
operators (`?`, `??`, `?.`, `!`), are **fixed constructs — never overloadable**; the bitwise symbols
(`& | ^ ~`, [Integer operations](types.md)) never collide with them.

`float` sits out `Ord` and `Hash` — its `NaN` breaks a total order and the `equal ⇒ hash` law — so a
`float` is never a sorted-collection element or a key, and a composite **containing** one inherits this
transparently: a **derived `Eq`** compares the field with `==`, so it is **non-reflexive** for a
`NaN`, and the type gets no `Ord`/`Hash` either. To key or sort such a type the author **implements them
explicitly**, handling `float`'s two traps: a **reflexive** `equal` with **canonical `±0.0`** (equal, so
must hash alike) for `Hash`, and a **total order** (IEEE `totalOrder`, `NaN` at an end) for `Ord`. A
stdlib total-order/hashable `float` wrapper is deferred.

**Iteration.** An **`Iterator`** has an associated **`type Item`** and `next() -> Result[Item]` —
`Left(v)` for the next element, or `Right(StopIteration)` at the end (**`StopIteration`** is a built-in
`Err`). An **`Iterable`** likewise has a `type Item` and `iter()`, producing a fresh `Iterator` whose
`Item` matches. Because `Item` is an **associated type** — fixed per impl, not a parameter chosen per use
— `for x in X` has **one** element type: it requires `X: Iterable`, binds `x: X.Item` to each `Left`,
**exits cleanly on `Right(StopIteration)`**, and **raises any other `Right(err)`** — a mid-stream failure
is never silently swallowed (drive `next()` by hand and `guard` to inspect it). Since `<-ch` already
yields `Result[T]`, **a channel is an `Iterator`** with `Item = T`: `for v in ch` drains it, ending on a
clean close and re-raising a producer crash. An `Iterator` is trivially `Iterable`, so **lazy adapters**
(`map`, `filter`, `take`, `zip`, …) are ordinary stdlib iterators that chain — each returns a **concrete
adapter type** (`map` a `Map[This, U]` whose `Item = U`, holding the source and the closure), so a chain
stays fully **monomorphized**, no boxing. `for mut x in X` binds each element as an in-place `mut` — only
when `X` is `mut`.
