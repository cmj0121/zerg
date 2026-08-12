# Zerg Values & Memory

How a value is owned, copied, and freed — scope ownership, copy-by-value, `mut`, `del` / `defer`,
and the `Ref[T]` escape hatch. Part of the [Language Reference](../language.md). Also in
[繁體中文](memory.zh-TW.md).

No garbage collector, no pointer syntax. Every value is **scope-owned** (freed at scope exit) and
passed **by value**. Copy-by-value is the semantics; the compiler elides copies when safe:

- **Single flow** — an immutable value may pass by-ref invisibly; a mutable one falls back to a copy.
- **Across coroutines** — always copied: no shared mutable state, no data races; propagating a change
  back is the caller's job (e.g. via a channel).
- **Extract / return** — unwrap (`?`, `!`), `match`, and `return` copy out; the source is never
  invalidated. Move is only a silent optimization when the source is dead afterward.

Recursive and self-referential types need no pointer — declare the field directly (e.g. `Node?`, or an
`enum Expr { Num(int); Add(Expr, Expr) }`) and the compiler **auto-boxes the self-referential slot behind a
refcounted cell**. A recursive value therefore copies **by reference** (refcount-shared), not by deep clone:
copying bumps the cell's count rather than duplicating the whole chain, and the chain is freed at the last
holder's scope exit. Two bounded MVP artifacts hold this phase: a runtime **cycle** built by
reassigning a recursive field through a `mut` binding **leaks**, as there is no cycle collector yet
(**[deviation]**); and freeing a long chain **recurses O(depth) on the native C stack** and can overflow it
(**[deviation]** — the same unrecoverable stack-overflow deviation catalogued in
[Conformance](../conformance.md) and [Errors](../code/errors.md)).

> **[not yet]** A recursive **`struct`** cannot be declared at all, so neither artifact above is reachable
> through one. `struct Node { value: int; next: Node? }` is rejected with _`Node` is part of a cycle of
> by-value declarations — a type holding itself, however indirectly, has no size_: sizing runs over the
> declaration graph before any boxing decision is reached, so the self-referential slot never gets the cell
> that would have given it a size. The recursive **`enum`** is the half that works, boxing and
> refcount-sharing exactly as described. The `Node` used below — in Copy vs reference semantics, where it is
> the one place a shared mutation is observable — is the specified form and does not compile today.

**A `struct`'s layout is its declaration.** Fields sit in **declaration order**, the value is laid out
**inline** in its owner (no indirection beyond the recursive auto-boxing above), and the compiler **never
reorders** them — so a Zerg `struct` _is_ a C `struct`, field for field, at natural alignment with standard
padding. This falls out of transpiling to C, and it is what makes a struct **FFI-ready by default** (see
[FFI](../runtime/ffi.md)): there is no separate "optimized" layout to opt out of, so Zerg needs no `repr(C)` marker. (A
sum type's payload is likewise inline; only the exact C encoding of its discriminant is a deferred FFI
detail.) Tighter control — dropping padding (**packed**) or forcing a wider **alignment**, for wire formats
and memory-mapped hardware — is a niche knob, **deferred** until a concrete need.

Mutability belongs to the **instance** — the binding — not the type or any field: `mut x := …` makes
the whole constructed instance mutable (every field), a plain `x := …` keeps it immutable; a field
carries only visibility (`pub` or private). Zerg has no general reference; code shares storage only
through:

- **Mutable-ref parameter** (`mut &` param) — the one explicit by-ref path: the callee mutates the
  caller's (`mut`) variable in place. It is confined to the call — value positions (field, `return`,
  channel send) copy its current value, it can only pass onward to another `mut &` param, and it cannot
  cross a `spawn`. **Two `mut &` arguments never share storage** — a guarantee the callee relies on:
  static aliasing (`f(x, x)`) is a **compile error**, and where the compiler cannot prove it
  (`f(xs[i], xs[j])` with `i == j` at runtime) the call **aborts** (`AliasError`). A check is
  inserted only where `mut &` arguments could dynamically alias.
- **Channels** — shared by ref across coroutines, for communication only.

> **[not yet]** There is no run-time `AliasError`, and no run-time check of any kind: the compiler decides
> aliasing **statically and conservatively**, and two `mut &` arguments drawn from the same variable are
> rejected whatever the indices say. So the provably distinct `two(xs[0], xs[1])` is refused outright with
> _`xs` is given to two `mut &` parameters of `two` in one call — a borrow may not alias, which is what keeps
> it safe without a borrow checker_. The guarantee the callee relies on does hold, and it holds by **rejecting
> legal programs**: the specified rule accepts this call and aborts only where the indices really do meet.

