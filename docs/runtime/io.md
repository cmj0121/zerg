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
  **[not yet]** — `Ref[T]` and its `drop` action are both built ([Values & Memory](../core/memory.md)); what
  a handle waits on is the type itself, and `handle` is a name no declaration carries — _E4056 no type named
  `handle`_, [FFI](ffi.md)'s own marker — so the whole-file leaves below are what reading and writing go
  through;
- **failure is a value** — a fallible call returns `Result[T]`, `?`-propagated; EOF is not one.

> **Status.** The compiler ships a **subset** of this surface, and the subset is the **whole-file and
> whole-stream leaves** — tabled in [Standard Library](stdlib.md#io) — plus the `print` keyword. A missing
> or unreadable file raises **`IOError`**, which `guard { io.read_file(p) }` demotes to a `Result`; decode
> with `str(…)` when the bytes are text. The **`Reader` / `Writer` spec surface** — `read_bytes` / `read()`
> / `write` and the `io.stdin` · `io.stdout` · `io.stderr` stream objects described below — is
> **[not yet]**: the intended semantics stand as specified, and reaching one of those names is **`E3084`** —
> _module `io` has no `stdout`_ — in any position, including a method call's receiver.

## Streams — `Reader` & `Writer`

The two specs are built, and so is the one concrete stream this phase has: **`io.Fd`**, a raw file
descriptor, which is what a child process's three ends are. The **module-level stream objects** —
`io.stdin` · `io.stdout` · `io.stderr` — are still **[not yet]**, _E3084 module `io` has no `stdout`_; use
`io.read_file` for input and `io.write` / `io.println` for output.

Each has **one required method**; the rest are provided defaults (Specs & Generics), so a new stream
supplies the primitive and inherits the conveniences.

**`Reader`** — `read_bytes(n: uint) -> Result[list[byte]]`, up to `n` bytes (empty = end of input, never a
blocking wait). Over it are the whole-input defaults **`read_all() -> Result[list[byte]]`** and
**`read_text() -> Result[str]`**, the second being the first decoded.

> **[not yet]** The ITERATOR defaults — **`read() -> Iterator[str]`**, the line reader `for line in f.read()`
> walks, and `bytes()` / `chunks(n)` — are defaults over a protocol `for … in` does not walk yet
> ([Specs & Generics](../core/specs.md)), so they are not declared: a default that cannot be called is a
> promise rather than a method, and calling one is _E3131 the method `read` on a Fd_. For
> possibly-invalid UTF-8, take `read_bytes` and decode under `guard`.

**`Writer`** — `write(bytes: list[byte]) -> Result[uint]` (count written); provided `write_str(s: str)`.
A **`flush()`** default is **[not yet]**: nothing this phase buffers, so it would be a method that does
nothing — _E3131 the method `flush` on a Fd_ names it as one the type does not declare. A write failure — full
disk, broken pipe — is a value, `?`-propagated; it never silently
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

> **[not yet]** The `File` handle and `open` / `create` are unbuilt this phase — `io.open` is _E3084 module
> `io` has no `open`_ — and what is wired is the pair of
> whole-file leaves, which raise **`IOError`** (demote with `guard`) — consistent with "a missing file is an
> expected value-failure" once `guard`ed. Neither leaf hands back a handle, so nothing has to be closed and
> `defer f.close()` has nothing to name.

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
> **`E3084`** — _module `io` has no `stdout`_ — including as a method call's receiver, the position the
> example above writes it in. What is wired this phase is free functions
> ([Standard Library](stdlib.md#io)); each writer returns `Result[nil]` but writes best-effort, never
> yielding an `Err` yet.

**Standard output is unbuffered**, and that is what makes the two spellings one stream: `print` lowers to
libc `printf` and `io.*` to `write(2)`, so a buffer on either would put them in the order their buffers
flushed rather than the order the program wrote them. It does not, so a program alternating `print` and
`io.println` prints them alternating, at a terminal and through a pipe alike, and an abort's line on
standard error lands after the output written before it. Go's stdout is unbuffered for the same reason.

The cost is a write syscall per line, which is the price of the guarantee. A program that wants it
amortized **builds its line and writes it once** — a thing it can do, and a buffer is not.

## Blocking — at the coroutine, not the thread

Native `io` reads synchronously but never blocks the runtime: a `read_bytes`/`write` that must wait
**parks its coroutine** and the scheduler runs another — the fairness guarantee of any channel wait
([Coroutines & Channels](../code/coroutine.md)), with no `async`/`await` and no colored functions. The one
exception is the FFI edge: a blocking **foreign (FFI) C call** parks its whole OS thread, since Zerg does not
own that frame ([FFI](ffi.md)).

> **[not yet]** Coroutine-parking `io` is part of the unbuilt stream surface above, and reaches a reader as
> that surface does, _E3084_. Per-thread parking of a
> blocking foreign call has arrived with the **M:N** scheduler ([Coroutines & Channels](../code/coroutine.md)): such
> a call now occupies **one worker** while the others keep running Zerg coroutines. On a single-worker host
> it is still the whole program that stops.

## Process & command execution

A child process is spawned with a **backtick command literal** and observed through its three streams —
`stdin` is a `Writer`, `stdout` and `stderr` are `Reader`s — and `wait()` answers its status the way a
shell reports one (the exit code, or 128+signal when it was killed). The literal IS the handle, and
`os.command(argv)` is the same thing written by hand ([Standard Library](stdlib.md)).

**There is no shell, in either form.** A command literal builds an **argument vector** and runs it
directly:

- **`` `git status` ``** — a **static** literal, split to argv on whitespace with quotes respected.
- **`` f`git checkout {branch}` ``** — **interpolated**, and each `{x}` is **one argument** whatever is
  inside it. A path with a space in it is one path; a value that reads like a command is data. There is no
  string for a shell to re-split, which is the whole safety property — with no shell there is nothing to
  inject into. A hole is a **whole argument** and cannot be joined to the text beside it (_E2081_): building
  an argument out of text is what a shell does.

So pipes, redirection and globbing are not part of the form. A program that wants them runs the shell
itself — `` f`sh -c {script}` `` — where the shell is visible in the argv rather than implied by the
syntax.

```zerg
import "os"

fn main() {
 p := `echo hello`
 print p.out()
 print p.wait()

 dir := "my docs"
 q := f`echo {dir}`
 print q.out()

 c := `cat`
 c.stdin.write_str("in\n")!
 c.stdin.release()
 print c.stdout.read_text()!
}
```

A command literal is `os.command(…)`, so the file has to `import "os"` — a literal in a file that does not
is _E2083_, which names the literal rather than the module call it lowers to.

To wait on several at once — stdout, stderr, a timeout — bridge each `Reader` into a channel and `select`
(fan-in, [Coroutines & Channels](../code/coroutine.md)); the model adds no new waiting primitive. A child is a
foreign resource, so its thread-safety and lifetime follow the FFI rules — one owner coroutine unless
deliberately shared ([FFI](ffi.md)).

## Deferred

- The concrete **`io` catalogue** — open modes, seeking, buffered wrappers, sockets/network — is stdlib.
- A **write-back buffer** (`read`/`recv` filling a caller's `list[byte]`) is the FFI out-buffer open
  question ([FFI](ffi.md)); until then `read_bytes` returns a fresh `list[byte]`.
- **Format specifiers** (`f"{x:>.2f}"`) route to a per-type format protocol (Formatting & text).
