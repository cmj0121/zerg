# The Zerg bootstrap seed

English | [繁體中文](README.zh-TW.md)

A Go-hosted Zerg compiler whose only remaining job is to **build the self-hosting compiler
in `src/compiler/`**. It is a seed, not a product: once `zerg` compiles itself, everything
a user-facing toolchain does (`fmt`, `lint`, `test`, diagnostics worth reading) belongs to
the Zerg-written compiler, and the seed keeps only what the first build needs.

That single purpose is the design rule. The seed supports the `Zerg-boot` subset — the
slice of the language the self-host source is actually written in — and nothing else. A
program outside the subset is rejected with one line and a nonzero exit code; it is never
silently miscompiled.

## Usage

```sh
zerg0 build <file.zg>              # compile and link a binary (default --emit bin)
zerg0 build --emit c <file.zg>     # stop after emitting the C translation unit
zerg0 build -o out --keep-c f.zg   # choose the output path, keep the generated .c
```

`build` is the only subcommand. Failures print `file:line:col: message` on stderr and exit
nonzero — enough to locate the problem, no more.

## The bootstrap chain

```text
make build                        # or by hand, the three steps it runs:
go build -o bin/zerg0 ./cmd/zerg  # 1. the Go seed — named zerg0, not zerg
zerg0 build src/compiler/zergc.zg -o bin/.zerg-stage1
                                  # 2. the seed builds an INTERMEDIATE compiler
zerg build --emit bin -o bin/zerg src/compiler/zergc.zg
                                  # 3. the intermediate builds the zerg that ships
```

The compiler that ships is built by a compiler written in Zerg, not by the seed — the seed
only has to produce an intermediate good enough to build the real one. That keeps the seed
off the delivery path, and it means every build exercises the self-host path. After that,
the seed is only needed to re-derive `zerg` on a machine that has no `zerg` yet.

## The contract: three tiers, and nothing in between

The seed has one job — build the self-hosting compiler — so what it MUST support is not a
taste question. It is whatever `src/compiler/zergc.zg`, `src/compiler/zerg/*.zg` and the
stdlib modules those import (`io`, `ascii`, `strconv`, `cli`) are actually written in. Every
form the language has falls into exactly one of three tiers.

**Tier 1 — supported.** The `Zerg-boot` subset. Lowered to C, and covered by the seed's own
unit suites.

**Tier 2 — `NotImplemented`.** Valid Zerg the self-host source does not use. Refused BY NAME,
with a nonzero exit, before anything is emitted. The seed is not a smaller Zerg — it is a
compiler for one program, and everything else is somebody else's job.

**Tier 3 — `SystemError`.** A state the seed cannot classify at all: an AST shape with no
lowering, a type that resolved to nothing, a call whose target never bound. These are not
the programmer's mistake, they are the seed's — and they must ABORT saying so, never fall
through to `0` and hand cc a program nobody wrote. Tier 3 is what makes tiers 1 and 2 a
closed set rather than two lists with a silent gap between them.

`make refuse` gates tiers 2 and 3: a case asserts a non-zero exit, the expected sentence, and
that the message came from the seed rather than from cc against generated C.

## Tier 1 — what the seed supports

Every entry is exercised by the self-host chain, which is why it is still here. The counts
are code lines in that chain as of 2026-07-30, comments and string literals excluded.

| Feature                             | Notes                                                             |
| ----------------------------------- | ----------------------------------------------------------------- |
| `fn`, incl. `mut &` parameters      | by-reference receivers (296 uses)                                 |
| value structs                       | declaration, construction, field access, nesting                  |
| enums — plain, payload, RECURSIVE   | tagged unions, auto-boxed self-reference (`Ty`, `Expr`, `Stmt`)   |
| inherent `impl T` + `This`          | value receiver, flattened to a `this: T` first parameter (25)     |
| `match`                             | expression arms, newline-separated, with destructuring (104)      |
| `list[T]`                           | `append` / `len` / `x[i]` read and write, `for … in` (520)        |
| `str`, `byte`, **f-strings**        | concatenation, `str(bytes)` / `list[byte](str)`, `f"…"` (162)     |
| `Result[T]`, `guard`, `raise`       | the error path the driver, `io` and `strconv` use                 |
| `return e if c`                     | the postfix conditional return — 385 uses, the most-used sugar    |
| `if` / `else`, `for` in three forms | `for cond`, bare `for`, `for x in xs`; `break`, `continue`, `nop` |
| `import`, `pub`                     | module-qualified calls, whole-program flattening                  |
| `__zrt_*` intrinsics                | the runtime floor, including `__zrt_exec` (how `zerg` runs `cc`)  |

Types: `int`, `uint`, `float`, `bool`, `byte`, `str`, `nil`, `list[T]`, a named struct or
enum, `Result[T]`, and `This` inside an `impl`.

## Tier 2 — what the seed refuses by name

**This table is why the language specification does not mention the seed.** A form the seed
refuses is not a gap in Zerg — the shipped `zerg` lowers it — so `docs/` marks nothing here,
and a reader writing Zerg never meets these. They are the seed's own contract, and this is
where they are recorded.

