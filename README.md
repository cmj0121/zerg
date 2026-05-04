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
- [ ] **v0.7** — concurrency runtime.
- [ ] **v0.8** — standard library.
- [ ] **v0.9** — developer tooling.
- [ ] **v0.10** — hardening and language reference.
- [ ] **v1.0** — source stability.

A phase ships when each example's stdout matches between `zerg run` and `zerg build` — byte-identical
for sequential code, equivalent under any valid scheduling for concurrent code.

## Building & running

The compiler is a Go program that interprets `.zg` source directly, or compiles it by emitting C and
shelling out to the system C compiler. v0.0 (toolchain bootstrap), v0.1 (procedural core), v0.2
(composite data), v0.3 (borrow checking), v0.4 (polymorphism), v0.5 (modules), and v0.6 (generics
and null-safety) share the same `zerg` binary; earlier examples (`00_nop.zg`, `01_hello.zg`, and
the v0.1 / v0.2 / v0.3 / v0.4 / v0.5 corpora) keep working unchanged.

### Prerequisites

- Go 1.23 or newer.
- A C compiler reachable as `cc` on `PATH` (override with `$CC`). It must accept `-fwrapv`; gcc and
  clang on macOS / Linux qualify. tcc and MSVC are out of scope at v0.6.
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
template machinery.

### REPL

The v0.6 REPL is multi-line: input accumulates until it parses cleanly, so you can paste a function
body, a `for` block, a `struct`/`enum`/`spec`/`impl` declaration, or a `match` one line at a time.
The borrow checker runs against each accepted program — diagnostics fire at the prompt, not the
next prompt. `import` is intentionally not admitted at the REPL: typing `import "x"` produces the
dedicated diagnostic `import not supported at REPL` and the session continues.

<!-- markdownlint-disable MD013 -->

```sh
./src/bootstrap/bin/zerg repl
# Zerg REPL v0.6 — accepts the v0.6 surface (procedural core, composite data, borrow checking, polymorphism, modules, generics, null-safety)
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
# zerg> :help
# Statements: let/mut/const, fn, struct/enum/spec/impl, if/elif/else, for, match, return/break/continue, print. Generics: [T: A + B] on fn/struct/enum/spec/impl. Null-safety: T?, nil, ?, ??, ?.. Run :exit to quit.
# zerg> :exit
```

<!-- markdownlint-enable MD013 -->

### Example version gating

Examples `02_variables.zg` through `13_asm.zg` carry a `# requires: vX.Y` first-line comment for the
version they need. The CLI inspects that comment before lexing the body and refuses to run anything
beyond the current toolchain version with a clean message:

```sh
./src/bootstrap/bin/zerg run examples/12_concurrency.zg
# zerg: examples/12_concurrency.zg requires v0.7 (current is v0.6)
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
qualifier) lie outside the v0.6 corpus and still error at parse time. The v0.6 surface is
exercised end-to-end by the `test/v0_6/` corpus. The authoritative parity corpora live at
`src/bootstrap/test/v0_2/`, `src/bootstrap/test/v0_3/`, `src/bootstrap/test/v0_4/`,
`src/bootstrap/test/v0_5/`, and `src/bootstrap/test/v0_6/`.

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
unannotated `nil`, user redecl of `Option`, and the `T??` double-nullable parse reject. Run the
full corpus with:

```sh
make test
```

## DDD (Dream-Driven Development)

This project is based on the DDD (dream-driven development) methodology which means
the project is based on what I dream of.

All the features are based on my needs and my dreams.

## Release notes

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
