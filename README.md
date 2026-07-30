# Zerg

English | [繁體中文](README.zh-TW.md)

> Write the code as you think — one way, and only one way, to do it.

Zerg is a **compiled, general-purpose language**. The compiler translates your Zerg source to **C**
(**C17** by default, **C99** as a fallback), then hands it off to a C compiler (`cc`) to build the native
binary. Programs are fast to write, easy to read, and overwhelmingly straightforward.

> **Project status — Phase-1 MVP.** Zerg is at an early bootstrap stage. The _language_ (as defined in
> the specification) is deliberately larger than what the _bootstrap compiler_ implements today, so every
> feature in the spec carries a status marker — **implemented**, **not yet**, or **implementation-defined**.
> See **[Status & Limitations](#status--limitations)** for the headline gaps, and the
> **[Language Specification](docs/language.md)** for the per-feature detail.

## Design Principles

| Principle        | Description                                                                                           |
| ---------------- | ----------------------------------------------------------------------------------------------------- |
| small and crisp  | minimal syntax                                                                                        |
| safe by default  | immutable and private unless explicitly `mut` / `pub`                                                 |
| null-safe        | optionals instead of null; no billion-dollar mistake                                                  |
| concurrent       | built-in coroutines and channels (a cooperative, non-preemptive **M:N** scheduler in this phase)      |
| procedural-first | straightforward, top-down control flow                                                                |
| scope-owned      | no tracing GC — values are freed at scope exit; recursive types and strings are                       |
|                  | reference-counted                                                                                     |
| strongly typed   | catch errors at compile time                                                                          |
| explicit casts   | no implicit conversion by default; a value converts by re-construction (`T(x)`)                       |
| copy-by-value    | value types are copied on assignment; a reference-counted value is shared                             |
| zero-dependency  | like Go — no third-party library. The **runtime** (fixed by spec + its C impl) is the only floor      |
|                  | reaching the OS; the **stdlib** is pure Zerg over it, an implementation detail bound by its interface |

Full semantics — primitive & user types, conversions, the memory model, concurrency, and null-safety —
are in the **[Language Specification](docs/language.md)**, with companion chapters for
**[Modules, Packages & Programs](docs/runtime/package.md)**, **[Coroutines & Channels](docs/code/coroutine.md)**,
**[Grammar](docs/surface/grammar.md)**, **[Syntax Sugar](docs/surface/syntax-sugar.md)**,
**[Collections](docs/code/collections.md)**, **[Derive & Default Behavior](docs/core/derive.md)**,
**[Process & I/O](docs/runtime/io.md)**, and the **[FFI](docs/runtime/ffi.md)**.

## Quickstart

Build the bootstrap toolchain (Go ≥ 1.26 and a C compiler are required), then compile a program:

```sh
make                                 # ./bin/zerg0 (the Go seed), then ./bin/zerg
cat > hello.zg <<'ZG'
fn main() {
    print "hello, world"
}
ZG
./bin/zerg build --emit bin hello.zg # emit C, then invoke cc → ./hello
./hello                              # hello, world
```

`make` builds two compilers, and the second is the one you use. `zerg0` is the Go-hosted
seed, cut down to a single job: building the compiler. `zerg` is that compiler — written
in Zerg, in [`src/compiler/`](src/compiler), and compiled by itself (the seed only builds
an intermediate, which builds the one that ships).

| Command                     | What it does                                              |
| --------------------------- | --------------------------------------------------------- |
| `zerg build <file>`         | compile a module to an object (`--emit lib`, the default) |
| `zerg build --emit bin <f>` | link a program                                            |
| `zerg fmt <file>`           | rewrite source in the one canonical style                 |
| `zerg lint <file>`          | report unused imports and dead private declarations       |

`--emit` also takes `tokens`, `ast`, and `c` to print an intermediate form instead of
producing a file. A program is built module by module: `-j` compiles several units at
once, and results are cached by content in `.zerg-cache/`, so a rebuild that changes one
module recompiles one module.

## A small core, a little sugar

The surface is mostly **sugar over a tiny core** — one way to do it, nothing hidden:

```text
break if done                   # → if done { break }
with open(path) as f { … }      # scoped resource, torn down on every exit
print f"{count} × {ratio:.2f}"  # Python-style interpolation → str concatenation

#[derive(Eq, Ord)]              # the compiler writes the impls from the structure
struct Point { x: int; y: int }
```

Each form desugars to the core — the full table is in **[Syntax Sugar](docs/surface/syntax-sugar.md)**.

Control flow stays flat: `break` / `continue` act only on the nearest `for`, and there are **no loop
labels** — to leave an outer loop, extract a function and `return`.

## Built-in functions

A small, **fixed** set of compiler-recognized functions — no `import` needed:

| Built-in                                  | Does                                                                 |
| ----------------------------------------- | -------------------------------------------------------------------- |
| `Ref(x)` / `deref(r)`                     | construct / read the reference-counted box                           |
| `int` `uint` `float` `bool` `byte` `rune` | primitive conversion `T(x)`; `int("…")` also parses a decimal string |
| `str(bytes)`, `list[byte](s)`             | the str ⇄ list bridges (also `runes`)                                |
| `ValueError` … `KeyError`                 | build an `Err` of that fixed kind                                    |
| `addr` `ptr` `ptr[T]` `uint(p)`           | raw-pointer ops — specified, **not built by either compiler today**  |

`print` is a **keyword**, not a function; `list.len()` and friends are **methods**. Full detail —
**[Built-in Functions](docs/runtime/builtins.md)**.

## Standard library

Pure-Zerg packages over the self runtime (zero external dependency), reached with `import "<name>"`:

| Package       | Provides                                       |
| ------------- | ---------------------------------------------- |
| **`io`**      | stdout writers, whole-file & stdin read/write  |
| **`fs`**      | `exists` / `remove`                            |
| **`os`**      | `env`, `exit`, `platform`, `arch`, `run`       |
| **`strings`** | `split` / `join`, search, trim, case folding   |
| **`ascii`**   | byte classification for a tokeniser            |
| **`cli`**     | option parsing and the `--help` it renders     |
| **`strconv`** | base-N `parse_int` / `to_string`, `parse_bool` |
| **`time`**    | `now`, `monotonic`, `after` / `ticker` timers  |
| **`math`**    | numeric helpers, `sqrt` / `pow`, `pi` / `e`    |
| **`rand`**    | a deterministic, non-cryptographic generator   |
| **`atomic`**  | the safe shared-mutable primitive              |
| **`testing`** | `assert` / `assert_eq` / `assert_ne`           |

Full catalogue with signatures — **[Standard Library](docs/runtime/stdlib.md)**.

## Compile Flow

```text
┌──────────────────┐
│  Zerg source     │
│  (.zg)           │
└────────┬─────────┘
         │
         ▼
┌────────────────────────── Zerg compiler ───────────────────────────┐
│                                                                    │
│  ┌─────────┐    ┌─────────┐    ┌────────────┐    ┌─────────────┐   │
│  │  lexer  │──> │ parser  │──> │ type check │──> │  C codegen  │   │
│  └─────────┘    └─────────┘    └────────────┘    └─────────────┘   │
│  └───────────────── frontend ──────────────┘     └── backend ──┘   │
└─────────────────────────────────┬──────────────────────────────────┘
                                  │
                                  ▼
                     ┌───────────────────────────┐
                     │  C source code            │
                     │  (C17 → C99)              │
                     └─────────────┬─────────────┘
                                  │
                                  ▼
                     ┌───────────────────────────┐
                     │  C compiler (cc)          │
                     └─────────────┬─────────────┘
                                  │
                                  ▼
                     ┌───────────────────────────┐
                     │  native executable        │
                     └───────────────────────────┘
```

Bootstrap compiler: **Go**, intentionally minimal. It emits C and shells out to `cc`; a small hosted
runtime (in C) provides the scheduler, channels, reference counting, and the string/collection primitives.

**Zero-dependency, in two layers.** A compiled program links no third-party library. The **runtime** — the
small C floor that reaches the OS through the platform C library (libc / libSystem) and nothing else — is
fixed by **both the specification and its implementation** (the semantics, plus the concrete layout and ABI
the compiler depends on). The **standard library** (`src/stdlib/*.zg`) is **pure Zerg** built on that floor,
an implementation detail bound **only by its interface** — so `io.read_file` loops the runtime's syscall
leaves and `math.sqrt` is a Zerg algorithm, never a libc / libm binding. See
[`src/runtime/README.md`](src/runtime/README.md).

## Status & Limitations

Zerg is a Phase-1 MVP. The compiler that ships is **`zerg`**, written in Zerg and compiled
by itself; **`zerg0`** is a Go-hosted seed whose only job is building it. Every status claim
below — and every marker in the specification — is about **`zerg`**, the one a `make` build
puts in `bin/`. The seed's narrower subset is its own contract, in
[`src/bootstrap/README.md`](src/bootstrap/README.md), and a reader writing Zerg never meets it.

**The contract.** A form is either lowered correctly or refused **by name** at compile time.
It is never a crash, never a silently wrong answer, and never an error reported by the C
compiler or the linker against generated code nobody wrote. Where the specification marks a
feature **[not yet]**, using it raises `NotImplemented` and stops.

**What is built.** Structs and enums (payload, recursive, and with observable
discriminants), `match` with exhaustiveness checking, `list[T]` and `map[K, V]`, strings and
bytes, `mut &` parameters, optionals with `?` / `??` / `?.` / `!`, the whole value tier
(`Either[X, Y]` / `Result[T]` / `Left` / `Right`), `guard` / `raise` with cause chaining,
`defer` and `del`, ranges, f-strings, inherent `impl` and `spec` / `impl Spec for T`,
modules with `pub` and `init()`, and the whole concurrency chapter — `spawn`, `chan[T]`,
directional ends, `close`, `select` and `for select` with the non-blocking `_` arm, and
`time.after` / `time.ticker`.

**Not yet (each refused by name).** Generics and generic type aliases; `derive`, and with it
`==` / `<` on a composite; `spec` provided methods; closures that capture; `set[T]`; fixed
arrays `[T; N]`; slicing; `list` / `map` equality; tuple, struct and list patterns, and
or-patterns; a block as a `match` arm body; f-string conversions (`!r` / `!s` / `!a`), format
specs and `{x=}`; structural rendering of a composite; `for c in <str>`; `Ref[T]` and the
`atomic` module; command literals; `unsafe`, raw pointers and inline assembly; the `is`
type-test for non-error types; the `Reader` / `stdin` I/O surface; and the `zerg test` runner.

**Known deviations (bugs the spec records against current behavior).** Arithmetic lowers to
plain C, so overflow and division-by-zero do not trap and the wrapping `+%` operators are
the same as `+`; out-of-range literals for the named integer types are not rejected; the
bootstrap emits `-std=c11` rather than the specified C17-default / C99-fallback;
left-to-right evaluation order of call arguments and operands is not enforced; and the
scheduler is **cooperative, not preemptive** — nothing takes a coroutine off its worker
until it parks, so a CPU-bound coroutine occupies one, and as many of them as there are
workers stop the program. Each is marked where it appears in the spec.

## DDD (Dream-Driven Development)

Features are driven by what the author dreams of and needs — nothing more.
