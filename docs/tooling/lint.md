# Zerg Linter Rules

Every rule `zerg lint` applies, each with the code that names it. Part of the
[Language Reference](../language.md). Also in [繁體中文](lint.zh-TW.md).

```sh
zerg lint <file.zg>...   # prints findings; exits nonzero when there is one
```

A rule has a **code** so it can be named — in a finding, in a review, in the `#[allow(…)]`
that suppresses it. The prefix groups them the way a Python linter's does, and the grouping
is by **what a rule does**, not by which pass implements it.

| Prefix | Group       | Is                                                 |
| ------ | ----------- | -------------------------------------------------- |
| `L1xx` | dead code   | things written that nothing reaches                |
| `L2xx` | null safety | an optional operator that does not do what it says |
| `L3xx` | capture     | what a coroutine or a deferred call actually took  |
| `L4xx` | resolution  | a name that answers to more than one thing         |
| `L5xx` | conversion  | a literal that took a type the page does not show  |
| `L6xx` | the binary  | something legal that ships and should not have     |

The formatter's `F` codes are the same scheme over a different question — what the source
LOOKS like rather than what it says ([Formatter Rules](fmt.md)). The compiler's `E` codes
are not a tool's rules at all: they name what stops a build
([Compile Diagnostics](diagnostics.md)).

Every check is answered from the parsed file alone — no types, no flow analysis — which
keeps it honest about what it can claim. Findings come back in source order, each with the
place it is about (`path:line:col`).

## Severity, and the two exit codes

A finding carries a **severity**, and there are three:

| Severity      | Printed as              | `zerg lint` | `zerg lint --strict` |
| ------------- | ----------------------- | ----------- | -------------------- |
| **a finding** | `path:line:col: L101 …` | **fails**   | **fails**            |
| **warning**   | `… warning: L601 …`     | exits 0     | **fails**            |
| **info**      | `… info: L106 …`        | exits 0     | exits 0              |

A **finding** is a rule firing on the program, which is what the tool is for. A **warning** is
what a rule says about a program that is not wrong — a `#[test]` that ships is a decision
somebody is allowed to have made — so it prints and does not fail. An **info** never changes an
exit status at all.

Only the default prints no adjective: it is what `zerg lint` is for and needs no word in front
of it, while a line that does **not** change the exit status has to say so or it reads as one
that did.

`--strict` is what `make lint` runs, over this project's own source, where neither a shipping
test nor a suppression that will never apply is acceptable. A gate board stricter than the tool
has precedent here — `refuse-check` asserts more about a refusal than `zerg build` requires.

## Suppressing a finding — `#[allow(…)]`

`#[allow(L103)]` on a statement suppresses that code over the statement it leads, and over its
block when it has one — the scope is the size of the statement, which is one rule and not a
choice between a line and a scope. It does not reach the next statement and cannot reach
another file; there is deliberately **no file-level scope**. It names **`L` codes only**: an `E`
code is a compiler diagnostic and suppressing one would make bypassing a compiler check an
official feature. See [Decorators](../core/decorators.md).

Two codes are about a suppression itself. `L106` matters more than it looks: a stale allow
silences a rule that has stopped firing, and nobody learns when the real problem returns.

| Code   | Severity    | Finding                                                    |
| ------ | ----------- | ---------------------------------------------------------- |
| `L106` | **info**    | the allow had nothing to suppress                          |
| `L107` | **warning** | the allow names a code no rule has, so it will never apply |

## L1xx — dead code

| Code   | Finding                       | Why it is worth a line                                                     |
| ------ | ----------------------------- | -------------------------------------------------------------------------- |
| `L101` | unused import                 | read, parsed and merged in for nothing, and it lies about what is needed   |
| `L102` | private function never called | a public one is a module's interface; a private one with no caller is dead |
| `L103` | binding never read            | the value was computed for nobody                                          |
| `L104` | `_ := expr`                   | the expression is already a statement; the binder is what nothing reaches  |
| `L105` | `with … as x`, `x` never read | the block already scopes the resource; the name is what nobody said        |

