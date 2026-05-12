<!-- markdownlint-disable MD013 -->

# Zerg Language Reference

Authoritative description of the Zerg surface that ships through v0.12.
Each production reflects what the v0.12 toolchain (parser + typeck)
actually accepts and runs. v1.0 source-stability promises will be made
against this document.

## Overview

Zerg is a small, statically-typed, ahead-of-time compiled language that runs
through two halves of the same toolchain: an interpreter (`zerg run`) and a
C-codegen back-end (`zerg build`). The two halves obey the **parity rule**:

- For sequential code, `zerg run` and the compiled binary produce
  byte-identical stdout.
- For concurrent code, both halves are correct under any valid scheduling;
  well-formed programs do not depend on tie-break order.

A program is a sequence of top-level declarations and statements; there is no
`main` entry. Files may carry an optional `# requires: vMAJOR.MINOR` marker
that gates parsing against the toolchain version.

## Lexical structure

### Comments

```text
# this is a comment
```

`#` introduces a line comment that runs to the next newline. The shebang
`#!/usr/bin/env zerg` is a regular comment (the `!` is not special).

### Identifiers

```ebnf
ident      = letter ( letter | digit | '_' )*
letter     = 'a'..'z' | 'A'..'Z' | '_'
digit      = '0'..'9'
```

Identifiers are ASCII-only. `_` is a normal identifier character; in `match`
and `select` arms a bare `_` token also serves as the wildcard.

### Keywords

The lexer's reserved word set as of v0.13:

```text
and    as     asm      break    const     continue defer    elif
else   enum   false    fn       for       if       impl     import
in     let    loop     match    mut       nil      nop      not
or     print  pub      return   select    spawn    spec     struct
this   true   while    xor
```

`let` remains reserved at the lexer level but has **no admitted
syntactic form** at v0.11: the immutable-binding `let X := …` shape was
retired in favour of the bare `X := …` form (see `bind_stmt`). The token
is held in reserve so future surface work cannot collide with it.

### Reserved type names

The following names are reserved at type position. User declarations
(`struct`, `enum`, `spec`) of these names reject at typeck:

| Name        | Introduced | Meaning                                     |
| ----------- | ---------- | ------------------------------------------- |
| `int`       | v0.0       | 64-bit signed integer                       |
| `float`     | v0.1       | 64-bit IEEE 754                             |
| `bool`      | v0.0       | `true` / `false`                            |
| `str`       | v0.0       | UTF-8 string, immutable                     |
| `byte`      | v0.2       | 8-bit unsigned integer                      |
| `rune`      | v0.2       | 32-bit Unicode codepoint                    |
| `list`      | v0.2       | `list[T]` constructor                       |
| `tuple`     | v0.2       | `tuple[T1, T2, ...]` constructor (>= 2)     |
| `Option`    | v0.6       | built-in enum `Option[T]`                   |
| `Result`    | v0.6       | built-in enum `Result[T, E]`                |
| `chan`      | v0.7       | `chan[T]` constructor                       |
| `WaitGroup` | v0.7       | synthetic struct returned by `wait_group()` |
| `never`     | v0.9       | bottom type (no values)                     |

### Literals

| Form                          | Type                     |
| ----------------------------- | ------------------------ |
| `42`, `0xFF`, `0b1010`, `0o7` | `int`                    |
| `1_000_000`                   | `int`                    |
| `3.14`                        | `float`                  |
| `'A'`                         | `byte`                   |
| `'漢'`                        | `rune`                   |
| `"text"`                      | `str`                    |
| `true`, `false`               | `bool`                   |
| `nil`                         | `Option[T].None` (v0.6+) |

Integer literals admit `_` as a digit separator, never adjacent to a prefix
or doubled. Float requires digits on both sides of the dot (`.5` and `1.`
reject). String escapes: `\n`, `\t`, `\r`, `\\`, `\"`, `\'`, `\0`, `\{`.
String interpolation (`{expr}` inside a literal) is **not** admitted at
v0.9 — it rejects at lex time.

## Grammar (EBNF)

Convention: `UPPER` denotes a token kind; `lower` denotes a non-terminal;
`{ x }` is zero-or-more, `[ x ]` is optional, `|` is alternation. Implicit
NEWLINE separates top-level declarations and statements; inside `(`, `[`
parens and inside the brace region of struct / match / impl / spec / enum
bodies, NEWLINE is transparent.

### Module

