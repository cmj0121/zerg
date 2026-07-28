# Zerg 內建函式（Built-in Functions）

[English](builtins.md) | 繁體中文

一組**固定的、編譯器內建的函式**，每支程式都能呼叫、**免 `import`**——這是語言本身提供的唯一自由函式呼叫。這組是
**封閉的**：使用者無法自行擴充。屬 [語言規格（Language Reference）](../language.zh-TW.md)；本頁是逐項細節。

以下**不是**內建函式：`print` / `raise` / `guard` / `spawn` / `defer` / `del` 是**關鍵字**；`list.len()` /
`map.get()` 是內建型別上的**方法**；`math.sqrt` / `io.read_file` 則是需 `import` 的
**[標準函式庫](stdlib.zh-TW.md)**函式。

## 總覽

| 內建                                          | 簽名                          | 摘要                          |
| --------------------------------------------- | ----------------------------- | ----------------------------- |
| [`Ref`](#ref) / [`deref`](#deref)             | `Ref(x)`, `deref(r)`          | 建立 / 讀取 refcounted box    |
| [轉換](#原生型別轉換primitive-conversions)    | `int(x)` … `T(x)`             | 原生型別重新建構              |
| [數字解析](#解析字串parsing-a-string)         | `int(s)` `uint(s)` `float(s)` | 從字串解析數字                |
| [str 橋接](#str--list-橋接)                   | `str(42)`, `str(bytes)`       | scalar display 或 str ⇄ list  |
| [error kind](#error-建構子error-constructors) | `ValueError(msg)` …           | 建出固定 kind 的 `Err`        |
| [raw pointer](#raw-pointer僅限-unsafe)        | `addr` `ptr` `.load` …        | bare-metal——**僅限 `unsafe`** |
| [`sizeof` / `alignof`](#sizeof--alignof)      | `sizeof[T]`, `alignof[T]`     | 型別的大小 / 對齊             |

## `Ref`

`Ref(x: T) -> Ref[T]`——配置一個持有 `x` 的 **reference-counted heap box** 並回傳。`Ref[T]` 是唯一**按 reference**
共享的值（複製時 retain、最後一個持有者釋放一次）；它讓值得以超出定義它的 scope、或跨 `spawn` 共享。見
[值與記憶體](../core/memory.zh-TW.md)。

## `deref`

`deref(r: Ref[T]) -> T`——把 box 的內容讀出來。讀出是一個值（非 POD 的 `T` 會複製）；box 本身不受影響。

## 原生型別轉換（Primitive conversions）

`T(x)`，其中 `T` 為 `int` / `uint` / `float` / `bool` / `byte` / `rune`（以及固定寬度的 `i8`…`i64` /
`u8`…`u64` / `f32` / `f64`）——把 `x` 的值**重新建構**成 `T`，絕非位元的 reinterpret。不合目標範圍的值會
**abort**（`OverflowError`，如 `uint(-1)` 或會失去範圍的縮窄），所以轉換是被檢查的、不會無聲失真。見
[型別](../core/types.zh-TW.md)。

## 解析字串（Parsing a string）

`int(s: str) -> int`、`uint(s: str) -> uint`、`float(s: str) -> float` 是**解析**字串（而非重新建構值）的轉換：
讀出數字文字，格式錯誤 raise `ValueError`、超出範圍 raise `OverflowError`。用 `guard { int(s) } ?? default`
降級。其餘目標不解析（`bool(s)` / `byte(s)` 會被拒）。

## str ⇄ list 橋接

- `str(x: T) -> str`、`T` 為 **scalar**——渲染值的內建 `display()`（`str(42)` → `"42"`），與 `print`、f-string
  hole 產生的文字相同。
- `str(bytes: list[byte]) -> str` / `str(runes: list[rune]) -> str`——組出 `str`，並**驗證**不變式（合法 UTF-8、
  無內嵌 NUL）；非法序列 raise `EncodingError`。
- `list[byte](s: str) -> list[byte]` / `list[rune](s: str) -> list[rune]`——把 `str` 拆成 octets 或 Unicode
  code points。

見 [Collection](../code/collections.zh-TW.md)。

## error 建構子（Error constructors）

**固定**集合 `ValueError` / `OverflowError` / `IOError` / `EncodingError` / `IndexError` / `KeyError`，各以
`Kind(msg: str) -> Err` 呼叫，建出該 kind、帶訊息的 `Err`。搭配 `raise` 觸發 abort，或放進 `Either` 值；以
`e is IOError` 測試已抹除的 `Err`。這組由編譯器擁有——本階段程式無法自訂新 kind。見
[Null-safety 與錯誤處理](../code/errors.zh-TW.md)。

## raw pointer（僅限 `unsafe`）

**只在 `unsafe` 情境內**合法。自由函式 `addr(x) -> ptr[T]`（可定址值的位址）、`ptr(p) -> ptr` /
`ptr[T](p) -> ptr[T]`（raw-address cast）、`uint(p) -> uint`（指標轉整數）；以及指標**方法** `p.load()`、
`p.store(v)`、`p.offset(n)`。這是通往 bare-metal 的唯一入口。見 [值與記憶體](../core/memory.zh-TW.md)。

## `sizeof` / `alignof`

`sizeof[T] -> uint` 與 `alignof[T] -> uint` 是型別的**位元組大小**與**對齊**，在**編譯期**決定——這是唯一需要
編譯器 layout 知識、純 Zerg 表達不出來的 built-in。引數是**型別**，寫法與 `list[T]` 的型別引數一致：`sizeof[int]`
（8）、`sizeof[Point]`、`sizeof[list[byte]]`。主要用於 FFI 與低階 layout。見 [值與記憶體](../core/memory.zh-TW.md)。
