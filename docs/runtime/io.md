# Zerg Process & I/O

How a Zerg program reads and writes the outside world — files, the standard streams, and child processes.
Built on the memory, error, spec, and iteration models in the [Language Reference](../language.md), the
`Ref[T]` box and coroutines in [Coroutines & Channels](../code/coroutine.md), and the C boundary in the
[FFI](ffi.md). Also in [繁體中文](io.zh-TW.md).

I/O is the stdlib **`io`** package (`import "io"`, never ambient); the sole exception is the **`print`**
keyword (Formatting & text), the no-import shortcut for writing a value to stdout. Three ideas carry it,
each reusing an existing model:

- a **stream** is a `Reader` or `Writer` — a byte source/sink, drained with `for`;
- a **handle** is a `Ref[T]` — a file or socket, scope-owned and closed exactly once;
  **[not yet]** — there is no `Ref[T]` type in this compiler (`Ref(x)` is refused by name), so no handle
  type exists and the whole-file leaves below are what reading and writing go through;
- **failure is a value** — a fallible call returns `Result[T]`, `?`-propagated; EOF is not one.

> **Status.** The compiler ships a **subset** of this surface, and the subset is the **whole-file and
> whole-stream leaves**: `io.read_file(path) -> list[byte]` and `io.write_file(path, data)` (a missing or
> unreadable file raises **`IOError`**, which `guard { io.read_file(p) }` demotes to a `Result`; decode with
> `str(…)` when the bytes are text), `io.read_stdin() -> list[byte]`, the stdout writers `io.write` /
> `io.println` / `io.write_int`, the stderr writers `io.ewrite` / `io.eprintln`, and the `print` keyword.
> The **`Reader` / `Writer` spec surface** — `read_bytes` / `read()` / `write` and the `io.stdin` ·
> `io.stdout` · `io.stderr` stream objects described below — is **[not yet]**: the intended semantics stand
> as specified, and reaching one of those names is **`E388`** — _module `io` has no `stdout`_ — in any
> position, including a method call's receiver.

## Streams — `Reader` & `Writer`

**[not yet]** — the streaming surface below is specified but unbuilt; today use `io.read_file` for input
and `io.write` / `io.println` for output.

Each has **one required method**; the rest are provided defaults (Specs & Generics), so a new stream
supplies the primitive and inherits the conveniences.

**`Reader`** — `read_bytes(n: uint) -> Result[list[byte]]`, up to `n` bytes (empty = end of input, never a
blocking wait). Over it: **`read() -> Iterator[str]`**, the default line reader — `for line in f.read()`
drains cleanly, ending at EOF (`StopIteration`) and re-raising a decode or device error mid-stream
(Iteration); for possibly-invalid UTF-8, take `read_bytes` and decode under `guard`. Also
`bytes() -> Iterator[byte]` and `chunks(n) -> Iterator[list[byte]]`.

