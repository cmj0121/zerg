# Zerg Modules, Packages & Programs

How Zerg source is organized, built, and started. This builds on the visibility, memory, spec, and
error models in the [Language Reference](../language.md). Also in [繁體中文](package.zh-TW.md).

## Four layers

Source is organized in four nested roles — from the whole program down to a single file — each owning
one concern:

| Layer       | What it is                                  | The boundary it draws                           |
| ----------- | ------------------------------------------- | ----------------------------------------------- |
| **program** | a build rooted at an entry file with `main` | the run — the root of the dependency graph      |
| **package** | a tree of modules                           | the **distribution / version** and **API** unit |
| **module**  | a directory                                 | the default **privacy** and **namespace** unit  |
| **file**    | a physical slice of one module              | none — files in a module share one namespace    |

Keeping encapsulation/naming (`module`) and distribution/API (`package`) in two layers is what gives
`pub` a precise meaning.

> **[not yet]** The **package** layer does not exist in this toolchain. There is no manifest, no version
> declaration, no resolver and no dependency fetch: a build is an entry file and the modules its imports
> reach on disk. Everything below that names a package — versioning, the package DAG, one-version
> selection, package-public as a position, and the orphan rule that rests on the graph being acyclic —
> describes a layer nothing implements yet. `import "name"` that resolves to nothing on disk is accepted
> silently and only `zerg lint` mentions it (`L101 unused import`).
>
> **[deviation]** The **module** layer is built, and is not the privacy unit the table says it is: every
> module is flattened into one namespace and no visibility is checked. See Visibility below.
>
> **[not yet]** Two modules that declare the same **public** top-level name are refused by name. A
> **private** one is not: nothing outside a module can reach its private names, so a bare call always means
> the caller's own, and the two only have to be told apart in C — where each gets a module tag, its
> position in a sorted list of the program's modules. Sorted rather than first-seen because that name has
> to be the same on every run.
>
> The public case has nowhere to be unique. This page declines a global registry on purpose (below), so a
> public collision is a compile error plus a **link-name override**, which is what [FFI](ffi.md) already
> specifies and which needs the package layer to exist first.

### Programs & the entry point

A **program is a build**, not a special kind of package. You point the compiler at an **entry file** —
`zerg build --emit bin entry.zg` — and it roots the build there, following its imports out across the
dependency DAG.

- The entry filename is **not reserved**; the build invocation designates it. What the language
  requires is **content**: the entry file must define a top-level **`main`** entry function — its shape
  (inputs and result) reuses models already defined, just below.
- `main` isn't `pub`, so it can never be imported — the "you can't depend on a program" property
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
  `Right(err)` prints `err` to stderr as `Kind: message` and exits `1`, and an uncaught **abort** unwinds
  the main stack and exits `1` with its own line on stderr. What the exit distinguishes is success from
  failure — status `0` against status `1` with a message — and nothing finer: a `Right` returned from
  `main` reports through the same root handler an abort uses, so the two are one status and one stderr
  line, and the `Kind:` prefix does not tell them apart either (a runtime fault and a forced `Err`
  report in that same shape, while `raise "msg"` is a bare line). `?` works directly in `main`.

### Program lifetime & top-level initialization

`main`'s body is the **root scope of the program**: when it returns, everything scope-owned beneath it
is freed and any still-running coroutine is abandoned where it stands (there's no join — drive a
coroutine to a channel-observed finish if it must complete first; see
[Coroutines & Channels](../code/coroutine.md)).

Outside `main` lives only **immutable top-level state** — constants, functions, types, and specs —
readied before `main` runs. Top-level constants are initialized in **dependency order** — a
constant is ready before any constant whose initializer reads it — a topological order of the reads-from
graph; if they form a cycle, that's a compile error. Where the graph leaves two constants unordered
(neither reads the other), the tie is broken **deterministically**: by **canonical module name**, then by
**source order** within a module. This whole ordering — topological, with the module-name-then-source
tie-break — holds.

