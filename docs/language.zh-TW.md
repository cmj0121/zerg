# Zerg 語言參考（Language Reference）

[README](../README.zh-TW.md) 中設計原則背後的詳細語意。本頁是規格的**前言與地圖**：先陳述閱讀慣例，再索引每一章、
概述各章決定了什麼。每則概述都連到它的完整參考。亦有 [English](language.md) 版本。

## 如何閱讀本規格

`docs/` 各章對**語意具規範性**；根目錄的 [`GRAMMAR`](../GRAMMAR) 檔對**語法**具規範性。Zerg 以整體被規範，而
Phase-1 bootstrap 實作其子集，所以每個特性都帶一個**狀態標記**，標出語言與當前編譯器之間的落差：

| 標記                         | 意義                                           |
| ---------------------------- | ---------------------------------------------- |
| **[implemented]**            | bootstrap 編譯器一如規格實作。                 |
| **[not yet: Phase N]**       | 已規範、尚未建置；使用它是一個乾淨的編譯錯誤。 |
| **[implementation-defined]** | 規格不釘定；conforming 實作自行選擇。          |
| **[deviation]**              | bootstrap 當前行為與規格不符（一個 bug）。     |

**[Conformance](conformance.zh-TW.md)** 章定義這些標記、「conforming」的意義，以及每一章倚賴的可觀察契約
（diagnostics、runtime abort、undefined 與 implementation-defined 行為）。請先讀它。

## 章節

### 閱讀本文件

| 章節                                | 涵蓋                                       |
| ----------------------------------- | ------------------------------------------ |
| [Conformance](conformance.zh-TW.md) | 閱讀慣例、狀態標記、diagnostics/abort 契約 |

### 型別系統

| 章節                                      | 涵蓋                                                     |
| ----------------------------------------- | -------------------------------------------------------- |
| [型別](core/types.zh-TW.md)               | primitive、`struct`、`enum`、tuple、strong-typedef、轉換 |
| [值與記憶體](core/memory.zh-TW.md)        | scope ownership、`mut &`、`del` / `defer`、`Ref[T]`      |
| [Spec 與 Generics](core/specs.zh-TW.md)   | `spec` 作 bound / conformance / 型別；泛型；`is` 測試    |
| [Derive 與預設行為](core/derive.zh-TW.md) | 結構化衍生 vs spec 的 default method                     |
| [Decorator](core/decorators.zh-TW.md)     | 固定、compiler 擁有的 `#[…]` 指令集                      |

### 寫程式

| 章節                                              | 涵蓋                                                  |
| ------------------------------------------------- | ----------------------------------------------------- |
| [控制流與模式比對](code/control-flow.zh-TW.md)    | `if`、`for`、`match` 與 pattern                       |
| [函式與閉包](code/functions.zh-TW.md)             | 一等函式、預設值、named args、closure                 |
| [Null-safety 與錯誤處理](code/errors.zh-TW.md)    | `Result[T]` / `T?`、`?` `??` `?.` `!` `raise` `guard` |
| [Collection](code/collections.zh-TW.md)           | `list`、`map`、`set`、定長 `[T; N]` 陣列              |
| [Coroutines 與 Channels](code/coroutine.zh-TW.md) | `spawn`、channel、`select`、排程                      |
| [慣用法](code/patterns.zh-TW.md)                  | closure、pipeline、builder——道地寫法，無新語法        |

### 表面形式

| 章節                                    | 涵蓋                                 |
| --------------------------------------- | ------------------------------------ |
| [語法糖](surface/syntax-sugar.zh-TW.md) | 每個表面寫法與它 desugar 回的核心    |
| [文法](surface/grammar.zh-TW.md)        | 形式表面文法（`GRAMMAR` 的散文伴讀） |

### 程式，以及它以外的世界

