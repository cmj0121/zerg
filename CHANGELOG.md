# Changelog

Zerg's releases, newest first. **This file carries the highlights only** — enough to decide whether a version is
worth your afternoon. The full account of a release, broken out by area and with its gaps named, lives beside it
under [`notes/`](notes); each entry links to its own.

The number a build reports comes from [`VERSION`](VERSION) at the repository root. That file is the single source
both compilers are generated from, and `make version-check` is what holds the three derivations of it together.

## Unreleased

**0.1.0 was written up but never tagged**, so `VERSION` still reads `0.1.0` and a build made from `main` today
reports that number while containing everything below. This section exists so that is written down rather than
discovered. 1059 commits since the entry beneath it.

- **The standing contract is true on both halves, and measured.** _A form is implemented or refused by name —
  never a crash, never a silently wrong answer, never a `cc` error._ Every gap the 0.1.0 notes listed is closed:
  a `str` literal match arm fires, an if-expression checks that its branches agree, `~` on a `byte` is a byte,
  `defer` runs on the abort path out of `main`, nothing reaches `cc`, and 800 levels of nesting raises
  `StackOverflowError` instead of a SIGSEGV. `make reject` now pins **526** ill-formed programs and `make refuse`
  **238** refusals by name.
- **`zerg test`.** Recursive discovery over a directory or a single file; a package is the module its test file
  names, so a suite sits beside what it tests and reaches its private surface; `#[test]` is found wherever it is
  written; fixtures are stood up once and torn down in reverse; `--only` filters before anything is built;
  a test that does not finish is `STUCK`, which is its own verdict and not a failure. Four exit codes, and a run
  that searched and found nothing is no longer a run that passed.
- **`assert` is a reserved word**, self-hosting only. It reports the file, the line, the source text of the claim
  and the values the operands held — three things a function cannot carry, because Zerg has no `__FILE__` and no
  caller location. Operands bind to temporaries before the test, so a condition with a side effect happens once.
  `AssertionError` is the eleventh error kind, and it earns the ABI slot immediately: the runner tells a claim
  that did not hold from a program that broke.
- **`log`, and the standard library's first `unsafe` group.** A zerolog-shaped builder where a line nobody
  prints costs nothing to not print, with a floor under that claim. Its global is the tree's reference for
  process-wide mutable state, and the comment above the cell is the document — what it is an exception to, what
  it costs, when not to copy it, and what would take the `unsafe` away.
- **The standard library is 15 modules.** `json` moved out of the language server rather than being copied;
  `time` learned to render; `os` gained `isatty`, `set_env` and `del_env`, and with them the naming rule
  `xxx` reads, `set_xxx` writes, `del_xxx` removes — for a property, which is something with a getter named for
  the thing.
- **Every diagnostic code is four digits, and declared once.** `E204` is `E2004`: a range is a thousand numbers
  and a stage owns exactly one, which retires the continuation ranges (`E6xx`, `E7xx`) that had stopped the first
  digit from naming the stage. The 104 `[not yet]` refusals move to `E9xxx` — a form the language has and this
  compiler has not built is a different kind of finding, and it retires when the form is built. The codes
  themselves live in `src/compiler/zerg/rule.zg` as one enum, so a hand-spelled code is a type error and two
  parallel changes collide in git rather than in CI. The three-digit numbering is retired whole; nothing shipped
  under it.
- **`zerg build --emit check`** — the front end without code generation, 2.30 s / 0.14 GB where `--emit c` costs
  3.16 s / 2.40 GB. A `check-equal` gate pins the two stages to byte-identical diagnostics, because a check that
  reports less than the build would show a clean buffer for a file that will not build.
- **The tools got honest.** `zerg fmt` had been rewriting a valid file into one that does not parse and then
  calling it canonical; both ends are closed under one invariant — _if the input parses, the output must_ — and a
  round-trip gate holds it. The linter's usage walk was missing three of the places a name can appear. The
  language server had been treating the opened file as the program's entry point, so it underlined correct code
  in every multi-file module.
- **The driver is a command line again**: `src/compiler/zergc.zg` went from 3359 lines to 95, with the
  sub-commands and their shared machinery in `src/compiler/cmd/`. The emitted C is byte-identical, which is what
  makes it a move rather than a rewrite.
- **The gate board is 39**, from 33.

### Known issues

Filed with the measurement that found each one, so nobody has to rediscover it.

