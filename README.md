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
- [x] **v0.3** — borrow checking.
- [x] **v0.4** — polymorphism and errors.
- [x] **v0.5** — modules.
- [x] **v0.6** — generics and null-safety.
- [x] **v0.7** — concurrency runtime.
- [x] **v0.8** — standard library.
- [x] **v0.9** — developer tooling.
- [ ] **v0.10** — hardening and language reference.
- [ ] **v1.0** — source stability.

A phase ships when each example's stdout matches between `zerg run` and `zerg build` — byte-identical
for sequential code, equivalent under any valid scheduling for concurrent code.

## Building & running

The compiler is a Go program that interprets `.zg` source directly, or compiles it by emitting C and
shelling out to the system C compiler. v0.0 (toolchain bootstrap), v0.1 (procedural core), v0.2
(composite data), v0.3 (borrow checking), v0.4 (polymorphism), v0.5 (modules), v0.6 (generics
and null-safety), v0.7 (concurrency runtime), v0.8 (standard library), and v0.9 (process surface
and time, plus the `never` bottom type) share the same `zerg` binary; earlier examples
(`00_nop.zg`, `01_hello.zg`, and the v0.1 / v0.2 / v0.3 / v0.4 / v0.5 / v0.6 / v0.7 / v0.8
corpora) keep working unchanged.

### Prerequisites

- Go 1.23 or newer.
- A C compiler reachable as `cc` on `PATH` (override with `$CC`). It must accept `-fwrapv` and
  `-pthread`; gcc and clang on macOS / Linux qualify. tcc and MSVC are out of scope at v0.7.
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

`zerg build --emit-c <file>` prints the generated C to stdout instead of compiling. The v0.4
runtime header (`zerg_str`, `zerg_print_*`, `zerg_str_concat`, plus per-shape `zerg_list_*` /
`zerg_tuple_*` / `zerg_struct_*` / `zerg_enum_*` helpers — including `<T>_clone` for the
opt-in deep-copy builtin, the in-place list `<T>_push` lowering, the auto-derived `<T>_eq`
structural equality helpers, the per-spec `zerg_dyn_<Spec>` fat-pointer / `zerg_vtable_<Spec>`
struct, and `zerg_match_panic` / `zerg_not_implemented`) is emitted inline at the top of the
output — no external runtime to link against. v0.5 keeps the single-TU emit and prefixes every
type/fn/method symbol with a per-module mangle so multi-file programs link cleanly without a
separate object-file step. v0.6 reuses that path for monomorphized generics: each unique
`(decl, type-args)` pair lowers to one C struct/fn whose mangle suffixes the canonical type-arg
vector (e.g. `Box[int]` → `Box__int`), so generic instances dedup at link time without a separate
template machinery. v0.7 extends the embedded prelude with a pthread-backed concurrency runtime —
per-element-type `zerg_chan_<T>` (mutex + condvar + ring buffer), `zerg_chan_send` / `_recv` /
`_close`, `zerg_select` array-of-cases helper, `pthread_create` wrapper for `spawn`, a per-thread
LIFO defer stack drained at fn exit, and `zerg_wait_group_t` (mutex + condvar + counter). The build
path adds `-pthread` to the cc flags; macOS and Linux both ship pthreads with the toolchain.

### REPL

The v0.7 REPL is multi-line: input accumulates until it parses cleanly, so you can paste a function
body, a `for` block, a `struct`/`enum`/`spec`/`impl` declaration, or a `match` one line at a time.
The borrow checker runs against each accepted program — diagnostics fire at the prompt, not the
next prompt. `import` is intentionally not admitted at the REPL: typing `import "x"` produces the
dedicated diagnostic `import not supported at REPL` and the session continues. `defer` at the
prompt is rejected (no enclosing fn). A `spawn fn() { ... }()` at the prompt is admitted; the REPL
synchronously waits for the spawned task to finish before printing the next prompt so output stays
deterministic.

<!-- markdownlint-disable MD013 -->

```sh
./src/bootstrap/bin/zerg repl
# Zerg REPL v0.9 — accepts the v0.9 surface (procedural core, composite data, borrow checking, polymorphism, modules, generics, null-safety, concurrency, stdlib, process surface, time)
# Type :exit to quit, :help for syntax
# zerg> let x := 1 + 2
# zerg> print x
# 3
# zerg> fn double(n: int) -> int {
# ...     return n * 2
# ... }
# zerg> print double(21)
# 42
# zerg> let opt: int? = 7
# zerg> print opt ?? -1
# 7
# zerg> let ch := chan[int](1)
# zerg> ch <- 42
# zerg> print <- ch
# Option.Some(42)
# zerg> :help
# Statements: let/mut/const, fn, struct/enum/spec/impl, if/elif/else, for, match, return/break/continue, print, spawn, defer, select. Generics: [T: A + B] on fn/struct/enum/spec/impl. Null-safety: T?, nil, ?, ??, ?.. Concurrency: chan[T], <-, close, for v in ch, anon fn, wait_group. Run :exit to quit.
# zerg> :exit
```

<!-- markdownlint-enable MD013 -->

### Example version gating

Examples `02_variables.zg` through `13_asm.zg` carry a `# requires: vX.Y` first-line comment for the
version they need. The CLI inspects that comment before lexing the body and refuses to run anything
beyond the current toolchain version with a clean message:

```sh
./src/bootstrap/bin/zerg run examples/13_asm.zg
# zerg: examples/13_asm.zg requires v0.10 (current is v0.9)
```

The v0.0 examples (`00_nop.zg`, `01_hello.zg`) carry no `requires` line and continue to run. The
v0.2-tagged showcase examples in `examples/` (`02_variables.zg`, `09_struct.zg`, …) advertise the
broader v0.2 surface; they pass the version gate but still exercise features such as `set`/`map`
literals and `pub` that PLAN defers past v0.2 — those bodies will still error out at lex/parse/type
time. v0.2-tagged programs that today rely on `let ys := xs` re-reading the source list may also
hit the v0.3 borrow checker (composite bind is now a move); rewrite as `let ys := clone(xs)` to keep
both bindings live. `examples/10_specs.zg` advertises v0.4 but exercises generics
(`fn display[T: Printable](item: T)`) which now ship at v0.6 — it passes the gate and the
generic-fn surface is supported, though the example's body still relies on shapes outside the
v0.6 corpus and may error at parse time. `examples/08_imports.zg` advertises v0.5 and now passes
the version gate, but its body uses surface forms (bare `result := …` without `let`, plus a
missing sibling `math.zg` and an external git import) that are not supported at v0.5, so it still
errors at parse / module-load time. `examples/11_null_safety.zg` advertises v0.6 and now passes
the gate; the surface it exercises (`Result[T, E]`, `?` propagation, `T?` ≡ `Option[T]`, `nil`)
is supported, though the example's bare-variant constructors (`Ok(...)` without the `Result.`
qualifier) lie outside the v0.6 corpus and still error at parse time. `examples/12_concurrency.zg`
advertises v0.7 and now passes the gate; the surface it exercises (`chan[T]`, `spawn`, anon fn,
`select`, `defer`) is supported, though the example's body may still rely on shapes outside the
v0.7 corpus. The v0.8 stdlib surface (`import "std/io"`, `import "std/strings"`, `import "std/math"`,
`import "std/os"`) does not have a dedicated `examples/` entry — the authoritative coverage lives
in `test/v0_8/`. The v0.9 process surface (`os.argv`, `os.exit`, `std/time`) and the `never`
bottom type also have no dedicated `examples/` entry — coverage lives in `test/v0_9/`. The
authoritative parity corpora live at `src/bootstrap/test/v0_2/`, `src/bootstrap/test/v0_3/`,
`src/bootstrap/test/v0_4/`, `src/bootstrap/test/v0_5/`, `src/bootstrap/test/v0_6/`,
`src/bootstrap/test/v0_7/`, `src/bootstrap/test/v0_8/`, and `src/bootstrap/test/v0_9/`.

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
  enforcement, not the model. _(At v0.3 the implicit deep-copy on bind is gone — composites move
  on `let ys := xs` and the user opts back into the v0.2 shape with `let ys := clone(xs)`.)_
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

