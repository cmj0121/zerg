# Zerg Values & Memory

How a value is owned, copied, and freed — scope ownership, copy-by-value, `mut`, `del` / `defer`,
and the `Ref[T]` escape hatch. Part of the [Language Reference](../language.md). Also in
[繁體中文](memory.zh-TW.md).

No garbage collector, no pointer syntax. Every value is **scope-owned** (freed at scope exit) and
passed **by value**. Copy-by-value is the semantics; the compiler elides copies when safe:

> **[deviation]** **What the release logic tracks is a BINDING**, so a heap value that is nobody's binding
> is nobody's to end. Two shapes leak without bound in an ordinary loop, and they are one defect
> ([issue #11](https://github.com/cmj0121/zerg/issues/11)):
>
> - **a temporary handed straight from one call into another.** `strings.count(strings.join(…), "a")` in a
>   500 000-round loop peaks at **25.7 MB**; binding the middle result to a name first peaks at **1.7 MB**.
> - **the old buffer of a field written over.** `b.xs = […]` in a loop peaks at **25.7 MB** over 300 000
>   rounds and **49.9 MB** over 600 000 — it doubles with the loop, so it is unbounded, not a fixed cost.
>
> The two peaks agreeing at 25.7 MB is a coincidence of the round counts each repro chose, not one figure
> written twice; they were measured separately and the 600 000-round doubling is what shows the second is
> a rate rather than a ceiling.
>
> Both are legal programs that run correctly and consume memory forever, which the standing contract —
> _implemented, or refused by name_ — has no third state for. Neither has a case in `make mem-check`, and
> the section on assignment below carries the same defect from the binding side.

- **Single flow** — an immutable value may pass by-ref invisibly; a mutable one falls back to a copy.
- **Across coroutines** — always copied: no shared mutable state, no data races; propagating a change
  back is the caller's job (e.g. via a channel).
- **Extract / return** — unwrap (`?`, `!`), `match`, and `return` copy out; the source is never
  invalidated. Move is only a silent optimization when the source is dead afterward.

Recursive and self-referential types need no pointer — declare the field directly (e.g. `Node?`, or an
`enum Expr { Num(int); Add(Expr, Expr) }`) and the compiler **auto-boxes the self-referential slot behind a
refcounted cell**. A recursive value therefore copies **by reference** (refcount-shared), not by deep clone:
copying bumps the cell's count rather than duplicating the whole chain, and the chain is freed at the last
holder's scope exit.

> **[deviation]** Freeing a chain **recurses one C stack frame per node**, so a chain longer than the
> native stack cannot be freed at all. Measured on a default 8 MiB main stack: **60 000 nodes complete and
> 70 000 do not**, which is about 128 bytes of stack per node. It dies with its name —
> `StackOverflowError: stack overflow`, status 1, from the runtime's fault handler — rather than as a bare
> SIGSEGV, but that is a diagnosis and not a recovery: the free runs on the scope-exit and abort-unwind
> paths, where raising is not safe, so no `guard` can catch it and no `defer` after it runs. An
> **iterative** chain teardown is what this owes, and it is not built.
>
> The **freeing** half of this is closed. The cell's drop is the enum's own,
> a binding registers it where it is declared, and an assignment gives the old value back after
> materialising the new one. Measured over a counting allocator: 200 rounds that each build and drop a
> 2000-node `enum L { Nil; Cons(int, L) }` end with exactly as many live allocations as five rounds do
> (`make mem-check`). The stack-overflow sentence above is the price of that fix: it was unreachable while
> nothing recursed, because nothing was freed.

---

> **[not yet]** A recursive **`struct`** cannot be declared at all, so the deviation above is not reachable
> through one. `struct Node { value: int; next: Node? }` is rejected with _`Node` is part of a cycle of
> by-value declarations — a type holding itself, however indirectly, has no size_: sizing runs over the
> declaration graph before any boxing decision is reached, so the self-referential slot never gets the cell
> that would have given it a size. The recursive **`enum`** is the half that builds, boxing and
> refcount-sharing as described — what it does not do is free, which is the deviation above. The `Node`
> used below — in Copy vs reference semantics, where it is the one place a shared mutation is observable —
> is the specified form and does not compile today. It
> carries a second unbuilt form as well: its **named arguments** (`Node(value: 1, …)`) are `E223`, since
> arguments bind by position here (see [Types](types.md)).

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

> **[deviation]** Two combining forms still hand their operands to one C construct and inherit C's
> unspecified order: an **enum variant's payload** (`E.V(f(1), g(2))`) and a call through a **function
> value**. Each needs two or more effectful operands before the order is observable at all. The built-in
> **`list` / `map` methods** stood here as a third and cannot: the ones that would take two effectful
> operands — `insert`, `set`, `get` — are themselves refused by name, so no such call can be written.
> Everything else named above is ordered.

A form that reads an operand **more than once** is ordered by the same rule, and the trigger is the only
thing that differs. `v in lo..hi` is the one: the membership test is a bounds comparison, so it names `v`
at each bound — and where the run above exempts its first operand, because nothing precedes it, this one
cannot, because `v`'s second reading comes after the bounds. So the subject is evaluated **once**, before
either bound, and the bounds after it in source order: `f() in 1..10` calls `f()` exactly once.

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

> **[deviation]** A carrier owns its **Left** and not its **Right**. Whether a carrier owns anything at all
> is asked of the Left's type alone, so an `Either[int, str]` is judged to own nothing and gets **no copy
> helper and no drop at all** — while the wrap that BUILDS a Right retains its payload. One reference is
> therefore leaked per Right constructed, and copying such a carrier is a bit copy that counts nothing: a
> leak, never a double free, which is why it is invisible under ASan. A `Result[T]` is unaffected — its
> Right is an `Err`, whose storage is the runtime's. What is owed is the same pair over the other side.
>
> The **Left** half of this paragraph is closed. A carrier now has a copy helper, so a binding can register
> the drop the way every other owning type does: `got := <-c` releases its payload at scope exit, and so do
> a carrier passed as an argument, returned, held in a struct field or a `list[T?]` element, and the value
> `if v := <-c { … }` retains into its binding — which was a second leak, and did not need the carrier to be
> named at all. Measured over a counting allocator, 200 rounds against five (`make mem-check`).

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

**Assignment** is a drop too: writing over a binding that owns something frees what it held, and the new
value is built **before** the old one is released — `s = s + x` reads `s` to make its own right-hand side.

> **[deviation]** Only a recursive `enum` and a carrier do that. Assigning over a `str`, a `list`, a `map`,
> a tuple, a struct or a **held function** binding **abandons** the old value, which leaks it — as does
> writing over a **field** (the unbounded shape measured at the head of this chapter), the
> collection a `for … in` over a **map** copies to walk, and the payload a **force-unwrap** copies out:
> `q!` hands back a copy of what the carrier holds, and an expression that reads one field of it discards
> the rest. Measured for the fn value: `mut cur := f` then `cur = g` in a loop leaks two allocations a
> round, the closure's environment and the value it captured. Measured for the force-unwrap: `p: str? = s`
> then `q!` in a loop leaks one a round. Each is the same missing half of one pair rather than a rule of
> its own, and none of them has a case in `make mem-check` yet — which is what a gate not finding something
> looks like.
>
> A **tuple at scope exit** was on that list and no longer is. It had a copy helper and no drop at all, so
> `t := (1, s)` retained the `str` and nothing gave it back; it has a `_drop` beside its `_copy` now, and
> `make mem-check`'s `tuple_heap` counts it. That half was also why `(int, str)?` did not compile at all —
> a carrier decides what to emit from the drop question and its callers name it from the copy question, so
> the type those two disagreed about was named in C and declared nowhere.

A **`spawn`'s captured values are the coroutine's**, not the spawning scope's: the environment takes a
reference of its own for each one and HANDS IT OVER to the coroutine, whose by-value parameters give it
back when the body returns. That is one give-back per capture on every exit, the abort-unwind one included.

> **[deviation]** A coroutine that never runs never gives them back. The environment is filled at the
> `spawn` and released only by the body, so a spawn whose coroutine the scheduler never gets to — a program
> that ends first — leaks a reference per captured value and the environment block with them. It is the one
> leak class in this neighbourhood that has no case anywhere: `make sanitize-conc` runs programs whose
> coroutines all complete, and `make mem-check` has no concurrency case beyond a drained channel.

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

> **Status.** The last row of the table is the one `zerg` does not reach at all. `del` of a `Ref` value is
> **[not yet]** in both of its halves: `del ch` on a channel is refused by name (_E470 NotImplemented:
> `del ch` on a CHANNEL_, which says to write `close(ch)` instead), and there is no `Ref[T]` type here to
> `del` in the first place — naming `Ref` refuses too (`E446`). What the compiler does with a channel is
> release it where its binding's scope ends, so the holder is dropped and `drop` still runs at the last
> one; what is missing is the ability to say so **early**, by name.
>
> ---
>
> `del` of an **owning** value — a local `struct`, `list`, or `map` — to free its storage **early** is
> **[not yet]** for the same reason from the other side: today such a `del` revokes the name's access, but
> the storage is reclaimed at ordinary scope exit rather than at the `del`. The "storage freed" row above
> is thus the intended behavior, not yet the bootstrap's for owning values.

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

> **[not yet]** The paragraph above is the specified rule; `del ch` itself is refused (`E470`, the Status
> note above). The half of it that already holds is the advice: `close(ch)` ends a stream and scope exit
> releases the hold, and those are what a program writes today.

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

Three constructs share one axis — _when_ cleanup fires: `del` revokes a name **now**; `defer` fires at
**this block's** exit (in the order Drop order gives it); a `Ref[T]` drop fires at the **last holder's**
exit. The dividing line is a single question — does the resource escape its scope?
**No → `defer`; yes → `Ref[T]`.**

A **`with` block** scopes such a resource to a lexical region — and it is **purely syntactic** sugar over
the bare block that already does so: `with acquire() as y { … }` is `{ y := acquire(); … }`, and the
nameless `with e { … }` still binds, to a name only the compiler writes, because `e; …` would end the
value's life at that statement instead of at the `}`. It introduces **no fourth mechanism**: what runs the
release is the axis above, unchanged, and that axis already covers every exit including an abort.

A resource whose release is a **method someone must remember to call** is not a `with` case at all — it is
a `defer`, written out, in the block `with` just opened.

`with` is **built**, and it is exactly the expansion above and nothing else — it carries no teardown of
its own. `examples/18_scoped.zg` is the shipped demonstration.
