# Zerg Type System

The concepts every type rule is derived from — what a type **is**, and how one is **decided**. Part of
the [Language Reference](../language.md). Also in [繁體中文](type-system.zh-TW.md).

[Types](types.md) holds the forms and the rules; this chapter holds only the concepts they hang on.

## What to remember

1. Numbers, `[]`, `{:}`, `nil`, and an untyped closure parameter **fit the slot they are in**.
2. Everything else **matches exactly** — crossing types is one **written** step, `T(x)`
   ([Type Conversion](types.md#into--an-ordinary-conversion-spec)).
3. When there is no slot, make one: the type goes **on the left** — `xs: list[byte] = []`.

The rest of this chapter says those three precisely.

## What a type is

Three **axioms** — they say what the words mean.

| Concept                       | In one line                                                                       |
| ----------------------------- | --------------------------------------------------------------------------------- |
| **identity is name and args** | a declared type is its name plus its type arguments; an unnamed form is its parts |
| **a conversion rebuilds**     | crossing types builds a new value; nothing views one type's storage as another    |
| **a type comes with nothing** | no `Object`, no `==`, no ordering, until `derive` or an `impl` gives it one       |

A `struct`, `enum`, `spec` or `type X = Y` is its **name**: two of identical shape are two types. A
generic is its name **and** its arguments — `Result[int]` is `Result[int]` wherever written. An unnamed
form — a tuple, a `chan[T]`, a `fn` type, an array, a `T?` — is its **parts**: two written the same are
one type, and a carrier is an ordinary type under that row, not a spelling of null or of an exception.

## How a type is decided

A **position** — the memo's slot — is what an expression is to the construct around it: the right of a
typed binding, an argument, a branch, a container element. It is structural, not syntactic — `(e)` is
the same position `e` was — and the exhaustive list is [Types](types.md#typed-positions).

Three **rules**, then three **consequences**, each one step from a rule.

| Concept                            | In one line                                                                    |
| ---------------------------------- | ------------------------------------------------------------------------------ |
| **a form may have no type**        | it takes the one its position wants, checked where it is written               |
| **a value never re-types itself**  | a conversion is written; nothing converts on its own                           |
| **one position, one type**         | expressions sharing a position end up one type; source order is never an input |
| **parts decide, positions demand** | a type comes up from its parts; a demand comes down to what has no type        |
| **inference is local**             | a type is fixed where it is written; no later use revises it                   |
| **ambiguity is an error**          | no unique answer and no declared default is a refusal, never a guess           |

Five notes carry the rules into the language:

- **The four typeless forms** — a numeric literal (unconstrained it defaults, `int` or `float`,
  `GRAMMAR#literal`), the containers `[]` and `{:}`, `nil`, and an untyped closure parameter
  (`GRAMMAR#closure-param`). Everything else brings its type with it.
- **A demand stops at parts.** It descends only through the four forms — never into an expression whose
  parts already decided — and an operator chain meets pairwise, each meeting its own position. An
  expression whose parts are **all** typeless has decided nothing, so the demand passes through it:
  `x: byte = 100 + 100` is byte arithmetic, and `x: float = 1 / 2` is `0.5`, because the literals take
  the type before the operator runs. A part that does not fit is refused where it is written — `300`
  in `x: byte = 300 - 100` — and the arithmetic that follows is the target's own, overflow and all.
- **A carrier moves the position in.** `x: int? = e` puts `e` against `int` — a position may **wrap**
  a value this way (a carrier, a spec's box); it never converts one.
- **A spec-typed position builds.** The spec's box goes around the value, one way — no cast comes back,
  and the one question left is `is` ([Specs & Generics](specs.md#type-tests--is)).
- **A call solves its own parameters.** `T` comes from the arguments, never from where the answer
  lands: `x: float = id(5)` solves `T = int` (the literal defaults at `x: T`), and the `int` answer is
  then refused at the binding — write `float(id(5))`. The demand neither solves `T` nor converts the
  answer: **inference is local**, twice over.

> **[not yet]** Two notes run ahead of the compiler: an untyped closure parameter and a spec used as a
> type are each refused by name — of the spec positions, the error tier (`Err` in a `Result[T]`) is the
> one built.
>
> Both compilers hold the rest of this chapter, and say where: a position that converts, an operator
> whose operands are two types, and a typeless form with no position to take one from are each refused
> with a place.

## Why these nine

Axioms say what the words mean; rules say what may happen; each consequence is a rule followed one step:

- **parts decide, positions demand** — a typed value may not be re-typed (rule two), so a demand can
  act only on what has no type (rule one).
- **inference is local** — a later use that revised a type would be the value re-typing itself.
- **ambiguity is an error** — with no demand and no declared default, any choice would be an input the
  rules do not grant.

That is the test this chapter is held to — a concept that is neither an axiom, a rule, nor a consequence
of one does not belong here, and a rule that needs a fourth is a rule that is not yet understood.
