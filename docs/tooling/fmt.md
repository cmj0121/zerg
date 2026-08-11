# Zerg Formatter & Linter Rules

Every rule `zerg fmt` and `zerg lint` apply, each with the code that names it. Part of the
[Language Reference](../language.md). Also in [繁體中文](fmt.zh-TW.md).

A rule has a **code** so it can be named — in a diagnostic, in a review, on a command line
that turns it off. The prefix groups them the way a Python linter's does, and the grouping
is by **what a rule does**, not by which pass implements it.

| Prefix | Group       | Is                                                 |
| ------ | ----------- | -------------------------------------------------- |
| `F1xx` | layout      | where the line breaks and how far it is indented   |
| `F2xx` | spacing     | where a space goes between two tokens              |
| `F3xx` | trivia      | what happens to what a person wrote for a person   |
| `F4xx` | rewrites    | the rules that MOVE code rather than space it      |
| `L1xx` | dead code   | things written that nothing reaches                |
| `L2xx` | null safety | an optional operator that does not do what it says |
| `L3xx` | capture     | what a coroutine or a deferred call actually took  |
| `L4xx` | resolution  | a name that answers to more than one thing         |
| `L5xx` | conversion  | a literal that took a type the page does not show  |
| `E1xx` | lexical     | text that is not a token                           |

## `zerg fmt`

```sh
zerg fmt <file.zg>...              # rewrite in place; prints the files it changed
zerg fmt --check <file.zg>...      # report what is not canonical, change nothing
zerg fmt --off F401 <file.zg>...   # leave one rule alone (repeatable)
```

The formatter works from **tokens**, not from the AST. That is the whole design decision:
an AST has already thrown away the comments and the blank lines a reader put there, and a
formatter that eats those is one nobody runs twice.

It is **idempotent by construction** — the output is built from the same token stream the
input would produce, so formatting formatted source changes nothing.

`--check` is the question a CI job asks — is this tree already canonical — answered without
touching anything, non-zero when it is not. It exists because the gate that asks it was
otherwise written in shell: copy each file, format the copy, `cmp`. A formatter that cannot
be asked is one every CI reinvents.

**It will not rewrite a file whose brackets do not close.** Reading tokens means fmt will
reformat anything that lexes, and a file with a syntax error came back reformatted and
exit 0 — the tokens intact, but the spacing decided by rules that had nothing true to work
from. The gate is bracket balance rather than a parse, deliberately: a formatter has to
work on source the compiler cannot **compile**, which is exactly when a person reaches for
one. A file that balances and is still ill-formed is formatted as before.

### F1xx — layout

| Code   | Rule                                                                    |
| ------ | ----------------------------------------------------------------------- |
| `F101` | one tab per nesting level; `}` closes at the level it opened            |
| `F102` | one statement per line — the lexer's inserted `;` **is** the line break |
| `F103` | a wrapped expression continues one level in per open bracket            |
| `F104` | a run of blank lines survives as exactly one, inside a group too        |
| `F105` | a group that spans lines closes on its own line                         |
| `F106` | a run of arms lands every `=>` in one column                            |
| `F107` | a run of trailing comments lands every `#` in one column                |

`F105` is what gives a wrapped chain a visible end:

```zerg
a := (
    builder()
    .run()
    .fast()
)
```

rather than a `))` a reader has to count. It applies to any `(` or `[` whose closer is on
a later line than its opener, so a wrapped argument list gets the same shape.

`F103` counts brackets rather than statements, so a nested group steps in one level per
open bracket and each closer lands under its own opener:

```zerg
x := sum(
    sum(
        1,
        2
    ),
    3
)
```

Measuring from the statement instead put every closer of a nest in one column, where a
reader could not tell which closed which — and left an inner closer to the LEFT of the
content it closed.

Parentheses around a multi-line chain are not decoration — they are what makes it parse.
A line break after `)` ends the statement (see the ASI rule in [`GRAMMAR`](../../GRAMMAR)),
and no `;` is inserted inside an unclosed `(`, so the parentheses are what hold the chain
together.

`F106` reads a match over a small closed set as what it is — a **lookup table** — and lays
its arms out as rows:

```zerg
Tok.Eof       => "EOF"
Tok.Illegal   => "ILLEGAL"
Tok.FStrBegin => "FSTR_BEGIN"
```

rather than as a ragged left edge with the answers scattered along it. `F107` is the same
pass over a different mark, because a column of notes beside a column of code is read the
same way:

```zerg
kind: int   # what the lexer decided this is
lexeme: str # the source spelling, kept verbatim
line: int   # 1-based, for a diagnostic
```

A **run** is consecutive lines, at the same indent, that each carry the mark. Everything
else ends it, and the edges are most of the rule:

- a blank line, or a comment on its own line — what follows is a new paragraph, and a
  paragraph brings its own column;
- a **guarded** arm — `n if n > 99 =>` is a sentence rather than a row, so it lines up with
  nothing and ends the run it meets;
- an arm whose body is a **block**, or that puts its body on the next line;
- a head more than **12 columns** wider than the run's narrowest.

That last one is the budget, and it bounds the padding any one line takes: without it a run
creeps wider one arm at a time and a three-character pattern ends up half a line from its
answer. 12 is measured off a `select`, whose arm heads are four different shapes —
`v := <-work`, `<-quit`, `out <- total`, `_` — and 8 cut the `_` arm out of its own table.

The padding is **spaces** while the indent stays **tabs**, so the column holds at any tab
width: the tabs are identical across a run and cancel out. A line may pass the column
`F403` wraps at by as much as the budget, which is the price of the table.

The marks come from the printer rather than from a scan of the finished text, and that is
load-bearing rather than incidental: once the tokens are gone, a `=>` inside a string
literal reads exactly like an arm's, and padding that one would not lay out a table — it
would rewrite the string.

