# Zerg Process & I/O

How a Zerg program reads and writes the outside world — files, the standard streams, and child
processes. It builds on the memory, error, spec, and iteration models in the
[Language Reference](language.md), the `Ref[T]` resource box and coroutine model in
[Coroutines & Channels](coroutine.md), and the C boundary in the [FFI](ffi.md). Also in
[繁體中文](io.zh-TW.md).

I/O lives in the stdlib **`io`** package — imported like any package (`import io`), never ambient. The
one exception is the **`print`** keyword (see [Language Reference](language.md), Formatting & text), the
no-import shortcut for "write a value to stdout"; everything richer is `io`.

Three ideas carry all of it, each reusing a model you already have:

- **a stream is a `Reader` or a `Writer`** — a byte source or sink, drained with `for` (Iteration);
- **a handle is a `Ref[T]`** — a file or socket is a scope-owned, closed-exactly-once resource;
- **failure is a value** — every fallible operation returns `Result[T]`, `?`-propagated; EOF is not one.

## Streams — `Reader` & `Writer`

A byte source implements **`Reader`**, a sink **`Writer`**. Each has **one required method**; the rest
are provided defaults (Specs & Generics), so a new stream type supplies the primitive and inherits the
conveniences.

**`Reader`** — required `read_bytes(n: uint) -> Result[list[byte]]`: up to `n` bytes, an **empty list at
end of input** (never a blocking "wait"). Everything else is a provided default over it:

- **`read() -> Iterator[str]`** — the **default line reader**: decode UTF-8 and yield one `str` per
  line. `for line in f.read() { … }` drains a source cleanly, ending at EOF (`StopIteration`) and
  **re-raising** a decode or device error mid-stream — the ordinary iteration protocol (Iteration). For
  bytes that may not be valid UTF-8, take them with `read_bytes` and decode under `guard`.
- **`bytes() -> Iterator[byte]`** and **`chunks(n) -> Iterator[list[byte]]`** — the byte and
  fixed-block views, for binary work.

**`Writer`** — required `write(bytes: list[byte]) -> Result[uint]` (the count written); provided
`write_str(s: str)` and `flush() -> Result[nil]`. A `Writer` failure is a value — a full disk, a broken
pipe — so it `?`-propagates like any `Result`; it never silently drops (that convenience is `print`'s
alone).

```text
fn copy_lines(src: Reader, mut dst: Writer) -> Result[nil] {
    for line in src.read() {           # EOF ends the loop; a read error re-raises
        dst.write_str(line)?           # a write error early-returns
        dst.write_str("\n")?
    }
    return dst.flush()
}
```

## Files & handles

A **`File`** is a **`Ref[handle]`** wrapped in a newtype you cannot see into — the opaque foreign-handle
pattern from the [FFI](ffi.md), so a file is **closed exactly once**, at the last holder's scope exit (or
an explicit `del`). It escapes its scope through that `Ref[T]`; a file confined to one scope wants
`defer f.close()` instead — the escape-or-not line from the [Language Reference](language.md).

```text
open(path: str)   -> Result[File]      # read; File implements Reader
create(path: str) -> Result[File]      # write/truncate; File implements Writer
```

`open` returns a `Result` — a missing file is an **expected** failure, a value, never an abort. Open
modes (append, read-write), seeking, and metadata are `io` methods on `File`; their catalogue is stdlib
detail, not a new concept. A **socket** is the same shape — a `Ref[handle]` that is a `Reader` and a
`Writer` — with the concrete network API left to `io`.

## Standard streams

`import io` binds the three ambient streams: **`io.stdin`** (a `Reader`), **`io.stdout`** and
**`io.stderr`** (`Writer`s). They are read-only OS facts reached through the stdlib, like `env` and the
clock (see [Modules, Packages & Programs](package.md)) — never `main` parameters.

```text
import io

for line in io.stdin.read() {          # a line-oriented filter
    io.stdout.write_str(transform(line))?
    io.stdout.write_str("\n")?
}
```

The **`print`** keyword is the no-import shortcut for the common case — `print x` writes `x.display()`
and a newline to stdout, best-effort (Formatting & text). Reach for `io.stdout` when you need the
`Result`, `io.stderr`, or raw bytes.

## Blocking — at the coroutine, not the thread

Native `io` is **synchronous to write but non-blocking to run**: a `read_bytes` or `write` that must
wait **parks its coroutine** and the M:N scheduler runs another — the same fairness guarantee as any
channel wait (see [Coroutines & Channels](coroutine.md)). There is **no `async` / `await` and no colored
functions**; ordinary top-down code never freezes unrelated coroutines.

The one exception is the FFI edge already noted: a **blocking `extern` C call** parks its whole OS
thread, because Zerg does not own that frame (see [FFI](ffi.md)). Native `io` is scheduler-integrated; a
raw C blocking call is thread-occupying.

## Process & command execution

A child process is spawned with a **backtick command literal**, and observed through the **same stream
model** — its pipes are `Reader`s and a `Writer`, its handle a `Ref[T]`.

**Two forms, and the `f` marks the danger:**

- **`` `git status` ``** — a **static** literal: split into an argv list on whitespace (quotes
  respected) and executed **directly, with no shell**. No interpolation, so no injection, no glob or
  pipe — the safe default, matching Go / Rust / Elixir.
- **`` f`git checkout {branch}` ``** — an **interpolated** command, run **through a shell** (so pipes
  and redirection work). Each `{x}` is **shell-quoted to a single argument** by default, which defeats
  command-injection though not a hostile `-flag` (an argument is still an argument). A **raw** splice —
  building a pipeline dynamically — is the explicit `{x:raw}`, the opt-in footgun. The `f` reads exactly
  as on an `f`-string: "this interpolates" (Formatting & text).

Both yield a **process handle** — a `Ref[proc]` newtype whose `drop` waits for (or kills) the child, so
it is reaped exactly once:

```text
p := `ls -l`
for line in p.stdout.read() { print line }    # stdout is a Reader
code := p.wait()!                             # block this coroutine for the exit status
```

- **`p.stdin`** is a `Writer`; **`p.stdout`** and **`p.stderr`** are `Reader`s — feed and drain them
  like any stream, and a `read` blocks only this coroutine.
- **`p.wait() -> Result[int]`** blocks the coroutine until the child exits and yields its status.
- To wait on **several** at once — stdout, stderr, a timeout — bridge each `Reader` into a channel with
  a draining coroutine and `select` (the fan-in pattern, [Coroutines & Channels](coroutine.md)); the
  process model adds no new waiting primitive.

Because a child process is a foreign resource, its thread-safety and lifetime follow the FFI rules — the
handle is owned by one coroutine unless the work is deliberately shared (see [FFI](ffi.md)).

## Deferred

- **The concrete `io` catalogue** — open modes, seeking, buffered wrappers, and the socket/network API
  are stdlib surface, not language concepts.
- **A `read` / `recv` write-back buffer** — filling a caller's `list[byte]` in place — is the FFI
  out-buffer open question ([FFI](ffi.md)); until it lands, `read_bytes` returns a fresh `list[byte]`.
- **Format specifiers** (`f"{x:>.2f}"`) route to a per-type format protocol — Formatting & text.
