# Zerg release notes

One-screen summary per version of what shipped. Rationale and implementation detail live in the
commit log; the EBNF grammar reference lives in [`docs/GRAMMAR`](docs/GRAMMAR).

## v0.17 — arbitrary-precision arithmetic + operator-spec wiring

- `std/math/big` module: `BigInt` (signed unbounded integers) and `BigDecimal` (signed
  unbounded fixed-point decimals). Pure-Zerg; opaque internals (sign / digits / unscaled /
  scale all private). Schoolbook O(n²) arithmetic; constructors `from_int(x)` / `from_str(s)`
  and `decimal_from_int(x)` / `decimal_from_bigint(bi)` / `decimal_from_str(s)`.
- Bundled operator specs replace the per-operator design:
  - `Arithmetic { add sub mul div mod neg }` lights `+`, `-`, `*`, `/`, `%`, unary `-`.
  - `Comparable { eq lt }` lights `==`, `!=`, `<`, `<=`, `>`, `>=` (the four derived
    orderings desugar via swap and/or negation at typeck time).
  - `From[T] { from(value: T) -> Self }` declared as a Rust-style conversion contract;
    user impls require v0.18 (`impl X for Spec[T]` parser support + static-method dispatch).
- Primitive types auto-satisfy the relevant bundles (Arithmetic covers int / float;
  Comparable covers int / float / byte / rune / str). Generic-fn bounds
  `fn sum[T: Arithmetic](xs: list[T]) -> T` compose uniformly across primitives and user
  types (BigInt).
- `std/math` reorganised as a directory module: `math/{mod.zg, utils.zg, spec.zg, big.zg}`.
  `import "math"` still resolves (via `math/mod.zg`); the new `math/spec` ships a prose
  contract for the bundled operator specs ahead of the v0.18 source-level declarations.
- BigInt impls both Arithmetic and Comparable; BigDecimal impls Comparable only (the
  bundled Arithmetic shape can't admit BigDecimal's three-arg `div(other, scale, mode)` —
  users call `bd.add(other)` / `bd.mul(other)` / `bd.div_checked(other, scale, mode)` as
  inherent methods until v0.18 splits Arithmetic into Numeric + DivMod sub-bundles).
- Mixed-type operands reject with a focused diagnostic: `BigInt + int` fails at typeck —
  convert one operand explicitly with `big.from_int(n)`.
- `cliVersion` 0.17.0; `version.Minor` 17.

## v0.16 — bare-identifier string interpolation

- `"hello {name}, you are {age} years old"` — bare-identifier interpolation in string
  literals. The interpolated identifier must resolve to one of the six primitive types
  (int, float, bool, str, byte, rune) at typeck; composite types reject.
- Lexer pivots to a structured-token sequence on encountering an unescaped `{`, emitting
  `InterpStart / InterpLit / InterpVar* / InterpEnd` tokens that the parser assembles
  into an `InterpolatedStringLit` AST node.
- Escape mechanism extends to `\{` and `\}` for literal braces. Composes with the existing
  `\n` / `\t` / `\"` / `\\` escapes.
- Arbitrary expressions inside `{...}` are reserved — only a bare IDENT is admitted at
  v0.16. Multi-line and raw strings remain reserved for v1.0+.
- Five new runtime helpers (`zerg_int_to_str` / `_float_` / `_bool_` / `_byte_` / `_rune_to_str`)
  wired through both `zerg run` and `zerg build` paths with byte-identical output.
- `cliVersion` 0.16.0; `version.Minor` 16.

## v0.15 — tuple parallel reassignment + docs retirement

- `a, b = b, a + b` — bare-comma multi-LHS reassignment. RHS evaluated entirely before
  any LHS slot is written, so each RHS expression reads the pre-assignment value of every
  LHS name. Canonical Fibonacci step in one line.
- LHS: bare identifiers only (≥ 2, distinct). Field access (`p.x, p.y = ...`) and
  index-assign (`arr[i], arr[j] = ...`) deferred to v1.0+. Compound multi-assign
  (`a, b += 1, 1`) deferred.
- Every LHS name must be a pre-declared `mut` binding. `let` / `const` reject with focused
  diagnostics.
- RHS admits either a comma-list of expressions matching the LHS arity, or a single
  expression that types as a tuple of matching arity (e.g. `q, r = divmod(10, 3)`).
- `docs/LANGUAGE.md` and `docs/STDLIB.md` retired in this cycle — `docs/GRAMMAR` is now the
  canonical language reference.
- `cliVersion` 0.15.0; `version.Minor` 15.

## v0.14 — pure-Zerg stdlib + sys/syscall + v0.14 nullable surface

- Stdlib migrates off the `bootstrap_provided/` shims onto pure-Zerg implementations atop
  `sys/syscall` intrinsics. `math.zg`, `strings.zg`, `io.zg`, `time.zg`, `os.zg` all
  re-authored in pure-Zerg with no `__builtin` outside `sys/syscall/`.
- New `sys/syscall` per-host module form: `mod_<goos>_<goarch>.zg` selected by the loader's
  per-host probe (matches `sys/path.zg`'s flat single-file form).
- `T?` nullable surface refined: bare `Ok` / `Err` sugar at typed contexts; β-pure
  auto-unwrap at the call boundary; `Option` hidden as user-visible spelling.
- `never` bottom type retired — its sole client (`os.exit`) now returns `void` and the
  `__zerg_unreachable` marker carries the diverge property at codegen time.
- `cliVersion` 0.14.0; `version.Minor` 14.

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
- New formal references shipped in-tree: `docs/LANGUAGE.md` (grammar + type system) and
  `docs/STDLIB.md` (per-module fn reference). Both retired post-v0.14; see `docs/GRAMMAR`.
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
