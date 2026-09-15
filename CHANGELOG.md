# Changelog

Zerg's releases, newest first. **Highlights only** — enough to decide whether a version is worth your afternoon.
The full account of a release, broken out by area and with its gaps named, lives beside it under
[`notes/`](notes).

The number a build reports comes from [`VERSION`](VERSION), the single source both compilers are generated from.
**A release's date is its tag's**, so no entry here writes one down.

## 0.4.0

The release in which the type system grows into something you can write a **library** with. →
[full notes](notes/0.4/0.4.0_CHANGELOG.md)

> **0.3.0 let a user write generic DATA. 0.4.0 lets them write an ABSTRACTION.**

- **A `spec` is a value's type.** A spec-typed position builds a counted box carrying the value and a witness
  table; a call dispatches through it, `is` works on it, and the table carries a rendering so `print` says
  what the value says. A parameterized spec is a type too. `E9115`, `E9116` and `E9048` retire.
- **An `enum` can be generic** — `E9003` retires — with a variant's parameters solved from the payload,
  supplied at the application, or refused by name when neither reaches them. **A method carries its own type
  parameters**, and `E9044` retires with it.
- **`log`'s `Sink` is the acceptance test**, and that module is written the way it wanted to be: a parameter
  can say _any destination_ rather than naming one.
- **Two doctrines were being restated, not provided.** Four ledgers and six run-and-compare loops each had
  their own copy; each is one implementation now. Collecting them found two `gates-check` clauses that could
  report and not fail, two sites that ran a program with `2>/dev/null`, a gate that never asked the exit
  status, and a skip list that checked an exit status where it meant a rule.
- **Three dead-code questions had no gate.** `make dead-code` asks the two that are about the repository —
  a `pub` function of the compiler nothing calls, and a script nothing invokes — and the third turned out to
  be a defect in `L101`: an import belongs to the FILE that wrote it, and the rule was asking the merged
  program.
- **60 gates**, up from 58.

**Two leftovers ship unfixed**, which is the milestone's rule for a find from the previous release:
[#123](https://github.com/cmj0121/zerg/issues/123) (an `unsafe fn` as a `spec` requirement) and
[#127](https://github.com/cmj0121/zerg/issues/127) (`fmt` ordering a run of bindings).

## 0.3.0

The release in which the **grammar stops being a promise**: the forms `GRAMMAR` derives are lowered, not
refused. → [full notes](notes/0.3/0.3.0_CHANGELOG.md)

- **Seventeen forms built.** Generic `struct`, `impl` and `fn`; `Ref[T]` and `deref`; associated functions
  and associated values; destructuring both ways; struct, list, tuple and or-patterns; `pattern as name`;
  the array type `[T; N]`; a `spec` member with a body; a `mut &` in a function type; the command literal;
  an f-string's `{x!r}` / `{x=}` / `{x:spec}` tails; `asm`; `ptr`; a standalone `unsafe fn`.
- **A generic is a name.** A declaration is a template and an application is an ordinary type with that
  name, so nothing in the type tree grew a case — measured before it was chosen.
- **What is still not built is named row by row**, each with the code that refuses it, and `chapter-codes`
  fails if a row outlives its form. Seventeen rows had.
- **The gates were audited against themselves.** A skip list that only checked one direction, an exclusion
  that outlived its reason, four gates that compared what a program prints and not what it dies with, and a
  file list that read 347 of 769 sources under a line claiming all of them.
- **The corpus runs under the sanitizers.** 179 cases that had never been measured; sixteen leaked, six are
  closed, and the ten that remain are listed by name with what allocates.
- **58 gates**, up from 52.

## 0.2.0

The release in which the specification stops disagreeing with the compiler, and the **file** becomes the unit
a name belongs to. → [full notes](notes/0.2/0.2.0_CHANGELOG.md)

- **No `[deviation]` markers.** 0.1.0 shipped a specification that named the places it was wrong; this one
  has none. Every marker it carried was closed three ways only — the compiler was fixed, the spec moved, or
  the requirement became a door with a threshold — and then the tree was searched for the ones nobody had
  written down. (The doors were disposed of in 0.3.0: each became a position in the chapter that owns the
  question, or an `[implementation-defined]` where a reader meets it.)
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