```ebnf
program     = { stmt }
stmt        = decl | top_stmt
decl        = import_decl
            | [ 'pub' ] ( fn_decl | struct_decl | enum_decl | spec_decl )
            | impl_decl
top_stmt    = simple_stmt | compound_stmt
```

### Imports

Imports are top-level only. Resolution is filesystem-relative (sibling `.zg`)
with a fall-through to the toolchain-embedded stdlib: a bare `import "name"`
checks the sibling first and, on miss, resolves as `std/name`. The `std/`
prefix is the implicit default — an explicit `import "std/name"` is the
same module and skips the sibling check. The `sys/<name>` prefix is
always required (platform-specific modules opt in deliberately).

```ebnf
import_decl    = 'import' ( import_entry
                          | '(' { NEWLINE import_entry } NEWLINE ')' )
import_entry   = STRING [ 'as' IDENT ]
```

A bare `import "name"` binds the module under its path string; `as alias`
overrides. Reserved keywords cannot be used as alias names.

### Visibility

`pub` is a top-level modifier on `fn`, `struct`, `enum`, `spec`, and
`impl`-method `fn`. Field-level visibility is not admitted.

### Function declarations

```ebnf
fn_decl     = 'fn' IDENT [ type_params ] '(' [ param_list ] ')'
              [ '->' type ] block
type_params = '[' type_param { ',' type_param } ']'
type_param  = IDENT [ ':' type_bound ]
type_bound  = type { '+' type }
param_list  = param { ',' param }
param       = IDENT ':' type
```

### Struct / enum / spec

```ebnf
struct_decl = 'struct' IDENT [ type_params ] '{' { field NEWLINE } '}'
field       = IDENT ':' type

enum_decl   = 'enum' IDENT [ type_params ] '{' { variant NEWLINE } '}'
variant     = IDENT [ '(' type { ',' type } ')' ]

spec_decl   = 'spec' IDENT [ type_params ] '{' { spec_item NEWLINE } '}'
spec_item   = 'fn' IDENT [ type_params ] '(' [ param_list ] ')' [ '->' type ]
              [ block ]
```

A `spec_item` with a body declares a default implementation; without a body
it is an abstract method that conforming impls must provide.

### impl

```ebnf
impl_decl   = 'impl' [ type_params ] type [ 'for' type ]
              '{' { [ 'pub' ] fn_decl NEWLINE } '}'
```

The shape `impl T for Spec` is a spec-conforming impl; `impl T` is an
inherent impl. Generic impls (`impl[T] Box[T] for Spec`) cover every
monomorphisation. The orphan rule rejects `impl A.X for B.Y` when both
`A` and `B` are foreign modules.

### Statements

```ebnf
simple_stmt   = bind_stmt | mut_stmt | const_stmt | assign_stmt | expr_stmt
              | return_stmt | break_stmt | continue_stmt
              | print_stmt | nop_stmt
              | spawn_stmt | defer_stmt | send_stmt
compound_stmt = if_stmt | for_stmt | match_stmt | select_stmt
              | fn_decl | struct_decl | enum_decl | spec_decl | impl_decl

bind_stmt     = bind_target ( ':=' expr | ':' type '=' expr )
mut_stmt      = 'mut' bind_target ( ':=' expr | ':' type '=' expr )
const_stmt    = 'const' IDENT     ( ':=' expr | ':' type '=' expr )
bind_target   = IDENT | tuple_pattern_lhs                ; LHS, not pattern
tuple_pattern_lhs = '(' IDENT ',' IDENT { ',' IDENT } ')'  ; >= 2 names

assign_stmt   = lvalue assign_op expr
lvalue        = IDENT | postfix_expr_index_only          ; IDENT or xs[i]
assign_op     = '=' | '+=' | '-=' | '*=' | '/=' | '%='
              | '&=' | '|=' | '^=' | '<<=' | '>>='

return_stmt   = 'return' [ expr ] [ 'if' expr ]
break_stmt    = 'break'    [ 'if' expr ]
continue_stmt = 'continue' [ 'if' expr ]

print_stmt    = 'print' expr
nop_stmt      = 'nop'

if_stmt       = 'if' expr block { 'elif' expr block } [ 'else' block ]

for_stmt      = 'for' block                              ; infinite
              | 'for' expr block                         ; while-style
              | 'for' IDENT 'in' for_head block
for_head      = expr '..'  expr                          ; half-open range
              | expr '..=' expr                          ; closed range
              | expr                                     ; iter (list / chan)

match_stmt    = 'match' expr '{' { match_arm NEWLINE } '}'
match_arm     = pattern [ 'if' expr ] '=>' ( block | simple_stmt )

spawn_stmt    = 'spawn' fn_call_expr
defer_stmt    = 'defer' ( block | simple_stmt )           ; fn-body scope only
send_stmt     = expr '<-' expr                            ; chained send rejects

select_stmt   = 'select' '{' { select_arm NEWLINE } '}'    ; >= 1 arm
select_arm    = select_op '->' ( block | simple_stmt )
select_op     = '_'
              | '<-' expr                                  ; recv-discard
              | IDENT ':=' '<-' expr                       ; recv-bind
              | expr '<-' expr                             ; send

expr_stmt     = call_expr | method_call_expr | recv_expr   ; only these shapes
block         = '{' { stmt NEWLINE } '}'
```

