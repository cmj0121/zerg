# Zerg Standard Library

English | [繁體中文](stdlib.zh-TW.md)

The bundled **standard-library packages** — reached with `import "<name>"` (never ambient; the sole
exception is the `print` keyword). Zerg is **zero external dependency, like Go**: every package is **pure
Zerg** built on the self runtime — logic in the language, only the irreducible syscall/hardware leaves in
the C runtime (see [`src/runtime`](../src/runtime/README.md) and the [zero-dependency principle](ffi.md)).
Nothing here binds a third-party library.

For the compiler-provided functions that need **no** import, see [Built-in Functions](builtins.md).

## Packages

| Package               | Import             | Provides                                         |
| --------------------- | ------------------ | ------------------------------------------------ |
| [`io`](#io)           | `import "io"`      | standard-stream output and whole-file read/write |
| [`fs`](#fs)           | `import "fs"`      | filesystem structure — existence, removal        |
| [`os`](#os)           | `import "os"`      | environment, process exit, target platform/arch  |
| [`time`](#time)       | `import "time"`    | wall-clock and monotonic clocks                  |
| [`math`](#math)       | `import "math"`    | numeric helpers and pure-Zerg transcendentals    |
| [`rand`](#rand)       | `import "rand"`    | a deterministic, non-cryptographic generator     |
| [`atomic`](#atomic)   | `import "atomic"`  | the safe shared-mutable primitive                |
| [`testing`](#testing) | `import "testing"` | assertion helpers for `#[test]` functions        |

## `io`

Reading and writing the outside world. A write returns `Result[nil]` (a failure is a value); a whole-file
op raises `IOError` on failure, which `guard { … }` demotes to a `Result`. The full `Reader` / `Writer`
stream surface is specified in [Process & I/O](io.md) but **not yet** built — these are the wired leaves.

| Function                                  | Summary                                                   |
| ----------------------------------------- | --------------------------------------------------------- |
| `write(s: str) -> Result[nil]`            | write `s` to stdout, no trailing newline                  |
| `println(s: str) -> Result[nil]`          | write `s` to stdout with a trailing newline               |
| `write_int(n: int) -> Result[nil]`        | write the decimal text of `n` to stdout                   |
| `read_file(path: str) -> list[byte]`      | read a whole file's bytes (raises `IOError`)              |
| `write_file(path: str, data: list[byte])` | create/truncate and write a whole file (raises `IOError`) |

## `fs`

The filesystem **structure** — a file's _contents_ are `io.read_file` / `io.write_file`.

| Function                    | Summary                                      |
| --------------------------- | -------------------------------------------- |
| `exists(path: str) -> bool` | whether a file or directory exists at `path` |
| `remove(path: str)`         | delete a file (raises `IOError`; files only) |

## `os`

Process and platform facts. `platform` / `arch` resolve at **compile time**, so they name the target the
binary was built for. The program's own arguments arrive as `fn main(args: list[str])`, not from here.

| Function                | Summary                                              |
| ----------------------- | ---------------------------------------------------- |
| `env(key: str) -> str?` | an environment variable's value, or `nil` when unset |
| `exit(code: int)`       | terminate the process with `code` (does not return)  |
| `platform() -> str`     | target OS — `"linux"`, `"darwin"`, `"windows"`, …    |
| `arch() -> str`         | target CPU — `"arm64"`, `"x86_64"`, …                |

## `time`

Clocks. `now` is a date; `monotonic` is meaningful only as a **difference** (elapsed time) and never runs
backwards.

| Function             | Summary                                              |
| -------------------- | ---------------------------------------------------- |
| `now() -> int`       | wall-clock time, whole seconds since the Unix epoch  |
| `monotonic() -> int` | a monotonic reading in nanoseconds (use differences) |

## `math`

Numeric helpers over the primitives, plus **pure-Zerg** transcendentals (numerical algorithms, never a
libm binding). A domain error (e.g. `sqrt` of a negative) raises, demotable with `guard`.

| Function                              | Summary                                           |
| ------------------------------------- | ------------------------------------------------- |
| `abs(x: int) -> int`                  | absolute value of an integer                      |
| `fabs(x: float) -> float`             | absolute value of a float                         |
| `min(a: int, b: int) -> int`          | the smaller of two integers                       |
| `max(a: int, b: int) -> int`          | the larger of two integers                        |
| `sqrt(x: float) -> float`             | square root (Newton's method); negative raises    |
| `pow(base: float, exp: int) -> float` | integer exponent by squaring                      |
| `pi() -> float`                       | π (a function; the grammar has no value constant) |
| `e() -> float`                        | Euler's number                                    |

## `rand`

A fast, deterministic, **non-cryptographic** generator (xorshift64\*). The state is a plain `uint` the
caller holds; each draw advances it in place through a `mut &` reference — no hidden global. **Not** for
keys or tokens.

| Function                             | Summary                                     |
| ------------------------------------ | ------------------------------------------- |
| `seed(n: uint) -> uint`              | build a generator state from a seed         |
| `next(mut &g: uint) -> uint`         | advance `g` in place, return the next value |
| `below(mut &g: uint, n: int) -> int` | advance `g`, return a value in `[0, n)`     |

```text
mut g := rand.seed(42)
x := rand.next(g)        # g advances
d := rand.below(g, 6)    # g advances; d in [0, 6)
```

## `atomic`

The safe way to share mutable state across coroutines (GRAMMAR group 10): an immutable `:=` binding holds
an `Atomic[int]` cell whose contents mutate through sequentially-consistent operations. MVP: `int`-typed.

| Function                                                       | Summary                                   |
| -------------------------------------------------------------- | ----------------------------------------- |
| `atomic(v: int) -> Ref[int]`                                   | a fresh shared cell holding `v`           |
| `load(a: Ref[int]) -> int`                                     | read the cell                             |
| `store(a: Ref[int], v: int) -> int`                            | write `v`, return it                      |
| `swap(a: Ref[int], v: int) -> int`                             | write `v`, return the previous value      |
| `fetch_add(a: Ref[int], n: int) -> int`                        | add `n`, return the previous value        |
| `compare_swap(a: Ref[int], expect: int, desired: int) -> bool` | CAS: set to `desired` iff it was `expect` |

## `testing`

Assertion helpers for `#[test]` functions ([`zerg test`](package.md)). A satisfied assertion is `nil`; a
violated one `raise`s so an enclosing `guard` recovers it, or it aborts with the message.

| Function                                      | Summary                   |
| --------------------------------------------- | ------------------------- |
| `assert(cond: bool) -> Result[nil]`           | succeed when `cond` holds |
| `assert_eq[T: Eq](a: T, b: T) -> Result[nil]` | succeed when `a == b`     |
| `assert_ne[T: Eq](a: T, b: T) -> Result[nil]` | succeed when `a != b`     |
