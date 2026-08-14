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
> describes a layer nothing implements yet. An `import "name"` that resolves to nothing on disk is a hard
> build error, not a silence — _E502 cannot resolve import `name` under any source root_ — reported before
> a byte of it is lexed.
>
> **[deviation]** The **module** layer is built, and is not the privacy unit the table says it is: every
> module is flattened into one namespace, and visibility is checked on some of what a module holds and not
> all — a function and a module constant are (_E301 `helper` is not a public member of module `lib`_, with
> a place), a struct's fields are not. See Visibility below.
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
readied before `main` runs.

That is a statement about **what runs where**, so it settles a form the grammar allows and this section
never spoke to: a **statement written at the top level**. `GRAMMAR#program` derives one — `program ::=
stmt-list` is Zerg's **script mode**, and the grammar opens the language with the `nop` program — so it is
well-formed syntax and a compiler reads it whole. A **compiled** program has no moment at which to run it:
execution begins at `main`, and everything above is state readied before that. It is therefore **refused by
name, with a place**, by the build rather than by the parse — the same split a program with no `fn main`
takes ([Conformance](../conformance.md)). `nop` is the one exception, and not really an exception: it does
nothing and yields nothing, so running nothing for it is running it.

Top-level constants are initialized in **dependency order** — a
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
binding may not be `mut` outside a module-level `unsafe { … }` group, and one that is inside a group is
**module-private**, never `pub` (see [Visibility](#visibility--exposing-a-declaration)).

If an `init()` **aborts**, the abort propagates from the **first-use site** that triggered it — guardable
there, or else crashing that stack like any uncaught abort (the main stack ends the program, a coroutine
only itself). The module is then **poisoned**: `init()` is **not re-run** (exactly-once holds even on
failure, so no side effect repeats), and every later use **re-aborts with the same cached error**. A
half-initialized module never becomes usable, and concurrent first-uses all observe that one failure.

> **[deviation]** Initialization is **eager, not lazy**. Every `init()` in the program runs before
> `main`'s first statement, rather than at the first use of the module that owns it. Exactly-once holds,
> and so does the order: a module's imports are readied before it is, and its own blocks run FIFO. What
> does not hold is "the first time the module is used" — an `init()` in a module a run never touches
> still runs.
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

The orphan rule is enforced, and by the module rather than by the package the paragraph above reasons
from: a third module writing `impl Spec for T` while owning neither is refused with _E277 `impl Speak for
Dog` is in neither's module — a spec and a type belong to whoever declared them, and an impl belongs with
one of the two_, with a place. Two `impl`s giving one type the same method name in one build are refused
too, which is the narrower thing the flattened namespace can see on its own.

### Modules

A **module is a directory**; the files inside it are physical slices that share one namespace — the
number of files is layout, not meaning. A module is the default privacy unit: a plain declaration is
visible across the module's files but not outside it (see Visibility).

An import path is resolved **beside the importer first** — the directory of the file that wrote the
`import` — then beside the entry file, then in the standard library, first match winning. So a module
carries its own dependencies with it: `api/` may hold the `api/util/` it imports, and moving the pair
somewhere else moves both. (The seed compiler searches the entry file's directory only, and refuses a
module reached this way.)

Nesting is **flat**: a directory laid out under another only lengthens the import path — there is no
hierarchical privacy, so a nested module gets no special access to an enclosing one. **Import cycles
between modules are rejected** — a module that comes up again while it is still on the way down has no
order for its `init()` blocks and module constants to be readied in, and the refusal names the loop
rather than the walk that reached it. A module two others import is not a cycle, and a module importing
**itself** is the one-node case of the same rule.

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
> ---
>
> **[deviation]** A **single file** is importable as a module. `import "sib"` beside a `sib.zg` resolves
> to that one file and its `pub` names, though a module is a directory here and `E502`'s own sentence says
> so — _a module is a directory of `.zg` files beside the importer or in the standard library_. So the
> import path has a second, undocumented shape, and the diagnostic that would teach a reader the first one
> denies the second exists.

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

**Enforced on every declaration a module can name.** Naming another module's module-private FUNCTION is a
compile error, reported with a place — both as a bare call and as the namespaced `lib.helper()` — and so
is reading its module-private CONSTANT, in the same two shapes: the bare `FLOOR` and the namespaced
`lib.FLOOR`. A module-private TYPE is refused the same way, whether it is written bare (`s: Secret`, a
`Secret(…)` construction) or through the namespace (`lib.Secret`); a module-private FIELD is refused on
read and on write alike, which is the other half of the rule requiring one to carry a default. And the
normative sentence above is a rule of its own: a **`pub` declaration that names a type which is not
`pub`** is refused **at the declaration**, so a `pub fn` cannot return, take, or hold in a `pub` field a
type its dependents could never spell.

Two findings are reported where both are true — the export that leaks and the read that reaches in —
because they are two mistakes in two files and one message cannot point at both. The export is the one
with a fix available: the module that wrote `pub` decided to hand the type out and is the only party who
can mark the type `pub` or stop returning it, where a dependent reading a private field can do neither.

Every module is still flattened into one namespace, which is why two modules that declare the same name
collide — that refusal is about the name, not about the visibility.

> **[deviation]** What the rule compares is the DIRECTORY a declaration was read from against the
> directory doing the reading, which is the **module** boundary and not the package one. So
> **package-internal** and **package-public** above are still one tier as far as the compiler is
> concerned: a `pub` declaration is nameable by every other module of the build that imports it, and
> nothing narrows that to a package's root surface, because no package exists yet to be the unit that
> narrowing is measured against. Re-export (`import pub`) builds a surface; nothing yet requires a name to
> be on one.

The one declaration that may not be `pub` at all is a **mutable global** — a `mut` binding inside a
module-level `unsafe { … }` group, which the grammar makes module-private by construction (`GRAMMAR`
group 12). A group is one module's bargain with its own author, and `pub` on it would offer that bargain
to everyone who imports the module; it is refused at the declaration, with a place. Expose a `pub fn`
that reads the binding instead.

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
> **The "no transitivity" rule is enforced.** A binding belongs to the MODULE of the file that wrote the
> `import` — module-grained and not file-grained, because a module's files share one namespace — so the
> namespaces a module may name are the ones its own files bound, its `as` aliases included and another
> module's excluded. Naming a module this one never imported is a compile error with a place, at every
> position that can spell one: a call, a member read, a `spawn` / `defer` callee, a construction, a variant
> read, and a TYPE — the annotation `c: lib.Counter` and the signature `fn take(c: lib.Counter)` alike. The
> import graph decides what is compiled into the build AND what is nameable inside it, and the two answers
> are told apart from a third: an invented prefix (`bogus.f()`) names nothing anywhere and stays an
> **undefined name**, because sending a reader to add an import for a module that does not exist would be
> worse than the hole.
>
> > **[deviation]** **A type position discards a qualifier it cannot resolve.** `c: bogus.Counter` builds,
> > and reads as `Counter`: a qualified type name resolves to its last segment — the flatten this chapter
> > documents — and an unknown qualifier is dropped there rather than reported. So the three-way answer
> > above is complete only at the expression positions; a type position tells "this module did not import
> > it" from "this is a real namespace", and neither of those from a typo.
>
> **[deviation]** **The member is looked up program-wide.** Once the prefix resolves, the name after the
> `.` is found in the one flattened namespace rather than in the module the prefix named, so with `a` and
> `b` both imported, `b.helper()` answers a `helper` that module `a` declared. `pub` is still checked
> against the module that DECLARED the member, which is why a private one is refused naming its real owner
> rather than the module the program wrote. The seed compiler resolves this half correctly, and answers
> `module "b" has no public member "helper"`.
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
> **[deviation]** The reserved set is **what the toolchain binds**, which is narrower than the prelude
> this page describes. `struct list`, `fn int`, `enum Left` and `spec Eq` are refused at the declaration
> — _E611 `list` is a prelude name — a built-in container type — and cannot name a struct_, with a place
> — and so are `map`, `bytearray`, `runearray`, `Either`, `Result`, `Err`, `Right` and `Into`. The names
> the same paragraph promises and **nothing here declares** — `Ord`, `Hash`, `Error`, `Iterator`,
> `Iterable`, `Ref` and `set` — are not reserved, because a program's own `spec Ord` is the only `Ord`
> there is and refusing it would hold a name for a feature that does not exist. Each joins the set on
> the day it is bound.
>
> The **function slot takes a narrower set than the type slots**, and `map` is the whole of the
> difference: `fn map(xs, f)` is legal, every other name in the set is not. A type declaration's name
> lands in the namespace all of them are bound in, while a function's lands where only the ones a
> **call can spell** are — and `map[…](…)` as a constructor is built by neither compiler, so the name
> has no value form to take. The rest do: a callee spelling `int`, `byte`, `bytearray` or `list` is read
> as a conversion, and one spelling `Either`, `Result`, `Err`, `Eq`, `Into`, `Left` or `Right` as a
> construction, before any user symbol is looked for.
>
> Two positions are outside the rule and are not deviations from it. A **method** name is its type's,
> not the program's, so `impl P { fn set(v: int) }` is legal. A **binding** — a local one or a module
> constant, which are one form in the parser — may still take a prelude name; shadowing one inside a
> scope is a loud error at its first use, which is the thing a declaration was not.

### Testing & visibility

A test is ordinary code under the same visibility rules — there is **no test-only backdoor** into
privacy. That decides where a test lives:

- **White-box** — to exercise a module's `module-private` internals, put the test in a **file of that
  same module**; sharing the namespace, it sees the internals directly, with no import and no special
  access.
- **Black-box** — to exercise an API, import it: a sibling module sees the package-internal (`pub`)
  surface, and a separate package that depends on this one sees exactly the package-public surface — a
  true external view.

White-box placement works in **either** shape a directory can have, because a test build resolves a test
file's package the way an import is resolved: **most specific first**. `module_at` answers a single
`<name>.zg` file before it answers a directory, so a `.zg` file beside its neighbours is a module in its
own right — which is what `src/stdlib` is, sixteen independent modules in one flat directory. A
`strings_test.zg` there is therefore the test of `strings.zg` alone, and its package is that pair; a test
file that names no such sibling belongs to the **directory**, as before.

What that costs: in a directory that _is_ one module, a `*_test.zg` whose name matches one of the module's
files takes that file alone — `a_test.zg` in a directory of `a.zg` and `b.zg` no longer sees `b.zg`. It is
a loud failure (an undeclared name, at the line that used it) and never a silent one, and the way out is to
name the test file for the module rather than for one of its parts. In exchange the rule is **monotone**:
adding a test file never changes what another package is built from.

Test files are recognized **by the build tool's convention** (e.g. a `*_test.zg` name) and included
only in a test build, never in a normal one. So a test's declarations never reach the shipped artifact
or a package's public surface — even a `pub` declaration in a test file in the root module stays out of
the external API. As with the entry file, the language itself ascribes no meaning to the name; the tool
does.

> **[not yet]** `zerg test` is a **scaffold**. It walks a path for the packages under it, compiles
> each — its sources, its test files and a generated driver, so a white-box test reaches the
> module's internals with no import and no `pub` — and runs every `#[test]` it finds, reporting
> `ok` / `FAIL` / `SKIP` / `STUCK` / `CRASH` grouped by file, counting skips and timeouts apart
> from passes and failures, and exiting non-zero if any did not hold.
>
> **A package is the module the test file names**, resolved most specific first: `strings_test.zg`
> beside `strings.zg` is a package of that pair (plus the directory's own `fixtures_test.zg`, if
> there is one, and the driver), and a test file naming no such sibling makes the directory the
> package. One directory may therefore hold several packages, each with its own driver and its own
> process.
>
> **The path may be one `.zg` file**, and then it is the **package that file is in** that runs —
> `zerg test src/stdlib/strings_test.zg` runs `strings.zg` + `strings_test.zg`, and not the other
> packages of that directory. Ancestors are counted from the file's directory, exactly as they
> would be for the directory itself. A build of the test file **alone** is a build of nothing, so
> the file selects a package rather than a file list; `--only` is the tool for one test.
>
> **The exit status distinguishes a search that found nothing.** `0` every test that ran passed or
> skipped, `1` a test failed or timed out, `2` the command could not be carried out (no such path,
> a `--only` that matched nothing, a fixture parameter that resolves to nothing), and `3` the
> search ran and there was no test in what it searched. `3` is not folded into `2` because the
> reader's next move differs: a `2` is fixed by editing the command line and a `3` by looking at
> the tree. A run that found nothing says so on stderr as well, but a sentence alone leaves a CI
> line green forever, and the reader of a CI line is a shell.
>
> **`--only <name>`** runs the tests whose name **begins with** `<name>` — a whole name selects one
> test, a stem selects the family. It is applied before anything is generated, so a test it did not
> select is not compiled and its fixtures are never built. A filter that selected nothing exits `2`
> rather than reporting a green run of nothing: the command named a test that is not there.
>
> **`--timeout <seconds>`** is how long one test may take before the run stops waiting on it;
> the default is `60`. A test that goes over is reported `STUCK` and counted apart, and the run
> goes on to the tests after it: a timeout is not a failed assertion, because nothing was decided.
> Without it a hung test and a slow test are indistinguishable, and the hung one takes the whole
> run with it until CI's own timeout kills the job with nothing saying which test did it.
>
> **Two paths, and the report says which.** A test runs as a **coroutine**, inside a `guard`, in
> one process — which contains an assertion that does not hold and an uncaught abort alike, since
> a `guard` catches both and a coroutine that dies without one dies alone. What no coroutine can
> contain is a test that ends the **process**: a stack overflow, or `os.exit`. So a test with no
> result when the run ends is re-run in a **process of its own** — the remainder, exactly, since
> a result is written only after a body returns — and that is what attributes the death to the
> test that caused it. When it happens the report says so on a `NOTE` line; a run that quietly
> changed strategy is a run whose result nobody can interpret.
>
> A `#[test]` returns nothing and takes either **no parameter** or one **`testing.Context`**,
> identified by its type and not by its name; the driver writes whichever call the signature
> asks for. A context is passed **by value** and shares what matters anyway — its one field is a
> channel, and a channel is a `Ref` value, so a copy shares it — and carries `ctx.name()`,
> `ctx.log(msg)` (shown only if the test fails), `ctx.skip(reason)` and `ctx.fatal(msg)`. The
> last two `raise` to unwind and leave the reason on the context, so nothing has to read a
> message string to tell a skip from a failure. A claim is `assert cond`, the keyword
> ([Grammar](../surface/grammar.md), group 8), which writes its own message and needs nothing from
> the context; what is left for `ctx.log` is a **domain note**, a thing about the fixture rather
> than about the expression. `testing.assert_raises` stays a free function because it is not an
> assertion: it asks what an already-finished call raised.
>
> **Fixtures.** A test **declares what it needs**, the framework builds it once, hands it over and
> tears it down. A `#[fixture]` is a function that takes its tests as a **continuation**:
>
> ```zerg
> #[fixture]
> fn db(use: fn (Conn)) {
>     c := connect("postgres://tmp/test")
>     defer c.close()
>     use(c)
> }
>
> #[fixture]
> fn schema(db: Conn, use: fn (Schema)) {
>     s := make_schema(db)
>     defer drop_schema(s)
>     use(s)
> }
> ```
>
> `use: fn (T)` is identified **by type**, and it is two things at once: the continuation the tests
> run inside, and the declaration of what this fixture **produces**. Every other parameter is a
> **dependency matched by name** against another fixture — one rule for test-to-fixture and
> fixture-to-fixture both. Teardown is `defer`, the language's own idiom, and needs nothing from the
> runner.
>
> A test resolves its parameters the same way: a `testing.Context` **by type**, a fixture **by
> name**, and no parameter meaning a direct call.
>
> ```zerg
> #[test]
> fn test_insert(schema: Schema) { … }
>
> #[test]
> fn test_query(db: Conn, ctx: testing.Context) { … }
>
> #[test]
> fn test_pure() { … }
> ```
>
> The framework **composes by nesting** — `db(fn (c) { … schema(c, fn (s) { … }) })` — so dependency
> order, teardown order (an inner `defer` fires first), and "only when needed" are all consequences
> of where the calls were written. There is no topological sort at runtime and no teardown registry,
> and a level no test goes through is never generated at all.
>
> **Everything is resolved before anything runs.** A parameter naming no fixture, a parameter whose
> type is not what the fixture produces, and a **circle** among fixtures are each reported with a
> **place**, for the whole tree at once, and the run exits `2` having executed nothing.
>
> **A fixture that cannot be built fails every test that needed it** — `FAIL test_query` with
> _fixture `db` could not be built: …_ under it, and the run exits non-zero: a test that could not
> run must never look like one that passed. If the raise arrives when every test under it has
> already answered for itself, what broke was the **teardown**, and the failure is recorded against
> the fixture instead of against anybody's test.
>
> **Where a fixture lives, and what inherits it.** A fixture is declared in a `*_test.zg`, so it
> reaches no shipping build, and it serves the tests of **its own directory**. One that is to serve
> the directories **below** as well goes in **`fixtures_test.zg`** — the one file an ancestor
> contributes downward, to **every** level below it and not merely the next one. That is pytest's
> `conftest.py` model, and the fixed name is load-bearing twice: an ordinary `*_test.zg` is a file
> **of** its module and reads that module's private names, so carrying it into a directory below
> would put it in a scope where those names are not; and an ancestor's own tests would otherwise run
> once per descendant directory. A `#[test]` written in a `fixtures_test.zg` runs in its own
> directory, once, like any other. Ancestors are counted from the path `zerg test` was **given**.
>
> An inherited file is **copied into the package** for the length of the run, because a module is a
> **directory** — the same reason the generated driver is written there. The copy is byte for byte,
> so a diagnostic inside one points at the right line, and it is taken away when the run ends. A
> package therefore cannot **shadow** an inherited fixture: two declarations of one name in one
> scope are refused as the collision they are.
>
> **The scope is the package, not the session.** `pkg/sub` and `pkg/sub2` each build their **own**
> instance of an inherited fixture, because each is one driver in one process. That is pytest's
> `scope="package"`. Session scope is deliberately not wanted and is anyway unreachable: `E705`
> refuses two modules both defining a `pub` name, so one driver over a whole tree cannot be built,
> and a live value does not cross a process boundary.
>
> A fixture value reaches a test the way any value reaches a call — **by value** — and the test body
> runs on the far side of a `spawn`, so a `Ref` inside it (a channel, a boxed handle) is shared and
> the rest is copied. Tests are **serial**; `ctx.parallel()` is **[not yet]**, and when it lands,
> tests sharing a fixture will share one instance of it.
>
> **The fallback rebuilds what it needs.** When a test ends the process and the remainder is re-run
> one process each, each of those processes enters only the levels the test it was asked for is
> under — so it stands that test's fixtures up again, and no others.
>
> The generated driver and the copy of an inherited fixture file come **off disk as soon as they
> have been read**, before the build can emit, compile, link or report anything — so a run that
> fails leaves a source directory exactly as it found it. That matters most where it is least
> visible: the compiler resolves the standard library by **listing** `src/stdlib`, and a driver
> left behind there is a file every later build reads.
>
> What is **not** built is everything past that: a doc comment (`##`), a doc example run as a
> test, benchmarks, and running two tests at once. A failing assertion `raise`s, and a raise is
> control flow, so it unwinds out of the test body on its own.
>
> The **assertion** side has since been filled in, and it is a KEYWORD rather than a library.
> `assert cond` raises `AssertionError` with the file, the line, the claim's own source text and
> the value of each operand a comparison came apart into. `testing.assert_raises` answers the
> `Err` a `guard`ed call raised and is the one helper left. What is still not there is a rendering for a **composite**,
> so `assert xs == ys` over two lists is `E445` and a claim about a list is made through something
> that reduces it to a scalar.
>
> The **exclusion** is built. A normal build compiles no `*_test.zg` — the name is matched where a
> module's directory is read, in both compilers — so nothing a test declares reaches the shipped
> artifact or joins a module's surface, and a name it repeats or a file that does not parse costs a
> normal build nothing. Naming one of its declarations is _E388 module `lib` has no `only_in_test`_,
> and naming the file is _E512 `lib/lib_test` names a test file, and a normal build compiles none_,
> both with a place. E388 does not go on to say that a test file declares one: that fact is the
> loader's, and the rule that reports it is in the checker.
>
> A test file is not importable from anywhere, a sibling test file included — a white-box test shares
> its module's namespace and reaches its internals with no import at all, which is what makes `zerg
test` a matter of compiling more files rather than of relaxing a visibility rule.

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
