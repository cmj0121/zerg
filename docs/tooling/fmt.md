# Zerg Formatter & Linter Rules

Every rule `zerg fmt` and `zerg lint` apply, each with the code that names it. Part of the
[Language Reference](../language.md). Also in [繁體中文](fmt.zh-TW.md).

A rule has a **code** so it can be named — in a diagnostic, in a review, on a command line
that turns it off. The prefix groups them the way a Python linter's does, and the grouping
is by **what a rule does**, not by which pass implements it.

| Prefix | Group     | Is                                               |
| ------ | --------- | ------------------------------------------------ |
| `F1xx` | layout    | where the line breaks and how far it is indented |
| `F2xx` | spacing   | where a space goes between two tokens            |
| `F3xx` | trivia    | what happens to what a person wrote for a person |
| `F4xx` | rewrites  | the rules that MOVE code rather than space it    |
| `L1xx` | dead code | things written that nothing reaches              |

## `zerg fmt`

```sh
zerg fmt <file.zg>...              # rewrite in place; prints the files it changed
zerg fmt --off F401 <file.zg>...   # leave one rule alone (repeatable)
```

The formatter works from **tokens**, not from the AST. That is the whole design decision:
an AST has already thrown away the comments and the blank lines a reader put there, and a
formatter that eats those is one nobody runs twice.

It is **idempotent by construction** — the output is built from the same token stream the
input would produce, so formatting formatted source changes nothing.

### F1xx — layout

| Code   | Rule                                                                    |
| ------ | ----------------------------------------------------------------------- |
| `F101` | one tab per nesting level; `}` closes at the level it opened            |
| `F102` | one statement per line — the lexer's inserted `;` **is** the line break |
| `F103` | a wrapped expression continues one level in per open bracket            |
| `F104` | a run of blank lines survives as exactly one, inside a group too        |
| `F105` | a group that spans lines closes on its own line                         |

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

### F2xx — spacing

| Code   | Rule                                                                      |
| ------ | ------------------------------------------------------------------------- |
| `F201` | one space around a binary operator, after a comma, and around `=>`        |
| `F202` | no space after `(` `[`, before `)` `]` `,`, or between a name and its `(` |

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

| Code   | Rule                                                                      | Default |
| ------ | ------------------------------------------------------------------------- | ------- |
| `F401` | a one-jump if-block becomes the postfix guard it is sugar for             | on      |
| `F402` | imports group — standard library first, then the rest — each alphabetical | on      |
| `F403` | an argument list is one line, or one element per line — never half        | on      |
| `F404` | two or more imports become one parenthesized group                        | on      |

`GRAMMAR` defines `return x if c`, `break if c` and `continue if c` **as** sugar for
`if c { … }` around the same jump — one postfix `if`, three jumps. So the two forms say the
same thing and one of them says it in four lines. The formatter picks the short one, which
is what a guard clause is for: the exceptional exit stops interrupting the shape of the
code it guards. A bare early exit works the same way — `return if c`.

Preferring the sugar is the general rule, not a special case for `return`: **where the
language offers a shorter surface for exactly what is written, the canonical form is the
shorter one**, and a reader stops having to notice that the two are the same thing.

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
import "io"      →        "cli"
import "strconv"          "io"
                          "strconv"
import "zerg"
                          "zerg"
                      )
```

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

A group with **no top-level comma** is exempt from both. A chain and a parenthesised
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

### Which rules can be switched off

`F1xx`–`F3xx` are not negotiable: they are what "canonical" means, and a formatter with
options for them is a formatter two people configure differently. `F4xx` changes the
code's **shape** rather than its spacing, so it is the group `--off` exists for. `F105`
sits in layout rather than in rewrites because it only decides where an existing token
goes; `F403` is a rewrite for the same reason in reverse — it inserts line breaks nobody
wrote and drops a token a joined list no longer needs.

## `zerg lint`

```sh
zerg lint <file.zg>...   # prints findings; exits nonzero when there is one
```

Every check is answered from the parsed file alone — no types, no flow analysis — which
keeps it honest about what it can claim. Findings come back in source order, and a nonzero
exit makes `zerg lint` usable as a gate rather than as decoration.

### L1xx — dead code

| Code   | Finding                       | Why it is worth a line                                                         |
| ------ | ----------------------------- | ------------------------------------------------------------------------------ |
| `L101` | unused import                 | read, parsed and merged in for nothing, and it lies about what this file needs |
| `L102` | private function never called | a public one is a module's interface; a private one with no caller is dead     |
| `L103` | binding never read            | the value was computed for nobody                                              |

```text
L101 unused import "strconv"
L102 private function `never` is never called
L103 binding `unused` in `main` is never read
```

`main` is never reported by `L102`: the runtime calls it, whatever the source says.

## Adding a rule

Give it the next number in the group its EFFECT belongs to, add it to the table in
[`src/compiler/zerg/fmt.zg`](../../src/compiler/zerg/fmt.zg) or
[`lint.zg`](../../src/compiler/zerg/lint.zg), and add it here. A rule that moves code belongs
in `F4xx` and should be switchable; one that only spaces or indents does not need to be.
