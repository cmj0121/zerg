# Zerg Control Flow & Pattern Matching

The three control constructs — the `if` / `for` statements and the `match` expression — and the
patterns they destructure. Part of the [Language Reference](language.md). Also in
[繁體中文](control-flow.zh-TW.md).

## Control flow

All control flow is three constructs, split by what they yield: **`match`** produces a **value** (Pattern
matching); **`if`** and **`for`** are **statements** that run for effect. A value out of a branch always
comes from `match` (or `??` / `?.`) — `if` never yields one, so a choice produces a value one way, not two.

**`if`** — `if cond { … }` with optional `else` / `else if`; the condition is a `bool` (no truthiness).
The **binding form** `if x := expr { … }` runs the block only when `expr` matches the pattern `x` — the
one-arm-`match` sugar for a "value present" test (`if v := <-ch { use(v) }` fires only on a `Left`).

**`for`** — the one loop keyword, two forms: **`for { … }`** infinite (leave via `break` / `return`), and
**`for x in it { … }`** over an `it: Iterable`, binding `x` **by copy** each round (**`for mut x`** binds
in place, only when `it` is `mut`; the iteration protocol — clean `StopIteration` exit, any other error
re-raised — is [Iteration](specs.md)). There is **no `while` and no C-style three-clause `for`**: a condition loop is
`for { … break if done }`.

**`break` / `continue`** act on the **nearest `for`**; there are **no labels** (leave an outer loop by
extracting a function and `return`). The sugar **`break if cond`** / **`continue if cond`** is exactly
`if cond { break }` / `if cond { continue }`:

```text
for {
    line := <-input ?? break       # drain until the channel closes
    continue if line.empty()       # skip blank lines
    break if line == "quit"        # stop on a sentinel
    handle(line)
}
```

`for` is a statement — it yields no value; build a result with an iterator adapter (`map` / `filter` /
`fold`) or by appending into another collection ([Collections](collections.md)), never a break-with-value.

## Pattern matching

`match` is an **expression**: it tries a value against **arms** (`pattern -> result`), runs the first
that fits, and yields its result. Every arm yields the **same type**, so a `match` is a value usable at a
`:=`, a `return`, or an argument — arms that yield `nil` read as a plain statement. Coverage is
**advised, not forced** — miss a case and you just get a **warning** (a linter may enforce it), not a
compile error — so **adding an `enum` variant never breaks a dependent's `match`**. A trailing **`_`**
covers the rest; a value that reaches a `match` no arm covers **aborts** at runtime (`MatchError`), and a
**redundant** arm (one an earlier arm already covers) is a warning too.

A **pattern** is one of: a **variant with a payload binding** (`Left(v)`) — bound **by copy**, like
`?`/`return`, the source never invalidated; a **literal** (`0`, `"y"`, `true`) — matched by `equal`; a
**nested** pattern (`Left(Some(v))`); an **or-pattern** (`A | B ->`, its alternatives binding the same
names at the same types); or the **wildcard `_`**, matching anything and binding nothing.

```text
msg := match ev {
    Click(p)           -> render(p)
    Key(k) | Scroll(k) -> handle(k)
    _                  -> nil
}
```

A `match` **pattern** never inspects an existential's real type — a spec used as a type erases the value
one-way, with no downcast — it destructures variants and compares values, nothing more; the one query it
allows on an existential is the boolean **`is`** test ([Specs & Generics](specs.md)), used as a **condition**, never
as a binding that hands the concrete value back. A **product pattern** destructures
a `struct` **by field** (`Div{q, r}`) or a tuple **positionally** (`(a, b)`), binding each part by copy;
it works both in a `match` arm and at a plain `:=` binding (`(q, r) := divmod(x, y)`) — the way a multiple
return is consumed. **Guard conditions** (`Left(v) if v > 0`) remain deferred.
