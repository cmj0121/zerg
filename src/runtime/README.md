# Zerg Runtime

English | [繁體中文](README.zh-TW.md)

`src/runtime/` is the **Zerg runtime** — the small, self-contained layer that every compiled Zerg program links
against, together with the Go glue that ships it inside the `zerg` toolchain.

Zerg is **zero external dependency, like Go**: a compiled program pulls in no third-party library. The runtime is
the one irreducible floor — it reaches the OS through the platform C library (libc / libSystem, the same foundation
Go links) and nothing else. Everything above it — the standard library in [`src/stdlib/`](../stdlib) — is **pure
Zerg**.

## Two layers

- **`csrc/`** — the runtime **itself**, in C plus a small per-architecture assembly core. This is what `cc` links
  into a program: allocator, reference counting, collections, strings, formatting, the scheduler, channels, the
  syscall floor, and the unwind mechanism. See [`csrc/README.md`](csrc/README.md) for the file-by-file map.
- **`embed.go`** — Go glue (not shipped into a program). It `go:embed`s the `csrc/` tree into the `zerg0` SEED binary so
  `zerg build` can materialize the sources next to the emitted C for `cc`.
- **`runtime_test.go`** — Go tests that compile and exercise the C runtime directly (via `csrc/zrt_test.*`).
- **`go.mod`** — the runtime's Go module, wired into the root `go.work`.

## How it reaches a program

1. At toolchain build time the C sources are embedded into the `zerg0` seed (`embed.go`, `go:embed`). The
   self-hosted `zerg` does not embed them: it reads the tree from disk, at `$ZERG_RUNTIME` or
   `$ZERG_ROOT/src/runtime/csrc`.
2. `zerg build foo.zg` emits C for `foo`, **materializes** the runtime sources beside it, and invokes `cc`.
3. `cc` compiles and links them into one binary. A value-only program that needs no runtime links none of it — its
   emitted C is self-contained.

## Cross-compilation

Because the backend is **emit C → `cc`**, cross-compiling a Zerg program is just cross-compiling that C:
`cc --target=…` builds both the emitted code and the runtime for the target. The runtime is portable POSIX C, so any
hosted platform with a libc works. The one per-architecture exception is the coroutine context switch
(`csrc/ctx_*.S`), selected by target arch — the same small platform-specific core Go also keeps.

## The runtime / stdlib boundary

The runtime is the **thinnest possible floor**: raw syscalls, memory, reference counting, the scheduler, container
primitives, text rendering. All higher-level logic lives in pure Zerg above it ([`src/stdlib/`](../stdlib)) — for
example `io.read_file`'s read loop and `io.write_int`'s decimal conversion are Zerg over the runtime's syscall
leaves, and `math`'s `sqrt` / `pow` are Zerg numerical algorithms, never a libm binding. Keeping this line sharp is
what makes the language self-contained.
