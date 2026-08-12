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

## Two profiles

The language is one, and what an implementation must answer for is two.

The **core profile** is everything whose meaning is the language's own: literals, expressions, functions,
control flow, types, patterns, concurrency, modules and cleanup. An implementation targeting anything at
all can answer for it, and a conforming implementation **must**.

The **system profile** is inline assembly, raw pointers, and the `unsafe` groups that hold them — forms
whose meaning belongs to a machine rather than to the language. An implementation with no machine to speak
of, one targeting a VM or a checker that never emits, **may decline the profile**. Declining is not
silence: every form in it must still be **refused by name**, which is the standing rule everywhere else.
What the profile changes is whether that refusal is a defect.

An implementation **states which profiles it claims**. This one claims the core and declines the system
profile. Claiming a profile is not the same as having finished it: where `zerg` falls short of a core form
the chapter says so with a `[not yet]`, and the form is refused by name — that is a debt inside a claimed
profile. A declined profile carries no such debt, which is the whole difference.

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

The rule the project holds itself to is stronger than the paragraphs above, and it is worth stating on its
own, because it is the yardstick every finding in this specification is measured against:

**A form is either lowered correctly or refused by name.** It is never a crash, never a silently wrong answer,
and never an error reported by the C compiler or the linker against generated code nobody wrote.

One consequence is worth writing down here, because no single chapter owns it: a program with no `fn main` is
grammatical — `program ::= stmt-list`, the `nop` program the grammar opens with — so what rejects it is the
**build**. `--emit bin` reports that the entry file declares no `fn main`, with a place, before anything
reaches cc or the linker (see [Packages & Programs](runtime/package.md) for the rule); `--emit lib` builds the
same source to an object file, which is what a module is for.

> **[implementation-defined]** **Nesting depth is a translation limit.** A program that nests more than **200**
> levels deep is refused with a diagnostic naming the limit and the place, rather than parsed on until the
> native stack it runs on overflows. The bound is enforced where the tree is BUILT, and in two ways, because
> nesting reaches the parser by two routes: it counts its own recursion — one level per nested expression,
> block or type, which is what `(((…)))` costs — and it measures the DEPTH of each finished expression tree,
> which is what recursion cannot see, since a flat chain (`1 + 1 + … + 1`, a long method chain) parses in a
> LOOP and deepens the tree without deepening the parser. So no expression a program WRITES is deeper than
> 200, and every later pass that walks one — the checker, the linter, the language server, the substitution a
> generic instantiation runs — inherits that bound rather than counting again.
>
> The limit is on the tree the implementation must WALK, which is not always one the program wrote: an
> omitted defaulted argument is filled in at the call site, so a 190-level default backfilled into a
> 190-level call is a 380-level walk that no expression in the source states. The emitter counts its own
> depth for exactly that, and refuses with a sentence about a chain rather than about nesting. However a
> program reaches 200 levels — written or composed — the answer is a refusal.
>
> The number is measured headroom, not language: unbounded, the parser died of `SIGSEGV` at about 485 nested
> parentheses on an 8 MB stack, the expression walk gave out at about 310 method links (530 flat `+` terms)
> and the substitution pass at about 400, while the deepest nesting anywhere in this repository is five
> levels. A conforming implementation may set another bound; ISO C itself promises only 63 nested
> parenthesized expressions.

---

> **[implementation-defined]** **A format spec's width and precision are translation limits.** A `width`
> or a `precision` past **4096** is refused rather than honoured: both are a size the text being
> formatted asks the implementation to produce, and an unbounded one is a request for memory dressed
> as a rendering. A **float** additionally renders at most **100** fractional digits, which is a bound
> on digits rather than on a field and so is the float's own. The `type` letter is a closed set per rendering
> ([Text & Formatting](runtime/format.md)); a letter outside it is refused the same way.

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

## The C the reference implementation emits

The reference implementation lowers Zerg to C and hands the result to a C compiler (`cc`). Per
[What is normative](#what-is-normative) this is an implementation note, not a requirement on a conforming
implementation — nothing here binds one that emits machine code, and nothing here is observable to a Zerg
program. It is written down because the dialect is a claim the project makes on its front page, and a
number in a compiler flag that no chapter states is one that drifts.

The dialect is **C17**. `ZERG_CSTD` names another for a build that needs one — `c99` and `c11` are the
others the runtime is written to compile under, and the build cache keys an object by **the dialect and
the resolved `cc`** — the two inputs to a compile that the emitted C does not already stand for — so two
dialects, or two compilers, do not hand each other's objects back.

> **[not yet]** The **fallback** is not automatic. The intent is that a `cc` which cannot do C17 is
> retreated from to C99; no probe for that is built, so the retreat is something a build **asks** for with
> `ZERG_CSTD=c99` rather than something the compiler discovers. Both dialects are compiled and run on CI.

## Undefined and implementation-defined behavior

The specification uses these terms precisely:

- **Undefined behavior (UB)** — the spec places no requirement on the result. A conforming program must
  avoid it; a conforming implementation may do anything, including crash. Zerg's design goal is to have
  **no reachable UB from safe code**; where the bootstrap currently admits UB, the chapter marks it a
  **[deviation]** (for example, a coroutine stack overflow is a hardware fault rather than a clean
  `StackOverflowError` — see [Errors](code/errors.md)).
- **Implementation-defined** — the result is one of a set the implementation documents but the spec does
  not fix. A conforming program should not depend on a particular choice. Current implementation-defined
  points, each detailed in its chapter, include:
  the winning arm of a `select` among several ready arms ([Coroutines](code/coroutine.md)); the precision and
  spelling of floating-point rendering ([Format](runtime/format.md)); and any coroutine ordering beyond the
  guaranteed send→receive happens-before ([Coroutines](code/coroutine.md)).

Anything the specification neither requires nor marks implementation-defined is unspecified and may change;
do not rely on it.
