<!-- markdownlint-disable MD013 MD024 -->

# STDLIB.md — Zerg standard library reference

## Overview

The Zerg standard library ships with the toolchain. User programs reach
modules with `import "<m>"` (the `std/` prefix is the implicit default,
so `import "io"` and `import "io"` reach the same module). Five
modules ship at v0.10: `io`, `strings`, `math`, `os`, `time`. Their
public surface is locked at v0.10 and stable through v1.0; the
"provisional" marker the README carried since v0.8 is dropped at this
release. Signatures, error variants, edge-case semantics, and parity
guarantees documented in this file are part of the Zerg source-stability
promise.

Examples in this file use the bare-name form (`import "io"`) — the
recommended way to reach the stdlib. Explicit `import "io"` remains
supported for code that wants to be unambiguous when a sibling with the
same name could shadow the stdlib. Platform-specific `sys/*` modules
(e.g. `sys/path`) always require the `sys/` prefix.

Each module's `# requires:` floor is recorded in the per-module section.
A program importing a module must declare a `# requires:` at least that
high; otherwise the import rejects with a compile error.

Examples in this file are extracted by
`src/bootstrap/test/docs/stdlib_examples_test.go` and run through the
loader + typechecker. Every code block tagged `<!-- example: program -->`
is a runnable `.zg` source — copy-paste-able, no wrapping needed.

---

## std/io

`# requires: v0.8`

File system access. Whole-file reads and writes against the host
filesystem. There is no path sandbox at v0.10 — `read_file("../foo")`
resolves against the caller's working directory and reaches whatever the
host process can see.

### Signatures

| Function                                                       | Description                                         |
| -------------------------------------------------------------- | --------------------------------------------------- |
| `read_file(path: str) -> Result[str, IoError]`                 | Reads the entire file into a string. UTF-8 only.    |
| `write_file(path: str, content: str) -> Result[bool, IoError]` | Writes `content` to `path`, creating or truncating. |

### `IoError`

| Variant            | Bucket                                                      |
| ------------------ | ----------------------------------------------------------- |
| `NotFound`         | Host reports `ENOENT` / `fs.ErrNotExist`.                   |
| `PermissionDenied` | Host reports `EACCES` / `fs.ErrPermission`.                 |
| `AlreadyExists`    | Host reports `EEXIST` / `fs.ErrExist`.                      |
| `InvalidPath`      | Host reports `EINVAL` / `fs.ErrInvalid`.                    |
| `Other`            | Catch-all — every host error not bucketed above lands here. |

Both halves (interpreter, C codegen) bucket against the same set —
parity is by variant identity, never by host error text.

### `read_file`

- **Behaviour:** reads the entire file into memory and returns it as a
  string. UTF-8 is assumed; non-UTF-8 bytes are returned verbatim and may
  break downstream string operations.
- **Memory:** whole-file slurp. Suitable for config / source files; not
  for large blobs. No streaming or size cap at v0.10.
- **Errors:** see `IoError` above.

<!-- example: program -->

```zerg
# requires: v0.8
import "io"

match io.read_file("config.toml") {
    Result.Ok(content) => print content
    Result.Err(IoError.NotFound) => print "missing"
    Result.Err(_) => print "io error"
}
```

### `write_file`

- **Behaviour:** writes `content` to `path`. Creates the file with
  permissions `0o644` if it does not exist; truncates if it does.
- **Returns:** `Result.Ok(true)` on success. The boolean payload exists
  to keep the type a discriminated `Result`; the literal `true` carries
  no extra meaning.
- **Errors:** see `IoError`.

<!-- example: program -->

```zerg
# requires: v0.8
import "io"

match io.write_file("/tmp/zerg-out.txt", "hello\n") {
    Result.Ok(_) => print "wrote"
    Result.Err(_) => print "io error"
}
```

### Notes

- **No sandbox.** `read_file("../../etc/passwd")` works. Trust boundary
  is the host process. v1.0+ may layer capability scoping; not at v0.10.
- **UTF-8 only.** Binary files round-trip byte-for-byte but any string
  operation (slicing, `len`) treats the buffer as UTF-8.

