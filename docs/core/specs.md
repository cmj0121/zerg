# Zerg Specs & Generics

How Zerg abstracts over behavior — the `spec` interface, generic bounds, spec-as-type existentials,
and the built-in specs every type gets. Part of the [Language Reference](../language.md). Also in
[繁體中文](specs.zh-TW.md).

Behavior comes in two tiers. A type may define **inherent methods** — its own behavior, usable only
when you hold the concrete type. **Abstraction**, however, always goes through a **`spec`**: a named
interface of behavior — method signatures, some carrying a **default body** (below) — and **never
fields**, **never an associated type**, and **never an associated value** (Associated types and values,
below). Satisfaction is **nominal**: a type
must explicitly declare it implements a `spec`, and there is **one canonical implementation per
(type, spec)** pair — a **parameterized** spec counts its parameters into the pair, so
`Indexable[int, T]` and `Indexable[Range, list[T]]` are distinct, each with its own canonical impl
(Resolving a parameterized spec, below).

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
  `derive(Hash)` (both **[not yet]**); comparing two values of a type that has no `Eq` impl is a compile
  error. The structural memory operations above are the exception, because they are properties of the
  representation rather than behavior a spec abstracts. The compiler-owned **structural derivation** that
  backs `derive` (built for **`Eq`** on a `struct` and on a fieldless `enum`; **[not yet]** for `Ord`,
  `Hash`, `Encode`, `Decode`, and for `Eq` on a payload `enum`) is the
  [Derive & Default Behavior](derive.md) reference.