**Evaluation order is left-to-right.** Function arguments, operator operands, and the elements of a
`list` / `map` literal or a `set(...)` constructor evaluate **in source order**, deterministically — so a
side effect (a `mut &` argument, an abort) sequences predictably, unlike C, whose argument-evaluation order
is unspecified.

Binary operands, a call's arguments, a method call's receiver-and-arguments, and a struct construction's
fields are each **sequenced** where the order is observable: an operand after the first that can run code
makes the whole list evaluate into temporaries, in source order. An operand that cannot run code — a
literal, or a plain name read — is left where it stands, so the common `f(g())` and `x + 1` are unchanged.
The **short-circuit** operators — `and`, `or`, `??`, `?.`, and the `?` unwrap — are left-to-right in the
stronger sense that the right side is **skipped** when the left decides the result.

> **[deviation]** Three combining forms still hand their operands to one C construct and inherit C's
> unspecified order: an **enum variant's payload** (`E.V(f(1), g(2))`), the built-in **`list` / `map`
> methods**, and a call through a **function value**. Each needs two or more effectful operands before the
> order is observable at all. Everything else named above is ordered.

---

> **[deviation]** `v in lo..hi` evaluates `v` **more than once** — two or three times, depending on the
> bounds — because the membership test is inlined as a bounds comparison rather than built as a range. So
> `f() in 1..10` calls `f()` repeatedly. This is a repeated evaluation rather than a misordered one, and it
> is the same defect in both compilers.

**Reference-counted values** are scope-owning's one exception: a value whose type implements **`Ref`** —
the built-in **`chan`**, or a stdlib **`Ref[T]`** box — is shared **by reference**, not copied. The
runtime counts holders and frees it at the **last** holder's scope exit; everything else stays pure
scope-owned, no GC/refcount. Copying a value refcount-bumps any `Ref` value it (transitively) contains
and deep-copies the rest; a `Ref` value is shared, never duplicated. (As an **implementation detail**,
runtime `str` values the program produces are likewise refcounted internally and freed at last use — no
surface change, but produced strings no longer leak.)

For the **explicit** ref-counted values, **refcounting is cycle-complete by construction**, so they need no
cycle collector and no weak reference: a `Ref[T]`'s referent is **fixed when the box is built** (to point
elsewhere, build a new `Ref`), and with values immutable by default and constructed bottom-up there is no way
to make an existing `Ref` point back at a later one — a reference cycle can never form, so the last-holder
free is always complete. (The lone pathological case — a `chan` buffering a reference to itself — is a
programmer error, not a checked one.) The one exception is the **auto-boxed recursive cell** above: because a
`mut` recursive field can be reassigned into a back-edge, a cycle _can_ form there — and, this phase, is **not
collected and leaks** (a bounded, documented MVP gap, the cost of allowing self-referential types directly).

## Copy vs reference semantics

Whether two names share storage is decided by one line, drawn between two disjoint categories:

- A **value type** — every scalar, a `struct`, a tuple, and the heap containers `list` and `map` — is
  **copied**. A scalar, `struct`, or tuple is copied inline; a `list` or `map` is copied **elementwise**, by
  this same rule. Two names of a value type therefore **never alias**: writing through one changes only that
  holder's own copy.

  A `list`'s buffer realizes that copy as **copy-on-write** — the copy shares it and the elements are
  duplicated by whichever holder writes first. That is an **implementation detail**: no program can tell,
  because the duplication happens before any write the other holder could see. What it buys is that passing
  a collection to a function that only reads it, or handing one to a coroutine, costs an increment rather
  than the whole buffer.

- A **reference-counted value** — a `str`, a `chan`, a `Ref[T]`, and the **auto-boxed sub-nodes of a
  recursive type** — is **shared**: copying retains the existing cell (refcount++) instead of duplicating
  it, and the last holder frees it. A mutation reachable **through a shared recursive tail is therefore
  visible via every holder** of that tail.

> **[deviation]** A reference-counted value that **enters a carrier** — the `T?` a channel receive
> answers, a `Result[T]` — is **never released**. The drop exists and nothing calls it: a carrier has no
> copy helper, so registering the drop would let two names for one value each give it back. Everything
> refcounted crossing a `chan[T]` leaks one reference per value, which is invisible for a `chan[int]` and
> real for a `chan[str]` — measured under LeakSanitizer with `test-data/codegen/chan_str_shared.zg`, the
> first case in this tree to send one.

