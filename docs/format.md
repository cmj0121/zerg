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
time** to `str` concatenation (Collections), with no variadics and no runtime format engine;
`f"{x.debug()}"` embeds the developer view. **Specifiers** `f"{x:>.2f}"` (width/precision/base/alignment)
are **deferred** to a separate per-type **format protocol**, not a `display` parameter.

**`print`** writes `x.display()` and a newline to stdout — a **reserved keyword**, always in scope with no
import, so the smallest program is `print f"hello {name}"`. It is **best-effort** (a write error is
dropped, never raised), so it needs no `?`; the checked, full I/O surface is the imported `io` package
(see [Process & I/O](io.md)).
