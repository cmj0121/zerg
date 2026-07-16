# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What Zerg is

A **compiled, general-purpose language** that transpiles Zerg source to **C source** (default
**C17**, fallback **C99**), which a C compiler then builds into a native binary. C is the codegen
target, not just an FFI boundary. The **bootstrap compiler is written in Go**, kept intentionally
minimal.

**State:** inception — docs and scaffolding only (`README.md`, `docs/language.md`, each with a
`zh-TW` mirror, plus `Makefile` and lint/hook config). No compiler or stdlib source in tree yet;
`Makefile`'s `build`/`test`/`run` targets are empty placeholders. Read the `Makefile` before
claiming a command works; add real targets rather than inventing them. README stays high-level;
detailed language semantics live in `docs/language.md` (keep both language versions in sync).

## Design principles (drive every decision)

`small and crisp` (one way to do a thing) · `safe by default` (immutable/private unless `mut`/`pub`)
· `null-safe` · `scope-owned` (no GC; freed at scope exit) · `copy-by-value` · `strongly typed` ·
`explicit casts` (no implicit conversion unless a type opts in to an auto-cast) · `concurrent` ·
`procedural-first`.

## Language semantics (decided — full prose in `docs/language.md`)

- **Primitives (fixed set; no `i8`/`i16`/… ladder):** `bool`, `byte` (u8, the char), `rune` (Unicode
  code point), `int` (i64), `uint` (u64), `float` (f64), `str` (immutable, null-terminated Unicode, no
  NUL), `nil` (`T?` placeholder). `str` iterates as `rune`, **not indexable** (`list[byte]` for
  raw/binary); implements `Ord` (code-point/byte lexicographic, not locale collation), `Hash` (key),
  `Add` (`+` concatenates → new str; build in loops via list-collect, not repeated `+`). Build =
  `str(...)` from `list[rune]`/`list[byte]`, validates UTF-8/no-NUL → raises (checked = `guard`);
  value→text formatting/interpolation deferred. Integer overflow + int ÷0 → runtime error
  (`OverflowError`/`DivideByZeroError`), never wraps; wrapping opt-in via `+%`/`-%`/`*%` (mod 2^n),
  `checked` = `guard`, saturating deferred.
  **Bitwise** `& | ^ ~ << >>` (`>>` arithmetic on `int`, logical on `uint`/`byte`; over-shift raises;
  overloadable via specs). Mixed `int`/`uint` = compile error (explicit cast). **Literals** are untyped,
  context-typed (default `int`/`float`), fit-checked at compile time, int↛float (write `1.0`/`float(1)`).
  Narrowing cast out-of-range raises; truncate via `byte(x & 0xFF)`; `float`→int drops fraction, raises
  on out-of-range/NaN/Inf. `float` = pure IEEE-754 (`±Inf`/`NaN`, no raise; `NaN` ≠ everything); no
  `Ord`/`Hash`. A composite **containing** `float` inherits this: auto-`equal` non-reflexive on `NaN`, no
  auto `Ord`/`Hash` — to key/sort, author implements them, handling `NaN` + canonical `±0.0` (stdlib
  ordered-float wrapper deferred).
- **Types & specs:** user `struct` (product) + `enum` (sum), generic over `[...]`. **Visibility:**
  every declaration (type/field/fn) is **private to its module** unless prefixed `pub`. Mutability is
  a separate axis — **per-instance (the binding), not per-field/type**. `Either`/`Result[T]`/`T?` are
  stdlib enums, not built-ins. Generics are bounded by a **`spec`** (nominal; empty = any type;
  `Object` = top spec, TBD); a `spec` may be used **as a type** (existential — heap-boxed, dynamic
  dispatch, the one non-monomorphized case; on a boxed value **unary** ops dispatch — spec methods,
  `copy`, `debug`, `del`/pass/store/send — but **binary same-type** ops `equal`/`==`/`Ord`/`Hash` do NOT
  (their `other: This` is erased), so existentials are never comparable/sortable/keyable; keep the
  concrete type for those). `Err` = the `Error` spec. **Spec methods = required
  (signature) + provided (default body, overridable); dispatch always to the canonical impl (no Swift
  static-dispatch — a default calls the override).** Method **receiver = `this`**, self-type = **`This`**
  (the implementing type — for same-type operands / associated-fn returns; distinct from a generic `[T]`).
  **Methods/functions carry their own type params** (`map[U]`), stacked on the receiver's `T`/`This`,
  monomorphized; provided methods may be generic. Lazy adapters return **concrete adapter types**
  (`map` → `Map[This, U]: Iterator[U]`) — chains stay monomorphized, no boxing (impl-return deferred).
