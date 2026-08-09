# Zerg Grammar

The formal surface grammar of the language — what is syntactically well-formed, independent of what any
program means. The root [`GRAMMAR`](../../GRAMMAR) file is the **normative** definition of Zerg syntax; **this
page is its informative prose companion** and defers to it on any discrepancy. Part of the
[Language Reference](../language.md). Also in [繁體中文](grammar.zh-TW.md).

Syntax being well-formed is separate from a feature being built: a construct can be recognized by the
grammar yet **[not yet]** at code generation, or its runtime behavior a **[deviation]** — this page notes
those where the surface form invites them. See [Conformance](../conformance.md) for the status markers.

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

| #   | Group                | Covers                                                           | Status |
| --- | -------------------- | ---------------------------------------------------------------- | ------ |
| 1   | nop & skeleton       | `program`, `statement`, statement separators, `nop`              | landed |
| 2   | Lexical              | comments, identifiers, keywords, newlines, blocks                | landed |
| 3   | Literals             | `bool`, `int` (`0x`/`0o`/`0b`), `float`, `rune`, `byte`, `str`   | landed |
| 4   | Bindings & Expr      | `:=`, `mut`, operators and precedence                            | landed |
| 5   | Functions            | `fn`, params, defaults, named arguments, closures, `return`      | landed |
| 6   | Control flow         | `if`, `for … in`, `match` and patterns                           | landed |
| 7   | Types                | `struct`, `enum`, tuple, `type X = Y`, `spec`                    | landed |
| 8   | Null-safety & Errors | `?` `??` `?.` `!` `raise` `guard`, and the `T?` / `Result` tiers | landed |
| 9   | Concurrency          | `spawn`, `chan[T]()`, `ch <- v`, `<-ch`, `select`                | landed |
| 10  | Modules & Programs   | `import`, `import pub`, `init()`, `pub`, `main`                  | landed |
| 11  | Resource cleanup     | `defer expr`, `del name`                                         | landed |
| 12  | Unsafe               | `unsafe { }`, `unsafe fn`, `ptr` / `ptr[T]`, `asm(…)`            | landed |

All groups above are landed — the surface grammar is complete. Raw memory and inline assembly land with
group 12 (`unsafe` / `ptr` / `asm`), so bare-metal work (MMIO, page tables, a syscall via `asm`) is
expressible. Two things are deliberately **not surface grammar**: **FFI import** (calling a C symbol like
`malloc` from a C library) is a **stdlib facility** — the stdlib binds foreign symbols and a foreign call is
**unsafe** (allowed only inside `unsafe`); there is no `extern` keyword. **FFI export** needs no syntax
either: a package's `pub` surface already _is_ its C ABI (see [FFI](../runtime/ffi.md)).

## Group 1 — `nop` & the program skeleton

A Zerg program is a sequence of statements:

```text
program       ::= stmt-list
stmt-list     ::= stmt-sep* ( statement ( stmt-sep+ statement )* stmt-sep* )?
stmt-sep      ::= NEWLINE | ';'
statement     ::= simple-stmt | compound-stmt | decorated-decl
simple-stmt   ::= nop | …          # no block; fits on one line
compound-stmt ::= …                # owns a '{ … }' block (if / for / …)
decorated-decl ::= …               # a name-introducing declaration, optional #[…] prefix (fn / struct / …, group 7)
nop           ::= 'nop'
```

A statement is **simple** (no block — it fits on one line: `nop`, a binding, `return`, …),
**compound** (it owns a `{ … }` block: `if`, `for`, …), or a **declaration** (`fn`, `struct`, `enum`,
`spec`, … — it introduces a name and may carry a `#[…]` decorator; group 7). `nop` is the smallest simple
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

Comments are not statements — a `#` runs to the end of the line. A `##` begins a **doc comment** (attached
to the declaration that follows), and `#[` begins a decorator (group 7); Zerg has **no block comments**:

```text
# a full-line comment
nop    # a trailing comment
## a doc comment — attaches to the declaration below
fn answer() -> int { return 42 }
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
COMMENT     ::= '#' [^#[\n] [^\n]* | '#' NEWLINE  # '#' not before '#' or '[' → line comment
DOC-COMMENT ::= '##' [^\n]*                       # doc comment; attaches to the following declaration
block      ::= '{' stmt-list '}'
```

An **identifier** starts with a letter or `_` and continues with letters, digits, or `_`. A **reserved
keyword** is never an identifier; the full reserved set is:

```text
nop   fn     mut     pub      return   import
if    else   for     in       break    continue
match spawn  select  struct   enum     spec
chan  type   impl    package  init
defer del    close   raise    guard    is
not   and    or      print    this     with
as    from   true    false    nil      const
unsafe ptr   asm
```

(`derive` is not a keyword — it is the decorator name in `#[derive(…)]`.)

A **block** groups a statement list in braces — the body a later group hangs on a function, loop, or
conditional. Its inner statements follow the same separator rules as the top level, so an empty block is
written with the placeholder: `{ nop }`.

