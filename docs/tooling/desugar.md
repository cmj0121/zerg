# Zerg Desugar Rules

Every rule `zerg desugar` applies, each with the code that names it. Part of the
[Language Reference](../language.md). Also in [繁體中文](desugar.zh-TW.md).

`zerg desugar` rewrites a source into the **core forms its sugar is defined as**. It is the other
direction from [`zerg fmt`](fmt.md), which prefers the shorter surface wherever the language offers
one: `F401` turns `if c { return x }` into `return x if c`, and `D101` turns it back.

```sh
zerg desugar <file.zg>...              # rewrite in place; prints the files it changed
zerg desugar --check <file.zg>...      # report what is not already core, change nothing
zerg desugar --off D103 <file.zg>...   # leave one rule alone (repeatable)
```

> **[deviation]** `--check` answers a wider question than it asks. It compares the file against what
> `zerg desugar` would **write**, and what that writes is canonical-formatted core — so a file holding no
> sugar at all still fails when its whitespace is not what `zerg fmt` would produce, and it fails saying
> _still holds sugar (run `zerg desugar`)_. A four-space-indented `fn main() { x := 1; print x }` is the
> whole reproduction. The exit status is right for "this file would change" and the sentence is wrong
> about why.

## Why it exists

[`GRAMMAR`](../../GRAMMAR) defines several surface forms **as** something else. `return x if c` is
`if c { return x }`. `for c { … }` is `for { if not (c) { break } … }`. `for i in a..b { … }` is that
with a counter. Each of those definitions is a claim that two programs are the same program — and
nothing in this tree was checking any of them.

They are not checked by accident, either. The compiler lowers each surface form **directly**:
`c_return_if` emits the conditional return, `c_forrange` emits the counted loop. So the core form the
sugar is _defined as_ goes down a **different path in the emitter**, and the two paths meet nowhere.
A step a `continue` jumps over, a bound evaluated twice, a teardown registered on one path and not
the other — every one of those compiles, runs, and prints the wrong answer.

`make desugar` is the check: desugar a copy of every program in the corpus, build both, run both,
compare what they print and what they exit with. The corpus is the input, so the gate grows when a
case is added rather than when somebody remembers to extend it.

**The desugared source is an artifact, not a canonical source.** Running `zerg fmt` over it puts
every sugar back, which is correct rather than a conflict: canonical means sugared, and that is why
this is a command of its own rather than a `--desugar` mode on `fmt`.

## The rules

| Code   | Rule                                                     | Same C |
| ------ | -------------------------------------------------------- | ------ |
| `D101` | a postfix guard becomes the `if` block it is sugar for   | yes    |
| `D102` | a while-`for` becomes the infinite `for` it is sugar for | no     |
| `D103` | a range-`for` becomes the infinite `for` it is sugar for | no     |
| `D104` | an `assert` becomes the guarded raise it is sugar for    | yes    |

**Same C** is a real distinction and the gate measures it. `D101` produces a program whose emitted C
is **byte-identical** to the sugar's, because four of the five postfix guards are desugared in the
**parser** and the fifth (`c_return_if`) emits the same `if` block. `D104` is byte-identical for the
same reason one step further on: `assert` is desugared in the parser too, and this rule writes out
exactly the statements it builds. `D102` and `D103` produce a `for (;;)` where the sugar produced a
`while` or a counted `for` — the same program, not the same text. So the equivalence this tool
asserts is **behavioural**, and the stronger claim is asserted only for the files it holds for:
`make desugar` asks whether `D101` was the only rule that fired, and compares the C when it was.

**The numbering is not the order.** `D104` runs FIRST, because what it emits is `raise … if c` —
`D101`'s own sugar. Emitted last it would leave this pass with output it rewrites on the next run,
and a rule whose answer depends on how many times it was run is exactly what the fixpoint half of
the gate exists to catch. After that the rules run in numbered order, and `D101` running before
`D103` is load-bearing — see `D103`.

### `D101` — a postfix guard becomes its block

```zerg
fn clamp(n: int) -> int {        fn clamp(n: int) -> int {
    return 0 if n < 0                if n < 0 {
    return 9 if n > 9        →           return 0
                                     }
    return n                         if n > 9 {
}                                        return 9
                                     }
                                     return n
                                 }
```

