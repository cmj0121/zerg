# Zerg

English | [繁體中文](README.zh-TW.md)

> Write the code as you think — one way, and only one way, to do it.

Zerg is a **compiled, general-purpose language**. The compiler translates your Zerg source to **C**
(**C17** by default, **C99** as a fallback), then hands it off to a C compiler to build the native binary.
Programs are fast to write, easy to read, and overwhelmingly straightforward.

## Design Principles

| Principle        | Description                                                          |
| ---------------- | -------------------------------------------------------------------- |
| small and crisp  | minimal syntax                                                       |
| safe by default  | immutable and private unless explicitly `mut`/`pub`                  |
| null-safe        | no billion-dollar mistakes                                           |
| concurrent       | built-in support for concurrency                                     |
| procedural-first | straightforward, top-down control flow                               |
| scope-owned      | no GC — memory freed at scope exit                                   |
| strongly typed   | catch errors at compile time                                         |
| explicit casts   | no implicit conversion by default; a type may opt in to an auto-cast |
| copy-by-value    | values copied by default; compiler may optimize                      |

Full semantics — primitive & user types, casts, the memory model, concurrency, and null-safety —
are in the **[Language Reference](docs/language.md)**, with companion references for
**[Modules, Packages & Programs](docs/package.md)**, **[Coroutines & Channels](docs/coroutine.md)**,
**[Collections](docs/collections.md)**, **[Derive & Default Behavior](docs/derive.md)**,
**[Process & I/O](docs/io.md)**, and the **[FFI](docs/ffi.md)**.

## Influences

It's a synthesis, not a clone — every piece has prior art:

- **Rust** — the `spec` system (nominal traits: coherence + orphan rule, default methods,
  monomorphization, `dyn` existentials), the value/abort two-tier errors, `Ref` ≈ `Rc`/`Arc`.
- **Go** — `spawn` + channels on an M:N scheduler, untyped literals, module-as-directory, `defer`.
- **Erlang / Pony** — **no** shared mutable state, taken as an absolute, with fair scheduling.
- **Swift** — value semantics, a `mut` parameter ≈ `inout`, the `?.` / `??` / `!` null-safety suite.
- **Zig** — overflow raises; the `+%` / `-%` / `*%` wrapping operators.
- **Nim / V** — transpiling to C as the codegen target.

But the combination is Zerg's own: **transpile-to-C, a Rust-grade `spec` system, CSP channels with _no_
shared mutable state, and copy-by-value with no `move` syntax.**

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
                     │  (default C17 → C99)      │
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

Bootstrap compiler: **Go**, intentionally minimal.

## DDD (Dream-Driven Development)

Features are driven by what the author dreams of and needs — nothing more.
