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
(see [Keys](#keys--eq-free-hash-explicit) below). The two **[not yet]** rows each name themselves: `set[T]`
in either type or value position is _E466 NotImplemented: the built-in `set`_, and `[T; N]` is _E233
NotImplemented: an array type `[T; N]` — this compiler has `list[T]`, whose length is not part of its type_.

Richer shapes are compositions, not new built-ins. `list[byte]` is the raw byte sequence (indexable, may
hold a NUL); `str` stays a separate immutable primitive (below).

## Values, not references

A collection is a **scope-owned value**: **copy-by-value** (the compiler elides or moves when safe), freed
at scope exit, **no aliasing** — copying gives the holder **its own** elements and **retains**
(refcount-bumps) any
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

> **[not yet]** Of the growth methods named above, only `append` is built: `insert` and `remove` are each
> refused by name on both `list` and `map` (_E444 NotImplemented: the list method `insert` — this compiler
> has `len` and `append`_), so a collection grows at its end and does not shrink at all.

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
> key is restricted to **`int`** or **`str`**: anything else is _E431 NotImplemented: a map key of type … —
> a key needs `Hash`, and this compiler has one for `int` and for `str`_. `derive(Hash)` and general keyed
> types are not built.

## Access — `[]` asserts, `.get` checks

Indexing mirrors the force-vs-check split of `!` / `?`:

- **`xs[i]` / `m[k]`** — the element **by value**; **aborts** on a bad index or missing key
  (`IndexError` / `KeyError`). A bad index is a **bug**, just like overflow.
- **`xs.get(i)` / `m.get(k)`** — the checked path → **`T?`** / **`V?`**, for when you expect absence.
- **`x in xs` / `x in s` / `k in m`** → `bool`; on a `mut` collection **`xs[i] = v`** sets in place. A list
  is **scanned** and a map **hashes** — same question, different cost — and the value looked for meets the
  element type like a value entering any other [typed position](../core/types.md#typed-positions), so
  `72 in bytearray(…)` is a byte.

```text
first := xs[0]                 # aborts if empty
name  := m.get(id) ?? "anon"   # checked, then default
```

> **[not yet]** The checked path does not exist: `xs.get(i)` and `m.get(k)` are both `E444`, so
> the `m.get(id) ?? "anon"` line above does not compile and indexing — which aborts — is the only way into
> a container. Expected absence is therefore not a question a program can ask; it is one it has to head off
> with `k in m` before indexing.

## Slicing — read-only subranges

A **subrange** — `xs.slice(a, b)`, the elements `[a, b)` — is an ordinary **read-only `list[T]` value**,
not a borrow: it never writes back into its parent, so there is **no aliasing** and no borrow checker, and
it obeys the same copy-by-value model as any collection. The compiler may realize that copy as
**copy-on-write** — sharing the parent's backing storage until either side is mutated, then copying — so
value semantics hold while the read-only case stays **zero-copy**; COW is an unobservable optimization
alongside copy-elision and the move (Values & Memory), adding no visible sharing, only a cheaper `copy`.

So a lexer scans by index (`xs[i]` is O(1)) and takes read-only `slice` windows at no copy cost,
materializing a `str` only when it keeps a token.

> **[not yet]** The **method** spelling is what is unbuilt: `xs.slice(a, b)` is `E444`. The
> **`x[a..b]`** slice-index sugar is built and correct — `xs[1..3]` yields a fresh two-element `list`,
> `xs[0..=2]` a three-element one, each an independent value — so a subrange is written with the bracket
> form until the method lands. The read-only, copy-on-write design above is the intended semantics of both.

## Order & equality

`list` walks in index order; `map`/`set` in **insertion order** — deterministic, no hash-ordering surprise.
Iterating reads each element **by value** (elidable to read-only by-ref); to edit in place, bind `mut x`
(a by-ref, requiring the collection be `mut`). The intended structural equality is that `list`s compare
**in order** and `map`s / `set`s compare **order-insensitively** (insertion order governs iteration, never
equality).

> **[not yet]** Container equality is unbuilt: comparing two `list`s or two `map`s with `==` / `!=` is
> _E445 NotImplemented: `==` on a list[int] — structural equality over a container is unbuilt, and a
> container has no declaration to derive it on_. Only **`str ==`** compares. `for mut x` over a collection
> is **[not yet]** for **every** element type, POD included:
> `for mut x in ys` is `E242` whatever `ys` holds, so the second line of the example below is not a program.

```text
for x in xs { total = total + x }         # read
for mut x in ys { x = x * 2 }             # edit in place — [not yet], refused for every element type
```

## Iterating & mutation

Within a `for … in xs`, `xs` is **frozen against structural change** — appending, inserting, removing,
growing/shrinking, or rebinding it inside the loop is a **compile error** — so an iterator can **never be
invalidated** (no dangling cursor, no runtime fail-fast check). This is a **local** rule — the loop knows
the collection it walks — so it needs no borrow checker and costs you nothing at runtime. Editing an
**element** in place (`for mut x`) stays fine: it never moves the cursor — though that binding is itself
**[not yet]** (see [Order & equality](#order--equality)).

> **[deviation]** The freeze sees only a **bare name**. `for x in xs { xs.append(x) }` and
> `for x in xs { xs = [9] }` are compile errors (`E393`), but the same structural change reached through a
> **path** is not: `for x in p.xs { p.xs.append(v) }`, `for x in p.xs { p.xs = [9] }`,
> `for x in xs[0] { xs[0].append(v) }`, and a function taking `mut &xs` and appending inside the loop all
> compile today and really grow or rebind the collection being walked. No iterator is invalidated — the
> loop walks a copy-on-write copy taken at its head, so the program stays memory-safe — but the **compile
> error this section promises does not arrive**: the loop silently walks the collection as it was, rather
> than saying so. Only the bare-name spelling is enforced.

To transform in place, use a single `mut` method whose internal walk is controlled (`xs.retain(pred)`), or
rebuild (`xs = xs.filter(pred)` — a rebind after the loop). To accumulate while reading `xs`, append to a
**different** collection.

> **[not yet]** Neither alternative exists: `xs.retain(pred)` and `xs.filter(pred)` are both `E444`, so a
> transform is written as a `for` that appends into a second `list`, and a rebind after the loop.

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

  The **count is the same compile-time constant** an array length is — a literal, a name whose binding
  folds (module-level or local), or the arithmetic over them: `[0; 256]`, `[0; ROWS * COLS]` and
  `[b'\0'; WIDTH]` are one form. A count that does not fold — a value read at run time, a call — is an
  error **at the fill**, not at the binding it names, because the fill is the line that wanted a
  compile-time value; so is a negative one, since a count is how many copies to make.

  `v` is evaluated **once** and copied `N` times, which is what "N copies of v" means: a fill whose element
  expression has a side effect or an observable cost runs it once, not `N` times. Every copy after the first
  is a real copy, so a fill of a `str` or a `list` gives `N` independent elements rather than `N` slots
  sharing one.

- **Access** — `a[i]` by value, bounds-checked → `IndexError`, with a constant index outside `[0, N)` caught
  at **compile time**; `a.get(i) -> T?` is the checked path. `mut a` edits elements in place (`a[i] = v`) but
  can **never grow or shrink** — the size is in the type; a plain `a` is frozen.
- **Length** — `a.len()` is `N`, itself a compile-time constant.
- **In a signature** — a function is generic over the length through a **value generic**,
  `fn sum[N: int](xs: [int; N])`, with `N` inferred from the argument and never written at the call site.

  > **[not yet]** A value parameter is refused — _E266 NotImplemented: a value generic parameter `N: int`_ — so a
  > function today takes one concrete length (`[int; 4]`) and nothing else, and a routine over arbitrary
  > lengths takes a `list[T]` instead.

- **Iterate / derive / slice** — it implements `Iterator` / `Iterable` (`for x in a`; **`for mut x in a` is
  [not yet]** for every element type, POD included), and derives **element-wise**: an array derives `Eq` / `Ord`
  (and, when built, `Hash` / `Encode`) exactly when its element type `T` does — two same-type arrays then
  compare (and hash) element-wise. There is **no** blanket auto-derived `Object`; equality comes only from
  `derive(Eq)` on the element. `a.slice(p, q)` is intended to yield a **read-only `list[T]`** view — the COW
  bridge from an array back into the list family — but the `slice` **method** is **[not yet]** (see
  [Slicing](#slicing--read-only-subranges)).

## Strings & bytes

`str` is a **distinct immutable primitive**, not a collection — it iterates as `rune` and is **not
indexable**. Bridge through **`bytearray(s)`** (raw bytes, may hold a NUL) or **`runearray(s)`** (code
points). Each names a list rather than a new type: **`bytearray` IS `list[byte]`** and **`runearray` IS
`list[rune]`** — interchangeable with the spelled-out form in every position, and **not** a strong typedef
(`type X = Y`, [Types](../core/types.md)). The set is **closed** at these two. Going the other way, build a
string by collecting into a `list` and converting with
**`str(...)`**, which **validates** the
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

Everything marked **[not yet]** above is deferred with the feature it names. One shape is deferred without
being specified anywhere else: an **ordered variant** — a sorted `map` / `set` keyed on `Ord` rather than
`Hash` — if the need proves real.
