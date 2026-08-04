# Zerg Control Flow & Pattern Matching

The three control constructs — the `if` / `for` statements and the `match` expression — and the
patterns they destructure. Part of the [Language Reference](../language.md). Also in
[繁體中文](control-flow.zh-TW.md).

## Control flow

All control flow is three constructs, split by what they yield: **`match`** is an **expression** that
produces a **value** (Pattern matching); **`for`** is a **statement** that runs for effect; **`if`** is
**both** — a statement, and (with a mandatory trailing `else`) an expression. A **block** is itself an
expression whose value is its **last statement's value**, so a branch that ends in an expression carries
that value out.

**`if`** — as a **statement**, `if cond { … }` with optional `else` / `else if` runs for effect and yields
no value; the condition is a `bool` (no truthiness). With a **mandatory trailing `else`** it is instead an
**expression** (`if-expr`, a `primary`): it yields the **taken branch's block value**, and **every branch
must yield the same type** (`x := if hot { warm() } else { cool() }`). At statement position the statement
form wins, so the value form is the one that reaches a `:=`, a `return`, or an argument. The **binding
form** `if x := expr { … }` (if-let) runs the block only when `expr` is **present** — an optional holding a
value, a `<-ch` that received (a `Left`) — and **binds the unwrapped value** as `x` in the **then-block
only**: `x` is not in scope in the `else`, nor after the `if`. It is the one-arm-`match` sugar for a "value
present" test (`if v := <-ch { use(v) }` fires only on a `Left`), and works **wherever an `if` does** — as a
statement, as an expression (with a trailing `else`), and as a returned if-expression (`return if x := opt {
use(x) } else { fallback }`). It carries a non-POD `str?` too — the unwrapped `str` binds in the
then-block only.

**`for`** — the one loop keyword, three forms: **`for { … }`** infinite (leave via `break` / `return`),
**`for x in it { … }`** over an `it: Iterable`, binding `x` **by copy** each round (**`for mut x`** binds
in place, only when `it` is `mut`; the iteration protocol — clean `StopIteration` exit, any other error
re-raised — is [Iteration](../core/specs.md)), and **`for cond { … }`** the **while** form — repeat while `cond`
(a `bool`) holds. There is **no `while` keyword** (bare `for cond` is the while loop) and **no C-style
three-clause `for`**. The infinite form, the while form, and `for x in it` over a **range**, a **`list`**, a
**`map`** (binding each **key**) all work. Over a **`str`** it binds each **`rune`** — the code points, not
the bytes; walk `list[byte](s)` when you want those. **`for mut x`**, the mutable loop binding that
writes each edited element back to its slot, is **[not yet]**. Testing membership with **`v in range`**
(`x in 0..n` → `bool`) works; treating a **range as a value** anywhere else is **[not yet]**.

**`break` / `continue`** act on the **nearest `for`**; there are **no labels** (leave an outer loop by
extracting a function and `return`). The sugar **`break if cond`** / **`continue if cond`** is exactly
`if cond { break }` / `if cond { continue }`. The same postfix `if` carries a `return` and a `raise`:

```text
for {
    line := <-input ?? break       # drain until the channel closes
    continue if line.empty()       # skip blank lines
    break if line == "quit"        # stop on a sentinel

    handle(line)
}
```

**`return`** carries the same postfix `if`: **`return expr if cond`** is sugar for `if cond { return expr }`
(and **`return if cond`** for a bare early exit) — on a **false** condition control **falls through**
(`return MAX if v > MAX`). Mind the disambiguation: a leading `if` with a **block** after the condition is
instead an **if-expression being returned** (`return if c { a } else { b }` yields a value); the
conditional-return `if` takes a **bare condition and no block**.

`for` is a statement — it yields no value; build a result with an iterator adapter (`map` / `filter` /
`fold`) or by appending into another collection ([Collections](collections.md)), never a break-with-value.

## Pattern matching

`match` is an **expression**: it tries a value against **arms** (`pattern => result`, the arm separator
`=>` deliberately distinct from the `->` that introduces a function's return type), runs the first
that fits, and yields its result. Every arm yields the **same type**, so a `match` is a value usable at a
`:=`, a `return`, or an argument — arms that yield `nil` read as a plain statement. Coverage is
**required** — a `match` that misses a case is a **compile error** (so **adding an `enum` variant a
dependent's `match` doesn't handle breaks the build**, caught at compile time rather than silently). A
guarded or range arm (below) does **not** count toward coverage — the compiler can't prove a guard holds —
so a case still needs an **unguarded** arm or a trailing **`_`**. Because every value is thus statically
covered, `MatchError` is only the runtime backstop for that residual guard-gap; a **redundant** arm (one an
earlier arm already covers) is a warning.

A **pattern** is one of: a **variant with a payload binding** (`Left(v)`) — bound **by copy**, like
`?`/`return`, the source never invalidated; a **literal** (`0`, `"y"`, `true`, or a negative literal) —
matched by value; a **nested** pattern (`Left(Some(v))`); or the **wildcard `_`**, matching anything and
binding nothing. These, together with a **product pattern** (below) and a **range** arm (`1..=2 =>`, which
matches by containment), all fire. An **or-pattern** (`A | B =>`, and the binding form
`A(x) | B(x) =>` whose alternatives bind the same names at the same types) and a **list pattern**
(`[h, ..t]`) are **[not yet]**: `GRAMMAR` derives both, and the list pattern even type-checks.

> **An or-pattern is refused, by name.** `|` in pattern position is read as the bitwise operator, so
> `1 | 2 =>` would fold to `3 =>` and match neither 1 nor 2 — it used to compile and be silently wrong, and
> that is exactly what a compiler must not do, so the arm is now turned away instead. `zerg fmt` rewrites
> the one case that has a working spelling (consecutive integers become the range `1..=2`, rule `F408`);
> everything else waits on the language work.

```text
msg := match ev {
    Click(p)  => render(p)
    Scroll(d) => scroll(d)
    _         => nil
}
```

> **Note.** Exhaustiveness checking of **nested** payloads is currently weaker than full coverage: the
> compiler proves the top-level variants are covered but does not always prove every nested combination, so
> a nested case may compile and fall to the `MatchError` backstop where a fully precise checker would demand
> another arm.

A `match` **pattern** never inspects an existential's real type — a spec used as a type erases the value
one-way, with no downcast — it destructures variants and compares values, nothing more; the one query it
allows on an existential is the boolean **`is`** test ([Specs & Generics](../core/specs.md)), used as a
**condition**, never
as a binding that hands the concrete value back. A **product pattern** destructures
a `struct` **by field** (`Div{q, r}`) or a tuple **positionally** (`(a, b)`), binding each part by copy;
it works both in a `match` arm and at a plain `:=` binding (`(q, r) := divmod(x, y)`) — the way a multiple
return is consumed. The product pattern is **[not yet]**: destructure with `.0` / `.1` and field access.
**Guard conditions** work — an
arm may carry an **`if expr`** after its pattern (`Left(v) if v > 0`) that must also hold for the arm to
fire; the guard sees the pattern's **bindings**, and on `A | B if c` (once or-patterns land — see above)
covers the **whole or-pattern**. A guarded arm does **not** count toward exhaustiveness, so a guarded case
still needs an unguarded arm or `_`. A **range arm** (`200..300 =>`, `400..=499 =>`, `500.. =>`) is a
match-only sugar for `_ if _ in <range>` — it matches by **range containment** (not value equality), binds
**nothing**, and likewise doesn't count toward coverage (write `x if x in <range>` to bind the value).