Both halves of that are built. A constant whose initializer reads one declared **after** it gets the
value, not a zero — `const A: int = B + 1` above `const B: int = 10` yields `A == 11` — and a cycle is a
named refusal: _these constants depend on each other and none can be given a value first_.

A module may also define **`init()`** functions (**multiple allowed**) — its **lazy** one-time setup.
They run **exactly once**, the **first time the module is used** (later uses skip them; concurrent
first-uses still run them once), in **declaration (FIFO) order** within a module and in **dependency
order** across modules (a module's imports initialize first), before any of that module's own code and
before `main`: each one runs **exactly once, in FIFO order, before `main`**.
`init()` carries multi-step or effectful startup (open a resource, register, seed) rather than hiding it in
a constant's initializer, and readies the module's immutable state. There is still **no mutable global**:
shared mutable state travels by value or through channels, never a module-level variable — a top-level
binding may not be `mut` outside a module-level `unsafe { … }` group.

If an `init()` **aborts**, the abort propagates from the **first-use site** that triggered it — guardable
there, or else crashing that stack like any uncaught abort (the main stack ends the program, a coroutine
only itself). The module is then **poisoned**: `init()` is **not re-run** (exactly-once holds even on
failure, so no side effect repeats), and every later use **re-aborts with the same cached error**. A
half-initialized module never becomes usable, and concurrent first-uses all observe that one failure.

> **[deviation]** Initialization is **eager, not lazy**. Every `init()` in the program runs before
> `main`'s first statement, in declaration order over the whole program, rather than at the first use of
> the module that owns it. Exactly-once and FIFO-within-a-module hold; "the first time the module is
> used" does not, so an `init()` in a module a run never touches still runs.
>
> **[not yet]** Two modules that each declare an `init()` are refused by name — the flattened namespace
> cannot tell `init__0` from `init__0`. So cross-module initialization **order** has no program that can
> observe it yet.
>
> **[not yet]** **Poisoning.** An aborting `init()` ends the program on the main stack; there is no
> cached error, no re-abort at a later use, and no first-use site to guard at, because the call is not at
> a use site.

### Packages

A **package** is a tree of modules and the unit of **distribution, dependency, and versioning** — the
thing you publish, depend on, and pin a version of. Packages form a **dependency DAG** (a directed
acyclic graph — dependencies never loop back): cycles between packages are rejected.

A build selects **one version per package** across the whole graph — the same package never appears at
two versions in one program — so a package's types keep a single identity program-wide.

> **[not yet]** All of it — see the marker under Four layers. There is no package, so there is no
> version, no graph between packages, and nothing to select.

### Coherence & the orphan rule

A `spec` implementation is **globally unique**: across the entire program there is exactly one
canonical implementation of any `(type, spec)` pair. This is enforced locally by the **orphan rule** —
an implementation must live in the package that defines the type, or the package that defines the spec.

This still lets you **extend an imported type with new behavior**: define your own spec and implement
it for that foreign type — you own the spec, so the orphan rule is satisfied. Giving someone else's
type a new capability is a first-class, everyday move, not a workaround. The only combination the rule
forbids is a **foreign spec on a foreign type** — implementing another package's spec for another
package's type, owning neither; for that rarer case, wrap the type in a **newtype** you own (a
single-field struct: construction wraps it, and the way back out is a written accessor — nothing
casts on its own) and implement the spec on the wrapper.

Coherence needs **no global registry** — the orphan rule plus the **acyclic** package graph guarantee
it. To author an implementation of `(type, spec)` a package must name both; because the dependency
graph is a DAG, at most one of the two owning packages can depend on — and so name — the other, and no
third package can name both without owning either. So the implementation, if it exists, is unique by
construction. Single-version selection is what makes "one type, one implementation"
well-defined.

