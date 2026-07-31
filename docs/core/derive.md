# Zerg Derive & Default Behavior

Two ways a type can pick up an implementation without its author spelling out every method — and the
firm line between them. This builds on **[Specs & Generics](specs.md)**. Also in
[繁體中文](derive.zh-TW.md).

## Two sources of "free" behavior

A type can be handed an implementation it didn't hand-write in **two** distinct ways, and Zerg keeps
them strictly apart:

| Source                      | Who writes it       | May read fields? | Input it works from    | User-definable? |
| --------------------------- | ------------------- | ---------------- | ---------------------- | --------------- |
| **behavioral default body** | the **spec** author | **no**           | `this` and its methods | **yes**         |
| **concrete impl**           | the **type** owner  | yes              | the type's own fields  | yes (by hand)   |
| **structural derive**       | the **compiler**    | yes (privileged) | the type's structure   | **no**          |

The concrete impl is the manual baseline — ordinary module-local code that can of course read its own
fields. The other two are the "free" tiers, and the rest of this note is about the line between them.

## The invariant: specs are field-blind

A `spec` — both its **required** signatures and its **provided** default bodies — sees a value only
through **`this`** and the methods its interface (and other specs') expose. It **never reads a field**.

Reading a value's **structure** — its fields, its variants — is a **compiler privilege**, not a
language-level capability. Nothing written as a `spec` can do it. This keeps three things intact at once:

- **abstraction stays independent of representation** — a bound `T: X` exposes behavior, never layout;
- **spec code carries no structural `match`** — no destructuring a struct or switching on a variant to
  provide a method, so specs stay small and general-purpose;
- **there is one place structure is read** — the compiler — so "who can see the fields?" has a single,
  auditable answer.

## Behavioral default — the spec's own, and user-definable

A provided method is a **default body written over other methods on `this`**, never over fields (the
invariant above). It lets a spec expose many methods from a small required core; an implementer
**inherits** them or **overrides** one. Every user spec can carry these — this is the **extensible**
tier.

```text
spec Summable {
    fn zero() -> This                       # required
    fn add(other: This) -> This             # required

    fn sum(items: list[This]) -> This {     # provided — reads only methods, no fields, no match
        mut acc := This.zero()
        for x in items { acc = acc.add(x) }
        return acc
    }
}
```

Implement `zero` and `add`; `sum` comes free. This derives **behavior from behavior** and touches no
structure, so it obeys field-blindness and is fully user-definable — the answer to "how do I define my
own reusable default?"

## Structural derive — the compiler's privilege, and closed

`#[derive(X)]` asks the **compiler** to generate the canonical `(T, X)` implementation by **reading
T's structure** — a product **field-by-field**, a sum **variant-by-variant**, recursing into each
field's own `X`. It's **not** sugar for an empty impl inheriting a default body: a spec has no
structural default (it's field-blind), so `derive` is a compiler **code generator keyed on a blessed
spec**, distinct from both tiers above.

**Why a user can't define a new structural derive.** Generating an impl from structure needs code that
**reads structure**. Such code can only be:

- a **spec / default body** — forbidden by field-blindness, or
- a **macro** — Zerg has none, by design, or
- the **compiler** — which is not user-authored.

So a user-defined structural derive is impossible **by construction**, not by omission. The derivable
set is **fixed and compiler-owned**; a user spec is never in it (`#[derive(UserSpec)]` is a compile
error). The extensible tier is the behavioral default above; the structural tier is closed.

## The derivable specs

The blessed set — each with a canonical structural reading the compiler owns. Every one is **opt-in**
via `derive`; there is **no auto-derived equality** and no implicit `Object` spec. Only **`Eq`** and
**`Ord`**, **`Hash`**, **`Encode`**, and **`Decode`** are all specified here but **[not
yet: Phase 2]** — naming one in a `#[derive(…)]` is a clean compile error today.

| Spec     | Structural rule                               | Requires (each field) | Excludes                 |
| -------- | --------------------------------------------- | --------------------- | ------------------------ |
| `Eq`     | `eq`/`ne` (`==` / `!=`) per field             | `Eq`                  | —                        |
| `Ord`    | lexicographic by field, then by variant order | `Eq` and `Ord`        | any `float` field        |
| `Hash`   | mix field / tag hashes (`equal ⇒ same hash`)  | `Hash`                | any `float` field        |
| `Encode` | product per field; sum: tag then payload      | `Encode`              | `chan`/`Ref`/`fn`/handle |
| `Decode` | rebuild per field / from tag + payload        | `Decode`              | `chan`/`Ref`/`fn`/handle |

`Eq` is the sole source of `==` / `!=` on a `struct` or `enum`: a type with neither `#[derive(Eq)]` nor a
hand-written `impl Eq` **cannot** be compared with `==` — that is a compile error, not a silent structural
default. `Ord` requires `Eq` (the super-spec `spec Ord: Eq`), so `#[derive(Ord)]` obliges you to derive —
or hand-write — `Eq` as well.

A field that fails the requirement makes the derive a **compile error naming that field**, never a
silent skip — `#[derive(Ord)]` on a `T` with a `float` field is rejected exactly as the hand-written rule in
[Specs & Generics](specs.md) demands (a `float` has no total order; author it by hand with a
canonical `±0.0` and `NaN` at an end).

Cross-cutting cases fall out of the existing memory model, no new rule:

- **Recursive / self-referential** (auto-boxed) types derive fine — the generated impl recurses through
  the transparent box like any other field.
- **A `Ref` value** (`chan`, `Ref[T]`) copies by refcount-bump (the memory model's rule, not a spec) and,
  under a derived `Eq`, compares by identity; it is not `Encode`/`Decode`, so a type holding one cannot
  derive those.
- **Adding an `enum` variant** re-derives automatically — there is no `match` for the author to update,
  because the compiler, not user code, walks the structure.

## `derive` semantics & coherence

- `#[derive(X)]` yields **the one canonical** `(T, X)` implementation — the same slot a hand impl
  fills, generated instead of written.
- To specialize, **hand-write `impl X for T { … }` instead** of deriving; you may not have both
  (duplicate impl is an error), consistent with **one canonical implementation per `(type, spec)`**.
- The **orphan rule is unchanged**: a `derive` is authored where a hand impl legally could be — the
  package that owns `T` or the package that owns `X`.
- Only a **blessed** spec may appear in `#[derive(…)]`; a user spec there is a compile error.

## Serialization — the worked example

> **[not yet: Phase 2]** `Encode` / `Decode` — and the `Sink` / `Source` specs used below — are specified
> but not implemented; `#[derive(Encode, Decode)]` is a compile error today, since the blessed derivable
> set is exactly `Eq` and `Ord`. The example below illustrates the **intended** shape of structural
> derivation for when they land.

Serialization is the case structural derive exists to serve: a mechanical, field-by-field mapping no
one should hand-write per type, yet one that needs neither reflection nor a macro.

```text
# stdlib specs — behavioral interfaces, field-blind like every spec
spec Encode {
    fn encode(mut out: Sink)
}
spec Decode {
    fn decode(mut src: Source) -> Result[This]     # This = the reconstructed value
}

#[derive(Encode, Decode)]             # the compiler reads User's structure, writes both canonical impls
struct User {
    id:    int
    name:  str
    tags:  list[str]
    email: str?
}
```

What the compiler generates — conceptually; you never write or see this — is the obvious field-by-field
walk, each field delegating to **its own** `Encode`:

```text
impl Encode for User {                            # generated, not written
    fn encode(mut out: Sink) {
        out.begin(4)
        out.field("id")
        this.id.encode(out)
        out.field("name")
        this.name.encode(out)
        out.field("tags")
        this.tags.encode(out)     # list[str]: length, then each str
        out.field("email")
        this.email.encode(out)    # str?: presence, then value
        out.end()
    }
}
```

A sum type derives over its variants — **tag, then payload**:

```text
#[derive(Encode)]                     # generated: write the variant tag, then each payload field
enum Shape {
    Circle(float)
    Rect(float, float)
}
```

When the wire format must differ, **hand-write the impl instead of deriving** — still one canonical
implementation, still no macro:

```text
impl Encode for User {                            # replaces the derived one
    fn encode(mut out: Sink) {
        out.field("uid")
        this.id.encode(out)       # custom key
        out.field("name")
        this.name.encode(out)
        # tags and email deliberately omitted from the wire form
    }
}
```

`Decode` returns `Result[This]`, so a malformed input is an ordinary value-tier failure — `guard`-free
on the happy path, `?`-propagated on error — never an abort. (`Result[T]` is not FFI-safe, but that's
no constraint here: `Encode`/`Decode` are pure-Zerg specs and never cross the C boundary — see the
[FFI](../runtime/ffi.md) reference.)
