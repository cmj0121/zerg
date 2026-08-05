# Changelog

Zerg's releases, newest first. **This file carries the highlights only** — enough to decide whether a version is
worth your afternoon. The full account of a release, broken out by area and with its gaps named, lives beside it
under [`notes/`](notes); each entry links to its own.

The number a build reports comes from [`VERSION`](VERSION) at the repository root. That file is the single source
both compilers are generated from, and `make version-check` is what holds the three derivations of it together.

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
- **Memory is scope-owned with no tracing GC.** Values are freed at scope exit on every path including an abort
  unwind; lists are copy-on-write behind an atomic refcount; two names of a value type never alias.
- **A form is implemented or refused by name.** It is never a crash, never a silently wrong answer, and never an
  error reported by `cc` against generated code nobody wrote. `make refuse` pins 141 such refusals and
  `make reject` pins 159 ill-formed programs the compiler turns away itself.
- **A broken RULE says where; a refused FORM does not.** A rule the compiler checks reports `file:line:col`, the
  source line, a caret, and every other finding in the same run rather than only the first. A form it has not
  built says the form's name and nothing else — no file, no line. That is 173 of 235 reporting sites, and it is
  the half you meet first.
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
