# Zerg Patterns & Idioms

How to write the everyday patterns — closures, chained pipelines, and builders — the Zerg way, with the
small core it already gives you. Part of the [Language Reference](../language.md). Also in
[繁體中文](patterns.zh-TW.md).

## Closures & higher-order functions

Zerg's anonymous function `fn(…) -> R { … }` **is** the closure — first-class, capturing by copy (see
[Functions & Closures](functions.md)). Capturing is built: an **immutable** value — a scalar, or a non-POD
`str` / `list` / `map` — and a **`mut`** binding alike, the latter taking the value it held at the point the
closure was written. There is deliberately **no terser
`|x|` lambda**: a little verbosity nudges you toward Zerg's procedural-first style rather than deep
functional chains. Three ways to keep closures readable, in order of preference:

1. **Name the function** — first-class functions pass by name, so a named `fn` is reusable, testable, and
   clutter-free at the call site.
2. **Write a `for` loop** — often the clearest, and the most procedural-first.
3. **Inline `fn` that fits its slot** — for a one-off, an untyped parameter takes its type from the
   function type the closure is checked against, and so does an omitted result type
   ([Type System](../core/type-system.md)). Where there is no such position — `f := fn (x) { … }` — there is
   nowhere to take either from: the parameter is a named error, and an absent `-> type` means nil, never an
   inference.

## Chained pipelines

Method calls chain (`a.f().g()`), so `map` / `filter` / `fold` compose. Prefer **named functions** over
inline closures:

```text
fn double(x: int)   -> int  { return x *% 2 }
fn positive(x: int) -> bool { return x > 0 }

result := xs.map(double).filter(positive).fold(0, add)
```

> **[not yet]** The adapters themselves do not exist. `xs.map(double)` reports _NotImplemented: the list
> method `map` — this compiler has `len` and `append`_, and `filter` and `fold` answer the same way, so the
> chain above has nothing to chain. Method calls do chain, and the ones a `list` has are the two named in
> that message; until the adapters land, the loop below is not merely the procedural-first alternative but
> the only spelling.

Or, procedural-first, just loop — the idiom [Control Flow](control-flow.md) endorses (append into another
collection):

```text
mut out: list[int] = []
for x in xs {
    continue if not positive(x)
    out.append(double(x))
}
```

The type is written on the left because the empty list has none of its own — `mut out := []` is _E336 the
binding `out` gives the empty list `[]`, which has no type of its own_. That matters more here than it
usually would: with the adapters unbuilt this loop is the only spelling, so it had better be one that
builds.

When an inline function is genuinely one-off, the parameter type its position supplies keeps it short:

```text
ys := xs.map(fn(x) -> int { return x *% 2 })   # x: int taken from xs; -> int written
```

> **[not yet]** What is left unbuilt in this line is `map`, marked above. The untyped parameter is not:
> `x` takes its type from the function type the closure is checked against, so the line could be written
> `xs.map(fn(x) { return x *% 2 })` the day the adapter exists.

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

> **[not yet]** Both calls above are the named-argument form, and named arguments are not built (see
> [Functions & Closures](functions.md)): `connect("example.com", port: 8080, tls: false)` and
> `Config(host: "example.com", port: 8080)` alike report _NotImplemented: the named argument `port:` — this
> compiler binds arguments by position only_. A struct is built positionally today —
> `Config("example.com", 8080)` — and a defaulted parameter can only be dropped off the end of a call, so the
> one-call builder this section recommends over the fluent ceremony is the part of it with nothing to run on.

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
step — the everyday way a multiple return or a small record is consumed; both are **[not yet]**, as are the
**struct**, **tuple** and **`as`** patterns in a `match`. Read a tuple back by static index (`.0` / `.1`)
and a struct by field. What a `match` does destructure is the **variant**, **wildcard `_`**, **range**, and
**negative-literal** patterns, together with their **nesting**. Two more
forms `GRAMMAR` allows are **[not yet]**: an **or-pattern** (`A | B =>`, binding or not) and a **list
pattern** (`[h, ..t]`). Both are rejected at code generation: the list pattern after type-checking, the
or-pattern because `|` there is read as the bitwise operator and an arm that matched the wrong value in
silence is worse than one that does not build. See [Control Flow](control-flow.md) for what `zerg fmt` can
do about the contiguous-integer case.

> **[not yet]** Of that list it is the **nesting** that does not exist; the four kinds each work on their
> own. A variant pattern's payload position accepts a binding name or `_` and nothing else — a
> sub-pattern there is never read — so `L(Yes(v))` and `L(0)` are both refused, by name and with a place:
> _E492 NotImplemented: a sub-pattern inside a variant payload, beginning at `…`_. (It used to be a bare
> parser message with no error code and no place, naming whichever token stood there.) A RESERVED WORD in
> that position is a different rule and keeps its own: `L(this)` is `E245`. Match one level, bind the
> payload, and `match` the binding in turn.

## Deliberately not added

Two conveniences that other languages reach for are left out to stay small and procedural-first:

- **UFCS** (`x.f(y)` ≡ `f(x, y)`) would let free functions chain like methods, but adds a name-resolution
  rule.
- **A pipe operator** `|>` reads well for pipelines but is a new operator and leans functional.

Reach for a named function and a `for` loop first; they cover these cases without new syntax.