Idempotence is still the printer's. Whitespace between two tokens is not a token, so a
second pass lexes the padded source to the same stream, prints the same single space, and
pads it back to the same column — which is why `F201` reads "at least one space".

### F2xx — spacing

| Code   | Rule                                                                        |
| ------ | --------------------------------------------------------------------------- |
| `F201` | one space around a binary operator, after a comma, at least one around `=>` |
| `F202` | no space after `(` `[`, before `)` `]` `,`, or between a name and its `(`   |
| `F203` | a prefix operator is tight to its operand — `-1`, `&x`, `<-ch`              |
| `F204` | a range operator is tight to both its bounds — `0..n`, `a..=b`              |

`F203` is decided by what is to the LEFT, not by which token it is. `-` is a negation and a
subtraction, `<-` is a receive and a send and a direction marker; each is prefix exactly
when nothing before it could be an operand. Getting one backwards reprints to a STABLE
wrong answer — `-1` became `- 1`, and stayed that way on the second pass — so an
idempotence check cannot see it and only a case containing the shape can.

`F204` is the same lesson learned twice. The range operators are binary, so the default said
yes and `xs[1..3]` came back as `xs[1 .. 3]` — but a range reads as a **constructor** rather
than as an operation, and every range in this tree, in `GRAMMAR` and in these docs is written
tight. It survived because no fmt case contained a range at all.

Two exceptions, both real. A comma keeps the space it owes, so a list pattern's rest marker is
`[h, ..t]`. And a token that cannot begin a bound keeps the space in front of it, because
`20.. =>` printed tight is `20..=>` — which lexes as `20`, `..=`, `>`. The formatter would have
rewritten a working arm into a parse error.

### F3xx — trivia

| Code   | Rule                                                                       |
| ------ | -------------------------------------------------------------------------- |
| `F301` | a comment keeps its text, on its own line or trailing the code it followed |

### Examples in comments

A code example inside a comment is marked with a **doctest prompt** — `>>>` opens a
top-level item, `...` continues it:

```zerg
# Example — greeting someone:
#
# >>> fn main() {
# ...     print greet("world")
# ... }
#
# and it prints:
#
#     hello, world
```

`F301` keeps all of it exactly as written; the prompt is a convention the formatter
reads nothing into. Which prompt a line carries is likewise an authoring convention —
both mean "the rest of this line is Zerg".

The marker is **explicit**, and that is the load-bearing decision. A comment carries two
kinds of indented block: source, and a sample of what the program **prints**. Inferring
from layout alone would highlight the second as if it were the first — in `cli`'s own
header a pasted help screen would light up `Options:` as a field name, `--output` as
operators and `VALUE` as a type. Wrong highlighting is worse than none, so the author says
which is which.

It is a prompt rather than a `fence because comments are to become documentation,
and that generator emits markdown — so` is the **output** syntax. Spelling the input
the same way would leave a generator that must pass one through while producing the other
with no way to tell them apart by looking.

### F4xx — rewrites

| Code   | Rule                                                                           | Default |
| ------ | ------------------------------------------------------------------------------ | ------- |
| `F401` | a one-jump if-block becomes the postfix guard it is sugar for                  | on      |
| `F402` | imports group — standard library first, then the rest — each alphabetical      | on      |
| `F403` | an argument list is one line, or one element per line — never half             | on      |
| `F404` | two or more imports become one parenthesized group                             | on      |
| `F405` | a string built with `+` becomes the f-string it already is                     | on      |
| `F406` | a blank line where one is load-bearing: guard runs, declaration runs, comments | on      |
| `F407` | a discarded receive binder drops — `_ := <-ch => …` is `<-ch => …`             | on      |
| `F408` | an or-pattern over consecutive integers becomes the range it is                | on      |
| `F409` | a bare block that opens with a binding becomes the `with` it is sugar for      | on      |

**`F409`** is the same move on the other sugar `GRAMMAR` defines by expansion: `with e as x { … }` **is**
`{ x := e; … }`, so a bare block whose first statement binds is written as the `with` it already is.

```text
{                        →   with acquire() as h {
    h := acquire()               use(h)
    use(h)                   }
}
```

The trigger is **purely syntactic** — a bare block, a first statement that binds. It must not read the
binding's **`defer`**: `with` carries no teardown of its own, so a rule that folded
`{ x := e; defer x.close(); … }` into one would delete the `defer`.

Both are **token rules**, and `L105` is the reason to say so: `with` expands in the PARSER, so by the
time there is an AST there is no `with` left to lint. Every rule about this form reads tokens for that
reason, the linter's included.

`GRAMMAR` defines `return x if c`, `break if c`, `continue if c` and `raise e if c` **as** sugar
for `if c { … }` around the same jump — one postfix `if`, every **diverge**. So the two forms say the
same thing and one of them says it in four lines. The formatter picks the short one, which
is what a guard clause is for: the exceptional exit stops interrupting the shape of the
code it guards. A bare early exit works the same way — `return if c`.

Preferring the sugar is the general rule, not a special case for `return`: **where the
language offers a shorter surface for exactly what is written, the canonical form is the
shorter one**, and a reader stops having to notice that the two are the same thing.

The rule that reads this one backwards is [`D101`](desugar.md), and the two are each other's
test: a source through both comes back to itself. `zerg desugar` is where the rest of that
argument lives — the sugared and the core form must build the same program, and `make desugar`
is what asks.

A jump that ALREADY carries its own guard keeps its block. There is no single `if` that says
what `if m { return 0 if n < 0 }` says, and writing `return 0 if n < 0 if m` would be source
no compiler parses — so the rule declines rather than inventing one.

