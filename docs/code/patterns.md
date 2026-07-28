# Zerg Patterns & Idioms

How to write the everyday patterns — closures, chained pipelines, and builders — the Zerg way, with the
small core it already gives you. Part of the [Language Reference](../language.md). Also in
[繁體中文](patterns.zh-TW.md).

## Closures & higher-order functions

Zerg's anonymous function `fn(…) -> R { … }` **is** the closure — first-class, capturing only immutable
values and channels, by copy (see [Functions & Closures](functions.md)). Capturing an **immutable** value —
a scalar, or a non-POD `str` / `list` / `map` / `Ref` — is **[implemented]**; capturing a **`mut`** binding
is **[not yet]** (snapshot it into an immutable binding first). There is deliberately **no terser
`|x|` lambda**: a little verbosity nudges you toward Zerg's procedural-first style rather than deep
functional chains. Three ways to keep closures readable, in order of preference:

1. **Name the function** — first-class functions pass by name, so a named `fn` is reusable, testable, and
   clutter-free at the call site.
2. **Write a `for` loop** — often the clearest, and the most procedural-first.
3. **Inline `fn` with inferred types** — for a one-off, when the function type is known at the call site the
   parameter and result types may be omitted.

## Chained pipelines

Method calls chain (`a.f().g()`), so `map` / `filter` / `fold` compose. Prefer **named functions** over
inline closures:

```text
fn double(x: int)   -> int  { return x *% 2 }
fn positive(x: int) -> bool { return x > 0 }

result := xs.map(double).filter(positive).fold(0, add)
```

Or, procedural-first, just loop — the idiom [Control Flow](control-flow.md) endorses (append into another
collection):

```text
mut out := []
for x in xs {
    continue if not positive(x)
    out.append(double(x))
}
```

When an inline function is genuinely one-off, inferred types keep it short:

```text
ys := xs.map(fn(x) { return x *% 2 })      # x: int and -> int inferred from xs
```

## Builders

**Named arguments and default parameters are Zerg's builder.** Most fluent `Builder().x().y().build()`
ceremony exists only to make inputs optional — which [Functions & Closures](functions.md) already gives you
in one call:

```text
fn connect(host: str, port: int = 443, tls: bool = true, timeout: int = 30) -> Conn { … }

c := connect("example.com")                          # all defaults
c := connect("example.com", port: 8080, tls: false)  # override only what you name
```

For plain data, **construction is a call** with named fields, which does the same:

```text
cfg := Config(host: "example.com", port: 8080)
```

When you genuinely need a **staged / fluent** builder (e.g. a query builder), **copy-by-value makes a
fluent-immutable builder fall out** — each step reads `this`, modifies a copy, and returns it, so the chain
never shares mutable state:

```text
q := new_query().where("age > 18").order("name").limit(10)

# fn where(clause: str) -> Query {
#     mut q := this                # a local copy of the receiver
#     q.filters.append(clause)
#     return q
# }
```

To mutate the builder in place instead, use a `mut fn` method on a `mut` binding (inside a block or `with`).

## Destructuring & pattern support

Destructuring binds directly at a `:=`: a tuple `(a, b) := e` and a struct `P{x, y} := e` both unpack in one
step **[implemented]** — the everyday way a multiple return or a small record is consumed. In a `match` (see
[Control Flow](control-flow.md)) the **struct**, **tuple**, **variant**, **wildcard `_`**, **`as`**,
**range**, and **negative-literal** patterns, together with their **nesting**, are **[implemented]**. Two
forms `GRAMMAR` allows are **[not yet]**: an **or-pattern that binds** (`A(x) | B(x) =>`) and a **list
pattern** (`[h, ..t]`) — the list pattern parses and type-checks but is rejected at code generation.

## Deliberately not added

Two conveniences that other languages reach for are left out to stay small and procedural-first:

- **UFCS** (`x.f(y)` ≡ `f(x, y)`) would let free functions chain like methods, but adds a name-resolution
  rule.
- **A pipe operator** `|>` reads well for pipelines but is a new operator and leans functional.

Reach for a named function and a `for` loop first; they cover these cases without new syntax.
