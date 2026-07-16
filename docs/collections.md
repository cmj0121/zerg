# Zerg Collections

Zerg's built-in containers — **`list`**, **`map`**, **`set`** — one canonical type per role, no variant
zoo. They are ordinary **scope-owned values**, built on the [Language Reference](language.md). Also in
[繁體中文](collections.zh-TW.md).

| Type        | Role                        | Element / key requirement | Iteration order     |
| ----------- | --------------------------- | ------------------------- | ------------------- |
| `list[T]`   | an **ordered sequence**     | any `T` (no bound)        | index order         |
| `map[K, V]` | an **associative** table    | `K: Hash`                 | **insertion** order |
| `set[T]`    | a **unique-membership** set | `T: Hash`                 | **insertion** order |

Richer shapes are compositions, not new built-ins. `list[byte]` is the raw byte sequence (indexable, may
hold a NUL); `str` stays a separate immutable primitive (below).

## Values, not references

A collection is a **scope-owned value**: **copy-by-value** (the compiler elides or moves when safe), freed
at scope exit, **no aliasing** — copying deep-copies elements and refcount-bumps any contained `Ref` value (a
channel or `Ref[T]`), the existing memory rule. There is no shared container behind two names: share for
**reading** by immutable pass, for **mutation** by a `mut` parameter; a collection sent over a channel is
copied like any payload.

## Mutability — one per-binding knob

Mutability is the ordinary **per-instance** axis: a **single knob** unlocking _both_ content edits and
rebinding — the Rust `let mut` / Swift `var` model, not a variable-vs-elements split.

- **`mut xs`** — may **edit elements** (`xs[i] = v`), **grow/shrink** (append, insert, remove), and
  **rebind** (`xs = other`). Edits and growth are `mut self` methods, like a struct's mutators.
- **plain `xs`** — **fully frozen**, Zerg's fixed array. (You may still `:=` re-declare the name — a _new_
  binding, the old one `del`-ed — never a mutation.)

So one `list` type is both fixed array (plain) and growable vector (`mut`); **only a `mut` collection can
modify its elements**.

```text
xs := [1, 2, 3]            # frozen: xs.append(4) and xs[0] = 9 are errors
mut ys := [1, 2, 3]
ys.append(4)               # grow  ·  ys[0] = 9  # edit  ·  ys = [2, 4]  # rebind
```

## Keys — `equal` free, `Hash` explicit

`list[T]` takes **any** `T` (only the structural ops every value has). A `map` key / `set` element needs
**`Hash`** (keys compare by `equal`). The halves are deliberately asymmetric: `Object` **auto-derives `equal`**,
but **`Hash` is not derived — a type implements it explicitly** to be a key, keeping "what may be a key" an
opt-in, `safe by default` choice. The author owns the contract the compiler can't check: **equal ⇒ same
hash**. Because a key is **copied in** as a frozen snapshot, even a `mut` collection is usable as one.

## Access — `[]` asserts, `.get` checks

Indexing mirrors the force-vs-check split of `!` / `?`:

- **`xs[i]` / `m[k]`** — the element **by value**; **aborts** on a bad index or missing key
  (`IndexError` / `KeyError`). A bad index is a **bug**, like overflow.
- **`xs.get(i)` / `m.get(k)`** — the checked path → **`T?`** / **`V?`**, for expected absence.
- **`x in s` / `k in m`** → `bool`; on a `mut` collection **`xs[i] = v`** sets in place.

```text
first := xs[0]                 # aborts if empty
name  := m.get(id) ?? "anon"   # checked, then default
```

## Order & equality

`list` walks in index order; `map`/`set` in **insertion order** — deterministic, no hash-ordering surprise.
Iterating reads each element **by value** (elidable to read-only by-ref); to edit in place, bind `mut x`
(a by-ref, requiring the collection be `mut`). Equality is structural: `list`s compare **in order**,
`map`s/`set`s **order-insensitively** (insertion order governs iteration, never equality).

```text
loop x in xs { total = total + x }        # read
loop mut x in ys { x = x * 2 }            # edit in place — ys must be mut
```

## Strings & bytes

`str` is a **distinct immutable primitive**, not a collection — it iterates as `rune` and is **not
indexable**. Bridge through **`list[byte]`** (raw bytes, may hold a NUL) or **`list[rune]`** (code points):
build a string by collecting into a `list` and converting (`str(...)`); editing text means a **new** `str`.

The `Ord` / `Hash` specs (and `Object`'s `equal`) behind these rules are catalogued in the
[Language Reference](language.md) (Built-in specs); a `float` implements neither `Ord` nor `Hash`, so it
is never a sorted-collection element or a key.

## Deferred

- **Ordered variants** — a sorted `map`/`set` keyed on `Ord` rather than `Hash`, if wanted.
