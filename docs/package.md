# Zerg Modules, Packages & Programs

How Zerg source is organized, built, and started. This builds on the visibility, memory, spec, and
error models in the [Language Reference](language.md). Also in [繁體中文](package.zh-TW.md).

## Four layers

Source is organized in four nested roles — from the whole program down to a single file — each owning
one concern:

| Layer       | What it is                                  | The boundary it draws                                        |
| ----------- | ------------------------------------------- | ------------------------------------------------------------ |
| **program** | a build rooted at an entry file with `main` | the run — the root of the dependency graph                   |
| **package** | a tree of modules                           | the **distribution / dependency / version** and **API** unit |
| **module**  | a directory                                 | the default **privacy** and **namespace** unit               |
| **file**    | a physical slice of one module              | none — files in a module share one namespace                 |

A `module` handles encapsulation and naming; a `package` handles distribution and the external
contract. Keeping the two concerns in two layers is what gives `pub` a precise meaning.

### Programs & the entry point

A **program is a build**, not a special kind of package. The compiler is pointed at an **entry file** —
`zerg entry.zg` — and roots the build there, following its imports out across the dependency DAG.

- The entry filename is **not reserved**; the build invocation designates it. What the language
  requires is **content**: the entry file must define a top-level **`main`** entry function — its shape
  (inputs and result) reuses models already defined, just below.
- `main` is not `pub`, so it can never be imported — the "you cannot depend on a program" property
  falls out for free, with no special _binary package_ kind. **Every package is an importable
  library.**
- **Multiple executables** are just multiple entry files, each with its own `main`, each built by
  pointing the compiler at it.
- Keeping `main` a thin wiring layer is a **convention**, not a rule — logic you want to reuse or test
  must live in an importable package anyway, which naturally empties `main`.

`main`'s shape reuses models already defined:

- **Inputs.** The command-line arguments — the program's own interface — arrive as a **parameter**.
  Ambient OS facts that are read-only by nature (environment variables, clock, randomness) are reached
  through the stdlib, not the signature; they are not mutable global state.
- **Result.** `main` returns `Result[nil]`, so exit reuses the error model: a `Left` exits `0`, a
  `Right(err)` prints `err` to stderr and exits non-zero, and an uncaught **abort** unwinds the main
  stack and crashes. Expected failure (`Right`) and a bug (abort) stay two distinct exits, and `?`
  works directly in `main`.

### Program lifetime & top-level initialization

`main`'s body is the **root scope of the program**: when it returns, everything scope-owned beneath it
is freed and any still-running coroutine is abandoned where it stands (there is no join — drive a
coroutine to a channel-observed finish if it must complete first; see
[Coroutines & Channels](coroutine.md)).

Outside `main` lives only **immutable top-level state** — constants, functions, types, and specs —
readied before `main` runs. Top-level constants are initialized in **dependency order**; a cycle among
them is a compile error. There is **no `init()` hook** and **no mutable global**: shared mutable state
travels by value or through channels, never through a module-level variable.

### Packages

A **package** is a tree of modules and the unit of **distribution, dependency, and versioning** — the
thing you publish, depend on, and pin a version of. Packages form a **dependency DAG** (a directed
acyclic graph — dependencies never loop back): cycles between packages are rejected.

A build selects **one version per package** across the whole graph — the same package never appears at
two versions in one program — so a package's types keep a single identity program-wide.

### Coherence & the orphan rule

A `spec` implementation is **globally unique**: across the entire program there is exactly one
canonical implementation of any `(type, spec)` pair. This is enforced locally by the **orphan rule** —
an implementation must live in the package that defines the type, or the package that defines the spec.

This still lets you **extend an imported type with new behavior**: define your own spec and implement
it for that foreign type — you own the spec, so the orphan rule is satisfied. Giving someone else's
type a new capability is a first-class, everyday move, not a workaround. The only combination the rule
forbids is a **foreign spec on a foreign type** — implementing another package's spec for another
package's type, owning neither; for that rarer case, wrap the type in a **newtype** you own (a
single-field struct, with an opt-in auto-cast to smooth the wrapping) and implement the spec on the
wrapper.

Coherence needs **no global registry** — the orphan rule plus the **acyclic** package graph guarantee
it. To author an implementation of `(type, spec)` a package must name both; because the dependency
graph is a DAG, at most one of the two owning packages can depend on — and so name — the other, and no
third package can name both without owning either. The implementation, if it exists, is therefore
unique by construction. Single-version selection is what makes "one type, one implementation"
well-defined.

### Modules

A **module is a directory**; the files inside it are physical slices that share one namespace — the
number of files is layout, not meaning. A module is the default privacy unit: a plain declaration is
visible across the module's files but not outside it (see Visibility).

