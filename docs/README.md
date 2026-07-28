# Zerg documentation

Also in [繁體中文](README.zh-TW.md).

## Start here

- **[Language Reference](language.md)** — the index. Every chapter, grouped, with a line
  on what each covers. If you do not yet know which chapter you want, start here.
- **[Conformance](conformance.md)** — how to read the specification: the status markers
  (`[implemented]`, `[not yet]`, `[deviation]`), and what a diagnostic or an abort
  promises. Read once; it changes how everything else reads.

## What is in each directory

| Directory  | Holds                                                                                 |
| ---------- | ------------------------------------------------------------------------------------- |
| `core/`    | the type system — types, values & memory, specs & generics, derive, decorators        |
| `code/`    | writing code — control flow, functions, errors, collections, coroutines, idioms       |
| `surface/` | the surface itself — the sugar table, and the formal grammar                          |
| `runtime/` | a program and the world outside it — modules, I/O, formatting, built-ins, stdlib, FFI |
| `tooling/` | what the toolchain does to your source — the formatter and linter rules               |

The authoritative grammar is not here: it is [`GRAMMAR`](../GRAMMAR) at the repo root.
[`surface/grammar.md`](surface/grammar.md) is its prose companion.

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
