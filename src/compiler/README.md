# Self-hosting Zerg compiler

English | [繁體中文](README.zh-TW.md)

The Zerg compiler, written in Zerg. It lexes, parses, and emits C over the existing
runtime floor, then invokes `cc` — the same pipeline the Go bootstrap runs, re-expressed
in the language itself. The compiler here is the one that ships; the Go seed exists to build it.

## Confirmed decisions

1. **Emit target — transpile to C, reuse the runtime.** The emitter lowers to C (the same
   C the Go bootstrap emits) and hands off to `cc`, reusing `src/runtime/csrc` wholesale.
   No native/asm codegen at this stage.
2. **Driver invokes `cc` through a new runtime `exec` leaf.** The runtime today has no
   subprocess primitive. We add `zrt_exec` (an OS-syscall floor: `posix_spawn`/`execvp`
   then `waitpid`, zero third-party dependency), surface it as the `__zrt_exec`
   intrinsic, and wrap it in an `os.run` / process stdlib module. This also fills the
   spec's "command literal — not yet" gap.
3. **Coverage — a `Zerg-boot` subset first, grown incrementally.** Milestone 1 only needs
   to support the language subset the compiler's own source is written in. Every pass is
   validated by diffing against the Go bootstrap's `--emit` output. Coverage grows toward
   the full language after the fixpoint holds.

## Layout

```text
src/compiler/
  zergc.zg        # the driver: argument parsing, module loading, cc invocation
  zerg/           # the compiler library — one directory module, shared scope
    token.zg      # Kind enum + Token type
    lexer.zg      # source text -> token stream (comments kept on request)
    ast.zg        # recursive AST node types (enum payloads)
    parser.zg     # tokens -> AST
    emit.zg       # AST -> C, with the minimal typecheck emit needs
    fmt.zg        # tokens -> canonical source
    lint.zg       # AST -> findings
```

## Using it

```sh
zerg build <file.zg>    # compile a module to an object (--emit bin links a program)
zerg build --emit bin -j8 app.zg   # a program, eight units compiling at once
zerg build --emit c <file.zg>      # stop at the C; likewise `tokens` and `ast`
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
those enums, with `zergc.zg` as a thin driver that only ever calls the module's `pub`
functions — never constructs a variant. The original `src/compiler/zerg/` instinct was
right.

## How a build is put together

A program is compiled UNIT by unit. A unit is a module — one file, or the several files of
a directory module, which share a scope and cannot be split — and each becomes its own
object, which one link puts together. That split is what everything else rests on:

- **Separate compilation.** A unit declares the whole program but defines only its own
  module, so two modules that share an import can be linked side by side. Whole-program
  emission cannot: each object would carry its own copy of the shared module.
- **Caching.** A unit's key is a hash of the C it emitted — which already folds in its own
  source, everything it can see, and the compiler that produced it, since a change to any
  of those changes the C. Content, not timestamps, which is safe precisely because the
  fixpoint proves emission is deterministic. Change a comment and nothing recompiles: a
  comment does not reach the emitted C. The runtime's translation units are cached the
  same way.
- **Parallelism.** Units do not depend on each other once emitted, so `-j` compiles
  several at once. It comes from OS processes, not coroutines: the runtime's scheduler is
  cooperative N:1, so coroutines give concurrency, not CPU parallelism.

Building the compiler with itself, six units: 1.28s at `-j1`, 0.56s at `-j4`, 0.33s when
nothing changed.

## The corpus

`test-data/` is this compiler's. It describes the LANGUAGE — which is what `zerg` is
growing toward — so it follows the language, not the seed; the Go seed in
[`src/bootstrap/`](../bootstrap) covers its own narrow contract with unit tests and reads
none of it.

```sh
make corpus     # build zerg, then run it over test-data/codegen/
make refuse     # every program that must be turned away, is — by the compiler
```

Each case is a `.zg` program beside the stdout it must produce. The Makefile's
`CORPUS_PASS` is the set `zerg` gets right today and is the **gate**: a case that leaves
it is a regression and fails the target. The remaining eight are reported, not enforced:
they need generic **function** definitions, a generic type parameter as a field's type,
`derive`, spec bounds, or `#[dyn]`, none of which the self-hosting compiler has yet. Each
is refused by name — `gen_struct` answers _no type named `T` (field `Box.val`)_ — rather
than mis-emitted.

`make refuse` is the other side of that. Every gate here asks what the toolchain BUILDS,
and the property a refusal needs is not that a bad program fails — it always did — but WHO
says so. A program the compiler emits anyway reaches cc, which reports a real error against
generated C under `.zerg-cache`, at a line the programmer cannot open. So each case in
`scripts/refuse-check.sh` asserts three things: a non-zero exit, the expected sentence, and
no mention of the cache.

Each case that starts passing is a fix or a feature landing, and moves into the list.

**Emit is validated end-to-end, not byte-exact.** Reproducing the Go seed's exact C
formatting and naming across ~9.5k LOC would cost far more than it is worth, so the bar
is **functional equivalence**: the emitted C must compile and the program must print what
the corpus says. Determinism (required anyway by the fixpoint) is what makes that stable;
byte-identical C is explicitly a non-goal.