Nesting is **flat**: a directory laid out under another only lengthens the import path — there is no
hierarchical privacy, so a nested module gets no special access to an enclosing one. **Import cycles
between modules are rejected.**

Mutually recursive types and functions therefore live in **one module** — which costs nothing, since a
module is a directory of many files sharing a namespace: an `ast` module can spread `Expr` and `Stmt`
across separate files that reference each other with no import, and the compiler forward-declares while
auto-boxing gives the recursion a finite layout, exactly as for a self-referential type. When two types
in genuinely separate concerns refer to each other, the ban is a nudge to break the cycle by
reference-by-id — usually the better design — rather than to merge them (the package graph is a DAG for
the same reason one level up: mutually dependent packages must become one).

The only thing that must be acyclic regardless of layout is **top-level constant initialization** (see
Program lifetime & top-level initialization): a cycle there has no valid order and is a compile error.
A type naming another type is never such a cycle — only a constant whose initializer transitively needs
its own value.

### Visibility — exposing a declaration

Every declaration starts **module-private**. `pub` is the one visibility marker, and it means exactly
one thing — **visible to the rest of this package** — never, on its own, to the world. So there are
three scopes but a single keyword:

- **module-private** (the default) — visible only inside the defining module (across its files, which
  draw no boundary).
- **package-internal** — a `pub` declaration; the other modules of the same package may name it.
- **package-public** — not a marker but a **position**: a package's external API is exactly the `pub`
  surface of its **root module** (the top directory of the package's module tree). A declaration reaches
  dependents only by appearing there.

To expose an inner type to dependents, the root module **re-exports** it — pulling a package-internal
name, or a whole module, onto the root's own public surface. Re-export is the single mechanism that
builds a package's public surface: nothing escapes the package unless the root names it, so reshaping inner
modules never disturbs the external contract. A declaration can never be more visible than the types it
names — a package-public function cannot take or return a type that is **not itself on the public
surface** (whether module-private, or package-internal and never re-exported), because a dependent could
not name that type. A type's **`pub` methods travel with it**: once the type reaches the public surface,
its `pub` methods are callable by dependents too — visibility reads on a method exactly as on a function.

### Importing & referencing

Referencing another declaration is always **explicit** — no wildcard, no transitivity (importing a
package gives you its public surface only, never the packages it imports in turn), and no ambient
imports. Every name is either declared, imported, or a toolchain **built-in** — the primitive keywords
and the prelude (see The prelude & std). What you import depends on the distance:

- **Same module** (same directory) — nothing to import; a module's files share one namespace.
- **Another module of the same package** — import that sibling module, then name its package-internal
  (`pub`) declarations.
- **Another package** — import the package and see only its root public surface; a dependency's inner
  modules are **not** reachable, so the root's public surface is all a dependent gets.

Because every dependency is written down, the import graph is explicit — which is what lets module and
package **cycles be rejected**.

### The prelude & std

The **prelude** is not imported — its names are **built into the toolchain** and bound in every module
from the start, exactly like the primitive keywords. It holds what the language itself leans on: the
types the operators desugar to (`Either`, `Result`, `T?`, `nil`), the root specs (`Object`,
`Error`/`Err`), and a few pervasive types — the `list`, `map`, and `set` containers (see
[Collections](collections.md)). (Primitives — `bool`, `int`, `str`, … — and
`chan` are likewise grammar and runtime, not imported names.) These names are **reserved**: a
declaration may not shadow or redeclare them, so the operators that desugar to them can never be
knocked out from under the language.

Everything else is the **standard library** — an ordinary package with one difference: **std ships with
the toolchain**, so its version is the compiler's and you never declare it as a dependency. It is
imported explicitly like any package: `io`, `math`, further collections, and the ambient-OS functions
(`env`, the clock, randomness) that read read-only OS state.

Because the prelude is built-in rather than an implicit import, "no ambient imports" holds without
exception — there is simply a fixed set of toolchain-bound names always in scope, as keywords are.

### Testing & visibility

A test is ordinary code under the same visibility rules — there is **no test-only backdoor** into
privacy. That decides where a test lives:

- **White-box** — to exercise a module's `module-private` internals, put the test in a **file of that
  same module**; sharing the namespace, it sees the internals directly, with no import and no special
  access.
- **Black-box** — to exercise an API, import it: a sibling module sees the package-internal (`pub`)
  surface, and a separate package that depends on this one sees exactly the package-public surface — a
  true external view.

Test files are recognized **by the build tool's convention** (e.g. a `*_test.zg` name) and included
only in a test build, never in a normal one. So a test's declarations never reach the shipped artifact
or a package's public surface — even a `pub` declaration in a test file in the root module stays out of
the external API. As with the entry file, the language itself ascribes no meaning to the name; the tool
does.