A `simple_stmt` after `=>` or `->` is wrapped into a one-element block.

### Patterns

```ebnf
pattern         = literal_pattern
                | IDENT                                     ; bind / wildcard `_`
                | tuple_pattern
                | struct_pattern
                | enum_pattern
literal_pattern = INT | FLOAT | STRING | RUNE | 'true' | 'false' | 'nil'
tuple_pattern   = '(' pattern { ',' pattern } ')'           ; >= 2
struct_pattern  = IDENT '{' [ field_pattern { ',' field_pattern }
                              [ ',' '..' ] ] '}'
field_pattern   = IDENT [ ':' pattern ]                     ; shorthand binds field
enum_pattern    = qualified_name [ '(' [ pattern { ',' pattern } ] ')' ]
qualified_name  = IDENT { '.' IDENT }                       ; e.g. Color.Red
```

### Types

```ebnf
type            = type_atom [ '?' ]                         ; '?' means Option[T]
type_atom       = IDENT [ '.' IDENT ] [ type_args ]         ; named, optionally qualified
                | 'list'  '[' type ']'
                | 'tuple' '[' type ',' type { ',' type } ']' ; >= 2
type_args       = '[' type { ',' type } ']'                 ; >= 1
```

Channel types are written as the constructor `chan[T]()` / `chan[T](N)`
(see expressions); `chan[T]` is not a syntactic type — it lives only at the
constructor call. `T?` desugars to `Option[T]`; `T??` rejects.

### Expressions

Precedence, lowest to highest. Each level is left-associative unless noted.

| #   | Operators                      | Notes            |
| --- | ------------------------------ | ---------------- |
| 1   | `??`                           | right-assoc      |
| 2   | `or`, `xor`                    |                  |
| 3   | `and`                          |                  |
| 4   | `not` (prefix)                 | right-assoc      |
| 5   | `==` `!=` `<` `>` `<=` `>=`    | non-associative  |
| 6   | `\|`                           | bitwise or       |
| 7   | `^`                            | bitwise xor      |
| 8   | `&`                            | bitwise and      |
| 9   | `<<` `>>`                      | shifts           |
| 10  | `+` `-`                        | additive         |
| 11  | `*` `/` `//` `%`               | multiplicative   |
| 12  | unary `-`, `~`, `<-`           | right-assoc      |
| 13  | postfix `()` `[]` `?` `?.` `.` | left-assoc chain |
| 14  | atoms                          |                  |

Range operators (`..`, `..=`) are not part of the expression grammar; they
only appear in `for x in ...` heads and in slice brackets (`xs[lo..hi]`).

