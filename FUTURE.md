# Zerg — the doors left open

English | [繁體中文](FUTURE.zh-TW.md)

This is the list of things Zerg **decided not to have**, and what it would take to reopen each one.

It is not a roadmap. A roadmap says what is coming; this says what was weighed and declined, so that
reopening a case means answering the argument that closed it rather than re-running the discussion. Every
entry names the **threshold** — what would have to be true — because a door with no threshold is not a
door, it is a wish.

The [specification](docs/conformance.md) is where the language is. Nothing here is part of it.

## `dyn` — runtime existential dispatch

**Status: closed as redundant.**

Both encodings of an existential are already in the core language. The **open** one is a closure — a
struct of function values over a captured implementer, which is what `#[obj]` generates
([Specs & Generics](docs/core/specs.md)). The **closed** one is an `enum`, whose `match` gives back the
concrete type. `dyn` would be a third spelling of the first, with a vtable where the closure already is.

**If it were reopened**, it would be a **type-position keyword** and a **fat pointer at the boundary** —
Go's model — and never a decorator: what a value's type IS cannot be spelled by an annotation beside a
declaration. Two things stay excluded regardless:

- **A per-instance header is permanently out.** Every value would pay for a use site that P1 measures at
  zero; it does not solve heterogeneous size (so a box comes back, and metadata belongs on the box);
  every value-semantics precedent goes the other way (Go and Swift put it on the boundary, C++ makes it
  opt-in, Java and Objective-C are reference languages where the question does not arise); and a
  descriptor beside the emitter's static copy and drop is a second copy of a decision.
- **Object safety, if it ever matters**: `Self` in parameter position is forbidden, and in return position
  must box. That is the same matrix `#[obj]` refuses by today, which is the evidence the encoding is the
  same one.

**Threshold: a measurement.** A use case where the set of types is decided after the compiler has left,
inside the hunting ground — not a plugin (that is a process boundary, and `zerg lsp` is the proof), not
runtime introspection, not a binary-stable SDK (that is the C ABI).

## `#[derive(Reflect)]` — a type's structure, as a value

**Status: open, unbuilt, and desugarable.**

An opt-in per-type constant describing the type's fields and their types, generated on request. Opt-in is
the whole design: the knowledge is the compiler's already ([Derive](docs/core/derive.md)), and marking a
type is what makes it pay.

**Threshold: a caller.** Serialization is the obvious one and `Encode`/`Decode` may well answer it without
a general description; the entry stays because a description that several derives share is cheaper than
several derives that each read structure.

## `#[derive(From)]` — an error enum that wraps its variants

**Status: open, and the one candidate with a measured demand.**

The gap this language chose to keep is **open error downcast** — a value whose error type is decided by a
layer the compiler cannot see. The answer taken instead is a **per-layer error enum**, and the cost of
that answer is the wrapping written by hand at every boundary. `#[derive(From)]` is that wrapping:

```zerg
#[derive(From)]
enum AppError {
    Io(IoError)
    Parse(ParseError)
}
```

generating the conversion each variant implies, so `?` at a boundary lifts a layer's error into the
caller's without a `match` written for it.

**Threshold: none — this is a candidate to build**, not a case to reopen. It is listed here beside the
closed doors because it is the same kind of decision, read the other way: the wrapping is a cost the
language chose, and this is the sugar that would pay it.

## `f.[T]` — explicit type arguments at a use site

**Status: closed. A postfix bracket is an index.**

`id[int](7)` reads as an index of `id`, and telling that subscript from a type argument list means knowing
what `id` is — a symbol table, in the parser, for one form. A generic takes its type from its arguments,
and where inference is not enough a **typed binding** steers it (`xs: list[int] = empty()`).

**If it were reopened**, the spelling would have to be one no index can be, and `f.[T]` is the placeholder
that has always been kept for it — a `.` before the bracket, which no index has. It is a placeholder and
not a plan.

**Threshold: an inference failure a typed position cannot fix.** None has been found; the one that looked
like it — a type parameter appearing only in the return — is answered by the binding's type.