Note what this postfix `if` is NOT. It attaches to a jump, not to an expression — Zerg has
no `A if X else B`. The conditional EXPRESSION is the block form, with a mandatory `else`:
`x := if c { 1 } else { 2 }`.

```zerg
fn clamp(n: int) -> int {        # before
    if n < 0 {
        return 0
    }
    if n > 9 {
        return 9
    }
    return n
}

fn clamp(n: int) -> int {        # after
    return 0 if n < 0
    return 9 if n > 9

    return n
}
```

It rewrites only what it can rewrite **without losing anything**, and declines otherwise:

- exactly one statement in the block, and it is a jump;
- no `else` — an else has a second branch the guard form cannot carry;
- no comment anywhere inside, because a comment is something a person put there and this
  pass has nowhere to put it back.

`F402` is the Go convention, and for the same reason: an import list is read far more
often than it is edited, and one that is grouped and sorted answers "does this file use X"
by looking rather than by scanning. The standard library goes first because it is the part
a reader already knows; everything else is what this program brought.

What counts as standard library is the fixed bundled set. A module resolves by its LAST
path segment, so `import "std/io"` groups with `import "io"`.

It rewrites only a run of plain `import "path"` statements and declines the moment
anything else appears among them — a comment, which belongs to the import it sits on and
would be stranded by sorting, or an `import pub`, whose re-export is an ordering the
author chose.

`F404` then folds the run into the parenthesized form `GRAMMAR` gives `import`, one spec
per line, with `F402`'s blank line surviving as the separator inside it:

```zerg
import "cli"          import (
import "zerg"    →        "cli"
import "io"               "io"

                          "zerg"
                      )
```

The ordering there is `F402`'s and the parentheses are `F404`'s. Both are shown together
because that is what `zerg fmt` prints — the state in between is one nobody ever sees.

**One import is left as it is.** That is gofmt's answer and for gofmt's reason: the group
holds a list together, and a list of one is the statement itself with two lines of
ceremony around it.

Unlike `F402`, `F404` takes `pub` and `as` with it — those are what `F402` declines over
because it **reorders**, and this rule moves nothing past anything:

```zerg
import (
    "io"
    pub "util/text"
    "a/text" as at
)
```

It declines on a comment between the imports, for `F401`'s reason, and leaves a run that
is already grouped alone. A comment **after** the run belongs to the next declaration, so
it ends the run rather than declining it.

Writing this form is also what found that the self-hosted parser could not read it back:
the group was consumed and yielded no path at all, so a file importing through one handed
the driver an empty import list — and the failure surfaced stages later as an unknown
type name rather than at the import that was never read.

`F105` says where a closer goes once a group already spans lines. `F403` says what shape
a group that spans them takes. **A line its author fitted on one line stays on one line** —
this rule never breaks up a group that was not already broken.

A group its author DID break is joined back onto one line unless one of these vetoes it:

- printed flat, it would end at or past column 80 — a tab counts as 4;
- it holds 6 or more top-level elements.

When one does, the group breaks at **every** top-level comma instead. Never half of each:
a group broken around one of its elements used to print the rest after the closer, leaving
a `), 3` hanging off the end, which is neither shape.

```zerg
x := sum(                        # before
    1,
    2,
)
y := sum(sum(
    1,
    2,
), 3)

x := sum(1, 2)                   # after
y := sum(sum(1, 2), 3)
```

Width is the real judgement and the count is the backstop for what width cannot see: six
arguments read as a list to scan rather than a line to read, however short each one is.

Neither threshold **orders** a break, and that is deliberate. The pass sees one group at a
time rather than the whole line, so the group that crosses column 80 is the last one on
the line rather than the one worth breaking — in `return 0 if a or b or c(x, y)` it is
`c`, whose two arguments would go on three lines while the condition that actually made
the line long, and that has no brackets to break at, stayed as it was.

There is one thing that DOES order a break: a group that **holds a block**. `f(match n { … })`
has no one-line form to choose — the `{` of a block ends its line, which is the printer's
rule and not this one's — so measuring it flat produced a shape that was neither joined nor
split, and left `F105` to move the closer on a LATER run. That made `zerg fmt` non-idempotent on
the one input nothing in this tree writes: a block inside a call. A group holding one spans
lines however it was written, and is split at every top-level comma.

**A `guard` block the author wrote on one line is not one of them.** `guard { e } ?? d` is an
expression inside a line, and `check("max + 1 raises", guard { max + 1 } ?? 7, 7)` reads as the
line it is — exploded it costs seven, and one example carried eighteen. So the printer's block
rule is not applied to it: no break after the `{`, no line of its own for the `}`, no level in
between, and the group around it is measured like any other. The exemption is earned per guard,
not granted to the keyword — every token through the `}` must sit on the author's line, and none
may be a comment (it runs to end of line) or a `;` (a second statement is a block again). A guard
whose block its author broke keeps the block shape, breaks and all: this rule never **joins**, it
only declines to break.

A group with **no top-level comma** is exempt from both thresholds. A chain and a parenthesised
expression break where their author broke them — those breaks say where the steps are,
not that the line ran out of room. What they get is the opener ending its own line, so
every step starts in the same column:

```zerg
n := (
    builder()
    .run()
)
```

Like `F401`, it declines outright when a comment is anywhere inside: a comment is
something a person put there and joining lines has nowhere to put it back.

The **trailing comma goes** either way, joined or split. On one line it is a comma before
a closer that nothing follows; on several, a multi-line parameter list is the one place
the grammar does not accept one, and dropping it in a signature while keeping it in a call
would be one shape in two spellings.

`F405` is `F401`'s principle applied to strings: where the language offers a shorter
surface for exactly what is written, the canonical form is the shorter one.

```zerg
"n=" + s + " of " + t                    →   f"n={s} of {t}"
"v=" + strconv.to_string(n, 10) + "!"    →   f"v={n}!"
```

