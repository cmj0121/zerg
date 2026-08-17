# Zerg Formatter Rules

Every rule `zerg fmt` applies, each with the code that names it. Part of the
[Language Reference](../language.md). Also in [繁體中文](fmt.zh-TW.md).

```sh
zerg fmt <file.zg>...              # rewrite in place; prints the files it changed
zerg fmt --check <file.zg>...      # report what is not canonical, change nothing
zerg fmt --off F401 <file.zg>...   # leave one rule alone (repeatable)
```

A rule has a **code** so it can be named — in a diagnostic, in a review, on the command line
that turns it off. The prefix groups them the way a Python linter's does, and the grouping
is by **what a rule does**, not by which pass implements it.

| Prefix | Group    | Is                                               |
| ------ | -------- | ------------------------------------------------ |
| `F1xx` | layout   | where the line breaks and how far it is indented |
| `F2xx` | spacing  | where a space goes between two tokens            |
| `F3xx` | trivia   | what happens to what a person wrote for a person |
| `F4xx` | rewrites | the rules that MOVE code rather than space it    |

The linter's `L` codes are the same scheme over a different question — what the source says
rather than what it looks like ([Linter Rules](lint.md)). The compiler's `E` codes are not a
tool's rules at all: they name what stops a build ([Compile Diagnostics](diagnostics.md)).

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
from. The gate on the INPUT is bracket balance rather than a parse, deliberately: a
formatter has to work on source the compiler cannot **compile**, which is exactly when a
person reaches for one. A file that balances and is still ill-formed is formatted as before.

**It will not write output it cannot re-parse.** That gate is on the OUTPUT and it is a
different question, asked one way round: **if the input parses, the result must**. `F401`
once rewrote a binding head into source that stops at the `:=`, and `zerg fmt` destroyed
the file, said nothing and exited 0. A formatter is only safe to run because meaning
survives it, and a rewrite that does not parse has no meaning at all. So a file that came
in a program goes out a program, or nothing is written and the run says which file and why.
What fmt owes is that it never SUBTRACTS — a file that did not parse on the way in is left
exactly as unreadable as it was found, because fmt is not what broke it.

`--check` asks the same question and answers it the same way, because a safety net that
asks less than the tool it watches is not one: a file that formatting would break is
reported as such, and the advice `run zerg fmt` is withheld, since on that file the advice
is what does the damage. `make fmt-roundtrip` is the gate over both.

## F1xx — layout

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
- an arm whose body is a **block**, or that puts its body on the next line.

A **budget** then decides whether the run it found is a table at all: if its widest head is
more than **12 columns** past its narrowest, nothing in it is padded and every line keeps its
single space. That bounds the padding any one line takes — without it a run creeps wider one
arm at a time and a three-character pattern ends up half a line from its answer — and it is
**all or nothing** rather than a fourth edge. Cutting the run at the first head that overruns
is what this did first, and it turns one `match` into three or four tables in three or four
columns, which reads worse than the ragged edge it replaced. A table is one thing or it is
not a table. 12 is measured off a `select`, whose arm heads are four different shapes —
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

## F2xx — spacing

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

## F3xx — trivia

| Code   | Rule                                                                       |
| ------ | -------------------------------------------------------------------------- |
| `F301` | a comment keeps its text, on its own line or trailing the code it followed |

## Examples in comments

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

It is a prompt rather than a ` ``` ` fence because comments are to become documentation,
and that generator emits markdown — so ` ``` ` is the **output** syntax. Spelling the input
the same way would leave a generator that must pass one through while producing the other
with no way to tell them apart by looking.

## F4xx — rewrites

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

A **binding head** keeps its block too, for the same reason one step further on. `GRAMMAR`
derives an if-head as `expr | identifier ':=' expr`, and the second alternative is the only
condition in the language that DECLARES: `if s := v` unwraps the optional and binds `s` for
the block. The postfix guard has nowhere to put a declaration, so `if s := v { return s }`
came back as `return s if s := v` — which stops at the `:=`, and whose `s` names nothing now
that the binding it came from is gone. The head is what declines it, not the jump.

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
  pass has nowhere to put it back;
- a head that does not bind — the guard form carries a condition, not a declaration.

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

- printed flat, it would end at or past column 120 — a tab counts as 4;
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

> **[deviation]** A group the pass **declines** keeps its trailing comma, and nothing else
> then objects. `x := sum( # before` with `1,` / `2,` under it holds a comment on the open
> line, so `F403` leaves the group broken — and `zerg fmt --check` calls the file canonical,
> exit 0, while `zerg build` refuses the same file with _E289 a trailing comma before the
> closing `)` of an argument list_. A formatter that certifies a file the compiler will not
> take is worse than one that reformats it, because the certificate is the whole point.

Neither threshold **orders** a break, and that is deliberate. The pass sees one group at a
time rather than the whole line, so the group that crosses column 120 is the last one on
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

The **trailing comma goes** either way, joined or split, and it is a repair rather than a
preference: `GRAMMAR` writes the comma BETWEEN elements and derives one before a closer
nowhere at all — not in a call, not in a tuple, list or map literal, not in a signature —
so a file carrying one is not a Zerg program and the compiler says so (`E289`). The
formatter reads the token stream and not the tree, which is what lets it fix a file the
parser turns away — exactly the file a person reaches for a formatter over.

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

`F406` writes a blank line, and the bar for doing that is deliberately high: when the rule
was written this whole tree had **ten** blank lines inside function bodies, and `fmt.zg` —
1300 lines of it then — had none. So the rule puts one in exactly two places, both of them
places the authors here already put one.

It is the rule's own output that makes that number unmeasurable now: `src/compiler/` carries
roughly 1,700 such blanks today, and `fmt.zg` — 3,057 lines — has 81. The bar is what the
rule DECLINES, below, and not a count anybody can take again.

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
is [`L104`](lint.md)'s, which suggests rather than rewrites.

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

Both are **token rules**, and [`L105`](lint.md) is the reason to say so: `with` expands in the PARSER, so by the
time there is an AST there is no `with` left to lint. Every rule about this form reads tokens for that
reason, the linter's included.

## Which rules can be switched off

`F1xx`–`F3xx` are not negotiable: they are what "canonical" means, and a formatter with
options for them is a formatter two people configure differently. `F4xx` changes the
code's **shape** rather than its spacing, so it is the group `--off` exists for. `F105`
sits in layout rather than in rewrites because it only decides where an existing token
goes; `F403` is a rewrite for the same reason in reverse — it inserts line breaks nobody
wrote and drops a token a joined list no longer needs, and `F406` writes a line nobody
wrote at all.

## Adding a rule

A new SURFACE FORM needs a case in `test-data/fmt/` in the same change, not only a rule.
The formatter's failures are stable — a form printed wrongly is printed the same wrong way
on the second pass — so `make fmt-corpus` is green until some case actually contains the
shape. Both spacing defects found so far (`chan[T]<-` and friends, then `-1`) hid in forms
no case had.

A new `F4xx` REWRITE owes `make fmt-roundtrip` as well. It is the only group that can write
a form the grammar does not have, and it is the group `fmt-tokens` turns off to ask its own
question — so a rewrite is measured by the round-trip gate or by nothing. State the shapes
the rule DECLINES as corpus cases too: a decline is a claim, and a case that is already
canonical is how one is written down.

Give it the next number in the `F` group its EFFECT belongs to, add it to the table in
[`src/compiler/zerg/fmt.zg`](../../src/compiler/zerg/fmt.zg), and add it here. A rule that
moves code belongs in `F4xx` and should be switchable; one that only spaces or indents does
not need to be.