**Newlines & ASI.** A line break is realized as a `;` separator by the lexer (automatic `;` insertion): at
a line break the lexer inserts a `;` when the line's last token can **end an item** — an identifier, a
literal, `)`, `]`, `}`, `?`, `_`, `this`, or `return` / `break` / `continue` / `nop`. It inserts nothing
inside an unclosed `(` or `[`, so an expression or type may span lines there (put a trailing operator at the
line's end to continue). This one rule gives **statements, struct fields, enum variants, and match arms** a
single newline separator. `,` instead separates the elements of a **value list** — arguments, tuples,
generics, a variant payload, the fields of a struct pattern (`Div{q, r}`), and the entries of a map literal
(both `{ … }` composites). A struct **value** has no brace literal — it is built by a call (group 7).

## Group 3 — Literals

A literal denotes a constant value:

```text
literal     ::= bool-lit | nil-lit | float-lit | int-lit
              | rune-lit | byte-lit | str-lit | raw-str-lit | cmd-lit
bool-lit    ::= 'true' | 'false'
nil-lit     ::= 'nil'
int-lit     ::= dec-int | hex-int | oct-int | bin-int
float-lit   ::= dec-int '.' dec-int exponent? | dec-int exponent
rune-lit    ::= "'" ( rune-char | escape ) "'"
byte-lit    ::= 'b' "'" ( byte-char | byte-escape ) "'"
str-lit     ::= '"' ( str-char | escape )* '"'
              | '"""' ( ml-str-char | escape )* '"""'   # multiline; line breaks literal
raw-str-lit ::= 'r' '"' raw-char* '"'
cmd-lit     ::= '`' cmd-char* '`'                        # COMMAND literal — run directly, no shell (f-form: group 5)
```

- **Numbers.** An integer is decimal or based — `0x1F`, `0o17`, `0b1010`. A float has a fractional part,
  an exponent, or both — `1.0`, `1e3`, `6.022e23`. A numeric literal is **untyped**: it adopts the type
  its position demands (an integer defaults to `int`, a fractional/exponent literal to `float`). A `_` may
  **group digits** between digits only — `1_000_000`, `0xDE_AD_BE_EF`. A sign is not part of the literal;
  `-5` is unary minus (an operator) applied to `5`.
- **`rune` and `byte`.** A **`rune`** is one Unicode code point in single quotes — `'a'`, `'\n'`,
  `'\u{1F600}'`. A **`byte`** is one octet, `b`-prefixed — `b'a'`, `b'\x41'` — or written `byte(0x41)` by
  cast. Single quotes are for these two; strings use double quotes.
- **`str`, multiline, and raw strings.** A **`str`** is double-quoted and processes escapes (`\n \t \r \0 \\
\" \'` and `\u{…}`). A **triple-quoted** `"""…"""` `str` is the same but **spans lines** — line breaks are
  literal, and a lone `"` or `""` inside needs no escape (only `"""` ends it), so it fits SQL/JSON/prose. A
  **raw string** is `r`-prefixed and processes **none** — `r"C:\tmp\new"` is ten literal characters. A `str`
  cannot hold a NUL, so `\0` and `\u{0}` are invalid inside `"…"` (they are fine in a `rune` or `byte`).
- **Command literal.** A backtick `` `git status` `` is a **command** — a child process run **directly** (no
  shell), its argv split on whitespace (quotes respected), so no interpolation and no injection/glob/pipe. The
  interpolating `` f`…` `` form (group 5) instead runs through a **shell** and **shell-quotes** each hole
  (`{x:raw}` opts out). Execution — pipes as `Reader`/`Writer`, the process `Ref[proc]` — is **stdlib**
  ([Process & I/O](../runtime/io.md)), not grammar. Both command-literal forms are **[not yet]**: recognized by the
  grammar but **rejected at code generation** this phase.

`f"…"` string interpolation is **not** here — it is an expression, deferred to a later group and its own
commit.

## Group 4 — Bindings & Expressions

A **binding** introduces a name; a reassignment updates one:

```text
binding       ::= ( 'mut' | 'const' )? bind-target ':=' expr        # inferred; 'const' = shadow-proof
              | ( 'mut' | 'const' )? identifier ':' type '=' expr    # type-annotated; the ':' picks this over reassign
reassign      ::= assign-target '=' expr
expr-stmt     ::= expr
lvalue        ::= identifier ( '.' identifier | '.' dec-int | '[' expr ']' )*   # '.0' = tuple element
assign-target ::= lvalue | '(' assign-target ( ',' assign-target )* ')'
                | type-name '{' field-target ( ',' field-target )* ( ',' '..' )? '}'
field-target  ::= identifier ( ':' assign-target )?
```

`:=` binds a **new, immutable** name **inferring** its type; `mut x := …` makes it rebindable; `const x := …`
is immutable **and shadow-proof** (nothing may shadow it, and it may not shadow a visible name — either
direction is an error). A **type-annotated** binding spells `name: T = expr` — it fixes the type and
**context-types** the RHS (a bare `[…]` becomes a `list`, or an array against a `[T; N]` target); the leading
`:` is what tells it apart from a `=` **reassign**, which updates an existing `mut` binding (or field/element).
An expression alone — a call, or a `match` run for its effect — is a
statement. A `:=` binding may **destructure** into new names (`(q, r) := divmod(x, y)`, group 6), and `=`
**mirrors it into existing lvalues** — `(a, b) = swap(a, b)`, `Div{q, r} = divmod(x, y)` — each leaf being
any lvalue (`(a, obj.f) = …`).

Expressions are a precedence cascade. Every binary level is **left-associative**; **comparison is
non-associative** — `a < b < c` does not parse, by design.

| Precedence | Operators                             | Assoc     |
| ---------- | ------------------------------------- | --------- |
| 1 highest  | `.` `()` `[]` (field / call / index)  | left      |
| 2          | `not` `~` unary `-` `-%`              | right     |
| 3          | `*` `/` `%` `*%` `<<` `>>` `&`        | left      |
| 4          | `+` `-` `+%` `-%` `\|` `^`            | left      |
| 5          | `..` `..=` (range)                    | —         |
| 6          | `==` `!=` `<` `>` `<=` `>=` `is` `in` | non-assoc |
| 7          | `and`                                 | left      |
| 8 lowest   | `or`                                  | left      |

The `%`-suffixed `+%` `-%` `*%` are the **wrapping** arithmetic operators; `~` is bitwise complement.
Bitwise `&` `<<` `>>` sit with the multiplicatives and `\|` `^` with the additives — one notch tighter
than comparison, so `a & b == c` reads as `(a & b) == c`, sidestepping C's precedence trap. `is` tests an
existential against a spec or variant name (full form in groups 6–7); `in` tests **membership**, and what a
set is depends on what names it — a container names its elements, a range its members, and an **error kind
names itself and everything below it** in the taxonomy ([Errors](../code/errors.md)). A sign is
an operator, not part of a literal. A **range** binds one notch tighter than comparison so `v in 0..10`
reads `v in (0..10)`: `x..y` is **sugar** for `range(x, y)`, `x..=y` for `range(x, y + 1)`, `x..` for an
open range, and `v in r` for `r.contains(v)` — the `Range` / `contains` machinery is stdlib. (`??` from
group 8 remains the single loosest binary, looser than `or`.)

The null-safety and error operators (`?` `??` `?.` `!`) live in group 8; the postfix ones (`?` `!` `?.`)
join `postfix` above, and `??` sits at the loosest level.

A postfix `[…]` is an **index** or **explicit type arguments** (`parse[int]("42")`, `collect[K, V](…)`),
told apart by **name resolution**: a value base subscripts, a generic function or type constructor base takes
type arguments — a name is exactly one of these (a type and a function cannot share a name, group 7), so no
turbofish is needed. A comma (`[X, Y]`) is unambiguously type arguments. This is the same name resolution
that tells a pattern **variant** from a **binding** (group 6); the grammar is resolved with scope in hand.

### Composite literals

Values are **built** in expression position — the mirror of the patterns (group 6) that take them apart:

```text
tuple-lit ::= '(' expr ',' expr ( ',' expr )* ')'    # (a, b) — 2+ elements
list-lit  ::= '[' ( expr ( ',' expr )* )? ']'        # [1, 2, 3]; empty []
          | '[' expr ';' const-expr ']'              # fill form: N copies of v — [0; 256]
map-lit   ::= '{' map-entry ( ',' map-entry )* '}'   # {k: v, …}
          | '{' ':' '}'                               # empty map {:}
map-entry ::= expr ':' expr
```

- **Tuple `(a, b)`** — a parenthesized 2+ list; a single `(expr)` is just grouping, so there is no 1-tuple
  and no empty `()`. This is what lets `divmod` write `return (q, r)`. Read an element back by **static
  index** — `t.0`, `t.1` — not `t[i]`: a tuple is heterogeneous, so the index must be a compile-time constant
  for the element's type to be known (`a.0.1` is `(a.0).1`, never a float). There is **no tuple struct** —
  `type P = (A, B)` is the named positional type, `struct` the named-field one.
- **List `[1, 2, 3]`** (empty `[]`), ordered. In a position typed `[T; N]`, a list literal of the right
  length **adopts** that array type. The **fill form `[v; N]`** is `N` copies of `v` (`N` a const-expr,
  mirroring the `[T; N]` array type's `;`) — the way to build a large array without spelling every element.
- **Map `{k: v}`** (empty `{:}`). The `:` is what tells a `{…}` **map** from a **block** — `k: v` is not a
  statement, so a braced map is unambiguous, whereas a **bare-element** `{…}` is **always a block**.
- **Set `set([1, 2, 3])`** (empty `set()`) — built through its constructor, **not** a brace literal, since
  `{1}` would be indistinguishable from a one-statement block. `set`'s parameter defaults to `[]`, so
  `set()` is the empty set.
- Two rules fall out of `{` being the block opener: at a **statement's start** `{` is a block statement (its
  value discarded), and **any `{`-opening expression — a block or a map literal — at the start of an
  `if`/`for`/`with`/`match` head** must be parenthesized.

### String interpolation & `print`

An **f-string** is a primary expression — a string with `{ expr }` holes:

```text
fstr-lit    ::= 'f' '"' ( fstr-char | escape | '{{' | '}}' | interp )* '"'
fcmd-lit    ::= 'f' '`' ( cmd-char | interp )* '`'   # interpolating command literal — runs through a shell
interp      ::= '{' expr '='? conversion? format-spec? '}'
conversion  ::= '!' ( 'r' | 's' | 'a' )     # r = debug, s = display, a = ascii
format-spec ::= ':' fmt-char*               # read by the type's Format protocol
print       ::= 'print' expr
```

A hole is **Python-style**: `expr`, then an optional `=`, `!` conversion, and `:` format spec. `f"sum={x + y}"`
renders each hole through `display()` and joins the pieces — it **desugars at compile time** to `str`
concatenation, with no runtime format engine. The same `{ … }` holes power the interpolating **command
literal** `` f`…` `` (the group-3 command literal, **[not yet]**): it runs through a shell and
**shell-quotes** each hole (`{x:raw}` opts out), so a value splices in as one safe argument.

- **`{x}`** renders through `display`. **`{x!r}`** / **`{x!s}`** / **`{x!a}`** convert first — `debug` /
  `display` / ascii. All three are **[not yet]** — a conversion in a hole is refused by name
  ([Format](../runtime/format.md)). **`{x=}`** is self-documenting: it emits the expression's source text
  and `=`, then the value (`f"{n=}"` → `n=42`) — **[not yet]** as well.
- **`{x:spec}`** hands `spec` to the type's **`Format`** protocol — `f"{pi:.2f}"`, `f"{n:04d}"`,
  `f"{p:>10}"`. The spec **string's meaning is the type's** (stdlib numbers/`str` read the usual
  fill/align/sign/`#`/`0`/width/`.precision`/type); the grammar treats it as opaque up to `}`.
- A plain `"…"` is a literal (its braces are ordinary); only an f-string reads `{…}`, and `{{` / `}}` write
  literal braces.

**`print`** writes a value's `display()` and a newline to stdout — a reserved keyword, always in scope,
best-effort (it never raises), so `print f"hello {name}"` is the smallest program.

## Group 5 — Functions

A function is a first-class value — a named declaration, an anonymous expression, and a type:

```text
fn-decl    ::= 'pub'? 'unsafe'? 'mut'? 'fn' identifier generics? '(' param-list? ')' ret-type? block
fn-expr    ::= 'fn' '(' closure-param-list? ')' ret-type? block   # anonymous — never generic/unsafe; types inferrable
fn-type    ::= 'unsafe'? 'fn' '(' param-type-list? ')' ret-type?
ret-type   ::= '->' type
return     ::= 'return' expr? ( 'if' expr )?     # 'return x if c' — conditional early exit (sugar)
param      ::= ( 'mut' '&' )? identifier ':' type ( '=' expr )?
param-type ::= ( 'mut' '&' )? type
closure-param ::= ( 'mut' '&' )? identifier ( ':' type )? ( '=' expr )?   # ': type' optional in a closure
```

- **Declaration vs expression.** `fn name(…) -> R { … }` binds a name; an **anonymous** `fn(…) -> R { … }`
  is an expression (a closure). `pub` exports a declaration's name and is not part of the type.
- **Inferred closure params.** A **closure** may omit a parameter's `: type` — `xs.map(fn(x) { x *% 2 })` —
  and the compiler infers it from the function type the closure is checked against (an argument's declared
  parameter type, or a typed binding's RHS). A named `fn` declaration cannot: its signature is its contract.
- **Closure capture is immutable.** A closure captures values and channels **by copy, read-only** — it
  cannot mutate a captured variable. This is deliberate: value semantics makes a captured mutation invisible
  to the outside anyway, a by-ref capture would dangle without a GC, and immutable capture is what makes a
  `spawn`-ed closure **data-race free**. The three things a mutating closure would do have direct idioms
  instead: **accumulate** with a `for` loop (`for x in xs { sum = sum + x }`), keep **state** in a `struct`
  with a `mut fn`, and share **concurrent** state through a `chan`.
- **Return.** `return expr` exits with a value, `return` alone with none. An **absent `-> type`** means the
  function returns `nil`. A **trailing `if`** makes it conditional — `return MAX if v > MAX` is sugar for
  `if v > MAX { return MAX }` (and bare `return if done`), the same postfix `if` as `break if` / `continue
if` / `raise e if`; on a false condition control falls through. A leading `if` _with a block_ is instead
  an if-expression
  being returned (`return if c { a } else { b }`); the conditional-return `if` takes a bare condition, no block.
- **Parameters.** A parameter passes **by value** (a copy) and may carry a **default** `= expr`. A **named
  argument** at the call is `name: value` (the `arg` form from group 4): positional arguments come first,
  then any may be named, and once one is named the rest must be too — which is what lets a defaulted
  parameter be skipped.
- **`mut &` — mutable reference.** `mut &x` passes a **mutable reference**: the callee may change `x` and the
  change **affects the caller's argument**. Two controls meet — the **caller** decides whether its variable
  is `mut`, the **callee** decides via `mut &` whether it writes back — so a visible mutation needs **both**,
  and the argument must be a `mut` lvalue. There is **no call-site marker**: the signature is the contract. A
  `mut &` reference lives only for the call — it cannot **escape** (be captured by a `spawn` or stored past
  the call) nor **alias** (the same variable to two `mut &` in one call), which is safe with no borrow
  checker. There is **no plain `mut x` parameter**; for a private mutable copy, shadow — `mut x := x`.
  `mut fn` is exactly the `mut &this` case for a method's receiver.
- **Argument conventions.** Names are resolved at **compile time** against the signature, so a `map` does
  **not** splat into named arguments (runtime string keys, one homogeneous value type vs compile-time
  heterogeneous parameters). There are **no variadics** either. Pass a **`list`** for many positional values,
  and an **options struct** with field defaults for a bag of named options — `draw(Style(width: 2))`, the
  statically typed stand-in for keyword arguments. A `map` is passed as an ordinary value, never expanded
  into a call.
- **Type.** A function's type is `fn(P…) -> R` — parameters (with `mut &`) and result, nothing else; defaults
  and parameter names live in the declaration, not the type.
- **`mut fn` (mutating method).** A `mut fn` marks a **method** that mutates its implicit receiver `this` in
  place; the call site must hold the receiver in a `mut` binding. It is meaningful only inside an `impl` or
  `spec` — a free function or closure has no receiver. `mut fn` tracks mutation of the value's **own fields**,
  not effects on a resource behind a `Ref[T]`/foreign handle (which need no `mut` — see group 7).
- **Generics.** A `fn` (and a `spec` method) may take type parameters — `fn max[T: Ord](a: T, b: T) -> T` —
  with optional spec **bounds**; the full generics grammar and its monomorphization model are in group 7. An
  **anonymous** `fn(…)` is never generic (wrap it in a named `fn` if you need type parameters).

## Group 6 — Control flow & Pattern matching

`for` is a statement; `match` is an expression; `if` is both (an expression when it has an `else`):

```text
block       ::= '{' stmt-list '}'                     # a bare block opens a nested scope
with-stmt   ::= 'with' expr ( 'as' identifier )? block
if-stmt     ::= 'if' if-head block ( 'else' 'if' if-head block )* ( 'else' block )?
if-expr     ::= 'if' if-head block ( 'else' 'if' if-head block )* 'else' block   # else required
if-head     ::= expr | identifier ':=' expr
for-stmt    ::= 'for' block | 'for' 'mut'? identifier 'in' expr block | 'for' expr block
break       ::= 'break' ( 'if' expr )?
continue    ::= 'continue' ( 'if' expr )?
match-expr  ::= 'match' expr '{' match-arm+ '}'
match-arm   ::= ( pattern | range-arm ) ( 'if' expr )? '=>' expr   # '=>' separates arm from body
range-arm   ::= range-bound ( '..' range-bound? | '..=' range-bound )   # sugar: '_ if _ in <range>'
range-bound ::= '-'? literal | identifier
pattern       ::= sub-pattern ( '|' sub-pattern )*
sub-pattern   ::= pattern-core ( 'as' identifier )?
pattern-core  ::= variant-pat | struct-pat | tuple-pat | list-pat | literal-pat | binding-pat | '_'
struct-pat    ::= type-name '{' struct-fields? '}'
struct-fields ::= field-pat ( ',' field-pat )* ( ',' '..' )? | '..'
list-pat      ::= '[' ( list-pat-elem ( ',' list-pat-elem )* )? ']'   # at most one '..'
list-pat-elem ::= pattern | '..' identifier?
```

- **Block & `with`.** A bare `{ … }` opens a **nested scope** — its bindings and scope-owned values are
  freed at the `}`. A block is also an **expression** (`primary`, group 4): its **value is its last
  statement's value** — an expr-statement yields its expr; any other statement, or an empty block, yields
  `nil`. The ASI `;` only **separates** statements, it does not discard a value, so `guard { … }` and a
  multi-statement `match` arm (`P => { …; v }`) both yield. **`with expr as y { … }`** is sugar over a bare
  block: it binds a scoped resource `y` and guarantees the resource's **teardown runs on every exit**
  (normal, `return`, or an abort). The value implements the built-in **`Scoped`** spec (its one method is the
  teardown); a `Ref[T]`'s drop already satisfies it. So `with open(p) as f { f.read() }` ≈
  `{ f := open(p); defer f.<teardown>; … }`. `as y` is optional when the resource is used only for its scope
  (a held lock).
- **`if`.** The condition is a `bool` — no truthiness. The **binding head** `if x := expr { … }` runs the
  block only when `expr` is present (the one-arm-`match` sugar). `else if` / `else` chain as usual. An `if`
  with a **mandatory `else`** is also an **expression** (`x := if c { a } else { b }`) — it yields the taken
  branch's block value, and every branch must yield the same type; at statement position the statement form
  wins (value discarded).
- **`for`.** The one loop, in three forms: **`for { … }`** infinite (leave via `break` / `return`),
  **`for cond { … }`** while `cond` (a `bool`) holds, and **`for x in it { … }`** over an `Iterable`, binding
  `x` by copy (**`for mut x`** binds in place). The iterate form is taken when `mut` or an `identifier in`
  follows `for`; a bare `for expr` is the while condition. There is no C-style three-clause `for`. **`break` /
  `continue`** act on the nearest loop; **`break if c`** and **`continue if c`** are sugar for
  `if c { break }` / `if c { continue }`. There are **no loop labels** — to exit an outer loop from a
  nested one, extract a function and `return` (or use a flag with `break if`).
- **`match`.** An expression: it tries the value against arms in order, yields the first fit, and every arm
  yields the same type — so a `match` is usable at a `:=`, a `return`, or an argument. A trailing **`_`**
  covers the rest. An arm separates pattern from body with **`=>`** — distinct from the **`->`** of a
  function's return type, so the two never blur.
- **Patterns** destructure by copy: a **variant** with a payload binding (`Left(v)`, nested `Left(Some(v))`),
  a **struct** (`Div{q, r}`), a **tuple** (`(a, b)`), a **literal**, optionally signed (`-1`), matched by
  `equal`; a plain
  **binding** name, an **or-pattern** (`A | B`, its sides binding the same names), or the wildcard **`_`**.
  A tuple or struct pattern also destructures at a `:=` binding — `(q, r) := divmod(x, y)`.
- **Guards.** An arm may add `if expr` after the pattern (`Some(v) if v > 0 => …`) — a condition, seeing the
  pattern's bindings, that must also hold; on `A | B if c` it covers the whole or-pattern. A guarded arm
  **does not count toward exhaustiveness** (the compiler can't prove the guard holds), so the case still
  needs an unguarded arm (or `_`).
- **Range arm.** A **match-only** `200..300 =>` / `400..=499 =>` / `500.. =>` is **sugar** for a guard —
  `_ if _ in <range>` — so it matches by **containment** (the `..` operators), not `equal`, and inherits a
  guard's exhaustiveness (it does not count as covering). It does **not bind**; to use the value, write the
  explicit `x if x in <range>`. Bounds are compile-time constants; a range arm is recognized by its `..`.
- **Rest & partial.** A **struct pattern must list every field** or end with `..` — `Div{q, r}` (all),
  `Div{q, ..}` (rest ignored), `Div{..}` (any). Listing all by default means adding a field **breaks** old
  patterns, forcing you to look. A **list pattern** matches a list — `[a, b]`, `[head, ..tail]`,
  `[..init, last]`, `[]` — with **at most one `..`**; `..name` binds the skipped run as a list, a bare `..`
  drops it (a struct's `..` only ignores, never binds). In pattern position `..` is **rest**, distinct from
  the value-level range `..`.
- **Variant vs binding** is decided by **name resolution**: a bare name is a variant when it resolves to a
  known type or enum variant in scope, and a fresh binding otherwise. Names are **case-free**, so this is
  resolution, not capitalization — the same name resolution the postfix `[…]` uses (group 4).
- **Qualified variant.** A variant may also be named **by its enum** — `Color.Red`, `Shape.Line(5)`, and the
  same in pattern position. It is the identical value; what it adds is that the reader is told **which** enum
  without resolving the name, which the bare form asks of them. The qualification must be **true**:
  `Color.Apple` names a variant of another enum and is an error, not a fresh binding.
- **`as` binding.** `pattern as name` also binds the **whole** matched value to `name` while the pattern keeps
  destructuring — `Move{x, y} as m`, `[first, ..] as all`, nested `Some(inner as v)`. It reads like `with` /
  `import`: `<thing> as <name>`. On an or-pattern `as` binds the nearest alternative (`A | B as m` is
  `A | (B as m)`); bind both sides with `A as m | B as m`.

## Group 7 — Types & Declarations

The type expressions used since group 5, and the declarations that introduce types and behavior:

```text
type        ::= base-type '?'?
base-type   ::= type-name type-args? ( '.' identifier )*   # 'I.Item' projects; chainable 'I.Item.Sub'
              | tuple-type | array-type | chan-type | fn-type | ptr-type   # ptr-type: group 12 (unsafe)
type-args   ::= '[' generic-arg ( ',' generic-arg )* ']'
generic-arg ::= type | const-expr             # a type, or a const-expr filling a value generic param
array-type  ::= '[' type ';' const-expr ']'   # N is a const-expr
struct-decl ::= 'pub'? 'struct' identifier generics? '{' field-list? '}'
enum-decl   ::= 'pub'? 'enum' identifier generics? '{' variant-list? '}'
type-decl   ::= 'pub'? 'type' identifier generics? '=' type
const-expr  ::= expr                          # compile-time-foldable expr (no 'const' keyword)
spec-decl   ::= 'pub'? 'spec' identifier generics? ( ':' bound )? '{' spec-member* '}'
impl-decl   ::= 'impl' generics? type-name type-args? 'for' type '{' impl-item* '}'  # spec impl
              | 'impl' generics? type '{' impl-item* '}'                             # inherent
impl-item   ::= fn-decl | assoc-bind | val-bind   # method/assoc fn, assoc-type binding, or assoc value
assoc-bind  ::= 'type' identifier '=' type    # 'type Item = int' fills a spec's assoc type
val-bind    ::= identifier ':=' const-expr    # 'BITS := 32' fills a spec's associated value
spec-member ::= fn-sig | fn-decl | assoc-type | assoc-val
assoc-type  ::= 'type' identifier ( ':' bound )?   # 'type Item' (optionally bounded)
assoc-val   ::= identifier ':' type           # 'BITS: int' — a required associated value
generics    ::= '[' type-param ( ',' type-param )* ']'
type-param  ::= identifier ( ':' bound )?     # bound: a spec → type param; a concrete type → value param
bound       ::= type-name ( '+' type-name )*  # a conjunction of specs
decorated-decl ::= decorator* declaration   # a decorator prefix leads any declaration (group 1)
decorator   ::= '#[' deco-item ( ',' deco-item )* ']'
deco-item   ::= identifier ( '(' deco-arg ( ',' deco-arg )* ')' )?
deco-arg    ::= type-name | const-expr        # derive(Encode, Decode), align(16), align(SIZE*2)
```

- **Type expressions.** A **name** with optional **type arguments** (`int`, `User`, `list[int]`,
  `Either[A, B]`); a **tuple type** `(A, B)`; an **array** `[T; N]` (the other use of `;`); a **channel**
  `chan[T]`, with Go-style direction — `<-chan[T]` is receive-only (a receiver) and `chan[T]<-` is
  send-only (a sender); or a **function type** `fn(P…) -> R` (group 5). A trailing **`?`** makes any type an
  **optional** — `str?`. A type name is just an identifier, so the set of built-in **numeric** types
  (`int`, `uint`, `float`, and any fixed-width `i32`/`u8`/`f64`/…) is a **stdlib** decision, not grammar.
- **`struct` / `enum`.** A **product** with named, typed fields or a **sum** with variants that each carry
  an optional payload — `Circle(float)`, `Rect(float, float)`. Fields and variants are separated **exactly
  like statements** (a line break, or `;` inline) — there is **no `,`** between them; the `,` inside a
  payload `(A, B)` is an ordinary list. Both may be generic — `enum Either[X, Y] { … }`.
- **Enum discriminants.** When **every** variant is fieldless, a variant may take an explicit integer
  **discriminant** — `enum Status { Ok = 200; NotFound = 404 }` — a C-style enum whose value is observable
  (`int(Status.Ok)` reads it, `Status.of(200) -> Status?` reverses). Values are compile-time constants,
  distinct; an unspecified one is the previous `+ 1` (from `0`). A **payload** enum keeps its tag **opaque**
  (match-only) and cannot take discriminants. The backing is `int` by one default rule — a specific width is
  an opt-in layout decorator (`#[repr]`), and the **wire form is the `Encode`/`Decode` impl**, never a
  decorator.
- **Field visibility & defaults.** A field is `pub` for external **instance access** (read/write of
  `u.field`); a non-`pub` field is module-private and **must carry a default**. There are **no zero values**
  — a non-optional field with no default is **required** at construction; the one implicit default is `nil`
  for a `T?` field (its natural absent state). So `struct Config { host: str; port: int? = 8080; tags: str? }`
  → `Config(host: "x")` gives `port = 8080`, `tags = nil`, and omitting `host` is an error.
- **Construction.** A type name is also its **constructor** — `User(id: 1, name: "x")` builds a struct (its
  fields become parameters in declaration order, positional then named) and `Circle(3.0)` an enum variant.
  The name **shares the value namespace** with functions, so a type and a function cannot share a name (Zerg
  has no overloading). The field-wise `T(...)` is **public and primitive**; a custom constructor is a named
  associated function (an inherent `impl`) that builds via `T(...)`. **`#[sealed]`** is _intended_ to demote
  `T(...)` to module-private (external code must then use a public custom constructor, the module still
  building with `T(...)` internally), but is **recognized-and-rejected, not yet implemented** this phase (see
  [Decorators](../core/decorators.md)).
- **`type X = Y`.** A **strong typedef** — a new, distinct type, not a transparent alias, lowering to `Y` at
  runtime. The `generics?` slot is **parsed** but a **generic** alias `type X[T] = …` is **not yet
  implemented** this phase (rejected in semantic analysis).
- **Compile-time constants (implicit).** Compile-time folding is **implicit** and independent of the `const`
  keyword (which only marks a shadow-proof binding — Group 4). Any binding — `:=` or `const` — whose
  initializer is a `const-expr` is **folded at compile time** and may be used where a compile-time value is
  required: an array length `[T; N]`, an enum discriminant, a decorator argument, a value-generic argument.
  Using such a name where its initializer is **not** const-foldable is a compile error at the use site. A
  `const-expr` is an `expr` the compiler folds with **no evaluation engine** — literals, other const-foldable
  names, discriminants, and operators; **no function calls** (so `sizeof`/`len` are not const-exprs). A spec
  may require an **associated value** — `BITS: int` — each impl supplies with `BITS := 32` (a `val-bind`).
- **Generics & bounds.** A parameter list `[T, …]` may bound each parameter to specs — `[T: Ord]`, a `+`
  conjunction `[K: Hash + Eq, V]`. The same `bound` is a spec's **super-spec** (`spec Ord: Eq` — an
  `impl Ord` then also needs `impl Eq`, and `Ord`'s body may call `Eq` on `This`). An `impl`'s own type
  params sit **after `impl`** — `impl[T] Summable for list[T]` — so `T` is usable in the target. Generics
  **monomorphize**: each distinct type argument yields its own specialized C function, so a bound is
  load-bearing (it names the impl to specialize, and `a < b` in generic code needs the bound that provides
  `<`) and a generic function is not a first-class value until instantiated. Soundness relies on
  **coherence** (one `impl Spec for Type` program-wide) and an **orphan rule** (own the spec or the type);
  generics are **invariant**. `#[dyn]` instead emits one shared witness-table body (size for speed), and the
  compiler can flag instantiation bloat. Explicit call-site type arguments are `f[T]` (disambiguated from an
  index by name resolution — group 4). **Value generics** let a parameter be a compile-time value with **no
  `const` keyword**: in `[X: Y]`, a spec `Y` makes `X` a type param, a concrete type `Y` makes `X` a value
  param (`[N: int]`, a primitive; composite deferred). A function's value param is **inferred** from an
  argument's type (`fn sum[N: int](xs: [int; N])`) while a type's is written in type position
  (`Matrix[3, 4]`). There is **no disjunction bound**
  (`T: A | B`) — a body could not know which methods `T` has, so it cannot monomorphize. To accept several
  types, **parameterize a spec** and write one impl per type: `spec Indexable[K]` with `impl Indexable[int]`
  (element) and `impl Indexable[Range]` (slice) is how `xs[k]` dispatches statically on `k`'s type, each impl
  keeping its own associated `Output` — or use an `enum` for a runtime choice.
- **`spec`.** A behavioral interface: members are **required** (a signature with no body), **provided** (a
  full method), or an **associated type** (`type Item` — a type the `impl` fills in, functionally determined,
  one per impl). A method takes **no explicit receiver** — `this` is implicit inside a method, reached through
  the instance it is called on; a `fn` that uses `this` with no instance bound is a compile error. The self
  type is **`This`**. `impl … for …` supplies a spec's methods (and its `type Item = …` bindings) for a type
  by hand.
- **Inherent `impl`.** A `for`-less `impl User { … }` adds methods **not tied to any spec** — a named
  constructor `User.from_json(…)` (an associated fn, no `this`, called `Type.f(…)`) or a private method
  `u.recompute()` (uses `this`, called `x.f(…)`). Every method and associated fn on a type shares one
  namespace, inherent or from a spec alike; a duplicate is an error.
- **Associated types.** An associated type makes a **single-output** protocol well defined: `for x in it`
  has one element type because `Iterator`'s `Item` is fixed per impl, not chosen per use as a generic
  `Iterable[T]` would allow. Reference it by **projection** in type position — `I.Item`
  (`fn collect[I: Iterator](it: I) -> list[I.Item]`), **chainable** when the projected type has its own
  associated type — `I.Item.Sub`. An `impl` supplies it with `type Item = int`. A spec's associated
  **value** is the value counterpart — `BITS: int` required, supplied by the impl as `BITS := 32`.
- **Decorators & `#[derive(…)]`.** A **decorator** `#[…]` is a compiler directive; its `decorator*` prefix
  leads **any declaration** (`decorated-decl`, group 1) and binds to it. Which decorators are valid on which
  declaration is a **semantic** rule — `#[derive(Encode, Decode)]` on a `struct`/`enum` asks the compiler to
  **generate** the canonical impls of the named specs by reading the type's structure (see
  [Derive & Default Behavior](../core/derive.md)); a logging decorator would sit on a `fn`. Decorators are a
  **fixed, compiler-owned set** — users cannot define new ones (Zerg has no macros); an **unknown or
  misspelled decorator is a compile error**, never silently dropped. `#[derive]` is the one the compiler
  reads; `#[dyn]`, `#[test]`, `#[sealed]` and the layout directives (`#[repr]` / `#[packed]` /
  `#[align]`) are reserved names, recognized-and-rejected until built. `#[` is the one `#` that is not a
  comment — the lexer
  peeks one
  character.

## Group 8 — Null-safety & Errors

Failure comes in **two tiers**. A **recoverable** failure is an ordinary value of a sum type —
`Either[X, Y]`, `Result[T]` = `Either[T, Err]`, and `T?` = `Either[T, nil]` with the placeholder `nil`. A
**bug** is an **abort** that unwinds the stack (running `defer`s). Six operators bridge the tiers:

```text
coalesce-expr ::= or-expr ( '??' coalesce-rhs )?
coalesce-rhs  ::= coalesce-expr | diverge
diverge       ::= 'break' | 'continue' | return | raise
raise         ::= 'raise' expr ( 'from' expr )?
guard-expr    ::= 'guard' block
postfix       += '?' | '!' | '?.' identifier
```

- **`x?`** — **propagate**: unwrap the `Left`, or **early-return** the `Right` from the enclosing function.
- **`a ?? b`** — **default**: `a`'s `Left` if present, else `b`; loosest binary, **right-associative**,
  short-circuits. The right side may instead **diverge** — `x ?? break`, `v ?? return nil`, `p ?? raise e` —
  since `break` / `continue` / `return` / `raise` never yield a value.
- **`a?.b`** — **optional chain** (`T?` only): read `.b` when `a` is present, else short-circuit to `nil` in
  place (it never returns from the function, unlike `?`).
- **`x!`** — **force-unwrap**: unwrap the `Left`, or **raise `UnwrapError`** (a value→abort hatch). Logical
  negation is the keyword `not`, so postfix `!` is free.
- **`raise e`** — **abort** carrying an `Err` (value→abort); **`raise e from c`** records `c` as `e`'s cause.
- **`guard { … }`** — **demote** any abort inside the block back to a value, yielding `Result[T]`
  (abort→value). It is the sole way back from the abort tier, so a guarded abort is an ordinary `Result`
  handled by the same `?` / `??` / `match`.

## Group 9 — Concurrency

Concurrency is **coroutines + channels only** (CSP) — no shared mutable state, no locks, no join/handle.

```text
spawn-stmt  ::= 'spawn' expr
send-stmt   ::= expr '<-' expr
close-stmt  ::= 'close' '(' expr ')'          # end this stream early; expr is a channel
chan-new    ::= 'chan' '[' type ']' '(' expr? ')'
chan-type   ::= 'chan' '[' type ']'           # bidirectional
              | '<-' 'chan' '[' type ']'      # receive-only (a receiver), Go-style
              | 'chan' '[' type ']' '<-'      # send-only (a sender), Go-style
recv-base   ::= '<-' recv-base | primary
select-stmt ::= 'select' '{' select-arm+ '}'
for-select  ::= 'for' 'select' '{' select-arm+ '}'
select-arm  ::= recv-arm | send-arm | '_' '=>' stmt
recv-arm    ::= ( ( identifier | '_' ) ':=' )? '<-' expr '=>' stmt
send-arm    ::= expr '<-' expr '=>' stmt
```

- **`spawn f(args)`** starts a **fire-and-forget** coroutine (Go's `go`) — no handle, no join; you observe
  it only through channels. Captures are restricted to immutable values and channels.
- **`chan[T](cap?)`** builds a channel — capacity `0` (the default) is an unbuffered **rendezvous**. A bare
  `chan[T]` is bidirectional and narrows to `<-chan[T]` / `chan[T]<-` via a type annotation.
- **`ch <- v`** sends (no value; blocks or aborts on a closed channel). **`<-ch`** receives, yielding
  **`T?`** — `nil` once the stream has ended, and `nil` every time after. A **crash** close is a failure
  rather than an absence and is **raised**, carrying the producer's own `Err`. So `<-ch ?? d`, `<-ch!`,
  `<-ch?` and `if v := <-ch { … }` are the receive's operators, and **`chan[T?]` is refused** — `nil` would
  otherwise mean both the value and the end.
- **`select { … }`** is the only multi-way wait: it PICKS one ready arm (fair ties) and runs it; a receive
  arm binds a plain **`T`**, because a cleanly ended channel drops out of the wait instead of firing.
  **`for select { … }`** is the same wait as a loop — one ready arm per round — and it ENDS when every
  watched receive channel has. There is no terminal arm. **`_`** fires when nothing is ready **now**
  (non-blocking) and is not an answer to an exhausted select; it is **contextual**, special only as an arm
  head, and the only bare identifier an arm may open with. There is **no `yield`**.
- **`close(ch)`** ends a stream **early**. A channel normally closes **by itself** when its last sender
  leaves — the everyday form, and the only one a crashing producer can take. `close` is a **statement and
  not a call**: it is a keyword, names no function and yields no value, so it cannot be passed, bound or
  spawned, and `defer` spells it out as its one non-expression form (**`defer close(ch)`**). It marks the
  **channel**, not a holder — every handle stays readable, buffered values still drain, closing twice
  changes nothing, and a **receive-only** end may not close.

## Group 10 — Modules & Programs

Source nests as **program › package › module (a directory) › file**.

```text
import-stmt ::= 'import' ( import-spec | '(' import-spec* ')' )
import-spec ::= 'pub'? import-path ( 'as' identifier )?
import-path ::= str-lit                     # "util/text" — a '/'-separated module path
init-decl   ::= 'init' '(' ')' block
```

- **`pub`** (already a prefix on every declaration) is the one visibility marker: a plain declaration is
  **module-private**, `pub` exposes it to the **rest of the package**, and a package's public API is the
  `pub` surface of its **root module**.
- **`import "path"`** binds a **namespace** — the path is a **string** and its **last segment** names the
  binding (`import "util/text"` binds `text`), reached with `.`: `text.split(…)`. **`as`** renames it
  (`import "a/text" as at`), which is how two imports that share a last segment coexist; a collision with a
  local name is an error, resolved by `as`. A leading **`pub`** on the spec **re-exports** the namespace onto
  this module's surface — `import pub "util/text"` — the single mechanism by which a root module builds a
  package's public API. **Many imports group** in a parenthesized list, one self-delimiting spec per line:

  ```text
  import (
      pub "util/text"
      "util/text/abc" as cc
  )
  ```

  No separator is needed (each spec is `pub`? then a string then an optional `as name`), and the line breaks
  inside the `( … )` are insignificant. There is **no selective (`from … import`) or glob import** — to use a
  member unqualified, bind it locally (`split := text.split`), since a function is a value.

- **`init()`** is a module's **lazy** setup, run on first use. A module may declare **several** `init()`
  blocks; they run in **declaration (FIFO) order**, each **exactly once**. There are **no mutable globals in
  safe code**: a top-level binding may not be `mut` — a top-level `:=` is an **immutable module constant**
  evaluated at init. The one exception is a `mut` binding inside a module-level **`unsafe { … }`** group
  (group 12); the safe way to share mutable
  global state is an immutable `:=` holding a stdlib **`Atomic[T]`**.
- A **program** is a build rooted at an entry file that defines a top-level `fn main(…) -> Result[nil]`;
  `main` is an ordinary function (not reserved). **`package`** is the distribution/versioning unit — a
  directory tree selected by the build tool, with **no in-source `package` declaration**.

## Group 11 — Resource cleanup (`defer` & `del`)

Three constructs share one axis — **when** cleanup fires.

```text
defer-stmt ::= 'defer' ( expr | close-stmt )   # an expression, or the one statement that is not one
del-stmt   ::= 'del' identifier
```

- **`defer expr`** runs `expr` at the **enclosing block's exit**, on **every path out** — normal, `return`,
  or an abort unwind — in last-scheduled-first order. It is the procedural tool for a scope-bound effect
  (release a lock, flush a buffer, close a scope-local resource). It also takes **`close(ch)`** (group 9),
  spelled out because `close` is a keyword rather than a callee, so `defer expr` alone could never reach it.
- **`del name`** revokes that name's access to its storage **now**; the storage is freed only if the revoked
  access was the **owning** one and no other holder remains. For a `Ref[T]` / `chan` it drops a refcount
  **and** revokes the name, so the name is unusable afterwards — `del ch` is not how a stream is ended
  (that is `close(ch)`, or the binding's scope exit).
- The third point on the axis — a **`Ref[T]` drop** at the last holder's scope exit — is not a statement;
  it falls out of scope ownership. The dividing question is `defer` vs `Ref[T]`: does the resource escape
  its scope? No → `defer`; yes → `Ref[T]`.

## Group 12 — Unsafe (raw pointers & inline assembly)

The one door to bare-metal. Everything here is legal **only inside `unsafe`**; the safe world (`Ref[T]`,
`mut &`, no mutable globals, checked `T?`) is untouched.

```text
unsafe-expr  ::= 'unsafe' block            # in a function: block-expression; unsafe ops legal only here
unsafe-group ::= 'unsafe' '{' stmt-sep* ( unsafe-item ( stmt-sep+ unsafe-item )* stmt-sep* )? '}'
unsafe-item  ::= decorated-decl | binding  # a decl (unsafe here); a 'mut' binding is a mutable global
fn-decl     ::= 'pub'? 'unsafe'? 'mut'? 'fn' …    # 'unsafe fn' — the single-function form
ptr-type    ::= 'ptr' ( '[' type ']' )?    # 'ptr' = raw address; 'ptr[T]' = typed pointer
asm-expr    ::= 'asm' '(' str-lit ( ',' asm-operand )* ')'
asm-operand ::= 'in' '(' str-lit ')' expr | 'out' '(' str-lit ')' lvalue
              | 'inout' '(' str-lit ')' lvalue | 'clobber' '(' str-lit ( ',' str-lit )* ')'
```

- **`unsafe { … }`.** Two shapes, told apart by **position**. In a **function** body it is a
  **block-expression** (it yields the block's value) inside which raw operations are legal; anywhere else
  they are a compile error. At **module** level the same `unsafe { … }` is a **declaration group**: every
  item inside is unsafe — a `fn` is an unsafe fn, and a `mut` binding is a module-private mutable **global**
  (persistent; the group scopes names to the module, it is not a fresh value scope). `unsafe` is a **trust
  boundary** — the compiler makes no memory-safety guarantee about its contents; the author vouches for
  them. **`unsafe fn`** is the single-function form; an unsafe fn may only be **called** from another
  `unsafe` context.
- **Global mutable state.** The one exception to _no mutable globals_ (group 10) is a `mut` binding **inside
  a module-level `unsafe { … }` group** — the bare-metal escape hatch (a page table plus the functions that
  touch it, grouped together). There is **no `unsafe mut` prefix** and no `static` keyword. A mutable global
  is **module-private** (never `pub`). Prefer the **safe** alternative — an immutable `:=` holding a stdlib
  **`Atomic[T]`** — which shares mutable global state across cores with no `unsafe` (the binding is
  immutable; the `Atomic`'s interior is not). **Atomics are stdlib, not grammar**: `Atomic[T]` with `load` /
  `store` / `swap` / `fetch_add` / `compare_swap` and a memory-ordering argument. **[not yet]** — it needs
  `Ref[T]`, for
  **`Atomic[int]`** with **sequential consistency**; the **memory-ordering argument** and a **generic
  `Atomic[T]`** are **[not yet]**.
- **Raw pointers (`ptr` / `ptr[T]`).** `ptr` is a platform-width raw **address** (C's `void*` / `uintptr`);
  `ptr[T]` types that address to a pointee `T` (same width — `[T]` only types the load/store/offset). Because
  `T` is any type, **function pointers** fall out for free — `ptr[fn(int) -> nil]` (an interrupt vector) — as
  do `ptr[ptr[T]]` and the bare `ptr`. A `ptr` is **inherently nullable** (address `0`) and is **orthogonal
  to `T?`** — there is **no `ptr[T]?`**; test with `p == 0`. The **type** may appear in a signature or field
  (to describe pointer-shaped data), but every **operation** — `addr(x)`, `p.load()`, `p.store(v)`,
  `p.offset(n)`, casts, volatile/atomic — is an **unsafe stdlib intrinsic** (not grammar), legal only inside
  `unsafe`. There is **no `*`/`&` operator**; the bracket type keeps pointers consistent with `list[T]` /
  `Ref[T]` / `chan[T]`.
- **Inline assembly (`asm`).** A template string (triple-quoted for multi-line) with operands binding Zerg
  values to registers/constraints. `out` / `inout` / `clobber` are **contextual** (special only in an asm
  operand list; `in` is already a keyword). The constraint string (`"rax"`, `"r"`, `"m"`, …) is opaque here —
  its meaning is the target backend's. `asm` is `unsafe`-only; a **syscall** is issued this way.

## What is specified and not built

Every form below is **[not yet]**: the grammar defines it, `zerg` refuses it **by its own
name**, and no program that uses one compiles into something else. This list is not prose —
`scripts/refuse-check.sh` holds a case for each, so a form that quietly starts working, or
quietly starts failing differently, fails the gate.

| Group | Form                                                                                   |
| ----- | -------------------------------------------------------------------------------------- |
| 2     | command literal `` `…` `` and its interpolating form `` f`…` ``                        |
| 3     | destructuring binding `(a, b) := …`                                                    |
| 4     | a callee that is not a name — `f[T](…)`, `fs[0](…)`, `p?.m(…)`                         |
| 5     | f-string `{x!r}` / `{x=}` / `{x:spec}`                                                 |
| 5     | a named argument `f(b: 1)` — arguments bind by position                                |
| 6     | generic `struct` / `enum`; a generic METHOD; a bound naming two specs (`T: Eq + Ord`)  |
| 6     | a closure that captures; an untyped closure parameter                                  |
| 7     | `with`; struct, list, tuple and or-patterns; `pattern as name`; `if v := <enum>`       |
| 8     | array type `[T; N]`; `spec` as a type or a dispatch; a `spec` member with a body       |
| 8     | associated function `Type.f(…)`; an associated type or value, in a `spec` or an `impl` |
| 8     | an `impl` on a built-in type (`impl Tag for int`)                                      |
| 8     | every decorator but `#[derive(…)]`                                                     |
| 8     | a map key that is not an `int` or a `str` — a key needs `Hash`                         |
| 8     | the built-ins `Ref` / `deref` / `sizeof[T]` / `alignof[T]`                             |
| 12    | `unsafe` block, `asm`, `ptr` / `ptr[T]`                                                |

A `spec`'s **required members are enforced** on an `impl … for …` even though nothing
dispatches on the spec — that much a declared interface means. What is enforced is the
**signature**: arity, and at each position the parameter's name, type and `mut &`, and
whether it has a default, plus the return type. **Position** matters because Zerg has
positional calls; the default's **value** does not, because its presence is interface and
its value is what runs. A **super-spec** (`spec Ord: Eq`) is required too, and an `impl`
naming a spec no file declares is an error. Everything else about `spec` is on the list.

## Editor tooling

Syntax highlighting for Neovim lives under [`editors/nvim/`](../../editors/nvim) as classic Vim syntax files:

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

To eyeball the result, open any of the runnable samples under [`examples/`](../../examples) — a numbered tour
of the language that the highlighting colours.