The formatter has no types and does not need them here. `+` is never heterogeneous — the
emitter lowers it to a string concat exactly when the left operand is a `str` — so **one
string literal anywhere in a chain types the whole of it**. That is the trigger.

`strconv.to_string(X, 10)` narrows to `{X}`, since base 10 is what a hole renders in
anyway. Every other base keeps its call: a hole has no spelling for one.

The trap this rule is mostly built around is **precedence**. `+` shares its level with
`-`, `|`, `^`, `+%` and `-%`, so `"a" + b - c` parses as `("a" + b) - c` and the `+` chain
is _not_ the whole token run — rewriting the run would re-associate the expression. What
may sit between operands is therefore a whitelist, and any of those five at the chain's own
depth ends it.

It declines on: fewer than three operands; no literal; no hole; a chain spanning more than
one line; a raw, triple-quoted or `\u{…}` literal; a hole carrying `"`, `\`, `{` or `}`;
a literal carrying a brace, which is correct doubled and reads worse than what it replaces;
and the accumulation `out = out + …`, where a hole would make the accumulator
indistinguishable from the values being interpolated.

`F406` writes a blank line, and the bar for doing that is deliberately high: this whole
tree has **ten** blank lines inside function bodies, and `fmt.zg` — 1300 lines of it — has
none. So the rule puts one in exactly two places, both of them places the authors here
already put one.

A run of **more than one** guard is followed by a blank:

```zerg
fn conv_ty(s: str) -> Ty {        fn conv_ty(s: str) -> Ty {
    return TInt if s == "int"         return TInt if s == "int"
    return TStr if s == "str"    →    return TStr if s == "str"
    return TNil
}                                     return TNil
                                  }