| 章節                                                        | 涵蓋                                                |
| ----------------------------------------------------------- | --------------------------------------------------- |
| [Module、Package 與 Program](runtime/package.zh-TW.md)      | 組織、可見性、coherence、程式啟動                   |
| [Process 與 I/O](runtime/io.zh-TW.md)                       | stream、file、stdio、process——`io` package          |
| [格式化與文字](runtime/format.zh-TW.md)                     | `display` / `debug` 渲染、`f"…"`、`print`           |
| [內建函式（Built-in Functions）](runtime/builtins.zh-TW.md) | 免 import 的固定函式——`Ref`、轉換、error kind       |
| [標準函式庫（Standard Library）](runtime/stdlib.zh-TW.md)   | 可 import 的隨附套件——io、fs、os、time、math、rand… |
| [FFI](runtime/ffi.zh-TW.md)                                 | C ABI 邊界——`pub` export、unsafe foreign import     |

### 工具

| 章節                                         | 涵蓋                                           |
| -------------------------------------------- | ---------------------------------------------- |
| [格式化器與檢查器規則](tooling/fmt.zh-TW.md) | `zerg fmt` 與 `zerg lint` 的每一條規則及其代碼 |

## 型別（Types）

每個程式起步的純量 primitive——`bool`、`byte`、`rune`、`int`、`uint`、`float`、`str`——以及你在其上建立的**積型別**
（`struct`）與**和型別**（`enum`）、tuple 與 strong-typedef：一個型別如何宣告、建構,以及如何轉換（一律 re-construction、
絕不 reinterpret）。見 **[型別](core/types.zh-TW.md)**。

## Spec 與 Generics（Specs & Generics）

Zerg 如何抽象行為。**`spec`** 是唯一機制——一個 nominal 介面,同時扮演泛型 **bound**、型別所宣告的 **conformance**,以及
**型別本身**（heap-boxed、動態 dispatch 的 existential）。涵蓋內建 spec（`Eq`、`Ord`、`Hash`、`Error`、運算子——
**沒有 auto-implement 的 `Object` spec**、也沒有隱式 `==`：相等與排序是經 `derive(Eq)` / `derive(Ord)` 或手寫 impl
**opt-in**）、迭代協定,以及 `is` 型別測試（對 existential 的 `x is T` 為 **[implemented]**；對任意值的一般 `x is T`
為 **[not yet]**）。見 **[Spec 與 Generics](core/specs.zh-TW.md)**。

## Decorator 與 compiler 代寫的行為

compiler 能依型別的**結構**幫你**寫出實作**,以型別上的 **decorator** 請求：`struct`/`enum` 上的
`#[derive(Encode, Decode)]` 會生成逐欄位(與逐 variant)的 canonical impl。可 derive 的是一組**固定、compiler 擁有的
受祝福 spec**——全部 opt-in：`Eq`、`Ord`、`Hash`、`Encode`、`Decode`（沒有一律 derive 的 `Object`）。**使用者 spec
永遠不能被 derive**(`#[derive(MySpec)]` 是編譯錯誤)：從結構產碼需要會讀 field 的程式,而那只有 compiler 能做——**沒有 macro**。要
客製就手寫 `impl X for Y`。decorator 是 Zerg 給這類 compiler 指令的唯一通道,且保持封閉(使用者不可自訂)。`derive`
只是一個小固定集合中的一員——`#[dyn]`、`#[sealed]` 等——完整清單見 **[Decorator](core/decorators.zh-TW.md)**。另見
**[Derive 與預設行為](core/derive.zh-TW.md)**。

## 控制流與模式比對（Control Flow & Pattern Matching）

三個構造,依「產出什麼」區分：**`if`** 與 **`for`** 是為副作用而跑的 statement,**`match`** 是產出值的 expression。再加上
`match`（或 `:=` 綁定）所解構的 pattern——variant、literal、tuple、struct、or-pattern。見
**[控制流與模式比對](code/control-flow.zh-TW.md)**。

## 值與記憶體（Values & Memory）

無 GC 的所有權模型：每個值都是 **scope-owned** 且以**值傳遞**,`mut &` 是唯一顯式的 by-ref 路徑,`del` 與 `defer` 控制清理
時機,而 **`Ref[T]`**（或 `chan`）是「資源逃出自身 scope」的 reference-counted 例外。見 **[值與記憶體](core/memory.zh-TW.md)**。

## 函式與閉包（Functions & Closures）

