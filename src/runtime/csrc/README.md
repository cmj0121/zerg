# Zerg Runtime — C Sources

English | [繁體中文](README.zh-TW.md)

The C implementation of the [Zerg runtime](../README.md): the layer `cc` links into every Zerg program that needs
it. It is portable C over the platform C library (libc / libSystem), with a minimal per-architecture assembly core
for coroutine context switching. Every symbol carries the `zrt_` prefix so it never collides with the `zg_` names
the compiler emits.

## Files, by concern

### Memory

- **`alloc.c`** — the allocator wrapper over `malloc` / `free` (the one seam a freestanding target re-points).
- **`ref.c`** — `Ref[T]`, the reference-counted heap box; retain / release, and the last holder frees it once.

### Collections

- **`list.c`** — the built-in `list[T]`, a by-value growable sequence.
- **`map.c`** — the built-in `map[K, V]`, a by-value insertion-ordered hash table.

### Text & conversion

- **`str.c`** — `str` (a NUL-terminated UTF-8 `const char*`) and the `str` ⇄ `list[byte]` / `list[rune]` bridges.
- **`fmt.c`** — text rendering and the `f"…"` format surface (width, precision, alignment, integer bases).
- **`conv.c`** — primitive conversions `T(x)`: re-construction with range checks, never reinterpretation.

### Concurrency

- **`sched.c`** — the N:1 cooperative coroutine scheduler and `spawn`.
- **`chan.c`** — channels: typed, buffered, with `select`.
- **`ctx_arm64.S`** — the AArch64 coroutine context switch.
- **`ctx_x86_64.S`** — the x86-64 (System V) coroutine context switch.
- **`ctx_ucontext.c`** — a portable `ucontext` fallback where no arch-specific `.S` applies.

### Control & system

- **`entry.c`** — the program-entry shim (`zrt_run` sets up and runs `main`).
- **`unwind.c`** — the abort / unwind mechanism and the `defer` cleanup stack.
- **`sys.c`** — the minimal system surface: the process floor the self-hosted compiler is built on
  (`zrt_exec`, `zrt_proc_spawn` / `zrt_proc_wait`, `zrt_mkdir`, `zrt_listdir`), and the `write` / `read` /
  `open` / `close` syscall floor the stdlib `io`
  lowers onto, abort reporting, the `Atomic[int]` operations, and the command-line `args`.

### Header & tests

- **`zergrt.h`** — the **sole** public header; the compiler's emitted C includes only this, and only when the
  program needs the runtime.
- **`zrt_test.c` / `zrt_test.h`** — the `#[test]` runner harness. Unreferenced: `zerg test` emits a driver
  that reports for itself, in Zerg, so nothing links these.

## Conventions

- **One header.** Emitted C includes `zergrt.h` and nothing else. A value-only program (no `Ref`, no collections,
  no `defer` / `spawn`) includes none of the runtime, and its C is self-contained.
- **`zrt_` prefix** on every symbol, disjoint from the compiler's `zg_` names.
- **Hosted on libc** for the MVP. Two seams are marked for a later freestanding / atomic re-target without changing
  emitted code: the allocator wrapper (`alloc.c`) and the single-threaded reference count (`ref.c`).
- **Layout is a build contract.** The `Ref` / `list` / `map` header layouts the compiler depends on are fixed here;
  they are internal and never FFI-frozen.
- The runtime is the **irreducible floor** — all higher-level logic lives in pure Zerg in
  [`src/stdlib/`](../stdlib) (see [`../README.md`](../README.md)).
