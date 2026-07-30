# Zerg Collections

Zerg's built-in containers — **`list`**, **`map`**, **`set`**, plus the fixed-size **`[T; N]`** array —
one canonical type per role, no variant zoo. They're just ordinary **scope-owned values**, built on the
[Language Reference](../language.md). Also in [繁體中文](collections.zh-TW.md).

| Type        | Role                        | Element / key requirement | Iteration order     | Status        |
| ----------- | --------------------------- | ------------------------- | ------------------- | ------------- |
| `list[T]`   | an **ordered sequence**     | any `T` (no bound)        | index order         |               |
| `map[K, V]` | an **associative** table    | `K: Eq + Hash`            | **insertion** order |               |
| `set[T]`    | a **unique-membership** set | `T: Eq + Hash`            | **insertion** order | **[not yet]** |
| `[T; N]`    | a **fixed-size array**      | any `T` (no bound)        | index order         | **[not yet]** |

The `map` key requirement above is the intended one; this phase a key is restricted to **`int`** or **`str`**
(see [Keys](#keys--eq-free-hash-explicit) below), and `set[T]` is **[not yet]** in both type and value
position.

Richer shapes are compositions, not new built-ins. `list[byte]` is the raw byte sequence (indexable, may
hold a NUL); `str` stays a separate immutable primitive (below).

## Values, not references

A collection is a **scope-owned value**: **copy-by-value** (the compiler elides or moves when safe), freed
at scope exit, **no aliasing** — copying **deep-copies** the elements and **retains** (refcount-bumps) any
**reference-counted** element it holds: a `chan`, a `Ref[T]`, a `str`, or the boxed tail of a recursive
type. That is exactly the memory rule — the value-type parts are copied, the reference-counted parts shared
(see [Values & Memory](../core/memory.md#copy-vs-reference-semantics)). There's no shared container hiding behind two
names: you share for **reading** with an immutable pass, for **mutation** with a `mut &` parameter; a
collection sent over a channel is copied like any other payload.

## Mutability — one per-binding knob

Mutability is the ordinary **per-instance** axis: a **single knob** unlocking _both_ content edits and
rebinding — the Rust `let mut` / Swift `var` model, not a variable-vs-elements split.

- **`mut xs`** — may **edit elements** (`xs[i] = v`), **grow/shrink** (append, insert, remove), and
  **rebind** (`xs = other`). Edits and growth are `mut this` methods, like a struct's mutators.
- **plain `xs`** — **fully frozen**: fixed _contents_, though its length is still a runtime value on the
  heap. (You may still `:=` re-declare the name — a _new_ binding, the old one `del`-ed — never a mutation.)
  For a fixed _size_ known at compile time and laid out inline, reach for a `[T; N]` array (below).

So one `list` type is both a frozen sequence (plain) and a growable vector (`mut`); **only a `mut`
collection can modify its elements**.

```text
xs := [1, 2, 3]            # frozen: xs.append(4) and xs[0] = 9 are errors
mut ys := [1, 2, 3]
ys.append(4)               # grow  ·  ys[0] = 9  # edit  ·  ys = [2, 4]  # rebind
```

## Keys — `Eq` free, `Hash` explicit

`list[T]` takes **any** `T` (only the structural ops every value has). A `map` key / `set` element needs
both **equality** and **`Hash`** — a key compares by `==` and hashes. Neither is automatic: equality is
opt-in via **`derive(Eq)`** (or a hand-written `impl Eq`), and **`Hash` is likewise explicit** — a type gains
it through `derive(Hash)` or by hand — keeping "what can be a key" an opt-in, `safe by default` choice. The
author owns the contract the compiler can't check: **equal ⇒ same hash**. Because a key is **copied in** as a
frozen snapshot, even a `mut` collection is usable as one.

> **Status.** The intended rule — **any `Eq + Hash` type** as a key — is **[not yet]**. This phase a `map`
> key is restricted to **`int`** or **`str`**; `derive(Hash)` and general keyed types are not built, and
> `set[T]` is **[not yet]** entirely.

## Access — `[]` asserts, `.get` checks

Indexing mirrors the force-vs-check split of `!` / `?`:

- **`xs[i]` / `m[k]`** — the element **by value**; **aborts** on a bad index or missing key
  (`IndexError` / `KeyError`). A bad index is a **bug**, just like overflow.
- **`xs.get(i)` / `m.get(k)`** — the checked path → **`T?`** / **`V?`**, for when you expect absence.
- **`x in s` / `k in m`** → `bool`; on a `mut` collection **`xs[i] = v`** sets in place.

```text
first := xs[0]                 # aborts if empty
name  := m.get(id) ?? "anon"   # checked, then default
```

## Slicing — read-only subranges

A **subrange** — `xs.slice(a, b)`, the elements `[a, b)` — is an ordinary **read-only `list[T]` value**,
not a borrow: it never writes back into its parent, so there is **no aliasing** and no borrow checker, and
it obeys the same copy-by-value model as any collection. The compiler may realize that copy as
**copy-on-write** — sharing the parent's backing storage until either side is mutated, then copying — so
value semantics hold while the read-only case stays **zero-copy**; COW is an unobservable optimization
alongside copy-elision and the move (Values & Memory), adding no visible sharing, only a cheaper `copy`.

So a lexer scans by index (`xs[i]` is O(1)) and takes read-only `slice` windows at no copy cost,
materializing a `str` only when it keeps a token.

> **[not yet]** Slicing is unbuilt this phase: neither `xs.slice(a, b)` nor the **`x[a..b]`** slice-index
> sugar (the latter also deferred at the grammar level — see [Deferred](#deferred)) is available yet. The
> read-only, copy-on-write design above is the intended semantics.

## Order & equality

`list` walks in index order; `map`/`set` in **insertion order** — deterministic, no hash-ordering surprise.
Iterating reads each element **by value** (elidable to read-only by-ref); to edit in place, bind `mut x`
(a by-ref, requiring the collection be `mut`). The intended structural equality is that `list`s compare
**in order** and `map`s / `set`s compare **order-insensitively** (insertion order governs iteration, never
equality).

> **[not yet]** Container equality is unbuilt: comparing two `list`s or two `map`s with `==` / `!=` is a
> **loud compile error** today, and `set[T]` does not exist yet. Only **`str ==`** compares. `for mut x`
> over a collection of **non-POD** elements (a `list[str]`, or elements of a recursive / boxed type) is also
> **[not yet]** — over POD elements it is available.

```text
for x in xs { total = total + x }         # read
for mut x in ys { x = x * 2 }             # edit in place — ys must be mut (POD elements today)
```

## Iterating & mutation

Within a `for … in xs`, `xs` is **frozen against structural change** — appending, inserting, removing,
growing/shrinking, or rebinding it inside the loop is a **compile error** — so an iterator can **never be
invalidated** (no dangling cursor, no runtime fail-fast check). This is a **local** rule — the loop knows
the collection it walks — so it needs no borrow checker and costs you nothing at runtime. Editing an
**element** in place (`for mut x`) stays fine: it never moves the cursor.

To transform in place, use a single `mut` method whose internal walk is controlled (`xs.retain(pred)`), or
rebuild (`xs = xs.filter(pred)` — a rebind after the loop). To accumulate while reading `xs`, append to a
**different** collection.

## Fixed-size arrays — `[T; N]`

A **`[T; N]`** is a **fixed-size array** — `N` values of `T` laid out **inline** (on the stack, or within
its enclosing value), with **no heap and no `Ref`**. Its length **`N` is part of the type** and fixed at
**compile time**, so `[int; 3]` and `[int; 4]` are **different types** with no implicit conversion between
them. This is the one thing a `list` cannot be: a `list[T]` is heap-backed and its length is a runtime
value, whereas an array's size is known statically and its storage is inline — which is why an array, not a
`list`, is what maps to a C `T[N]` field (see [FFI](../runtime/ffi.md)) and what you reach for when layout matters.

`N` is a **compile-time constant** — an integer literal, a top-level or **type `const`** (see
[Type constants](../core/specs.md)), or an arithmetic/bitwise combination of those folded by the compiler
(`[int; ROWS * COLS]`). It is never a runtime value and never a **function call**: Zerg does no general
compile-time evaluation, so `[int; f(x)]` is an error.

```text
xs: [int; 4] = [1, 2, 3, 4]     # a list literal, typed as an array by its target — length must be 4
buf := [0; 256]                 # bare := — this is a list[int] of 256, NOT an array [int; 256]
row := [b'\0'; WIDTH]           # WIDTH is a top-level const — also a list under bare :=
```

An array is an ordinary **value**: copy-by-value copies all `N` elements (bumping any contained `Ref`), it
is freed at scope exit, and it never aliases — exactly the container value model. Everything else falls out
of the rules already stated for `list`:

- **Build** — the list literal `[a, b, …]` is **context-typed**: a `list[T]` by default, an array when the
  target is `[T; N]` (its length is checked at compile time). The **fill form `[v; N]`** is intended to make
  **`N` copies of `v`** — the way to build a large collection without spelling every element; there is no
  implicit zero-fill. Under a bare `:=` the fill form builds a **`list[T]`**, not the array type `[T; N]`;
  the array-typed fill form (in explicit `[T; N]` position) is **[not yet]**.

  > **[deviation]** The list fill form currently **re-evaluates `v` on each of the `N` iterations** instead
  > of copying one value `N` times. With a pure constant (`[0; 256]`) this is harmless, but a fill whose
  > element expression has a side effect or observable cost is evaluated `N` times rather than once.

- **Access** — `a[i]` by value, bounds-checked → `IndexError`, with a constant index outside `[0, N)` caught
  at **compile time**; `a.get(i) -> T?` is the checked path. `mut a` edits elements in place (`a[i] = v`) but
  can **never grow or shrink** — the size is in the type; a plain `a` is frozen.
- **Length** — `a.len()` is `N`, itself a compile-time constant.
- **Iterate / derive / slice** — it implements `Iterator` / `Iterable` (`for x in a`; **`for mut x in a`
  over non-POD elements is [not yet]**), and derives **element-wise**: an array derives `Eq` / `Ord`
  (and, when built, `Hash` / `Encode`) exactly when its element type `T` does — two same-type arrays then
  compare (and hash) element-wise. There is **no** blanket auto-derived `Object`; equality comes only from
  `derive(Eq)` on the element. `a.slice(p, q)` is intended to yield a **read-only `list[T]`** view — the COW
  bridge from an array back into the list family — but slicing is **[not yet]** (see [Slicing](#slicing--read-only-subranges)).

## Strings & bytes

`str` is a **distinct immutable primitive**, not a collection — it iterates as `rune` and is **not
indexable**. Bridge through **`list[byte]`** (raw bytes, may hold a NUL) or **`list[rune]`** (code points):
build a string by collecting into a `list` and converting with **`str(...)`**, which **validates** the
`str` invariant (valid UTF-8 from bytes, no embedded NUL) and **raises** on violation — for untrusted
input, `guard { str(bytes) }` demotes that to a `Result[str]` (the checked path from the error model; no
separate constructor). Editing text always yields a **new** `str`.

`str` implements **`Ord`**, **`Hash`**, and **`Add`** — catalogued in the
[Specs & Generics](../core/specs.md) (Built-in specs): it sorts lexicographically by code point, is a natural
`map`/`set` key (being immutable), and `a + b` **concatenates** into a new `str`. Build a string up in a
loop with that list-collect, not by repeated `+`, which would copy the whole
accumulator each step. A `float` implements neither `Ord` nor `Hash`, so it is never a sorted-collection
element or a key. (Rendering a non-text value to text — an `int` to `"42"`, `f"…"` interpolation — is
**[Formatting & Text](../runtime/format.md)**, built on `display`.)

## Deferred

- **`set[T]`** — the unique-membership set is specified above but **[not yet]** built, in both type and
  value position.
- **Container equality** — structural `==` / `!=` on `list` and `map` is **[not yet]**; only `str ==`
  compares today.
- **Slicing** — both `slice(a, b)` and the **`x[a..b]` slice-index sugar** (the range-index form, also a
  grammar/syntax concern) are **[not yet]**.
- **Ordered variants** — a sorted `map`/`set` keyed on `Ord` rather than `Hash`, if wanted.
