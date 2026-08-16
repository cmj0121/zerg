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

A **block reaches a value position on its own**: `x := { y := 1  y + 1 }`, a block as an argument, a
block after a `return`. Its value is its last statement's — an expression statement yields its expression,
and any other statement, or an empty block, yields `nil`. The `;` the lexer inserts only **separates**
statements; it does not discard the value the way a trailing `;` does in some languages. What decides
whether a `{` opens a block or a **map literal** is the `:` (see [Types](../core/types.md) and
`GRAMMAR#map-lit`), which is why the empty map is spelled `{:}` and a brace with no `:` is always a block.

At a **statement's start** the same braces are a block **statement** whose value is discarded, and a
`{`-opening expression at the start of an `if` / `for` / `with` / `match` head must be parenthesized
(`E290`).

**`if`** — as a **statement**, `if cond { … }` with optional `else` / `else if` runs for effect and yields
no value; the condition is a `bool` and there is **no truthiness**, so an optional there is _E354 the
condition of an `if` … must be bool, and this one is int? — bind it with `if v := x { … }`, which also hands
over what it holds_. With a **mandatory trailing `else`** it is instead an
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

**Every branch must yield the same type**, and both constructs say so in the same words —
_E321 an `if` expression answers ONE type, and its branches give int and float_, beside `match`'s `E322`. A
`nil` branch is the exception, and not one: it carries no type to disagree with, so
`x: int? = if c { 1 } else { nil }` is a carrier taking a value on one side and absence on the
other. Every other branch brings its own type, a literal included — a branch is not a typed
position for its sibling, the same way one match arm is not one for the next.

That `nil` branch lowers as **absent**: the fit into the carrier is distributed over the branches, so each
spelling enters the carrier on its own and `x: int? = if c { 1 } else { nil }` with a false `c` is empty —
`x ?? 99` answers `99`. It used to wrap the whole ternary as one present payload, so the `nil` became a zero
inside a present carrier and the absence was gone with nothing reported; the mirror image, `nil` in the
**then** branch, was refused outright, so one spelling complained and the other miscompiled in silence.

---

> **[not yet]** One shape of the value form is refused by name: **if-let in an expression position**.
> `return if x := opt { use(x) } else { fallback }`, and any if-let reaching a `:=` or an argument, reports
> _E270 NotImplemented: a binding head in an `if` EXPRESSION_ — so the binding form is a statement only this
> phase, not the "wherever an `if` does" the paragraph above specifies. The **`else if` chain** used to stand
> beside it and no longer does: `x := if a { 1 } else if b { 2 } else { 3 }` is built, yields the taken
> branch, and the one-type rule holds across the whole chain.

**`for`** — the one loop keyword, three forms: **`for { … }`** infinite (leave via `break` / `return`),
**`for x in it { … }`** over an `it: Iterable`, binding `x` **by copy** each round (**`for mut x`** binds
in place, only when `it` is `mut`; the iteration protocol — clean `StopIteration` exit, any other error
re-raised — is [Iteration](../core/specs.md)), and **`for cond { … }`** the **while** form — repeat while `cond`
(a `bool`) holds. There is **no `while` keyword** (bare `for cond` is the while loop) and **no C-style
three-clause `for`**. The infinite form, the while form, and `for x in it` over a **range**, a **`list`**, a
**`map`** (binding each **key**) all work. Over a **`str`** it binds each **`rune`** — the code points, not
the bytes; walk `bytearray(s)` when you want those. **`for mut x`**, the mutable loop binding that
writes each edited element back to its slot, is **[not yet]** (`E242`). Testing membership with **`v in range`**
(`x in 0..n` → `bool`) works. Treating a **range as a value** anywhere else is **[not yet]** — the form is
refused by name and with a place (`E493`); a range exists only as the thing a `for` walks, a `match` arm
contains, and an `in` tests against.

**`break` / `continue`** act on the **nearest `for`**; there are **no labels** (leave an outer loop by
extracting a function and `return`). The sugar **`break if cond`** / **`continue if cond`** is exactly
`if cond { break }` / `if cond { continue }`. The same postfix `if` carries a `return` and a `raise`:

