# Zerg Formatter & Linter Rules

Every rule `zerg fmt` and `zerg lint` apply, each with the code that names it. Part of the
[Language Reference](language.md). Also in [繁體中文](fmt.zh-TW.md).

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
| `F103` | a wrapped expression continues one level in from its statement          |
| `F104` | a run of blank lines survives as exactly one                            |
| `F105` | a group that spans lines closes on its own line                         |

`F105` is what gives a wrapped chain a visible end:

```zerg
a := (builder()
    .run()
    .fast()
)
```

rather than a `))` a reader has to count. It applies to any `(` or `[` whose closer is on
a later line than its opener, so a wrapped argument list gets the same shape.

Parentheses around a multi-line chain are not decoration — they are what makes it parse.
A line break after `)` ends the statement (see the ASI rule in [`GRAMMAR`](../GRAMMAR)),
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

### F4xx — rewrites

| Code   | Rule                                                                      | Default |
| ------ | ------------------------------------------------------------------------- | ------- |
| `F401` | a one-jump if-block becomes the postfix guard it is sugar for             | on      |
| `F402` | imports group — standard library first, then the rest — each alphabetical | on      |

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

```zerg
import "cli"
import "io"
import "strconv"

import "zerg"
```

What counts as standard library is the fixed bundled set. A module resolves by its LAST
path segment, so `import "std/io"` groups with `import "io"`.

It rewrites only a run of plain `import "path"` statements and declines the moment
anything else appears among them — a comment, which belongs to the import it sits on and
would be stranded by sorting, or an `import pub`, whose re-export is an ordering the
author chose.

### Which rules can be switched off

`F1xx`–`F3xx` are not negotiable: they are what "canonical" means, and a formatter with
options for them is a formatter two people configure differently. `F4xx` changes the
code's **shape** rather than its spacing, so it is the group `--off` exists for. `F105`
sits in layout rather than in rewrites because it only decides where an existing token
goes.

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
[`src/compiler/zerg/fmt.zg`](../src/compiler/zerg/fmt.zg) or
[`lint.zg`](../src/compiler/zerg/lint.zg), and add it here. A rule that moves code belongs
in `F4xx` and should be switchable; one that only spaces or indents does not need to be.