```

One guard is part of the line it guards. Two or more are a **table of exits**, and a table
wants an edge — both of them, so a run that starts mid-function gets a blank in front as
well. At the top of a body the `{` is already that edge. That restraint is most of the
rule: 182 of this tree's 218 guard runs are a single guard and none of them is touched.

A **run of three or more declarations** starting mid-block gets a blank in front of it, and
nothing after it:

```zerg
fn c_index(mut &em: Emitter, target: Expr, idx: Expr, sb: Subst) -> str {
    em.used_rt = true

    ct := c_type(c_list_elem(c_infer(em, target)))
    tgt := c_expr(em, target, sb)
    ix := c_expr(em, idx, sb)
```

The asymmetry with a guard run is structural. A guard run is **terminal** — nothing after
it consumes it, which is why it reads as a table and wants both edges. A declaration run is
a **preamble**: of this tree's 167 runs, 106 are followed by a `for` that references every
name they declare, and 86 of those are the shape where the last declaration is the
induction variable of the very next line. A blank there would split `i = 0` from `i < n`.
It has no far edge because it has not finished being read.

Three, not two — two declarations are a pair, not a paragraph, the same step this rule
takes when it says one guard is part of the line it guards.

A comment on its **own line**, with code before and after it, gets a blank in front. A
comment there heads a new chunk — that is what makes it its own line rather than a trailing
one — and half the blank lines already in this tree's bodies are that shape. It applies in
any block, so a `struct`'s commented field group gets the same separation Go's does.

It declines in five cases: a comment ahead of a guard run, which is that run's **heading**
— the blank goes in front of the comment, not between it and the table it introduces; a
comment at the top level, which heads a declaration whose spacing is the author's; a
comment inside a wrapped argument list, which belongs to the element it sits on and would
be separated from it; a comment with only the closing `}` after it, which heads nothing;
and a blank that is already there.

What it deliberately does **not** do is put a blank after a nested block's `}`. That is 683
places in this tree — not a rule about readability but a rewrite of the tree.

`F407` drops the binder from a select arm that discards what it receives:

```zerg
_ := <-cancel => { … }    →    <-cancel => { … }
```

`GRAMMAR` makes a recv-arm's binder **optional** — `<-ch => stmt` is the whole form — so
`_ := <-ch =>` names the received value only in order to say it has no name. Go needs `_ =`
because an unused variable is an error there; Zerg has no such rule, so the binder buys
nothing and costs the next reader the question "where is `_` read?". This is `F401`'s
principle again: where the language offers a shorter surface for exactly what is written,
the canonical form is the shorter one.

It fires only where an `=>` proves a select arm. A **statement** `_ := <-ch` is a binding,
and dropping its binder would leave a bare receive standing where a statement was; that case
is `L104`'s, which suggests rather than rewrites.

`F408` is that principle a third time, over a match arm's pattern:

```zerg
4 | 5 | 6 | 7 => "mid"    →    4..=7 => "mid"
```

`GRAMMAR` gives an arm a **range** form that is sugar for the guard `_ if _ in 4..=7`, so it
says exactly what the or-pattern says and says it once however many values there are.

Here the shorter form is also the only one that **builds**. `|` in pattern position is read as
the bitwise operator, so `1 | 2` would fold to `3` and match neither 1 nor 2 — which is why the
compiler now refuses an or-pattern outright rather than emitting it. This rule is what turns
that refusal into working code for the one case that has a working spelling; every other
or-pattern waits on the language work.

That order matters for what a formatter is allowed to be. `zerg fmt` does not change what a
working program does here — the program it rewrites does not compile.

The shape it accepts is narrow, and each part of it is load-bearing:

- the whole arm **head** is integer literals separated by `|`, and nothing else. A token pass
  has no other way to know a `|` is in pattern position, and `out <- a | b => …` is a select
  send arm whose value happens to hold one;
- the values **ascend by one** — `1 | 3` is not `1..=3`, which would take 2 with it;
- two or more alternatives, and no guard.

Each bound keeps its own **lexeme**, so `0x10 | 0x11` becomes `0x10..=0x11` rather than being
restated in decimal. A literal's surface is the author's.

### Which rules can be switched off

`F1xx`–`F3xx` are not negotiable: they are what "canonical" means, and a formatter with
options for them is a formatter two people configure differently. `F4xx` changes the
code's **shape** rather than its spacing, so it is the group `--off` exists for. `F105`
sits in layout rather than in rewrites because it only decides where an existing token
goes; `F403` is a rewrite for the same reason in reverse — it inserts line breaks nobody
wrote and drops a token a joined list no longer needs, and `F406` writes a line nobody
wrote at all.

## E1xx–E5xx — compile errors

These are not advisory. A program that hits one does not build, so each is a **compile
error** the build stops on. They carry codes because a code is a **stable identity for a
rule** where a sentence is not: prose gets better, and a gate that pins the sentence turns
red when it does. The codes group by the **stage** that reports them, which is also the
order a build meets them:

| Range  | Stage    | What it reports                                                    |
| ------ | -------- | ------------------------------------------------------------------ |
| `E1xx` | lexical  | text that is not Zerg tokens                                       |
| `E2xx` | parser   | tokens that are not a Zerg form                                    |
| `E3xx` | checking | a form whose meaning does not hold together                        |
| `E4xx` | emitting | a form this compiler will not lower, including a `[not yet]`       |
| `E5xx` | building | the program as a set of files, which no single file's text answers |

`E5xx` is the one range that is not a point in that order. A build resolves imports before
it lexes what they name and looks for `fn main` after everything is emitted, so the driver's
own findings bracket the other four rather than sitting between two of them.

A code sits at the **front of the message**, before the sentence: `E109 invalid escape in a
rune literal`. Where a diagnostic carries a place, the renderer's `error:` opens the line
ahead of it (`error: E109 …`); a refusal that has not learned its place yet prints the
message alone, so the code is the first thing on the line either way.

**A code exists when a gate pins it, and not before.** `scripts/refuse-check.sh` and
`scripts/reject-check.sh` assert the code rather than the sentence, and a `zerg` case that
pins prose instead is a failure by name — otherwise a list that is mostly codes with a few
sentences left in it looks finished from the outside. `scripts/error-codes-check.sh` holds
the three lists to each other: what the compiler reports, what a gate pins, and what this
table lists. Asking it that question is what found
**thirteen rules no case had ever made fire**; they are the last section of
`reject-check.sh`.

A reject case keeps a **sentence** as well only where several cases share a code, since what
each one then proves is which values the rule named. The seed keeps sentence matching
throughout: codes are the language's contract, and the seed is the tool that builds the
shipping compiler rather than a part of it (the line
[Conformance](../conformance.md) draws when it declines to mark the seed's gaps).

| Code   | Rule                                                                                        |
| ------ | ------------------------------------------------------------------------------------------- |
| `E101` | a string literal is not closed before the end of the line                                   |
| `E102` | a rune literal is empty                                                                     |
| `E103` | a rune literal holds exactly one character, and this holds more                             |
| `E104` | this character is not part of any Zerg token                                                |
| `E105` | a triple-quoted string is never closed                                                      |
| `E106` | a raw string has no closing quote on this line                                              |
| `E107` | a command literal has no closing backtick                                                   |
| `E108` | a based number needs at least one digit after its prefix                                    |
| `E109` | invalid escape in a … literal                                                               |
| `E110` | a string literal may not contain a NUL                                                      |
| `E201` | `close` is not a select arm head                                                            |
| `E202` | a select needs at least one arm                                                             |
| `E203` | `…` is not a select arm head                                                                |
| `E204` | expected `…`, found `…`                                                                     |
| `E205` | expected a newline or `;` to separate statements, found `…`                                 |
| `E206` | `Either[…, …]` has the same type on both sides                                              |
| `E207` | a parameterized `…[…]` as … — **[not yet]**                                                 |
| `E208` | `#[derive(…)]` has no declaration under it                                                  |
| `E209` | a closure parameter without a type — **[not yet]**                                          |
| `E210` | a `spec` member with a BODY — **[not yet]**                                                 |
| `E211` | an associated value in a `spec` — **[not yet]**                                             |
| `E212` | a generic enum `…[…]` — **[not yet]**                                                       |
| `E213` | an enum discriminant is distinct across variants, and `… = …` repeats one already given     |
| `E214` | a discriminant `… = …` on an enum whose variants carry a payload — its tag is opaque        |
| `E215` | a generic struct `…[…]` — **[not yet]**                                                     |
| `E216` | a default on field `…` — **[not yet]**                                                      |
| `E217` | the decorator `#[…]` — **[not yet]**                                                        |
| `E218` | an associated value binding `… := …` in an `impl` — **[not yet]**                           |
| `E219` | `…` as an `impl` item — **[not yet]**                                                       |
| `E221` | a struct pattern `…{…}` — **[not yet]**                                                     |
| `E222` | calling … — **[not yet]**                                                                   |
| `E223` | the named argument `…:` — **[not yet]**                                                     |
| `E224` | `unsafe { … }` as an EXPRESSION — **[not yet]**                                             |
| `E225` | an f-string ':spec' format spec — **[not yet]**                                             |
| `E226` | an f-string '!r' / '!s' / '!a' conversion — **[not yet]**                                   |
| `E227` | the f-string '{expr=}' self-documenting form — **[not yet]**                                |
| `E230` | an associated type in a `spec` — **[not yet]**                                              |
| `E231` | an associated type binding `type … = …` in an `impl` — **[not yet]**                        |
| `E232` | a tuple pattern in a `match` arm — **[not yet]**                                            |
| `E233` | an array type `[T; N]` — **[not yet]**                                                      |
| `E234` | an `as` binding in a `match` arm — **[not yet]**                                            |
| `E235` | an interpolating command literal — **[not yet]**                                            |
| `E236` | a command literal — **[not yet]**                                                           |
| `E238` | a destructuring binding `(a, b) := …` — **[not yet]**                                       |
| `E239` | a range with no lower bound — **[not yet]**                                                 |
| `E240` | a list pattern in a `match` arm — **[not yet]**                                             |
| `E241` | an or-pattern — **[not yet]**                                                               |
| `E242` | `for mut v in …` — **[not yet]**                                                            |
| `E243` | a struct pattern `…{…}` in a `match` arm — **[not yet]**                                    |
| `E244` | this program nests more than … levels deep                                                  |
| `E245` | `…` is a reserved word and cannot name …                                                    |
| `E246` | a tuple type has two elements or more                                                       |
| `E247` | `pub import` is not a form                                                                  |
| `E248` | `pub` does not go on `init()`                                                               |
| `E249` | `pub` does not go on an `impl` block                                                        |
| `E250` | a decorator leads its declaration and `pub` sits inside it: write `#[…] pub fn …`, not …    |
| `E251` | a free function is never `mut fn`                                                           |
| `E252` | `pub` does not go on an `unsafe { … }` group                                                |
| `E253` | a module-level `unsafe { … }` group does not nest                                           |
| `E254` | a module-level `unsafe { … }` group holds declarations                                      |
| `E255` | `pub` binds to a declaration, and a statement takes none                                    |
| `E256` | this module-level `unsafe { … }` group is never closed                                      |
| `E257` | `…` is a reserved word and cannot name a binding                                            |
| `E258` | `…(…)` converts a VALUE and was given none                                                  |
| `E259` | `…(…)` converts one value, and this gives …                                                 |
| `E260` | `list[T](…)` converts a VALUE and was given none                                            |
| `E261` | `list[T](…)` converts one value, and this gives …                                           |
| `E262` | a match arm's guard goes before the `=>`                                                    |
| `E263` | a parameter is `mut &` or nothing                                                           |
| `E264` | a standalone `unsafe fn` declaration — **[not yet]**                                        |
| `E265` | an associated type projection `….…` — **[not yet]**                                         |
| `E266` | a value generic parameter `…: …` — **[not yet]**                                            |
| `E267` | an import path is a string                                                                  |
| `E268` | `…[…]` with no call after it — **[not yet]**                                                |
| `E269` | an `if` EXPRESSION whose branch has more than one statement — **[not yet]**                 |
| `E270` | a binding head in an `if` EXPRESSION — **[not yet]**                                        |
| `E271` | `asm(…)` — **[not yet]**                                                                    |
| `E272` | `…(…)` converts a VALUE and was given none                                                  |
| `E273` | `…(…)` converts one value, and this gives …                                                 |
| `E274` | a pattern names a variant through its enum, and this one is bare                            |
| `E301` | `…` is not a public member of module `…`                                                    |
| `E302` | `…` is not a place, and an assignment needs one                                             |
| `E303` | cannot assign to `…`: it is a module `const`, and a constant is never written               |
| `E304` | `type … = …` over a non-scalar — **[not yet]**                                              |
| `E305` | cannot assign to `…`: it is a module binding, and the top level is immutable                |
| `E306` | cannot assign to `this`: a method's receiver is a copy, and the form that writes through …  |
| `E307` | cannot assign to `…`: it is immutable                                                       |
| `E308` | cannot assign through `…`: it is immutable                                                  |
| `E309` | parameter `…` of `…` is a `mut &` and cannot have a default                                 |
| `E310` | the default for `…` of `…` is …, and the parameter is …                                     |
| `E311` | `…` carries … and this … …                                                                  |
| `E312` | argument … of `…` is a `mut &` and cannot cross a `…`: a borrow may not be captured, and …  |
| `E313` | cannot store through …                                                                      |
| `E314` | no spec named `…`                                                                           |
| `E315` | `…` is parameterized by …, and this `impl` gives … type argument(s)                         |
| `E316` | `…` extends `…`, and nothing in this program declares a spec by that name                   |
| `E317` | `….…` does not match what `…` requires: …                                                   |
| `E318` | `…` does not implement `…`, which `…` requires                                              |
| `E319` | the integer literal `…` does not fit an `int`                                               |
| `E320` | a `str` is not indexable                                                                    |
| `E321` | an `if` expression answers ONE type, and its branches give … and …                          |
| `E322` | a `match` answers ONE type, and its arms give … and …                                       |
| `E323` | … borrows …, which is a value rather than a place                                           |
| `E324` | … writes back to `this`, and the enclosing method holds its receiver by value               |
| `E325` | … writes back to `…`, which is not `mut`                                                    |
| `E326` | `…` is given to two `mut &` parameters of `…` in one call                                   |
| `E327` | `…` takes … and this gives …                                                                |
| `E328` | `…` needs … and this gives …                                                                |
| `E329` | element … of this list literal is …, and this gives …                                       |
| `E330` | `…` is not a value a … holds                                                                |
| `E331` | this divides by a constant `0`                                                              |
| `E332` | this expression's value is past what an `int` holds, so it cannot be measured against …     |
| `E333` | this function's answer is …, and this gives …                                               |
| `E334` | the binding `…` is annotated `…`, and no type of that name is declared in this program      |
| `E335` | cannot bind … to a … binding: `…`                                                           |
| `E336` | the binding `…` gives …, which has no type of its own                                       |
| `E337` | `type … = …` names no type                                                                  |
| `E338` | a struct field or an enum payload is …, and this gives …                                    |
| `E339` | cannot assign … to …, which holds …                                                         |
| `E340` | argument … of `…` is …, and this gives …                                                    |
| `E341` | an optional is not an operand of `…`                                                        |
| `E342` | operator `…` has no meaning on … and …                                                      |
| `E343` | operator `…` takes bool operands, and these are … and …                                     |
| `E344` | operator `…` takes int operands, and these are … and …                                      |
| `E345` | operator `…` takes numeric operands, and these are … and …                                  |
| `E346` | operator `…` orders two numbers or two strs, and these are … and …                          |
| `E347` | cannot compare a variant with a number — a variant is a value of ITS enum                   |
| `E348` | cannot compare … and … — they are different kinds of value                                  |
| `E349` | operator `…` has no meaning on …                                                            |
| `E350` | operator `not` takes a bool operand, and this one is …                                      |
| `E351` | operator `-` takes a numeric operand, and this one is …                                     |
| `E352` | operator `~` takes an int operand, and this one is …                                        |
| `E353` | operator `…` has … on one side and … on the other, and an operator's operands must …        |
| `E354` | the condition of … is an optional, and a condition is bool — bind it with `if v := x { … }` |
| `E355` | the condition of … must be bool, and Zerg has no truthiness                                 |
| `E356` | `…` re-binds a `const`                                                                      |
| `E357` | `const …` shadows a binding already visible here                                            |
| `E358` | the top-level binding `…` may not be `mut` outside a module-level                           |
| `E359` | `….…()` renders the value as text, so it takes no arguments beyond the value                |
| `E360` | `….…()` renders the value as text, so it is a plain `fn` and not a `mut fn`                 |
| `E361` | `….…()` answers the `str` the value shows as                                                |
| `E362` | `…` is declared twice, once as a generic                                                    |
| `E363` | `…` is declared both as a generic and as a plain function                                   |
| `E364` | `This` is the self type, and … is outside an `impl`                                         |
| `E365` | `…` declares a parameter named `…` twice                                                    |
| `E366` | `…(…)` converts ONE value                                                                   |
| `E367` | `…(…)` does not parse a `str`                                                               |
| `E368` | `…` is not generic, so `…[…]` has no type argument to take                                  |
| `E369` | `…` holds an …, and an … is not callable                                                    |
| `E370` | `…` needs a value for … (…): only a `T?` field has an implicit default, and it is `nil`     |
| `E371` | `this` is a method's receiver, and this function has none                                   |
| `E372` | undefined name `…`                                                                          |
| `E373` | `…` is declared as both a module constant and a function                                    |
| `E374` | a slice bound is an int, and this is …                                                      |
| `E375` | a list index is an int, and this is …                                                       |
| `E376` | no field `…` on …                                                                           |
| `E377` | `.…` reads a tuple element, and … is not a tuple                                            |
| `E378` | a tuple of … has no `.…`                                                                    |
| `E379` | `for … in` walks a list, a map, a str, a range or a channel, and … is not iterable          |
| `E380` | raise carries an `Err`, or a message to build one from                                      |
| `E381` | `…` is declared twice, once as one kind of declaration and once as another                  |
| `E382` | `…` is declared twice as the same kind — every module flattens into one namespace           |
| `E383` | a variant is named through its enum, and this one is bare                                   |
| `E384` | a side of an `Either` is named through its type, and this one is bare                       |
| `E401` | `…` outside of a loop: it belongs to a `for`, and a `select` arm is not one                 |
| `E402` | a `from` cause is an `Err`, and … is not one                                                |
| `E403` | `…` leaving a `guard` block — **[not yet]**                                                 |
| `E404` | a channel of optionals is refused                                                           |
| `E405` | `…(…)` names one side of an `Either`, which holds exactly one value                         |
| `E406` | `?.` reads through an optional, and … is not one                                            |
| `E407` | `int(v)` reads the discriminant, and enum `…` carries a payload, so its tag is opaque       |
| `E408` | `?` early-returns the RIGHT of …, so the enclosing function must answer a carrier with …    |
| `E409` | a generic METHOD `….…[…]` — **[not yet]**                                                   |
| `E410` | `…` has been instantiated … times and is still asking for more                              |
| `E411` | the type parameter `…` of `…` is not decided by this call                                   |
| `E412` | `…` does not implement `…`, which `…`'s type parameter `…` is bounded by                    |
| `E413` | the raw-pointer built-in `…` — **[not yet]**                                                |
| `E414` | the compile-time built-in `…[T]` — **[not yet]**                                            |
| `E415` | an `impl` on the built-in type `…` — **[not yet]**                                          |
| `E416` | the `spec` `…` used as a TYPE (…) — **[not yet]**                                           |
| `E417` | `str(…)` over a list bridges bytes or code points, and this is …                            |
| `E418` | `…(…)` converts a value, and … may not have one                                             |
| `E419` | an enum converts to `int`                                                                   |
| `E420` | `….of(n)` reverses the discriminant, and enum `…` carries a payload, so its tag is opaque   |
| `E421` | `[…]` indexes a value, and … may not have one                                               |
| `E422` | `…` MUTATES its list, and `…` is a value rather than a place — **[not yet]**                |
| `E423` | an open-ended range has no upper bound here — **[not yet]**                                 |
| `E424` | `….…(…)` is an associated function — **[not yet]**                                          |
| `E425` | undefined function `…`                                                                      |
| `E426` | `…` has … fields and this gives …                                                           |
| `E427` | variant pattern `…` cannot match a subject of type …                                        |
| `E428` | non-exhaustive match: missing variant ….…                                                   |
| `E429` | a closure capturing `…` — **[not yet]**                                                     |
| `E430` | `…` on a … needs an `Eq` — there is no structural equality by default                       |
| `E431` | a map key of type … — **[not yet]**                                                         |
| `E432` | `…` is declared … and the value is …: unwrap it with `?? …`, `!` or `if … := …`             |
| `E433` | `print` needs a value, and … may not have one                                               |
| `E434` | `if … := …` over a … — **[not yet]**                                                        |
| `E435` | `…` is declared to answer …, and its body falls off the end                                 |
| `E436` | `#[derive(…)]` — **[not yet]**                                                              |
| `E437` | cannot derive `…`: the derivable specs are compiler-owned, and a `spec` you write is …      |
| `E438` | `#[derive(Eq)]` on `…` — **[not yet]**                                                      |
| `E444` | the list method `…` — **[not yet]**                                                         |
| `E445` | structural equality over a container — **[not yet]**                                        |
| `E446` | a refcounted box `Ref(x)` / `deref(r)` — **[not yet]**                                      |
| `E449` | rendering a … as text — **[not yet]**                                                       |
| `E451` | `…` declares `…` twice                                                                      |
| `E452` | `…` is part of a cycle of by-value declarations                                             |
| `E453` | `…` declares … named `…` twice                                                              |
| `E454` | this expression chains more than … levels deep                                              |
| `E455` | `…(…)` converts a scalar, and … is not one                                                  |
| `E456` | `…` is not a variant of `…`                                                                 |
| `E457` | `…` is a variant of `…`, not of `…`                                                         |
| `E458` | this catch-all arm makes the following arms unreachable                                     |
| `E459` | `…(…)` says which side of an `Either` a value is, so it needs a declared one to be          |
| `E460` | a … is an identity rather than a value, and the language gives it no equality               |
| `E461` | a second `impl Into[…] for …` — **[not yet]**                                               |
| `E462` | `in` over a list whose elements have no `==` — **[not yet]**                                |
| `E463` | `in` over anything but a list, a map or an error kind — **[not yet]**                       |
| `E464` | `into` is a method of the `Into` spec, and no built-in type implements it                   |
| `E465` | `…` is part of the fixed-width ladder — **[not yet]**                                       |
| `E466` | the built-in `set` — **[not yet]**                                                          |
| `E467` | non-exhaustive match: missing a catch-all `_` arm                                           |
| `E468` | a `return` with no value, in a function declared to answer …                                |
| `E469` | … is a `mut &`, and a function VALUE cannot carry one — **[not yet]**                       |
| `E470` | `del …` on a CHANNEL — **[not yet]**                                                        |
| `E471` | `…[…](…)` as a constructor — **[not yet]**                                                  |
| `E472` | `nil` as a `match` pattern — **[not yet]**                                                  |
| `E473` | a … may hold no value, so `…` has nothing to compare                                        |
| `E501` | this entry file declares no `fn main`                                                       |
| `E502` | cannot resolve import `…` under any source root                                             |

