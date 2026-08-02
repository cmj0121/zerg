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
- **`embed.go`** — Go glue (not shipped into a program). It `go:embed`s the `csrc/` tree into the `zerg0`
  SEED binary so
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

Timers sit on that line too, and show where it falls. The runtime owns exactly one thing a coroutine cannot do for
itself: park until a deadline (`zrt_sleep_ns`), which needs the scheduler because an idle worker has to sleep to that
deadline and because the deadlock detector has to know a sleeping coroutine is going to make progress. Everything a
program actually uses is Zerg above it — `time.after(d)` is a coroutine that sleeps and then sends, `ticker(d)` is
that in a loop, and a timeout is a `select` arm on the channel either returns. No duration type, no formatting and no
policy is built into the runtime, because none of it has to be.

## Testing the scheduler, and where ThreadSanitizer stops

`make -C src/runtime test` compiles `csrc/` with the host `cc` and runs it. That suite is the only place the runtime
is exercised as C rather than as a by-product of a Zerg program, and until it was wired into the root `make test` it
never ran at all — a `map` bug and three scheduler races all lived here undisturbed.

`TestConcurrencyStress` is the part that watches for races. It cannot check _when_ things happened, because under M:N
nothing about the order is promised, so it checks the one thing an interleaving may not change: the arithmetic. Every
producer sends a known set of values, the consumer adds up what arrives, and the total is fixed. A lost wake-up hangs
it, a doubly-queued coroutine or a hand-off delivered twice moves the sum. It runs many times, because a race that
survives one pass is ordinary.

Every concurrency test runs twice: once with the machine's worker count and once with `ZRT_WORKERS=1`. The point is
to tell two failures apart — a bug in the scheduler's own logic survives with one worker, while a race needs several
— and the single-worker mode is the harsher of the two, because nothing else is running to paper over a coroutine
that never yields. It earned its place immediately: `select` had a livelock that only one worker could expose, since
with several the coroutines that would have unstuck it were simply run by somebody else.

Alongside the stress test, the suite pins the behaviours the scheduler promises rather than only its consistency:
that main returning ends the program, that a deadlock is a clean catchable abort which leaves no waiter behind in a
channel's queue, that `select` raises rather than firing an arm when everything it watches has closed, and that a
timer wakes on time without burning a CPU while it waits. Each of those fails by hanging, so each runs under its own
deadline — a regression names itself instead of stopping the suite.

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

## Debugging a race: `ZRT_TRACE`

A race in the scheduler or the channels is a question about **order**, and the two tools
that can answer most questions cannot answer that one: AddressSanitizer names the
instruction that faulted and the allocation it touched, and a debugger stops the world the
race needs. What is missing is the sequence.

`-DZRT_TRACE` compiles in two things, and a build without it has **neither** — every macro
expands to nothing, not even a branch, so a shipped binary is byte-identical to one from a
tree that never had this.

- **The live-waiter invariant**, armed whenever the define is present. A `zrt_waiter` lives
  on the stack of the coroutine that parked, so a queue still pointing at one whose stack is
  being freed is a hand-off into unmapped memory — the SEGV in `wq_pop`. Every waiter on a
  queue is registered; freeing a coroutine's stack asserts none of them lies inside it. It
  catches the run that MAKES the mistake rather than the one that trips over it, which is a
  far larger set of runs, and it prints nothing unless it fires.
- **A per-event log**, additionally gated at runtime by `ZRG_TRACE=1`, one line per
  queue push/pop/remove, per close, per sender release and per stack free.

`make sanitize-conc` arms the invariant by default; `TRACE=0` turns it off, and `ZRG_TRACE=1`
in the environment adds the log:

```sh
make sanitize-conc                                   # invariant on, no output
CASES=test-data/codegen/conc_actor.zg TRACE=0 make sanitize-conc
```