> **[not yet]** The **orphan rule is not enforced**. A third module may write `impl Spec for T` owning
> neither the spec nor the type, and it compiles. What IS checked is the narrower thing the flattened
> namespace can see: two `impl`s giving one type the same method name in one build are refused.

### Modules

A **module is a directory**; the files inside it are physical slices that share one namespace — the
number of files is layout, not meaning. A module is the default privacy unit: a plain declaration is
visible across the module's files but not outside it (see Visibility).

Nesting is **flat**: a directory laid out under another only lengthens the import path — there is no
hierarchical privacy, so a nested module gets no special access to an enclosing one. **Import cycles
between modules are rejected.**

So mutually recursive types and functions live in **one module** — which costs nothing, since a
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

> **[deviation]** The **entry file's own directory** is not a module. A file beside the entry file is not
> in its namespace and is not compiled into the build: naming a function declared there reports
> `undefined function`. Files share one namespace in every module that is reached by an `import`; the
> module rooted at the entry file is the exception.
>
> **[deviation]** **Import cycles are not rejected.** Two modules that import each other compile and run.
> Nothing detects the cycle, at either layer.

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

> **[deviation]** **Enforced on functions and module constants, and on nothing else yet.** Naming another
> module's module-private FUNCTION is a compile error, reported with a place — both as a bare call and as
> the namespaced `lib.helper()` — and so is reading its module-private CONSTANT, in the same two shapes:
> the bare `FLOOR` and the namespaced `lib.FLOOR`. A
> module-private TYPE and a module-private FIELD are still readable across the boundary: a `pub fn` may
> return a private struct and a dependent may read a private field of it, with no finding. Every module is
> flattened into one namespace, which is also why two modules that declare the same
> name collide — that refusal is about the name, not about the visibility. What the rule compares is the
> DIRECTORY a declaration was read from against the directory doing the reading, which is the module
> boundary and not the package one; **package-internal** and **package-public** above still need a package
> to exist.

### Importing & referencing

Referencing another declaration is always **explicit** — no wildcard, no transitivity (importing a
package gives you its public surface only, never the packages it imports in turn), and no ambient
imports. Every name is either declared, imported, or a toolchain **built-in** — the primitive keywords
and the prelude (see The prelude & std). What you import depends on the distance:

- **Same module** (same directory) — nothing to import; a module's files share one namespace.
- **Another module of the same package** — import that sibling module, then name its package-internal
  (`pub`) declarations. A named function among them is a **first-class value**, not only a call target:
  `f := other.helper` binds it, `f(x)` calls it (see [Functions & Closures](../code/functions.md)).
- **Another package** — import the package and see only its root public surface; a dependency's inner
  modules are **not** reachable, so the root's public surface is all a dependent gets.

Because every dependency is written down, the import graph is explicit — which is what lets module and
package **cycles be rejected**.

> **Status.** The surface these sections describe is wired today: **string-path imports**
> (`import "util/text"`), **parenthesized import groups** (`import ( … )`), **`as` renames**
> (`import "a/text" as at` binds the whole namespace — its functions and its module constants alike), and
> **one-level `import pub` re-export** onto the root module's public surface. (The re-export is one level:
> `import pub` exposes the named module on this module's surface; it does not transitively re-export what
> that module itself re-exports.)
>
> The **binding** an import introduces is enforced: the prefix of a qualified name must be a namespace
> some `import` in the build actually bound, so an invented `bogus.f()` or a path segment used as a name
> (`util.f()` under `import "util/text"`) is rejected as an undefined name, with a place; two imports
> whose bindings collide, and a binding a top-level declaration already took, are rejected too, and `as`
> is how both are resolved.
>
> **[deviation]** **The binding is the BUILD's, and so is the member.** Two halves of one flatten, and
> both are visible to a program:
>
> - **No transitivity is not enforced.** The namespaces in scope are every namespace every module of the
>   build bound — including another module's `as` alias — so a module this one never imported is one it
>   can still name. The import graph decides what is COMPILED into the build, not what is nameable inside
>   it.
> - **The member is looked up program-wide.** Once the prefix resolves, the name after the `.` is found in
>   the one flattened namespace rather than in the module the prefix named, so with `a` and `b` both
>   imported, `b.helper()` answers a `helper` that module `a` declared. `pub` is still checked against the
>   module that DECLARED the member, which is why a private one is refused naming its real owner rather
>   than the module the program wrote. The seed compiler resolves this half correctly, and answers
>   `module "b" has no public member "helper"`.
>
> **[not yet]** A cross-module function is a **call target only**: `other.helper(x)` works and
> `f := other.helper` reports that the module has no such member, so the first-class value this section
> promises stops at the module boundary.

