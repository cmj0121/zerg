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

> **[not yet]** A **block used as an expression** is refused by name: a bare `{ … }` at a `:=`, in an
> argument, or after a `return` does not compile. The block-value rule holds where a construct asks for a
> block — an `if` branch, a `match` arm — and nowhere else, so a block never reaches a value position alone.

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

> **[deviation]** The rule that **every branch must yield the same type** is not checked for `if`.
> `x := if false { 1 } else { 2.5 }` compiles and prints `2` — the `float` arm truncated into the `int` the
> first arm settled on — and `if false { 1 } else { true }` prints `1`. When the two types have no C
> conversion between them the mismatch escapes the compiler entirely and `cc` reports it, against the
> generated C rather than against the Zerg that caused it. The same rule IS checked for `match`
> (`error: a match answers ONE type, and its arms give int and str`), which is what makes this an omission
> in one construct rather than a decision about how branches are typed.
>
> **[not yet]** Two shapes of the value form are refused by name. An **`else if` chain** in an if-expression
> (`x := if a { 1 } else if b { 2 } else { 3 }`) is one: the expression form takes a single trailing `else`,
> and the chain stays a statement. **if-let in an expression position** is the other — `return if x := opt {
use(x) } else { fallback }`, and any if-let reaching a `:=` or an argument, is turned away — so the
> binding form is a statement only this phase, not the "wherever an `if` does" the paragraph above specifies.

**`for`** — the one loop keyword, three forms: **`for { … }`** infinite (leave via `break` / `return`),
**`for x in it { … }`** over an `it: Iterable`, binding `x` **by copy** each round (**`for mut x`** binds
in place, only when `it` is `mut`; the iteration protocol — clean `StopIteration` exit, any other error
re-raised — is [Iteration](../core/specs.md)), and **`for cond { … }`** the **while** form — repeat while `cond`
(a `bool`) holds. There is **no `while` keyword** (bare `for cond` is the while loop) and **no C-style
three-clause `for`**. The infinite form, the while form, and `for x in it` over a **range**, a **`list`**, a
**`map`** (binding each **key**) all work. Over a **`str`** it binds each **`rune`** — the code points, not
the bytes; walk `list[byte](s)` when you want those. **`for mut x`**, the mutable loop binding that
writes each edited element back to its slot, is **[not yet]**. Testing membership with **`v in range`**
(`x in 0..n` → `bool`) is **[not yet]** — the form is refused by name — as is treating a **range as a
value** anywhere else; a range exists only as the thing a `for` walks and a `match` arm contains.

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

> **[not yet]** The iterator adapters are not built: `map`, `filter` and `fold` are each refused by name, so
> appending into another collection is the only way to carry a result out of a loop this phase.

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

> **[not yet]** The **redundant-arm warning** is not built: an arm an earlier arm already covers produces
> nothing at all — no warning, no note — and stays in the emitted code as an arm no value can reach.
> Coverage in the other direction, the case no arm handles, is checked and is an error.

A **pattern** is one of: a **variant with a payload binding** (`Left(v)`) — bound **by copy**, like
`?`/`return`, the source never invalidated; a **literal** (`0`, `"y"`, `true`, or a negative literal) —
matched by value; a **nested** pattern (`Left(Some(v))`); or the **wildcard `_`**, matching anything and
binding nothing. These, together with a **product pattern** (below) and a **range** arm (`1..=2 =>`, which
matches by containment), all fire. An **or-pattern** (`A | B =>`, and the binding form
`A(x) | B(x) =>` whose alternatives bind the same names at the same types) and a **list pattern**
(`[h, ..t]`) are **[not yet]**: `GRAMMAR` derives both, and the list pattern even type-checks.

> **[deviation]** A **`str` literal** arm never fires. `match s { "y" => 1  "n" => 0  _ => -1 }` answers
> `-1` for `s == "y"`: `--emit c` shows the arm lowered to a **pointer** comparison between the subject and
> the arm's literal, while `"y" == "y"` written as an expression in the same file lowers to
> `strcmp(…) == 0`. The `int`, `bool`, `rune` and negative-literal arms all compare by value as specified;
> only `str` is wrong, and it is wrong **silently** — there is no diagnostic, the trailing `_` absorbs every
> miss, and a `match` over strings behaves exactly as if the subject matched no case at all.
>
> **[not yet]** A **nested pattern** does not parse: `Left(Some(v))`, and `L(0)` too, are turned away with
> ``a pattern binding needs a name, and `(` is not one`` — a payload position takes a name and nothing else,
> so every pattern is one level deep. That also empties the Note below on nested exhaustiveness: there is no
> nested case for the checker to be weak about, because there is no nested pattern a program can write.
>
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
> another arm. This describes the intended checker, not one a program can meet today: the nested pattern it
> is weak on does not parse at all (above), so nothing reaches the weakness.

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

> **[not yet]** The workaround just offered is not writable: `x if x in <range>` needs the membership test
> `v in range`, which is refused by name (see `for`, above). So a range arm's value cannot be bound by any
> route today — not by the arm, which binds nothing by design, and not by the guard that was to stand in
> for it.
