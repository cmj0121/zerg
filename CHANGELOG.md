# Changelog

Zerg's releases, newest first. **Highlights only** — enough to decide whether a version is worth your afternoon.
The full account of a release, broken out by area and with its gaps named, lives beside it under
[`notes/`](notes).

The number a build reports comes from [`VERSION`](VERSION), the single source both compilers are generated from.
**A release's date is its tag's**, so no entry here writes one down.

## 0.2.0

The release in which the specification stops disagreeing with the compiler, and the **file** becomes the unit
a name belongs to. → [full notes](notes/0.2/0.2.0_CHANGELOG.md)

- **No `[deviation]` markers.** 0.1.0 shipped a specification that named the places it was wrong; this one
  has none. Every marker it carried was closed three ways only — the compiler was fixed, the spec moved, or
  the requirement became a door in `FUTURE.md` — and then the tree was searched for the ones nobody had
  written down.
- **The file is the unit.** Visibility and imports are the file's, a folder is a module by holding `mod.zg`,
  and a project's own modules are spelled `./`. A module is what an import RESOLVED to, not how it was
  spelled, so one directory reached two ways is one module.
- **A public name is not program-global.** Two modules may each declare `pub fn helper`. The module tag that
  separated private names was simply not given to public ones.
- **Every refusal carries a code and a place**, and a code's range names the stage that reports it. Sixty-odd
  numbers moved to the range that answers their question; the ones that stayed in `E9xxx` are the forms this
  compiler has not built.
- **A marker names its rule.** Every `[not yet]` in the specification quotes a code the compiler still
  raises, or names the ticket for the rule that does not exist yet.
- **Five more gates**, and two that had been passing without checking anything now prove they can see before
  they report what they saw.
- **Tooling.** An ignore file and the first pattern matcher this toolchain has; `fmt`, `lint` and `desugar`
  take files; `zerg test` no longer follows a symlink into itself.

**Still no compatibility promise.** The surface moves, and a `[not yet]` becoming built is the ordinary way
it will move. Eighteen specified features remain refused by name — pattern matching beyond the binding and
literal forms, the f-string's format spec, command literals, `asm`, and the rest are listed in the notes.

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
