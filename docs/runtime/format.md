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

> **Status.** Rendering a **scalar**, a **`str`**, or an **`Err`** — through a plain `{x}` hole, `print`,
> or an f-string — works, and an **override is consulted** on any named type (`type X = Y`, a `struct`, an
> `enum`) that declares one. An `Err` renders as its **message**; its kind is there to be compared
> (`e is IOError`), not read out. The **structural default rendering of a composite** (a `struct`, `list`,
> or `map` with no override) is **[not yet]**: such a composite is **rejected at compile time** today, and
> by two codes at the two doors — _E9059 NotImplemented: rendering a `P` as text — a composite needs the
> structural `Display` this compiler does not generate; render its fields_ from `print`, a hole and
> `str(x)` alike, and _E4011 `str(…)` over a list bridges bytes or code points_ where the argument is a
> `list` of something else. An **`enum`** is a third door with a third code — _E9085 NotImplemented:
> rendering an `E` as text — an enum has no name for it_ — which the 0.2.0 re-measurement found unnamed
> here (#74). So the intended "every value renders" holds for scalars, strings, errors and
> overridden types now, and for a **composite** once structural `debug` lands. It does not hold for every
> remaining receiver: a **channel**, a **function value** and **nil** are waiting for nothing, and the
> paragraph on the three of them below says why a structural `debug` would not be their answer if it
> arrived. The exact spelling of a structural `debug` string is therefore **not pinned** ([not yet]).
>
> It is one gap with a third face: a composite has no structural **equality** either, so `xs == ys` over
> two lists is `E9057` ([Specs & Generics](../core/specs.md)). Rendering and comparing are the two things a
> reader most expects a container to do for free, and neither is derived.
>
> **The renderings are reached by name, not by call.** `str(x)`, a hole and `print` consult them on every
> value, and `x.display()` / `x.debug()` written out reach the override alone: a type that declares one
> answers through it, and a value that has not — an `int`, a `str`, a `list`, a `map`, an `Err`, a carrier —
> is **[not yet]**, _E9107 NotImplemented: the method `display` on a int — `str(x)` renders it, and an
> `impl` on a declared type is how a type overrides that_, and the same sentence for _NotImplemented: the
> method `debug` on a int_. It is one answer for every receiver, which is what "on every value" means: a
> `map` does not get the map's sentence about `len` and `has` for a rendering. So "available on every value"
> holds through the three spellings above and not yet through the fourth.
>
> **What to write while it waits is NOT one answer for every receiver**, and that is the paragraph above
> read back against this one. `str(x)` stands in only where the value renders at all — a scalar, a `str`,
> an `Err`, a `list[byte]`, a `list[rune]`, or a type with an override. Both list spellings are there
> because `str(…)` **bridges** them — bytes and code points are the two ways back to a string — and it is
> the bridge and not the word "list" that decides it. On the composites the Status note rejects there is
> nothing to stand in, because the fourth spelling is waiting on the same gap the first three are, and the
> message says so rather than naming an expression this same compiler refuses: _E9107 NotImplemented: the
> method `display` on a list[int] — there is nothing to write in its place — this value has no rendering of
> its own until the structural `Display` this compiler does not generate, so render its parts_. For a
> composite it is one gap and not two.
>
> **Three receivers are in neither set, and what they are missing is not what a composite is missing.** A
> composite is _waiting_: the structural `Display` above is exactly what stands between it and a rendering,
> and its parts are what to render meanwhile. A **channel** and a **function value** are not waiting for
> anything. They are an identity rather than a value — the same class `==` is refused on, _E4034 a … is an
> identity rather than a value, and the language gives it no equality_ — so there are no parts to render
> instead, and a `Display` would not be their answer if it arrived: _E9107 NotImplemented: the method
> `display` on a chan[int] — there is nothing to write in its place — a chan[int] is an identity rather than
> a value_. **nil** is a third answer, because nil is not a value at all: a `fn` with no `-> type` answers it
> ([`GRAMMAR#fn-decl`](../../GRAMMAR)), `str(f())` is told so by name — _E3086 this rendering needs a value,
> and this one is nil_ — and what the reader needs is a `fn` that answers with something, not a rendering.
> So `str(x)` stands in on the first set; on the second there is nothing to stand in and something to wait
> for; and on these three there is nothing to stand in and nothing coming.

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
