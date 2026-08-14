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
| `e in ValueError` — the error taxonomy's SUBTREE test                  | the `in` of docs/code/errors.md  |

`e is ValueError` the seed does build; it is `in` it has no reading for. The two are
different relations (identity and subtree, docs/code/errors.md) and only one of them is
here, which is worth saying because it decides how a corpus case is written: a case asking
`is` is one both compilers can be held to, and a case asking `in` is one only `zerg` answers.
Five oracle skips rest on this single gap (`error_tree`, `err_kind_subtree`, and their
kin) — see `test-data/oracle-skips.txt`.

It is also a SECOND refusal with the wrong sentence. `e in ValueError` reads to the seed as
a membership test against a value called `ValueError`, so the name resolves as an ordinary
expression and the answer is `undefined name "ValueError"` — the message a misspelling gets,
naming nothing about the operator. Tier 2 says refused BY NAME and this is not; it is
recorded here rather than fixed because the self-host source never asks the question.

One other entry in this tier is a REFUSAL WITH THE WRONG SENTENCE, kept deliberately: the seed
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
`Ref[T]`, struct and tuple patterns, a block as a `match` arm body, `for c in s` over a
str's code points, an `import … as` rename, and a `pub` module constant. On several of those
the seed is the **wider** of the two compilers, which is a fact about the seed and not about
the language.

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

"Itself" is the whole of it. Two of the entries below were counted as refusals for a year
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
- **A default that CALLS anything is refused, in a sentence that claims the language forbids
  it.** `struct C { c: chan[int] = chan[int]() }` — or any default that is not a literal, a
  module constant, or arithmetic over those — is turned away with _a default value must be a
  constant expression that does not reference a parameter/field_. The language says the
  opposite: a default "is evaluated **per construction** rather than once at the declaration
  — an expression in it (a call, a sum over module constants) runs again for every
  construction that omits the field" (`docs/core/types.md`, "Field defaults"), and `zerg`
  takes it. The seed backfills a default VERBATIM at every call and construct site and never
  TYPES the expression at all — `checkConstDefault` validates its shape, so a default carries
  no recorded `ExprType` and a call would reach cc as bad C. The refusal is therefore right
  about the seed and wrong about Zerg: a `NotImplemented` wearing a language rule's clothes.
  It is also why `Context.events` in `src/stdlib/testing.zg` is `pub` and carries no default —
  a module-private field must carry one, and the only default a fresh channel could have is
  a call.
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
- **A tuple that OWNS something cannot be COPIED.** `t := (1, s)` where `s` is a `str`, and
  any tuple holding a `list` or a `map`, is refused by name — "copying a (int, str) is not
  supported in Phase 1d iteration 2 (only Ref[T] and structs holding Refs)". A tuple of
  scalars copies fine, and so does a struct holding the same things, so this is the tuple
  alone. `zerg` copies either: a tuple gets a per-shape `_copy` with a `_drop` beside it,
  which is what makes `(int, str)` give its `str` back at scope exit.
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
- **`Either.Left(v)` is not read; the bare `Left(v)` is.** The two sides of an `Either` are
  variants of a built-in rather than of a declared enum, so the seed's enum-namespace path
  does not find `Either` and reports an undefined name. `zerg` requires the qualified form
  (a variant is named through its type, with no exception for a built-in), which is why the
  three corpus programs that use it are skipped here with that reason.
- **`#[obj]` is an unknown decorator.** `zerg` expands it into a companion struct of
  function values and a generic wrap; the seed reads `#[derive(…)]` and no other. Its
  expansion needs closure capture, which the seed also has not got, so the hand-written half
  of that pair is refused here too.
- **A closure that CAPTURES is refused.** `zerg` lifts a lambda's captures into a per-site
  environment struct and hands it to the call through the fn value's own env slot; the seed
  turns a capturing lambda away — "a closure used as a value is not yet supported" — and is
  the narrower compiler here.
- **`#[derive(S)]` on an ENUM is refused for a spec you wrote.** The delegating half of
  `derive` — each arm handing the call to its payload — is a rewrite that exists for any
  spec, so `zerg` derives it. The seed keeps one blessed set for both halves and turns the
  program away; it is the narrower compiler here rather than the wrong one.
- **An associated type or value in a `spec` is accepted.** GRAMMAR#spec-member derives a
  required signature and a provided method, and nothing else — a spec carries behaviour. The
  seed reads `type Item` and `BITS: int` inside one and carries on; `zerg` refuses both by
  name. (A member that is neither, like `SIZE := 4096`, the seed does turn away.)
- **A call that WRITES its type arguments is accepted in its multi-argument shape.**
  `pairup[str, int]("k", 9)` builds here; `zerg` refuses it, because GRAMMAR makes a postfix
  `[ … ]` always an index and a generic takes its type from its arguments. The
  one-argument shape (`id[int](7)`) the seed does turn away, for a reason of its own.