They are reported the moment a file is **read**, before its imports are scanned — scanning
them parses, and a parser handed unreadable text can only say something untrue about it.
That is what it used to say: `` `b'b` is not an expression this compiler reads ``, which
names the wrong layer, the wrong problem, and a fragment of what the person wrote.

`E108` had no message at all. `0x` lowered to a C `0x`, which cc read as zero, so a
malformed literal compiled and the program answered 0.

## `zerg lint`

```sh
zerg lint <file.zg>...   # prints findings; exits nonzero when there is one
```

Every check is answered from the parsed file alone — no types, no flow analysis — which
keeps it honest about what it can claim. Findings come back in source order, and a nonzero
exit makes `zerg lint` usable as a gate rather than as decoration.

### L1xx — dead code

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

`L104` is why `L103` says nothing about `_`: an unread `_` is what `_` **means**, so "never
read" is the one thing there is no point saying about it. The select-arm spelling of the same
redundancy is `F407`'s, because `GRAMMAR` makes that binder optional and
dropping it leaves an arm. A statement's binder has no such spelling.

### L2xx — null safety

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

`main` is never reported by `L102`: the runtime calls it, whatever the source says.

### L3xx — capture

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

### `L4xx` — resolution

| Code   | Rule                                        |
| ------ | ------------------------------------------- |
| `L402` | a `mut fn` that never writes through `this` |