It covers every **diverge** the postfix `if` attaches to — `return x if c`, the bare `return if c`,
`break if c`, `continue if c`, `raise e if c` — which is the set `GRAMMAR` defines it for.

There is no ambiguity to resolve. `return if …` is **always** the guard: Zerg has no `A if X else B`,
and the conditional expression is the block form with a mandatory `else`, which the parser reads
before it ever looks for one here.

It **declines on a comment anywhere in the statement**, including the one that trails it. A guard is
one line and its block is four, and a note written at the end of that line has nowhere honest to go —
left where it fell it would head the statement _after_ the block instead.

### `D102` — a while-`for` becomes the infinite one

```zerg
mut i := 0                       mut i := 0
for i < 4 {                      for {
    print i              →           if not (i < 4) {
    i = i + 1                            break
}                                    }
                                     print i
                                     i = i + 1
                                 }
```

The condition is **parenthesized** rather than trusted to `not`'s precedence. `not a == b` is not
`not (a == b)`, and a rule that must be right about every condition anyone writes cannot be right by
knowing the precedence table — only by not depending on it.

No fixup is needed for `continue`: a while-loop's `continue` re-tests the condition, and so does this
one, because the test is the first statement in the body.

It reads the three heads apart the way `GRAMMAR` does — a `{` straight after `for` is the infinite
form, `mut` or an `identifier in` is the iterate form, everything else is a condition — so
`for (v in r) { … }` is a while, the parenthesis being exactly what keeps a membership test from
reading as an iteration.

### `D103` — a range-`for` becomes the infinite one

```zerg
for i in 0..3 {                  mut zgd_i7c2 := 0
    print i              →       zgd_hi7c2 := 3
}                                for {
                                     if zgd_i7c2 >= zgd_hi7c2 {
                                         break
                                     }
                                     i := zgd_i7c2
                                     print i
                                     zgd_i7c2 = zgd_i7c2 + 1
                                 }
```

The upper bound is **hoisted and evaluated once**, after the initial value — the two bounds are
computed in the order they are written, which is the order `c_forrange` computes them in and the
order every other operand list in the language is now evaluated in. A bound re-evaluated per
iteration would be a loop that means something else, and `for i in f()..g()` is where both halves
of that show.

The bindings are named for the **line and column** of the `for` they came from, so two loops in one
function cannot collide and the name says where it came from. They are hoisted into the enclosing
scope rather than wrapped in a block of their own because a bare `{ … }` statement is a form this
compiler refuses.

**The inclusive form is not `i <= hi`.** `for i in 0..=MAX` has no value to step to after its last
one: the step overflows, and under this language's [checked arithmetic](../core/types.md) that
**raises** rather than wrapping. The emitter answers with a flag that goes false at the last value
instead of stepping past it, and so does this:

```zerg
for i in 1..=4 { … }     →     zgd_hi3c2 := 4
                               mut zgd_i3c2 := 1
                               mut zgd_done3c2 := zgd_i3c2 > zgd_hi3c2
                               for {
                                   if zgd_done3c2 {
                                       break
                                   }
                                   i := zgd_i3c2
                                   …
                                   if zgd_i3c2 == zgd_hi3c2 {
                                       zgd_done3c2 = true
                                   } else {
                                       zgd_i3c2 = zgd_i3c2 + 1
                                   }
                               }
```

**`continue` is the whole difficulty of this rule.** In the sugared form the step belongs to the loop
header and `continue` runs it; in the core form the step is the last statement of the body, and a
`continue` that jumped over it would leave the induction variable where it was — a loop that never
ends, produced by a tool whose one promise is that it changed nothing. So every `continue` the loop
owns gets the step written in front of it, and a `continue` inside a **nested** loop is that loop's
and is left alone.

That is why `D101` runs first: `continue if c` has to have become `if c { continue }` before this
rule can put two statements where the `continue` was. Where it cannot — a `continue` still carrying
its own guard because `D101` was switched off, or one written as a match arm's body, where two
statements do not fit — the whole loop declines rather than the rule inventing somewhere to put it.

### `D104` — an `assert` becomes its guarded raise

```zerg
assert count(xs) == 3    →    zga_l7c9 := count(xs)
                              if not (zga_l7c9 == 3) {
                                  raise AssertionError(f"a.zg:7  assert count(xs) == 3\n  count(xs) = {zga_l7c9}")
                              }
```