### Supported syntax at v0.3

v0.3 layers a borrow checker and list mutation on top of v0.2; everything in v0.0 / v0.1 / v0.2
still parses and runs (with the one caveat below). What's new:

- **Move on bind for composites.** `let ys := xs` (or `mut ys := xs`) now MOVES `xs` into `ys`.
  After the bind, `xs` is invalid for any subsequent use — reading, indexing, passing to a fn, or
  rebinding all reject at compile time with a precise diagnostic. Primitives (`int`, `float`,
  `bool`, `byte`, `rune`, `str`, enums) still copy; only `list[T]`, tuples, and structs move.
- **The user-visible rule, in one sentence.** _After you give a composite away, you can't use it
  again — but reading it doesn't give it away._ `print xs`, `len(xs)`, indexing `xs[i]`, slicing
  `xs[lo..hi]`, iteration `for x in xs { … }`, and _passing `xs` to a function_ are all reads.
  Function calls implicitly share-borrow composite arguments for the duration of the call; the
  caller retains ownership when the call returns, so `f(xs); print xs[0]` is fine.
- **List mutation through `mut` bindings.** `mut`-bound lists support two new write forms:
  - `xs[i] = v` — replace element at index `i`. Bounds-checked at run time; v's type must match
    the element type. `xs[i] OP= v` (compound) is rejected at v0.3.
  - `push(xs, v)` — append. Built-in fn (function-call syntax, not method-call). Mutates `xs` in
    place; cap doubles on growth so amortised append is O(1).
    Both require `xs` to be a top-level `mut`-bound list. List mutation through fn parameters needs
    `&mut` references and is deferred — inside a fn body, composite parameters are implicitly
    shared-borrowed and cannot be mutated. Build via return-and-rebind: `mut xs := add_some(xs)`.
- **`clone(xs)` opts back into v0.2 deep-copy semantics.** Built-in fn; receiver is read (not
  moved); returns a fresh deep copy with no shared state. Reuses the per-shape `_copy` helpers
  the v0.2 codegen emitted — they don't go away, they become opt-in.
- **Borrow checker rejects common bugs at compile time.** Use-after-move, aliased mutation
  through fn-call observation, moves out of an outer binding inside a `for` body, and any branch
  that disagrees on whether a binding was moved (every branch of an `if` / `elif` / `else` must
  agree, otherwise you get a hard error pointing at the disagreeing branches).
- **Move-out sites.** A composite is given away by: `let y := x` / `mut y := x`, `return x`,
  inclusion in a struct literal `Point { xs: x }`, inclusion in a tuple literal `(x, …)`,
  inclusion in a list literal `[x, …]`, a bind-arm match `match xs { ys => … }`, and tuple
  destructure `let (a, b) := pair`.
- **Read sites.** `print x`, `len(x)`, `x[i]`, `x.field`, slice `x[lo..hi]`, `for v in x`, every
  fn call, and the match scrutinee (under shared borrow for the duration of the match — moved
  iff some arm bound the scrutinee whole).
- **Out of scope at v0.3, deferred.** Explicit `&` / `&mut` reference syntax (PLAN defers until
  ergonomics demand it), `&mut T` parameters and fn-param mutation, structural composite `==`
  (deferred to v0.4 with a `Comparable` spec), method-call syntax `xs.push(v)` /
  `xs.clone()` (deferred to v0.4 with `impl`), `Drop` / destructors (the push grow path leaks the
  old buffer at v0.3 — bounded by the corpus), non-lexical lifetimes (NLL), generics, and lifetime
  parameters.

A small v0.3 sample — list mutation:

```zerg
mut xs := [1, 2]
push(xs, 3)
xs[0] = 99
print xs
print len(xs)
# [ 99, 2, 3 ]
# 3
```

`clone()` opts back into v0.2-style deep copy, so the original stays usable:

```zerg
let xs := [1, 2, 3]
let ys := clone(xs)
print xs
print ys
# [ 1, 2, 3 ]
# [ 1, 2, 3 ]
```

Function calls observe a list without consuming it — the caller still owns it after the call:

```zerg
fn first(xs: list[int]) -> int {
    return xs[0]
}

let xs := [10, 20, 30]
print first(xs)
print xs[0]
# 10
# 10
```

Programs that _need_ the v0.2 shape (re-read after rebind) get a precise diagnostic and the
two-character fix is to wrap the source in `clone(…)`:

```zerg
let xs := [1, 2, 3]
let ys := xs
print xs[0]
# borrow error at 3:7: use of moved value: "xs" (moved at 2:11)
```

### Supported syntax at v0.4

v0.4 layers polymorphism on top of v0.3; everything in v0.0 / v0.1 / v0.2 / v0.3 still parses and
runs. What's new:

- **Inherent `impl` blocks.** `impl Counter { fn double() -> int { return this.count * 2 } }`
  attaches methods to a struct or enum without claiming spec conformance. Multiple inherent blocks
  for the same type aggregate. Inside the body, `this` is an implicit `BorrowedShared` local of
  the receiver type; reading `this.field` is fine, reassigning `this` or moving it out is not.
- **Specs.** `spec Printable { fn to_string() -> str }` declares a contract. Empty bodies are
  allowed (marker spec). Method signatures (no body) are required to be implemented; `fn` items
  with a body in the spec are **default impls** any conforming type inherits unless it overrides.
- **Spec `impl` blocks.** `impl Counter for Printable { fn to_string() -> str { … } }`. The
  receiver type must be a struct or enum — primitives reject (`<int>` cannot impl spec at v0.4).
  An empty body inherits the spec's defaults; methods with no override and no default raise
  `NotImplemented` at runtime when called. Two `impl T for Spec` blocks for the same `(T, Spec)`
  pair reject at typeck.
- **Method-call syntax.** `c.method()` and `c.method(args)`. Field access (`c.x`) keeps its
  existing meaning when no `(` follows. v0.3's `push(xs, v)` and `clone(xs)` builtins gain method
  forms (`xs.push(v)` / `xs.clone()`); both syntaxes accepted. Inherent-vs-spec and
  inherent-vs-inherent collisions on the same type reject with a precise diagnostic; cross-spec
  same-named methods are admitted and disambiguated by the receiver type at the call site.
- **Specs as types — runtime polymorphism.** `let x: Printable = c` constructs a fat pointer
  `(data, vt)` and `x.method(args)` dispatches through the vtable. `list[Printable]` holds mixed
  concrete types as long as each impls Printable. Coercion **heap-boxes** the source value
  (`malloc(sizeof(T))` + move), so the wrapper's data pointer never aliases stack storage —
  meaning **a fn may return a spec-typed value** (the original PLAN had this as a deferred
  feature; the heap-box decision admits it at v0.4). Trade-off: every spec-coercion site is one
  allocation. `==` on two spec-typed bindings is rejected (`Comparable` spec needs generics —
  defers to v0.6).