```text
L101 unused import "strconv"
L102 private function `never` is never called
L103 binding `unused` in `main` is never read
L104 `_ :=` in `main` — the expression is already a statement, so the binder says nothing
```

`main` is never reported by `L102`: the runtime calls it, whatever the source says.

`L104` is why `L103` says nothing about `_`: an unread `_` is what `_` **means**, so "never
read" is the one thing there is no point saying about it. The select-arm spelling of the same
redundancy is [`F407`](fmt.md)'s, because `GRAMMAR` makes that binder optional and
dropping it leaves an arm. A statement's binder has no such spelling.

`L101` and `L102` judge a declaration by its **uses**, and a use counts wherever it is
written — not only inside a function body. A **type position** is one (`ctx: testing.Context`
uses the `testing` import and writes no expression at all), and so is a module-level `const`
initialiser, a struct field's default and a parameter's default. Each of those is code that
belongs to a declaration rather than to a body.

## L2xx — null safety

Nothing here is a compile error: each of these programs runs, and does something slightly
other than what it says. That is what makes them a linter's business rather than the
compiler's.

| Code   | Finding                            | Why it is worth a line                                                |
| ------ | ---------------------------------- | --------------------------------------------------------------------- |
| `L201` | `?? nil`                           | the fallback IS the absent value, so the `??` changes nothing         |
| `L202` | `!` in a function answering a `T?` | `?` hands the absence back; `!` aborts instead, and is easier to type |

```text
L201 `?? nil` in `keep` changes nothing — the result is optional either way
L202 `!` in `forced`, which answers a `T?` — `?` hands the absence back instead of aborting
```

Both are answered from the parsed file alone, like every other rule here — `?? nil` is a
shape, and so is a `!` inside a function whose declared result carries an absence. Neither
needs a type nobody wrote down.

## L3xx — capture

| Code   | Rule                                                                        |
| ------ | --------------------------------------------------------------------------- |
| `L301` | a `mut` binding captured by `spawn` / `defer` and written after the capture |

`spawn f(k)` and `defer f(k)` take their arguments as a **snapshot**, at the line they are
written on. A write to `k` afterwards is not seen by the call — the coroutine may not have
started, and the deferred call has not run:

```zerg
mut k := 5
spawn show(k)      # captures 5
k = 99
# the coroutine prints 5
```

That is the right semantics and it is the single most misreadable thing in the language.
It is a **lint** and not an error because the program is correct and the snapshot is
usually what was wanted — the tool says what happened rather than refusing it.

A **channel** is reported too, and only for a **rebinding**. A channel is a `Ref`-like
**handle**, not a value: the coroutine gets its own handle to the same channel and
everything sent afterwards **is** seen — but a send is `ch <- v`, which is not a write and
was never a candidate. What reaches the rule is `ch = <another channel>`, after which the
coroutine holds the **old** one. An earlier version exempted channels entirely and so
suppressed nothing but correct findings.

A **write** is not only an assignment: `xs.append(2)` after capturing `xs` is exactly the
misreading this rule exists for, since a captured `list` is snapshotted by deep copy. It is
not **every** call either — `show(k)` after the capture is a read. A call counts when it
passes the binding to a `mut &` parameter, and a method when it writes through its receiver;
both are read off the declaration rather than guessed at.

It looks within one block, at the statements after the capture — including the block a
closure or a `guard` carries, which hangs off an expression rather than a statement. A write
from a **different** block is not reported: the rule reports the shape that misleads rather
than every shape that could.

## `L4xx` — resolution

| Code   | Rule                                        |
| ------ | ------------------------------------------- |
| `L402` | a `mut fn` that never writes through `this` |

`L401` stood here and has **retired**. It reported a variant name two enums declare: a bare
name was a variant when it resolved to one, resolution took the **first** declaration, and
`c := Red` was a coin toss decided by declaration order. Neither half of that survives. A
variant is named through its enum ([Grammar](../surface/grammar.md)), so `Red` alone is
_E383 `Red` is a variant of `Colour`, and a variant is named through its enum_ in either
enum; and a qualified `Signal.Red` is resolved **inside the enum it names**, so the two
declarations are two different variants that never compete. The rule is gone from the
linter rather than left running, which is what its own case in `lint-check` would otherwise
have gone on asserting.

