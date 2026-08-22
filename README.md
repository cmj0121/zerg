# Zerg

English | [繁體中文](README.zh-TW.md)

[![CI](https://github.com/cmj0121/zerg/actions/workflows/ci.yml/badge.svg)](https://github.com/cmj0121/zerg/actions/workflows/ci.yml)
[![version](https://img.shields.io/badge/version-0.1.0-blue)](VERSION)

> Write the code as you think — one way, and only one way, to do it.

Zerg is a **compiled, general-purpose language**. The compiler translates your source to **C**
(**C17**, or **C99** / **C11** when `ZERG_CSTD` asks), then hands it to a C compiler (`cc`) for the
native binary.

```text
hello.zg → lexer → parser → type check → C codegen → C17 → cc → ./hello
```

> **Phase-1 MVP.** The specified language is deliberately larger than what the compiler builds today;
> [Status](#status) says where it falls short.

## Quickstart

`brew tap cmj0121/zerg https://github.com/cmj0121/zerg && brew install zerg` installs a
toolchain, and the [release page](https://github.com/cmj0121/zerg/releases) has a tarball for
Linux and Apple Silicon. To build it yourself — which is what the rest of this page is about —
you need Go ≥ 1.26 and a C compiler.

```sh
make                       # ./bin/zerg0, the Go seed → ./bin/zerg, the compiler you use
cat > hello.zg <<'ZG'
fn main() {
    print "hello, world"
}
ZG
./bin/zerg build hello.zg  # emit C, then invoke cc → ./hello
./hello                    # hello, world
```

`make` builds two compilers, and the second is the one you use. `zerg0` is the Go-hosted seed, cut
down to a single job: building the compiler. `zerg` is that compiler — written in Zerg, in
[`src/compiler/`](src/compiler), and compiled by itself.

| Command               | What it does                                                        |
| --------------------- | ------------------------------------------------------------------- |
| `zerg build <file>`   | compile — an executable when the entry declares `main`, else object |
| `zerg test [path]`    | run the `#[test]` functions under a path, or in one file's package  |
| `zerg fmt <path>`     | rewrite source in the one canonical style                           |
| `zerg lint <path>`    | report unused imports and dead private declarations                 |
| `zerg desugar <path>` | rewrite source into the core forms its sugar stands for             |
| `zerg doc [name]`     | read what a module exposes and the comments that document it        |
| `zerg lsp`            | the language server, over stdio (JSON-RPC)                          |

`--emit` stops at a stage instead: `tokens`, `ast`, `check` (the diagnostics alone), `c`, `lib` (an
object), `bin` (an executable). A program builds module by module — `-j` compiles several units at
once, and results are cached by content in `.zerg-cache/`, so a rebuild that changes one module
recompiles one module.

**`-o` names the file written**, at every stage — `--emit c f.zg -o f.c` writes `f.c`, and
`--emit lib f.zg -o out.o` writes exactly `out.o`. It is worth stating because it used to mean
something different at each: `--emit c` discarded the flag and wrote stdout regardless, so a build
that asked for a file got none and exited 0. What differs per stage is only the DEFAULT, when no
`-o` is given:

| Stage                | With no `-o`                                                |
| -------------------- | ----------------------------------------------------------- |
| `tokens`, `ast`, `c` | stdout, so the stage stays pipeable — `--emit c f.zg > f.c` |
| `--emit check`       | nothing — it produces diagnostics and no artifact           |
| `--emit lib`         | the source name with `.o` — `f.zg` gives `f.o`              |
| `--emit bin`         | the source name — `f.zg` gives `f`                          |

## The language

| Principle        | What it means                                                                           |
| ---------------- | --------------------------------------------------------------------------------------- |
| small and crisp  | minimal syntax — a small core, with sugar that desugars back to it                      |
| safe by default  | immutable and private unless explicitly `mut` / `pub`                                   |
| null-safe        | optionals instead of null; no billion-dollar mistake                                    |
| strongly typed   | errors caught at compile time; no implicit conversion — a value converts by `T(x)`      |
| copy-by-value    | value types are copied on assignment; a reference-counted value is shared               |
| scope-owned      | no tracing GC — values are freed at scope exit; recursive types and strings are counted |
| procedural-first | straightforward, top-down control flow — no loop labels, no hidden dispatch             |
| concurrent       | built-in coroutines and channels, over an **M:N** scheduler                             |
| zero-dependency  | like Go — a compiled program links no third-party library                               |

```zerg
break if done                   # → if done { break }
print f"{count} items"          # interpolation → str concatenation

#[derive(Eq)]                   # the compiler writes the impl from the structure
struct Point { x: int; y: int }
```

Zero-dependency is two layers. The **runtime** — the small C floor reaching the OS through the
platform C library and nothing else — is fixed by both the specification and its implementation. The
**standard library** (`src/stdlib/*.zg`) is **pure Zerg** over that floor, bound only by its interface:
`io.read_file` loops the runtime's syscall leaves, and `math.sqrt` is a Zerg algorithm, never a libm
binding. The packages that import cleanly today — `io`, `fs`, `os`, `strings`, `ascii`, `cli`,
`strconv`, `json`, `log`, `sha256`, `time`, `math`, `rand`, `testing` — are reached with `import "<name>"`.

## Documentation

**[Getting started](docs/getting-started.md)** takes `hello, world` to a program in more than one
file — the toolchain, the one canonical style, a module, a test beside it — and **[Zerg by
example](examples/README.md)** is thirty-three programs in a reading order, every one of them built
and run by a gate.

**[`docs/README.md`](docs/README.md)** is the entry point: which chapter to open first, what each
directory holds, and how the specification is meant to be read. Syntax is normative in
[`GRAMMAR`](GRAMMAR); semantics are normative in the chapters under [`docs/`](docs).

**[`FUTURE.md`](FUTURE.md)** is the other half: what the language decided **not** to have, and the
threshold that would reopen each case. Nothing in it is part of the specification.

## Status

The compiler that ships is **`zerg`**, written in Zerg and compiled by itself; **`zerg0`** is a
Go-hosted seed whose only job is building it. Every status claim here — and every marker in the
specification — is about `zerg`, the one a `make` build puts in `bin/`. The seed's narrower subset is
its own contract, in [`src/bootstrap/README.md`](src/bootstrap/README.md), and a reader writing Zerg
never meets it.

**The contract.** A form is either lowered correctly or refused **by name** at compile time. It is
never a crash, never a silently wrong answer, and never an error reported by the C compiler or the
linker against generated code nobody wrote. A feature the specification marks **[not yet]** raises
`NotImplemented` and stops.

**Where the compiler falls short of that, the specification says so** — a **[deviation]** in the
chapter the feature belongs to. The ones worth knowing before writing anything are the **silent**
ones, where a program gets an answer and no diagnostic:

| Silent deviation                                                     | Chapter                                 |
| -------------------------------------------------------------------- | --------------------------------------- |
| `for x in p.xs { p.xs.append(v) }` compiles, and grows what it walks | [collections](docs/code/collections.md) |
| an `init()` in a module the run never touches still runs             | [modules](docs/runtime/package.md)      |

Two more are structural, and a running program feels them: the scheduler is **cooperative, not
preemptive**, so a CPU-bound coroutine occupies a worker until it parks
([coroutines](docs/code/coroutine.md)); and module visibility is enforced on functions and module
constants, not yet on types or fields ([modules](docs/runtime/package.md)).

Everything else — what is built, what is refused by name, and every remaining deviation — is marked
where it belongs in the specification. The gates that keep those markers honest are the targets
`make help gates` lists; **`make test`** runs the whole board, and `make gates` is what stops a gate
from being on it in name only.

## License

Zerg is licensed in **layers**, on one question: does this code end up inside the binary you ship?

| Part                                | License          | What it means                                            |
| ----------------------------------- | ---------------- | -------------------------------------------------------- |
| runtime, standard library, examples | MIT              | linked into your program — ship it however you like      |
| compiler (self-hosted and seed)     | GPL-3.0-or-later | changing and redistributing the toolchain is share-alike |
| specification and `GRAMMAR`         | CC-BY-SA-4.0     | quote, translate, reimplement — with attribution         |

**A program you write in Zerg is yours.** The compiler's license does not reach its output, and the
runtime that IS linked into your binary is MIT. See **[LICENSE](LICENSE)** for the whole arrangement,
including what it does not grant: the name.

## DDD (Dream-Driven Development)

Features are driven by what the author dreams of and needs — nothing more.
