# Zerg Formatting & Text

How every value renders — the built-in `display` / `debug` renderings, `f"…"` interpolation, and the
`print` keyword. Part of the [Language Reference](language.md). Also in [繁體中文](format.zh-TW.md).

Every value renders two ways. These are **built-in value renderings**, not methods of any `Object` spec —
Zerg has no auto-implemented `Object` spec ([Specs & Generics](specs.md)) — so `display` and `debug` are
available on every value without opting into anything:

- **`debug` — the developer view.** Rendered **structurally** (a sum by tag-then-payload), overridable.
  Logging, `stderr`, and an abort backtrace use it — mechanical, never guessed prose.
- **`display` — the human view.** Its **default is `debug`**, so it always exists. Override it for how an
  end user should read the value (a price, a date); the compiler never derives a semantic rendering, so a
  meaningful `display` is override-only.

> **Status.** Rendering a **scalar** or a **`str`** — through a plain `{x}` hole, `print`, or an f-string
> — is **[implemented]**. The **structural rendering of a composite** (a `struct`, `list`, or `map`) is
> **[not yet]**: a composite in a format hole is **rejected at compile time** today, so the intended "every
> value renders" holds for scalars and strings now, and for composites once structural `debug` lands. The
> exact spelling of a structural `debug` string is therefore **not pinned** ([not yet]).

**Interpolation — `f"…"`.** A plain `"…"` is a literal (braces are ordinary characters). An **`f`-string**
embeds `{ expr }`, rendered through `display` and joined — `f"sum={x + y}"` — **desugaring at compile
time** to `str` concatenation (Collections), with no variadics and no runtime format engine. A hole is
**Python-shaped** — `{ expr =? !conv? :spec? }`:

- **`{x}`** uses `display`; a **conversion** picks another view first — **`!r`** the developer `debug`,
  **`!s`** `display`, **`!a`** an ASCII-escaped debug. `f"{x!r}"` renders `x` through `debug`. **`!s`** is
  **[implemented]**.
  > **[deviation]** `!r` (debug) and `!a` (ascii) are currently **aliased to `display`** — they render
  > exactly as `!s` today rather than as the developer/ASCII-escaped view. The intended distinct `debug`
  > and ASCII renderings stand; the aliasing goes away when structural `debug` lands.
- **`{x=}`** is self-documenting: it prints the expression's source text, `=`, then the value —
  `f"{n=}"` → `n=42` (compose with the rest: `f"{n=:04d}"`). **[not yet]** — parsed, but **rejected at code
  generation** this phase.
- **`{x:spec}`** hands the spec string to the type's **`Format`** protocol — `f"{pi:.2f}"`, `f"{n:04d}"`,
  `f"{p:>10}"`. This is a **per-type protocol**, not a `display` parameter: the language fixes only the
  `:spec` **syntax** (opaque text up to `}`); what a spec **means** is the type's own — the stdlib numbers
  and `str` read the usual `[[fill]align][sign][#][0][width][.precision][type]`, mirroring Python. A format
  spec on a **scalar or `str`** is **[implemented]**.
  > **[implementation-defined]** Floating-point rendering — the default `%g`-style form (6 significant
  > figures) and the spelling of `NaN`, `Inf`/`-Inf`, and `-0.0` — is not pinned by the spec; a conforming
  > implementation documents its own. Do not depend on an exact float spelling.

**`print`** writes a value's `display` rendering and a newline to stdout — a **reserved keyword**, always
in scope with no import, so the smallest program is `print f"hello {name}"`. It is **best-effort** (a write
error is dropped, never raised), so it needs no `?`; the checked, full I/O surface is the imported `io`
package (see [Process & I/O](io.md)).