`L401` stood here and has **retired**. It reported a variant name two enums declare: a bare
name was a variant when it resolved to one, resolution took the **first** declaration, and
`c := Red` was a coin toss decided by declaration order. A variant is now always named by
its enum ([Grammar](../surface/grammar.md)), so `Red` alone names nothing and the two
declarations never compete — the lint's whole subject is a form the grammar no longer
derives. The number is not reused.

`mut fn` is not a hint: it makes the receiver a `mut &`, so **every** call site has to hold
the instance in a `mut` binding. A method that only reads charges its callers that and gives
nothing back — and they cannot see why, because the signature is the whole contract and
`mut fn` is all of it. The test is a **write** to `this`, not a mention of it.

### `L5xx` — conversion

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

## Adding a rule

A new SURFACE FORM needs a case in `test-data/fmt/` in the same change, not only a rule.
The formatter's failures are stable — a form printed wrongly is printed the same wrong way
on the second pass — so `make fmt-corpus` is green until some case actually contains the
shape. Both spacing defects found so far (`chan[T]<-` and friends, then `-1`) hid in forms
no case had.

A new LINT rule needs a program in `scripts/lint-check.sh` that makes it fire, for the reason
`make lint` cannot supply one: it runs over the compiler and the stdlib, which are clean, so a
rule that stopped working looks exactly like a rule with nothing to say. That script also fails
when a code documented in `lint.zg` has no case, so the pairing is checked rather than
remembered.

Give it the next number in the group its EFFECT belongs to, add it to the table in
[`src/compiler/zerg/fmt.zg`](../../src/compiler/zerg/fmt.zg) or
[`lint.zg`](../../src/compiler/zerg/lint.zg), and add it here. A rule that moves code belongs
in `F4xx` and should be switchable; one that only spaces or indents does not need to be.