函式是**一等值**,它的型別就是輸入／輸出契約、僅此而已——除了引數改動與可回復錯誤,不追蹤任何其他 effect。涵蓋預設參數、
具名引數,以及只以複製捕獲 immutable 值與 channel 的閉包。見 **[函式與閉包](code/functions.zh-TW.md)**。

## 內建函式（Built-in functions）

一組**固定**的、編譯器內建的函式，免 `import`——這是語言本身提供的唯一自由函式呼叫。使用者無法自行擴充這組。

- **`Ref(x)` / `deref(r)`**——建立與讀取 reference-counted box（[值與記憶體](core/memory.zh-TW.md)）。
- **原生型別轉換 `T(x)`**——`int` / `uint` / `float` / `bool` / `byte` / `rune`（以及固定寬度的
  `i8`…`i64` / `u8`…`u64` / `f32` / `f64`）：帶範圍檢查的**重新建構**，絕非 reinterpret；`int("…")` 另外會
  **解析**十進位字串（[型別](core/types.zh-TW.md)）。
- **`str(bytes)` / `str(runes)`** 與 **`list[byte](s)` / `list[rune](s)`**——str ⇄ list 的橋接，驗證
  `str` 不變式（[Collection](code/collections.zh-TW.md)）。
- **Error 建構子**——固定的 `ValueError` / `OverflowError` / `IOError` / `EncodingError` / `IndexError` /
  `KeyError`，各自建出該 kind 的 `Err`（[Null-safety 與錯誤處理](code/errors.zh-TW.md)）。
- **Raw pointer 內建（僅限 `unsafe`）**——`addr` / `ptr` / `ptr[T]` / `uint(p)`，以及指標方法
  `.load` / `.store` / `.offset`（[值與記憶體](core/memory.zh-TW.md)）。

其餘看起來可呼叫的都**不是**內建函式：`print` / `raise` / `guard` / `spawn` / `defer` / `del` 是**關鍵字**；
`list.len()` / `map.get()` 是內建型別上的**方法**；`math.sqrt` / `io.read_file` 則是需 `import` 的**標準函式庫**函式。
逐項細節見 **[內建函式（Built-in Functions）](runtime/builtins.zh-TW.md)**；可 import 的套件見
**[標準函式庫（Standard Library）](runtime/stdlib.zh-TW.md)**。

## 格式化與文字（Formatting & Text）

一個值如何變成文字——結構化的 **`debug`** 與給人看的 **`display`**，兩者是**內建的值渲染**（不是任何 `Object` spec
的 method）、`f"…"` 內插,以及永遠在 scope 內的 `print` 關鍵字。見 **[格式化與文字](runtime/format.zh-TW.md)**。

## Null-safety 與錯誤處理（Null-safety & Errors）

失敗分**兩層**：可回復的失敗是一般的值（`Result[T]`、`T?`）,bug 則是一次 unwind stack 的 **abort**。運算子 `?` `??` `?.`
`!` `raise` 與 `guard` 橋接兩層,而 `is` 對被抹除的 `Err` 分派。見 **[Null-safety 與錯誤處理](code/errors.zh-TW.md)**。

## 並行（Concurrency）

Zerg 的並行**只有 coroutine 與 channel**：`spawn`（Go 的 `go`）,fire-and-forget、無 join/handle,只捕獲
**immutable 值與 channel**。預期的 scheduler 是搶佔式 **M:N**，但 bootstrap 今日跑的是合作式 **N:1** 單執行緒
（**[deviation]**——一個從不 park 的 CPU-bound coroutine 會餓死其餘；見
[Coroutines 與 Channels](code/coroutine.zh-TW.md)）。channel 是 reference-counted 的 by-ref **管道**（一個為通訊而生的
`Ref` 型別;`Ref[T]` 是它持有資源的手足——見 [值與記憶體](core/memory.zh-TW.md)）——payload 複製、在最後一個 sender 離場時
**自動 close**、以 **`Result[T]`** 接收（`Right` = 已關,攜帶崩潰 `Err` 或 `StopIteration` 哨兵）、並用 **`select`** 多路等待。

完整模型——buffering、receive/close 語意、directional 端、`select`、deadlock——見
**[Coroutines 與 Channels](code/coroutine.zh-TW.md)** 參考文件。
