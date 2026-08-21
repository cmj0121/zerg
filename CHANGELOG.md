# Changelog

Zerg's releases, newest first. **Highlights only** — enough to decide whether a version is worth your afternoon.
The full account of a release, broken out by area and with its gaps named, lives beside it under
[`notes/`](notes).

The number a build reports comes from [`VERSION`](VERSION), the single source both compilers are generated from.
**A release's date is its tag's**, so no entry here writes one down.

## 0.1.0

The first release: the one in which Zerg stopped being a Go program that reads Zerg and became a Zerg program
that reads Zerg. → [full notes](notes/0.1/0.1.0_CHANGELOG.md)

- **Self-hosting.** A Go seed builds an intermediate, the intermediate builds the compiler that ships, and the C
  is byte-identical across generations.
- **The language.** Structs, enums with payloads, exhaustive `match`, `list` and `map`, optionals, `guard` /
  `raise`, `defer`, `spec` / `impl`, modules, and generic functions.
- **Concurrency is a chapter, not a library.** `spawn`, channels, `select`, over an M:N scheduler of stackful
  coroutines. A channel is the iterator, so the language needs no `yield`.
- **Checked integer arithmetic.** Overflow raises; the `%`-suffixed operators wrap.
- **Scope-owned memory, no tracing GC.** Copy-on-write containers, no aliasing between value names, and every
  unwinding exit runs its `defer`s.
- **A form is implemented or refused by name** — the standing contract, and the first release to measure it
  rather than assert it. A refusal carries a code and a place.
- **A pure-Zerg standard library** over a self runtime. Nothing third-party is linked into anything.
- **Seven commands**: `build` `test` `fmt` `lint` `desugar` `doc` `lsp`.
- **A normative specification**, paired English / `zh-TW`, with `GRAMMAR` as the syntax half and a status marker
  on every gap — each one re-measured against this release.
- **A gate board that is the argument.** Two compilers may not accept a program and disagree about it, the
  compiler is a fixpoint of itself, and the formatter's output parses if its input did.

**No compatibility promise.** 0.1.0 is the first number, not a stability claim: the surface will move, and a
`[not yet]` becoming built is the ordinary way it will move.
