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
