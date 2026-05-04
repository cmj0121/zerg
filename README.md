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
- [x] **v0.2** — composite data.
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
shelling out to the system C compiler. v0.0 (toolchain bootstrap), v0.1 (procedural core), and v0.2
(composite data) share the same `zerg` binary; earlier examples (`00_nop.zg`, `01_hello.zg`, and the
v0.1 corpus) keep working unchanged.

### Prerequisites

- Go 1.23 or newer.
- A C compiler reachable as `cc` on `PATH` (override with `$CC`). It must accept `-fwrapv`; gcc and
  clang on macOS / Linux qualify. tcc and MSVC are out of scope at v0.2.
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

`zerg build --emit-c <file>` prints the generated C to stdout instead of compiling. The v0.2
runtime header (`zerg_str`, `zerg_print_*`, `zerg_str_concat`, plus per-shape `zerg_list_*` /
`zerg_tuple_*` / `zerg_struct_*` / `zerg_enum_*` helpers and `zerg_match_panic`) is emitted inline at
the top of the output — no external runtime to link against.

### REPL

The v0.2 REPL is multi-line: input accumulates until it parses cleanly, so you can paste a function
body, a `for` block, a `struct`/`enum` declaration, or a `match` one line at a time.

```sh
./src/bootstrap/bin/zerg repl
# Zerg REPL v0.2 — accepts the v0.2 procedural core plus composite data
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
# Statements: let/mut/const, fn, struct/enum, if/elif/else, for, match, return/break/continue, print. Run :exit to quit.
# zerg> :exit
```

### Example version gating

Examples `02_variables.zg` through `13_asm.zg` carry a `# requires: vX.Y` first-line comment for the
version they need. The CLI inspects that comment before lexing the body and refuses to run anything
beyond the current toolchain version with a clean message:

```sh
./src/bootstrap/bin/zerg run examples/10_specs.zg
# zerg: examples/10_specs.zg requires v0.4 (current is v0.2)
```

The v0.0 examples (`00_nop.zg`, `01_hello.zg`) carry no `requires` line and continue to run. The
v0.2-tagged showcase examples in `examples/` (`02_variables.zg`, `09_struct.zg`, …) advertise the
broader v0.2 surface; they pass the version gate but still exercise features such as `set`/`map`
literals and `pub` that PLAN defers past v0.2 — those bodies will still error out at lex/parse/type
time. The authoritative v0.2 corpus lives at `src/bootstrap/test/v0_2/`.

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

### Supported syntax at v0.2

v0.2 is composite data on top of the v0.1 procedural core. New surface (everything in v0.1 still
works):

- **New primitives.** `byte` (8-bit unsigned, default for ASCII rune literals like `'A'`) and `rune`
  (32-bit Unicode code point, default for non-ASCII literals like `'漢'`). Both print as their
  decimal value. Use the annotated form (`upper: rune = 'A'`) to override the default classification.
- **Tuples.** `(a, b)` literal, 2 or more elements; one-element parens remain grouping (`(a,)` is
  rejected). Destructuring binds with `let (x, y) := pair`. Print format: `( e1, e2 )`.
- **Lists.** `[a, b, c]` literal; empty `[]` is allowed only in annotated/inferable position
  (`let xs: list[int] = []`). Built-in `len(xs)`, indexing `xs[i]` (read-only, non-negative),
  iteration `for x in xs { … }`. Slicing `xs[lo..hi]`, `xs[lo..=hi]`, `xs[..hi]`, `xs[lo..]`,
  `xs[..]` — each returns a freshly allocated list, no views. Print format: `[ e1, e2, e3 ]`; empty
  list prints `[]`.
- **Strings.** `str` indexing `s[i]` returns a `rune` (the i-th UTF-8 code point). Slicing on `str`
  is deferred.
- **Structs.** `struct Point { x: int, y: int }` declaration (top-level only, no methods, no `pub`,
  no recursive shapes). `Point { x: 1, y: 2 }` literal must list every declared field. Field read
  `p.x`. Print format: `Point { x: 1, y: 2 }` in declaration order.
- **Enums.** `enum Color { Red, Green, Blue }` declaration with **variant names only** (no payloads
  at v0.2). Variant access `Color.Red`. Print format: `Color.Red`.