`mut fn` is not a hint: it makes the receiver a `mut &`, so **every** call site has to hold
the instance in a `mut` binding. A method that only reads charges its callers that and gives
nothing back — and they cannot see why, because the signature is the whole contract and
`mut fn` is all of it. The test is a **write** to `this`, not a mention of it.

## `L5xx` — conversion

| Code   | Rule                                          |
| ------ | --------------------------------------------- |
| `L502` | a **literal** took a type that is not its own |

An adoption away from a literal's default is a finding
([Types](../core/types.md#into--an-ordinary-conversion-spec)) — so `1.5 + 1` is reported and
`1.5 + 1.0` is not. It is advisory: adoption is legal, and the page should show it — `1` and
`1.0` should be different types to a **reader**, not only to the compiler.

`L501` stood beside it and has **retired**. It reported a value that converted at a position
— `f: float = i` — which was legal, one step, and invisible. A position wraps a value and
never converts one ([Type System](../core/type-system.md)), so its whole subject is a refusal
now, and a lint whose programs the compiler rejects reports nothing on any program it is
given. The number is not reused: a reader who meets `L501` in an old log should find what it
was and why it went.

This one is the only rule the linter does not answer from the parsed tree. A literal's adopted
type is a fact about **types**, so the lowering walk records it and `zerg lint` asks the walk —
the C it produces is thrown away. A program that does not compile reports none of them, which
is right: there is nothing to advise about the types of a program whose types are wrong.

## `L6xx` — what the binary carries

| Code   | Rule                                                                 |
| ------ | -------------------------------------------------------------------- |
| `L601` | a `#[test]` or `#[fixture]` outside a `*_test.zg` file — **warning** |
| `L602` | an `assert` outside a `*_test.zg` file — **warning**                 |

Such a function is **legal** and it **ships**: it is compiled into the binary like any other,
it appears twice in the emitted C, nothing calls it, and its `import "testing"` travels with
it — which is how a test-only dependency reaches a shipped program. So the message states that
**consequence** rather than the preference. A warning that only says "move it" is style advice
and gets scrolled past; what the reader has to weigh is dead code in the artifact.

`#[allow(L601)]` silences it for a `#[test]` that is meant to ship. One decorator per item, so
the two are written as one: `#[allow(L601), test]`.

`L602` is the same argument about a **live** claim rather than dead weight. `assert` is always
compiled in — there is no flag that strips it, deliberately, because a program with its
assertions and the same program without them are two programs, and nobody writes anything
load-bearing into a check that may not run. So an `assert` outside a test file is a check that
ships and can abort a running process, and the message names the **replacement**, which is what
makes the warning earned: `assert` in production is not _weaker_ than the check somebody meant
to write, it is _less specific_ — it says the claim was false, and never what it meant.
`raise ValueError("xs must be non-empty") if xs.len() == 0` says both.

`#[allow(L602)]` silences it, on a `fn` for the whole body or on a single statement for that
statement. Move the code into a `*_test.zg` file afterwards and `L106` tells you to drop the
allow, which is what keeps the suppression from outliving its reason.

## Adding a rule

A new LINT rule needs a program in `scripts/lint-check.sh` that makes it fire, for the reason
`make lint` cannot supply one: it runs over the compiler, the stdlib and the test suites, which
are clean, so a rule that stopped working looks exactly like a rule with nothing to say. That
script also fails when a code documented in `lint.zg` has no case, so the pairing is checked
rather than remembered.

A rule that judges a declaration by its uses owes a **second** case there: a well formed program
it must stay **silent** on. `L101` called an import unused when the only thing reaching it was a
type, and `L102` called a private function dead when the only thing calling it was a
module-level `const` — and every positive case stayed green through both.

Give it the next number in the `L` group its EFFECT belongs to, add it to the table in
[`src/compiler/zerg/lint.zg`](../../src/compiler/zerg/lint.zg), and add it here.
