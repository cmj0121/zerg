# Zerg Built-in Functions

English | [繁體中文](builtins.zh-TW.md)

The **fixed, compiler-recognized functions** every program can call with **no `import`** — the only
free-function calls the language itself provides. The set is **closed**: a user cannot add to it. Part of the
[Language Reference](../language.md); this page is the per-function detail.

Not listed here, because they are **not** built-in functions: `print` / `raise` / `guard` / `spawn` / `defer`
/ `del` are **keywords**; `list.len()` / `map.get()` are **methods** on a built-in type; and `math.sqrt` /
`io.read_file` are **[standard-library](stdlib.md)** functions reached with `import`.

## Summary

| Built-in                                 | Signature                     | Summary                        |
| ---------------------------------------- | ----------------------------- | ------------------------------ |
| [`Ref`](#ref) / [`deref`](#deref)        | `Ref(x)`, `deref(r)`          | build / read a refcounted box  |
| [conversions](#primitive-conversions)    | `int(x)` … `T(x)`             | primitive re-construction      |
| [number parse](#parsing-a-string)        | `int(s)` `uint(s)` `float(s)` | parse a number from a str      |
| [str bridges](#str--list-bridges)        | `str(42)`, `str(bytes)`       | scalar display / str ⇄ list    |
| [error kinds](#error-constructors)       | `ValueError(msg)` …           | build an `Err` of a kind       |
| [raw pointers](#raw-pointers-unsafe)     | `addr` `ptr` `.load` …        | bare-metal — **`unsafe` only** |
| [`sizeof` / `alignof`](#sizeof--alignof) | `sizeof[T]`, `alignof[T]`     | a type's size / alignment      |

## `Ref`

`Ref(x: T) -> Ref[T]` — allocate a **reference-counted heap box** holding `x` and return it. A `Ref[T]`
is the one value shared **by reference** (copies retain, the last holder frees it once); it is how a value
outlives its defining scope or is shared across a `spawn`. See [Values & Memory](../core/memory.md).

> **[not yet]** There is no `Ref[T]` type in this compiler, so neither built-in exists. Both are refused by
> name — _NotImplemented: a refcounted box `Ref(x)` / `deref(r)` — this compiler has no `Ref[T]` type_. What
> IS reference-counted is `chan`, a `str` and a recursive type, each managed by the compiler rather than
> through this box; the `atomic` module and the `Reader` surface both wait on this one.

## `deref`

`deref(r: Ref[T]) -> T` — read the payload out of a box. The read is a value (a copy for a non-POD `T`);
the box itself is unaffected.

## Primitive conversions

`T(x)` where `T` is `int` / `uint` / `float` / `bool` / `byte` / `rune` (and the fixed-width `i8`…`i64` /
`u8`…`u64` / `f32` / `f64`) **re-constructs** `x`'s value as a `T` — never a reinterpretation of bits. A
value that does not fit the target **aborts** with `OverflowError` (e.g. `uint(-1)`, or a narrowing that
loses range), so a conversion is checked, not silent. See [Types](../core/types.md).

> **[not yet]** The **fixed-width ladder** is not built: `i8`…`i64`, `u8`…`u64`, `f32` and `f64` are neither
> types nor conversions, and `i32(5)` reports _undefined function `i32`_ — an ordinary unresolved name rather
> than a refusal saying the form is unbuilt, which is the one place in this chapter where the standing
> contract is met in outcome and not in wording. The six named above all work, and `uint(-1)` aborts with
> _OverflowError: integer conversion out of range_ exactly as specified.

## Parsing a string

`int(s: str) -> int`, `uint(s: str) -> uint`, and `float(s: str) -> float` are the conversions that
**parse** a string rather than re-construct a value: each reads the number's text, raising `ValueError` on
a malformed string and `OverflowError` on an out-of-range value. Demote the failure with
`guard { int(s) } ?? default`. No other target parses (`bool(s)` / `byte(s)` are rejected).

**The text each accepts is the language's own literal**, and nothing else. `float(s)` reads digits, an
optional `.` with digits on both sides, and an optional exponent ([`GRAMMAR#float-lit`](../../GRAMMAR)) —
a bare run of digits too, since `float(12)` is a legal conversion and `float("12")` is the same value read
from text. What it does **not** read is anything the language never describes: a hexadecimal float
(`0x1p3`), `inf`, `nan`, or a decimal separator that depends on the host's locale. Handing the text to a C
library and taking whatever it accepted is how a conversion comes to have a grammar nobody wrote down.

## `str` ⇄ `list` bridges

- `str(x: T) -> str` where `T` is a **scalar** — render the value's built-in `display()` as text
  (`str(42)` → `"42"`), the same text `print` and an f-string hole produce.
- `str(bytes: list[byte]) -> str` / `str(runes: list[rune]) -> str` — build a `str`, **validating** the
  invariant (valid UTF-8, no embedded NUL); an invalid sequence raises `EncodingError`.
- `bytearray(s: str) -> list[byte]` / `runearray(s: str) -> list[rune]` — decode a `str` to its octets
  or its Unicode code points.

See [Collections](../code/collections.md).

## Error constructors

The **fixed** set `ValueError` / `OverflowError` / `IOError` / `EncodingError` / `IndexError` / `KeyError`,
each called as `Kind(msg: str) -> Err`, builds an `Err` of that kind carrying the message. Use one with
`raise` to abort, or in an `Either` value; test an erased `Err` with `e is IOError`. The set is
compiler-owned — a program cannot define a new kind this phase. See [Null-safety & Errors](../code/errors.md).

> **[deviation]** The kind survives in the TYPE and is lost in the MESSAGE. `e is IOError` answers correctly
> on a constructed `Err`, but a `raise ValueError("bad input")` that reaches the top writes only _bad input_
> to standard error, where the [abort contract](../conformance.md) specifies `Kind: message` — which is the
> shape a runtime-raised one does use (_IndexError: index out of range_). One kind, two output shapes,
> depending on who raised it.

## Raw pointers (`unsafe`)

Legal **only inside an `unsafe` context**. The free functions `addr(x) -> ptr[T]` (the address of an
addressable value), `ptr(p) -> ptr` / `ptr[T](p) -> ptr[T]` (a raw-address cast), and `uint(p) -> uint`
(a pointer-to-integer cast); plus the pointer **methods** `p.load()`, `p.store(v)`, and `p.offset(n)`.
These are the one door to bare-metal work. See [Values & Memory](../core/memory.md).

> **[not yet]** None of it is built, and the refusals say so — _NotImplemented: the raw-pointer built-in
> `addr` — bare-metal memory access, which is `unsafe`-only and not built here_, and _NotImplemented: `ptr`
> is not an expression this compiler reads_. In a TYPE position the wording is weaker: `fn f(p: ptr)` reports
> _no type named `ptr`_ and `p: ptr = 0` reports _cannot bind int to a ptr binding_, which reads as though
> `ptr` were an existing type the value did not suit. The `unsafe` context they need is itself unbuilt.

## `sizeof` / `alignof`

`sizeof[T] -> uint` and `alignof[T] -> uint` are a type's **byte size** and **alignment**, resolved at
**compile time** — the one built-in that needs compiler layout knowledge, unexpressible in pure Zerg. The
argument is a **type**, written like a type argument on `list[T]`: `sizeof[int]` (8), `sizeof[Point]`,
`sizeof[list[byte]]`. Mainly for FFI and low-level layout. See [Values & Memory](../core/memory.md).

> **[not yet]** Refused by name — _NotImplemented: the compile-time built-in `sizeof[T]` — this compiler does
> not compute a type's layout_, and the same for `alignof[T]`. Note that [FFI](ffi.md) describes the same
> pair as a **standard-library** facility rather than a built-in; the two chapters disagree about where it
> will live, and neither has it.