## A depth-checked stack overflow

**Status: closed as a fault.** Opened as a door by 0.2.0, which moved the specification onto it.

A stack overflow is a **fault, not an abort**: the runtime names it and the process exits `1`, but the
pending `defer`s are skipped, no `guard` can catch it, and it ends the whole process rather than the one
coroutine ([Errors](docs/code/errors.md), [Conformance](docs/conformance.md)). The reason is not an
omission — it is arithmetic. The stack that would run those `defer`s is the one that is exhausted, and a
signal handler standing on it cannot unwind what it is standing on.

The specification asked for the other thing until this release: a runtime that **owns every stack** and
**checks call depth itself**, so the overflow is caught one frame before the fault and unwinds cleanly.
That is Go's model and it is a real design; what it is not is a property a runtime can add to a native
stack after the fact. It needs the runtime to allocate and grow the stacks it runs on, and a check on
every call that could exceed one — which is a cost every call pays for a case almost none reaches.

**If it were reopened**, the check belongs where the frame size is known — the prologue the compiler
emits — and not in the runtime, so it can be elided for a leaf that provably fits. The two halves have to
land together: a depth check with no owned stack has nothing to compare against, and an owned stack with
no check merely moves where the fault happens.

**Threshold: the runtime owning and growing its own stacks**, which is not on any list here. Until then
an unbounded recursion is a named death rather than a catchable one, and `for` is the loop.

## Preemptive scheduling

**Status: closed as cooperative.** Opened as a door by 0.2.0, which moved the specification onto it.

The scheduler is **cooperative**: a coroutine yields at a channel operation, a `select`, or a sleep, and
nothing takes it off its worker until it does ([Coroutines & Channels](docs/code/coroutine.md)). A
CPU-bound coroutine that never parks therefore occupies one worker for as long as it runs, and `M` of them
leave nothing to run anything else — including `main`.

The specification asked for the property rather than the mechanism until this release: _no coroutine can
indefinitely starve others, not even a CPU-bound one that never touches a channel_. That is a real
guarantee and Go bought it in 1.14 with asynchronous preemption. What it costs is not the signal handling
— it is that **every** coroutine's stack must be interruptible at a safepoint the runtime can identify,
which reaches the emitter, the stack maps, and every place a value lives across a call.

**If it were reopened**, the cheap half comes first and is worth having on its own: **compiler-inserted
safepoints** at back-edges, which turn a `for` loop with no channel operation into a yielding one and cost
a load and a branch per iteration. That covers the loop-shaped spinner, which is the shape almost every
real one has. True asynchronous preemption — a signal into arbitrary code — is the other half and needs
the stack maps.

**Threshold: a spinner that a back-edge safepoint would not catch**, in a program somebody actually wrote.
Until then the discipline is the one every cooperative runtime asks for: put a channel operation in the
loop.

## An iterative chain teardown

**Status: open, with a measured bound.** Named by 0.2.0, which put the bound in the specification instead
of a deviation.

Freeing a recursive value recurses one C stack frame per node, so how long a chain can be freed is bounded
by the stack the free runs on — about 60 000 nodes on a default 8 MiB main stack
([Memory](docs/core/memory.md)). Past that the process dies with its name and cannot be caught: the free
runs on the scope-exit and abort-unwind paths, where raising is not safe.

The fix is not subtle — walk the chain into an explicit worklist and release node by node, instead of
letting the C stack be the worklist. What makes it work rather than a rewrite is that the drop is
GENERATED: every recursive type's teardown comes out of one place in the emitter, so the shape changes once
rather than per type.

**If it were done**, the thing to be careful of is the ORDER. A recursive drop releases a node's payload
before its tail, and a worklist that pushed tails first would reverse that — observable through any drop a
program can write, once user drops exist.

**Threshold: a program that needs a chain deeper than the bound**, rather than a benchmark. Every recursive
structure this toolchain has built stays far under it, and a structure that genuinely goes that deep is
better served by a `list` indexed by position — which is a shape the language already has, and which frees
in one loop.