```ebnf
expr            = coalesce_expr
coalesce_expr   = or_expr [ '??' coalesce_expr ]
or_expr         = and_expr   { ( 'or' | 'xor' ) and_expr }
and_expr        = not_expr   { 'and' not_expr }
not_expr        = 'not' not_expr | cmp_expr
cmp_expr        = bitor_expr [ cmp_op bitor_expr ]
cmp_op          = '==' | '!=' | '<' | '>' | '<=' | '>='
bitor_expr      = bitxor_expr { '|' bitxor_expr }
bitxor_expr     = bitand_expr { '^' bitand_expr }
bitand_expr     = shift_expr  { '&' shift_expr }
shift_expr      = add_expr    { ( '<<' | '>>' ) add_expr }
add_expr        = mul_expr    { ( '+' | '-' ) mul_expr }
mul_expr        = unary_expr  { ( '*' | '/' | '//' | '%' ) unary_expr }
unary_expr      = ( '-' | '~' | '<-' ) unary_expr | postfix_expr
postfix_expr    = atom { postfix_op }
postfix_op      = '(' [ expr { ',' expr } ] ')'              ; call
                | '[' index_or_slice ']'                     ; index / slice
                | '.' IDENT [ '(' [ expr { ',' expr } ] ')' ] ; field / method
                | '?.' IDENT                                 ; safe nav
                | '?'                                        ; propagation
index_or_slice  = expr | expr '..' [ expr ] | expr '..=' expr
                | '..'  [ expr ] | '..=' expr
atom            = INT | FLOAT | STRING | RUNE
                | 'true' | 'false' | 'nil' | 'this'
                | IDENT
                | list_lit | tuple_lit | paren_expr
                | struct_lit
                | anon_fn_expr
list_lit        = '[' [ expr { ',' expr } [ ',' ] ] ']'
tuple_lit       = '(' expr ',' [ expr { ',' expr } ] [ ',' ] ')' ; >= 2 elements
paren_expr      = '(' expr ')'
struct_lit      = [ IDENT '.' ] IDENT '{' [ field_init { ',' field_init }
                                            [ ',' ] ] '}'
field_init      = IDENT ':' expr
anon_fn_expr    = 'fn' '(' [ param_list ] ')' [ '->' type ] block
```

`Ident '{'` parses as a struct literal only when the brace contents look
like `IDENT ':' ...` or `'}'`. A brace that opens with `IDENT ':='` (a
walrus binding) belongs to the surrounding statement (e.g. `if cond { x
:= 1 }`), as does any other shape that does not match the struct-literal
prefix.

### Special identifiers

A handful of identifiers carry semantics that the parser does not
distinguish from any other `IDENT`: `chan`, `wait_group`, `close`, `len`,
`push`, `clone`. They are lexed as `IDENT`, parse through the regular
postfix chain (`atom { postfix_op }`), and gain meaning at typeck.

- `chan[T]()` and `chan[T](N)` parse as `IDENT '[' type ']' '(' [expr] ')'`
  — an index on `chan` followed by a call. Typeck recognises `chan` and
  reinterprets the shape as a channel constructor.
- `wait_group()`, `close(ch)`, `len(x)`, `push(xs, v)`, `clone(xs)`,
  `bytes(s)` (v0.14), `to_str(buf)` (v0.14) parse as ordinary function
  calls; typeck binds them to the corresponding builtins.

Because the parser does not reserve these names, a local binding may
shadow them (`mut chan := 5`). The binding succeeds; any subsequent use
that relied on the builtin meaning fails at typeck. Avoid shadowing
these names in user code.

## Type system

### Primitives

| Type    | Storage         | Notes                                  |
| ------- | --------------- | -------------------------------------- |
| `int`   | 64-bit signed   | overflow is undefined-on-purpose       |
| `float` | 64-bit IEEE 754 | no implicit int/float coercion         |
| `bool`  | -               | `true` or `false`                      |
| `str`   | UTF-8 immutable | `s[i]` returns the i-th rune codepoint |
| `byte`  | 8-bit unsigned  | default for ASCII rune literals        |
| `rune`  | 32-bit          | default for non-ASCII rune literals    |

### Composites

| Type            | Notes                                                     |
| --------------- | --------------------------------------------------------- |
| `list[T]`       | growable; deep-copy on `clone(xs)`; `push(xs, v)` mutates |
| `tuple[T1,...]` | fixed-size, >= 2 elements                                 |
| struct types    | nominal; declaration-order field equality                 |
| enum types      | tag + optional payload tuple                              |
| spec types      | fat pointer `(data, vt)`; heap-boxes the value            |
| `chan[T]`       | unbuffered (rendezvous) or buffered FIFO                  |
| `WaitGroup`     | synthetic struct returned by `wait_group()`               |
| `Option[T]`     | built-in enum, variants `Some(T)` and `None`              |
| `Result[T, E]`  | built-in enum, variants `Ok(T)` and `Err(E)`              |
| function values | anon-fn expressions; immutable capture                    |

### `never` (v0.9)