- **Enum payloads.** `enum Token { Eof, Ident(str), Number(int, int) }`. Construction:
  `Token.Ident("foo")`, `Token.Number(10, 16)`, `Token.Eof` (no parens for empty payload).
  Pattern matching binds payload positions: `Token.Ident(name) => print name`. C lowering is
  `tag + union`. Print format: `Token.Ident(foo)` / `Token.Number(10, 16)` for non-empty
  payloads; `Token.Eof` for empty. Recursive enums (`enum Tree { Node(Tree, Tree) }`) reject at
  typeck — heap-boxed recursive payloads defer to v0.6+.
- **Composite ==.** `==` and `!=` on lists, tuples, structs, and enums (with or without payloads)
  auto-derive structural equality via per-shape `_eq` helpers. List equality is length-then-elem;
  struct equality is per-field in declaration order; enum equality is same-tag-then-payload.
  Recursion bottoms out at the v0.1 primitive comparisons.
- **Errors deferred to v0.6.** v0.4 ships polymorphism but **not** the `Option[T]` / `Result[T,
E]` / `?`-propagation half of the "polymorphism and errors" roadmap line — those need generics,
  which arrive at v0.6. At v0.4 the user hand-rolls error types via enum payloads + `match`. This
  is intentional; the trade-off is the price of cutting v0.4 free of generics.
- **Out of scope at v0.4, deferred.** Generic fns / specs / multi-bound (`fn f[T: Printable]`),
  `Option` / `Result` / `?`, lambdas / closures, method visibility (`pub fn`), `mut` methods that
  reassign `this` (needs `&mut`), composite ordering (`<`, `>`), spec inheritance (permanent —
  flat by design), and borrowed (non-boxing) spec coercions (`&dyn Spec`).

A small v0.4 sample — spec, impl, method call, vtable dispatch through `list[Printable]`:

```zerg
spec Printable {
    fn to_string() -> str
}

struct Counter { count: int }
struct Tag { name: str }

impl Counter for Printable {
    fn to_string() -> str {
        return "Counter " + str(this.count)
    }
}

impl Tag for Printable {
    fn to_string() -> str {
        return "Tag " + this.name
    }
}

let items: list[Printable] = [Counter { count: 7 }, Tag { name: "alpha" }]
for it in items {
    print it.to_string()
}
# Counter 7
# Tag alpha
```

Composite `==`:

```zerg
struct Point { x: int, y: int }
print Point { x: 1, y: 2 } == Point { x: 1, y: 2 }
print [1, 2, 3] == [1, 2, 3]
print (1, "a") == (1, "a")
# true
# true
# true
```

Enum payloads + match-bind:

```zerg
enum Token { Eof, Ident(str), Number(int) }

fn show(tok: Token) {
    match tok {
        Token.Eof => print "<eof>"
        Token.Ident(name) => print name
        Token.Number(n) => print n
    }
}

show(Token.Ident("hello"))
show(Token.Number(42))
show(Token.Eof)
# hello
# 42
# <eof>
```

### Supported syntax at v0.5

v0.5 layers modules on top of v0.4; everything in v0.0 / v0.1 / v0.2 / v0.3 / v0.4 still parses
and runs. Single-file programs without `import` and without `pub` are unchanged. What's new:

- **`pub` visibility modifier.** Top-level decls are private to their module unless prefixed with
  `pub`. Applies to `fn`, `struct`, `enum`, `spec`, and methods inside an `impl` block. Field-level
  visibility on struct/enum bodies is decl-level only at v0.5 — there is no `pub` on individual
  fields. Cross-module access to a non-`pub` decl rejects at typeck.
- **`import "name"` statement.** Appears at the top of a file (after any shebang and the
  `requires:` line, before any decl). Resolves `"name"` to a sibling `.zg` file in the same
  directory as the importing file (`./name.zg`). The imported module's `pub` decls become
  accessible via `name.member`.
- **`import "name" as alias`.** Bind the imported module to a different local name.
- **`import (...)` grouped form.** Multiple imports in one block; desugared to one `ImportDecl`
  per entry with the same resolution rules.
- **Cross-module member access via dot.** `name.foo()`, `name.MyStruct { ... }`,
  `name.Color.Red`, `name.Token.Ident("x")`, and `name.MySpec` as a type all work.
- **Cycle detection.** A→B→A and longer cycles reject at module-load time with a path-listing
  diagnostic.
- **Cross-module impls and the orphan rule.** `impl LocalType for OtherModule.SomeSpec` and
  `impl OtherModule.SomeType for LocalSpec` are admitted; `impl A.SomeType for B.SomeSpec` (both
  foreign) rejects at typeck with the cross-module orphan diagnostic.
- **Reserved-name rejection.** `import "<keyword>"` (no alias) and `import ... as <keyword>`
  reject at parse time against the same keyword set the lexer recognises, so a future keyword
  automatically tightens the rule.
- **`requires:` per imported module.** Every imported module's `# requires:` line is checked
  against the toolchain version exactly as the entry file's is. A v0.5 entry that imports a
  v0.6 module rejects at module-load time with the standard `requires` diagnostic, naming the
  offending file.
- **Module-mangled codegen.** Build emits a single translation unit with every type/fn/method
  symbol prefixed by a per-module mangle (`<basename>_h<hex8>` derived from the canonical
  resolution path). Hyphens, leading digits, and non-ASCII basenames are handled deterministically
  via FNV-1a hashing; the `test/v0_5/15_mangler_lock` corpus entry locks this behaviour in.
- **REPL: imports rejected.** Typing `import "x"` at the REPL emits the dedicated diagnostic
  `import not supported at REPL` and the session keeps running. Multi-module REPL is deferred
  past v0.5.
- **Out of scope at v0.5, deferred.** External git imports (`"github.com/user/repo"`), submodule
  paths (`import "a/b/c"`), re-exports (`pub use` / `pub import`), `pub` on individual struct
  or enum fields, conditional / version-gated imports, `pub(crate)`-style restricted visibility,
  and stdlib content. Built-ins (`print`, `len`, `clone`, `push`) stay built-in; a real stdlib
  lands at v0.8.

A small v0.5 sample — sibling `greet.zg` providing a `pub` fn, consumed via `import`:

```zerg
# greet.zg
pub fn hello(name: str) -> str {
    return "hello, " + name
}
```

```zerg
# main.zg
import "greet"

print greet.hello("world")
# hello, world
```

The grouped form plus an alias:

```zerg
import (
    "greet"
    "math" as m
)

print greet.hello("zerg")
print m.square(7)
```

Cross-module impl via the orphan rule (`LocalType for OtherModule.Spec` is admitted):

```zerg
import "shapes"

struct Counter { count: int }

impl Counter for shapes.Printable {
    fn to_string() -> str {
        return "Counter " + str(this.count)
    }
}

let c := Counter { count: 7 }
print c.to_string()
# Counter 7
```

### Supported syntax at v0.6

v0.6 layers generics and null-safety on top of v0.5; everything in v0.0 / v0.1 / v0.2 / v0.3 / v0.4
/ v0.5 still parses and runs. Single-file programs that don't use type-params, `Option`, `Result`,
`T?`, `nil`, `?`, `??`, or `?.` are unchanged. What's new:

- **Generic type parameters.** `[T]`, `[T: Spec]`, and multi-bound `[T: A + B]` are admitted on
  `fn`, `struct`, `enum`, `spec`, and `impl` declarations. `fn id[T](x: T) -> T`, `struct Box[T] {
value: T }`, `enum Pair[A, B] { Both(A, B), Left(A), Right(B) }`, and
  `spec Iterator[T] { fn next() -> T? }` all type-check; instantiation is by structural
  canonicalisation, so `Box[int]` and `Pair[int, str]` are interchangeable everywhere a type is
  expected.
- **Bidirectional type inference at call sites.** `id(42)` infers `T = int` from the argument;
  `let x: int? = nil` propagates the binding's annotation into the inferer so `nil` lands as
  `Option[int].None`. Inference draws from arg shapes and from the surrounding _expected_ type
  (let annotation, return type, fn-arg slot, list-element type, struct-field type). Explicit
  type-args at call sites (`id[int](42)`) are not parsed at v0.6 — every generic call is inferred.
- **Built-in `Option[T]` and `Result[T, E]`.** Compiler-synthesised at typeck time and visible
  from every module without an `import`. Shapes: `enum Option[T] { Some(T), None }` and
  `enum Result[T, E] { Ok(T), Err(E) }`. User redecls of either name reject at typeck with the
  reserved-name diagnostic.
- **`T?` ≡ `Option[T]` everywhere.** Postfix `?` on any type position desugars to `Option[T]`;
  the two spellings are interchangeable. `T??` rejects at parse time.
- **Symmetric `T → T?` lift.** Wherever a `T?` is expected and a bare `T` is supplied — fn-arg,
  let-init, return expr, struct field, list element under a `list[T?]` annotation — the compiler
  implicitly wraps as `Some(value)`. The lift is **boundary-only**: a sub-expression with no
  expected type doesn't lift, so the user keeps full control inside arithmetic / call chains.
- **`nil` literal.** New keyword. Resolves to `Option[T].None` for the contextually-inferred `T`.
  Outside an inferable position (`print nil`, `let x := nil`) it is rejected with
  `cannot infer type of nil — annotate the binding`.
- **`?` propagation operator.** Postfix on a `Result[T, E]` or `Option[T]` expression. Legal only
  inside a fn whose return type is `Result[U, E]` (matching `E`) or `Option[U]`. Lowers to: if
  Err / None, early-return that Err / None; else evaluate to the inner `T`.
- **`??` nil-coalesce.** `lhs ?? rhs` where `lhs: Option[T]` or `Result[T, E]` and `rhs: T` —
  yields `T`. RHS is evaluated only on None / Err; LHS is read once.
- **`?.` safe navigation.** `obj?.field` where `obj: T?` returns `Option[U]` for `U` the field's
  type on `T`. Chains: `obj?.a?.b` carries `None` forward end-to-end.
- **Generic `impl` blocks.** `impl[T] Box[T] for Tagged { … }` admits one impl block that covers
  every monomorphisation of `Box[T]`. The orphan rule still applies (one of `(Type, Spec)` must
  be local). Concrete-arg impls (`impl Box[int] for Tagged`) are also admitted.
- **Print parity for Option / Result.** `print Option[int].Some(7)` → `Option.Some(7)`;
  `print Option[int].None` → `Option.None`; `Result` analogous. The bracketed type-arg suffix is
  intentionally suppressed in print so golden files stay stable across re-monomorphisation; it
  still appears in diagnostics, where disambiguation matters.
- **Out of scope at v0.6, deferred.** Explicit type-arg syntax at call sites
  (`fn[T] -> ...; f[int](42)`), variance / where-clauses / associated types, spec inheritance
  (permanent — flat by design), `try` / `except` / `raise`, lazy `??` short-circuit on non-Option
  / non-Result expressions, generic existentials (`list[Box[Printable]]` mixing `Box[int]` and
  `Box[str]`), `pub` on individual fields, re-exports, and external git imports (carry-over from
  v0.5). Built-ins (`print`, `len`, `clone`, `push`) stay built-in; a real stdlib lands at v0.8.

A small v0.6 sample — generic fn with a spec bound, plus the `T?` / `nil` / `??` surface:

```zerg
spec Printable {
    fn label() -> str
}

struct Cat { name: str }

impl Cat for Printable {
    pub fn label() -> str {
        return "Cat: " + this.name
    }
}

fn announce[T: Printable](x: T) -> str {
    return x.label()
}

let c := Cat { name: "Mittens" }
print announce(c)
# Cat: Mittens

let a: int? = 7
let b: int? = nil
print a ?? 0
print b ?? 99
# 7
# 99
```

Built-in `Result[T, E]` with `?` propagation:

```zerg
fn parse(input: str) -> Result[int, str] {
    if input == "good" {
        return Result.Ok(42)
    }
    return Result.Err("bad input")
}

fn process(input: str) -> Result[int, str] {
    let v := parse(input)?
    return Result.Ok(v + 1)
}

print process("good")
print process("nope")
# Result.Ok(43)
# Result.Err(bad input)
```

Generic `impl` block that covers every `Box[T]` instantiation:

```zerg
spec Tagged {
    fn tag() -> str
}

struct Box[T] { value: T }

impl[T] Box[T] for Tagged {
    pub fn tag() -> str {
        return "Box"
    }
}

let bi: Box[int] = Box { value: 7 }
let bs: Box[str] = Box { value: "hi" }
print bi.tag()
print bs.tag()
# Box
# Box
```

### Supported syntax at v0.7

v0.7 layers a concurrency runtime on top of v0.6; everything in v0.0 / v0.1 / v0.2 / v0.3 / v0.4 /
v0.5 / v0.6 still parses and runs. Single-file programs that don't use `chan`, `spawn`, anon fn,
`select`, `defer`, or `wait_group` are unchanged. What's new:

- **`chan[T]` channels.** Built-in generic type. `chan[T]()` is unbuffered (rendezvous handshake);
  `chan[T](N)` is buffered (FIFO, capacity N). Send blocks when the buffer is full (or always, for
  unbuffered, until a matching receive); receive blocks when the buffer is empty (or always, for
  unbuffered, until a matching send). Channels are not `Printable` — `print ch` rejects at typeck.
- **Send `ch <- v`.** Statement form. Moves `v` into the channel; the sender no longer owns `v`
  after the send. Sending into a closed channel panics, matching Go semantics.
- **Receive `<- ch`.** Prefix-unary expression. Yields `Option[T]` so user code can match on
  drain-after-close: an open channel produces `Option.Some(v)` (blocking until a value arrives or
  the channel closes); a drained closed channel produces `Option.None`.
- **`close(ch)`.** Built-in fn. Once closed, sends panic; receivers drain remaining buffered values
  in FIFO order, then see `None`. Closing an already-closed channel panics.
- **`for v in ch`.** Iterator form. Receives until close; binds `v` to the inner `T` (the loop
  unwraps the `Option` for the user). Desugars at typeck onto the v0.6 `Option[T]` machinery.
- **`spawn fn-call`.** Statement form. Starts a fire-and-forget concurrent task. The argument must
  be a fn-call expression (named fn or IIFE anon fn). No return value.
- **Anon fn `fn(params) -> R { body }`.** Expression form. Captures only **immutable** outer
  bindings — `mut` capture rejects at typeck. Captured composites are deep-copied at closure
  creation via the same per-shape `_clone` helpers v0.3 introduced; primitives copy by value. Each
  capture is independent, so mutations to the original after closure creation don't reach the
  closure. Often invoked immediately (`fn() { ... }()`) or passed to `spawn`.
