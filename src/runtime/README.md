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

## Testing the scheduler, and where ThreadSanitizer stops

`make -C src/runtime test` compiles `csrc/` with the host `cc` and runs it. That suite is the only place the runtime
is exercised as C rather than as a by-product of a Zerg program, and until it was wired into the root `make test` it
never ran at all — a `map` bug and three scheduler races all lived here undisturbed.

`TestConcurrencyStress` is the part that watches for races. It cannot check _when_ things happened, because under M:N
nothing about the order is promised, so it checks the one thing an interleaving may not change: the arithmetic. Every
producer sends a known set of values, the consumer adds up what arrives, and the total is fixed. A lost wake-up hangs
it, a doubly-queued coroutine or a hand-off delivered twice moves the sum. It runs many times, because a race that
survives one pass is ordinary.

**ThreadSanitizer builds and runs, but is not a gate.** A binary is instrumented with `-fsanitize=thread` like any
other; the fiber annotations in `zergrt.h` are what make that possible at all, since without them TSan follows a
`zrt_ctx_swap` onto a stack it has no shadow for and dies on a signal rather than reporting anything.

What it then reports is, so far, all one artifact. The park protocol has a coroutine acquire a channel's lock and the
_scheduler_ release it, once the coroutine is off the CPU — that hand-off is the whole point, because a counterparty
must not find a waiter belonging to a coroutine that is still running. It happens on one OS thread, so pthreads is
satisfied, but TSan counts fibers as threads: it sees a mutex released by someone other than its owner and stops
deriving happens-before from it, after which every access under that lock reads as a race. The reports are real
output about a model that does not fit, not findings — each one lands on a line that provably holds the lock.

So the stress test is the gate and TSan is a tool to reach for deliberately. Making it a gate needs the hand-off
described to TSan rather than hidden from it, and that is not done.
