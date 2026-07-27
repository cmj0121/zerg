# Conformance & Specification Conventions

English | [繁體中文](conformance.zh-TW.md)

This chapter defines how to read the Zerg specification: what the documents are normative about, the
status markers that flag the gap between the language and the current bootstrap compiler, and the
observable contracts (diagnostics, runtime abort, undefined behavior) every other chapter relies on.

## What is normative

- **[`GRAMMAR`](../GRAMMAR)** (repo root, W3C-style EBNF) is the normative definition of Zerg **syntax**.
  A construct that `GRAMMAR` does not derive is not a Zerg program.
- The specification chapters under `docs/` are normative for **semantics** — static (typing, name
  resolution, coherence, visibility) and dynamic (evaluation, memory, concurrency, errors) — and reference
  `GRAMMAR` for surface forms rather than restating it.
- Notes on how the **reference bootstrap** lowers Zerg to C — its C ABI, name mangling, and memory layout —
  are **informative**: they document one implementation and are not binding on a conforming implementation.
- The English text is authoritative; the `*.zh-TW.md` editions are translations kept in lockstep and carry
  no independent normative weight.

A **conforming implementation** accepts every well-formed program whose features are marked implemented,
rejects every ill-formed program per the stated rules, and reproduces the specified **observable behavior**
— program output, exit status, and diagnostics — except where a behavior is explicitly marked
implementation-defined. A conforming implementation need not emit C, nor match the reference compiler's
generated code, mangling, or memory layout.

## The language versus this bootstrap

Zerg is specified as a whole; the Phase-1 bootstrap implements a subset. Rather than describe only what
ships, each chapter specifies the intended feature and tags its current status, so the specification is a
stable target and the gaps are explicit. Every feature carries one of:

| Marker                       | Meaning                                                                          |
| ---------------------------- | -------------------------------------------------------------------------------- |
| **[implemented]**            | The SEED (`zerg0`) implements this as specified.                                 |
| **[not yet: Phase N]**       | Specified, not yet built. Using it is a clean compile error today.               |
| **[implementation-defined]** | The spec deliberately does not pin this; a conforming implementation may choose. |
| **[deviation]**              | The seed's current behavior does **not** match this spec; a tracked bug.         |

**Which compiler a marker refers to.** There are two: `zerg0`, the Go-hosted seed whose
only job is building the compiler, and `zerg`, the self-hosted compiler that ships. The
markers in this specification are measured against the **seed**, because it is the wider
of the two — `zerg` implements a subset of what the seed does, and that subset is
documented in [`src/compiler/README.md`](../src/compiler/README.md) rather than marked
per-feature here. A feature marked **[implemented]** may therefore be one `zerg` does not
accept yet.

Some features were specified, built in the seed, and then REMOVED from it when the seed
was cut down to its one job — closures and function values, `map[K, V]`, coroutines,
channels and `select`, `#[dyn]` dispatch, and `unsafe` pointers and inline assembly. Those
are marked **[not yet]** again: the seed rejects them with a diagnostic, which is exactly
what that marker promises.

A section with no marker inherits the marker of its enclosing feature; a paragraph may override with its
own. A **[deviation]** always states both the specified behavior and what the bootstrap does instead.

## Diagnostics contract

A well-formed program compiles; an ill-formed one is rejected with one or more **diagnostics** and no
output binary is produced. Each diagnostic is written to standard error in the form

```text
file:line:col: message
```

where `line` and `col` are 1-based. A failed compilation exits with a non-zero status. Diagnostic wording
is not normative — two implementations may phrase the same rejection differently — but **which** programs
are rejected is (see each chapter's rules; the reject list is normative, the message text is not). The
`fmt` and `lint` tools are advisory and never change a program's meaning.

## Runtime abort contract

An **uncaught error** ends the program deterministically: a `raise` that reaches `main` uncaught, a failed
force `!` on an absent optional, or a built-in runtime fault (see [Errors](errors.md)) that no `guard`/`?`
recovers. On abort the runtime:

1. writes the error's message to **standard error**, followed by a newline;
2. runs the pending `defer`s on the unwound path (the same cleanup stack the normal return path uses); and
3. terminates the process with exit status **1**.

A built-in error's message has the form `Kind: text` (for example `IndexError: list index out of range`).
The exact `text` is not normative; the `Kind:` prefix for a taxonomy error is. See [Errors](errors.md) for
the built-in error kinds and which operations raise them.

> **[deviation]** A hardware fault that the runtime cannot intercept — today, a coroutine stack overflowing
> past its guard page, or `main`'s unguarded native stack — terminates the process by signal without
> running `defer`s, rather than as a clean `StackOverflowError` abort. See [Errors](errors.md).

## Undefined and implementation-defined behavior

The specification uses these terms precisely:

- **Undefined behavior (UB)** — the spec places no requirement on the result. A conforming program must
  avoid it; a conforming implementation may do anything, including crash. Zerg's design goal is to have
  **no reachable UB from safe code**; where the bootstrap currently admits UB, the chapter marks it a
  **[deviation]** (for example, integer overflow and division by zero lower to plain C today rather than
  trapping — see [Types](types.md)).
- **Implementation-defined** — the result is one of a set the implementation documents but the spec does
  not fix. A conforming program should not depend on a particular choice. Current implementation-defined
  points, each detailed in its chapter, include: the evaluation order of call arguments and operator
  operands ([Memory Model](memory.md) — the spec's intended left-to-right order is **[not yet]** enforced);
  the winning arm of a `select` among several ready arms ([Coroutines](coroutine.md)); the precision and
  spelling of floating-point rendering ([Format](format.md)); and any coroutine ordering beyond the
  guaranteed send→receive happens-before ([Coroutines](coroutine.md)).

Anything the specification neither requires nor marks implementation-defined is unspecified and may change;
do not rely on it.