`never` is the bottom type. A fn declared `-> never` cannot return — every
control-flow path must diverge (call another `-> never` fn, run an
unbounded loop with no break, etc.). `never <: T` for every concrete `T`,
so a `-> never` call is well-typed in any value position. The IDENT
`never` is recognised only at type position; user-declared `struct never`,
`enum never`, `spec never` reject. The only `-> never` calls in the v0.9
surface are `os.exit` and any user fn declared `-> never`.

### Subtyping and inference

- `never <: T` for every concrete `T`.
- `T -> T?` lift at boundaries: a bare `T` flowing into a `T?` slot is
  implicitly wrapped as `Option.Some(value)`. Boundaries are: fn argument,
  bare / mut / const initialiser with annotation, return expression,
  struct-literal field, list-element type under `list[T?]`.
- Bidirectional inference at call sites: generic type-args are inferred
  from argument shapes and from the surrounding expected type.
- No implicit numeric coercion: `int + float` is a type error.

### Equality and ordering

`==` / `!=` derive structurally on lists, tuples, structs, and enums (with
or without payloads). Recursion bottoms at primitive equality. `<`, `>`,
`<=`, `>=` are admitted on `int`, `float`, `byte`, `rune`, and `str`
(byte-lexicographic for strings).

## Semantics

### Evaluation order

Strict left-to-right, eager. Function arguments evaluate in declaration
order before the call. Short-circuit applies to `and` and `or`.

### Ownership / borrow rules (v0.3)

Composite values (`list`, tuple, struct, enum payload, spec-typed) are
**moved** on bind:

- `ys := xs` and `mut ys := xs` move `xs` into `ys`. Reading `xs`
  thereafter is rejected at compile time.
- `return x`, including a value in a struct / tuple / list literal, and
  binding into a tuple-destructure pattern are move sites.
- Function calls implicitly **share-borrow** composite arguments: the
  callee may read but not mutate; the caller retains ownership when the
  call returns.
- Primitives (`int`, `float`, `bool`, `byte`, `rune`, `str`, enums without
  payloads) copy on bind.

Read sites (no move): `print x`, `len(x)`, `x[i]`, `x.field`, slicing,
`for v in x`, all fn calls, `match` scrutinee.

`clone(xs)` opts back into v0.2-style deep copy semantics.

### List mutation

Through a top-level `mut`-bound list:

- `xs[i] = v` writes element `i`. Bounds-checked at run time.
- `push(xs, v)` (fn or `xs.push(v)` method) appends.

Compound assignment (`xs[i] += 1`) is not admitted at v0.9.

### `str` ↔ `list[byte]` bridge (v0.14)

The v0.14 surface adds three primitives that turn `str` (otherwise
opaque to user code) into a manipulable byte buffer and back:

- `len(s)` / `s.len()` — byte count of `s`. Returns `int`. Replaces the
  v0.2 rune-count reading, which was dead code (typeck rejected `str`).
- `bytes(s)` / `s.bytes()` — fresh `list[byte]` containing a copy of
  `s`'s bytes. The returned list is owned by the caller; mutating it
  does not affect `s` (which is immutable by design).
- `to_str(buf)` / `buf.to_str()` — fresh `str` owning a copy of `buf`'s
  bytes (`buf: list[byte]`). UTF-8 validity is **not** checked — invalid
  byte sequences pass through verbatim and may break downstream string
  operations, matching the v0.10 `io.read_file` contract.

Both `bytes` and `to_str` reserve their names against user redefinition
the same way `len` / `push` / `clone` do. The method-form
desugaring is the same path the list builtins use, so `xs.push(v)` and
`s.bytes()` share one dispatch rule.

These primitives unblock the pure-Zerg `strings.zg` rewrite: with
byte-level access and a byte → str constructor, the v0.8 string
operations (`split`, `trim`, `replace`, etc.) become expressible
without `__builtin`.

### Closures (v0.7)

Anonymous functions capture only **immutable** outer bindings. Captured
composites are deep-copied at closure creation; primitives copy by value.
Each capture is independent of the source after creation.

### Defer (v0.7)

`defer` registers a statement or block to run at fn-body exit in **LIFO**
order. v0.9 admits `defer` only at the immediate fn-body scope (never
inside `if` / `for` / `match` / inner blocks). The defer stack drains on
**every** exit path including `?` early-return — with one exception:

- **`os.exit(code)` does not drain defers** and does not join spawned
  tasks. This is an intentional v0.9 deviation, matching Go's
  `os.Exit` semantics. Cleanup before exit must be explicit.

### Concurrency (v0.7)

