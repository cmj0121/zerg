# Zerg Grammar

The formal surface grammar of the language — what is syntactically well-formed, independent of what any
program means. The authoritative productions live in the root [`GRAMMAR`](../GRAMMAR) file; this page is
its prose companion. Part of the [Language Reference](language.md). Also in [繁體中文](grammar.zh-TW.md).

## How the grammar is written

`GRAMMAR` is a plain-text file written in **W3C-style EBNF**, with `#` line comments — the same comment
syntax Zerg source uses. The notation is small:

| Form         | Meaning                                           |
| ------------ | ------------------------------------------------- |
| `name ::= …` | a production; `name` is a non-terminal            |
| `'text'`     | a literal terminal, matched verbatim              |
| `A B`        | `A` followed by `B`                               |
| `A \| B`     | `A` or `B`                                        |
| `( A )`      | grouping                                          |
| `A?`         | zero or one `A`                                   |
| `A*`         | zero or more `A`                                  |
| `A+`         | one or more `A`                                   |
| `[a-z]`      | a character in the range                          |
| `[^x]`       | any character except `x`                          |
| `UPPER`      | a lexical token (defined under the Lexical group) |

The grammar is built up **one group at a time, from core to minor** — each group is a single, focused
commit. `GRAMMAR` grows section by section, and the [nvim tooling](#editor-tooling) grows alongside it.

## Groups

| #   | Group           | Covers                                                         | Status  |
| --- | --------------- | -------------------------------------------------------------- | ------- |
| 1   | nop & skeleton  | `program`, `statement`, statement separators, `nop`            | landed  |
| 2   | Lexical         | comments, identifiers, keywords, newlines, blocks              | landed  |
| 3   | Literals        | `bool`, `int` (`0x`/`0o`/`0b`), `float`, `rune`, `byte`, `str` | planned |
| 4   | Bindings & Expr | `:=`, `mut`, operators and precedence                          | planned |
| 5   | Functions       | `fn`, params, defaults, named arguments, closures, `return`    | planned |
| 6   | Control flow    | `if`, `for … in`, `match` and patterns                         | planned |
| 7   | Types           | `struct`, `enum`, tuple, `type X = Y`, `spec`                  | planned |

Minor groups follow: the error operators (`?` `??` `?.` `!` `raise` `guard`), concurrency
(`spawn` / `chan` / `select` / `<-`), modules (`import` / `pub` / `package` / `init`), the FFI
(`extern "C"`), and `defer` / `del`.

## Group 1 — `nop` & the program skeleton

A Zerg program is a sequence of statements:

```text
program   ::= stmt-list
stmt-list ::= stmt-sep* ( statement ( stmt-sep+ statement )* stmt-sep* )?
stmt-sep  ::= NEWLINE | ';'
statement ::= nop
nop       ::= 'nop'
```

A statement is separated from the next by a **line break** or a semicolon `;`. Both are grammatically
valid, but the **formatter normalizes** a multi-statement line into one statement per line — so canonical
Zerg has **exactly one statement per line** and `;` rarely survives formatting. (The `;` also appears
inside array types and literals `[T; N]`, an unrelated position the formatter keeps.) The first statement
the grammar defines is **`nop`**: the placeholder for an **empty statement**. It does nothing and yields
nothing; it stands in wherever a statement is required but none is wanted:

```text
fn noop() { nop }        # an empty body

for {
    nop                  # an empty loop body — spin until interrupted elsewhere
}
```

Every later group extends `statement` with another form (a binding, an expression statement, a
declaration, …); `nop` remains the one statement that is always available and always inert.

Comments are not statements — a `#` runs to the end of the line, and Zerg has **no block comments**:

```text
# a full-line comment
nop    # a trailing comment
```

## Group 2 — Lexical

Source is UTF-8. Horizontal whitespace (space, tab) separates tokens; a line break is significant only as a
statement separator (group 1). The lexical group fixes what a token is:

```text
letter     ::= [a-zA-Z]
digit      ::= [0-9]
identifier ::= ( letter | '_' ) ( letter | digit | '_' )*
NEWLINE    ::= '\n'
WS         ::= ( ' ' | '\t' )+
COMMENT    ::= '#' [^\n]*
block      ::= '{' stmt-list '}'
```

An **identifier** starts with a letter or `_` and continues with letters, digits, or `_`. A **reserved
keyword** is never an identifier; the full reserved set is:

```text
nop   fn     mut     pub      return   import
if    else   for     in       break    continue
match spawn  select  struct   enum     spec
type  impl   derive  package  init     extern
defer del    raise   guard    is       not
and   or     true    false    nil
```

A **block** groups a statement list in braces — the body a later group hangs on a function, loop, or
conditional. Its inner statements follow the same separator rules as the top level, so an empty block is
written with the placeholder: `{ nop }`.

## Editor tooling

Syntax highlighting for Neovim lives under [`editors/nvim/`](../editors/nvim) as classic Vim syntax files:

| File                | Role                                             |
| ------------------- | ------------------------------------------------ |
| `ftdetect/zerg.vim` | detect `*.zg` as the `zerg` filetype             |
| `ftplugin/zerg.vim` | buffer conventions: `#` comments, 4-space indent |
| `syntax/zerg.vim`   | the highlighting rules                           |

The quickest way is **`make install`**, which symlinks the files into your nvim config
(`$XDG_CONFIG_HOME/nvim`, default `~/.config/nvim`); `make uninstall` removes them. Because it symlinks,
the highlighting tracks this checkout. Alternatively, add the `editors/nvim/` directory to your
`runtimepath`. Either way the highlighting tracks `GRAMMAR`: it covers exactly the groups that have landed
and grows with each new one.

To eyeball the result, open [`examples/syntax-example.zg`](../examples/syntax-example.zg) — a sample that
exercises every highlighted token category.