Nothing in this table appears in the self-host chain; each was verified absent, not assumed.
Measured against `zerg0` on 2026-07-31.

| Form                                                                   | What it is                       |
| ---------------------------------------------------------------------- | -------------------------------- |
| coroutines: `spawn`, `chan[T]`, `select`, `<-`, `close`, `for v in ch` | the whole concurrency chapter    |
| `map[K, V]` — literals, and copying one                                | the container                    |
| a **closure literal** used as a value                                  | a named `fn` value is supported  |
| slicing `xs.slice(a, b)` / `xs[a..b]`                                  | the subrange                     |
| module-level `const`                                                   | a binding outside a function     |
| `#[dyn]` dispatch                                                      | the decorator                    |
| `unsafe`, `asm`, `ptr[T]`                                              | the bare-metal door              |
| command literals `` `git status` ``                                    | the process-substitution literal |
| `for k in m` over a map                                                | the iteration                    |

One entry in this tier is a REFUSAL WITH THE WRONG SENTENCE, kept deliberately: the seed
rejects a **same-block re-declaration** (`x := 1` then `x := 2`) as `"x" is already declared
in this scope`. The language permits it — docs/core/memory.md specifies declare-del-declare,
and `zerg` builds it (the corpus case redeclare_same_block) — so this is the seed being
narrower, not a rule `zerg` lost. The self-host source never re-declares in one block (the
seed could not build it if it did), so the rule costs the chain nothing, and rewriting the
seed's scope tracking to lift it would move emitted C for no program the seed exists to
build.

Everything else the language has, the seed has: `defer`, `del`, `with`, tuples and `t.0`,
ranges as a value and as an iterable, optionals and the whole group-8 operator set, `init()`,
`spec` / `impl` including provided methods, generic function definitions, `#[derive(Eq, Ord)]`,
`Ref[T]`, struct and tuple patterns, a block as a `match` arm body, and `for c in s` over a
str's code points. On several of those the seed is the **wider** of the two compilers, which
is a fact about the seed and not about the language.

A form the FRONT END still parses is not thereby supported: the refusal may land in sema or
at the emitter's door. Narrowing the parser is a separate pass, and not an urgent one — what
matters is that nothing outside tier 1 reaches C.

## Tier 3 — what the seed reports as its own failure

`SystemError` is for the cases above neither tier covers, and it exists because the
alternative is what the seed used to do: fall through a `switch` to `"0"` and emit C naming
an identifier nothing declared. cc then reports a real error against a file under
`.zerg-cache` that the programmer cannot open — a diagnostic pointing at the wrong program.

| Situation                                       | What must happen                        |
| ----------------------------------------------- | --------------------------------------- |
| an AST node with no lowering                    | `SystemError: no lowering for <node>`   |
| a type that resolved to nothing                 | `SystemError: <site> has no type`       |
| a call whose target never bound                 | `SystemError: unresolved call <name>`   |
| a carrier / helper the emitter never registered | `SystemError: <helper> was not emitted` |

The rule: **the seed may refuse a program, and it may fail — but it may never be wrong
quietly.**

## Layout

