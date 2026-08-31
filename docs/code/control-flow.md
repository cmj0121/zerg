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
(`E2047`).

**`if`** — as a **statement**, `if cond { … }` with optional `else` / `else if` runs for effect and yields
no value; the condition is a `bool` and there is **no truthiness**, so an optional there is _E3052 the
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
_E3020 an `if` expression answers ONE type, and its branches give int and float_, beside `match`'s `E3021`. A
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
> _E9032 NotImplemented: a binding head in an `if` EXPRESSION_ — so the binding form is a statement only this
> phase, not the "wherever an `if` does" the paragraph above specifies. The **`else if` chain** used to stand
> beside it and no longer does: `x := if a { 1 } else if b { 2 } else { 3 }` is built, yields the taken
> branch, and the one-type rule holds across the whole chain.
>
> Two limits of the value form keep their own sentences. A branch of more than one statement is _E9031
> NotImplemented: an `if` EXPRESSION whose branch has more than one statement — this compiler lowers the
> expression form to a conditional, which holds no statements. Use the `if` STATEMENT and assign in it_.
> And a branch whose value the compiler cannot name a type for is _E9069 NotImplemented: … whose value
> has no type this compiler can name — give the block's last expression a call, a literal or a typed
> binding_.

**`for`** — the one loop keyword, three forms: **`for { … }`** infinite (leave via `break` / `return`),
**`for x in it { … }`** over an `it: Iterable`, binding `x` **by copy** each round (**`for mut x`** binds
in place, only when `it` is `mut`; the iteration protocol — clean `StopIteration` exit, any other error
re-raised — is [Iteration](../core/specs.md)), and **`for cond { … }`** the **while** form — repeat while `cond`
(a `bool`) holds. There is **no `while` keyword** (bare `for cond` is the while loop) and **no C-style
three-clause `for`**. The infinite form, the while form, and `for x in it` over a **range**, a **`list`**, a
**`map`** (binding each **key**) all work. Over a **`str`** it binds each **`rune`** — the code points, not
the bytes; walk `bytearray(s)` when you want those. **`for mut x`**, the mutable loop binding that
writes each edited element back to its slot, is **[not yet]** (`E9025`). Testing membership with **`v in range`**
(`x in 0..n` → `bool`) works. Treating a **range as a value** anywhere else is **[not yet]** — the form is
refused by name and with a place (`E9077`); a range exists only as the thing a `for` walks, a `match` arm
contains, and an `in` tests against.

> **[not yet]** Membership holds for the bounds this compiler can hand to C's `>=`, which is every scalar
> but a `str`. A range bounded by anything else is _E9062 NotImplemented: `in` over a range of str — a
> range's members are found by comparing its bounds, and str is not a type this compiler compares that
> way_, so `"c" in "a".."z"` is not a program today though `GRAMMAR#range-expr` derives it. A set that is
> no set at all — `3 in 5` — reads the same way and is not the same answer: it is `E3119`, and nothing is
> coming for it.

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

> **[not yet]** The iterator adapters are not built: `xs.map(f)` is _E9056 NotImplemented: the list method
> `map` — this compiler has `len` and `append`_, and the same sentence answers _NotImplemented: the list
> method `filter`_ and _NotImplemented: the list method `fold`_. So appending into another collection is
> the only way to carry a result out of a loop this phase. Each is named rather than covered by a `…`,
> because `E9056` is split by a list of names and `make method-gaps` reads these markers to hold it.

## Pattern matching

