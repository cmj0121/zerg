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
| [`strings`](#strings) | `import "strings"` | text utilities over the built-in `str`           |
| [`ascii`](#ascii)     | `import "ascii"`   | single-byte ASCII classification for a tokeniser |
| [`strconv`](#strconv) | `import "strconv"` | numeric text conversion in an arbitrary base     |
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
| `ewrite(s: str) -> Result[nil]`           | write `s` to stderr, no trailing newline                  |
| `eprintln(s: str) -> Result[nil]`         | write `s` to stderr with a trailing newline               |
| `write_int(n: int) -> Result[nil]`        | write the decimal text of `n` to stdout                   |
| `read_file(path: str) -> list[byte]`      | read a whole file's bytes (raises `IOError`)              |
| `read_stdin() -> list[byte]`              | read all of standard input (fd 0) to EOF                  |
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

## `strings`

Text utilities over the built-in `str`. Each function decodes the str to its bytes, works at the byte
level, and rebuilds a `str` — no foreign binding. Byte-level search is **UTF-8 correct** (UTF-8
self-synchronises, so a valid needle only matches at a code-point boundary); `index_of` returns a **byte**
offset, like Go's `strings.Index`. Case folding is **ASCII-only** — a non-ASCII byte is passed through
unchanged. An empty `split` separator, or a negative `repeat` count, raises `ValueError`.

| Function                               | Summary                                             |
| -------------------------------------- | --------------------------------------------------- |
| `has_prefix(s: str, prefix: str)`      | whether `s` begins with `prefix` (`-> bool`)        |
| `has_suffix(s: str, suffix: str)`      | whether `s` ends with `suffix` (`-> bool`)          |
| `contains(s: str, sub: str) -> bool`   | whether `sub` occurs anywhere in `s`                |
| `index_of(s: str, sub: str) -> int`    | byte offset of the first `sub`, or `-1` when absent |
| `split(s: str, sep: str) -> list[str]` | pieces between each `sep` (N seps → N+1 pieces)     |
| `join(parts: list[str], sep: str)`     | concatenate `parts` with `sep` between (`-> str`)   |
| `repeat(s: str, count: int) -> str`    | `s` concatenated `count` times                      |
| `trim(s: str) -> str`                  | drop leading/trailing ASCII whitespace              |
| `to_upper(s: str) -> str`              | fold ASCII lowercase letters to uppercase           |
| `to_lower(s: str) -> str`              | fold ASCII uppercase letters to lowercase           |
| `count(s: str, sub: str) -> int`       | number of non-overlapping occurrences of `sub`      |
| `replace(s: str, old: str, new: str)`  | replace every occurrence of `old` with `new`        |
| `trim_prefix(s: str, prefix: str)`     | drop one leading `prefix`, else `s` unchanged       |
| `trim_suffix(s: str, suffix: str)`     | drop one trailing `suffix`, else `s` unchanged      |
| `fields(s: str) -> list[str]`          | split around whitespace runs, no empty pieces       |

`count` and `replace` raise `ValueError` on an empty needle, like `split`.

## `ascii`

Single-byte **ASCII** classification — the honest tool for tokenising ASCII source (a byte ≥ 128 is never a
letter/digit/space here). The predicate set mirrors C's `<ctype.h>`; `to_upper` / `to_lower` are the
single-byte counterparts of the `strings` case folds; `digit_val` / `hex_val` map a digit byte to its value
(or `-1`) for hand-rolled number scanning. Every function is pure value arithmetic — no allocation.

| Function                    | Summary                                                      |
| --------------------------- | ------------------------------------------------------------ |
| `is_digit(b: byte) -> bool` | `b` is `'0'..'9'`                                            |
| `is_alpha(b: byte) -> bool` | `b` is `'A'..'Z'` or `'a'..'z'`                              |
| `is_alnum(b: byte) -> bool` | `b` is a letter or a decimal digit                           |
| `is_hex_digit(b: byte)`     | `b` is `'0'..'9'` / `'a'..'f'` / `'A'..'F'` (`-> bool`)      |
| `is_upper(b: byte) -> bool` | `b` is `'A'..'Z'`                                            |
| `is_lower(b: byte) -> bool` | `b` is `'a'..'z'`                                            |
| `is_space(b: byte) -> bool` | `b` is ASCII whitespace (tab..CR, space) — the C isspace set |
| `to_upper(b: byte) -> byte` | fold an ASCII lowercase letter to uppercase (else unchanged) |
| `to_lower(b: byte) -> byte` | fold an ASCII uppercase letter to lowercase (else unchanged) |
| `digit_val(b: byte) -> int` | value `0..9` of a decimal digit, or `-1`                     |
| `hex_val(b: byte) -> int`   | value `0..15` of a hex digit (either case), or `-1`          |

## `strconv`

Numeric text conversion in an arbitrary **base** 2..36 (digits `'0'..'9'` then `'a'..'z'`, case-insensitive
on input) — the layer the built-ins do not cover, since `int(s)` / `uint(s)` / `float(s)` parse decimal only
and `str(n)` formats decimal only. Use it to read a `0x…` / `0b…` literal by hand or render a hex dump. A bad
base, an out-of-base digit, or a malformed string raises `ValueError`; overflow of the target type is **not**
separately diagnosed this phase (parse bounded text).

| Function                                | Summary                                        |
| --------------------------------------- | ---------------------------------------------- |
| `parse_int(s: str, base: int) -> int`   | signed integer in `base`, optional `+`/`-`     |
| `parse_uint(s: str, base: int) -> uint` | unsigned integer in `base` (fills the top bit) |
| `to_string(n: int, base: int) -> str`   | render `n` in `base`, lowercase, INT_MIN-safe  |
| `parse_bool(s: str) -> bool`            | `"true"` / `"false"`, else `ValueError`        |

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
| `trunc(x: float) -> float`            | drop the fractional part, toward zero             |
| `floor(x: float) -> float`            | greatest integer `<= x`                           |
| `ceil(x: float) -> float`             | least integer `>= x`                              |
| `round(x: float) -> float`            | nearest integer, halves away from zero            |
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

Assertion helpers for `#[test]` functions. **[not yet]** — no compiler builds a test binary
today. A satisfied assertion is `nil`; a
violated one `raise`s so an enclosing `guard` recovers it, or it aborts with the message.

| Function                                      | Summary                   |
| --------------------------------------------- | ------------------------- |
| `assert(cond: bool) -> Result[nil]`           | succeed when `cond` holds |
| `assert_eq[T: Eq](a: T, b: T) -> Result[nil]` | succeed when `a == b`     |
| `assert_ne[T: Eq](a: T, b: T) -> Result[nil]` | succeed when `a != b`     |