- **`match`.** `match expr { pattern [if guard] => statement; … }`. Patterns: literal,
  wildcard `_`, identifier-bind, tuple destructure `(a, b)`, struct destructure `Point { x, y }` or
  `Point { x: 0, .. }`, enum variant `Color.Red`. Optional guard. Arms tested top-to-bottom; first
  match wins.
- **No-match panic.** If no arm matches at run time, both `zerg run` and the compiled binary print
  a `match: no arm matched` diagnostic with source position to stderr and exit 1. There is **no
  silent fall-through** at v0.2 — earlier PLAN drafts that mentioned silent fall-through have been
  retired.
- **Two-pass typeck.** Top-level `struct`, `enum`, and `fn` declarations may reference each other
  in any order; declaration order does not matter at file scope.
- **Value semantics, including lists.** `let ys := xs` deep-copies the list (allocates a new
  backing array). Slicing likewise allocates. There is no aliasing at v0.2 because there is no
  list mutation; the contract is consistent today and forever, with v0.3's borrow checker tightening
  enforcement, not the model.
- **Out of scope at v0.2, deferred.** Maps `{k: v}` and sets `{a, b}` (need a `Hashable` spec),
  enum payloads (`Color.Custom(r, g, b)`), struct update syntax `Point { x: 1, ..p }`, struct
  methods / `impl`, list mutation (`xs[0] = 1`, `xs.push(…)`), list patterns with `..tail`,
  negative indexing, slicing on `str`, string interpolation (`"hi {name}"`), multi-line `"""…"""`
  and raw `r"…"` strings, `pub` / visibility, single-element tuples `(a,)`, lambdas and closures,
  modules and imports, error handling, generics, channels.

A small v0.2 sample — struct + enum + match together:

```zerg
struct Point { x: int, y: int }
enum Quadrant { I, II, III, IV, Origin }

fn quadrant(p: Point) -> Quadrant {
    match p {
        Point { x: 0, y: 0 } => return Quadrant.Origin
        Point { x, y } if x > 0 and y > 0 => return Quadrant.I
        Point { x, y } if x < 0 and y > 0 => return Quadrant.II
        Point { x, y } if x < 0 and y < 0 => return Quadrant.III
        _ => return Quadrant.IV
    }
    return Quadrant.Origin
}

print quadrant(Point { x: 3, y: 4 })
print quadrant(Point { x: 0, y: 0 })
# Quadrant.I
# Quadrant.Origin
```

### Print format and numeric semantics

Both `zerg run` and `zerg build` implement the same table so stdout is byte-identical:

| Type    | Format                                                                                  |
| ------- | --------------------------------------------------------------------------------------- |
| int     | `printf("%lld", x)` / `strconv.FormatInt(x, 10)` — decimal                              |
| float   | `printf("%.17g", x)` / `strconv.FormatFloat(x, 'g', 17, 64)` — 17 significant digits    |
| bool    | `"true"` / `"false"`                                                                    |
| str     | raw bytes (no quotes, length-tracked)                                                   |
| byte    | decimal of the unsigned value (`%hhu` / `FormatUint`)                                   |
| rune    | decimal of the code point (`%d` on `int32` / `FormatInt`)                               |
| list[T] | `[ e1, e2, e3 ]` (each element printed by its own format); empty list prints `[]`       |
| tuple   | `( e1, e2 )` — comma+space between elements                                             |
| struct  | `Name { field1: v1, field2: v2 }` — declaration order, `:` between field name and value |
| enum    | `Name.VariantName`                                                                      |

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
20-program corpus at `src/bootstrap/test/v0_1/`; v0.2 adds a 23-program corpus at
`src/bootstrap/test/v0_2/` exercising `byte`/`rune` literals, `str` indexing, tuple literals and
destructure, list literal/index/iter/slice/copy, struct decl with nesting and forward references,
enum decl, and every `match` pattern form (literal, bind+guard, tuple, struct shorthand+rest, enum,
nested). Program 22 (`22_no_match_panics.zg`) is the panic case: both halves exit 1 with a
`match: no arm matched` stderr line. Run the full corpus with:

```sh
make test
```

## DDD (Dream-Driven Development)

This project is based on the DDD (dream-driven development) methodology which means
the project is based on what I dream of.

All the features are based on my needs and my dreams.
