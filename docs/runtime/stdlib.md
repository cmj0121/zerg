# Zerg Standard Library

English | [繁體中文](stdlib.zh-TW.md)

The bundled **standard-library packages** — reached with `import "<name>"` (never ambient; the sole
exception is the `print` keyword). Zerg is **zero external dependency, like Go**: every package is **pure
Zerg** built on the self runtime — logic in the language, only the irreducible syscall/hardware leaves in
the C runtime (see [`src/runtime`](../../src/runtime/README.md) and the [zero-dependency principle](ffi.md)).
Nothing here binds a third-party library.

For the compiler-provided functions that need **no** import, see [Built-in Functions](builtins.md).

## Runnable examples in a module's comments

A `pub` function's comment may carry an example, as a pair of fenced blocks in a plain `#` comment — the
expressions in ` ```zerg `, and what they print in ` ```output `. `make stdlib-test` **compiles and runs**
every pair and diffs the real output against the stated one, so an example is a claim that is checked
rather than one that is written down. (`##` doc comments and `zerg doc` are **[not yet]**; the fences are
the form they will take.)

````text
# ```zerg
# strings.index_of("日本語", "本")
# ```
# ```output
# 3
# ```
````

> **An ` ```output ` line may not end in whitespace.** The repository's own pre-commit hook trims trailing
> whitespace, and it does so in the very commit that adds the example — so an output block whose last
> character is legitimately a space is silently rewritten into one the example does not produce, and the
> example goes from true to false with nothing to show for it. `trim_left` is the case that found this.
>
> The workaround is to end the **expression** with a terminator the eye can see, and to assert the same
> form in the suite so the two agree:
>
> ````text
> # ```zerg
> # strings.trim_left("  hi  ") + "|"
> # ```
> # ```output
> # hi  |
> # ```
> ````
>
> Do not try to exempt the file from the hook: every other line in it should be trimmed, and an example
> that needs a trailing space is an example whose reader cannot see it either.

## Packages

