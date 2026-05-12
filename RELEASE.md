# Zerg release notes

One-screen summary per version of what shipped. Rationale and implementation detail live in the
commit log; the formal language reference lives in [`docs/LANGUAGE.md`](docs/LANGUAGE.md); the
per-module stdlib reference lives in [`docs/STDLIB.md`](docs/STDLIB.md).

## v0.13 — platform-suffix file resolution + inline assembly (macOS arm64)

- Inline assembly behind `asm { … }`. Restricted to macOS arm64 for v0.13; Linux + x86 defer.
  The keyword reserves at v0.13; v0.12 and earlier corpora keep parsing `asm` as `IDENT`.
- Body bytes are captured verbatim by the lexer between matching braces. Brace counting is
  string-literal aware: `}` inside `"…"` does not close the block. The interpreter cannot
  execute machine code, so `zerg run` rejects asm-bearing programs with
  `inline asm requires 'zerg build' (interpreter cannot execute machine code)`. `zerg build`
  is the path; the emitted C is a GCC `__asm__ volatile` with the conservative caller-saved
  arm64 clobber set (memory, cc; x0–x17 + x30; v0–v7 + v16–v31; x29 is user-preserve).
- `${name}` interpolation. `name` must resolve to an in-scope binding whose type is `byte`
  (lowered to a register-width input operand) or `list[byte]` (lowered to `.data` pointer).
  Other types reject at typecheck. `$$` is reserved.
- **Caveat:** v0.13 demos use raw `svc #0x80` syscalls because the existing
  `examples/13_asm.zg` fixture targets that surface. Apple has been clear that raw syscall
  numbers are not a stable ABI for user binaries; v0.14 is expected to migrate the demos to
  libc-call lowering. Programs that need long-term ABI stability against Darwin should reach
  libc through normal Zerg bindings, not through inline `svc` traps.
- New `*_macos.zg` / `*_linux.zg` platform-suffix rule (landed at U1) gates sibling-imported
  files on `runtime.GOOS`. Stdlib resolution does NOT consult the suffix table — stdlib stays
  platform-neutral; per-module platform branching defers to v0.14.
- `cliVersion` 0.13.0; `version.Minor` 13.

## v0.12 — M:N coroutine runtime

- Build-side concurrency rewritten from pthread-per-`spawn` to an M:N green-thread scheduler.
  Worker pool sized from `ZERG_MAXPROCS` (defaults to host CPU count); main becomes coroutine 0;
  the pool tears down once every spawned coroutine has finished and main has returned.
- Cooperative scheduling: coroutines yield at `chan` send/recv, `select`, `wait_group.wait()`,
  and `defer` block exit. CPU-bound tight loops without a yield point starve their worker —
  preemption defers to v0.13+. No new surface is added; user-visible `spawn`/`chan`/`select`/
  `wait_group`/`defer` semantics are unchanged.
- Fixed 256 KiB per-coroutine stack with one mmap'd PROT_NONE guard page; overflow surfaces as
  SIGSEGV instead of silent corruption. Per-arch context switch via POSIX `ucontext_t`
  (`getcontext` / `makecontext` / `swapcontext`).
- Channel runtime rewritten end-to-end: `_send` / `_recv` park the calling coroutine on the
  channel's wait queue instead of condvar-waiting. `wait_group` mirrors the pattern. `select`
  yields cooperatively between ready-arm sweeps (full wait-queue registration deferred).
- `defer` stack moves from per-OS-thread to per-coroutine. Top-level user code wraps in a
  `__zerg_top_main` coroutine so top-level defers + any user fn called from main runs in coro
  context. `os.exit()` keeps v0.9 semantics: bypasses scheduler via `_exit` (not `exit`) so libc
  atexit teardown does not race the still-running worker threads' access to scheduler globals.
- Interpreter half is unchanged (Go goroutines were already M:N); the v0.7 parity rule —
  byte-identical for sequential code, equivalent under any valid scheduling for concurrent code —
  carries through, with the full v0.7 / v0.9 / v0.11 corpus passing on the new runtime.