`match` is an **expression**: it tries a value against **arms** (`pattern => result`, the arm separator
`=>` deliberately distinct from the `->` that introduces a function's return type), runs the first
that fits, and yields its result. An arm's body is an **expression** (`GRAMMAR#match-arm`), and a **block
is one** — so `pattern => { … }` holds several statements and still yields, its value being the block's
last statement's. What an arm's whole body may **not** be is a statement, because there the arm has
nothing left to yield: `1 => print "one"` is _E2073 `print` is a statement, and an expression
is wanted here_ (a `return` in an arm is the same refusal), and a reassignment or a send is _E9041_, which
names which of the two it found. Brace it and the arm has a block, which yields.
Every arm yields the **same type** (_E3021 a `match` answers ONE type, and its arms give … and …_), so a
`match` is a value usable at a `:=`, a `return`, or an argument — arms that yield `nil` read as a plain
statement. Coverage is **required** — a `match` that misses a case is _E4019 non-exhaustive match: missing
variant …_ (so **adding an `enum` variant a dependent's `match` doesn't handle breaks the build**, caught at
compile time rather than silently). A guarded or range arm (below) does **not** count toward coverage — the
compiler can't prove a guard holds — so a case still needs an **unguarded** arm or a trailing **`_`**.
Because every value is thus statically covered, `MatchError` is only the runtime backstop for that residual
guard-gap; a **redundant** arm (one an earlier arm already covers) is a warning.

> An arm an earlier arm already covers is reported, and the two halves are reported by two tools. A
> **catch-all** followed by anything is a compile error — _E4032 this catch-all arm makes the following
> arms unreachable_ — because a binding or `_` swallows every value and what follows it is certainly
> dead. A **duplicate** pattern, the same literal or the same variant written twice, is `zerg lint`'s
> _L108_: it is a mistake worth naming and not worth refusing a build over. A **guarded** arm covers
> nothing and neither rule counts it, since the guard may not fire. Coverage in the other direction, the
> case no arm handles, is checked and is an error.

A **pattern** is one of: a **variant with a payload binding** (`Left(v)`) — bound **by copy**, like
`?`/`return`, the source never invalidated; a **literal** (`0`, `"y"`, `true`, or a negative literal) —
matched by value; a **nested** pattern (`Left(Some(v))`); or the **wildcard `_`**, matching anything and
binding nothing. These, together with a **product pattern** (below), a **list
pattern** (`[a, b]`, `[a, ..]`, `[a, .., z]`) and a **range** arm (`1..=2 =>`, which matches by
containment), all fire. A list is matched by **length first** — `==` for a pattern that names every
element, `>=` for one carrying a `..` — and where the `..` sits decides the rest: an element before it is
at its own index, one after it is that far from the **end**. Only `[..]` is a catch-all. An **or-pattern**
(`A | B =>`, and the binding form `A(x) | B(x) =>`) fires too: its lowering is the **or** of its sides'
conditions, and the rule that makes it a form is that **both sides bind the same names** — the arm's body
is one body, so a name only one side supplies is a name that is sometimes there (_E4088_). Where they do
bind, the name reads through **whichever side matched**, and a side that covers everything makes the whole
one cover it: `_ | A` is `_` with extra words.

A **`str` literal** arm compares TEXT, through the same `strcmp` an expression's `==` uses. It lowered to
a **pointer** comparison, so `match s { "y" => 1  _ => -1 }` answered `-1` for `s == "y"` — silently, since
the trailing `_` absorbs every miss and two equal literals may or may not share storage.

> **[not yet]** Three of the pattern kinds above are refused **in the parser**, so none of them reaches the
> checker or the emitter, and each names itself:
>
> - a **nested pattern** — `Left(Some(v))`, and `L(0)` too — is _E9076 NotImplemented: a sub-pattern inside a
>   variant payload_, so a payload position takes a binding name or `_` and every pattern is one level deep;
> - a **NAMED rest** in a list pattern — `[a, ..rest]` — is _E9110_, and `[a, ..]` is not: a named one binds
>   a fresh list, and a `match` arm is an expression with nowhere to keep it, so every use of the name would
>   allocate another and release none;
> - a **NAMED rest** — `[a, ..rest]` — is _E9110 NotImplemented: a NAMED rest in a list pattern_. The
>   anonymous `..` is built; a named one binds a fresh list, and a `match` arm is an expression with
>   nowhere to keep it, so every use of the name would allocate another and release none.
>
> Refusing at the parse also empties the intended checker's one soft spot — exhaustiveness over **nested**
> payloads was to prove the top-level variants without proving every nested combination — since no nested
> case reaches it.

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
return is consumed. **The tuple pattern is built in a `match` arm**, and it nests: an element is a pattern,
so `((a, b), c)` and `((1, b), c)` are both arms, and a pattern whose elements all bind is a **catch-all**
without saying `_`. Its arity is a fact about the type and is checked as one — _E4082 a tuple pattern names
3 element(s) and a (int, int) has 2_, _E4083 a tuple pattern matches a tuple_. **The struct pattern is
built there too**, and naming its fields is what makes it ask three questions the positional shapes do not:
the type it names must be the value's — the name is an **assertion**, not a reference, so `Q{x}` over a `P`
is _E4085_ — every field it names must exist (_E4086_), and without a `..` it names them **all** (_E4087_,
which says which one is missing). That last one is the point of the opt-in: a struct gaining a field breaks
the patterns written before it, by name. **At a `:=` binding** both are still **[not yet]** — `E9021` and
`E9008` — so the arm and the binding are told apart in the message rather than sharing one.
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