- **Casts:** none implicit by default (`bool(8)`/`int(c)`; primitive casts are compiler built-in, not
  user-extensible). A user type may opt in to an auto-cast: **single-step** (never chained ⇒ no
  ambiguity), fires **only at an explicit target** (typed binding `x: X = y`, `return`, typed arg;
  not inferred `:=`) — this injects a value/`Err`/`nil` into an `Either`.
- **Memory (scope-owned + copy-by-value; move is optimization only, no `move` syntax):** no GC, no
  pointer. Copy is the semantics, compiler elides when safe — single flow (immutable may pass by-ref,
  mutable copies); across coroutines always copy (no shared mutable state); extract/return (`?`/`!`,
  `match`, `return`) copy, source never invalidated. Recursive types (`Node?`→`Node`) **auto-boxed**
  (no `box`/pointer surface). **`Ref` = the sealed copy-by-ref spec** (`drop(self)` once at last
  holder's scope exit); `chan` is its built-in implementer, `Ref[T]` the stdlib resource box for a
  handle that escapes its scope; copying refcount-bumps contained `Ref` values, deep-copies the rest.
  **`defer`** = block-scope cleanup (runs on every exit incl. abort unwind) for a scope-local effect;
  `Ref[T]` when the resource escapes.
- **By-ref escapes** (mutability is per-instance/binding, not per-type or per-field): (1) **`mut`
  parameter** — callee
  mutates the caller's `mut` var in place; confined to the call (value positions copy; only onward to
  another `mut` param; can't cross `spawn`); **two `mut` args never share storage** (guarantee): static
  `f(x,x)` = compile error, runtime index alias (`f(mut xs[i], mut xs[j])`, `i==j`) → `AliasError` abort
  (check only where they could alias). (2) **channels** — communication only.
- **Concurrency:** `spawn` (= Go's `go`) on **M:N scheduler**; returns nothing — **channels-only**
  for results/completion (no join/handle). Captures **only immutable + `Ref` values (channels, `Ref[T]`)**.
  Channel = by-ref conduit, payloads **copied** (move-optimized); `ch <- v` / `v := <-ch`. **Memory model
  = one rule:** a channel `send` happens-before the matching `receive` completes; **no shared mutable
  _Zerg_ state** (a shared `Ref[T]` is a read-only view). Shared mutable state = the **actor pattern** (a
  coroutine owning `mut` state + a channel), no locks/atomics. **Scheduler is fair** (every ready
  coroutine eventually runs; no coroutine starves others, even CPU-bound) — spec the **property, not the
  mechanism** (preemption/safepoints unspecified); a blocking `extern` call is not preemptible.
- **Null-safety (one sum type):** `Either[X, Y]` (left = value, right = propagated; **`X` ≠ `Y`**,
  ambiguous injection = error → construct the variant); `Result[T]` = `Either[T, Err]`; `T?` =
  `Either[T, nil]`; `nil` = placeholder.
  - **`?`** unwrap left else early-return right (fn shares that right type); no `T?`↔`Result` bridge —
    use `opt.ok_or(err)` / `res.ok()`.
  - **`??`** `a ?? b`: left else `b` (right discarded); short-circuits, chains, any `Either`.
  - **`?.`** optional chain, **`T?` only** (else compile error); in-place, doesn't return from fn.
  - **`!`** force-unwrap or `UnwrapError`; logical negation is `not`.
- **Logical operators = keywords** (not symbols): `not` (unary), short-circuiting `and`/`or`, over `bool`
  only → `bool` (no truthiness). Fixed constructs, never overloadable. Logical xor = `a != b` (no `xor`
  keyword — can't short-circuit). Bitwise symbols `& | ^ ~` never collide with these keywords.

## Commands

- `make` — bootstrap: install pre-commit hooks + set `.git-commit-template`. Run first in a clone.
- `make help` / `make upgrade` (`pre-commit autoupdate`) / `make clean`.

## Conventions

- **Commits:** `<type>(scope): <subject>`; types: `feat` `docs` `test` `perf` `build` `style` `refactor`.
- **Pre-commit:** markdownlint (`MD013` width 128), prettier, whitespace/EOF fixers, gitleaks.
- **`test-data` submodule:** for test corpora; guard private ones with skip-in-CI / fatal-in-dev.
