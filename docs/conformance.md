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
| **[not yet]**                | Specified, not built. Using it is a clean compile error naming the form.         |
| **[implementation-defined]** | The spec deliberately does not pin this; a conforming implementation may choose. |
| **[deviation]**              | The behaviour does **not** match this spec; a tracked bug.                       |

The distinction that matters is between the second marker and the third. A **[not yet]**
is honest: the compiler says the form's name and stops. It usually says it as a
`NotImplemented`, and a handful of forms are turned away by an ordinary checked rule
instead — the chapter says which, and the point is the naming rather than the wording.
A **[deviation]** is a program
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
output binary is produced. Each diagnostic is written to standard error, and a reader meets **two
renderings** of one, because a diagnostic reaches standard error by two routes.

A rule the **checker** collects reports through the diagnostic list, and that is the full form:

```text
error: E335 cannot bind str to a int binding: `x`
  --> demo.zg:2:5
   |
 2 |     x: int = "s"
   |     ^
```

A form the **parser or the emitter refuses** stops the run where it stands, and what it prints is the
sentence and — when the site had a place to hand — the trailer, with neither the `error:` prefix nor the
quoted line and caret under it:

```text
E224 NotImplemented: `unsafe { … }` as an EXPRESSION — GRAMMAR makes it a block whose value the
expression takes, and this compiler builds only the module-level `unsafe { … }` GROUP
  --> demo.zg:2:7
```

Both are diagnostics and both are normative in the only sense the wording ever is: **which** programs are
rejected. Neither rendering is. The difference is worth naming rather than hiding because it is what a
reader actually meets, and because the second shape is the one a rule loses its place and its code in — see
the deviation below.

