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
- [ ] **v0.1** — procedural core.
- [ ] **v0.2** — composite data.
- [ ] **v0.3** — borrow checking.
- [ ] **v0.4** — polymorphism and errors.
- [ ] **v0.5** — modules.
- [ ] **v0.6** — generics and null-safety.
- [ ] **v0.7** — concurrency runtime.
- [ ] **v0.8** — standard library.
- [ ] **v0.9** — developer tooling.
- [ ] **v0.10** — hardening and language reference.
- [ ] **v1.0** — source stability.

A phase ships when each example's stdout matches between `zerg run` and `zerg build` — byte-identical
for sequential code, equivalent under any valid scheduling for concurrent code.

## Building & running v0.0

v0.0 is the toolchain bootstrap. The compiler is a Go program that interprets `.zg` source directly,
or compiles it by emitting C and shelling out to the system C compiler.

### Prerequisites

- Go 1.22 or newer.
- A C compiler reachable as `cc` on `PATH` (override with `$CC`).
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

### REPL

```sh
./src/bootstrap/bin/zerg repl
# Zerg REPL v0.0 — accepts: nop, print "..."
# Type :exit to quit.
# zerg> print "hi"
# hi
# zerg> nop
# zerg> :exit
```

### Supported syntax at v0.0

v0.0 accepts only two statements: `nop` and `print "<string literal>"`. Identifiers, numbers,
interpolation, functions, and control flow are parse errors at this version. v0.1 expands the
language.

### Parity rule

v0.0's e2e test asserts that `zerg run` and `zerg build`-then-execute produce byte-identical stdout
for every supported example. Run it with:

```sh
make test
```

## DDD (Dream-Driven Development)

This project is based on the DDD (dream-driven development) methodology which means
the project is based on what I dream of.

All the features are based on my needs and my dreams.
