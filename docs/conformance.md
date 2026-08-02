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

## The language versus this compiler

Zerg is specified as a whole; the compiler that ships implements a subset. Rather than
document only what ships, each chapter specifies the intended feature and marks what is
missing, so the specification is a stable target and the gaps are explicit.

**The default is that a feature works.** Prose with no marker describes something `zerg`
implements as specified — that is the ordinary case, and it is not annotated. Only these
carry a marker:

| Marker                       | Meaning                                                                          |
| ---------------------------- | -------------------------------------------------------------------------------- |
| **[not yet]**                | Specified, not built. Using it raises `NotImplemented` — a clean compile error.  |
| **[implementation-defined]** | The spec deliberately does not pin this; a conforming implementation may choose. |
| **[deviation]**              | The behaviour does **not** match this spec; a tracked bug.                       |

The distinction that matters is between the second marker and the third. A **[not yet]**
is honest: the compiler says the form's name and stops. A **[deviation]** is a program
that compiles and behaves differently from what is written here — and the project's
standing rule is that a form is implemented or refused by name, never silently wrong, so a
deviation is a bug with a fix owed, not a documented state.

**Which compiler.** The markers are measured against **`zerg`**, the self-hosting compiler
that ships and the one a `make` build puts in `bin/`. The other, `zerg0`, is a Go-hosted
seed whose only job is building `zerg`; it supports a narrower slice — the part the
compiler's own sources are written in — and refuses the rest by name. **The seed's gaps are
not marked here.** They are not gaps in the language, and a reader writing Zerg never meets
them; they are listed in [`src/bootstrap/README.md`](../src/bootstrap/README.md), which is
the seed's own contract.

A section with no marker inherits the marker of its enclosing feature; a paragraph may override with its
own. A **[deviation]** always states both the specified behavior and what the implementation does instead.

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

A diagnostic MAY be followed by the source line it is about and a caret marking what on that line it
concerns; `zerg` renders one. A conforming implementation need not, and the shape of it is not normative.
An ill-formed program SHOULD report every diagnostic it can find in one run rather than stopping at the
first — `zerg` does, for the rules it checks.

> **[deviation]** The `file:line:col` prefix is present on the rules `zerg` CHECKS, and absent on the forms
> it REFUSES: a `NotImplemented` from the parser or the emitter still reports the form's name with no place.
> The position `zerg` records is per STATEMENT, so a column names where the statement begins; the caret
> narrows to the token when the message quotes one that is on that line.

## Runtime abort contract

An **uncaught error** ends the program deterministically: a `raise` that reaches `main` uncaught, a failed
force `!` on an absent optional, or a built-in runtime fault (see [Errors](code/errors.md)) that no `guard`/`?`
recovers. On abort the runtime:

1. writes the error's message to **standard error**, followed by a newline;
2. runs the pending `defer`s on the unwound path (the same cleanup stack the normal return path uses); and
3. terminates the process with exit status **1**.

A built-in error's message has the form `Kind: text` (for example `IndexError: list index out of range`).
The exact `text` is not normative; the `Kind:` prefix for a taxonomy error is. See [Errors](code/errors.md) for
the built-in error kinds and which operations raise them.

> **[deviation]** A hardware fault that the runtime cannot intercept — today, a coroutine stack overflowing
> past its guard page, or `main`'s unguarded native stack — terminates the process by signal without
> running `defer`s, rather than as a clean `StackOverflowError` abort. See [Errors](code/errors.md).

## Undefined and implementation-defined behavior

The specification uses these terms precisely:

- **Undefined behavior (UB)** — the spec places no requirement on the result. A conforming program must
  avoid it; a conforming implementation may do anything, including crash. Zerg's design goal is to have
  **no reachable UB from safe code**; where the bootstrap currently admits UB, the chapter marks it a
  **[deviation]** (for example, a coroutine stack overflow is a hardware fault rather than a clean
  `StackOverflowError` — see [Errors](code/errors.md)).
- **Implementation-defined** — the result is one of a set the implementation documents but the spec does
  not fix. A conforming program should not depend on a particular choice. Current implementation-defined
  points, each detailed in its chapter, include: the evaluation order of call arguments and operator
  operands ([Memory Model](core/memory.md) — the spec's intended left-to-right order is **[not yet]** enforced);
  the winning arm of a `select` among several ready arms ([Coroutines](code/coroutine.md)); the precision and
  spelling of floating-point rendering ([Format](runtime/format.md)); and any coroutine ordering beyond the
  guaranteed send→receive happens-before ([Coroutines](code/coroutine.md)).

Anything the specification neither requires nor marks implementation-defined is unspecified and may change;
do not rely on it.
