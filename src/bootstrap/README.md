# The Zerg bootstrap seed

English | [繁體中文](README.zh-TW.md)

A Go-hosted Zerg compiler whose only remaining job is to **build the self-hosting compiler
in `src/compiler/`**. It is a seed, not a product: once `zergc` compiles itself, everything
a user-facing toolchain does (`fmt`, `lint`, `test`, diagnostics worth reading) belongs to
the Zerg-written compiler, and the seed keeps only what the first build needs.

That single purpose is the design rule. The seed supports the `Zerg-boot` subset — the
slice of the language the self-host source is actually written in — and nothing else. A
program outside the subset is rejected with one line and a nonzero exit code; it is never
silently miscompiled.

## Usage

```sh
zerg build <file.zg>              # compile and link a binary (default --emit bin)
zerg build --emit c <file.zg>     # stop after emitting the C translation unit
zerg build -o out --keep-c f.zg   # choose the output path, keep the generated .c
```

`build` is the only subcommand. Failures print `file:line:col: message` on stderr and exit
nonzero — enough to locate the problem, no more.

## The bootstrap chain

```text
go build ./cmd/zerg              # 1. the Go seed
zerg build src/compiler/zergc.zg # 2. the seed builds the self-hosting compiler
zergc build <out> <file.zg>...   # 3. zergc builds programs — and itself
```

Step 3 closing on itself is the point of the whole directory: after that, the seed is only
needed to re-derive `zergc` from source on a machine that has no `zergc` yet.

## What the seed supports

Every entry below is exercised by the self-host source (`src/compiler/zergc.zg`,
`src/compiler/zerg/*.zg`) or by the stdlib modules it imports (`io`, `ascii`, `strconv`),
which is why it is still here.

| Feature                         | Notes                                                             |
| ------------------------------- | ----------------------------------------------------------------- |
| value structs                   | declaration, construction, field access, nesting                  |
| enums — plain and payload       | tagged unions; a variant is constructed by its **bare** name      |
| recursive enums                 | self-referential payloads, auto-boxed (`Expr`, `Stmt`)            |
| `match`                         | expression arms, newline-separated, with destructuring            |
| `list[T]`                       | `append` / `len` / `x[i]` read and write, `for … in`              |
| `str` and `byte`                | concatenation, `str(bytes)` / `list[byte](str)`, f-strings        |
| `Result[nil]`, `guard`, `raise` | the error path the driver and `strconv` use                       |
| `mut &` parameters              | by-reference receivers (`fn at(mut &l: Lex, …)`)                  |
| generics                        | monomorphized at compile time                                     |
| tuples, optionals, `defer`      | retained: cheap, and still reachable from the subset              |
| `spec` / `impl` methods         | statically dispatched                                             |
| modules                         | `import "path"`, module-qualified calls, whole-program flattening |
| `__zrt_*` intrinsics            | the runtime floor, including `__zrt_exec` (how `zergc` runs `cc`) |

## The grammar the seed lowers

`Zerg-boot` in W3C-style EBNF, using the notation and production names of the full
[`GRAMMAR`](../../GRAMMAR). This is the backend's view: what the C emitter has a lowering
for. The front-end still parses more than this — anything below is what actually reaches C.

```ebnf
program        ::= stmt-list
statement      ::= simple-stmt | compound-stmt | decorated-decl
simple-stmt    ::= nop | binding | reassign | print | return | raise | break | continue
                 | del | defer | expr-stmt
compound-stmt  ::= block | if-stmt | for-stmt | with-stmt
decorated-decl ::= decorator* declaration
declaration    ::= fn-decl | struct-decl | enum-decl | spec-decl | impl-decl | import-decl

binding        ::= 'mut'? identifier ( ':' type )? ( ':=' | '=' ) expr
reassign       ::= lvalue '=' expr
lvalue         ::= identifier ( '.' identifier | '[' expr ']' )*

fn-decl        ::= 'pub'? 'fn' identifier type-params? '(' param-list? ')' ( '->' type )? block
param          ::= ( 'mut' '&' )? identifier ':' type
struct-decl    ::= 'struct' identifier '{' ( identifier ':' type stmt-sep )* '}'
enum-decl      ::= 'enum' identifier '{' ( variant stmt-sep )* '}'
variant        ::= identifier ( '(' type ( ',' type )* ')' )?
import-decl    ::= 'import' str-lit

type           ::= 'int' | 'uint' | 'float' | 'bool' | 'byte' | 'str' | 'nil'
                 | identifier | 'list' '[' type ']' | 'Result' '[' type ']' | type '?'

expr           ::= or-expr | coalesce-expr | range-expr | match-expr | guard-expr | if-expr
primary        ::= literal | fstr-lit | fcmd-lit | cmd-lit | identifier | list-lit
                 | tuple-lit | block | '(' expr ')'
postfix        ::= '.' identifier | '.' dec-int | '[' expr ']' | '(' arg-list? ')'
                 | '?' | '!' | '?.' identifier
match-expr     ::= 'match' expr '{' ( pattern '=>' expr stmt-sep )* '}'
pattern        ::= identifier ( '(' identifier ( ',' identifier )* ')' )? | '_'
```

Two shapes differ from what the full grammar allows, and the self-host source is written to
match: an enum variant is constructed by its **bare** name (`EBinary("or", a, b)`, not
`Expr.EBinary(…)`), and `match` arms and enum variants are separated by a **line break**,
not a comma.

## What the seed does not support

These were removed because the self-host source uses none of them. Each is rejected loudly
— a diagnostic and a nonzero exit, never a silently dropped construct.

| Removed              | What happens now                                       |
| -------------------- | ------------------------------------------------------ |
| `map[K, V]`          | rejected                                               |
| closures / fn values | `a closure used as a value is not yet supported`       |
| channels             | rejected                                               |
| `spawn`, `select`    | `statement not supported by the bootstrap seed`        |
| `#[dyn]` dispatch    | the decorator is ignored; the call monomorphizes       |
| `zerg test` backend  | gone — running Zerg tests is the Zerg toolchain's job  |
| `--emit tokens/ast`  | gone — `--emit c` and a linked binary are what remains |

The front-end still _parses_ some of this syntax; the rejection happens when the backend is
asked to lower it. Narrowing the parser is a separate pass.

## Layout

```text
src/bootstrap/
  cmd/zerg/        # the build-only driver: flags, cc invocation, exit codes
  internal/
    lexer/         # source text -> tokens
    parser/        # tokens -> AST
    ast/           # AST node types
    sema/          # name resolution and type checking
    types/         # the type representations sema works in
    module/        # import resolution, whole-program flattening
    mono/          # monomorphization (generics, recursive-enum boxing)
    emit/          # AST -> C, plus the runtime manifest the driver links against
    build/         # the pipeline in one call: load -> sema -> mono -> emit
    diag/          # diagnostics
```

## Changing the seed

The invariant that makes a change safe to make: **the C emitted for the self-host source
must not move**. If a change is genuinely dead-code removal, the emitted C is byte-identical
before and after; if it is not, the difference is the change's real blast radius.

```sh
zerg build --emit c src/compiler/zergc.zg > after.c   # compare against a pre-change capture
go build ./... && go test ./...                       # the seed's own suite
zerg build src/compiler/zergc.zg -o zergc             # and the chain still closes
```

Adding language coverage back here is almost always the wrong move: the self-hosting
compiler is where the language grows. The seed only has to stay good enough to build it.