- `spawn fn-call` starts a fire-and-forget concurrent task. Argument
  must be a `CallExpr` or `MethodCallExpr`.
- `chan[T]()` is unbuffered (rendezvous); `chan[T](N)` is FIFO buffered.
- `ch <- v` sends; sender no longer owns `v`. Send to a closed channel
  panics.
- `<- ch` receives; result is `Option[T]`. `Some(v)` while the channel is
  open or has buffered values; `None` when drained-and-closed.
- `for v in ch` iterates until the channel is drained-and-closed; binds
  the inner `T` (auto-unwraps the `Option`).
- `close(ch)` is a built-in; closing twice panics.
- `select { ... }` multiplexes channel ops; arms are tried in declaration
  order on ties (`zerg build` codegen) or randomly (`zerg run`); empty
  `select` rejects at parse time.
- `wait_group()` returns a `WaitGroup`; methods `add(n)`, `done()`,
  `wait()`.

Since v0.12, `zerg build` routes spawn / chan / select / wait_group
through an M:N green-thread runtime: cheap user-space coroutines on a
worker pool sized from `ZERG_MAXPROCS` (default = host CPU count).
Scheduling is cooperative — coroutines yield at chan send/recv,
`select`, `wait_group.wait()`, and defer block exit. Each coroutine has
a fixed 256 KiB stack with a guard page; CPU-bound tight loops without
a yield point starve their worker (preemption is deferred to v0.13+).
The interpreter is unchanged (Go goroutines were already M:N). User-
visible semantics — including the parity rule — are identical to v0.7.

### Error propagation

The `?` postfix operator on a `Result[T, E]` or `Option[T]` expression
desugars to: if `Err` / `None`, early-return that variant; else evaluate
to the inner `T`. Legal only inside a fn whose return type is
`Result[U, E]` (matching `E`) or `Option[U]`.

`??` is nil-coalesce: `lhs ?? rhs` yields `T` when `lhs` is `Some(v)` /
`Ok(v)`, otherwise evaluates `rhs`. RHS evaluates only on `None` / `Err`.

`?.` is safe navigation: `obj?.field` yields `Option[U]` for `U` the
field's type. Chains carry `None` end-to-end.

### Inline assembly (v0.13, macOS arm64 only)

`asm { … }` admits a target-machine assembly body. The body is captured
verbatim between matching braces; brace counting is string-literal aware
(`}` inside `"…"` does not close the block). The keyword reserves at
v0.13; pre-v0.13 sources lex the bareword `asm` as `IDENT` and continue
to parse.

The interpreter cannot execute machine code. A program containing
`asm { … }` rejects under `zerg run` with the diagnostic

```text
inline asm requires 'zerg build' (interpreter cannot execute machine code)
```

`zerg build` lowers each block to a GCC `__asm__ volatile` statement.

#### `${name}` interpolation

Inside the body, `${name}` is a typed reference to a Zerg binding. `name`
must resolve to an in-scope binding whose type is one of:

- `byte` — lowered as a register-width input operand (`((uint64_t)z_<name>)`).
- `int` (v0.14) — lowered as a register-width input operand
  (`((int64_t)z_<name>)`).
- `list[byte]` — lowered as a pointer to the first byte
  (`((uintptr_t)z_<name>.data)`). Input only.

Any other type rejects at typecheck. `$$` is reserved (no escape for
literal `$` is defined; if needed in a future version, the rule lifts
explicitly). Unknown names reject with
`asm interpolation '${name}' references unknown name`.

#### `mut` bindings as output operands (v0.14)

When `name` resolves to a `mut`-declared binding of an output-capable
scalar type (`int` or `byte`), the interpolation lowers as a GCC `"+r"`
inout operand instead of an input. The asm body may read the binding's
initial value and the value written by the body is reflected back into
the Zerg binding when the block exits. This is the surface used to
return syscall results from `svc` traps to the surrounding Zerg code:

```zerg
# requires: v0.14
mut fd: int = 0
asm {
    mov x0, ${fd}             // initial value (0) loaded by GCC
    // ...syscall returning into x0...
    mov ${fd}, x0             // result written back to z_fd
}
```

`mut list[byte]` is **not** an output surface — the cgen lowers `.data`
for `list[byte]`, and pointer-rebinding through inline asm is not
supported. A `mut list[byte]` binding interpolates as an input operand
exactly like an immutable `list[byte]`.

