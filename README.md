# Zerg

> Write the code as you think — one way, and only one way, to do it.

Zerg programs are fast to write, easy to read, and overwhelmingly straightforward — swarm your
problems with simplicity.

## Design Principles

| Principle        | Description                                         |
| ---------------- | --------------------------------------------------- |
| small and crisp  | minimal syntax                                      |
| safe by default  | immutable and private unless explicitly `mut`/`pub` |
| null-safe        | no billion-dollar mistakes                          |
| concurrent       | built-in support for concurrency                    |
| procedural-first | straightforward, top-down control flow              |
| scope-owned      | no GC — memory freed at scope exit                  |
| strongly typed   | catch errors at compile time                        |
| copy-by-value    | values are copied by default; compiler may optimize |

## Roadmap

Zerg has a long-term vision of being a general-purpose systems programming language, but the initial
focus is on building a workable prototype with a minimal feature set:

### Pre-1.0

- [x] **v0.0** — toolchain bootstrap.
- [x] **v0.1** — procedural core.
- [ ] **v0.2** — composite data.
- [ ] **v0.3** — borrow checking.
- [ ] **v0.4** — polymorphism and errors.
- [ ] **v0.5** — modules.
- [ ] **v0.6** — generics and null-safety.
- [ ] **v0.7** — concurrency runtime.
- [ ] **v0.8** — standard library.
- [ ] **v0.9** — developer tooling.
- [ ] **v0.10** — hardening and language reference.
- [ ] **v1.0** — source stability.

A phase ships when each example's stdout matches between `zerg run` and `zerg build` — byte-identical
for sequential code, equivalent under any valid scheduling for concurrent code.

## Building & running

The compiler is a Go program that interprets `.zg` source directly, or compiles it by emitting C and
shelling out to the system C compiler. v0.0 (toolchain bootstrap) and v0.1 (procedural core) share
the same `zerg` binary; v0.0 examples (`00_nop.zg`, `01_hello.zg`) keep working unchanged.

### Prerequisites

- Go 1.22 or newer.
- A C compiler reachable as `cc` on `PATH` (override with `$CC`). It must accept `-fwrapv`; gcc and
  clang on macOS / Linux qualify. tcc and MSVC are out of scope at v0.1.
- macOS or Linux. Windows is deferred.

### Build the toolchain

```sh
make build
```

This produces `src/bootstrap/bin/zerg`.

### Run a source file

```sh
./src/bootstrap/bin/zerg run examples/01_hello.zg
# Hello, Zerg!
```

### Compile to a native binary

`zerg build` writes the binary into the current working directory, named after the source basename.
The build path invokes `cc -fwrapv -O2 -o <out> <gen>.c -lm`; `-fwrapv` pins signed integer overflow
to two's-complement wrap, and `-lm` is linked because the generated code calls `floor` / `fmod` for
float `//` and `%`.

```sh
./src/bootstrap/bin/zerg build examples/01_hello.zg && ./01_hello
# Hello, Zerg!
```

`zerg build --emit-c <file>` prints the generated C to stdout instead of compiling. The v0.1
runtime header (`zerg_str`, `zerg_print_*`, `zerg_str_concat`) is emitted inline at the top of the
output — no external runtime to link against.

### REPL

The v0.1 REPL is multi-line: input accumulates until it parses cleanly, so you can paste a function
body or a `for` block one line at a time.

```sh
./src/bootstrap/bin/zerg repl
# Zerg REPL v0.1 — accepts the v0.1 procedural core
# Type :exit to quit, :help for syntax
# zerg> let x := 1 + 2
# zerg> print x
# 3
# zerg> fn double(n: int) -> int {
# ...     return n * 2
# ... }
# zerg> print double(21)
# 42
# zerg> :help
# Statements: let/mut/const, fn, if/elif/else, for, return/break/continue, print. Run :exit to quit.
# zerg> :exit
```

### Example version gating

Examples `02_variables.zg` through `13_asm.zg` carry a `# requires: vX.Y` first-line comment for the
version they need. The CLI inspects that comment before lexing the body and refuses to run anything
beyond the current toolchain version with a clean message:

```sh
./src/bootstrap/bin/zerg run examples/02_variables.zg
# zerg: examples/02_variables.zg requires v0.2 (current is v0.1)
```

The v0.0 examples (`00_nop.zg`, `01_hello.zg`) carry no `requires` line and continue to run.

### Supported syntax at v0.1

v0.1 is the procedural core. Surface summary:

- **Variables.** `let name := expr` (immutable, inferred), `let name: T = expr` (immutable,
  annotated), `mut` for reassignable bindings, `const` for compile-time-constant literals. Same form
  for annotated variants.
- **Types.** `int` (`int64`), `float` (`float64`), `bool`, `str`. No implicit coercion — `int +
float` is a type error, and v0.1 has no cast operator (the only float source is a float literal).
- **Operators.** Arithmetic `+ - * / // % ~`; bitwise `& | ^ << >>`; comparison `== != < > <= >=`
  (non-associative — `a < b < c` is a parse error); logical `and or not xor` with short-circuit
  `and` / `or`; `+` concatenates `str`. Range `..` (half-open) and `..=` (closed) only as the head
  of `for x in …`; anywhere else is a parse error.
- **Control flow.** `if` / `elif` / `else`; `for { … }` infinite, `for cond { … }` while-style,
  `for x in start..end { … }` and `..=` closed; `break`, `continue`, `return [expr]` with
  `if`-guard forms (`return e if cond`, `break if cond`, `continue if cond`).
- **Functions.** Top-level only — no nested fns, no closures, no default args, no named args, no
  varargs. Positional parameters with required type annotations; optional `-> T` return type.
- **Print.** `print expr` is a keyword, not a function. Takes one expression of any v0.1 primitive
  type. Format is pinned (see below).
- **Out of scope at v0.1, deferred.** Tuples beyond a literal multi-return shape, `list` / `set` /
  `map`, structs, enums, `match`, lambdas / closures, modules and imports, error handling,
  generics, channels, string interpolation, `byte` / `rune`. Each lands at the version that needs
  it; the example carrying that feature is gated to that version.

### Print format and numeric semantics

Both `zerg run` and `zerg build` implement the same table so stdout is byte-identical:

| Type  | Format                                                                               |
| ----- | ------------------------------------------------------------------------------------ |
| int   | `printf("%lld", x)` / `strconv.FormatInt(x, 10)` — decimal                           |
| float | `printf("%.17g", x)` / `strconv.FormatFloat(x, 'g', 17, 64)` — 17 significant digits |
| bool  | `"true"` / `"false"`                                                                 |
| str   | raw bytes (no quotes, length-tracked)                                                |

`print` appends a single trailing `\n`. Numeric semantics are pinned:

- Signed integer overflow wraps two's-complement (Go's natural 64-bit wrap; C side relies on
  `-fwrapv`).
- Integer division truncates toward zero. `//` on `int` is identical to `/` on `int`.
- Integer modulus follows the dividend's sign: `a == (a/b)*b + (a%b)`.
- `//` on float is `floor(a/b)` (libm) / `math.Floor(a/b)`; `%` on float is `fmod` / `math.Mod`.
- `INT64_MIN / -1`, integer division by zero, and `±Inf` / `NaN` printing are not exercised by the
  v0.1 corpus.

### Parity rule

The e2e test asserts that `zerg run` and `zerg build`-then-execute produce byte-identical stdout for
every supported example. v0.0 keeps its golden corpus at `src/bootstrap/test/golden/`; v0.1 ships a
20-program corpus at `src/bootstrap/test/v0_1/` exercising every v0.1 construct (variables,
literals, arithmetic, bitwise, comparisons, short-circuit logic, `if`/`elif`/`else`, all three
`for` forms, guarded `break`/`continue`/`return`, top-level functions and mutual recursion, string
concatenation, scope and shadowing, negative-dividend modulus, and overflow wrap). Run it with:

```sh
make test
```

## DDD (Dream-Driven Development)

This project is based on the DDD (dream-driven development) methodology which means
the project is based on what I dream of.

All the features are based on my needs and my dreams.