---

## std/strings

`# requires: v0.8`

ASCII-aware string utilities. Every function operates on Zerg `str`
values; behaviour is defined byte-by-byte against the underlying UTF-8
sequence, with case helpers explicitly ASCII-only.

### Signatures

| Function                                       | Description                                  |
| ---------------------------------------------- | -------------------------------------------- |
| `split(s: str, sep: str) -> list[str]`         | Splits `s` on every non-overlapping `sep`.   |
| `join(parts: list[str], sep: str) -> str`      | Concatenates `parts` interleaved with `sep`. |
| `trim(s: str) -> str`                          | Strips leading and trailing whitespace.      |
| `starts_with(s: str, prefix: str) -> bool`     | True iff `s` begins with `prefix`.           |
| `ends_with(s: str, suffix: str) -> bool`       | True iff `s` ends with `suffix`.             |
| `contains(s: str, needle: str) -> bool`        | True iff `needle` appears anywhere in `s`.   |
| `replace(s: str, old: str, new: str) -> str`   | Replaces every non-overlapping `old`.        |
| `to_upper(s: str) -> str`                      | ASCII-uppercase. `[a-z]` → `[A-Z]`.          |
| `to_lower(s: str) -> str`                      | ASCII-lowercase. `[A-Z]` → `[a-z]`.          |
| `parse_int(s: str) -> Result[int, ParseError]` | Parses a signed 64-bit decimal integer.      |

### `ParseError`

| Variant        | When                                                      |
| -------------- | --------------------------------------------------------- |
| `Empty`        | Input is empty after trimming whitespace.                 |
| `InvalidDigit` | Input contains a non-digit (after optional leading sign). |
| `Overflow`     | Parsed value does not fit in signed 64-bit.               |

### Pinned semantics (v0.10)

- `split(s, "")` **runtime-panics** on both halves with
  `split: empty separator`. There is no implicit "split into runes"
  fallback.