**`Writer`** — `write(bytes: list[byte]) -> Result[uint]` (count written); provided `write_str(s: str)`
and `flush()`. A write failure — full disk, broken pipe — is a value, `?`-propagated; it never silently
drops (that is `print`'s alone).

```text
fn copy_lines(src: Reader, mut &dst: Writer) -> Result[nil] {
    for line in src.read() {
        dst.write_str(line)?
        dst.write_str("\n")?
    }
    return dst.flush()
}
```

## Files & handles

A **`File`** is a `Ref[handle]` in an opaque newtype (the foreign-handle pattern, [FFI](ffi.md)), so it is
**closed exactly once**, at the last holder's scope exit or an explicit `del`; a file confined to one
scope wants `defer f.close()` instead.

```text
open(path: str)   -> Result[File]      # read; File implements Reader
create(path: str) -> Result[File]      # write/truncate; File implements Writer
```

A missing file is an **expected** value-failure, never an abort. Open modes, seeking, and metadata are
`io` methods — stdlib detail, not new concepts. A **socket** is the same shape: a `Ref[handle]` that is a
`Reader` and `Writer`, its network API left to `io`.

> **[not yet]** The `File` handle and `open` / `create` are unbuilt this phase; what is wired is the pair of
> whole-file leaves `io.read_file(path) -> list[byte]` and `io.write_file(path, data)`, plus
> `io.read_stdin()`. A missing or unreadable file raises **`IOError`** (demote with `guard`), consistent
> with "a missing file is an expected value-failure" once `guard`ed. Neither leaf hands back a handle, so
> nothing has to be closed and `defer f.close()` has nothing to name.

## Standard streams

`import "io"` binds **`io.stdin`** (`Reader`), **`io.stdout`** and **`io.stderr`** (`Writer`s) — read-only
OS facts through the stdlib, like `env` and the clock ([Modules, Packages & Programs](package.md)), never
`main` parameters. The **`print`** keyword is the no-import shortcut — `print x` writes the value's
`display` rendering and a newline to stdout, best-effort; use `io.stdout` when you need the `Result`,
`io.stderr`, or raw bytes.

```text
import "io"
for line in io.stdin.read() { io.stdout.write_str(transform(line))? }
```

> **[not yet]** The `io.stdin` / `io.stdout` / `io.stderr` stream objects are unbuilt, and writing one is
> **`E388`** — _module `io` has no `stdout`_ — including as a method call's receiver, the position the
> example above writes it in. What is wired this phase is free functions: `io.write(s)` / `io.println(s)` /
> `io.write_int(n)` to stdout, `io.ewrite(s)` / `io.eprintln(s)` to stderr, and `io.read_stdin()` for all of
> standard input at once (each writer returns `Result[nil]` but writes best-effort, never yielding an `Err`
> yet).
>
> **[deviation] / [implementation-defined] buffering.** `print` writes through **buffered libc stdio**
> while `io.*` writes go **unbuffered** through `write(2)`. Their output can therefore **interleave out of
> source order** — an `io.*` write may appear before an earlier `print` still sitting in the libc buffer.
> The spec does not fix a buffering discipline across the two; to force ordering, keep a run of output on
> one path, or flush. (See [Conformance](../conformance.md) on implementation-defined behavior.)

## Blocking — at the coroutine, not the thread

Native `io` reads synchronously but never blocks the runtime: a `read_bytes`/`write` that must wait
**parks its coroutine** and the scheduler runs another — the fairness guarantee of any channel wait
([Coroutines & Channels](../code/coroutine.md)), with no `async`/`await` and no colored functions. The one
exception is the FFI edge: a blocking **foreign (FFI) C call** parks its whole OS thread, since Zerg does not
own that frame ([FFI](ffi.md)).

> **[not yet]** Coroutine-parking `io` is part of the unbuilt stream surface above. Per-thread parking of a
> blocking foreign call has arrived with the **M:N** scheduler ([Coroutines & Channels](../code/coroutine.md)): such
> a call now occupies **one worker** while the others keep running Zerg coroutines. On a single-worker host
> it is still the whole program that stops.

## Process & command execution

**[not yet]** — a command literal is lexed and then **refused by the parser**, each form by its own name and
each with a place: the static `` `git status` `` is `E236`, the interpolating `` f`git checkout {b}` `` is
`E235`. The intended model below stands unchanged for when the runtime lands. What does ship is
`os.run(argv: list[str]) -> int` ([Standard Library](stdlib.md)) — argv straight to the OS, no shell and no
pipes, so it covers running a child and reading its exit status and nothing else about this section.

A child process is spawned with a **backtick command literal** and observed through the same streams — its
pipes are `Reader`s and a `Writer`, its handle a `Ref[proc]` whose `drop` waits for (or kills) it, reaping
it exactly once. **The `f` marks the danger:**

- **`` `git status` ``** — a **static** literal, split to argv on whitespace (quotes respected) and run
  **directly, no shell**: no interpolation, so no injection, glob, or pipe — the safe default (Go / Rust /
  Elixir).
- **`` f`git checkout {branch}` ``** — **interpolated**, run **through a shell** (so pipes and redirection
  work); each `{x}` is **shell-quoted to one argument** by default (defeating command-injection, not a
  hostile `-flag`), a **raw** splice being the explicit `{x:raw}`. The `f` reads as on an `f`-string.

```text
p := `ls -l`
for line in p.stdout.read() { print line }    # p.stdin: Writer; p.stdout/stderr: Reader
code := p.wait()!                             # blocks this coroutine for the exit status
```

To wait on several at once — stdout, stderr, a timeout — bridge each `Reader` into a channel and `select`
(fan-in, [Coroutines & Channels](../code/coroutine.md)); the model adds no new waiting primitive. A child is a
foreign resource, so its thread-safety and lifetime follow the FFI rules — one owner coroutine unless
deliberately shared ([FFI](ffi.md)).

## Deferred

- The concrete **`io` catalogue** — open modes, seeking, buffered wrappers, sockets/network — is stdlib.
- A **write-back buffer** (`read`/`recv` filling a caller's `list[byte]`) is the FFI out-buffer open
  question ([FFI](ffi.md)); until then `read_bytes` returns a fresh `list[byte]`.
- **Format specifiers** (`f"{x:>.2f}"`) route to a per-type format protocol (Formatting & text).