- **`defer expr` / `defer block`.** Registers a statement (or block) to run at fn-body exit, in
  **LIFO order**. v0.7 admits `defer` only at fn-body scope (not inside `if` / `for` / `match` /
  other nested blocks). The defer stack is drained on every exit path including the v0.6 `?`
  early-return propagation. `defer` at the REPL rejects (no enclosing fn).
- **`select { ... }`.** Multiplexed channel wait. Each arm is one channel op + a body:
  - `v := <- ch -> { body }` — recv-bind; `v` scopes to the arm body only.
  - `<- ch -> { body }` — recv-discard.
  - `ch <- expr -> { body }` — send arm.
  - `_ -> { body }` — default arm; makes the select non-blocking when no other arm is ready.
    Empty `select` rejects at parse time. On ties (multiple arms ready), the codegen path picks the
    first ready arm in declaration order while the interpreter picks at random; both are correct
    because well-formed programs must not depend on tie-break order. v0.8+ may unify on Go's
    randomised tie-break once non-deterministic goldens are admitted across the board.
- **`wait_group` builtin.** Minimal coordination primitive for fan-in. Surface:
  `wg := wait_group()` constructs the handle; `wg.add(n)` increments the counter (call before
  spawning); `wg.done()` decrements (call from the spawned task); `wg.wait()` blocks until the
  counter is zero. Lets multiple senders share a channel and have one closer call `close(ch)` after
  `wg.wait()`.
- **Borrow rules for concurrency.** Send moves the value into the channel. Receive moves the value
  out. Spawn captures only immutable bindings; the deep-copy happens at the spawn site so the
  spawned task and the parent never alias. The channel handle itself is shared — both sides keep a
  handle across send/recv.
- **Out of scope at v0.7, deferred.** General `sync[T]` mutex-protected shared value (defer to v0.8
  stdlib — `wait_group` is the only coordination primitive shipping at v0.7). Lambda `|x| x * 2`
  syntax (anon fn covers the surface). Cancellation contexts and timeouts (need a stdlib
  `sleep` / deadline). `defer` in nested blocks (only fn-body-level admitted at v0.7). Channel
  direction marks `chan<-[T]` / `<-chan[T]` (defer to v0.8 with stdlib). `raise` / `try` / `except`
  and a spec `Exception` (grammar reserves them; v0.7 does not parse them).

A small v0.7 sample — fan-in via channel + wait_group:

```zerg
fn worker(id: int, ch: chan[int], wg: WaitGroup) {
    defer wg.done()
    ch <- id * 10
}

let ch := chan[int](3)
let wg := wait_group()
wg.add(3)
spawn worker(1, ch, wg)
spawn worker(2, ch, wg)
spawn worker(3, ch, wg)

spawn fn() {
    wg.wait()
    close(ch)
}()

mut total := 0
for v in ch {
    total = total + v
}
print total
# 60
```

Anon fn capturing an immutable composite (deep-copied at creation) plus a `select` with default:

```zerg
let nums := [1, 2, 3]
let ch := chan[int](1)

spawn fn() {
    let snapshot := nums
    ch <- len(snapshot)
}()

select {
    v := <- ch -> { print v }
    _ -> { print "would block" }
}
# 3
```

`defer` runs in LIFO order at fn-body exit, including the `?` early-return path inherited from
v0.6:

```zerg
fn body() -> Result[int, str] {
    defer print "first deferred"
    defer print "second deferred"
    return Result.Ok(7)
}

print body()
# second deferred
# first deferred
# Result.Ok(7)
```

### Supported syntax at v0.8

v0.8 layers a focused standard library on top of v0.7; everything in v0.0 / v0.1 / v0.2 / v0.3 /
v0.4 / v0.5 / v0.6 / v0.7 still parses and runs. Single-file programs that don't `import "std/..."`
are unchanged. What's new:

- **Four toolchain-shipped stdlib modules.** `std/io`, `std/strings`, `std/math`, `std/os`. Their
  source ships embedded in the toolchain binary (Go `embed.FS`); `import "std/<m>"` resolves against
  the embed first and **does not** fall through to the working directory. A user-loaded
  `import "std/io"` that doesn't resolve to an embedded module rejects with a clear "stdlib module
  not found" diagnostic — there is no shadowing.
- **Typed errors, not strings.** Fallible calls return a `Result[T, IoError]` or
  `Result[T, ParseError]` enum-typed error so the parity surface doesn't depend on host-OS error
  text. Both halves bucket host errors into the same enum variants.
  - `IoError { NotFound, PermissionDenied, AlreadyExists, InvalidPath, Other }`
  - `ParseError { Empty, InvalidDigit, Overflow }`
- **`std/io` (2 fns).** `read_file(path: str) -> Result[str, IoError]` reads the whole file (UTF-8).
  `write_file(path: str, content: str) -> Result[bool, IoError]` truncates and writes; returns
  `Ok(true)` on success.
- **`std/strings` (10 fns).** `split(s, sep) -> list[str]`; `join(parts, sep) -> str`; `trim(s) -> str`
  (ASCII whitespace, both ends); `starts_with`, `ends_with`, `contains -> bool`;
  `replace(s, old, new) -> str` (all occurrences, non-overlapping, left-to-right);
  `to_upper`, `to_lower -> str` (ASCII only); `parse_int(s) -> Result[int, ParseError]`
  (base 10, leading `+`/`-` allowed, surrounding whitespace trimmed). Empty-sep `split(s, "")` is
  rejected at runtime in both halves.
- **`std/math` (4 fns).** `abs`, `min`, `max`, `gcd` — all over `int`. `gcd(0, 0) == 0`.
- **`std/os` (1 fn).** `env(name: str) -> Option[str]` — `Some(value)` when set, `None` when unset.
  Read-only.
- **`__builtin <ident>` fn-decl marker.** Stdlib `.zg` files terminate fn declarations with
  `__builtin <name>` instead of a `{ body }`; the parser admits the marker only inside files whose
  path begins with `std/` in the embedded FS, and only when their `# requires:` is `>= v0.8`. User
  code that writes `__builtin` is rejected at typeck — the marker is reserved for the toolchain.
- **Provisional surface through v1.0.** Stdlib fn signatures may change before v1.0 source
  stability lands.
- **Out of scope at v0.8, deferred.** `os.argv` and `os.exit` (need the `never` / bottom type and
  a main-signature change), `time.now` / `sleep` / deadlines, floating-point math (Zerg has no
  float type yet), `pow` / `sqrt`, regex, JSON, networking, path manipulation (`path.join` /
  `path.dir`), stdin streaming (`io.read_line`), random.

A small v0.8 sample — read a file, split on lines, count non-empty entries:

```zerg
import "std/io"
import "std/strings"

match io.read_file("notes.txt") {
    Result.Ok(content) => {
        let lines := strings.split(content, "\n")
        mut count := 0
        for line in lines {
            if strings.trim(line) != "" {
                count = count + 1
            }
        }
        print count
    }
    Result.Err(IoError.NotFound) => print "no file"
    Result.Err(_) => print "io error"
}
```

`std/strings` joining and case mapping:

```zerg
import "std/strings"

print strings.join(["zerg", "is", "fast"], " ")
print strings.to_upper("hello")
print strings.replace("aaa", "a", "b")
# zerg is fast
# HELLO
# bbb
```

`std/math` integer helpers:

```zerg
import "std/math"

print math.abs(-7)
print math.min(3, 8)
print math.max(3, 8)
print math.gcd(12, 18)
# 7
# 3
# 8
# 6
```

`std/os` environment lookup:

```zerg
import "std/os"

match os.env("ZERG_GREETING") {
    Option.Some(g) => print g
    Option.None => print "default"
}
```

`std/strings.parse_int` returning a typed error:

```zerg
import "std/strings"

print strings.parse_int("42")
print strings.parse_int("")
print strings.parse_int("12x")
# Result.Ok(42)
# Result.Err(ParseError.Empty)
# Result.Err(ParseError.InvalidDigit)
```

### Supported syntax at v0.9

v0.9 layers a process surface (`os.argv`, `os.exit`), a minimum time module (`std/time`), and the
`never` bottom type on top of v0.8; everything in v0.0 / v0.1 / v0.2 / v0.3 / v0.4 / v0.5 / v0.6
/ v0.7 / v0.8 still parses and runs. Single-file programs that don't use `never`, `os.argv`,
`os.exit`, or `std/time` are unchanged. What's new:

- **`never` bottom type.** A type with no values; subtype of every other type. A fn declared
  `-> never` cannot return — every code path must diverge (call another `-> never` fn, infinite
  loop). At a call site, a `-> never` call typechecks as any expected type, so
  `let x: int = exit(1)` and a `match` arm whose body is `exit(1)` are both well-formed. Match
  exhaustiveness treats a `-> never` arm as diverging (same machinery as v0.7 select). The IDENT
  `never` is recognized only at type position; user-declared `struct never` / `enum never` /
  `let never := …` reject at typeck.
- **`os.argv() -> list[str]`.** Returns the program's command-line arguments. Index 0 is the
  executable / source path under `zerg run` and `zerg build`-then-execute, and the literal string
  `"<repl>"` under the REPL. **Parity rule for tests:** corpus programs MUST NOT print `argv[0]`
  directly — the cgen-built binary is launched from a tempdir with an OS-assigned path, while the
  interpreter half receives whatever the harness sets. Print `argv[1:]` or specific indices `> 0`
  only.
- **`os.exit(code: int) -> never`.** Terminates the process with the given exit code. Cannot fall
  through (the return type is `never`). The REPL surfaces "process exited with code N" but does
  not terminate the host Go process.
- **Defer × exit deviation (intentional).** `os.exit()` does **not** drain defers and does **not**
  join spawned tasks. This is an explicit deviation from v0.7's "defers drain on every exit path
  including `?`-propagation" contract — it matches Go's `os.Exit` semantics. User code that needs
  cleanup before exit must call `wg.wait()` and run any cleanup explicitly before `exit()`.
- **`std/time` (2 fns).** `now_ms() -> int` returns milliseconds since the **first call to
  `now_ms()` in the running program** — the first call returns `0` on both halves, captured
  lazily, and subsequent calls return ms-since-first-call. Both halves agree on the absolute
  return value of every call by call-index, so corpus tests can `print time.now_ms()` without
  parity violation. `sleep_ms(ms: int) -> bool` blocks at least `ms` milliseconds and always
  returns `true`; negative `ms` clamps to `0`. POSIX-only — `clock_gettime(CLOCK_MONOTONIC)` and
  `nanosleep` are required.
- **Harness `argv:` manifest grammar.** Per-program manifest gains `argv: a b c` — a
  whitespace-separated list of args after argv[0]; argv[0] is auto-set by the harness.
- **Out of scope at v0.9, deferred to v0.10+.** `zerg fmt` (canonical formatter), `zerg lint`,
  `zerg debug` / debug-info / profiling, cancellation contexts and deadlines, signal handlers,
  stdin streaming (`os.stdin_read_line`), wall-clock formatting and a `Duration` type, float-typed
  time (Zerg has no float-typed stdlib surface yet). The roadmap line for v0.9 still reads
  "developer tooling" — fmt / lint / debug land in v0.10 alongside the language reference; v0.9
  ships the process surface and time primitives needed to write CLI programs end-to-end.

A small v0.9 sample — `never` and `os.exit` together for a precondition failure:

```zerg
import "std/os"

fn require_positive(n: int) -> never {
    if n <= 0 {
        print "expected positive int"
        os.exit(2)
    }
    os.exit(0)
}

require_positive(-1)
# expected positive int
```

`os.argv` printing the arg count and the args after argv[0]:

```zerg
import "std/os"

let args := os.argv()
print len(args)
for i in 1..len(args) {
    print args[i]
}
```

`std/time` measuring an interval; the first `now_ms()` call always returns 0 so both halves
agree:

```zerg
import "std/time"

let t0 := time.now_ms()
time.sleep_ms(50)
let t1 := time.now_ms()
print t0
print t1 >= 30
# 0
# true
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
| enum    | `Name.VariantName` (bare) / `Name.VariantName(p1, p2)` (payload, comma+space between)   |

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
`match: no arm matched` stderr line. v0.3 adds a 15-program corpus at `src/bootstrap/test/v0_3/`
covering the move-on-bind rule, `clone()` and `push()` builtins, list-index assignment, fn-call
implicit shared borrow, branch-join agreement, and the loop-body move rejection — split into
"success" programs paired with `.txt` golden output and "reject" programs paired with `.err` files
that pin the borrow-check diagnostic. v0.4 adds a 25-program corpus at `src/bootstrap/test/v0_4/`
covering inherent and spec impls, method-call syntax, `this`, default impls, vtable dispatch
(spec-as-type, `list[Printable]`, struct field of spec type), enum payloads (construction, match
bind, nested), structural `==` on lists / tuples / structs / enums, `xs.push(v)` / `xs.clone()`
method form, the runtime `NotImplemented` panic (`.notimpl` golden), and the recursive-enum typeck
reject (`rejects/`). v0.5 adds a multi-file corpus at `src/bootstrap/test/v0_5/` — each entry is a
subdirectory with a `main.zg` plus sibling files plus an `expected.txt`, covering basic imports,
`pub` gating across modules, cross-module spec impls, enum bare/payload construction across
modules, aliased and grouped imports, diamond import graphs, same-named type collisions, the
mangler-lock case (a sibling whose basename has both a hyphen and a leading digit), and the
`rejects/` cases for the orphan rule and cycle detection. v0.6 adds a 15-program corpus at
`src/bootstrap/test/v0_6/` covering generic fns (id, spec-bound `announce[T: Printable]`, multi
dispatch), generic structs / enums with type-args, built-in `Option[T]` and `Result[T, E]`
construction and print, `?` propagation in `Result`-returning and `Option`-returning fns, `??`
coalesce on Option/Result, `?.` safe-navigation chains, the symmetric `T → T?` lift at every
boundary (let-init, fn-arg, return, struct field, list element), `impl[T] LocalType[T] for Spec`
generic-impl dispatch, cross-module generic instantiation, nested `Option[Result[…, …]]` shapes,
plus a `rejects/` set pinning unmet-bound, `?` outside a Result/Option fn, mismatched `E`,
unannotated `nil`, user redecl of `Option`, and the `T??` double-nullable parse reject. v0.7 adds
a 14-program corpus at `src/bootstrap/test/v0_7/` plus a 3-program scheduling subdir at
`src/bootstrap/test/v0_7/scheduling/`. The deterministic-stdout half covers unbuffered and
buffered send/recv, `close` + drain, anon-fn IIFE and capture, `defer` LIFO ordering, `defer` ×
`?` propagation, `wait_group` fan-in, `for v in ch`, and `select` arms (default, send, recv-bind);
the scheduling subdir asserts invariants for non-deterministic surface (select tie-break,
send-blocks-on-full-buffer). The `rejects/` set pins defer-at-non-fn-scope, spawn-of-non-fn-call,
mut-capture, send-into-closed, and defer-at-REPL. v0.8 adds a 25-program corpus at
`src/bootstrap/test/v0_8/` covering `std/io` (`read_file`, `write_file`, `IoError` match), `std/strings`
(`split`, `join`, `trim`, `starts_with`, `ends_with`, `contains`, `replace`, `to_upper`, `to_lower`,
`parse_int` with `Empty` / `InvalidDigit` / `Overflow` corner cases), `std/math` (`abs`, `min`, `max`,
`gcd`), `std/os` (`env` set / unset), an end-to-end pipeline, a cross-module import chain, and a
v0.7 regression that pins `__builtin` lexing as a normal identifier in non-stdlib files. The
`rejects/` set pins user-code `__builtin` use, runtime panic on empty-sep `split`, and missing-stdlib
import. The harness sets `ZERG_TEST_TEMPDIR` and provisions per-test fixture files via each entry's
`manifest.txt`. The README's parity rule —
**byte-identical for sequential code, equivalent under any valid scheduling for concurrent code** —
is unchanged; the `scheduling/` subdir formalises the second half. Run the full corpus with:

```sh
make test
```

## DDD (Dream-Driven Development)

This project is based on the DDD (dream-driven development) methodology which means
the project is based on what I dream of.

All the features are based on my needs and my dreams.

## Release notes

### v0.9 — process surface + time (centrepiece: `never`)

- **`never` bottom type.** A type that has no values and is a subtype of every concrete type. A fn
  declared `-> never` cannot return — every code path must diverge. A `-> never` call typechecks
  as any expected type, so `let x: int = exit(1)` and a `match` arm whose body tail-calls
  `exit(1)` are both well-formed. Match exhaustiveness extends the v0.7 select branch-merge to
  treat `-> never` arms as diverging. The IDENT `never` is contextual at type position; user
  redecls (`struct never`, `enum never`, `let never := …`) reject at typeck with a focused
  diagnostic.
- **`std/os` extended with the v0.8-deferred process surface.** `argv() -> list[str]` returns
  the program's command-line arguments; index 0 is the executable / source path under
  `zerg run` and `zerg build`-then-execute, and the literal string `"<repl>"` under the REPL.
  `exit(code: int) -> never` terminates the process with the given exit code. The REPL surfaces
  "process exited with code N" but does not terminate the host Go process.
- **Defer × exit is an intentional deviation from v0.7.** v0.7's contract was "defers drain on
  every exit path including `?`-propagation". v0.9's `os.exit()` is an **explicit deviation** —
  defers do **not** drain, spawned tasks do **not** join. This matches Go's `os.Exit` semantics.
  User code that needs cleanup before exit must call `wg.wait()` and run any cleanup explicitly
  before `exit()`. A corpus reject test pins this behaviour on both halves.
- **`std/time` minimum surface.** `now_ms() -> int` returns milliseconds since the **first call
  to `now_ms()` in the running program**, captured lazily at first call. By construction, the
  first call returns `0` on both halves and both halves agree on the absolute return value of
  every call by call-index — so corpus tests can `print time.now_ms()` without parity violation.
  Implementation: a process-global monotonic epoch initialised on first call (Go `time.Time` on
  the interpreter side, `struct timespec` on the cgen side). `sleep_ms(ms: int) -> bool` blocks
  at least `ms` milliseconds and returns `true`; negative `ms` clamps to `0` with no error.
  POSIX-only — the cgen path uses `clock_gettime(CLOCK_MONOTONIC)` and `nanosleep`, which match
  the existing `-pthread` constraint.
- **`os.argv[0]` parity rule.** Corpus programs MUST NOT print `argv[0]` directly — the
  cgen-built binary is launched from a tempdir with an OS-assigned path, while the interpreter
  half receives whatever the harness sets. Tests print `argv[1:]` or specific indices `> 0` only.
  The harness lints test programs and rejects any that contain `print argv[0]`.
- **Harness `argv:` manifest grammar.** Per-program manifest gains `argv: a b c` — a
  whitespace-separated list of args after argv[0]. Both halves receive the argv list from the
  harness; cgen's `main(int argc, char **argv)` accepts the kernel's argv, the interpreter's
  `RunBundleWithOptions` takes an `Argv []string`. The CLI driver passes argv as
  `[file_path, arg1, arg2, ...]` for `zerg run file.zg arg1 arg2`.
- **Build-side codegen gate.** The `int main(int, char**)` signature swap and the v0.9 globals
  emit only when the program references a v0.9 builtin (`os.argv`, `os.exit`, `time.now_ms`,
  `time.sleep_ms`); v0.0–v0.8 codegen output stays byte-identical to the pre-v0.9 baseline.
  Mirrors the v0.7 / v0.8 gate machinery.
- **Note: `zerg fmt`, `zerg lint`, and `zerg debug` are deferred to v0.10** alongside the
  language reference. The roadmap line for v0.9 still reads "developer tooling"; this release
  ships the process surface and time primitives needed to write CLI programs end-to-end, while
  the formatter / linter / debugger surface — each milestone-sized in its own right — lands in
  the next round.
- **Out of scope at v0.9, deferred.** Cancellation contexts / deadlines / signals;
  `os.stdin_read_line` and interactive stdin; wall-clock formatting (`time.now_iso`) and a
  `Duration` type; float-typed time (Zerg has no float type yet); `os.signal_handler` and
  process-control. v0.10+.
- **Backward compatibility.** Every v0.0–v0.8 corpus continues to pass under `make test`. The
  REPL banner bumps to v0.9 and adds "process surface, time" to the comma-separated surface list;
  `cliVersion` advances to `0.9.0`.

### v0.8 — standard library

- Four toolchain-shipped stdlib modules — `std/io`, `std/strings`, `std/math`, `std/os` — accessible
  via the v0.5 module surface (`import "std/io"`). Source `.zg` files for each module live embedded
  in the toolchain binary via Go `embed.FS`; the loader resolves `std/...` import paths against the
  embed and rejects with a "stdlib module not found" diagnostic on miss — there is **no
  working-directory fallback** for `std/...` paths.
- `std/io`: `read_file(path) -> Result[str, IoError]` (whole-file UTF-8 read) and
  `write_file(path, content) -> Result[bool, IoError]` (truncate + write).
- `std/strings`: `split`, `join`, `trim`, `starts_with`, `ends_with`, `contains`, `replace`,
  `to_upper`, `to_lower`, and `parse_int(s) -> Result[int, ParseError]`. ASCII-only case mapping.
  `replace` is non-overlapping left-to-right. Empty-sep `split` rejects at runtime in both halves.
- `std/math`: `abs`, `min`, `max`, `gcd` over `int`. `gcd(0, 0) == 0`. (Floats and `pow` / `sqrt`
  defer until Zerg admits a float type.)
- `std/os`: `env(name) -> Option[str]` for read-only environment lookup. (`os.argv` and `os.exit`
  defer to v0.9+ — they need the `never` type and a main-signature change.)
- Typed-error contract: fallible calls return enum-typed errors (`IoError` and `ParseError`), not
  strings, so the parity rule doesn't depend on host-OS error text. `IoError` variants are
  `NotFound` / `PermissionDenied` / `AlreadyExists` / `InvalidPath` / `Other`. `ParseError`
  variants are `Empty` / `InvalidDigit` / `Overflow`. Both halves bucket host errors into the same
  variant; unclassifiable cases bucket to `Other`.
- New fn-decl form: `fn name(params) -> R __builtin <ident>`. The trailing `__builtin <ident>`
  replaces the body. The bareword identifier is validated against a closed registry at typeck — a
  typo produces a "unknown builtin" compile error rather than a runtime surprise. The `__builtin`
  keyword lexes only inside files under `std/` in the embedded FS whose `# requires:` is `>= v0.8`;
  user code that writes `__builtin` is rejected.
