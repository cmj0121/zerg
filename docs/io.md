# Zerg Process & I/O

How a Zerg program reads and writes the outside world — files, the standard streams, and child processes.
Built on the memory, error, spec, and iteration models in the [Language Reference](language.md), the
`Ref[T]` box and coroutines in [Coroutines & Channels](coroutine.md), and the C boundary in the
[FFI](ffi.md). Also in [繁體中文](io.zh-TW.md).

I/O is the stdlib **`io`** package (`import io`, never ambient); the sole exception is the **`print`**
keyword (Formatting & text), the no-import shortcut for writing a value to stdout. Three ideas carry it,
each reusing an existing model:

- a **stream** is a `Reader` or `Writer` — a byte source/sink, drained with `for`;
- a **handle** is a `Ref[T]` — a file or socket, scope-owned and closed exactly once;
- **failure is a value** — a fallible call returns `Result[T]`, `?`-propagated; EOF is not one.

## Streams — `Reader` & `Writer`

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
fn copy_lines(src: Reader, mut dst: Writer) -> Result[nil] {
    for line in src.read() { dst.write_str(line)?; dst.write_str("\n")? }
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

## Standard streams

`import io` binds **`io.stdin`** (`Reader`), **`io.stdout`** and **`io.stderr`** (`Writer`s) — read-only
OS facts through the stdlib, like `env` and the clock ([Modules, Packages & Programs](package.md)), never
`main` parameters. The **`print`** keyword is the no-import shortcut — `print x` writes `x.display()` and a
newline to stdout, best-effort; use `io.stdout` when you need the `Result`, `io.stderr`, or raw bytes.

```text
import io
for line in io.stdin.read() { io.stdout.write_str(transform(line))? }
```

## Blocking — at the coroutine, not the thread

Native `io` reads synchronously but never blocks the runtime: a `read_bytes`/`write` that must wait
**parks its coroutine** and the scheduler runs another — the fairness guarantee of any channel wait
([Coroutines & Channels](coroutine.md)), with no `async`/`await` and no colored functions. The one
exception is the FFI edge: a blocking **`extern` C call** parks its whole OS thread, since Zerg does not
own that frame ([FFI](ffi.md)).

## Process & command execution

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
(fan-in, [Coroutines & Channels](coroutine.md)); the model adds no new waiting primitive. A child is a
foreign resource, so its thread-safety and lifetime follow the FFI rules — one owner coroutine unless
deliberately shared ([FFI](ffi.md)).

## Deferred

- The concrete **`io` catalogue** — open modes, seeking, buffered wrappers, sockets/network — is stdlib.
- A **write-back buffer** (`read`/`recv` filling a caller's `list[byte]`) is the FFI out-buffer open
  question ([FFI](ffi.md)); until then `read_bytes` returns a fresh `list[byte]`.
- **Format specifiers** (`f"{x:>.2f}"`) route to a per-type format protocol (Formatting & text).