GCC numbers operands across both the output and input lists in
declaration order, outputs first. A body with one output and two
inputs reaches them as `%0` (output), `%1`, `%2` (inputs) regardless of
the order the interpolations appear in the source.

#### Conservative clobber set (Darwin arm64 ABI)

Every `asm` block emits the same caller-saved clobber list:

- `"memory"`, `"cc"`
- GPRs: `x0`–`x17`, `x30` (lr)
- FP/SIMD: `v0`–`v7`, `v16`–`v31`

`x29` (the frame pointer) is **NOT** clobbered. It is a user-preserve
register: code inside `asm { … }` MUST NOT modify `x29` or `x30` without
saving and restoring their values. There is no compiler-level
enforcement of this contract; a violation produces silent debugger
breakage (unwind / `bt` chains snap) without any Zerg-level diagnostic.

#### Interaction with the M:N runtime

Asm blocks run synchronously on the calling coroutine's worker. An asm
body does not span a yield point — the M:N scheduler never invokes
`swapcontext` while a body is executing. Caller-saved register
preservation across coroutine yields is handled by the v0.12 runtime
(the same path the v0.7 corpus exercises) and is unaffected by the
presence of asm bodies.

A blocking syscall _inside_ an asm body parks the underlying OS worker
thread, not the calling coroutine — same starvation rule as a tight
CPU loop without a yield point. Use a Zerg-level wait for coroutine-
friendly blocking; reserve asm for short, non-blocking sequences.

#### Caveat: raw `svc` syscalls are not a stable Darwin ABI

The shipped `examples/13_asm.zg` uses raw `svc #0x80` traps because the
existing fixture pre-dated a libc-call lowering. Apple has been clear
that raw syscall numbers and the trap surface are not stable across
macOS releases. Programs that need long-term ABI stability should reach
libc via normal Zerg bindings rather than trap directly. v0.14 is
expected to migrate the in-tree examples to libc-call lowering; the
language-level surface (`asm { … }` + `${name}`) is unchanged by that
migration.

### Process exit (v0.9)

`os.exit(code: int) -> never` terminates the process with the given exit
code. Does not drain defers, does not join spawned tasks. Under the REPL,
"process exited with code N" is reported but the host process keeps
running.

`os.argv() -> list[str]` returns the command-line arguments. Index 0 is
the source / executable path under `zerg run` and `zerg build`-then-exec,
and the literal `"<repl>"` under the REPL. Tests must avoid printing
`argv[0]` because the path differs across halves.

## Modules

A module is a single `.zg` file. `import "name"` resolves via a
fall-through chain: first as `./name.zg` relative to the importing file
(sibling wins when present), then as the stdlib module `std/name`. The
`std/` prefix is the implicit default — `import "math"` and `import
"std/math"` reach the same module when no sibling shadows it; the
explicit form skips the sibling check. `import "sys/<name>"` resolves
platform-specific modules (e.g. `sys/path`) and the `sys/` prefix is
_always required_ — bare names never fall through to `sys/*`.

Per-imported-module gating: every imported file's `# requires:` line is
checked against the toolchain version. A v0.5 entry that imports a v0.6
module rejects.

Cross-module `impl` follows the orphan rule: at least one of `(Type,
Spec)` must be local to the impl's module.

## Standard library (provisional through v1.0)

- `std/io` — `read_file`, `write_file` (Result-typed).
- `std/strings` — `split`, `join`, `trim`, `starts_with`, `ends_with`,
  `contains`, `replace`, `to_upper`, `to_lower`, `parse_int`.
- `std/math` — `abs`, `min`, `max`, `gcd` (over `int`).
- `std/os` — `env`, `argv`, `exit`.
- `std/time` — `now_ms`, `sleep_ms`.

The first call to `time.now_ms()` returns `0`; subsequent calls return
milliseconds since that first call. `sleep_ms(ms)` blocks at least `ms`
milliseconds and clamps negative inputs to `0`.

See `docs/STDLIB.md` for per-fn signatures.

## Examples

Each block below is tagged so the v0.10 extractor knows how to wrap it. Tag
`program` parses as-is; `fn-body` wraps in
`fn main() -> int { ... ; return 0 }`; `expression` wraps in
`__ := <expr>` (the bare immutable-binding form; v0.11 retired the
`let` keyword).

### Hello

<!-- example: program -->

```zerg
print "hello, world"
```

### Variables and arithmetic

<!-- example: fn-body -->