- `cliVersion` 0.12.0; `version.Minor` 12.

## v0.11 — bare-binding form (retire `let` from grammar)

- Immutable bindings drop the `let` keyword: `x := 10`, `x: int = 7`, `(a, b) := pair`.
- `mut`, `const`, and rebind `x = expr` are unchanged. `let` stays a reserved word at the lexer
  layer — no parser shape consumes it.
- Focused diagnostics for almost-binding shapes (`(a) :=`, `(a, b) =`, `(a, b): T =`,
  bare `IDENT NEWLINE`).
- `examples/` rewritten to use only shipped surface; new `TestExamplesBuild` gates the directory
  against future drift.
- Build fix: spawn-of-named-call codegen no longer mangles synthetic env-field slots;
  `spawn worker(7)` builds again.
- `cliVersion` 0.11.0; `version.Minor` 11.

## v0.10 — hardening + language reference

- `zerg fmt` canonical formatter with leading-line comment preservation; subflags `-w`
  (write-in-place, atomic) and `--check` (CI gate).
- New formal references shipped in-tree: [`docs/LANGUAGE.md`](docs/LANGUAGE.md) (grammar + type
  system) and [`docs/STDLIB.md`](docs/STDLIB.md) (per-module fn reference).
- Stdlib fn signatures frozen through v1.0; v0.8's "provisional" marker dropped.
- Diagnostic hardening: every reject-corpus diagnostic carries `file:line:col`; cascade messages
  suppressed.
- `cliVersion` 0.10.0.

## v0.9 — process surface + time (centrepiece: `never`)

- `never` bottom type: `-> never` fns must diverge; `never <: T` for every concrete `T`.
- `std/os.argv()` and `std/os.exit(code) -> never`.
- `std/time.now_ms()` (lazy zero-on-first-call epoch) and `time.sleep_ms(ms)`.
- `os.exit()` does NOT drain defers and does NOT join spawned tasks (matches Go's `os.Exit`).
- Per-feature codegen gates keep v0.0–v0.8 output byte-identical to pre-v0.9.
- `cliVersion` 0.9.0.

## v0.8 — standard library

- Toolchain-shipped modules `std/io`, `std/strings`, `std/math`, `std/os` accessed via the v0.5
  module surface (`import "std/io"`); source `.zg` lives embedded in the toolchain binary.
- Typed-error contract: `IoError` and `ParseError` enums replace error-string returns.
- New fn-decl form `fn name(...) -> R __builtin <ident>` for the closed-registry runtime hook;
  `__builtin` only lexes inside `std/` files at `# requires: >= v0.8`.
- Per-feature codegen gate keeps v0.0–v0.7 output byte-identical to pre-v0.8.
- `cliVersion` 0.8.0.

## v0.7 — concurrency runtime

- `chan[T]` (buffered / unbuffered), `ch <- v` send, `<- ch` recv (Option-typed), `close(ch)`,
  `for v in ch`.
- `spawn fn-call`; anon-fn `fn(p: T) -> R { ... }` with immutable-only captures (composites
  deep-copied at closure creation).
- `defer` at fn-body scope; drains on every exit path including `?` propagation.
- `select { ... }` with recv-bind / recv-discard / send / default arms.
- `wait_group()` for fan-in coordination.
- Parity rule extends: byte-identical for sequential code; equivalent-under-any-valid-scheduling
  for concurrent code (formalised by `test/v0_7/scheduling/`).

## v0.6 — generics and null-safety

- Generic type-params `[T]`, `[T: Spec]`, `[T: A + B]` on fn / struct / enum / spec / impl,
  monomorphised per use site.
- Built-in `Option[T]` and `Result[T, E]`; postfix `T?` sugar for `Option[T]` admitted in every
  type position.
- `nil` literal resolving to a contextually-inferred `Option[T].None`.
- Operators `?` (propagation), `??` (coalesce), `?.` (safe navigation).
- Symmetric `T → T?` lift at every typed boundary; bidirectional inference at call sites.
- User redecls of `Option` / `Result` reject with the reserved-name diagnostic.