- Build-side runtime: the embedded C prelude grows `zerg_io_*`, `zerg_strings_*`, `zerg_math_*`,
  and `zerg_os_*` runtime fns under a `programUsesV08` gate — they emit only when the program
  actually references a v0.8 builtin, so v0.0–v0.7 codegen output stays byte-identical to the
  pre-v0.8 baseline.
- The stdlib surface is **provisional** through v1.0. Fn signatures may change before source
  stability ships.
- Trust boundary: `read_file` / `write_file` operate on the host filesystem with no path sandbox —
  `read_file("../../etc/passwd")` works. Documented behaviour at v0.8; v0.9+ developer tooling may
  layer in capability scoping.
- Memory: `read_file` slurps the whole file into memory; no streaming or size cap at v0.8. Suitable
  for config / source files, not large blobs. Streaming may land at v0.9+.
- Backward compatibility: every v0.0–v0.7 corpus continues to pass under `make test`. Programs that
  used `__builtin` as a plain identifier in pre-v0.8 source continue to parse it as IDENT — the
  keyword is gated on `# requires: v0.8` AND on the file living under the embedded `std/` tree.

### v0.7 — concurrency runtime

- `chan[T]` built-in generic channel type. `chan[T]()` is unbuffered (rendezvous handshake);
  `chan[T](N)` is buffered (FIFO, capacity N). Send and receive are first-class on both halves —
  Go channels under the interpreter, a pthread + mutex + condvar + ring-buffer C runtime under
  the build path.
- Send `ch <- v` (statement) and receive `<- ch` (expression yielding `Option[T]`). Receiving from
  an open empty channel blocks; receiving from a drained closed channel produces `Option.None`.
- `close(ch)` builtin. Once closed, sends panic; receivers drain remaining buffered values in FIFO
  order and then see `None`. Closing twice panics.
- `for v in ch` iterator form. Loops until close; `v` binds to the inner `T`. Desugars onto the
  v0.6 `Option[T]` machinery at typeck.
- `spawn fn-call` statement starts a fire-and-forget concurrent task. Argument must be a fn-call
  (named fn or IIFE anon fn). Interpreter side: a Go goroutine. Build side: `pthread_create` with a
  capped stack size.
- Anon fn `fn(params) -> R { body }` as an expression — captures only **immutable** outer
  bindings; captured composites are deep-copied at closure creation via the v0.3 per-shape
  `_clone` helpers. `mut` capture rejects at typeck. Often invoked immediately or passed to
  `spawn`.
- `defer expr` / `defer block` registers a statement to run at fn-body exit in **LIFO order**. The
  defer stack drains on every exit path including the v0.6 `?` early-return propagation. v0.7
  admits `defer` only at fn-body scope; nested-block `defer` is deferred.
- `select { ... }` multiplexed channel wait. Arms: recv-bind `v := <- ch -> { ... }`, recv-discard
  `<- ch -> { ... }`, send `ch <- expr -> { ... }`, default `_ -> { ... }`. Empty `select` rejects
  at parse time. On ties, the codegen path picks first-ready in declaration order while the
  interpreter randomises; both are correct under the parity rule because well-formed programs must
  not depend on tie-break order.
- `wait_group` builtin for fan-in coordination. `wg := wait_group()` constructs the handle;
  `wg.add(n)` increments the counter, `wg.done()` decrements, `wg.wait()` blocks until zero.
  Interpreter uses `sync.WaitGroup`; codegen lowers to a mutex + condvar + counter.
- Borrow rules: send moves the value into the channel, receive moves it out. The channel handle
  itself is shared across both ends. Spawn captures only immutable bindings and deep-copies
  composites at the spawn site so the spawned task and the parent never alias.
- Parity rule extension: the byte-identical-stdout contract holds for every sequential program;
  for concurrent programs the contract becomes equivalent-under-any-valid-scheduling, formalised
  by the new `test/v0_7/scheduling/` invariant-asserting harness.
- Build-side runtime: the embedded C prelude grows a pthread-backed concurrency runtime —
  per-element-type `zerg_chan_<T>`, `zerg_select` array-of-cases helper, a per-thread defer stack,
  and `zerg_wait_group_t`. The build path adds `-pthread` to the cc flags. macOS and Linux both
  ship pthreads with the toolchain.
- Backward compatibility: every v0.0–v0.6 corpus continues to pass under `make test`. The v0.5
  module mangle and the v0.6 monomorphisation suffix both flow through unchanged for v0.7's new
  `chan[T]` and anon-fn-env types.

### v0.6 — generics and null-safety

- Generic type parameters `[T]`, `[T: Spec]`, and multi-bound `[T: A + B]` on `fn`, `struct`,
  `enum`, `spec`, and `impl` declarations — write a generic data structure or algorithm once and
  let the compiler monomorphise per use site.
- Built-in `Option[T]` and `Result[T, E]` available from every module without an `import`. User
  redecls of `Option` / `Result` reject with the reserved-name diagnostic.
- `T?` postfix sugar for `Option[T]`, accepted in every type position (params, returns, fields,
  type-args). `T??` rejects at parse time.
- `nil` literal that resolves to `Option[T].None` for the contextually-inferred `T`. Outside an
  inferable position, the typechecker says so explicitly.
- Symmetric `T → T?` lift at every boundary with a known expected type — fn-arg, let-init, return
  expr, struct field, list element. `let x: int? = 42` succeeds and lifts to `Some(42)`.
- `?` propagation operator: `parse(input)?` early-returns the `Err` / `None` and unwraps the
  inner value on the success path. Legal only inside fns whose return type is compatible
  `Result` / `Option`.
- `??` coalesce: `opt ?? default` yields the inner `T` on `Some` / `Ok`, the right-hand side on
  `None` / `Err`. Read once, lazy on the right.
- `?.` safe navigation: `obj?.field` carries `None` forward across chains, returning `Option[U]`
  for the field type `U` on `T`.
- Generic `impl` blocks: `impl[T] LocalType[T] for SomeSpec { ... }` covers every monomorphisation
  in one declaration; the orphan rule still applies.
- Bidirectional type inference at call sites — both arg shapes and the surrounding _expected_
  type (let annotation, return type, fn-arg slot, list-element type) feed the unifier. Explicit
  type-args at call sites are deferred past v0.6.
- Print parity: `Option.Some(7)` / `Option.None` / `Result.Ok(...)` / `Result.Err(...)` print
  without the bracketed type-arg suffix, so golden files stay stable across re-monomorphisation.
- Backward compatibility: every v0.0–v0.5 corpus continues to pass under `make test`. The v0.5
  module mangle still wraps every monomorphised symbol, so multi-file programs with generic
  instances link cleanly without a separate object-file step.
