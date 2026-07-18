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
| 2   | Lexical         | comments, identifiers, keywords, newlines, blocks              | planned |
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

Statements are separated by a **line break** or a semicolon `;` — one statement per line is the norm, and
`;` puts several on one line. The first statement the grammar defines is **`nop`**: the placeholder for an
**empty statement**. It does nothing and yields nothing; it stands in wherever a statement is required but
none is wanted:

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

## Editor tooling

Syntax highlighting for Neovim lives under [`editors/nvim/`](../editors/nvim) as classic Vim syntax files:

| File                | Role                                             |
| ------------------- | ------------------------------------------------ |
| `ftdetect/zerg.vim` | detect `*.zg` as the `zerg` filetype             |
| `ftplugin/zerg.vim` | buffer conventions: `#` comments, 4-space indent |
| `syntax/zerg.vim`   | the highlighting rules                           |

To use them, add the `editors/nvim/` directory to your `runtimepath` (or symlink its subdirectories into
`~/.config/nvim/`). The highlighting tracks `GRAMMAR`: it covers exactly the groups that have landed and
grows with each new one.
