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

| #   | Group           | Covers                                                         | Status |
| --- | --------------- | -------------------------------------------------------------- | ------ |
| 1   | nop & skeleton  | `program`, `statement`, statement separators, `nop`            | landed |
| 2   | Lexical         | comments, identifiers, keywords, newlines, blocks              | landed |
| 3   | Literals        | `bool`, `int` (`0x`/`0o`/`0b`), `float`, `rune`, `byte`, `str` | landed |
| 4   | Bindings & Expr | `:=`, `mut`, operators and precedence                          | landed |
| 5   | Functions       | `fn`, params, defaults, named arguments, closures, `return`    | landed |
| 6   | Control flow    | `if`, `for … in`, `match` and patterns                         | landed |
| 7   | Types           | `struct`, `enum`, tuple, `type X = Y`, `spec`                  | landed |

Minor groups follow: the error operators (`?` `??` `?.` `!` `raise` `guard`), concurrency
(`spawn` / `chan` / `select` / `<-`), modules (`import` / `pub` / `package` / `init`), the FFI
(`extern "C"`), and `defer` / `del`.

## Group 1 — `nop` & the program skeleton

A Zerg program is a sequence of statements:

```text
program       ::= stmt-list
stmt-list     ::= stmt-sep* ( statement ( stmt-sep+ statement )* stmt-sep* )?
stmt-sep      ::= NEWLINE | ';'
statement     ::= simple-stmt | compound-stmt
simple-stmt   ::= nop | …          # no block; fits on one line
compound-stmt ::= …                # owns a '{ … }' block (if / for / fn / struct / …)
nop           ::= 'nop'
```