## Growing emit to the self-compile subset (M3 → M5)

The examples subset (scalars, functions, if / for, match on int) is done end-to-end. The
compiler's own source needs far more, so emit grows feature by feature, each validated by
an end-to-end test program before the next. The C shapes the Go bootstrap emits (the
target) are:

| feature           | C shape                                                           | runtime? |
| ----------------- | ----------------------------------------------------------------- | -------- |
| struct            | `typedef struct {…} zg_T;` value; `(zg_T){a,b}`; `p.zg_f`         | no       |
| enum with payload | `{int32_t tag; union{ struct{…} Var; } u;}`; `.tag` / `.u.Var.fN` | no       |
| recursive enum    | payload fields become `void*` ref-boxes                           | yes      |
| list[T]           | `zrt_list` + `zrt_list_init/push/len/at`; for-in is an index loop | yes      |
| str build / ops   | `list[byte](s)` / `str(bs)` / `+` / `==`                          | yes      |

**Leak-style emit is the simplification.** A self-hosting compiler is a batch tool: it
compiles once and exits, so it never needs to free. Emit therefore **reuses the runtime's
data-structure primitives** (`zrt_list_*`, `zrt_ref_alloc` / `zrt_ref_payload` for boxing)
but **skips the whole memory-management discipline** the Go emit threads through every
function — no `zrt_scope_mark` / `zrt_defer` / `zrt_unwind_to`, no per-type
copy / drop / release, ref-boxes allocated and never released, lists with a `{NULL,NULL}`
element vtable. The OS reclaims on exit. This still honours the "emit C, reuse the runtime"
decision — the runtime data structures are reused — while cutting the bulk of the Go
emit's complexity. Determinism (the only property M5 needs) is unaffected by leaking.

**Increment ladder toward M5** (each end-to-end tested, then committed):

1. structs — decl, construction, field access, `mut &` params, field mutation _(no runtime)_
2. enums with payload — tagged union, construction, match destructuring _(no runtime)_
3. recursive enums — `void*` ref-boxing, leak-style _(runtime)_
4. list[T] + for-in — `zrt_list`, index-loop, monomorphized per element type _(runtime)_
5. str building / ops — `list[byte]` ↔ `str`, `+`, `==` _(runtime)_
6. generics / monomorphization — one concrete emission per instantiation used in source
7. imports — emit the flattened multi-file module as one translation unit

When all seven are in and the corpus of feature programs plus the stdlib compile and run
identically, the compiler can attempt its own source, and M5 begins.

## Self-host proof

The compiler reproducing itself is what makes "self-hosting" a claim rather than a
description, and `make build` is where it is checked: the seed builds an intermediate, the
intermediate builds the compiler that ships, and a compiler that cannot reproduce itself
cannot get through that. `make corpus` and `make lint` are the checks layered on top.

## Bootstrap minimization (M6)

Once the fixpoint holds, the Go bootstrap only needs to compile the `Zerg-boot` subset the
`src/compiler/*.zg` sources (and their imports `io` / `ascii` / `strconv` / `cli`) actually use.
Every removal is guarded by `make build` itself: the seed builds an intermediate, the
intermediate builds the shipped compiler, and a seed that lost something the compiler
needs cannot get through that. `make corpus` and `make lint` are the checks on top.

**The Zerg-boot subset** (what the minimal bootstrap MUST keep):

- declarations: `fn` (incl. `mut &` reference params), `struct`, `enum` (incl.
  self-recursive), `#[derive(Eq)]`, `import`, `pub`
- statements: `x := e` / `x: T = e` / `mut` / `const` bindings, assignment to a name /
  field / index lvalue, `print`, `return` (incl. `return e if c`), `if` / `else if` /
  `else`, `for cond` / `for` / `for x in xs`, `break`, `continue`, `nop`, `guard`, `raise`
- expressions: int / float / str / bool / byte literals, `nil`, identifiers, unary /
  binary operators, calls, field access, indexing, method calls, list literals `[]`,
  conversions (`int`/`byte`/`str`/`list[T]`(x)), `match` (literal / bind / wildcard /
  constructor patterns, optional guard), if-expressions
- types: `int`, `float`, `str`, `bool`, `byte`, `nil`, `list[T]`, named struct/enum,
  `Result[T]`, `This` inside an `impl`
- inherent `impl T { … }` methods with a **value receiver**: the parser flattens the block
  into ordinary functions carrying a `this: T` first parameter, and the C name is
  `zg_<T>_<name>` rather than the flat `zg_<name>` a free function gets. A `mut fn`
  (mutable receiver) is _not_ in the subset — take a `mut &` parameter, or return a new
  value, which is what a chainable builder does anyway.
- the `__zrt_*` runtime intrinsics the bundled stdlib lowers onto

