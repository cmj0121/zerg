# Zerg release notes

One-screen summary per version of what shipped. Rationale and implementation detail live in the
commit log; the formal language reference lives in [`docs/LANGUAGE.md`](docs/LANGUAGE.md); the
per-module stdlib reference lives in [`docs/STDLIB.md`](docs/STDLIB.md).

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
