# Zerg Built-in Functions

English | [繁體中文](builtins.zh-TW.md)

The **fixed, compiler-recognized functions** every program can call with **no `import`** — the only
free-function calls the language itself provides. The set is **closed**: a user cannot add to it. Part of the
[Language Reference](language.md); this page is the per-function detail.

Not listed here, because they are **not** built-in functions: `print` / `raise` / `guard` / `spawn` / `defer`
/ `del` are **keywords**; `list.len()` / `map.get()` are **methods** on a built-in type; and `math.sqrt` /
`io.read_file` are **[standard-library](stdlib.md)** functions reached with `import`.

## Summary

| Built-in                              | Signature                     | Summary                        |
| ------------------------------------- | ----------------------------- | ------------------------------ |
| [`Ref`](#ref) / [`deref`](#deref)     | `Ref(x)`, `deref(r)`          | build / read a refcounted box  |
| [conversions](#primitive-conversions) | `int(x)` … `T(x)`             | primitive re-construction      |
| [number parse](#parsing-a-string)     | `int(s)` `uint(s)` `float(s)` | parse a number from a str      |
| [str bridges](#str--list-bridges)     | `str(42)`, `str(bytes)`       | scalar display / str ⇄ list    |
| [error kinds](#error-constructors)    | `ValueError(msg)` …           | build an `Err` of a kind       |
| [raw pointers](#raw-pointers-unsafe)  | `addr` `ptr` `.load` …        | bare-metal — **`unsafe` only** |

## `Ref`

`Ref(x: T) -> Ref[T]` — allocate a **reference-counted heap box** holding `x` and return it. A `Ref[T]`
is the one value shared **by reference** (copies retain, the last holder frees it once); it is how a value
outlives its defining scope or is shared across a `spawn`. See [Values & Memory](memory.md).

## `deref`

`deref(r: Ref[T]) -> T` — read the payload out of a box. The read is a value (a copy for a non-POD `T`);
the box itself is unaffected.

## Primitive conversions

`T(x)` where `T` is `int` / `uint` / `float` / `bool` / `byte` / `rune` (and the fixed-width `i8`…`i64` /
`u8`…`u64` / `f32` / `f64`) **re-constructs** `x`'s value as a `T` — never a reinterpretation of bits. A
value that does not fit the target **aborts** with `OverflowError` (e.g. `uint(-1)`, or a narrowing that
loses range), so a conversion is checked, not silent. See [Types](types.md).

## Parsing a string

`int(s: str) -> int`, `uint(s: str) -> uint`, and `float(s: str) -> float` are the conversions that
**parse** a string rather than re-construct a value: each reads the number's text, raising `ValueError` on
a malformed string and `OverflowError` on an out-of-range value. Demote the failure with
`guard { int(s) } ?? default`. No other target parses (`bool(s)` / `byte(s)` are rejected).

## `str` ⇄ `list` bridges

- `str(x: T) -> str` where `T` is a **scalar** — render the value's built-in `display()` as text
  (`str(42)` → `"42"`), the same text `print` and an f-string hole produce.
- `str(bytes: list[byte]) -> str` / `str(runes: list[rune]) -> str` — build a `str`, **validating** the
  invariant (valid UTF-8, no embedded NUL); an invalid sequence raises `EncodingError`.
- `list[byte](s: str) -> list[byte]` / `list[rune](s: str) -> list[rune]` — decode a `str` to its octets
  or its Unicode code points.

See [Collections](collections.md).

## Error constructors

The **fixed** set `ValueError` / `OverflowError` / `IOError` / `EncodingError` / `IndexError` / `KeyError`,
each called as `Kind(msg: str) -> Err`, builds an `Err` of that kind carrying the message. Use one with
`raise` to abort, or in an `Either` value; test an erased `Err` with `e is IOError`. The set is
compiler-owned — a program cannot define a new kind this phase. See [Null-safety & Errors](errors.md).

## Raw pointers (`unsafe`)

Legal **only inside an `unsafe` context**. The free functions `addr(x) -> ptr[T]` (the address of an
addressable value), `ptr(p) -> ptr` / `ptr[T](p) -> ptr[T]` (a raw-address cast), and `uint(p) -> uint`
(a pointer-to-integer cast); plus the pointer **methods** `p.load()`, `p.store(v)`, and `p.offset(n)`.
These are the one door to bare-metal work. See [Values & Memory](memory.md).
