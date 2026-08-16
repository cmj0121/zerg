# Zerg Standard Library

English | [繁體中文](stdlib.zh-TW.md)

The bundled **standard-library packages** — reached with `import "<name>"` (never ambient; the sole
exception is the `print` keyword). Zerg is **zero external dependency, like Go**: every package is **pure
Zerg** built on the self runtime — logic in the language, only the irreducible syscall/hardware leaves in
the C runtime (see [`src/runtime`](../../src/runtime/README.md) and the [zero-dependency principle](ffi.md)).
Nothing here binds a third-party library.

For the compiler-provided functions that need **no** import, see [Built-in Functions](builtins.md).

**A module's suite sits beside it** — `src/stdlib/strings_test.zg` next to `strings.zg` — which is the
white-box placement [Modules, Packages & Programs](package.md) describes, and makes each pair a package of
its own. `zerg test src/stdlib` runs all of them; `zerg test src/stdlib/strings.zg` runs one.

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
> The workaround is to end the **expression** with a terminator the eye can see —
> `strings.trim_left("  hi  ") + "|"`, whose output is then `hi  |` — and to assert the same form in the
> suite so the two agree. Do not try to exempt the file from the hook: every other line in it should be
> trimmed, and an example that needs a trailing space is an example whose reader cannot see it either.

## Packages