Copying a composite applies the rule field by field — its value-type parts are copied and any
reference-counted part it contains (transitively) is retained. Because a `str` is immutable and a
`Ref[T]`'s referent is fixed at construction, the only place a shared mutation is observable is the
auto-boxed cell of a **`mut` recursive** binding:

```text
mut a := [1, 2, 3]                # value type
b := a                            # a copy — b is an independent list
a[0] = 9                          # a is [9, 2, 3]; b stays [1, 2, 3] — no alias

struct Node { value: int; next: Node? }
mut n := Node(value: 1, next: Node(value: 2, next: nil))
m := n                            # the struct is copied; its boxed `next` tail is refcount-shared
n.next!.value = 99                # reaches the shared tail — m.next!.value reads 99 too
```

## Drop order

At scope exit, locals drop in **reverse construction order** — last constructed, first freed — so teardown
mirrors setup. The order is pinned **inside an aggregate** too: a `struct`'s fields and an
`enum` payload's slots drop in **reverse declaration order**. A `defer` runs at block exit
on **every** path, **including the abort-unwind path**, and several `defer`s in a block run
**last-scheduled-first (LIFO)**, interleaved with the scope-owned frees and `Ref` drops of that same reverse
order.

## `Ref[T]` — a resource that outlives its scope

> **[not yet]** There is no `Ref[T]` in this compiler. `Ref(5)` is refused by name —
> _NotImplemented: a refcounted box `Ref(x)` / `deref(r)` — this compiler has no `Ref[T]` type_ — so this
> section, the `mut`-versus-effect distinction under it, and every mention of a `Ref[T]` elsewhere on this
> page describe a type nothing can construct. The **machinery** is built and works: the `Ref` spec has one
> implementer, the built-in `chan`, which is shared by reference, counted, and closed at the last holder's
> scope exit exactly as specified. What is missing is the second implementer — the stdlib box that carries an
> arbitrary value together with a user-written `drop` — so a resource that must escape its scope has, today,
> no answer at all rather than the one this section gives.

Most cleanup is just memory, which scope exit frees automatically. A **resource whose release is not that
automatic free** — a foreign handle (see [FFI](../runtime/ffi.md)), anything that must be closed **exactly once** — and
that must **escape the scope that opened it** (returned, stored in a field, sent over a channel) is held
in a **`Ref[T]`**: a reference-counted box carrying the value and a `drop` action. Because it copies
**by-ref**, every copy names **one** resource, and `drop` runs **once**, at the last holder's scope exit
(or an explicit `del`). This is the guarantee a bare copy-by-value handle cannot give — two copies of a
plain handle would each try to free the one resource. Reach for `Ref[T]` **only when the resource
escapes**; a resource confined to one scope wants `defer` (below).

### `mut` is for owned fields — a handle's state is an effect

`mut` (and a `mut fn` method) track a change to a value's **own Zerg-owned fields**. The state _behind_ a
`Ref[T]` — a foreign handle's internal state: an OS file's position, a socket, a database cursor — is **not
part of the Zerg value's bytes**. It belongs to the resource, reached through a handle that copy-by-ref
never duplicates and construction fixes in place. Touching it is an untracked **effect** (like any I/O),
**not a mutation** — so a method that advances it needs no `mut`, and its receiver may be **immutable**: an
immutable `File` can still `read()` and advance its cursor, exactly as a C `const FILE*` can `fread`.

This is a real modelling choice. Put mutable state you **own** in a plain field and change it with a
`mut fn` on a `mut` binding — tracked, and subject to the no-aliasing guarantee above. Put a resource whose
internal state the **foreign side owns** behind a `Ref[T]` — an immutable handle with effectful methods,
needing no `mut`. The dividing question mirrors the `defer`-vs-`Ref[T]` one: **are you changing your own
bytes, or reaching state a handle owns?** Zerg has **no interior mutability** for owned fields — the same
"immutable by default, a `Ref`'s referent fixed at construction" that keeps refcounting cycle-free also
keeps an immutable binding honestly immutable.

## Re-declaration & shadowing

A name may be **re-declared** — in the same block or a nested one — and the new binding may differ in
type and mutability from the old. Re-declaration is **declare-del-declare**: the right-hand side is
evaluated first (so it may read the _old_ binding), then the old binding is `del`-ed (see below), then
the name is bound afresh.

```text
x := read()          # immutable
x := parse(x)        # RHS reads the old x; the old x is del-ed; the name rebinds to the new value
mut x := x           # shadow again — now mutable, seeded from the previous copy
```

