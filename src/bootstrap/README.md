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

None of these appears in the self-host chain; each was verified absent, not assumed. `state`
says whether the seed refuses it today or still lowers it — the second column is the work
list, and a row leaves it by being refused, never by being quietly dropped.

| Form                                                                   | State     |
| ---------------------------------------------------------------------- | --------- |
| `map[K, V]` and map literals                                           | refused   |
| closures / first-class `fn` values                                     | refused   |
| `#[dyn]` dispatch                                                      | refused   |
| `spec` / `impl Spec for T`                                             | to refuse |
| generic **function** definitions `fn f[T]`                             | to refuse |
| coroutines: `spawn`, `chan[T]`, `select`, `<-`, `close`, `for v in ch` | refused   |
| optionals `T?`, `??`, `?.`, `!`                                        | to refuse |
| `with`, `defer`, `del`                                                 | to refuse |
| tuples `(a, b)` and `t.0`                                              | to refuse |
| ranges `a..b` as a value or `for` iterable                             | to refuse |
| slicing `xs[a..b]`                                                     | to refuse |
| decorators, incl. `#[derive]`                                          | to refuse |
| module-level `const`, `init()`                                         | to refuse |
| `unsafe`, `asm`, `ptr[T]`                                              | refused   |
| command literals                                                       | refused   |

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