```zerg
x := 1
y: int = 2
mut z := x + y
z = z * 10
print z
```

### Branching and loops

<!-- example: fn-body -->

```zerg
n := 10
mut sum := 0
for i in 0..n {
    if i % 2 == 0 {
        sum = sum + i
    }
}
print sum
```

### Tuples and lists

<!-- example: fn-body -->

```zerg
pair := (1, 2)
(a, b) := pair
print a + b
xs: list[int] = [1, 2, 3]
print len(xs)
```

### Struct and method

<!-- example: program -->

```zerg
struct Point { x: int, y: int }

impl Point {
    fn norm() -> int {
        return this.x * this.x + this.y * this.y
    }
}

p := Point { x: 3, y: 4 }
print p.norm()
```

### Enum and match

<!-- example: program -->

```zerg
enum Token {
    Eof,
    Ident(str),
    Number(int),
}

fn name(t: Token) -> str {
    match t {
        Token.Eof => return "eof"
        Token.Ident(s) => return s
        Token.Number(_) => return "number"
    }
    return "?"
}

print name(Token.Ident("hi"))
```

### Spec, impl, dispatch

<!-- example: program -->

```zerg
spec Printable {
    fn label() -> str
}

struct Cat { name: str }

impl Cat for Printable {
    pub fn label() -> str {
        return "Cat: " + this.name
    }
}

c := Cat { name: "Mittens" }
print c.label()
```

### Generics, Option, propagation

<!-- example: program -->

```zerg
fn first(xs: list[int]) -> Option[int] {
    if len(xs) == 0 {
        return Option.None
    }
    return Option.Some(xs[0])
}

fn double_first(xs: list[int]) -> Option[int] {
    v := first(xs)?
    return Option.Some(v * 2)
}

print double_first([10, 20])
print double_first([])
```

### Nullable types and coalesce

<!-- example: fn-body -->

```zerg
a: int? = 7
b: int? = nil
print a ?? 0
print b ?? 99
```

### Stdlib imports

<!-- example: program -->

```zerg
import "strings"
import "math"

print strings.to_upper("hi")
print math.abs(-3)
```

### Channels and spawn

<!-- example: program -->

```zerg
ch := chan[int](2)
spawn fn() {
    ch <- 1
    ch <- 2
    close(ch)
}()

for v in ch {
    print v
}
```

### Select

<!-- example: fn-body -->

```zerg
ch := chan[int](1)
ch <- 7
select {
    v := <- ch -> { print v }
    _ -> { print "would block" }
}
```

### Defer

<!-- example: program -->

```zerg
fn body() -> int {
    defer print "second"
    defer print "first"
    return 0
}

print body()
```

### `never` and `os.exit`

<!-- example: program -->

```zerg
import "os"

fn fail(msg: str) -> never {
    print msg
    os.exit(2)
}

n := 7
if n < 0 {
    fail("expected non-negative")
}
print n
```

### Expression atoms (sanity)

<!-- example: expression -->

```zerg
1 + 2 * 3 - 4 / 2
```

<!-- example: expression -->

```zerg
"foo" + "bar"
```

<!-- example: expression -->

```zerg
[1, 2, 3]
```

## Reserved for v1.0+

The following are not part of the v0.0–v0.12 surface and are explicitly
deferred:

- `sync[T]` shared-mutex container.
- `raise` / `try` / `except` / `finally` exception machinery.
- Lambda syntax (`|x| x * 2`, `||  { ... }`).
- `defer` inside nested blocks (only fn-body scope is admitted at v0.9).
- Channel direction marks (`<-chan[T]`, `chan<-[T]`).
- Cancellation contexts, deadlines, signal handlers.
- Float-typed stdlib surface (`pow`, `sqrt`); `**` exponentiation.
- Regex, JSON, network, path manipulation, stdin streaming, random.
- Map (`{k: v}`) and set (`{a, b}`) literals and types.
- String interpolation (`"hi {name}"`), multi-line `"""..."""`,
  raw `r"..."`.
- `pub` on individual struct / enum fields.
- Re-exports (`pub use` / `pub import`).
- External / git-backed imports.
- `&mut T` reference parameters (fn-param list mutation).
- Inline ARM64 `asm { ... }` blocks.
- Type aliases (`type Name = Type`).
- Compound assignment to list elements (`xs[i] += v`).
- Named call arguments and default parameter values.

<!-- markdownlint-enable MD013 -->