A `const` takes no part in this: it is shadow-proof in either direction ([`GRAMMAR`](../../GRAMMAR)
group 4), so a re-declaration may neither take a `const`'s name nor mint a `const` over a name any
visible binding holds — the same block included.

Because the old binding is dead the instant the RHS finishes, `x := transform(x)` needs no copy — the
source is provably dead, so the move optimization applies and the old storage is reused.

## `del` — explicit early release

`del name` **revokes that name's access to its storage** before the scope ends. Freeing the storage is
only a _consequence_: it happens when the revoked access was the **owning** one and no other holder
remains; otherwise `del` merely ends this name's (or this borrow's) access early and the owner keeps
the storage.

| `del` target                          | Own? | Effect                                                          |
| ------------------------------------- | ---- | --------------------------------------------------------------- |
| local, by-value param, captured copy  | yes  | last access → **storage freed**                                 |
| `mut &` param (borrows caller's var)  | no   | ends this call's borrow → **not freed**; caller keeps it        |
| captured value, inside a closure body | no   | ends **this invocation's** access only; next call still has it  |
| channel, `Ref[T]`                     | ref  | revokes the name, drops a holder (refcount--); last one `drop`s |

> **Status.** `del` of a `Ref` value — a `chan`; there is no `Ref[T]` here, above — dropping a holder (and
> running `drop` at the last one) works. `del` of an **owning** value — a local `struct`, `list`, or `map` —
> to free its storage **early** is **[not yet]**: today such a `del` revokes the name's access, but the
> storage is reclaimed at ordinary scope exit rather than at the `del`. The "storage freed" row above is
> thus the intended behavior, not yet the bootstrap's for owning values.

`del` can never dangle: revoking a borrow cannot free storage another name owns, and Zerg's existing
rules already stop an owner from outliving-then-freeing under a live borrower (a `mut &` parameter is
confined to its call; an escaping closure owns copies of its captures). The compiler knows statically
whether each `del` frees or merely revokes — only `Ref` values (channels and `Ref[T]`) carry a runtime
refcount.

`del` is **flow-consistent**: once a name is `del`-ed on any path, it is treated as dead on _every_
subsequent path (no runtime drop flags). A `del` inside one arm of an `if` therefore makes the name
unusable after the merge, symmetrically with the other arms.

**A channel is no exception.** `del ch` drops your hold _and_ revokes the name, so `ch` is unusable
afterwards — a later `ch <- v` or `<-ch` is a compile error (_`ch` is used after del_). It is therefore
**not** the way to signal "no more values": to end a stream while keeping the handle, use the channel-only
statement **`close(ch)`**; to end it by scope, let the binding's scope exit release what it holds. Both are
in [Coroutines](../code/coroutine.md). Use `del ch` when you are finished with the **name** as well.

## `defer` — cleanup at block exit

`defer expr` schedules `expr` to run when the enclosing **block** exits — on **every** path out,
**including an abort unwind**. It is the procedural tool for an effect bound to a scope — release a lock,
flush a buffer, close a scope-local resource — needing no type at all:

```text
{
    lock.acquire()
    defer lock.release()     # runs on every exit — normal, early return, or an abort inside risky()
    risky()
}
```

Several `defer`s in a block run **last-scheduled-first**, interleaved with scope-owned frees and `Ref`
drops in reverse construction order, so teardown mirrors setup. Three constructs share one axis — _when_
cleanup fires: `del` revokes a name **now**; `defer` fires at **this block's** exit; a `Ref[T]` drop fires
at the **last holder's** exit. The dividing line is a single question — does the resource escape its
scope? **No → `defer`; yes → `Ref[T]`.**

A **`with` block** scopes such a resource to a lexical region — and it is **purely syntactic** sugar over
the bare block that already does so: `with acquire() as y { … }` is `{ y := acquire(); … }`, and the
nameless `with e { … }` still binds, to a name only the compiler writes, because `e; …` would end the
value's life at that statement instead of at the `}`. It introduces **no fourth mechanism**: what runs the
release is the axis above, unchanged, and that axis already covers every exit including an abort.

A resource whose release is a **method someone must remember to call** is not a `with` case at all — it is
a `defer`, written out, in the block `with` just opened.

`with` is **built**, and it is exactly the expansion above and nothing else: the block, the binding, and
whatever `defer` the body writes for itself. It carries no teardown of its own — a `with` that frees
something frees it because a `defer` in the body says so, or because the value is scope-owned like any
other. `examples/18_scoped.zg` is the shipped demonstration.
