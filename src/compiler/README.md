# Self-hosting Zerg compiler

The Zerg compiler, written in Zerg. It lexes, parses, and emits C over the existing
runtime floor, then invokes `cc` — the same pipeline the Go bootstrap runs, re-expressed
in the language itself. This directory is the target of the `feat/self-hosting` effort.

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
  zergc.zg        # driver program: import "zerg"; main() [M1: token dump]
  zerg/           # the compiler library — one directory module, shared scope
    token.zg      # Kind enum + Token type (mirrors internal/token)   [M1]
    lexer.zg      # source text -> token stream                       [M1]
    ast.zg        # recursive AST node types (enum payloads)          [M2]
    parser.zg     # tokens -> AST                                     [M2]
    emit.zg       # AST -> C, with the minimal typecheck emit needs   [M3]
src/stdlib/
  cli.zg          # standalone CLI arg parser (argparse/kong-like)    [M0]
  os.zg           # + run()/process wrapper over __zrt_exec           [M0]
```

The `zerg/` module sub-level — which the original proposal called redundant — is in
fact required by two bootstrap facts found while building M1: (1) an `import "x"`
resolves to a **directory module** whose multiple files flatten into one shared scope,
so `token.zg`/`lexer.zg`/`parser.zg` can share the `Kind` and AST enums; but (2)
**enum variants are not reachable across a module boundary** (`token.Fn` is rejected).
The library must therefore live in one directory module (`zerg/`) whose files share
those enums, with `zergc.zg` as a thin driver that only ever calls the module's `pub`
functions — never constructs a variant. The original `src/compiler/zerg/` instinct was
right.

## Diff-test harness

Every front/back-end pass has a golden oracle in the Go bootstrap:

| pass   | oracle                            |
| ------ | --------------------------------- |
| lexer  | `zerg build --emit tokens <file>` |
| parser | `zerg build --emit ast <file>`    |
| emit   | `zerg build --emit c <file>`      |

`scripts/selfhost-difftest.sh` runs a pass of the Zerg compiler against the matching
Go-bootstrap oracle over a corpus (`examples/*.zg` + `test-data/`) and reports the first
divergence. This is the primary correctness signal until M5.

**Emit is validated end-to-end, not byte-exact.** The lexer and parser are structural,
so they are diff-tested byte-for-byte (tokens; and a structural AST dump). The emitter is
not: reproducing the Go bootstrap's exact C formatting and naming across ~9.5k LOC would
cost far more than it is worth. Instead the emit bar is **functional equivalence** — the
emitted C must compile and the program must behave identically to the Go-bootstrap build.
Validation is `zergc --emit c → cc → run`, diffing the program's output against the
Go-bootstrap build over the corpus. Determinism (required anyway by the M5 fixpoint) is
what makes this stable; byte-identical C is explicitly a non-goal.

## Self-host proof (M5)

The acceptance bar is a byte-identical fixpoint:

```text
stage0 = Go bootstrap
stage1 = stage0 compiles the src/compiler/*.zg sources
stage2 = stage1 compiles the same sources
assert  sha256(stage1) == sha256(stage2)
```

When stage1 and stage2 agree byte-for-byte, the compiler reproduces itself and the
self-host is real.

## Bootstrap minimization (M6)

Once the fixpoint holds, the Go bootstrap only needs to compile the `Zerg-boot` subset the
`src/compiler/*.zg` sources actually use. M6 surveys that subset and strips every Go
`sema`/`emit` branch off that happy path, keeping the minimal seed that still builds the
whole self-host compiler.

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
