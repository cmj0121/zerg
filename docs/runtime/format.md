# Zerg Formatting & Text

How every value renders — the built-in `display` / `debug` renderings, `f"…"` interpolation, and the
`print` keyword. Part of the [Language Reference](../language.md). Also in [繁體中文](format.zh-TW.md).

Every value renders two ways. These are **built-in value renderings**, not methods of any `Object` spec —
Zerg has no auto-implemented `Object` spec ([Specs & Generics](../core/specs.md)) — so `display` and `debug` are
available on every value without opting into anything:

- **`debug` — the developer view.** Rendered **structurally** (a sum by tag-then-payload), overridable.
  Logging, `stderr`, and an abort backtrace use it — mechanical, never guessed prose.
- **`display` — the human view.** Its **default is `debug`**, so it always exists. Override it for how an
  end user should read the value (a price, a date); the compiler never derives a semantic rendering, so a
  meaningful `display` is override-only.

An **override** is a method in the type's `impl` with a fixed shape: `fn display() -> str` (or
`fn debug() -> str`) — it receives the value alone, answers the `str` it shows as, and never mutates
(it is a plain `fn`, not a `mut fn`); a method of either name with any other shape is refused at its
declaration. `print`, a format hole and `str(…)` all consult the override, and a type that writes only
`debug` renders through it everywhere, since `display` defaults to it.

> **Status.** Every value renders except one class. A **scalar**, a **`str`**, an **`Err`** and a
> **composite** — a `struct`, an `enum`, a `list`, a `map`, a tuple, an array, a carrier — all render
> through a plain `{x}` hole, `print`, `str(x)` and the two methods, and an **override is consulted**
> first on any named type (`type X = Y`, a `struct`, an `enum`) that declares one. An `Err` renders as its
> **message**; its kind is there to be compared (`e is IOError`), not read out. What does **not** render is
> the class that has no parts and is waiting for nothing: a **channel**, a **function value** — _E4076 a …
> is an identity rather than a value, and the language gives it no rendering_ — and **nil**, which is not a
> value at all (_E3086 this rendering needs a value, and this one is nil_).
>
> **The structural spellings are the language's own literals.** A composite renders as the literal a reader
> would write for it, with one difference that is the whole point of a developer view: a **`str` inside a
> composite is quoted and escaped**, because `["a, b"]` and `["a", "b"]` are two different lists. A
> `str` **on its own** is its own text — the quoting is a property of the POSITION and not of the view,
> which is
> what keeps `print s` printing `hi` while `print [s]` prints `["hi"]`.
>
> | shape               | renders as                                                                         |
> | ------------------- | ---------------------------------------------------------------------------------- |
> | `list[T]`, `[T; N]` | `[1, 2]`                                                                           |
> | `map[K, V]`         | `{"k": 1}` — in **insertion** order, the order a `for` walks it in                 |
> | tuple               | `(1, "x")`                                                                         |
> | `struct P`          | `P(x: 1, y: "a")` — the constructor, with the names a reader would otherwise count |
> | `enum E`            | `E.A`, and `E.B(3)` for a variant that carries a payload                           |
> | `T?`                | the value, or `nil`                                                                |
> | `Either[X, Y]`      | `Left(1)` / `Right(…)` — tag, then payload                                         |
>
> A composite has no structural **equality**, which is the same question asked of a different verb: `xs ==
ys` over two lists is `E9057` ([Specs & Generics](../core/specs.md)). Rendering is derived and comparing
> is not — the two are separate decisions, and the reason is that a rendering is a view while an equality
> is a claim about the values.
>
> **All four spellings reach the same generator.** `str(x)`, a hole, `print` and `x.display()` /
> `x.debug()` written out consult the override first and fall to the structural rendering, so a `map` does
> not get the map's sentence about `len` and `has` for a rendering, and none of the four can disagree with
> the other three about a value.
>
> **The class that renders nowhere says so in one sentence.** A **channel** and a **function value** are an
> identity rather than a value — the same class `==` is refused on, _E4034_ — so there are no parts to
> render and nothing is coming: _E4076 a … is an identity rather than a value, and the language gives it no
> rendering_, from all four spellings. **nil** is a third answer, because nil is not a value at all: a `fn`
> with no `-> type` answers it ([`GRAMMAR#fn-decl`](../../GRAMMAR)), and `str(f())` is told so by name —
> _E3086 this rendering needs a value, and this one is nil_ — where what the reader needs is a `fn` that
> answers with something, not a rendering.

**Interpolation — `f"…"`.** A plain `"…"` is a literal (braces are ordinary characters). An **`f`-string**
embeds `{ expr }`, rendered through `display` and joined — `f"sum={x + y}"` — **desugaring at compile
time** to `str` concatenation (Collections), with no variadics and no runtime format engine. A hole is
**Python-shaped** — `{ expr =? !conv? :spec? }`:

- **`{x}`** uses `display`; a **conversion** picks another view first — **`!r`** the developer `debug`,
  **`!s`** `display`, **`!a`** an ASCII-escaped debug. `f"{x!r}"` renders `x` through `debug`. All three
  are **[not yet]** — _E9013 NotImplemented: an f-string '!r' / '!s' / '!a' conversion_.
- **`{x=}`** is self-documenting: it prints the expression's source text, `=`, then the value —
  `f"{n=}"` → `n=42` (compose with the rest: `f"{n=:04d}"`). **[not yet]** — recognized and then **refused by
  the parser** (`E9014`) this phase.
- **`{x:spec}`** hands the spec string to the type's **`Format`** protocol — `f"{pi:.2f}"`, `f"{n:04d}"`,
  `f"{p:>10}"`. This is a **per-type protocol**, not a `display` parameter: the language fixes only the
  `:spec` **syntax** (opaque text up to `}`); what a spec **means** is the type's own — the stdlib numbers
  and `str` read the usual `[[fill]align][sign][#][0][width][.precision][type]`, mirroring Python. A format
  spec is **[not yet]** — _E9012 NotImplemented: an f-string ':spec' format spec_.

  > **A spec is text the program wrote, and every field of it is bounded.** The `type` letter is a
  > **closed set** per rendering — a float takes `e E f F g G`, an int `b o x X c d`, a `str` `s` — and
  > `width` and `precision` have implementation limits ([Conformance](../conformance.md)). A spec
  > outside either is refused by name as a `ValueError`. Today that refusal is the **runtime's**: the
  > spec form itself is `[not yet]` in this implementation, so the compiler that would check one is
  > the one that does not build it. This is not a nicety. The letter used to be spliced into the C
  > formatter's own pattern, so `{x:.6s}` rendered a float through `%s` — a pointer read of a number —
  > and `{x:.6n}` reached `%n`, which **writes** through its argument.
  > **[implementation-defined]** Floating-point rendering — the default `%g`-style form (6 significant
  > figures) and the spelling of `NaN`, `Inf`/`-Inf`, and `-0.0` — is not pinned by the spec; a conforming
  > implementation documents its own. Do not depend on an exact float spelling.

**`print`** writes a value's `display` rendering and a newline to stdout — a **reserved keyword**, always
in scope with no import, so the smallest program is `print f"hello {name}"`. It is **best-effort** (a write
error is dropped, never raised), so it needs no `?`; the checked, full I/O surface is the imported `io`
package (see [Process & I/O](io.md)).
