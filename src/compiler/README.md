# Self-hosting Zerg compiler

English | [繁體中文](README.zh-TW.md)

The Zerg compiler, written in Zerg. It lexes, parses, and emits C over the existing
runtime floor, then invokes `cc` — the same pipeline the Go bootstrap runs, re-expressed
in the language itself. The compiler here is the one that ships; the Go seed exists to build it.

## Confirmed decisions

1. **Emit target — transpile to C, reuse the runtime.** The emitter lowers to C (the same
   C the Go bootstrap emits) and hands off to `cc`, reusing `src/runtime/csrc` wholesale.
   No native/asm codegen at this stage.
2. **The driver invokes `cc` through a runtime `exec` leaf.** `zrt_exec` is an OS-syscall
   floor — `posix_spawn`/`execvp` then `waitpid`, zero third-party dependency — surfaced
   as the `__zrt_exec` intrinsic and wrapped in `os`. It does **not** fill the spec's
   command-literal gap: `` `…` `` is still refused by name (`E236`).
3. **Coverage — the `Zerg-boot` subset first, grown outward.** The compiler had only to
   compile its own source before it could compile anything else, and that subset is the
   seed's contract today ([`src/bootstrap/README.md`](../bootstrap/README.md)). What `zerg`
   accepts **beyond** it is [below](#what-the-shipped-compiler-accepts).

## Layout

```text
src/compiler/
  zergc.zg        # the command line, declared: `main`, `root`, the version banner
  cmd/            # what each sub-command DOES — one directory module
    cmd.zg        # the module's own header, and no declaration (Go's `doc.go`)
    build.zg      # `zerg build` — the pipeline, and where its product is written
    test.zg       # `zerg test` — the run, the process per package, the report
    test_pkg.zg   #   which directories hold a test, and what each is built from
    test_fixture.zg #  what one package's run is: fixtures, tests, and the order
    test_driver.zg  #  the driver source that plan is compiled as
    fmt.zg        # `zerg fmt`
    desugar.zg    # `zerg desugar`
    lint.zg       # `zerg lint`
    lsp_cmd.zg    # `zerg lsp` (not `lsp.zg`: it would shadow `import "lsp"`)
    diag.zg       # shared: the lexical gate and the diagnostic renderer
    source.zg     # shared: reading a source, and resolving what it imports
    layout.zg     # shared: where things are, and what cc is called with
    unit.zg       # shared: a unit, its cached object, and the link
  zerg/           # the compiler library — one directory module, shared scope
    token.zg      # Kind enum + Token type
    lexer.zg      # source text -> token stream (comments kept on request)
    ast.zg        # recursive AST node types (enum payloads)
    parser.zg     # tokens -> AST
    check.zg      # the rules a PROGRAM must satisfy, apart from the code that emits it
    generic.zg    # monomorphization — one emission per instantiation, by substitution
    emit.zg       # AST -> C, with the minimal typecheck emit needs
    fmt.zg        # tokens -> canonical source
    desugar.zg    # tokens -> the core forms the sugar is defined as
    lint.zg       # AST -> findings
    version.zg    # generated from VERSION by ./scripts/gen-version.sh
  lsp/
    server.zg     # the language server — a module of its own
```

The four `cmd` files marked **shared** are there because more than one sub-command reaches
them, and which ones was measured over the call graph rather than guessed: `diag`, `layout`
and `unit` are `build` and `test`'s (and `lint` reads the first two), `source` is those
three plus `lsp`. They sit beside the commands rather than inside any of them — a directory
is one module, so nothing had to become `pub` for them to be shared, and nothing became
`pub` that a second module could then collide with (`E705`).

## Using it

```sh
zerg build <file.zg>    # a program when the entry declares `main`, else an object
zerg build -j8 app.zg   # the same, with eight units compiling at once
zerg build --emit c <file.zg>      # stop at the C; likewise `tokens` and `ast`
zerg build --emit check <file.zg>  # the diagnostics alone — no C, no artifact
zerg fmt <file.zg>...   # rewrite sources in the canonical style, in place
zerg lint <file.zg>...  # report unused imports and dead private code; nonzero if any
zerg --help             # commands, flags, and the environment variables below
```

The compiler resolves `import` itself and works from any directory. Where it looks is
answered by the environment first and the in-repo layout last, so a checkout needs no
setup and an install needs no checkout:

| Variable       | Is                    | Defaults to                   |
| -------------- | --------------------- | ----------------------------- |
| `ZERG_ROOT`    | the installation root | the current directory         |
| `ZERG_RUNTIME` | the runtime C sources | `$ZERG_ROOT/src/runtime/csrc` |
| `ZERG_STDLIB`  | the standard library  | `$ZERG_ROOT/src/stdlib`       |
| `ZERG_CACHE`   | the build cache       | `$ZERG_ROOT/.zerg-cache`      |

An import resolves against the entry file's own directory first, then the standard
library, and a module is either `<name>.zg` or a DIRECTORY of sources read in sorted
order — sorted because the emitted C must not depend on what a filesystem hands back.

The `zerg/` module sub-level — which the original proposal called redundant — is in
fact required by two bootstrap facts found while building M1: (1) an `import "x"`
resolves to a **directory module** whose multiple files flatten into one shared scope,
so `token.zg`/`lexer.zg`/`parser.zg` can share the `Kind` and AST enums; but (2)
**enum variants are not reachable across a module boundary** (`token.Fn` is rejected).
The library must therefore live in one directory module (`zerg/`) whose files share
those enums, with the driver — `zergc.zg` and the `cmd` module — only ever calling the
module's `pub` functions and never constructing a variant. The original `src/compiler/zerg/`
instinct was right.

## How a build is put together

A program is compiled UNIT by unit. A unit is a module — one file, or the several files of
a directory module, which share a scope and cannot be split — and each becomes its own
object, which one link puts together. That split is what everything else rests on:

- **Separate compilation.** A unit declares the whole program but defines only its own
  module, so two modules that share an import can be linked side by side. Whole-program
  emission cannot: each object would carry its own copy of the shared module.
- **Caching.** A unit's key is `sha256` over the C it emitted **plus the `cc` it will be
  handed to and the dialect** — the C folds in the source, everything it can see and the
  compiler that produced it, but not the two inputs downstream of it. Content, not
  timestamps, which is safe precisely because the fixpoint proves emission is
  deterministic. Change a comment and nothing recompiles: a comment does not reach the
  emitted C. `make cache-key-check` is the gate, and it exists because the `cc` half was
  missing for as long as the cache had existed — switching compilers read back the objects
  the first one built, and reported success.
- **Parallelism.** Units do not depend on each other once emitted, so `-j` compiles
  several at once. It comes from OS processes, not coroutines: the scheduler is M:N but
  **cooperative**, so a CPU-bound coroutine holds its worker and buys concurrency rather
  than a shorter compile.

Building the compiler with itself is **ten units** — the entry, `cmd/`, `zerg/`, `lsp/` and
the six stdlib modules they reach. Over three runs it takes roughly 7 s cold at `-j1` and 6 s
at `-j4`, with `-j8` inside the run-to-run spread of `-j4`, and about 5 s when nothing
changed. That the numbers are so close is the point: `zerg/` is one directory module and so
one unit, and it is most of the compiler, so `-j` cannot go below it. The cache is what buys
time here, not the parallelism.

## The corpus

`test-data/` is this compiler's. It describes the LANGUAGE — which is what `zerg` is
growing toward — so it follows the language, not the seed; the Go seed in
[`src/bootstrap/`](../bootstrap) covers its own narrow contract with unit tests and reads
none of it.

```sh
make corpus     # build zerg, then run it over test-data/codegen/
make refuse     # every form this compiler has not built is named, not emitted
make reject     # every program that is not Zerg is rejected — by the compiler, not by cc
```

Each case is a `.zg` program beside the stdout it must produce. The Makefile's
`CORPUS_PASS` is the set `zerg` gets right today and is the **gate**: a case that leaves it
is a regression and fails the target. `CORPUS_SKIP` holds back the rest, and deleting a name
from it **is** the gate for the feature that name waits on.

Six are waiting, each refused **by name** rather than mis-emitted — `gen_struct` answers
_E215 NotImplemented: a generic struct `Box[…]` — this compiler erases type parameters, and
a field names one_ — on a generic `struct` or `enum`, `#[dyn]`, or `derive` beyond `Eq` on a
fieldless enum. Two more, `spec_bound` and `gen_identity`, build and print what they must
today: the list has not caught up with them.

## What a program has to be, and who says so

The seed has a semantic-analysis pass; this compiler was written without one, and for most
of its life nothing asked whether a program was well formed. `x := 1` followed by `x = 2`
compiled and ran. `1 + "s"` became C pointer arithmetic and printed an address. `b: bool =
1` printed `true`, because both are `int64_t` once lowered. A type error that C could see
reached **cc**, which reported it against generated C under `.zerg-cache`.

`check.zg` holds those rules — mutability, one binding per block, bool conditions (every
form that asks a question, including a match arm's guard), operand types, and the four
slots a value enters: a declaration, an assignment, a `return` against its signature, and
an argument against its parameter, plus a call's argument count. They are a file rather
than a pass because the knowledge they need already exists in the emitter: `c_infer` types
every expression and the environment tracks every binding. A separate pass today would mean
a second walk and a second copy of
inference, and the second copy is the one that drifts. Collecting them apart from the
emission that calls them is what keeps the rules readable as a set, and what makes lifting
them into a real pass later a move rather than a rewrite — which is what has to happen when
the AST learns to carry source positions.

Types are compared by `ty_eq` (in `ast.zg`), which is structural over the `Ty` enum — not
by `ty_name`, which is the diagnostic SPELLING and collapses `TUnknown`, `TTuple` and
`TMap` onto one name. Fitting is not equality, so `chk_fits` keeps its own structure on
top: a slot whose type reshapes what it is given is never a mismatch, and a list fits a
list when its elements fit.

Two kinds of message, and the difference is a lifetime:

| message           | means                                              | lives in   |
| ----------------- | -------------------------------------------------- | ---------- |
| `NotImplemented:` | a form this compiler has not built yet             | `emit.zg`  |
| a plain sentence  | a program that is not Zerg — the language's answer | `check.zg` |

`make refuse` and `make reject` are the two gates, one per column.

`make refuse` is the other side of that. Every gate here asks what the toolchain BUILDS,
and the property a refusal needs is not that a bad program fails — it always did — but WHO
says so. A program the compiler emits anyway reaches cc, which reports a real error against
generated C under `.zerg-cache`, at a line the programmer cannot open. So each case in
`scripts/refuse-check.sh` asserts three things: a non-zero exit, the expected sentence, and
no mention of the cache.

**Emit is validated end-to-end, not byte-exact.** Reproducing the Go seed's exact C
formatting and naming across 18k lines of Zerg code — 35k with its comments — would cost
far more than it is worth, so the bar is **functional equivalence**: the emitted C must
compile and the program must print what the corpus says. Determinism (required anyway by
the fixpoint) is what makes that stable; byte-identical C is explicitly a non-goal.

## Which constructs need the runtime

The `runtime?` column is the axis, and it is what decides how much C `src/runtime/csrc` has
to contain: a construct whose C is a shape needs none, and one whose C needs a **lifetime**
— a heap box, a growable buffer, a refcount — reaches for `zrt_*` and cannot be a shape.

| feature           | C shape                                                           | runtime? |
| ----------------- | ----------------------------------------------------------------- | -------- |
| struct            | `typedef struct {…} zg_T;` value; `(zg_T){a,b}`; `p.zg_f`         | no       |
| enum with payload | `{int32_t tag; union{ struct{…} Var; } u;}`; `.tag` / `.u.Var.fN` | no       |
| recursive enum    | payload fields become `void*` ref-boxes                           | yes      |
| list[T]           | `zrt_list` + `zrt_list_init/push/len/at`; for-in is an index loop | yes      |
| str build / ops   | `list[byte](s)` / `str(bs)` / `+` / `==`                          | yes      |

## Teardown, and the argument that had to be withdrawn

Emit **reuses the runtime's data-structure primitives** (`zrt_list_*`, `zrt_ref_alloc` /
`zrt_ref_payload` for boxing). What it originally skipped was the whole memory-management
discipline around them — no `zrt_scope_mark` / `zrt_defer` / `zrt_unwind_to`, no per-type
copy / drop / release — on the argument that a self-hosting compiler is a batch tool: it
compiles once and exits, so it never needs to free.

**That argument reasoned about one program.** This is the shipping backend, and the same
emit compiles everything anybody writes in Zerg — none of which promised to be a batch
tool. A Zerg service leaked every string it formatted and every list it built, for as long
as it ran, with nothing in the language saying so and nothing in the toolchain warning. It
went unmeasured because the only sanitizer gate was `make sanitize-conc`, and until the
ASan fiber annotations landed LeakSanitizer was scanning the wrong range for roots and
reporting nothing at all. The first honest run named it.

So the discipline is emitted now. What each owner does today, and what is left:

| owner          | today                                             | what is left                |
| -------------- | ------------------------------------------------- | --------------------------- |
| `chan`         | binding, and a handle nobody binds                | —                           |
| `list` / `map` | binding, parameter, element vtable, rvalue temp   | —                           |
| `str`          | refcounted cell; binding, parameter, every join   | —                           |
| struct         | `zg_drop_<T>` beside `zg_copy_<T>`, same fields   | —                           |
| carrier        | copy + drop, binding, parameter, element vtable   | the Right of an `Either`    |
| enum           | `zg_drop_<E>` beside `zg_copy_<E>`, per variant   | —                           |
| ref-box        | the cell's drop is the enum's own                 | an ITERATIVE chain teardown |
| fn value       | the environment is a cell; one pair, `zg_*_fnptr` | —                           |
| tuple          | `_drop` beside `_copy`, per shape, element vtable | —                           |
| assignment     | the old value is dropped for an enum, a carrier   | every other owning type     |
| all of them    | registered where declared, given back by unwind   | —                           |

The concurrency corpus is at **0 leak reports**, from 39, and `scripts/sanitize-conc.sh`
runs with `detect_leaks=1` — a leak there is now a regression rather than a known debt. The
`str` row was the one that was not a matter of emitting more code, since a literal is static
storage and a concat is `malloc` and at runtime a `char*` cannot say which it is holding; it
moved to the refcounted cell, with a literal emitted as an IMMORTAL cell so both halves of
the count no-op on it.

The last row is what closed the abort path. A release is now REGISTERED where the binding is
declared and given back by unwinding to a mark — the one exit every other one already goes
through, including the abort the runtime unwinds itself. `c_release_from`'s old argument,
that a defer would hold the address of a C local whose block might end first, does not hold:
`zrt_unwind_abort` unwinds BEFORE the `longjmp`, so the frames are still live, and a block
that registers anything takes its own mark and unwinds at its own end.

**Where it has not been measured** is the rest of the corpus. `sanitize-conc` runs the
seventeen concurrency cases; a one-off sweep of the other forty-eight found 47 reports in
thirteen of them, in classes the concurrency cases do not reach — a chain of rvalue indexes,
a map temporary, `str(bytes)` in an expression, and the ref-boxed recursive types.

`make mem-check` is the first gate that runs anywhere else. It builds nine programs written
inside `scripts/mem-check.sh`, runs each at 5 rounds and at 200 against a counting allocator
linked in place of `alloc.c`, and holds the two live counts equal — so it needs neither
LeakSanitizer nor the private corpus, and runs on macOS and on a fork. The ref-box, the
carrier and the closure environment are what it was written for and it was RED on all three
before they were closed. Its declared limit is that a **bounded** leak — one per program, or
one per site rather than one per construction — is identical at both round counts and
invisible to it.

## Self-host proof

The compiler reproducing itself is what makes "self-hosting" a claim rather than a
description, and `make build` is where it is checked: the seed builds an intermediate, the
intermediate builds the compiler that ships, and a compiler that cannot reproduce itself
cannot get through that. `make corpus` and `make lint` are the checks layered on top.

## What the shipped compiler accepts

`Zerg-boot` — the subset the **seed** must keep to build stage 1 — is listed once, in
[`src/bootstrap/README.md`](../bootstrap/README.md): Tier 1 is what it supports, Tier 2 what
it refuses by name. What `zerg` accepts **beyond** that subset:

| form                              | note                                              |
| --------------------------------- | ------------------------------------------------- |
| `a..b` / `a..=b`, `for i in a..b` | as a `for` iterable; as a value it is refused     |
| `init()`                          | many, each once, in declaration order             |
| module-level `const`              | a C global, assigned before any `init()`          |
| `spec S { … }`                    | consumed whole; `impl S for T` already worked     |
| `(a, b)` and `t.0`                | one carrier struct per distinct shape             |
| `map[K,V]`, `{k: v}`, `{:}`       | POD keys and values                               |
| `defer f(args)`                   | at the enclosing block's exit, arguments by value |
| `fn f[T](…)`                      | monomorphized — one emission per instantiation    |

Concurrency is here in full and is where this compiler is now the WIDER of the two:
`chan[T](cap)`, `ch <- v`, `<-ch` as a real `Result[T]`, `close(ch)` and `defer close(ch)`,
`select` with all four arm shapes and a statement for an arm body, `for v in ch`, the
directional ends `<-chan[T]` / `chan[T]<-`, a `spawn` on a method or a namespaced function,
and the stdlib timers. A channel is first-class here too — held in a struct field, carried
as an enum payload, sent over another channel — and a payload is deep-copied at the send,
so a receiver never shares the sender's buffer.

Null safety is here in full: `T?` is a type with its own carrier, `nil` is its absent
value, and all four of GRAMMAR group 8's operators read one — `??` (right-associative,
short-circuiting, and taking a divergent right-hand side), `?.` (flattening when the field
is itself optional), `!`, and `?`, which early-returns the absence from a function whose
result can carry it. A declaration fills what a construction leaves off, `nil` for a `T?`
field and a named error for anything else.