- **A BARE VARIANT is accepted AS A VALUE.** GRAMMAR says an enum puts its own name into the
  value namespace and not its variants', so a variant is reached through its enum —
  `Color.Red`. The seed reads that form and does not require it, so `Red` alone still
  resolves here; `zerg` refuses it by name. The seed's own sources need no migration for the
  same reason its gaps are its own contract: it is the oracle on programs that follow the
  rule, not the enforcer of it. (In PATTERN position there is no gap and no rule to enforce:
  a bare name binds in both compilers, which is what GRAMMAR#pattern says it is.)
- **`This` as a DECLARATION's name is accepted.** `This` is the self type, written by every
  `impl` and declared by none, so it is reserved the way `this` is — but it is the one
  reserved word the lexer reads as an ordinary identifier, and the seed has no rule about a
  name beyond its keyword table. So `struct This`, `fn This()`, `type This = int`, a parameter
  and an `enum` variant all build here and `zerg` refuses each by name. (Lowercase `this` the
  seed does refuse, because that one IS a keyword token.)
- **An `impl` whose TARGET carries type arguments is accepted, and implements nothing.**
  `impl Size for list[int] { … }` and the inherent `impl Box[int] { … }` both build here: the
  seed parses the whole of `GRAMMAR#impl-decl`, including the `impl`'s own `generics?`, and
  then attaches the block to nothing a call can find, so `xs.size()` answers that a list has
  no method by that name — a refusal at the USE, one step from a declaration that was never
  turned away. `zerg` refuses the declaration itself, naming the form and its place. (The
  parameterized `impl[T] Spec for list[T]` the seed does turn away, though for a reason of
  its own: it drops the parameters it read, so the `T` in the target resolves to nothing and
  the answer is `unknown type "T"` rather than a word about the form.)
- **A SOURCE FILE THAT IS NOT UTF-8 is accepted.** `GRAMMAR#letter` says the source is UTF-8, so
  a file holding a stray `0xFF` is not a Zerg source file; the seed is byte-oriented from the
  read to the emit and has no str invariant to violate, so the byte travels through the lexer
  into a string literal and out as `"\377"` in the C. `zerg` reads a file into a `str` and
  refuses one that cannot be, naming the path — the encoding is the one place where it is the
  stricter compiler for a reason that is not a rule it added but a type it has.
- **A STATEMENT AT THE TOP LEVEL is accepted, and dropped.** `program ::= stmt-list`
  (`GRAMMAR#program`) is Zerg's script mode, so a top-level `print 999`, `if …` or `for …` is
  grammatical — and a compiled program has nowhere to run one, since outside `main` lives only
  immutable state readied before it (docs/runtime/package.md). The seed parses each into
  `file.Items` and neither lowers nor mentions it, so the program builds and prints nothing.
  `zerg` refuses it by name at the line it was written on, `nop` excepted. This is a rule
  `zerg` ADDED rather than one the seed lost, which is the ordinary direction here.
- **A CONVERSION FOLDS ONLY A WRITTEN LITERAL, so a known value reaching it through a name is
  left to run.** docs/core/types.md reports `byte(300)` at compile time because "the value is
  known", and what the language means by known is the const-expr — a literal, a binding whose
  initializer is one, a `const`, and the operators over any of them. `zerg` asks that question
  (the same one a fill count `[v; N]` asks), so `big := 300; byte(big)`, `const N := 300;
byte(N)` and `byte(N * 3)` are compile errors. The seed folds the literal alone: it builds all
  three and raises `OverflowError` where they run. Three cases in `reject-check.sh` carry the
  marker. Both compilers stop at the same place on the other side — a CALL and a `mut` binding
  are not constants for either — so the gap is the middle of the range and not its end.
- **EVERY CONVERSION BETWEEN TWO SCALARS is accepted, whatever the pair.** docs/core/types.md
  lists the pairs `T(x)` has "and no others" — `int` is the hub each of them stands on — but
  the seed lowers a conversion by SHAPE, a class and a width, and a shape has an answer for
  every pair. So `float(b)` on a `byte`, `rune(b)`, `uint(b)`, `byte(3.5)`, `uint(3.5)`,
  `rune(65.5)` and `int(1.9)` all build here. `zerg` refuses each: a `float` source is a
  decision spelled with a verb (`E394`, `math.trunc` and its three siblings), and any other
  absent pair is the two steps through `int` written as one (`E395`). Seventeen cases in
  `reject-check.sh` carry the marker, and this is the chapter where `zerg` is the stricter
  compiler rather than the reverse. (The seed's own sources need no migration: nothing in
  `src/stdlib` writes a pair off the table any more, which is what lets both compilers build
  the same standard library.)
- **TWO MODULES EACH DECLARING ONE `pub` FUNCTION OF THE SAME NAME are accepted, and one of
  them wins.** A public name has no package to be unique within
  ([package](../../docs/runtime/package.md)), so `zerg` refuses the pair by name (`E705`) and
  says what would be needed to keep both — the link-name override
  [ffi](../../docs/runtime/ffi.md) specifies. The seed flattens every module into one
  namespace as `zerg` does, but asks the question only of the PRIVATE pair, which it tags by
  module; a public one reaches C as a single mangled symbol and the second definition simply
  replaces the first. One case in `reject-check.sh` carries the marker.
- **AN INCLUSIVE RANGE WITH NO UPPER BOUND is accepted, and the arm it is written on never
  matches.** `GRAMMAR#range-arm` gives `..=` a mandatory bound, and the parser reads a missing
  one as `nil` — which a program may also write out, `1..=nil`. `zerg` refuses the shape
  (`E743`) wherever it arrives. The seed reads the absent bound as 0, so the arm is false for
  every value and the `match` falls through to its catch-all with nothing said. One case in
  `reject-check.sh` carries the marker.
- **A `spec` NAMED AS A STRUCT FIELD'S TYPE is accepted.** A spec is a bound and an interface,
  not a value's type ([specs](../../docs/core/specs.md)), and `zerg` refuses it at every
  position a type is written (`E416`). The seed asks the question of a parameter and a result
  and not of a field, so `pub v: Tag` declares a field whose type nothing gives a
  representation and the program builds. One case in `reject-check.sh` carries the marker.
- **A division by a constant `0` is accepted, and raises at run time.** `x := 1 / 0` is a
  value the compiler can work out, so `zerg` answers at the division rather than leaving the
  program to reach it — the same reasoning that folds a literal in a typed position. The
  seed folds nothing here and emits the division, whose runtime check then raises. Both
  refuse the program in the end; only one of them does it before the program runs.
- **A `pub` declaration may name a module-private TYPE.** `pub fn make() -> Secret` beside a
  module-private `struct Secret` is accepted, so a dependent obtains a value of a type it
  could never have spelled — "a declaration can never be more visible than the types it
  names" (docs/runtime/package.md) is unenforced. The same goes for a parameter, for a `pub`
  field whose type is private, and for a `pub` METHOD on a private type — the last is the
  same sentence read about a receiver, and the specification's "a type's `pub` methods travel
  with it" is what makes it one rule rather than a separate courtesy. `zerg` refuses each at
  the declaration, which is the party with a line to change. The seed IS the stricter
  compiler on the neighbouring rule — it has
  refused a module-private type named through a namespace (`lib.Secret`) since it was
  written, and only the qualified spelling, because it never flattens a type into the
  importer's namespace at all.
- **A module-private FIELD is readable, and writable, from another module.** The seed
  requires the default that GRAMMAR#field makes a private field carry — so external code can
  construct the type without naming a value it may not read — and then lets that value be
  read. `zerg` refuses the read and the write alike, at the use.
- **An import is not transitive, and the seed does not enforce it.** Both compilers bind a
  namespace into one program-wide space, so a module the build reached at all used to be one
  every module could name: `main` importing only `mid` could still write `lib.make()`.
  `zerg` now records which module WROTE each binding and refuses a namespace this one did not
  import — while still telling that apart from an invented prefix, which stays an undefined
  name in both compilers.
- **A DECLARED TYPE NAME NEED NOT BEGIN WITH AN UPPER-CASE LETTER.** `struct _Box`,
  `struct __Box` and `struct lower` all build and run here. `zerg` refuses each at the
  declaration (`E610`): the case of the first letter is how it tells a construction from a
  call and a module qualifier from an associated type, and the last two are decided by the
  PARSER, which has resolved nothing and has no table to consult — so a lower-case type
  would be legal in one position and misread in three. GRAMMAR#type-ident derives the rule.
  The seed resolves the name against its symbol table instead, so the letter never matters
  to it. Three cases in `reject-check.sh` carry the marker.
- **A COMPILER PRIMITIVE'S OPERAND TYPES are not checked.** The machinery is there —
  `unaryIntrinsic(n, Float, Int)` in `internal/sema/infer.go` names the argument type — and it
  does not fire, so `__zrt_trunc(true)` builds and prints `1`, and `__zrt_trunc("hello")` is
  emitted for cc to reject against a temp C file nobody wrote. `zerg` answers both at the call
  (`E398`): a primitive is lowered by NAME to a C function with a real signature, so a wrong
  operand is either a cc diagnostic or an answer that is quietly wrong where C converts it.
  Two cases in `reject-check.sh` carry the marker.
- **A `#[test]` FUNCTION IS NOT TYPE-CHECKED.** The seed strips every `#[test]` out of the item
  list before `sema.Check` runs (`dropTestItems`, `internal/build/build.go`), so `#[test] fn t()
{ x: int = "no" }` builds and runs, and so does one calling a function that does not exist.
  `zerg` reads `#[test]` as an ordinary decorator on an ordinary declaration and checks the body
  like any other: a test that does not compile is a compile error, in a normal build as much as
  under `zerg test`. The seed's stripping is not wrong for the seed — it keeps a normal build's
  emitted C byte-identical whether or not a `#[test]` is present — but it means the one
  compiler that runs tests is the only one that ever looks at them.

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