```text
for {
    line := <-input ?? break       # drain until the channel closes
    continue if line == ""         # skip blank lines
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

> **[not yet]** The iterator adapters are not built: `map`, `filter` and `fold` are each _E444 NotImplemented:
> the list method `…` — this compiler has `len` and `append`_, so appending into another collection is the
> only way to carry a result out of a loop this phase.

## Pattern matching

`match` is an **expression**: it tries a value against **arms** (`pattern => result`, the arm separator
`=>` deliberately distinct from the `->` that introduces a function's return type), runs the first
that fits, and yields its result. An arm's body is an **expression** (`GRAMMAR#match-arm`), and a **block
is one** — so `pattern => { … }` holds several statements and still yields, its value being the block's
last statement's. What an arm's whole body may **not** be is a statement, because there the arm has
nothing left to yield: `1 => print "one"` is _E605 NotImplemented: `print` is a statement, and an expression
is wanted here_ (a `return` in an arm is the same refusal), and a reassignment or a send is _E607_, which
names which of the two it found. Brace it and the arm has a block, which yields.
Every arm yields the **same type** (_E322 a `match` answers ONE type, and its arms give … and …_), so a
`match` is a value usable at a `:=`, a `return`, or an argument — arms that yield `nil` read as a plain
statement. Coverage is **required** — a `match` that misses a case is _E428 non-exhaustive match: missing
variant …_ (so **adding an `enum` variant a dependent's `match` doesn't handle breaks the build**, caught at
compile time rather than silently). A
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
(`[h, ..t]`) are **[not yet]**: `GRAMMAR` derives both.

A **`str` literal** arm compares TEXT, through the same `strcmp` an expression's `==` uses. It lowered to
a **pointer** comparison, so `match s { "y" => 1  _ => -1 }` answered `-1` for `s == "y"` — silently, since
the trailing `_` absorbs every miss and two equal literals may or may not share storage.

> **[not yet]** Three of the pattern kinds above are refused **in the parser**, which is why none of them
> reaches the checker or the emitter and why each names itself:
>
> - a **nested pattern** — `Left(Some(v))`, and `L(0)` too — is _E492 NotImplemented: a sub-pattern inside a
>   variant payload_, so a payload position takes a binding name or `_` and every pattern is one level deep;
> - an **or-pattern** is _E241 NotImplemented: an or-pattern_ — `|` there would otherwise read as the bitwise
>   operator, folding `1 | 2 =>` to `3 =>` and matching neither side, which is the silent wrong answer a
>   compiler must not give. `zerg fmt` rewrites the one case with a working spelling (consecutive integers
>   become the range `1..=2`, rule `F408`);
> - a **list pattern** is _E240 NotImplemented: a list pattern in a `match` arm_ — destructure a list with
>   indexing and a slice instead.
>
> Refusing at the parse is also what empties the intended checker's one soft spot: exhaustiveness over
> **nested** payloads was to be weaker than full coverage, proving the top-level variants without proving
> every nested combination. There is no nested case left for it to be weak about.

```text
msg := match ev {
    Event.Click(p)  => render(p)
    Event.Scroll(d) => scroll(d)
    _               => nil
}
```

A `match` **pattern** never inspects an existential's real type — a spec used as a type erases the value
one-way, with no downcast — it destructures variants and compares values, nothing more; the one query it
allows on an existential is the boolean **`is`** test ([Specs & Generics](../core/specs.md)), used as a
**condition**, never
as a binding that hands the concrete value back. A **product pattern** destructures
a `struct` **by field** (`Div{q, r}`) or a tuple **positionally** (`(a, b)`), binding each part by copy;
it works both in a `match` arm and at a plain `:=` binding (`(q, r) := divmod(x, y)`) — the way a multiple
return is consumed. The product pattern is **[not yet]**: destructure with `.0` / `.1` and field access.
Each of its four shapes is refused by its own name — `E238` and `E221` at a binding, `E232` and `E243` in
an arm — so the tuple and the struct are told apart in the message rather than sharing one.
**Guard conditions** work — an
arm may carry an **`if expr`** after its pattern (`Left(v) if v > 0`) that must also hold for the arm to
fire; the guard sees the pattern's **bindings**, and on `A | B if c` (once or-patterns land — see above)
covers the **whole or-pattern**. A guarded arm does **not** count toward exhaustiveness, so a guarded case
still needs an unguarded arm or `_`. A **range arm** (`200..300 =>`, `400..=499 =>`, `500.. =>`) is a
match-only sugar for `_ if _ in <range>` — it matches by **range containment** (not value equality), binds
**nothing**, and likewise doesn't count toward coverage (write `x if x in <range>` to bind the value). Its
**bounds are compile-time constants**: a literal, or the **name** of one
([`GRAMMAR#range-bound`](../../GRAMMAR)), which is any `:=` or `const` binding whose initializer the
compiler folds — so `LO..HI` and `MID..=HI` read as the numbers they name, and a bound built out of other
constants (`const MID := LO + 50`) is as good as a written one. A name that is **not** one — a value read
at run time, a `mut` binding — is reported at the arm that wanted it, not at the binding's own line; a
**call** is not a bound at all, since the production derives none, so `f()..300` is turned away by the
parser.
