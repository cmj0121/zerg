# Zerg

> Write the code as you think — one way, and only one way, to do it.

Zerg programs are fast to write, easy to read, and overwhelmingly straightforward — swarm your
problems with simplicity.

## Design Principles

| Principle        | Description                                         |
| ---------------- | --------------------------------------------------- |
| small and crisp  | minimal syntax                                      |
| safe by default  | immutable and private unless explicitly `mut`/`pub` |
| null-safe        | no billion-dollar mistakes                          |
| concurrent       | built-in support for concurrency                    |
| procedural-first | straightforward, top-down control flow              |
| scope-owned      | no GC — memory freed at scope exit                  |
| strongly typed   | catch errors at compile time                        |
| copy-by-value    | values are copied by default; compiler may optimize |

## Roadmap

Zerg has a long-term vision of being a general-purpose systems programming language, but the initial
focus is on building a workable prototype with a minimal feature set:

### Pre-1.0

- [x] **v0.0** — toolchain bootstrap.
- [x] **v0.1** — procedural core.
- [x] **v0.2** — composite data.
- [x] **v0.3** — borrow checking.
- [x] **v0.4** — polymorphism and errors.
- [x] **v0.5** — modules.
- [x] **v0.6** — generics and null-safety.
- [x] **v0.7** — concurrency runtime.
- [x] **v0.8** — standard library.
- [x] **v0.9** — process surface and time.
- [x] **v0.10** — hardening and language reference.
- [x] **v0.11** — bare-binding form (retire `let` from grammar).
- [ ] **v1.0** — source stability.

A phase ships when each example's stdout matches between `zerg run` and `zerg build` — byte-identical
for sequential code, equivalent under any valid scheduling for concurrent code.

## Building & running

The compiler is a Go program that interprets `.zg` source directly, or compiles it by emitting C
and shelling out to the system C compiler. The toolchain ships four subcommands: `zerg run`,
`zerg build`, `zerg fmt`, and `zerg repl`.

### Prerequisites

- Go 1.23 or newer.
- A C compiler reachable as `cc` on `PATH` (override with `$CC`). It must accept `-fwrapv` and
  `-pthread`; gcc and clang on macOS / Linux qualify.
- macOS or Linux. Windows is deferred.

### Build the toolchain

```sh
make build
```

This produces `src/bootstrap/bin/zerg`.

### Run a source file

```sh
./src/bootstrap/bin/zerg run examples/01_hello.zg
# Hello, Zerg!
```

### Compile to a native binary

`zerg build` writes the binary into the current working directory, named after the source basename:

```sh
./src/bootstrap/bin/zerg build examples/01_hello.zg && ./01_hello
# Hello, Zerg!
```

`zerg build --emit-c <file>` prints the generated C to stdout instead of compiling.

### REPL

`zerg repl` is a multi-line interactive prompt; input accumulates until it parses cleanly, so
you can paste a function body, a `for` block, a `struct`/`enum`/`spec`/`impl` declaration, or a
`match` arm at a time. Bindings persist across lines within a session. `import` is not admitted
at the REPL.

## DDD (Dream-Driven Development)

This project is based on the DDD (dream-driven development) methodology which means
the project is based on what I dream of.

All the features are based on my needs and my dreams.

## Further reading

- [`RELEASE.md`](RELEASE.md) — per-version release summaries (one screen per version).
- [`docs/LANGUAGE.md`](docs/LANGUAGE.md) — formal language reference (grammar EBNF, type system,
  evaluation order, ownership, defer / concurrency / `?` propagation, reserved words).
- [`docs/STDLIB.md`](docs/STDLIB.md) — per-module fn reference for `std/io`, `std/strings`,
  `std/math`, `std/os`, and `std/time`, with signatures, error variants, and runnable examples.