| Package               | Import             | Provides                                          |
| --------------------- | ------------------ | ------------------------------------------------- |
| [`io`](#io)           | `import "io"`      | standard-stream output and whole-file read/write  |
| [`fs`](#fs)           | `import "fs"`      | filesystem structure — existence, removal         |
| [`os`](#os)           | `import "os"`      | environment, process exit, target platform/arch   |
| [`strings`](#strings) | `import "strings"` | text utilities over the built-in `str`            |
| [`ascii`](#ascii)     | `import "ascii"`   | single-byte ASCII classification for a tokeniser  |
| [`strconv`](#strconv) | `import "strconv"` | numeric text conversion in an arbitrary base      |
| [`time`](#time)       | `import "time"`    | clocks, and timers as channels                    |
| [`math`](#math)       | `import "math"`    | numeric helpers and pure-Zerg transcendentals     |
| [`rand`](#rand)       | `import "rand"`    | a deterministic, non-cryptographic generator      |
| [`sha256`](#sha256)   | `import "sha256"`  | the FIPS 180-4 digest, for naming and integrity   |
| [`cli`](#cli)         | `import "cli"`     | a declared command line, and the help it renders  |
| [`atomic`](#atomic)   | `import "atomic"`  | the safe shared-mutable primitive                 |
| [`testing`](#testing) | `import "testing"` | what a `#[test]` needs that the language does not |

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
`run` is the one leaf that starts ANOTHER process — argv straight to the OS, no shell, no pipes, and the
exit status back (128+signal when it died on one, 127 when it could not be executed). The command literals
of [Process & I/O](io.md), which do have a shell and pipes, are **[not yet]**.

| Function                | Summary                                              |
| ----------------------- | ---------------------------------------------------- |
| `env(key: str) -> str?` | an environment variable's value, or `nil` when unset |
| `exit(code: int)`       | terminate the process with `code` (does not return)  |
| `run(argv: list[str])`  | run `argv[0]` (PATH-searched), wait, `-> int` status |
| `platform() -> str`     | target OS — `"linux"`, `"darwin"`, `"windows"`, …    |
| `arch() -> str`         | target CPU — `"arm64"`, `"x86_64"`, …                |

## `strings`

Text utilities over the built-in `str`. Each function decodes the str to its bytes, works at the byte
level, and rebuilds a `str` — no foreign binding. Byte-level search is **UTF-8 correct** (UTF-8
self-synchronises, so a valid needle only matches at a code-point boundary); `index_of` returns a **byte**
offset, like Go's `strings.Index`. Case folding is **ASCII-only** — a non-ASCII byte is passed through
unchanged. An empty `split` separator, or a negative `repeat` count, raises `ValueError`.

| Function                               | Summary                                               |
| -------------------------------------- | ----------------------------------------------------- |
| `has_prefix(s: str, prefix: str)`      | whether `s` begins with `prefix` (`-> bool`)          |
| `has_suffix(s: str, suffix: str)`      | whether `s` ends with `suffix` (`-> bool`)            |
| `contains(s: str, sub: str) -> bool`   | whether `sub` occurs anywhere in `s`                  |
| `index_of(s: str, sub: str) -> int`    | byte offset of the first `sub`, or `-1` when absent   |
| `split(s: str, sep: str) -> list[str]` | pieces between each `sep` (N seps → N+1 pieces)       |
| `join(parts: list[str], sep: str)`     | concatenate `parts` with `sep` between (`-> str`)     |
| `repeat(s: str, count: int) -> str`    | `s` concatenated `count` times                        |
| `trim(s: str) -> str`                  | drop leading/trailing ASCII whitespace                |
| `to_upper(s: str) -> str`              | fold ASCII lowercase letters to uppercase             |
| `to_lower(s: str) -> str`              | fold ASCII uppercase letters to lowercase             |
| `count(s: str, sub: str) -> int`       | number of non-overlapping occurrences of `sub`        |
| `replace(s: str, old: str, new: str)`  | replace every occurrence of `old` with `new`          |
| `trim_prefix(s: str, prefix: str)`     | drop one leading `prefix`, else `s` unchanged         |
| `trim_suffix(s: str, suffix: str)`     | drop one trailing `suffix`, else `s` unchanged        |
| `fields(s: str) -> list[str]`          | split around whitespace runs, no empty pieces         |
| `last_index_of(s: str, sub: str)`      | byte offset of the LAST `sub`, or `-1` (`-> int`)     |
| `trim_left(s: str) -> str`             | drop leading ASCII whitespace only                    |
| `trim_right(s: str) -> str`            | drop trailing ASCII whitespace only                   |
| `equal_fold(a: str, b: str) -> bool`   | equality ignoring ASCII case, folding no new string   |
| `pad_start(s, width: int, fill: str)`  | pad on the left to at least `width` bytes (`-> str`)  |
| `pad_end(s, width: int, fill: str)`    | pad on the right to at least `width` bytes (`-> str`) |

`count` and `replace` raise `ValueError` on an empty needle, like `split`. `pad_start` / `pad_end` raise
`ValueError` on a `fill` that is not exactly one byte — a multi-byte fill cannot land on a byte width
without cutting a code point in half — and return `s` unchanged when it is already that wide. The fill is
validated **before** the width is consulted, so a bad one is refused even on a call that would have padded
nothing.

An **empty needle is found**, not missing, at the end each function searches from: `index_of` answers `0`,
`contains` answers `true`, and `last_index_of` answers the string's byte length, since the last empty
needle is the one past the final byte. `split`, `count` and `replace` are the three that refuse it, and
they refuse it because a zero-width match would never advance them.

## `ascii`

Single-byte **ASCII** classification — the honest tool for tokenising ASCII source (a byte ≥ 128 is never a
letter/digit/space here). The predicate set mirrors C's `<ctype.h>`; `fold_upper` / `fold_lower` are the
single-byte counterparts of the `strings` case folds; `digit_val` / `hex_val` map a digit byte to its value
(or `-1`) for hand-rolled number scanning. Every function is pure value arithmetic — no allocation.

| Function                      | Summary                                                      |
| ----------------------------- | ------------------------------------------------------------ |
| `is_digit(b: byte) -> bool`   | `b` is `'0'..'9'`                                            |
| `is_alpha(b: byte) -> bool`   | `b` is `'A'..'Z'` or `'a'..'z'`                              |
| `is_alnum(b: byte) -> bool`   | `b` is a letter or a decimal digit                           |
| `is_hex_digit(b: byte)`       | `b` is `'0'..'9'` / `'a'..'f'` / `'A'..'F'` (`-> bool`)      |
| `is_upper(b: byte) -> bool`   | `b` is `'A'..'Z'`                                            |
| `is_lower(b: byte) -> bool`   | `b` is `'a'..'z'`                                            |
| `is_space(b: byte) -> bool`   | `b` is ASCII whitespace (tab..CR, space) — the C isspace set |
| `fold_upper(b: byte) -> byte` | fold an ASCII lowercase letter to uppercase (else unchanged) |
| `fold_lower(b: byte) -> byte` | fold an ASCII uppercase letter to lowercase (else unchanged) |
| `digit_val(b: byte) -> int`   | value `0..9` of a decimal digit, or `-1`                     |
| `hex_val(b: byte) -> int`     | value `0..15` of a hex digit (either case), or `-1`          |

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

## `sha256`

SHA-256 as specified by **FIPS 180-4**, in pure Zerg over `uint` and the bitwise operators — no libcrypto,
no runtime leaf. `zerg build` names every cached object with it.

| Function                       | Summary                                    |
| ------------------------------ | ------------------------------------------ |
| `sum(data: list[byte])`        | the 32-byte digest                         |
| `hex(data: list[byte]) -> str` | the same digest as 64 lowercase hex digits |

It is **not constant-time** and makes no claim to be. Use it to name a thing by its content, to notice that
a file changed, or to key a cache; do not use it to check a password. `make sha256` holds it to the
standard's known-answer vectors and to the system tool over random inputs — the oracle cannot check it,
since both compilers would be running this same source.

## `time`

Clocks and timers. `now` is a date; `monotonic` is meaningful only as a **difference** (elapsed time) and
never runs backwards. **A timer is a channel** — `after` and `ticker` answer receive-only channels, so a
`select` arm on one is a timeout or a tick with no new syntax (see [Coroutines](../code/coroutine.md)).
Durations are **nanoseconds**, the unit `monotonic` reads; a duration `<= 0` fires at once.

| Function                   | Summary                                                       |
| -------------------------- | ------------------------------------------------------------- |
| `now() -> int`             | wall-clock time, whole seconds since the Unix epoch           |
| `monotonic() -> int`       | a monotonic reading in nanoseconds (use differences)          |
| `after(d) -> <-chan[int]`  | one value once `d` nanoseconds have passed                    |
| `ticker(d) -> <-chan[int]` | a value every `d` nanoseconds; the channel holds **one** tick |

The value delivered is the **monotonic reading at the moment the timer fired**, not a placeholder: a tick
may arrive arbitrarily later than it fired, and the reading is how a receiver that cares tells how late it
is. A `ticker` that a receiver falls behind on **parks on the send** rather than queueing ticks, so a slow
consumer slows the ticker instead of building a backlog.

**Cost, and the one thing missing.** Each live timer is a **coroutine with its own 256KB stack**, so an
`after` inside a loop allocates one per iteration. There is **no stop**: a sleep cannot be cancelled, so a
`ticker`'s coroutine lives until the program does — put one at the top of a program, not in a loop.

## `math`

Numeric helpers over the primitives, plus **pure-Zerg** transcendentals (numerical algorithms, never a
libm binding). A domain error (e.g. `sqrt` of a negative) raises `ValueError`, demotable with `guard`.

| Function                              | Summary                                           |
| ------------------------------------- | ------------------------------------------------- |
| `abs(x: int) -> int`                  | absolute value of an integer                      |
| `fabs(x: float) -> float`             | absolute value of a float                         |
| `min(a: int, b: int) -> int`          | the smaller of two integers                       |
| `max(a: int, b: int) -> int`          | the larger of two integers                        |
| `sqrt(x: float) -> float`             | square root (Newton's method); negative raises    |
| `pow(base: float, exp: int) -> float` | integer exponent by squaring                      |
| `trunc(x: float) -> int`              | drop the fractional part, toward zero             |
| `floor(x: float) -> int`              | greatest integer `<= x`                           |
| `ceil(x: float) -> int`               | least integer `>= x`                              |
| `round(x: float) -> int`              | nearest integer, halves away from zero            |
| `pi() -> float`                       | π (a function; the grammar has no value constant) |
| `e() -> float`                        | Euler's number                                    |

**The rounding four answer an `int`, and that is what they are for.** `int(x)` on a `float` is refused —
dropping a fraction is a decision, and four answers are defensible ([Types](../core/types.md)) — so these
are the verbs that make it. A verb that gave back a `float` would leave the caller holding the very
conversion it called a verb to perform. A magnitude no `int` holds raises `OverflowError`, demotable with
`guard` like any other conversion that can fail; a narrower target is the verb and then the conversion,
`byte(math.trunc(x))`.

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

## `cli`

A command line is **declared**, not parsed by hand: a `Command` names its arguments, its sub-commands and the
function each one runs, and that declaration is the only description of the command line that exists — the
parser reads it, the help renderer reads it, the validator reads it. `zerg`'s own command line is declared with
it (see [`src/compiler/zergc.zg`](../../src/compiler/zergc.zg)).

| Function                                                | Summary                                          |
| ------------------------------------------------------- | ------------------------------------------------ |
| `command(name: str, about: str = "") -> Command`        | a command, the root or a sub-command             |
| `argument(short, long, help, fallback) -> Argument`     | an option taking a value                         |
| `flag(short, long, help) -> Argument`                   | an option that is present or absent              |
| `positional(name, help) -> Argument`                    | a positional argument                            |
| `Command.opt` / `.required` / `.flag` / `.pos`          | declare an argument inline, without building one |
| `Command.add(a)` / `.sub(c)` / `.run(f)`                | attach an argument, a sub-command, its function  |
| `Command.version` / `.usage` / `.epilog` / `.no_help`   | what `--help` and `--version` say                |
| `Command.exec(args: list[str]) -> int`                  | parse, dispatch, and answer the process's status |
| `Ctx.has` / `.get` / `.all` / `.int_of` / `.args`       | read what the parse produced                     |
| `Argument.required` / `.repeated` / `.env` / `.section` | narrow a declared argument                       |

## `atomic`

The safe way to share mutable state across coroutines (GRAMMAR group 10): an immutable `:=` binding holds
an `Atomic[int]` cell whose contents mutate through sequentially-consistent operations. MVP: `int`-typed.

> **[not yet]** The module ships and **cannot be imported**, and it is the one of the thirteen that
> does not. `Atomic[T]` is a generic struct and a generic struct is a form this compiler has not built,
> so `import "atomic"` is refused by name at the line that asked for it — _E511 the module `atomic`
> ships and cannot be imported_, with a place. The signatures below also name `Ref[T]`, which does not
> exist either. Share state across coroutines with a channel until this lands.
>
> It stays in the table rather than being taken out of the shipped set, because this compiler resolves
> the standard library by **listing its directory**: a module moved out of `src/stdlib/` also leaves
> `zerg fmt --check` and the rest of the self-source set, and rots unread until generics arrive. What
> would be left at the import is _E502 cannot resolve import `atomic`_ — a sentence about a module
> that is right there.

| Function                                                       | Summary                                   |
| -------------------------------------------------------------- | ----------------------------------------- |
| `atomic(v: int) -> Ref[int]`                                   | a fresh shared cell holding `v`           |
| `load(a: Ref[int]) -> int`                                     | read the cell                             |
| `store(a: Ref[int], v: int) -> int`                            | write `v`, return it                      |
| `swap(a: Ref[int], v: int) -> int`                             | write `v`, return the previous value      |
| `fetch_add(a: Ref[int], n: int) -> int`                        | add `n`, return the previous value        |
| `compare_swap(a: Ref[int], expect: int, desired: int) -> bool` | CAS: set to `desired` iff it was `expect` |

## `testing`

What a `#[test]` function needs that the LANGUAGE does not give it, for the tests `zerg test` builds and
runs — see [Modules, Packages & Programs](package.md) for how far that command goes.

**The assertion is not here.** `assert cond` is a keyword (see [Grammar](../surface/grammar.md), group 8):
the compiler writes the failure message, and it says three things a function never could — the file and
line the claim was written at, the claim's own source text, and the value of each operand a comparison
came apart into. Zerg has no `__FILE__` and no caller attribution, and a condition reaches a helper as a
`bool` with its shape already compiled away, so `assert_eq` bought back two of those values by taking the
operands apart at the call. That is why it existed and why it no longer has to.

A failed claim raises `AssertionError`, and nothing else does — which is how `zerg test` reports it as a
**failure** while anything else that reaches the top of a test body is a **crash**.

| Function                                | Summary                                  |
| --------------------------------------- | ---------------------------------------- |
| `assert_raises[T](r: Result[T]) -> Err` | answer the `Err` a `guard`ed call raised |

`assert_raises` is not an assertion and stays a function: it asks what an already-finished call raised. It
takes the **`guard` written at the point of the call** and hands back the error, so the kind is asked with
the language's own `is` rather than passed in — a type is not a value in Zerg, so
`assert_raises(f, ValueError)` cannot be spelled at all:

```text
e := testing.assert_raises(guard { strings.split("a,b", "") })
assert e is ValueError
```

> A **closure** — `assert_raises(fn () { strings.split("a,b", "") })` — reads better and does not compile:
> a closure body naming an imported module is _E735 a closure captures `strings`_, a namespace being a
> free name that a capture would have to give a type. Every test that reaches its module through `import`
> is that shape, so the `guard` is the form that serves them.

### What a running test says

`Context` is the channel a test speaks to its runner over, and every method on it is a message rather than
a claim.

| Method                             | Summary                                           |
| ---------------------------------- | ------------------------------------------------- |
| `name() -> str`                    | this test's own name, as the report prints it     |
| `log(msg: str)`                    | a note shown **only if** this test fails          |
| `skip(reason: str) -> Result[nil]` | this test does not apply here; not a failure      |
| `fatal(msg: str) -> Result[nil]`   | stop now, failed — going on would only make noise |

```text
ctx.log("row 42")
assert row.id == 42
```

**The chain is gone.** `ctx.str("file", p).int("row", i).assert(ok, "…")` existed because an assertion
that said only `assertion failed` needed somewhere to hang the facts that would make it readable — and
its terminal cannot be spelled now that `assert` is a keyword. What a chain was genuinely for beyond the
values is a **domain note**, a thing about the fixture rather than about the expression, and `log` is
already that and better at it: shown only on failure, attached to the test rather than to one assertion,
and needing no terminal to be complete.
