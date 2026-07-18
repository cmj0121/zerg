# Zerg Formatting & Text

How every value renders — the `debug` / `display` `Object` methods, `f"…"` interpolation, and the
`print` keyword. Part of the [Language Reference](language.md). Also in [繁體中文](format.zh-TW.md).

Every value renders two ways, both **`Object` methods** (no `spec` to opt into):

- **`debug() -> str`** — the **developer** view: **auto-derived** structurally (a sum by
  tag-then-payload), overridable. Logging, `stderr`, and an abort backtrace print it — mechanical, never
  guessed prose.
- **`display() -> str`** — the **human** view; its **default body is `debug()`**, so it always exists.
  Override it for how an end user should read the value (a price, a date); the compiler never derives a
  semantic rendering, so `display` is override-only.

**Interpolation — `f"…"`.** A plain `"…"` is a literal (braces are ordinary characters). An **`f`-string**
embeds `{ expr }`, rendered through `display()` and joined — `f"sum={x + y}"` — **desugaring at compile
time** to `str` concatenation (Collections), with no variadics and no runtime format engine. A hole is
**Python-shaped** — `{ expr =? !conv? :spec? }`:

- **`{x}`** uses `display()`; a **conversion** picks another view first — **`!r`** the developer `debug()`,
  **`!s`** `display()`, **`!a`** an ASCII-escaped debug. `f"{x!r}"` is `f"{x.debug()}"`.
- **`{x=}`** is self-documenting: it prints the expression's source text, `=`, then the value —
  `f"{n=}"` → `n=42` (compose with the rest: `f"{n=:04d}"`).
- **`{x:spec}`** hands the spec string to the type's **`Format`** protocol — `f"{pi:.2f}"`, `f"{n:04d}"`,
  `f"{p:>10}"`. This is a **per-type protocol**, not a `display` parameter: the language fixes only the
  `:spec` **syntax** (opaque text up to `}`); what a spec **means** is the type's own — the stdlib numbers
  and `str` read the usual `[[fill]align][sign][#][0][width][.precision][type]`, mirroring Python.

**`print`** writes `x.display()` and a newline to stdout — a **reserved keyword**, always in scope with no
import, so the smallest program is `print f"hello {name}"`. It is **best-effort** (a write error is
dropped, never raised), so it needs no `?`; the checked, full I/O surface is the imported `io` package
(see [Process & I/O](io.md)).