`f"…"` takes the plain hole only, and the `:spec`, `!r`/`!s`/`!a`, `{x=}` and
interpolating-command forms are each refused by name rather than by silence
([Compile Diagnostics](../../docs/tooling/diagnostics.md)). It desugars **in the parser** to the `+`
chain the form is defined to be, which is why the AST and the emitter know nothing about
f-strings at all — and why the seed only has to lex and parse one to build stage 1.

Still missing, and each refused by name rather than mis-emitted: `Ref[T]` (`E446`), a
generic `struct` or `enum` — a type parameter that a field or a payload names (`E215` /
`E212`, and it is `Atomic[T]` that makes `import "atomic"` an `E511`), named-argument
construction `T(a: 1)` (`E223`), and the command literal (`E236`).

## What performance work is left

Parallelism and the cache are [built](#how-a-build-is-put-together). The two things still
open are worth naming so neither is rediscovered as an idea:

- **A private change still recompiles a module's dependents.** The key is the whole emitted
  C, so any edit that reaches it invalidates everything downstream. Hashing a module's
  **exported surface** separately is what lets a body-only change stop at the one unit that
  changed (Go's export-data trade). It cannot make `-j` faster — nothing splits `zerg/` —
  but it is what makes the cache hit on the edit a person actually makes.
- **The front end cannot go parallel inside one process**, for the reason `-j` fans out
  across processes [above](#how-a-build-is-put-together). Coroutines are useful here only as
  the orchestration layer — a work queue feeding a bounded process pool.