### The prelude & std

The **prelude** is not imported — its names are **built into the toolchain** and bound in every module
from the start, exactly like the primitive keywords. It holds what the language itself leans on: the
types the operators desugar to (`Either`, `Result`, `T?`, `nil`), the built-in specs (`Eq`, `Ord`, `Hash`,
`Error`, `Iterator`/`Iterable`, `Ref`, and the operator specs — see [Specs & Generics](../core/specs.md); there is
**no `Object` spec** — equality and ordering are opt-in via `derive(Eq)` / `derive(Ord)`, and `display` /
`debug` are built-in value renderings, not spec methods, see [Format](format.md)),
and a few pervasive types — the `list`, `map`, and `set` containers (see
[Collections](../code/collections.md)) and the `Ref[T]` resource box. (Primitives — `bool`, `int`, `str`, … —
and `chan`, plus the `defer` and `print` constructs, are likewise grammar and runtime, not imported
names.) These
names are **reserved**: a
declaration may not shadow or redeclare them, so the operators that desugar to them can never be
knocked out from under the language.

Everything else is the **standard library** — an ordinary package with one difference: **std ships with
the toolchain**, so its version is the compiler's and you never declare it as a dependency. It is
imported explicitly like any package: `io`, `math`, further collections, and the ambient-OS functions
(`env`, the clock, randomness) that read read-only OS state.

Because the prelude is built-in rather than an implicit import, "no ambient imports" holds without
exception.

> **[not yet]** Of the built-in specs the prelude promises, only **`Eq`** and **`Into[T]`** exist.
> `Ord`, `Hash`, `Error`, `Iterator` / `Iterable`, `Ref` and the operator specs are not declared, so
> `impl Ord for P` reports that nothing in the program declares a spec by that name. `set` and `Ref[T]`
> are likewise absent — `list` and `map` are the containers there are.
>
> **[deviation]** Prelude names are **not reserved**. A program may declare `struct list`, `struct
Result`, `struct Ref` or `spec Eq` and none of it is refused, so the names the operators desugar to can
> in fact be knocked out from under the language.

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

> **[not yet]** There is **no test build**. `*_test.zg` is kept out of an ordinary build, which is the
> half of the convention that works; the command that would include it — `zerg test` — does not exist,
> so the white-box and black-box positions above are places to put a file rather than a way to run one.
> The `testing` module's `assert` family is callable from an ordinary program in the meantime.

### Target-conditional files

Platform and architecture differences are handled the **same way** — at the **file level, by build-tool
convention** — not by an inline `#ifdef` / `cfg` construct, which would fragment code against
`small and crisp`. A module keeps **per-target files** (a name suffix like `_linux` / `_darwin`,
alongside the `_test` convention), and the build includes only the ones matching the selected target; the
language itself stays **target-agnostic**, ascribing no meaning to the name. The exact target-naming and
matching scheme is a build-tool detail, **deferred**.

> **[not yet]** No suffix is recognized. Every `.zg` file in a module directory is compiled into the
> build, so a module holding `plat_linux.zg` and `plat_darwin.zg` is two declarations of one name and is
> refused as a collision — which is a clearer failure than picking the wrong one, and is still not the
> feature.
