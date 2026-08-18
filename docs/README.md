# Zerg documentation

Also in [繁體中文](README.zh-TW.md).

This is everything about the **language**. Where the project stands is the
[project README](../README.md); how the toolchain that implements the language is put together
is [below](#how-the-toolchain-is-built).

## Start here

- **[Language Reference](language.md)** — the index. Every chapter, grouped, with a line
  on what each covers. If you do not yet know which chapter you want, start here.
- **[Conformance](conformance.md)** — how to read the specification: the status markers
  (`[not yet]`, `[implementation-defined]`, `[deviation]`), and what a diagnostic or an abort
  promises. Read once; it changes how everything else reads.

## What is in each directory

| Directory  | Holds                                                                                         |
| ---------- | --------------------------------------------------------------------------------------------- |
| `core/`    | the type system — types, values & memory, specs & generics, derive, decorators                |
| `code/`    | writing code — control flow, functions, errors, collections, coroutines, idioms               |
| `surface/` | the surface itself — the sugar table, and the formal grammar                                  |
| `runtime/` | a program and the world outside it — modules, I/O, formatting, built-ins, stdlib, FFI         |
| `tooling/` | what the toolchain does with your source — fmt/lint, the codes it reports, desugar, an editor |

The authoritative grammar is not here: it is [`GRAMMAR`](../GRAMMAR) at the repo root.
[`surface/grammar.md`](surface/grammar.md) is its prose companion.

## How the toolchain is built

`make` builds **three** compilers and keeps the last one. The two that are thrown away are what
makes the third a self-hosted compiler rather than a claim about one.

```text
   src/bootstrap/*.go  ──go build──►  bin/zerg0           the SEED, written in Go
                                          │
                          src/compiler/*.zg│build
                                          ▼
                                   bin/.zerg-stage1       an INTERMEDIATE, written in Zerg
                                          │
                          src/compiler/*.zg│build
                                          ▼
                                     bin/zerg             the compiler that SHIPS
```

| Step | What runs                                              | What it leaves                      |
| ---- | ------------------------------------------------------ | ----------------------------------- |
| 0    | `./scripts/gen-version.sh`                             | `VERSION` becomes `zerg/version.zg` |
| 1    | `go build -o bin/zerg0 ./cmd/zerg` in `src/bootstrap/` | `bin/zerg0` — the Go seed           |
| 2    | `zerg0 build src/compiler/zergc.zg`                    | `bin/.zerg-stage1` — intermediate   |
| 3    | `.zerg-stage1 build --emit bin src/compiler/zergc.zg`  | `bin/zerg` — deleted stage 1 after  |

**The seed is not on the delivery path.** Step 3 is what produces the binary a user runs, and step
3 is run by a compiler written in Zerg — so the seed only has to be good enough to build something
that builds the real thing, and every `make` exercises the self-host path instead of leaving it to a
separate command nobody runs. A compiler that could not reproduce itself would not survive step 3.

**Each of the three answers a different question.**

| Compiler       | In   | Its job                                                      | Its contract                |
| -------------- | ---- | ------------------------------------------------------------ | --------------------------- |
| `zerg0`        | Go   | build the self-hosting compiler, nothing else                | the seed's own README       |
| `.zerg-stage1` | Zerg | build `zerg` — it exists for one command and is then removed | none; it is never installed |
| `zerg`         | Zerg | everything: `build`, `test`, `fmt`, `lint`, `desugar`, `lsp` | this specification          |

The seed understands only **`Zerg-boot`** — the slice of the language `src/compiler/` is written in.
Anything outside it is refused **by name** rather than miscompiled, and a reader writing Zerg never
meets that subset: every marker in these chapters is about `zerg`, the one `make` leaves in `bin/`.

**Inside `zerg`, one source file travels this far**, which is the same pipeline the seed runs:

```text
hello.zg → lex → parse → check → emit C (C17) → cc → ./hello
```

A program is compiled **module by module** — one file, or the several files of a directory module —
and each becomes an object that one link puts together. That is what `-j` parallelizes and what
`.zerg-cache/` keys by content, so changing one module recompiles one module.

**What holds the chain honest**, each a target in `make help`:

| Gate            | Asks                                                                  |
| --------------- | --------------------------------------------------------------------- |
| `make build`    | the compiler can still reproduce itself — steps 2 and 3 are the proof |
| `make fixpoint` | it emits the **same C** for its own source, run against run           |
| `make oracle`   | the seed and `zerg` agree about a valid program                       |
| `make corpus`   | every case in the corpus prints what it must                          |
| `make refuse`   | a form `zerg` has not built is named by the compiler, not by `cc`     |
| `make test`     | the whole board, in order                                             |

The **runtime** (`src/runtime/csrc`, C) and the **standard library** (`src/stdlib`, pure Zerg over
that runtime) are built into whatever the compiler compiles, not into the compiler — see
[modules](runtime/package.md) and [the standard library](runtime/stdlib.md). The deep versions of
this section are [`src/bootstrap/README.md`](../src/bootstrap/README.md) for the seed and
[`src/compiler/README.md`](../src/compiler/README.md) for the compiler that ships.

## Finding things

Every chapter is a pair — `X.md` and `X.zh-TW.md` — kept side by side in the same
directory, because the two are written and edited together.

```sh
rg "copy-by-value" docs/            # both languages
rg "copy-by-value" --glob '!*.zh-TW.md' docs/   # English only
rg "spec" docs/core/                # one topic
```

## Adding a chapter

Both languages, same directory, and a row in [`language.md`](language.md)'s chapter
table under the group it belongs to.

Every path this repo cites — including the plain-text `docs/…md` mentions inside source
comments, which no markdown tool can see — is checked by `make docs-links`. It runs in
CI. If you move or rename a page, that is what tells you what you missed.