**Strippable** (NOT in the subset — the self-host source never uses them): closures /
first-class functions; coroutines (`spawn` / `chan` / `select`); `map[K,V]`; `spec`
(and so `impl Spec for T`) and generic _function_ definitions; `unsafe` / `asm` / `ptr`;
command literals; `with` / `defer` / `del`; optionals (`T?` / `??` / `!`)
beyond `Result`. The
non-build subcommands (`fmt` / `lint` / `test`) are also dropped — the minimal seed is
`zerg build` only; the self-host compiler can reimplement the tools in Zerg later.

**F-strings left that list** when `F405` landed. The self-host source now uses them —
`zerg fmt` writes them — so the seed must lex and parse `f"…"` to build stage 1. It already
did; what changed is that it is now load-bearing rather than incidental. The shipped
compiler accepts the plain hole only: no `:spec`, no `!r`/`!s`/`!a`, no `{x=}`, and no
interpolating command form, each refused by name rather than by silence. It desugars in
the parser to the `+` chain the form is defined to be, so the AST and the emitter know
nothing about f-strings at all.

## What the shipped compiler accepts

The Zerg-boot list above answers a different question from this one. It says what the
**seed** must keep to build stage 1; it does not say what `zerg` itself understands, and
the two have been drifting apart. What the shipped compiler accepts beyond that subset:

| form                              | note                                              |
| --------------------------------- | ------------------------------------------------- |
| `a..b` / `a..=b`, `for i in a..b` | as a `for` iterable; as a value it is refused     |
| `init()`                          | many, each once, in declaration order             |
| module-level `const`              | a C global, assigned before any `init()`          |
| `spec S { … }`                    | consumed whole; `impl S for T` already worked     |
| `(a, b)` and `t.0`                | one carrier struct per distinct shape             |
| `map[K,V]`, `{k: v}`, `{:}`       | POD keys and values                               |
| `defer f(args)`                   | at the enclosing block's exit, arguments by value |

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

Still missing, and each refused by name rather than mis-emitted: `Ref[T]` (which takes
`std/atomic` with it), `?` over a **`Result[T]`** (which needs `Result[T]` to survive in a
signature, unlike the `T?` half above), a block as a `match` arm body, generic **function**
definitions, a generic type parameter used as a field's type, named-argument struct
construction `T(a: 1)`, and the command literal.

## Performance: parallelism & caching (M7)

A post-fixpoint performance layer. Correctness and a deterministic fixpoint come first
(M1–M5); M7 only adds speed on top, so the enablers below are designed in early and the
scheduler/cache themselves land last.

### Parallelism comes from OS processes, not coroutines

The runtime is a cooperative **N:1** scheduler: `spawn`/channels give concurrency on one
OS thread, not CPU parallelism (preemptive **M:N** is "not yet"). So in-process coroutines
cannot speed up CPU-bound compile work. Real parallelism is fanned out across processes,
and the driver is the orchestrator:

- **`cc` invocations** — many `cc` processes at once. The wall-clock bulk lives in the C
  backend, so the driver spawns a bounded pool and reaps them. Biggest, cheapest win.
- **front-end / unit** — one worker compiler process per module. `lex`/`parse`/`emit` is
  pure and independent per module, so it fans out `make -j`-style across processes.

Coroutines/channels stay useful as the **orchestration** layer — a work queue feeding a
bounded process pool — not as the compute parallelism. Scheduling walks the **module
dependency DAG** the module loader already computes (import edges + init plan): topologically
drain the ready-set, leaves first. When an M:N scheduler lands, the front-end can also go
parallel inside one process.

### Content-addressed cache, per module

- **Unit** — the module (already the compilation/dependency unit).
- **Key** — a `sha256` over: the module source, its imported modules' public-interface
  hashes, the target flags, and the compiler self-version. Missing any component risks a
  stale hit.
- **Two product layers** — `.zg → .c` (this compiler) and `.c → .o` (via `cc`). Because emit
  is deterministic, identical `.c` yields identical `.o`, so caching the `.c` plus a
  content-addressed `.o` store covers the whole pipeline.
- **Interface vs implementation** — hashing a module's exported surface separately lets a
  private-body change skip recompiling its dependents (Go export-data style). The MVP may
  start by hashing whole source and refine later.

### Shared prerequisites

- **Deterministic emit** — stable ordering, no map-iteration randomness, no timestamps in
  output. Required by the M5 fixpoint _and_ by a reliable cache — the same property serves
  both, so it is not extra work.
- **`zrt_mkdir` runtime leaf** — the runtime has no `mkdir` today (only
  open/read/close/open_write/write_bytes/exists/remove). The cache directory
  (`$XDG_CACHE_HOME/zerg/` or `.zerg-cache/`) needs one; add it alongside `zrt_exec` in M0.

### Designed-in enablers (land during M1–M4, not deferred)

- front-end passes stay pure and module-isolated (M2/M3)
- emit is deterministic (M3 — already a fixpoint prerequisite)
- the driver is a per-unit shell-out orchestrator from the start (M4)
- `zrt_exec` supports concurrent children + multi-way `waitpid` (M0)