- [#10](https://github.com/cmj0121/zerg/issues/10) — the emitter assembles its output quadratically, so 3.6 MB
  of C costs 2.4 GB to produce. Everything the compiler decides costs 0.22 GB; the rest is string accumulation.
- [#11](https://github.com/cmj0121/zerg/issues/11) — a value that is not a binding is never released: a nested
  call's temporary, and the old buffer of a field that is overwritten. Both leak without bound in a loop.
- [#12](https://github.com/cmj0121/zerg/issues/12) — a bare `{ }` block hides what is written inside it from
  the statement walks, so a type parameter used there is refused with a reason that is not true.
- [#13](https://github.com/cmj0121/zerg/issues/13) — a file in a module directory shadows the module its name
  spells, for every file beside it. The loud case is the lucky one.
- [#14](https://github.com/cmj0121/zerg/issues/14) — a `const` initialised through a call reads a later `const`
  as zero, silently. A direct reference is handled correctly, so this is a hole in an existing fix.
- [#26](https://github.com/cmj0121/zerg/issues/26) — a refusal carries no place, so an editor can only
  underline the top of the file.

Two bodies of work are open as stories rather than defects: the language server
([#15](https://github.com/cmj0121/zerg/issues/15)) cannot yet answer where a name is declared, and `zerg doc`
([#16](https://github.com/cmj0121/zerg/issues/16)) does not exist — though 43 of the examples it would render are
already compiled and diffed against their stated output.

Refused by name, and therefore not defects: generic `struct` and `enum` (`E215`, which is why `atomic` ships
unusable), a structural `Display` for composites (`E449`/`E417`/`E445`, three faces of one gap), named arguments
(`E223`), destructuring (`E238`), `set[T]` (`E466`) and `[T; N]` (`E233`).

## 0.1.0 — 2026-08-05

The first release: 1308 commits, 2026-04-22 to 2026-08-05. It is the release in which Zerg stopped being a Go
program that reads Zerg and became a Zerg program that reads Zerg.

- **The compiler is written in Zerg and compiles itself.** `make` builds a Go seed, the seed builds an
  intermediate, and the intermediate builds the compiler that ships — so the seed is off the delivery path. The C
  emitted is byte-identical across generations, which is what makes "self-hosting" a claim rather than a
  description.
- **The language a program is written in.** Structs, enums with payloads and observable discriminants, `match`
  with exhaustiveness checking, `list[T]` and `map[K, V]`, optionals with `?` / `??` / `?.` / `!`, `guard` /
  `raise` with the cause recorded, `defer`, `del`, ranges, f-strings, `spec` / `impl`, modules with `pub` and
  `init()`, and generic functions whose type arguments are solved from the call.
- **Concurrency is a chapter, not a library.** `spawn`, `chan[T]`, directional ends, `close`, `select` and
  `for select`, over an M:N scheduler of stackful coroutines. A channel is the iterator, which is why the
  language needs no `yield` and no generator type.
- **Integer arithmetic is checked.** `+`, `-`, `*` and unary `-` raise on overflow, `/` and `%` are Euclidean
  and raise on a zero divisor, and the `%`-suffixed `+%`, `-%`, `*%` wrap — a pairing that had meant nothing
  while both spellings emitted the same C.
- **Memory is scope-owned with no tracing GC.** Values are freed at scope exit on every path including an
  abort unwind caught by a `guard`; lists are copy-on-write behind an atomic refcount; two names of a value
  type never alias. Every exit that UNWINDS runs its pending `defer`s, including the uncaught abort that
  leaves `main` — which for a long time was the one that did not. `os.exit` and a coroutine abandoned at
  program end do not unwind, and say so where they are documented.
- **A form is implemented or refused by name** — the standing contract, and this release is the first to
  MEASURE it rather than assert it. `make refuse` pins 141 refusals and `make reject` pins 159 ill-formed
  programs the compiler turns away itself. The measurement also found where the contract is broken today, and
  the specification now marks each one: a `str` literal `match` arm never fires, an if-expression does not
  check that its branches agree, `~` on a `byte` yields the unmasked 64-bit complement, a `defer` does not run
  on the abort path out of `main`, `in` and `??` used as a whole condition reach `cc`, and 800 levels of
  nesting is a SIGSEGV. They are listed in full in the release notes.
- **A broken RULE says where; a refused FORM does not.** A rule the compiler checks reports `file:line:col`, the
  source line, a caret, and every other finding in the same run rather than only the first — `undefined name`
  among them. A form it has not built says the form's name and nothing else, with no place at all: 170 of 235
  reporting sites, and the widest-reaching deviation the specification records.
- **A pure-Zerg standard library over a self runtime**: `io`, `fs`, `os`, `strings`, `ascii`, `cli`, `strconv`,
  `time`, `math`, `rand`, `atomic`, `testing`. No third-party library is linked into anything.
- **One binary, three jobs.** `zerg build`, `zerg fmt`, `zerg lint` — with `--emit` for the stage you want,
  content-keyed caching in `.zerg-cache/`, and `-j` for parallel units.
- **A normative specification**, paired English / `zh-TW`, with `GRAMMAR` as the syntax half — and a status
  marker on every feature the specification has and this compiler does not.

**Known limitations and deviations are listed in full in the release notes**, and each is marked where it appears
in the specification. The headline ones: a generic `struct` / `enum` / method is refused, closures may not
capture, module visibility is not enforced, and the scheduler is cooperative rather than preemptive.

Full notes, by area — [`notes/0.1/0.1.0_CHANGELOG.md`](notes/0.1/0.1.0_CHANGELOG.md).