| Package               | Import             | Provides                                           |
| --------------------- | ------------------ | -------------------------------------------------- |
| [`io`](#io)           | `import "io"`      | standard-stream output and whole-file read/write   |
| [`fs`](#fs)           | `import "fs"`      | filesystem structure — existence, removal          |
| [`os`](#os)           | `import "os"`      | environment read/write, exit, target platform/arch |
| [`strings`](#strings) | `import "strings"` | text utilities over the built-in `str`             |
| [`ascii`](#ascii)     | `import "ascii"`   | single-byte ASCII classification for a tokeniser   |
| [`strconv`](#strconv) | `import "strconv"` | numeric text conversion in an arbitrary base       |
| [`json`](#json)       | `import "json"`    | reading and writing JSON, with one escaper         |
| [`log`](#log)         | `import "log"`     | structured logging, as a chained builder           |
| [`time`](#time)       | `import "time"`    | clocks, and timers as channels                     |
| [`math`](#math)       | `import "math"`    | numeric helpers and pure-Zerg transcendentals      |
| [`rand`](#rand)       | `import "rand"`    | a deterministic, non-cryptographic generator       |
| [`sha256`](#sha256)   | `import "sha256"`  | the FIPS 180-4 digest, for naming and integrity    |
| [`cli`](#cli)         | `import "cli"`     | a declared command line, and the help it renders   |
| [`atomic`](#atomic)   | `import "atomic"`  | the safe shared-mutable primitive                  |
| [`testing`](#testing) | `import "testing"` | what a `#[test]` needs that the language does not  |

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

| Function                        | Summary                                              |
| ------------------------------- | ---------------------------------------------------- |
| `env(key: str) -> str?`         | an environment variable's value, or `nil` when unset |
| `set_env(key: str, value: str)` | set `key` to `value`, replacing any current one      |
| `del_env(key: str) -> bool`     | remove `key`; answers whether it WAS there           |
| `exit(code: int)`               | terminate the process with `code` (does not return)  |
| `run(argv: list[str])`          | run `argv[0]` (PATH-searched), wait, `-> int` status |
| `platform() -> str`             | target OS — `"linux"`, `"darwin"`, `"windows"`, …    |
| `arch() -> str`                 | target CPU — `"arm64"`, `"x86_64"`, …                |
| `isatty(fd: int) -> bool`       | is this descriptor a terminal (0 in, 1 out, 2 err)   |

The three environment functions are the
[`xxx` / `set_xxx` / `del_xxx`](../code/functions.md#naming-a-property-and-its-two-writes) trio: `env`
reads, `set_env` writes, `del_env` removes. **`del_env` answers whether the key was there**,
which is the one thing a caller cannot find out for itself — `env` then `del_env` is two questions with a
window between them, and C's `unsetenv` reports success either way. **`set_env` raises `ValueError`** on a
name the host will not take (empty, or containing `=`); `del_env` is **total**, because a name that cannot
exist was not set and `false` is a true answer rather than a missing one.

> **Set the environment at startup, before any coroutine is spawned.** This is not a convention — it is the
> only safe use of these two functions, because they mutate **C runtime state rather than this language's**.
> POSIX's `environ` has no lock on it: `setenv` may reallocate the array and free the old one while `getenv`
> holds a pointer into it, so a write racing an `os.env` on another coroutine is a use-after-free inside
> libc. Zerg runs real OS worker threads ([Coroutines](../code/coroutine.md)), so two coroutines are two
> threads often enough that a program which happens to work proves nothing.
>
> That makes it **categorically unlike** the shared-state hazard [`log`](#log) documents: the logger's cell
> is state this project owns and an atomic could one day close that race, while `environ` is not ours and
> nothing done here can make it safe. There is no fix to wait for, only a time to call it.
>
> The compiler does **not** enforce it, and could not do so honestly: the workers exist before `main`'s
> first statement, so a rule keyed on "has anything been spawned yet" would report safety in a process that
> already has sixteen threads, and would refuse the write inside every `#[test]` (a test is a coroutine).

**`isatty` is about the DEVICE and nothing else.** Use it to choose a **rendering** — colour at a terminal,
plain into a pipe — and never a **format**; [`log`](#log) draws exactly that line, and says why. A
descriptor that is not open is not a terminal, so it answers `false` rather than raising; it is total, which
matters on a path where an abort would mean dying over escape codes.

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

## `json`

Reading and writing JSON. It is **one implementation on purpose**: the language server and the logger both
write JSON, and two escapers drift — the one that drifts is the one nobody is reading transcripts of. It
lived at `src/compiler/lsp/json.zg` until it had a second caller.

A value is a `Val`, and an object is a **`list[Field]`** rather than a map. A list keeps the order the fields
were put in, so the bytes are a function of the value alone — which is what makes a transcript diffable and a
log line greppable. `Val`'s variants are not public (an enum's variants cannot be constructed from outside
its module), so the way in is the constructors and the way out is the accessors. There is no `fields()`: a
`list[Field]` is not a variant, so a caller writes `mut fs: list[json.Field] = []`.

| Function                                              | Summary                                             |
| ----------------------------------------------------- | --------------------------------------------------- |
| `encode(v: Val) -> str`                               | the value as JSON text — one line, fields in order  |
| `decode(s: str) -> Val`                               | JSON text as a value; **raises** on malformed input |
| `get(v: Val, key: str) -> Val`                        | the value at `key`, or `Null`                       |
| `walk(v, a, b) -> Val` / `walk3(v, a, b, c)`          | a two- or three-key path in one call                |
| `has(v: Val, key: str) -> bool`                       | present, and not `null`                             |
| `as_str` / `as_int` / `as_list`                       | the payload, or `""` / `0` / `[]`                   |
| `is_null(v: Val) -> bool`                             | this value is JSON `null`                           |
| `null()` / `of_str(s)` / `of_list(xs)` / `of_obj(fs)` | build a `Val`                                       |
| `put(fs, k, v)` / `put_str` / `put_int` / `put_bool`  | append a field to a `list[Field]`                   |

**The accessors are TOTAL.** Asking a number for its string gives `""`, and asking a non-object for a key
gives `Null` — deliberate for input that comes from another program, where a field of the wrong shape should
get a default and a reply rather than an abort. Where a missing field is genuinely fatal, ask `has`. One
consequence to know: a key present with a `null` reads as **absent**, because `has` is written on `get`.

**Numbers are integers.** `Val` has no float variant, so `decode("1.5")` is `Int(1)` — the fraction and any
exponent are consumed and **dropped**, not refused. That was the shape the language server needed and it has
not changed; it is written here because a reader who is not told will find out from a wrong answer.

**`encode` escapes what JSON reserves and nothing else** — the two delimiters, the five short escapes, and a
control byte below `0x20` as `\u00XX`. Everything at `0x20` and above passes through, which is what carries
UTF-8 unchanged: a multi-byte code point is already legal JSON text. So `decode` then `encode` is not
byte-identical — `"\u00e9"` comes back as the character — while `encode` then `decode` is stable.

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

## `log`

Structured logging, as a **chained builder**:

```zerg
log.info().str("file", path).int("line", n).msg("compiling")
```

The builder is the shape **this** language leaves — no varargs, no `any`, no generic struct, each argued in
`src/stdlib/log.zg` — and it is also the answer to **lazy evaluation**: `log.debug()` at a level that is off
answers a **dead** entry, formatting and allocating nothing, because the caller hands over typed values
rather than a string it built first. What is still paid is evaluating the **arguments**, which Zerg does
before the call. That is why `enabled` is public:

```zerg
if log.enabled(log.Level.DEBUG) {
 log.debug().str("dump", expensive()).msg("state")
}
```

### The two surfaces

There is a **global logger**, configured by one function and used with no plumbing, and a **constructor** for
one you hold and pass. They are not two implementations: the global **is** an instance, held in this module's
own cell, so every field method, every level check and every writer exists once.

| Function                               | Summary                                              |
| -------------------------------------- | ---------------------------------------------------- |
| `new() -> Logger`                      | a logger configured from the environment, to stderr  |
| `install(lg: Logger)`                  | replace the global logger — the one mutation         |
| `parse_level(s: str) -> Level?`        | a level by name (`debug`), or `nil`                  |
| `enabled(lvl: Level) -> bool`          | would the global write a line at `lvl`               |
| `at_level(lvl)`, `trace()` … `fatal()` | begin a line on the global logger — all six levels   |
| `to_stderr() -> Sink`                  | the default destination — one write per line to fd 2 |
| `to_chan(ch: chan[str]) -> Sink`       | each finished line as a value on a channel           |

`Logger` answers `level(l)`, `format(f)`, `colour(on)`, `to(sk)`, `with_str(k, v)`, `with_int(k, v)` and
`enabled(l)`, each a **copy** — a logger handed to a component cannot reconfigure its caller's — plus
`at_level(l)` and the level methods.
`Entry` answers `str`, `int`, `bool`, `dur`, `err` and the terminal `msg`.

**There is no `Logger.debug()`, and the reason is a language rule.** `display` and `debug` are the two
renderings every value has ([Formatting](format.md)), so a method by either name must answer the `str` the
value shows as — `E361` refuses a level method called `debug`. It is a rule about **methods**, so the free
`log.debug()` above is the level's own name and is accepted; on an instance the sixth level is
`lg.at_level(log.Level.DEBUG)`. `at_level` is spelled that way rather than `at`, and `parse_level` rather
than `parse`, because a `pub` name has no package to be unique within: a free `pub at` is `E705` against the
compiler's own lexer, which has a module-private `at`, and a `pub parse` here would collide with a
module-private `parse` in any program that imports `log`.

**There is one mutating function and it takes a whole logger.** The `set_level` / `set_format` / `set_colour`
/ `set_sink` family was **deleted**, not renamed: the module already had four pure builders, so the setters
were a second way to say the same thing that happened to mutate shared state four times where one would do.

```zerg
log.install(log.new().level(log.Level.DEBUG).format(log.Format.JSON))
```

It is `install` and not `level` because `level` already means _derive a logger at this level_, and one word
may not be pure on an instance and effectful on the module. `enabled` lives in both places precisely because
it means the same in both.

**There is no `current()`.** Deriving from the installed logger would read as mid-flight reconfiguration, and
the cell is not safe for that (see [Configuring is a startup act](#configuring-is-a-startup-act)). Restoring
the default is `log.install(log.new())` — the cell is initialised at its declaration by that same public
constructor, so nothing needs to read it back, which is also how a test suite isolates itself.

**`log.new()` is the only way to build a `Logger` from outside.** Every field carries a default, which a
module-private field must (`E482`), so `Logger()` exists whatever the module wants — and its defaults name
module-private consts, so a caller writing `log.Logger()` gets `E301` rather than a second constructor that
silently ignores the environment.

### Configuring is a startup act

The global logger lives in the standard library's only module-level `unsafe { … }` group, and `log.zg` carries
the full pattern above it — this is the summary. The language enforces the first of its four rules and only
that one: a top-level `mut` outside such a group is `E358` and `pub` on one inside it is `E484`, which is two
codes for one rule. So configuration-by-function is not the recommended way, it is the only way the language
permits — and the rest of the shape is held by `scripts/log-check.sh`, which reads the module.

What it costs, stated without rounding it up: a `Logger` holds a `list` and a `Sink`, so installing one is
several machine stores and not one, and a read that runs while `install` runs is a data race. **The rule is
configure at startup, from one coroutine, then read** — a rule, not a guarantee. `log.new()` is the answer for
anything that has to change while the program runs; an instance is a value, and a value handed to a component
races with nobody. Installing twice is legal and the last one wins, silently.

### Levels

`TRACE` `DEBUG` `INFO` `WARN` `ERROR` `FATAL`, and `OFF`, as the variants of an **enum** rather than the `int`
constants they were: `E340` refuses an `int` where a `Level` belongs and a `Level` where an `int` does, `E347`
refuses comparing a variant with a number, and every renderer being an **exhaustive `match`** means a level
cannot be added without ranking it, naming it and colouring it — `E428` names the arm that was forgotten. The
`int` version accepted `log.new().level(99)`, printed a blank, and said nothing either time.

**`OFF` is not in the ordering at all.** It is the threshold that accepts nothing: a logger set to it writes
nothing, including a line written _at_ `OFF`. The ordering itself is one private function, and the enum's
declaration order is deliberately **not** a contract — nothing in the module consults a discriminant, so
alphabetising the variants would change nothing about what any program filters.

**`ZERG_LOG_LEVEL` picks the level, by name.** `ZERG_LOG_LEVEL=debug`, never `=1`: a number would pin the
discriminants the module refuses to consult. An unrecognised name is `INFO`, for the reason an unrecognised
`ZERG_LOG` is `pretty`. `log.parse_level(s)` is the same reader, public, for a program with a flag of its own.

`fatal` writes its line and then **exits 1**. It is not `panic` — Zerg says that with `raise`, and a logger
competing with the error taxonomy would give a program two unrelated ways to end. The exit happens **even at a
level where the line is not written**: silencing a logger changes what is reported, never what is done.

### One line is one write

The whole line, newline included, goes to a single `__zrt_write`. This is not a detail: `zrt_report` once split
a prefix and a message into two writes, and a stress test found **830 lines in 24000** carrying one kind with
another's message. `scripts/log-check.sh` holds it by reading the module — exactly one `io.ewrite(line)`, and
no `eprintln`, which is two writes. It does **not** hold it with a stress test, and says why: the same script
mutated `emit` into two writes and could not make 12000 lines from 24 coroutines tear, because coroutines are
cooperative and there is no parking point between two adjacent writes. That program still runs, for the claim
it can make — every line arrives, and arrives whole.

A value that would be ambiguous bare — empty, or carrying a space, a quote, a backslash, an `=` or a control
character — is written through `json.encode`, the tree's one escaper. So a newline in a value is escaped rather
than written, and one record stays one line.

### Two formats, and what picks each

**`pretty` is the default** and `ZERG_LOG=json` is the only thing that changes it, `Logger.format` aside. That
departs from zerolog, whose default is JSON, and the reason is who is on the other end: an unconfigured
program is one somebody is running.

```console
$ ./myprog
2026-08-15T10:22:31Z INF compiling  file=a.zg line=12

$ ZERG_LOG=json ./myprog
{"t":"2026-08-15T10:22:31Z","l":"info","msg":"compiling","file":"a.zg","line":12}
```

**Colour follows `isatty`; the format does not.** Colour is a rendering — it is about the device, and "is
this a device" is exactly what [`os.isatty`](#os) answers, so a line is coloured at a terminal and plain in a
pipe. A format is a semantic choice about who the output is for, and a program whose logs change **shape**
when they are redirected has logs that cannot be read the same way twice. `NO_COLOR` overrides the terminal,
by its **presence** — any value, the empty string included. An unrecognised `ZERG_LOG` is `pretty` rather
than an error: a logger that refused to start over a misspelt variable would take a program down for a
reason that has nothing to do with it.

Only the **level** is coloured. It is what a reader scans for, and colouring the message or the values would
fight with whatever they contain. A JSON line is never coloured at all — escape codes in a field a machine
parses are corruption, not decoration.

The JSON line is built through [`json`](#json), not by hand. Its three fixed keys — `t`, `l`, `msg` — come
first, in that order.

### The destination

A `Sink` is a **value carrying a mode**, not a spec and not a closure: a spec would need `#[dyn]` (there is a
deferred gap around non-`#[dyn]` provided methods) and a closure naming an imported module is `E735`. `to_chan`
is what makes a logger testable — there is no reading a `write(2)` back, so this module's own suite asserts the
bytes by receiving them. A channel sink needs capacity for what is written before it is drained, since a send
to a full channel parks the sender.

## `time`

Clocks, calendars and timers. `now` is a date; `monotonic` is meaningful only as a **difference** (elapsed
time) and never runs backwards. **A timer is a channel** — `after` and `ticker` answer receive-only
channels, so a `select` arm on one is a timeout or a tick with no new syntax (see
[Coroutines](../code/coroutine.md)). Durations are **nanoseconds**, the unit `monotonic` reads; a duration
`<= 0` fires at once.

| Function                   | Summary                                                       |
| -------------------------- | ------------------------------------------------------------- |
| `now() -> int`             | wall-clock time, whole seconds since the Unix epoch           |
| `monotonic() -> int`       | a monotonic reading in nanoseconds (use differences)          |
| `utc(t: int) -> Date`      | a Unix second count broken into its UTC calendar fields       |
| `rfc3339(t: int) -> str`   | the same instant as `2025-08-15T10:22:31Z`                    |
| `duration(ns: int) -> str` | a nanosecond count a person can read — `1.5s`, `250ms`        |
| `after(d) -> <-chan[int]`  | one value once `d` nanoseconds have passed                    |
| `ticker(d) -> <-chan[int]` | a value every `d` nanoseconds; the channel holds **one** tick |

**`Date` is UTC and nothing else**, and that is a decision rather than a gap: local time needs the zone
database, a host file a zero-external-dependency stdlib will not read. Its fields are `year`, `month`
(1..12), `day` (1..31), `hour`, `minute`, `second`. The conversion is the civil-from-days algorithm — exact,
no month table, no leap-year branch — and it is correct **before 1970 too**, because Zerg divides toward
negative infinity (`-1 / 86400` is `-1`, not `0`) and takes the modulo's sign from the divisor.

`rfc3339` renders seconds of precision, which is all `now()` has. A year outside `0000`–`9999` keeps the
digits it has, with a leading `-` if negative — that is **no longer RFC 3339**, and it is preferred to
truncation because a clock that far out is a bug the reader has to be able to see.

`duration` picks the largest unit that leaves a whole part (`s`, `ms`, `µs`, `ns`), then up to three
fraction digits with trailing zeros dropped and **truncated, not rounded**, so a duration that reads under
a threshold really was under it. Seconds are the largest unit: there are no minutes, because `1.5m` invites
being read as milli-anything.

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
`guard` like any other conversion that can fail.

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
| `Command.exclusive(xs)` / `.one_of(xs)`                 | at most one of a set / exactly one               |
| `Command.version` / `.usage` / `.epilog` / `.no_help`   | what `--help` and `--version` say                |
| `Command.render() -> str`                               | the help text, for a program that places it      |
| `Command.exec(argv: list[str]) -> int`                  | parse, dispatch, and answer the process's status |
| `Ctx.has` / `.get` / `.all` / `.int_of` / `.args`       | read what the parse produced                     |
| `Ctx.path() -> str`                                     | the command names that led here, joined          |
| `Argument.required` / `.repeated` / `.env` / `.section` | narrow a declared argument                       |

`one_of` is a name rather than a flag on `exclusive` because "at most one" and "exactly one" are two
constraints, and `.exclusive(xs, true)` says neither of them at a call site.

## `atomic`

The safe way to share mutable state across coroutines (GRAMMAR group 10): an immutable `:=` binding holds
an `Atomic[int]` cell whose contents mutate through sequentially-consistent operations. MVP: `int`-typed.

> **[not yet]** The module ships and **cannot be imported**, and it is the one of the fifteen that
> does not. `Atomic[T]` is a generic struct and a generic struct is a form this compiler has not built,
> so `import "atomic"` is refused by name at the line that asked for it — _E511 the module `atomic`
> ships and cannot be imported_, with a place. The signatures below also name `Ref[T]`, which does not
> exist either, and the module carries a second, `Atomic[T]`-shaped surface (`new_atomic`) waiting on the
> same thing. Share state across coroutines with a channel until this lands.
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
`bool` with its shape already compiled away; `assert_eq` existed to buy back two of those values, and no
longer has to.

A failed claim raises `AssertionError`, and nothing else does — which is how `zerg test` reports it as a
**failure** while anything else that reaches the top of a test body is a **crash**.

| Function                                | Summary                                  |
| --------------------------------------- | ---------------------------------------- |
| `assert_raises[T](r: Result[T]) -> Err` | answer the `Err` a `guard`ed call raised |

**The rest of the module is the runner's, not a test's.** `Event`, `Outcome`, `context`, `finished` and
`collect` are `pub` because the driver `zerg test` generates is a file **in the package under test**, so it
reaches them the way any importer would. A test body calls none of them; they are on this page so that a
reader who meets one in the module does not take it for a helper they were meant to use.

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
