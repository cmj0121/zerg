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

## `Ref`

`Ref(x: T) -> Ref[T]`——配置一個持有 `x` 的 **reference-counted heap box** 並回傳。`Ref[T]` 是唯一**按 reference**
共享的值（複製時 retain、最後一個持有者釋放一次）；它讓值得以超出定義它的 scope、或跨 `spawn` 共享。見
[值與記憶體](../core/memory.zh-TW.md)。

> **[not yet]** 本編譯器沒有 `Ref[T]` 型別，所以這兩個內建都不存在，兩者都被具名拒絕——
> _E446 NotImplemented: a refcounted box `Ref(x)` / `deref(r)` — this compiler has no `Ref[T]` type_。真正有
> reference count 的是 `chan`、`str` 與遞迴型別，各由編譯器自行管理、而非透過這個 box；`atomic` 模組與
> `Reader` 表面都在等這一項。

## `deref`

`deref(r: Ref[T]) -> T`——把 box 的內容讀出來。讀出是一個值（非 POD 的 `T` 會複製）；box 本身不受影響。

## 原生型別轉換（Primitive conversions）

`T(x)`，其中 `T` 為 `int` / `uint` / `float` / `bool` / `byte` / `rune`（以及固定寬度的 `i8`…`i64` /
`u8`…`u64` / `f32` / `f64`）——把 `x` 的值**重新建構**成 `T`，絕非位元的 reinterpret。不合目標範圍的值會
**abort**（`OverflowError`，如 `uint(-1)` 或會失去範圍的縮窄），所以轉換是被檢查的、不會無聲失真。見
[型別](../core/types.zh-TW.md)。

**`T(x)` 接受哪些配對是一張封閉的表**，而 `int` 是每一組配對都有一側是它的中樞。不在表上的配對就不是一個轉換
——`byte` 上的 `float(b)` 是 `E395`，改寫成穿過中樞的那兩步。`float` 作為**來源**的缺席屬於決定、而非缺了一步：
`float` 上的 `int(x)` 是 `E394`，小數要由一個動詞丟掉。表與這兩個理由都在[型別](../core/types.zh-TW.md)。

> **[not yet]** **固定寬度那一階**沒有建：`i8`…`i64`、`u8`…`u64`、`f32`、`f64` 既不是型別也不是轉換，而
> 兩個位置都指名它:`i32(5)` 與 `fn f(x: i32)` 一樣回報 _E465 NotImplemented: `i32` is part of the fixed-width
> ladder — … the built-in widths are `int`, `uint`, `byte`, `rune` and `float`_。上面點名的六個都可用；編譯器看不見
> 的負值確實以 _OverflowError: integer conversion out of range_ abort，與規格一致——它**看得見**的那種則改在編譯期
> 被拒（`E330`），依 [型別](../core/types.zh-TW.md) 的常數規則。

## 解析字串（Parsing a string）

`int(s: str) -> int`、`uint(s: str) -> uint`、`float(s: str) -> float` 是**解析**字串（而非重新建構值）的轉換：
讀出數字文字，格式錯誤 raise `ValueError`、超出範圍 raise `OverflowError`。用 `guard { int(s) } ?? default`
降級。其餘目標不解析（`bool(s)` / `byte(s)` 會被拒）。

**它們各自接受的文字就是語言自己的字面量**，此外不接受任何東西。`float(s)` 讀數字、可有一個兩側都有數字的
`.`，以及可有的指數（[`GRAMMAR#float-lit`](../../GRAMMAR)）——也讀單純一串數字，因為 `float(12)` 是合法的轉換，
而 `float("12")` 就是同一個值從文字讀進來。它**不**讀語言從來沒有描述過的東西：十六進位 float（`0x1p3`）、
`inf`、`nan`，或一個取決於主機 locale 的小數點。把文字交給 C 函式庫、然後照單全收它接受的東西，正是一個轉換
如何長出一份沒有人寫下來的文法。

## str ⇄ list 橋接

- `str(x: T) -> str`、`T` 為 **scalar**——渲染值的內建 `display()`（`str(42)` → `"42"`），與 `print`、f-string
  hole 產生的文字相同。
- `str(bytes: list[byte]) -> str` / `str(runes: list[rune]) -> str`——組出 `str`，並**驗證**不變式（合法 UTF-8、
  無內嵌 NUL）；非法序列 raise `EncodingError`。
- `bytearray(s: str) -> list[byte]` / `runearray(s: str) -> list[rune]`——把 `str` 拆成 octets 或 Unicode
  code points。

見 [Collection](../code/collections.zh-TW.md)。

## error 建構子（Error constructors）

這套分類是**十一個由編譯器擁有的 kind**，每一個都是 `Kind(msg: str) -> Err`——該 kind、帶訊息的 `Err`。搭配
`raise` 觸發 abort，或放進 `Either` 值；以 `e is IOError` 測試已抹除的 `Err`。本階段程式無法自訂新 kind。見
[Null-safety 與錯誤處理](../code/errors.zh-TW.md)。

`ValueError` · `OverflowError` · `IOError` · `EncodingError` · `IndexError` · `KeyError` ·
`DeadlockError` · `SendOnClosedError` · `StopIteration` · `DivideByZeroError` · `AssertionError`

它們的**編號是一份 ABI**，由 runtime（`ZRT_ERR_*`）與兩個編譯器共用，這也是這張表不會半途停下的原因：只加進其中
一邊的 kind，會讓同一個數字依「是哪個編譯器建的」而有兩種意思。

**`StopIteration` 可被測試、但不可被建構**，這是這組唯一不對稱之處。它是 channel 乾淨關閉所攜帶的 sentinel，
所以一個能建出它的 sender 會讓自己的 channel 戴著「正常結束」的記號關閉，而消費端會把當掉讀成善終。寫出它是
_E726 `StopIteration` is testable but not constructible_；`e is StopIteration` 不花任何代價，予以保留。

**`AssertionError` 由 `assert` raise，而且只有它會**，這正是 `zerg test` 能在不把訊息當協定讀的情況下，分辨
「一個不成立的主張」與「一支垮掉的程式」的原因。

## raw pointer（僅限 `unsafe`）

**只在 `unsafe` 情境內**合法。自由函式 `addr(x) -> ptr[T]`（可定址值的位址）、`ptr(p) -> ptr` /
`ptr[T](p) -> ptr[T]`（raw-address cast）、`uint(p) -> uint`（指標轉整數）；以及指標**方法** `p.load()`、
`p.store(v)`、`p.offset(n)`。這是通往 bare-metal 的唯一入口。見 [值與記憶體](../core/memory.zh-TW.md)。

> **[not yet]** 全部都沒有建，而每一個位置都以同一個代碼說明——_E413 NotImplemented: the raw-pointer built-in
> `addr` — bare-metal memory access, which is `unsafe`-only and not built here_，`ptr` 相同，**型別**位置也相同：
> `fn f(p: ptr)` 與 `p: ptr = 0` 都指名那個 raw-pointer 內建，而不再讀起來像是 `ptr` 是個既有型別、只是值不合。
> 它們所需的 `unsafe` 情境本身也還沒建。

## `sizeof` / `alignof` —— 已延後

**不屬於本規格。** `sizeof[T]` 與 `alignof[T]` 具名拒絕 —— _E414 NotImplemented: the compile-time built-in
`sizeof[T]` — this compiler does not compute a type's layout_ —— 而「型別的佈局屬於 **built-in** 還是
**標準函式庫**設施」這個問題尚未決定。原本一章說是前者、[FFI](ffi.zh-TW.md) 說是後者,兩處一併移除而不是留著
互相矛盾;**決定先於功能**。