The **place is a trailer**, not a prefix: the sentence comes first, and `--> file:line:col` sits on the
line under it, where `line` and `col` are 1-based. A failed compilation exits with a non-zero status.
Diagnostic wording
is not normative — two implementations may phrase the same rejection differently — but **which** programs
are rejected is (see each chapter's rules; the reject list is normative, the message text is not). The
`fmt` and `lint` tools are advisory and never change a program's meaning.

A diagnostic MAY be followed by the source line it is about and a caret marking what on that line it
concerns; `zerg` renders one for the first shape and not for the second. A conforming implementation need
not, and the shape of it is not normative — only that the place, where one is given, names a file, a line
and a column.
An ill-formed program SHOULD report every diagnostic it can find in one run rather than stopping at the
first — `zerg` does, for the rules it checks; a refusal ends the run at the first, which is the other half
of what the two shapes are.

> **[deviation]** A place and a code are owed on every diagnostic, and what decides whether one carries
> them is the **channel**, not whether the rule checks or refuses. All three stages that answer about a
> PROGRAM now have one — `chk_at` in check.zg, `p_diag` in parser.zg, `c_diag` in emit.zg — and each takes
> the code as an argument and reads the place itself, so a site cannot carry one and forget the other. What
> is left of this deviation is the **lexer**, whose two refusals carry neither.
>
> ---
>
> Measured today. **The parser is done.** All **103** of its refusals report through its channel. Nine
> rules that had no code at all — the catch-all _`X` is not an expression this compiler reads_ among them —
> were given `E601`–`E609`, a gate case each and a catalogue row each. One raise is left with neither on
> purpose: `p_impossible`, an arm no program reaches, where a code would be an identity no case could ever
> assert. What the change was visible as: **31** `no-place` markers retired from `scripts/reject-check.sh`,
> and `reject-fuzz`'s `write-immutable` ceiling, the parser's last place-less refusal, down to zero.
>
> **The emitter is done too.** It had **126** raise statements, **76** opening with a code and **13**
> appending a place; all **125** it has now go through `c_diag` / `c_diag_at`, and the missing one is a
> rule that stopped raising — a struct and an enum record their own position, so a duplicate declaration
> reaches the checking channel like the other four kinds. Forty-three rules that had no code were given
> `E701`–`E743`, a gate case each and a catalogue row each; `E4xx` closed at `E498` with `E499` retired
> unspent, exactly as `E2xx` closed for the parser. **No raise here is exempt.** Two were briefly written
> as an ICE on the reasoning that the parser refuses the only shape that reaches them, and both reasonings
> were wrong — `p_builtin_type_ctor` exempts six names from `E275` and four of them are not reserved, so
> `fn set[T](…)` and `set[int, str](1)` reach the arity rule; and `1..=nil` writes out by hand the shape a
> bare `..=` used to leave behind. An unreachable rule has to be shown unreachable, and neither was.
> What the change is visible as: **18** more `no-place` markers retired, and `scripts/refuse-check.sh`'s
> `place` marker gone entirely — a place is asserted of every `zerg` case there now, because there is no
> longer a case that may lack one.
>
> **The lexer is what is left**, and it is two: _f-string: unterminated literal_ and _f-string: a bare '}'
> is not text_. Both are raised from the driver rather than from a stage with a channel.
>
> `scripts/error-codes-check.sh` cannot see an uncoded rule by comparing its three sets: it compares codes
> that exist against the gates and the catalogue, and a rule with no code is absent from all three. It sees
> the parser's and the emitter's by a different question — a `raise` in either file that writes its own
> message rather than asking the channel for one is reported by name — which is the assertion that keeps a
> channel from being bypassed a site at a time. It is a ratchet and not a proof: it sees a string LITERAL,
> so a message picked into a variable first would pass, and it must let `raise anything(…)` through
> because every site in both files raises a call.
>
> ---
>
> **Checked rules are not exempt**, which is the part of this the older text had backwards, and the two
> that were named here have both moved. A constant cycle (`E732`) reported with no place and no code at
> all; it opens with its code and points at the first constant that cannot be given a value. `E382`, a name
> declared twice, reported with a place for some declarations and not for others — a duplicate `type A = …`
> carried one and a duplicate `struct` did not, because a struct and an enum were registered before
> anything recorded a position to give it. Both now carry the declaration's own line, and the channel that
> chose between raising and recording is gone with the reason for it.
>
> Two more used to be on that list and are not: `` `x` is used after del `` and its on-some-paths sibling,
> now `E297` and `E298`. Nothing about the rules changed — they moved from `raise` to the checking channel,
> which is the only thing that decides the question, and the move is the whole fix.
>
> The position `zerg` records is per STATEMENT, so a column names where the statement begins; the caret
> narrows to the token when the message quotes one that is on that line. A rule that runs over a
> DECLARATION — a field's default, a duplicate variant name, a method declared twice — takes the
> declaration's place instead, because it runs before any statement has been emitted for the marker to
> point at.

The rule the project holds itself to is stronger than the paragraphs above, and it is worth stating on its
own, because it is the yardstick every finding in this specification is measured against:

**A form is either lowered correctly or refused by name.** It is never a crash, never a silently wrong answer,
and never an error reported by the C compiler or the linker against generated code nobody wrote.

> **[deviation]** A form inside a **template nobody instantiates** is neither. Every rule this compiler
> enforces is driven by the walk that LOWERS a body, and a template is removed before that walk — only the
> specializations a call asks for are lowered — so `fn f[T](xs: list[T], v: T) { xs.append(v) }` that no call
> reaches compiles in silence, and so does the same body assigning to an immutable binding, which is `E307`,
> a rule enforced everywhere else. The seed diagnoses both, because its semantic pass walks a **declaration**
> rather than a lowering. It is one gap owed once and not a property of any one rule. Closing it needs a body
> checked against a type parameter's **bounds** rather than against a concrete type — `x.show()` on a
> `T: Show` has no method to resolve until `T` is one, and lowering is defined only over concrete types —
> which is a checker this compiler does not have.

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
>
> None of it is reachable yet. The only surface that asks for a width or a precision is a **format spec in
> an f-string hole**, and that is `[not yet]` — every `{x:…}`, `{x:.2f}` included, reports _E225
> NotImplemented: an f-string ':spec' format spec_. The three bounds are implemented in the runtime and the
> shipping compiler emits no call that reaches them, so this paragraph documents a contract a program
> cannot yet observe.

## Runtime abort contract

An **uncaught error** ends the program deterministically: a `raise` that reaches `main` uncaught, a failed
force `!` on an absent optional, or a built-in runtime fault (see [Errors](code/errors.md)) that no `guard`/`?`
recovers. On abort the runtime:

1. writes **one line** describing the error to **standard error**, followed by a newline;
2. runs the pending `defer`s on the unwound path (the same cleanup stack the normal return path uses); and
3. terminates the process with exit status **1**.

The line a **taxonomy** error writes has the form `Kind: text`, where `text` is the error's `message()` — for
example `IndexError: index out of range`. The exact `text` is not normative; the `Kind:` prefix is. It belongs
to the **line** and not to the message: `message()` answers `text` alone, and the prefix is rendered for **any**
raised taxonomy `Err` — a `raise ValueError("bad input")` a program wrote and a fault the runtime raised itself
report the same shape. An error carrying **no** kind (what a bare `raise "…"` builds) writes its message alone.
See [Errors](code/errors.md) for the built-in error kinds and which operations raise them.

> **[deviation]** A stack overflow — a coroutine running past its guard page, or `main` past its native
> stack — now dies with its name: the runtime's fault handler writes `StackOverflowError: stack overflow`
> to standard error and terminates with exit status **1**, steps 1 and 3 of the contract above. What
> still deviates is step 2: the faulting stack is exhausted and cannot be unwound from a signal handler,
> so the pending `defer`s are **skipped**, not run — and unlike an ordinary abort, which a coroutine
> contains to itself, an overflow ends the whole process wherever it happens. A fault the handler does
> not recognise is handed back to whatever held the signal before the runtime did (a sanitizer's handler,
> or the default disposition), so it dies as the signal it is with that handler's diagnostic intact. The
> two windows it does claim are **one page each** — a coroutine's guard page exactly, and the single page
> below `main`'s stack bound — which is also the whole of what it can misname: an access into that one
> page under `main` reads as an overflow. See [Errors](code/errors.md).

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
  **[deviation]** (for example, a stack overflow is a hardware fault the runtime names
  `StackOverflowError` and exits 1 on, rather than a clean unwind that runs the pending `defer`s — see
  [Errors](code/errors.md)).
- **Implementation-defined** — the result is one of a set the implementation documents but the spec does
  not fix. A conforming program should not depend on a particular choice. Current implementation-defined
  points, each detailed in its chapter, include:
  the winning arm of a `select` among several ready arms ([Coroutines](code/coroutine.md)); the precision and
  spelling of floating-point rendering ([Format](runtime/format.md)); and any coroutine ordering beyond the
  guaranteed send→receive happens-before ([Coroutines](code/coroutine.md)).

Anything the specification neither requires nor marks implementation-defined is unspecified and may change;
do not rely on it.