A `spec` may also be used **as a type**, not only a bound: a spec-typed value holds any implementing
type — heap-boxed, single-owner, scope-owned, and **dynamically dispatched** (the method to run is
picked at runtime from the value's real type). Erasure is **one-way for the value** — once boxed, the
concrete value is hidden and **can never be recovered** (no downcast, no reinterpret; the only route to a
concrete type is to have kept it, never to un-erase one). Its **identity** is a separate matter: **`x is
T`** asks whether the boxed value's concrete type is `T` and yields a plain **`bool`**, read off the
dispatch identity the box already carries (Type tests, below).

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

> **[not yet]** A `spec` cannot be used **as a type** at all, so the three paragraphs above — the heap-boxed
> existential, its dynamic dispatch, and the member-by-member account of what a box does and does not offer —
> describe a facility no program can reach. `fn go(g: Greet)` is _E9048 NotImplemented: the `spec` `Greet`
> used as a TYPE (parameter `g` of `go`) — a spec is a bound and an interface here, not yet a value's type;
> take the concrete type, or a generic parameter bounded by it_. A `spec` fills two of its three roles here
> and not the third; the same
> claim in the [Language Reference](../language.md) overview is unbuilt for the same reason, and the
> dynamic-dispatch half of the codegen paragraph below has nothing to dispatch on.
>
> What a program CAN reach is [`#[obj]`](#obj--a-specs-methods-held-as-values) below: the same
> existential, encoded as a struct of function values over a captured implementer instead of as a boxed
> pointer with a vtable. It offers what this section says a box offers and refuses the members this
> section says a box cannot serve — so the facility is here, and the **type** is what is not.

Concrete-bound generics are **monomorphized** in the emitted C — the compiler emits a separate
specialized version for each concrete type — while a spec used as a type is the one place codegen uses
dynamic dispatch. **Type arguments are solved from the call**, structurally: a `list[T]` parameter given a
`list[int]` decides `T`, so `max(a, b)` needs no `[int]` written. A parameter no argument mentions is a
compile error rather than a guess, and a **bound is checked at the instantiation** — that is where a
concrete type first exists to check it against. There is **no subtyping** between concrete types, so
generics are **invariant**: `list[Cat]` is not a `list[Animal]` — abstract over a family with a spec bound
(`[T: X]`), not subtype substitution.

> **[not yet]** A generic **`fn`** is built, and so is a bound naming more than one spec — `T: Eq + Show`
> is a conjunction, and the spec that is not met is the one the refusal names. A generic **`struct`** or
> **`enum`** and a generic **method** are each still refused by name.

An **implementation** (a type satisfying a spec) carries no visibility marker of its own: coherence
requires a `(type, spec)` pair — parameters included — to resolve to the same implementation everywhere,
so an implementation can be neither hidden nor duplicated — it is in effect exactly where both its type
and its spec are visible. Implementations are written for a **concrete or generic type** — `list[T]` may
implement `Iterator`.

> **[not yet]** An `impl` whose **target carries type arguments** is _E9038 NotImplemented: an `impl` on
> `list[int]` — a type ARGUMENT on the target: this compiler keys an implementation by the target's bare
> name, so every instantiation of `list` would share one_ — and it is both of the shapes
> `GRAMMAR#impl-decl` derives, the parameterized `impl[T] Spec for list[T]` and the fully concrete
> `impl Spec for list[int]`. So no implementation can be attached to a container type at all, and
> `list[T]` implementing `Iterator`, the line above, is specification rather than something `zerg`
> builds. What it needs is one implementation monomorphized per instantiation of its target, and a
> generic `fn` is the only thing this compiler monomorphizes. The form that works is an `impl` on a
> `struct` or an `enum` the program declares.

A **blanket implementation** conditioned on a bound — one covering every type that satisfies some spec — is
not offered, keeping resolution decidable; and there is **no "every type" implementation** either, the
per-type opt-in `Eq` above included.

> **[deviation]** The intended coherence rule is **one impl per `(spec, type)` program-wide**, keyed by
> each concrete instantiation — so `impl X for list[int]` and `impl X for list[str]` would be
> **distinct**, each resolvable — with the orphan rule enforced across packages. Two halves of that fall
> short, and a third turns out not to be a deviation at all.
>
> The ORPHAN half is enforced one scope in: an `impl` belongs in the spec's module or the type's, because
> a module is the only scope this implementation has and there is no package layer for the rule to reach.
> UNIQUENESS is not keyed on the `(spec, type)` pair at all — what makes a second `impl X for A` an error
> is that its methods collide in the one namespace a type's methods share, which is a narrower question
> than the one the rule asks. And the INSTANTIATION half of the key is a **[not yet]** rather than a
> deviation: a target carrying type arguments is refused above, so no instantiation ever reaches a key
> for a key to be imprecise about. Measured, the FIRST of `impl X for list[int]` / `impl X for list[str]`
> is refused and neither is ever keyed — so the key does not over-approximate, it is never consulted.

## `#[obj]` — a spec's methods, held as values

A **`spec` is a bound, never a type** (above), so a value cannot be typed by one. `#[obj]` is what you
write when you want a heterogeneous collection anyway: on a spec, it generates a companion **struct of
function values** — one field per method — and a **generic wrap** that turns any implementer into one.

```zerg
#[obj]
spec Draw { fn draw() -> str }
```

is:

```zerg
struct DrawObj { pub draw: fn () -> str }
fn draw_obj[T: Draw](v: T) -> DrawObj {
    return DrawObj(fn () -> str { return v.draw() })
}
```

Two fences rather than one, because they are two spellings of the same declarations and not a program that
holds both: writing them together is _E3078 `DrawObj` is declared twice_.

**The openness comes from the wrap point**, not from anything at run time: `draw_obj` is monomorphized
per implementer, and what comes back has one type. So `list[DrawObj]` is heterogeneous with **no vtable,
no header on any value, and no downcast** — you may call what the spec declares, and you may not ask what
is inside. When you need to ask, the answer is an `enum` and a `match`.

Three shapes are **refused**, by the same test the delegating `derive` uses — does the rewrite exist:

- a **`mut fn`**: a wrapped value is a copy, so writing through it would change something nobody can
  reach. An object is immutable here;
- a method taking **`This`**: it needs the type an object has forgotten — that shape is what
  `#[derive(S)]` on an `enum` is for;
- anything that is **not a spec**: there are no methods to hold.

> **[not yet]** A type cannot implement both. `impl A for P` and `impl B for P` where each spec declares
> `go` is refused at the **second declaration** — _E4025 `P` declares `go` twice — every method on a type
> shares one namespace, spec or inherent alike_ — so the narrowing remedy below has no program to apply to.
> The refusal is the same one that keeps a derived and a hand-written `Eq` from coexisting, applied one
> case too wide.

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
several at once — a `list` implements `Indexable[int, T]` (element access) and
`Indexable[Range, list[T]]` (slicing),
each keeping its own output type as a second parameter:

```text
spec Indexable[K, V] {
    fn index(k: K) -> V
}
impl[T] Indexable[int, T]         for list[T] { fn index(i: int)   -> T       { … } }
impl[T] Indexable[Range, list[T]] for list[T] { fn index(r: Range) -> list[T] { … } }
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

> **[not yet]** A parameterized spec may be implemented at **one** argument, not several, which is the whole
> of what this section is for. `impl Ix[int] for C` beside `impl Ix[str] for C` is rejected with _E4025 `C`
> declares `ix` twice — every method on a type shares one namespace, spec or inherent alike, and a type has one
> canonical implementation of a spec_: a method is keyed by its **name**, so the second impl's `ix` collides
> with the first instead of being told apart by the very argument that is supposed to distinguish them. The
> `Indexable[int, T]` / `Indexable[Range, list[T]]` pair above therefore cannot be declared, and the
> three-outcome resolution it feeds has nothing to resolve between. It is the same root cause as the one
> `Into` per type in [Types](types.md#into--an-ordinary-conversion-spec).

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
**non-error** type is **[not yet]**: _E9078 NotImplemented: `is P` — an `is` test names one of the built-in
error kinds here, and `P` is not one; GRAMMAR#cmp-expr takes any `type-name`, so this is a narrower test
than the grammar writes_.

## Methods, `this` / `This`, and default bodies

A **method** is a function with a **receiver** — the instance it is called on, named **`this`**; the
receiver's own type is **`This`**. `This` names "the implementing type" wherever the concrete type is not
yet known — a same-type operand (`less(this, other: This) -> bool`) or the result of an **associated
function** (`default() -> This`, a constructor — which, having no receiver, has no `this`) — and resolves
to the concrete type in each implementation. A **parameterized** spec's parameter (`Indexable[K, V]`) is a
**separate**, freely-chosen type — fixed per impl and folded into the (type, spec) identity. `This` alone
is the forced self-type, never a choice; there is no third, implementer-chosen kind (Associated types and
values, below).

A spec's methods come in two kinds:

- **required** — a signature with no body; every implementer must supply it.
- **provided** — a signature **with a default body**, written in terms of the required (and other spec)
  methods on `this`, never fields. An implementer **inherits** it or **overrides** it with a specialized
  version (a faster `contains`, say); an override must still mean the conventional thing, and the
  `(type, spec)` implementation stays canonical either way.

> **[not yet]** A `spec` member with a **body** is refused at the **declaration**, not merely at a call:
> _E9002 NotImplemented: a `spec` member with a BODY — a provided method's body is read and dropped here, so
> nothing in it is checked and it is not the method that runs; declare the signature and write the body in
> each `impl`_. So a `spec` in this compiler has required methods only, an implementer inherits nothing, and
> the free-derived-methods economy below — `Iterator` handing out `map` / `filter` / `count` from `next` — has
> no mechanism under it. The refusal names the form at the point it is written, so no program reaches the
> dispatch question at all.
>
> **[not yet]** A signature may be **`unsafe`** — `GRAMMAR` derives `fn-sig ::= 'unsafe'? 'mut'? 'fn' …`, so
> `unsafe fn peek() -> int` inside a `spec` is a member — and this compiler does not build it. It is read to
> the end of the signature and refused as itself: _E9036 NotImplemented: the `unsafe` `spec` signature `peek`_,
> with the place. The reason is the one a standalone `unsafe fn` gets (`E9027`): the trust boundary the keyword
> marks is not enforced ([FFI](../runtime/ffi.md)), and reading the signature as a safe one would erase the
> only thing `unsafe` says. Everything that starts **no** member at all — `unsafe { … }` in a spec body among
> them — still gets `E2036`.

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
method reaches the type's override (a defaulted `count` built on `next` uses an overridden `next`) — there is
**no static-dispatch exception for defaults**, and the mechanism is the one already defined above. This holds
for a **direct call on a concrete value** as well (**[not yet]** — a provided method is refused at its
declaration, above): `c.provided()` runs the type's **override** if it has one, else the spec's **default
body** — with no boxing needed, so a provided method is not confined to the dynamic-dispatch path.

## Associated types and values

A spec carries **behaviour and nothing else**. It declares no **associated type** — a type each impl fills
in, `type Item`, projected `This.Item` — and no **associated value** — a compile-time constant each impl
supplies, `BITS: int` required and `BITS := 32` provided. Neither is a member kind here, and neither is a
syntax the grammar derives: both are **refused by name**. An associated type is an output the **impl**
chooses, so checking a use of `This.Item` would need the impl in hand before the expression's type is
known — a type flowing backwards into inference. A parameterized spec says the same thing forwards: one
impl per argument (`Indexable[K, V]`, above) where an associated type was one output per impl.

The cost lands on a **single-output protocol**. `Iterable[T]` can be implemented at several `T` where a
fixed `Item` could not, so what pins the element type is **coherence** — at most one such impl per type,
which the compiler already checks for every other (type, spec) pair.

A per-impl **constant** is written as an ordinary **type constant** (below) — a value the type gives
itself, which no spec demands.

## Type constants

A **type constant** is a per-type compile-time value, declared inside the type's `impl` as a
**`val-bind`** — `NAME := <const-expr>` — and read as `Type.NAME` (`This.NAME` from inside the type). Its
initializer is a **constant expression** — a literal, another constant, or their folded arithmetic — that
the compiler **substitutes at compile time**, running no code and having **no side effect** (folding is
implicit and independent of the `const` keyword, which only marks a shadow-proof binding). Because
construction is an ordinary **call**, a type can give itself its own canonical values:
`ORIGIN := Point(x: 0, y: 0)`, reached as `Point.ORIGIN`. Being a compile-time constant, an `int`-typed one
is usable **wherever a compile-time constant is** — including a fixed-array size (`[byte; Buffer.SIZE]`,
see [Collections](../code/collections.md)). Visibility is the ordinary `pub` / private knob.

It shares a production with the **associated value** that left this chapter, and the difference is the
whole reason it stays: nothing about a type constant points at a spec. It is a value a type gives itself,
which no spec demanded and **none can require** — a spec that wanted one would be right back to an output
the impl chooses. Use the constant form for a value that must **fold**, and an associated fn
(`fn max() -> This`) for one that must **run**.

> **[not yet]** `NAME := 32` inside an `impl` reports _E9006 NotImplemented: an associated value binding
> `BITS := …` in an `impl`_, so `Type.NAME` names nothing and `Point.ORIGIN` cannot be declared. A
> fixed-array size that a type constant was to supply is written as a module-level constant instead.

## Built-in specs

Every built-in behavior is a spec, **gained by implementing (or deriving) it** — there is no
auto-implemented top spec, and none is implicit. The universal structural operations (`copy`, `del`, pass,
store, send over a channel) belong to the **memory model**, not to any spec bound
([Values & Memory](memory.md)); `copy` in particular is forced for every type and is never absent.
`debug` / `display` — the developer-facing and human-facing text renderings, `display` defaulting to
`debug` — belong to [Format](../runtime/format.md); their **structural auto-derivation** is **[not yet]**. Everything
else is a spec a type **opts into**, a generic bound gating on it:

- **`Eq`** — structural equality, driving `==` / `!=`, gained by `#[derive(Eq)]` or a hand-written
  `impl Eq`; a channel or `fn` field compares by identity. It requires **both** `eq` and `ne` — an impl
  supplying only one is _E3017 `P` does not implement `ne`, which `Eq` requires_ — because `!=` is
  dispatched, not derived by negating `==`. A type with **no `Eq` impl cannot be compared** — `==` on it
  is a compile error, never a silent structural default.

  > **[not yet]** A **container** cannot gain one at all, which is the rule met from a direction it has no
  > answer for: `xs == ys` over two `list`s, two `map`s or two tuples is _E9057 NotImplemented: `==` on a
  > `list[int]` — structural equality over a container is unbuilt, and a container has no declaration to
  > derive it on_. What the unnamed forms are owed is under Types' parts-inheritance rule — a tuple has
  > `Eq` exactly when every part has it — and that derivation is the unbuilt half. Compare the elements
  > you mean to compare meanwhile. It is the same hole [Format](../runtime/format.md) reports as `E9059`,
  > one operator over.

Zerg has **no instance-identity test** between two values: under copy-by-value distinct values are
distinct instances and there's no aliasing, so "same instance?" would be meaningful only for a channel —
too narrow to earn an operator. Equality, where a type opts into it, is the **structural** `Eq`. The
**`is`** keyword is a different question — the **type-identity** test `x is T` on an existential (Type
tests) — "what concrete type is boxed here?", never "are these two the same value?".

> **[not yet]** Of the built-in specs this section describes, exactly two are declared: **`Eq`** above, and
> **`Into[T]`** ([Types](types.md#into--an-ordinary-conversion-spec)). `Ord`, `Hash`, `Error`,
> `Iterator` / `Iterable`, the sealed `Ref`, and every operator spec — `Add`, `Sub`, `Mul`, `Div`, `BitAnd`,
> `BitOr`, `BitXor`, `Not`, `Shl`, `Shr` — do not exist as declarations at all, so they cannot be named:
> `impl Ord for P` reports _error: E3013 no spec named `Ord`_, the ordinary message for a spec nobody wrote,
> and `impl BitAnd for P` reports it too. The USE side is refused by the operator rather than by the spec:
> `P(1) < P(2)` reports _E3044 operator `<` orders two numbers or two strs, and these are P and P_, which
> names the operand types and says nothing about the missing `Ord`. Several of the **behaviours** are built
> in and reachable without their spec — `<` on an `int`, `+` concatenating a `str`, the error taxonomy `Err`
> names and the `message()` / `unwrap()` it answers, a `chan`'s refcounted
> close — but they are compiler-owned and a user type cannot join them, with a `#[derive(Eq)]` on the type
> or without one. Everything from here to the end of this chapter is specified against that gap.

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
- **`Into[T]`** — the conversion spec: a type declares what it converts to and generic code bounds on
  it; a conversion is always **written**, never applied by a position. It ships **no built-in impls** —
  between numbers the conversion is `T(x)`, and to text it is `str(x)`, which every type answers
  through `display` (see [Type Conversion](types.md#into--an-ordinary-conversion-spec)).

**`Ref` — copy-by-ref (sealed).** Unlike every spec above, implementing it adds no behavior — it changes
a value's **representation**. A `Ref` type is **reference-counted**: copying bumps a shared count instead
of deep-copying, and its `drop(this)` runs **once**, at the last holder's scope exit. The compiler
supplies the counting and the by-ref copy; only the `drop` body is written. `Ref` is **sealed** — its
sole implementers are the built-in **`chan`** (whose `drop` is close) and the stdlib **`Ref[T]`** resource
box (see [Values & Memory](memory.md)). Ordinary code **uses `Ref[T]`; it never implements `Ref`** — so "is this value
shared by reference?" always has a definite answer: only `chan` and `Ref[T]` are.

**Operators desugar to specs**, so a user type may overload the value operators by implementing the
matching one — `==` / `!=` already route through `Eq`'s `eq` / `ne`, and `<` through `Ord`. An overload must mean the
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
explicitly**, handling `float`'s two traps: a **reflexive** `eq` with **canonical `±0.0`** (equal, so
must hash alike) for `Hash`, and a **total order** (IEEE `totalOrder`, `NaN` at an end) for `Ord`. A
stdlib total-order/hashable `float` wrapper is deferred.

**Iteration.** An **`Iterator[T]`** has `next() -> Result[T]` — `Left(v)` for the next element, or
`Right(StopIteration)` at the end (**`StopIteration`** is a built-in `Err`). An **`Iterable[T]`** has
`iter()`, producing a fresh `Iterator[T]`. The element type is pinned by **coherence** rather than by a
member kind: a type declares **at most one** `Iterable[T]` impl, so `for x in X` still has **one**
element type — it requires `X: Iterable[T]`, binds `x: T` to each `Left`,
**exits cleanly on `Right(StopIteration)`**, and **raises any other `Right(err)`** — a mid-stream failure
is never silently swallowed (drive `next()` by hand and `guard` to inspect it). Since `<-ch` already
yields `Result[T]`, **a channel is an `Iterator[T]`**: `for v in ch` drains it, ending on a
clean close and re-raising a producer crash. An `Iterator[T]` is trivially `Iterable[T]`, so **lazy
adapters** (`map`, `filter`, `take`, `zip`, …) are ordinary stdlib iterators that chain — each returns a
**concrete adapter type** (`map` a `Map[This, U]` that is `Iterator[U]`, holding the source and the
closure), so a chain stays fully **monomorphized**, no boxing. `for mut x in X` binds each element as an
in-place `mut` — only when `X` is `mut`.