A statement is either **simple** (no block — it fits on one line: `nop`, a binding, `return`, …) or
**compound** (it owns a `{ … }` block: `if`, `for`, `fn`, `struct`, …). `nop` is the smallest simple
statement. A statement is separated from the next by a **line break** or a semicolon `;`. Both are grammatically
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
and   or     print   true     false    nil
```

A **block** groups a statement list in braces — the body a later group hangs on a function, loop, or
conditional. Its inner statements follow the same separator rules as the top level, so an empty block is
written with the placeholder: `{ nop }`.

**Newlines & ASI.** A line break is realized as a `;` separator by the lexer (automatic `;` insertion): at
a line break the lexer inserts a `;` when the line's last token can **end an item** — an identifier, a
literal, `)`, `]`, `}`, `?`, `_`, `this`, or `return` / `break` / `continue` / `nop`. It inserts nothing
inside an unclosed `(` or `[`, so an expression or type may span lines there (put a trailing operator at the
line's end to continue). This one rule gives **statements, struct fields, enum variants, and match arms** a
single newline separator. `,` instead separates the elements of a **value list** — arguments, tuples,
generics, a variant payload, and the fields of a struct pattern/literal (a composite, like Go's
`Point{X: 1, Y: 2}`).

## Group 3 — Literals

A literal denotes a constant value:

```text
literal     ::= bool-lit | nil-lit | float-lit | int-lit
              | rune-lit | byte-lit | str-lit | raw-str-lit
bool-lit    ::= 'true' | 'false'
nil-lit     ::= 'nil'
int-lit     ::= dec-int | hex-int | oct-int | bin-int
float-lit   ::= dec-int '.' dec-int exponent? | dec-int exponent
rune-lit    ::= "'" ( rune-char | escape ) "'"
byte-lit    ::= 'b' "'" ( byte-char | byte-escape ) "'"
str-lit     ::= '"' ( str-char | escape )* '"'
raw-str-lit ::= 'r' '"' raw-char* '"'
```

- **Numbers.** An integer is decimal or based — `0x1F`, `0o17`, `0b1010`. A float has a fractional part,
  an exponent, or both — `1.0`, `1e3`, `6.022e23`. A numeric literal is **untyped**: it adopts the type
  its context demands (an integer defaults to `int`, a fractional/exponent literal to `float`). A `_` may
  **group digits** between digits only — `1_000_000`, `0xDE_AD_BE_EF`. A sign is not part of the literal;
  `-5` is unary minus (an operator) applied to `5`.
- **`rune` and `byte`.** A **`rune`** is one Unicode code point in single quotes — `'a'`, `'\n'`,
  `'\u{1F600}'`. A **`byte`** is one octet, `b`-prefixed — `b'a'`, `b'\x41'` — or written `byte(0x41)` by
  cast. Single quotes are for these two; strings use double quotes.
- **`str` and raw strings.** A **`str`** is double-quoted and processes escapes (`\n \t \r \0 \\ \" \'`
  and `\u{…}`). A **raw string** is `r`-prefixed and processes **none** — `r"C:\tmp\new"` is ten literal
  characters. A `str` cannot hold a NUL, so `\0` and `\u{0}` are invalid inside `"…"` (they are fine in a
  `rune` or `byte`).

`f"…"` string interpolation is **not** here — it is an expression, deferred to a later group and its own
commit.

## Group 4 — Bindings & Expressions

A **binding** introduces a name; a reassignment updates one:

```text
binding   ::= 'mut'? identifier ':=' expr
reassign  ::= lvalue '=' expr
expr-stmt ::= expr
lvalue    ::= identifier ( '.' identifier | '[' expr ']' )*
```

`:=` binds a **new, immutable** name; `mut x := …` makes it rebindable; `=` **reassigns** an existing
`mut` binding (or field/element). An expression alone — a call, or a `match` run for its effect — is a
statement. (Destructuring a pattern at `:=`, like `(q, r) := divmod(x, y)`, arrives with patterns in
group 6.)

Expressions are a precedence cascade. Every binary level is **left-associative**; **comparison is
non-associative** — `a < b < c` does not parse, by design.

| Precedence | Operators                            | Assoc     |
| ---------- | ------------------------------------ | --------- |
| 1 highest  | `.` `()` `[]` (field / call / index) | left      |
| 2          | `not` `~` unary `-` `-%`             | right     |
| 3          | `*` `/` `%` `*%` `<<` `>>` `&`       | left      |
| 4          | `+` `-` `+%` `-%` `\|` `^`           | left      |
| 5          | `==` `!=` `<` `>` `<=` `>=` `is`     | non-assoc |
| 6          | `and`                                | left      |
| 7 lowest   | `or`                                 | left      |

The `%`-suffixed `+%` `-%` `*%` are the **wrapping** arithmetic operators; `~` is bitwise complement.
Bitwise `&` `<<` `>>` sit with the multiplicatives and `\|` `^` with the additives — one notch tighter
than comparison, so `a & b == c` reads as `(a & b) == c`, sidestepping C's precedence trap. `is` tests an
existential against a spec or variant name (full form in groups 6–7). A sign is an operator, not part of a
literal.

The null-safety and error operators (`?` `??` `?.` `!`) are **not** here — they belong to the error group.

### String interpolation & `print`

An **f-string** is a primary expression — a string with `{ expr }` holes:

```text
fstr-lit ::= 'f' '"' ( fstr-char | escape | '{{' | '}}' | interp )* '"'
interp   ::= '{' expr '}'
print    ::= 'print' expr
```

`f"sum={x + y}"` renders each hole through `display()` and joins the pieces — it **desugars at compile
time** to `str` concatenation, with no runtime format engine. A plain `"…"` is a literal (its braces are
ordinary); only an f-string reads `{…}`, and `{{` / `}}` write literal braces. Format specifiers like
`f"{x:>.2f}"` are **deferred** to a separate per-type format protocol. **`print`** writes a value's
`display()` and a newline to stdout — a reserved keyword, always in scope, best-effort (it never raises),
so `print f"hello {name}"` is the smallest program.

## Group 5 — Functions

A function is a first-class value — a named declaration, an anonymous expression, and a type:

```text
fn-decl    ::= 'pub'? 'fn' identifier '(' param-list? ')' ret-type? block
fn-expr    ::= 'fn' '(' param-list? ')' ret-type? block
fn-type    ::= 'fn' '(' param-type-list? ')' ret-type?
ret-type   ::= '->' type
return     ::= 'return' expr?
param      ::= 'mut'? identifier ':' type ( '=' expr )?
param-type ::= 'mut'? type
```

- **Declaration vs expression.** `fn name(…) -> R { … }` binds a name; an **anonymous** `fn(…) -> R { … }`
  is an expression (a closure). `pub` exports a declaration's name and is not part of the type.
- **Return.** `return expr` exits with a value, `return` alone with none. An **absent `-> type`** means the
  function returns `nil`.
- **Parameters.** A parameter is `name: type`, optionally `mut` (by-ref, in-place — part of the type) and
  optionally with a **default** `= expr`. A **named argument** at the call is `name: value` (the `arg` form
  from group 4): positional arguments come first, then any may be named, and once one is named the rest
  must be too — which is what lets a defaulted parameter be skipped.
- **Type.** A function's type is `fn(P…) -> R` — parameters (with `mut`) and result, nothing else; defaults
  and parameter names live in the declaration, not the type.

## Group 6 — Control flow & Pattern matching

`if` and `for` are statements; `match` is an expression:

```text
if-stmt     ::= 'if' if-head block ( 'else' 'if' if-head block )* ( 'else' block )?
if-head     ::= expr | identifier ':=' expr
for-stmt    ::= 'for' block | 'for' 'mut'? identifier 'in' expr block
break       ::= 'break' ( 'if' expr )?
continue    ::= 'continue' ( 'if' expr )?
match-expr  ::= 'match' expr '{' match-arm+ '}'
match-arm   ::= pattern '->' expr
pattern     ::= sub-pattern ( '|' sub-pattern )*
sub-pattern ::= variant-pat | struct-pat | tuple-pat | literal-pat | binding-pat | '_'
```

- **`if`.** The condition is a `bool` — no truthiness. The **binding head** `if x := expr { … }` runs the
  block only when `expr` is present (the one-arm-`match` sugar). `else if` / `else` chain as usual.
- **`for`.** The one loop, in two forms: **`for { … }`** infinite (leave via `break` / `return`), and
  **`for x in it { … }`** over an `Iterable`, binding `x` by copy (**`for mut x`** binds in place). There is
  no `while` and no C-style `for`. **`break` / `continue`** act on the nearest loop; **`break if c`** and
  **`continue if c`** are sugar for `if c { break }` / `if c { continue }`.
- **`match`.** An expression: it tries the value against arms in order, yields the first fit, and every arm
  yields the same type — so a `match` is usable at a `:=`, a `return`, or an argument. A trailing **`_`**
  covers the rest.
- **Patterns** destructure by copy: a **variant** with a payload binding (`Left(v)`, nested `Left(Some(v))`),
  a **struct** (`Div{q, r}`), a **tuple** (`(a, b)`), a **literal** (matched by `equal`), a plain
  **binding** name, an **or-pattern** (`A | B`, its sides binding the same names), or the wildcard **`_`**.
  A tuple or struct pattern also destructures at a `:=` binding — `(q, r) := divmod(x, y)`. Guard conditions
  (`Left(v) if v > 0`) are deferred.

## Group 7 — Types & Declarations

The type expressions used since group 5, and the declarations that introduce types and behavior:

```text
type        ::= base-type '?'?
base-type   ::= type-name type-args? | tuple-type | array-type | chan-type | fn-type
type-args   ::= '[' type ( ',' type )* ']'
array-type  ::= '[' type ';' expr ']'
struct-decl ::= 'pub'? 'struct' identifier generics? '{' field-list? '}'
enum-decl   ::= 'pub'? 'enum' identifier generics? '{' variant-list? '}'
type-decl   ::= 'pub'? 'type' identifier generics? '=' type
spec-decl   ::= 'pub'? 'spec' identifier generics? '{' spec-member* '}'
impl-decl   ::= 'impl' type-name generics? 'for' type '{' fn-decl* '}'
derive-decl ::= 'derive' type-name ( ',' type-name )* 'for' type-name
```

- **Type expressions.** A **name** with optional **type arguments** (`int`, `User`, `list[int]`,
  `Either[A, B]`); a **tuple type** `(A, B)`; an **array** `[T; N]` (the other use of `;`); a **channel**
  `chan[T]`, with Go-style direction — `<-chan[T]` is receive-only (a receiver) and `chan[T]<-` is
  send-only (a sender); or a **function type** `fn(P…) -> R` (group 5). A trailing **`?`** makes any type an
  **optional** — `str?`.
- **`struct` / `enum`.** A **product** with named, typed fields or a **sum** with variants that each carry
  an optional payload — `Circle(float)`, `Rect(float, float)`. Fields and variants are separated **exactly
  like statements** (a line break, or `;` inline) — there is **no `,`** between them; the `,` inside a
  payload `(A, B)` is an ordinary list. Both may be generic — `enum Either[X, Y] { … }`.
- **`type X = Y`.** A **strong typedef** — a new, distinct type, not a transparent alias; may be generic.
- **`spec`.** A behavioral interface: members are **required** (a signature with no body) or **provided** (a
  full method). A method takes **no explicit receiver** — `this` is implicit inside a method, reached through
  the instance it is called on; a `fn` that uses `this` with no instance bound is a compile error. The self
  type is **`This`**. `impl … for …` supplies a spec's methods for a type, and `derive …` asks the compiler
  to write them.

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
