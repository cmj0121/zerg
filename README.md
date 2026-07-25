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
| concurrent       | built-in coroutines and channels (a cooperative **N:1** scheduler in this phase)                      |
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
**[Modules, Packages & Programs](docs/package.md)**, **[Coroutines & Channels](docs/coroutine.md)**,
**[Grammar](docs/grammar.md)**, **[Syntax Sugar](docs/syntax-sugar.md)**,
**[Collections](docs/collections.md)**, **[Derive & Default Behavior](docs/derive.md)**,
**[Process & I/O](docs/io.md)**, and the **[FFI](docs/ffi.md)**.

## Quickstart

Build the bootstrap toolchain (Go ≥ 1.26 and a C compiler are required), then compile a program:

```sh
make                       # build the single `zerg` binary into ./bin/
cat > hello.zg <<'ZG'
fn main() {
    print "hello, world"
}
ZG
./bin/zerg build hello.zg  # emit C, then invoke cc → ./hello
./hello                    # hello, world
```

The one `zerg` tool carries the sub-commands you need:

| Command             | What it does                                        |
| ------------------- | --------------------------------------------------- |
| `zerg build <file>` | compile a `.zg` program to a native binary          |
| `zerg fmt <file>`   | format source to the one canonical style            |
| `zerg lint <file>`  | report unused imports and dead private declarations |
| `zerg test <file>`  | run the program's `#[test]` functions               |

`zerg build … --emit c` (or `tokens` / `ast`) prints the intermediate form instead of building.

## A small core, a little sugar

The surface is mostly **sugar over a tiny core** — one way to do it, nothing hidden:

```text
break if done                   # → if done { break }
with open(path) as f { … }      # scoped resource, torn down on every exit
print f"{count} × {ratio:.2f}"  # Python-style interpolation → str concatenation

#[derive(Eq, Ord)]              # the compiler writes the impls from the structure
struct Point { x: int; y: int }
```

Each form desugars to the core — the full table is in **[Syntax Sugar](docs/syntax-sugar.md)**.

Control flow stays flat: `break` / `continue` act only on the nearest `for`, and there are **no loop
labels** — to leave an outer loop, extract a function and `return`.

## Built-in functions

A small, **fixed** set of compiler-recognized functions — no `import` needed:

| Built-in                                  | Does                                                                      |
| ----------------------------------------- | ------------------------------------------------------------------------- |
| `Ref(x)` / `deref(r)`                     | construct / read the reference-counted box                                |
| `int` `uint` `float` `bool` `byte` `rune` | primitive conversion `T(x)`; `int("…")` also parses a decimal string      |
| `str(bytes)`, `list[byte](s)`             | the str ⇄ list bridges (also `runes`)                                     |
| `ValueError` … `KeyError`                 | build an `Err` of that fixed kind                                         |
| `addr` `ptr` `ptr[T]` `uint(p)`           | raw-pointer ops — **`unsafe` only** (plus `.load` / `.store` / `.offset`) |

`print` is a **keyword**, not a function; `list.len()` and friends are **methods**. Full detail —
**[Built-in Functions](docs/builtins.md)**.

## Standard library

Pure-Zerg packages over the self runtime (zero external dependency), reached with `import "<name>"`:

| Package       | Provides                                     |
| ------------- | -------------------------------------------- |
| **`io`**      | stdout writers, whole-file read/write        |
| **`fs`**      | `exists` / `remove`                          |
| **`os`**      | `env`, `exit`, `platform`, `arch`            |
| **`strings`** | `split` / `join`, search, trim, case folding |
| **`time`**    | `now` (wall clock), `monotonic`              |
| **`math`**    | numeric helpers, `sqrt` / `pow`, `pi` / `e`  |
| **`rand`**    | a deterministic, non-cryptographic generator |
| **`atomic`**  | the safe shared-mutable primitive            |
| **`testing`** | `assert` / `assert_eq` / `assert_ne`         |

Full catalogue with signatures — **[Standard Library](docs/stdlib.md)**.

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

Zerg is a Phase-1 MVP. The bootstrap compiler is complete enough to compile non-trivial programs — it
lexes, parses, type-checks, monomorphizes generics, and emits C over a hosted runtime, and it self-hosts
its own front-end primitives (string scanning, file reading, integer parsing). The specification defines
the whole language and marks each feature's status; the headline gaps a newcomer should know:

**Implemented.** Value & reference types, structs, enums with payloads, generics + monomorphization,
`spec` / `impl` with provided methods, `derive(Eq, Ord)`, pattern matching, optionals with `?` / `??` / `!`
/ `guard`, a fixed built-in error taxonomy, recursive types (auto-boxed, reference-counted), closures
(including capturing), coroutines + channels + `select` (a cooperative **N:1** scheduler), modules with
`pub` visibility and `init()`, raw pointers and inline assembly under `unsafe`, and the `build` / `fmt` /
`lint` / `test` tools.

**Not yet (defined, marked in the spec).** Arithmetic that traps on overflow / division-by-zero and the
wrapping `+%` operators (today arithmetic lowers to plain C); the full `derive` set beyond `Eq` / `Ord`
(`Hash` / `Encode` / `Decode`); `set[T]`; `list` / `map` equality; command literals (`` `git status` ``);
the `is` type-test for non-error types; a preemptive **M:N** scheduler; the `Reader` / `stdin` I/O surface;
generic type aliases; and a handful of smaller forms tracked in the spec's status markers.

**Known deviations (bugs the spec records against current behavior).** A few observable behaviors do not
yet match the intended semantics — the bootstrap emits `-std=c11` rather than the specified C17-default /
C99-fallback; left-to-right evaluation order of call arguments and operands is not yet enforced; and
out-of-range literals for the named integer types are not yet rejected. Each is marked where it appears in
the spec.

## DDD (Dream-Driven Development)

Features are driven by what the author dreams of and needs — nothing more.