```text
src/bootstrap/
  cmd/zerg/        # the build-only driver: flags, cc invocation, exit codes
  internal/
    token/         # token kinds and their spellings
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

## Known gaps

The seed is deliberately the narrower compiler, and it refuses most of what it has not
built. These are the places it does NOT — where it does not turn a program away ITSELF, so
`zerg` is the stricter one and `scripts/reject-check.sh` marks the case `seed-gap`.

"Itself" is the whole of it. Two of the two remaining were counted as refusals for a year
because the seed emitted C that **clang** rejected: `-Wint-conversion` and
`-Waddress-of-temporary` are errors there and warnings under gcc, so the same seed and the
same program read as green on macOS and red on Linux. A cc diagnostic is the seed emitting
the program, which is what the assertion exists to catch.

- **A `mut &` parameter with a DEFAULT is accepted, and a call that uses the default
  segfaults.** GRAMMAR#param makes a `mut &` valid only for the call and its argument a `mut`
  lvalue; a default has no caller variable to point at. The seed emits the default
  expression where a pointer goes, so `f(5)` on `fn f(a: int, mut &b: int = 0)` dereferences
  a literal. `zerg` refuses it at the declaration.
- **A `mut &` argument crossing a `defer` is accepted.** GRAMMAR#param says a borrow cannot
  escape; the seed hands the deferred thunk the value where a pointer goes. `zerg` refuses
  it at the callsite.
- **A default that cannot fit its parameter is accepted.** `fn f(a: int, b: str = 1)` is
  emitted as written and cc reports the type. `zerg` judges a default at the declaration.
- **A TOP-LEVEL binding's annotation is not checked against its value.** `answer: bool =
42` builds and the global is whatever the seed makes of it; the same mismatch on a LOCAL
  binding the seed does refuse. `zerg` honours a top-level annotation the way it honours a
  local's — the global takes the declared type and the mismatch is refused at the
  declaration.
- **A module constant that takes a FUNCTION's name is accepted.** `const f := 1` beside
  `fn f()` — in either source order — is emitted, and cc reports "redefinition of 'zg_f'"
  against generated code: both flatten to one top-level namespace and one C symbol. `zerg`
  refuses the collision at the constant's declaration. (A LOCAL named after a function
  stays legal in both compilers — it shadows, which is the ordinary scoping rule.)
- **`match` of an optional against a range is accepted.** `zerg` refuses the arm.
- **An `int` narrowed to a `byte` parameter is accepted.** `take(1000)` on a `fn take(b:
byte)` compiles to a truncation and cc warns about the generated C. `zerg` refuses it: a
  `byte` WIDENS to an `int` and nothing goes the other way, because byte arithmetic stays
  in `byte` and so nothing needs it to.
- **A FIELD or a VARIANT declared twice is accepted.** `struct A { v: int; v: str }` and
  `enum E { X; X }` both build and run under the seed. `zerg` refuses both — the second
  variant is unreachable, so a `match` naming the first reads as exhaustive over an enum
  that has two. (A repeated PARAMETER the seed does catch.)
- **An optional TUPLE `(A, B)?` was emitted as `void`.** The carrier scan asks `ctype()` of
  the element before tuple C types are named, so `Opt[Tuple]` was not recognised as a
  carrier and the function silently returned nothing; cc reported "variable has incomplete
  type 'void'" against generated C. It is refused by name now — a plain `(A, B)` return
  works, and `zerg` builds both. This is why the STDLIB may not use an optional tuple: it
  is compiled by the seed as well, which is the same reason nothing there uses slicing.
- **A TYPE NAME declared twice is accepted.** A `struct`, an `enum` and a `spec` share one
  namespace, and every module of a program flattens into one scope in both compilers — so
  `enum E` twice, `spec T` twice, and a `struct A` beside a `spec A` are all one name for
  two declarations. The seed builds and runs each of them. `zerg` refuses the pair, naming
  the two kinds when they differ.
- **A mis-shaped `display` / `debug` override is accepted.** docs/runtime/format.md fixes
  the override contract — `fn display() -> str`, the value alone in, its text out — and
  `zerg` refuses a method of either name that takes an argument or answers something else,
  at the declaration. The seed has no rendering dispatch, so the method is to it an
  ordinary method and the program builds.
- **Nesting deeper than `zerg`'s translation limit is accepted.** `zerg` refuses a program
  that nests more than 200 levels of expressions, blocks or types (docs/conformance.md) —
  counting its own recursion, and measuring the depth of each expression tree it finishes,
  which is what catches a FLAT chain (`1 + 1 + … + 1`, a long method chain) that parses in
  a loop without the parser ever getting deeper. Its emitter counts a third time, over the
  tree it WALKS rather than the one the program wrote, which is what catches a depth the
  source never states — a defaulted argument backfilled into a call site composes the two.
  The seed parses on Go's growable stack and accepts every one of those shapes, in every
  position. The gap is harmless in the narrowing direction —
  the seed's only input, the compiler's own source, nests five levels — but it is a gap:
  the seed enforces no bound at all, and a deep enough program would end as a Go
  stack-limit panic rather than a diagnostic.
- **A BARE VARIANT is accepted, in a pattern and as a value.** GRAMMAR says an enum puts
  its own name into the value namespace and not its variants', so a variant is reached
  through its enum — `Color.Red`. The seed reads that form and does not require it, so `Red`
  alone still resolves here; `zerg` refuses both halves by name. The seed's own sources need
  no migration for the same reason its gaps are its own contract: it is the oracle on
  programs that follow the rule, not the enforcer of it.
- **`This` as a DECLARATION's name is accepted.** `This` is the self type, written by every
  `impl` and declared by none, so it is reserved the way `this` is — but it is the one
  reserved word the lexer reads as an ordinary identifier, and the seed has no rule about a
  name beyond its keyword table. So `struct This`, `fn This()`, a parameter and an `enum`
  variant all build here and `zerg` refuses each by name. (Lowercase `this` the seed does
  refuse, because that one IS a keyword token.)
- **A division by a constant `0` is accepted, and raises at run time.** `x := 1 / 0` is a
  value the compiler can work out, so `zerg` answers at the division rather than leaving the
  program to reach it — the same reasoning that folds a literal in a typed position. The
  seed folds nothing here and emits the division, whose runtime check then raises. Both
  refuse the program in the end; only one of them does it before the program runs.

## Changing the seed

The invariant that makes a change safe to make: **the C emitted for the self-host source
must not move**. If a change is genuinely dead-code removal, the emitted C is byte-identical
before and after; if it is not, the difference is the change's real blast radius.

```sh
zerg0 build --emit c src/compiler/zergc.zg > after.c  # compare against a pre-change capture
go build ./... && go test ./...                       # the seed's own suite (unit tests only)
make build                                            # and the chain still closes
```

The seed is covered by unit tests alone. The `test-data/` corpus describes the language,
so it belongs to the self-hosting compiler and is run by `make corpus` — see
[`src/compiler/README.md`](../compiler/README.md).

Adding language coverage back here is almost always the wrong move: the self-hosting
compiler is where the language grows. The seed only has to stay good enough to build it.