**The message is part of the definition.** A rule that rewrote the test and dropped the message
would be undoing half a sugar — and it is the half the behavioural gate cannot see, because a claim
that holds never renders one. `test-data/desugar/assert_claim.zg` is the case that pins it as text.

The operands are **bound first**, which is the rule the whole form rests on: the message names them,
so rendering them out of the condition a second time would make `assert next(it) == 3` advance the
iterator twice. A literal operand is left where it is (`3 = 3` carries nothing), an `and` becomes
one claim per conjunct, and under `or` / `??` only the first conjunct is bound — no operand is ever
lifted across a short-circuiting operator.

The position it bakes in is the file's **basename**, which is the most a source-to-source transform
can honestly say: the text has to keep meaning the same thing after the file has been copied
somewhere else to be built, and a basename survives that where a path does not.

**Which conjuncts, which operator, which operands** are not decided here. This rule calls the
parser's own analysis over the token stream, because those are facts about the language and a second
scanner is how two spellings of one form come to disagree. What is written here is the other half:
the parser builds a tree, and this builds text.

Like `D101` it declines on a comment anywhere in the statement — the rewrite becomes several
statements, and a comment runs to the end of whichever line it lands on.

## What it declines, and why

A formatter that guessed would be a tool that changes what a program does. Each of these is left
exactly as written:

| Form                        | Why                                                                       |
| --------------------------- | ------------------------------------------------------------------------- |
| `for x in xs`               | needs types — a list, map, str and channel lower four different ways      |
| `for x in f(0..n)`          | the same case with a range inside a call; the `..` is not the head's      |
| `for mut x in …`            | binds the element in place, which the counted form does not do            |
| `for select { … }`          | the fourth head, and not a condition                                      |
| `for i in 0..`              | no upper bound to count to; the compiler refuses it, so this leaves it    |
| a guard carrying a comment  | one line becoming four has nowhere to put a note written at the end of it |
| `lo..=hi =>` (a range arm)  | no rule is written for it; its core form now builds (see below)           |
| `with` / `if x := e` / `?`  | need types, or a core form this compiler does not have yet                |
| `??` / `?.` / `!` / `print` | the same                                                                  |

The general `for x in xs` is the one worth stating twice, because it is the sugar this task started
from. `c_forin` dispatches on the **type** of what is iterated — a range is a counted loop, a channel
is a receive, a map walks keys in insertion order, a str becomes its code points, a list indexes
through the runtime — and a token pass has nothing to tell it which. Rewriting it as an index loop is
right for a list and wrong for a map, so it declines.

The **range arm** is a different kind of decline. `GRAMMAR` says `200..300 =>` is sugar for the guard
it writes `_ if _ in 200..300`, and that guard used to be the reason: `in` over a range was unbuilt, so
the sugar was the only working spelling and a desugaring can only be checked where its core form
exists. **The core form now builds** — `v if v in 200..300 => …` compiles and matches — so what is
left is a rule nobody has written rather than a rule nothing could check. The arm is passed through
unchanged.

`v` and not `_`, which is the part a rule would have to get right: `GRAMMAR`'s `_ if _ in …` is
notation for an arm that does not BIND, and the second `_` is not a name a guard can read
(_E372 undefined name `_`\_). Writing the core form means inventing a binder the source never had,
and one that must not collide with anything the arm's body already names.

## The gates

`make desugar` runs two:

- **`desugar-check`** — every program in `examples/`, `test-data/codegen/` and `test-data/desugar/`
  is desugared as a whole directory (a program is not always one file, and `import` resolves against
  the source's own directory), built both ways, run both ways, and compared on **stdout and exit
  status**. It also asserts the C is identical for every file `D101` alone changed, and that every
  desugared source is its own fixpoint. It carries a floor, because "these two agree" is trivially
  true of an empty list.
- **`desugar-golden`** — each `test-data/desugar/<case>.zg` desugars to exactly the
  `<case>.core.zg` beside it, byte for byte, so a change to what a rule emits arrives in a diff
  rather than in a number. The core file is desugared again and must not move. Finally, every rule
  must have a case that makes it **fire** — asked rather than declared, by switching the rule off
  and seeing whether the output changes.

The behavioural gate cannot see a rule that quietly does nothing, and the golden gate cannot see a
rule that produces a program which reads correctly and behaves differently. Both are needed, and the
declines need the golden one: a wrong `for x in xs` over a list happens to work.