- `split("", sep)` returns `[""]` — a one-element list whose sole element
  is the empty string. (Matches Go and Python; differs from naive "no
  separator → no parts" intuition.)
- `replace` is **left-to-right, non-overlapping** (Go / Python
  `str.replace` semantics). After each match the cursor advances past
  the inserted `new`, so overlapping `old` patterns produce a single
  replacement, not cascades.
- `to_upper` and `to_lower` are **ASCII-only**. Non-ASCII bytes pass
  through unchanged. Locale-sensitive case mapping is not provided at
  v0.10.
- `parse_int` accepts an optional leading `+` or `-`. Whitespace is
  stripped before parsing. The bucket order matters: empty (after
  trimming) → `Empty`, otherwise overflow before invalid-digit.

### `split`

<!-- example: program -->

```zerg
# requires: v0.8
import "strings"

print strings.split("a,b,c", ",")
print strings.split("only", ",")
print strings.split("a::b::c", "::")
print strings.split("", ",")
```

Output (last line is `[""]`):

```text
[ a, b, c ]
[ only ]
[ a, b, c ]
[  ]
```

### `join`

<!-- example: program -->

```zerg
# requires: v0.8
import "strings"

xs := strings.split("a,b,c", ",")
print strings.join(xs, "-")
print strings.join(xs, "")
print strings.join([], "-")
```

### `trim`

Strips leading and trailing ASCII whitespace
(spaces, tabs, `\n`, `\r`).

<!-- example: program -->

```zerg
# requires: v0.8
import "strings"

print strings.trim("  hello  ")
print strings.trim("\tzerg\n")
print strings.trim("noop")
```

### `starts_with` / `ends_with` / `contains`

<!-- example: program -->

```zerg
# requires: v0.8
import "strings"

print strings.starts_with("hello", "he")
print strings.ends_with("hello", "lo")
print strings.contains("hello", "ell")
```

Edge case: every string starts with, ends with, and contains the empty
string — `starts_with("x", "") == true`.

### `replace`

Left-to-right, non-overlapping. `replace(s, "", new)` is undefined; the
v0.10 implementation passes through to the host (Go `strings.ReplaceAll`)
and produces an unspecified result. Treat empty `old` as a programmer
error.

<!-- example: program -->

```zerg
# requires: v0.8
import "strings"

print strings.replace("foo bar foo", "foo", "baz")
print strings.replace("aaaa", "aa", "b")
print strings.replace("ababab", "ab", "X")
```

### `to_upper` / `to_lower`

ASCII-only — bytes outside `[a-z]` / `[A-Z]` pass through. The chosen
implementation is libc-free in the C codegen so output is byte-identical
across platforms.

<!-- example: program -->

```zerg
# requires: v0.8
import "strings"

print strings.to_upper("abc XYZ 123")
print strings.to_lower("AbCdE")
```

### `parse_int`

Accepts a 10-digit signed decimal in the signed-64-bit range. Leading
whitespace is trimmed; an optional leading `+` or `-` is consumed; the
remaining bytes must all be `[0-9]`.

<!-- example: program -->

```zerg
# requires: v0.8
import "strings"

print strings.parse_int("42")
print strings.parse_int("  -7  ")
print strings.parse_int("+0")
print strings.parse_int("")
print strings.parse_int("abc")
print strings.parse_int("99999999999999999999")
```

Output:

```text
Result.Ok(42)
Result.Ok(-7)
Result.Ok(0)
Result.Err(ParseError.Empty)
Result.Err(ParseError.InvalidDigit)
Result.Err(ParseError.Overflow)
```

---

## std/math

`# requires: v0.8`

Integer math. No floats (Zerg has no float type at v0.10).

### Signatures

| Function                     | Description                                      |
| ---------------------------- | ------------------------------------------------ |
| `abs(x: int) -> int`         | Absolute value of `x`.                           |
| `min(a: int, b: int) -> int` | Smaller of `a` and `b` (≤ comparison).           |
| `max(a: int, b: int) -> int` | Larger of `a` and `b` (≥ comparison).            |
| `gcd(a: int, b: int) -> int` | Greatest common divisor; sign of inputs ignored. |

### Pinned semantics (v0.10)

- `gcd(0, 0) == 0`. (Conventional choice; matches the Euclidean-algorithm
  fixpoint at zero.)
- `gcd(a, b)` operates on `|a|, |b|` — sign is discarded before the
  reduction, so `gcd(-12, 18) == 6`.
- `abs(INT64_MIN)` is **host-defined**. The two's-complement minimum has
  no positive counterpart in signed 64-bit; the Zerg implementation
  performs the integer negation directly and inherits the host's wrap /
  saturate behaviour. Avoid passing `INT64_MIN`.

### Examples

<!-- example: program -->

```zerg
# requires: v0.8
import "math"

print math.abs(-5)
print math.abs(7)
print math.min(3, 7)
print math.min(-1, -2)
print math.max(3, 7)
print math.gcd(12, 18)
print math.gcd(0, 0)
print math.gcd(-12, 18)
```

---

## std/os

`# requires: v0.9`

Process surface. Environment access (`env`), command-line argv
(`argv`), and process termination (`exit`).

> `env` ships from v0.8; `argv` and `exit` ship from v0.9. The
> per-fn `requires:` lines below pin the floor each fn entered the
> stable surface.

### Signatures

| Function                 | Since | Description                                   |
| ------------------------ | ----- | --------------------------------------------- |
| `env(name: str) -> str?` | v0.8  | Looks up an environment variable.             |
| `argv() -> list[str]`    | v0.9  | Returns the program's command-line arguments. |
| `exit(code: int)`        | v0.9  | Terminates the process with `code`.           |

### `env`

- **Behaviour:** consults the host process's environment. Returns the
  raw `str` value when the variable is present (even if empty); `nil`
  otherwise.

<!-- example: program -->

```zerg
# requires: v0.8
import "os"

match os.env("HOME") {
    nil => print "no HOME"
    h   => print h
}
```

### `argv`

- **Behaviour:** returns the program's argument vector as `list[str]`.
- **Index 0:** the executable name (`prog.zg` for source-driven runs,
  the compiled binary path for `zerg build` outputs). Under the REPL,
  index 0 is the literal string `"<repl>"`.
- **Indices 1..n:** user-supplied arguments in left-to-right order.
- **Empty case:** if the toolchain is invoked without an argv (rare —
  e.g. embedded harness), `argv()` returns `[]`.

<!-- example: program -->

```zerg
# requires: v0.9
import "os"

a := os.argv()
print len(a)
for i in 1..len(a) {
    print a[i]
}
```

### `exit`

- **Behaviour:** terminates the process with exit code `code`. Does not
  return at runtime — the kernel reaps the process before control flow
  resumes. The declared return type is unit (no annotation).
- **Defer:** `exit` does **not** drain pending `defer`s on the stack.
  Code in `defer` blocks is only run when control flow naturally leaves
  the enclosing block; `exit` skips this. Matches Go's `os.Exit`.
- **Concurrency:** `exit` does **not** join spawned tasks. The first
  call to `exit` from any goroutine wins; others are abandoned. The
  interpreter and C codegen agree on this — under interp, an `exit`
  raised inside `spawn` surfaces at the bundle boundary; under cgen,
  libc's `exit(3)` brings the whole process down.
- **Code range:** the host typically truncates to 8 bits. `exit(256)`
  is host-defined; portable code should pass codes in `0..=255`.

<!-- example: program -->

```zerg
# requires: v0.9
import "os"

print "before"
os.exit(0)
print "unreachable"
```

<!-- example: program -->

```zerg
# requires: v0.9
import "os"

fn body() {
    defer print "deferred"
    os.exit(0)
}

body()
```

The second example prints nothing — `defer print "deferred"` is skipped
because `os.exit` does not drain defers.

---

## std/time

`# requires: v0.9`

Monotonic-clock helpers. POSIX-only at v0.10 (uses `nanosleep` in the
C codegen and Go's `time.Sleep` in the interpreter).

### Signatures

| Function                    | Description                                        |
| --------------------------- | -------------------------------------------------- |
| `now_ms() -> int`           | Milliseconds since this program's `now_ms` epoch.  |
| `sleep_ms(ms: int) -> bool` | Blocks at least `ms` milliseconds; returns `true`. |

### Pinned semantics (v0.10)

- `now_ms` uses a **lazy-zero-on-first-call epoch**. The first call in
  the running program captures the current monotonic time and returns
  `0`. Every subsequent call returns the elapsed milliseconds since
  that capture. Both the interpreter and the C codegen agree on every
  return value the program observes.
- The epoch is process-global. Under `spawn`, every goroutine sees the
  same epoch — the first `now_ms` call across all goroutines (in
  observable program order) sets it.
- `sleep_ms(ms)` with `ms <= 0` returns immediately and reports `true`
  (no error). Negative durations clamp to zero, matching `time.Sleep`.
- `sleep_ms` always returns `true` at v0.10. The `bool` result reserves
  room for future `false` (interrupted, deadline) returns; today it is
  a constant.

### `now_ms`

<!-- example: program -->

```zerg
# requires: v0.9
import "time"

print time.now_ms()
_ := time.sleep_ms(50)
t := time.now_ms()
if t >= 30 {
    print "elapsed"
} else {
    print "fast"
}
```

The first line prints `0` deterministically. The conditional accounts
for sleep-resolution slop on busy hosts.

### `sleep_ms`

<!-- example: program -->

```zerg
# requires: v0.9
import "time"

if time.sleep_ms(-5) {
    print "ok"
} else {
    print "fail"
}
```

Prints `ok`. Negative durations are not errors.

---

## Stability

The signatures, error variants, and pinned semantics in this document
are the v0.10 stdlib surface. They are stable through v1.0. Future
additions ship as **new** modules or **new** functions; existing fns do
not change shape.

Out-of-scope at v0.10 (deferred to v1.0+): `std/io` streaming,
`std/io` size caps, locale-aware case mapping, regex, JSON, network,
floats, stdin streaming.

<!-- markdownlint-enable MD013 MD024 -->
