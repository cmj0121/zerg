# Zerg 語言參考（Language Reference）

[README](../README.zh-TW.md) 中設計原則背後的詳細語意。本頁是**地圖**：每一節說明一個主題決定了什麼、並連到它的完整參考。
亦有 [English](language.md) 版本。

## 型別（Types）

每個程式起步的純量 primitive——`bool`、`byte`、`rune`、`int`、`uint`、`float`、`str`——以及你在其上建立的**積型別**
（`struct`）與**和型別**（`enum`）、tuple 與 strong-typedef：一個型別如何宣告、建構,以及如何轉換（一律 re-construction、
絕不 reinterpret）。見 **[型別](types.zh-TW.md)**。

## Spec 與 Generics（Specs & Generics）

Zerg 如何抽象行為。**`spec`** 是唯一機制——一個 nominal 介面,同時扮演泛型 **bound**、型別所宣告的 **conformance**,以及
**型別本身**（heap-boxed、動態 dispatch 的 existential）。涵蓋內建 spec（`Object`、`Ord`、`Hash`、`Error`、運算子）、
迭代協定,以及 `is` 型別測試。見 **[Spec 與 Generics](specs.zh-TW.md)**。

## Decorator 與 compiler 代寫的行為

compiler 能依型別的**結構**幫你**寫出實作**,以型別上的 **decorator** 請求：`struct`/`enum` 上的
`#[derive(Encode, Decode)]` 會生成逐欄位(與逐 variant)的 canonical impl。可 derive 的是一組**固定、compiler 擁有的
受祝福 spec**——`Object`(一律 derive)以及可 opt-in 的 `Ord`、`Hash`、`Encode`、`Decode`。**使用者 spec 永遠不能被
derive**(`#[derive(MySpec)]` 是編譯錯誤)：從結構產碼需要會讀 field 的程式,而那只有 compiler 能做——**沒有 macro**。要
客製就手寫 `impl X for Y`。decorator 是 Zerg 給這類 compiler 指令的唯一通道,且保持封閉(使用者不可自訂)。見
**[Derive 與預設行為](derive.zh-TW.md)**。

## 控制流與模式比對（Control Flow & Pattern Matching）

三個構造,依「產出什麼」區分：**`if`** 與 **`for`** 是為副作用而跑的 statement,**`match`** 是產出值的 expression。再加上
`match`（或 `:=` 綁定）所解構的 pattern——variant、literal、tuple、struct、or-pattern。見
**[控制流與模式比對](control-flow.zh-TW.md)**。

## 值與記憶體（Values & Memory）

無 GC 的所有權模型：每個值都是 **scope-owned** 且以**值傳遞**,`mut` 是唯一顯式的 by-ref 路徑,`del` 與 `defer` 控制清理
時機,而 **`Ref[T]`**（或 `chan`）是「資源逃出自身 scope」的 reference-counted 例外。見 **[值與記憶體](memory.zh-TW.md)**。

## 函式與閉包（Functions & Closures）

函式是**一等值**,它的型別就是輸入／輸出契約、僅此而已——除了引數改動與可回復錯誤,不追蹤任何其他 effect。涵蓋預設參數、
具名引數,以及只以複製捕獲 immutable 值與 channel 的閉包。見 **[函式與閉包](functions.zh-TW.md)**。

## 格式化與文字（Formatting & Text）

一個值如何變成文字——結構化 **auto-derive 的 `debug`** 與給人看的 **`display`**（兩者都是 `Object` method）、`f"…"` 內插,
以及永遠在 scope 內的 `print` 關鍵字。見 **[格式化與文字](format.zh-TW.md)**。

## Null-safety 與錯誤處理（Null-safety & Errors）

失敗分**兩層**：可回復的失敗是一般的值（`Result[T]`、`T?`）,bug 則是一次 unwind stack 的 **abort**。運算子 `?` `??` `?.`
`!` `raise` 與 `guard` 橋接兩層,而 `is` 對被抹除的 `Err` 分派。見 **[Null-safety 與錯誤處理](errors.zh-TW.md)**。

## 並行（Concurrency）

Zerg 的並行**只有 coroutine 與 channel**：`spawn`（Go 的 `go`）跑在 **M:N scheduler** 上,fire-and-forget、無
join/handle,只捕獲 **immutable 值與 channel**。channel 是 reference-counted 的 by-ref **管道**（一個為通訊而生的 `Ref`
型別;`Ref[T]` 是它持有資源的手足——見 [值與記憶體](memory.zh-TW.md)）——payload 複製、在最後一個 sender 離場時
**自動 close**、以 **`Result[T]`** 接收（`Right` = 已關,攜帶崩潰 `Err` 或 `StopIteration` 哨兵）、並用 **`select`** 多路等待。

完整模型——buffering、receive/close 語意、directional 端、`select`、deadlock——見
**[Coroutines 與 Channels](coroutine.zh-TW.md)** 參考文件。

## 配套參考（Companion references）

建立在上述核心語言之上：

- **[文法（Grammar）](grammar.zh-TW.md)**——形式表面文法（W3C-EBNF）、權威的 [`GRAMMAR`](../GRAMMAR) 檔,與 nvim 語法工具。
- **[語法糖（Syntax Sugar）](syntax-sugar.zh-TW.md)**——每個方便的表面寫法,以及它 desugar 回的核心,收在一張表。
- **[Collection](collections.zh-TW.md)**——內建容器 `list`、`map`、`set`,以及定長 `[T; N]` 陣列;一角色一個 canonical 型別。
- **[Derive 與預設行為](derive.zh-TW.md)**——兩種「免費」行為的來源：compiler 的結構化衍生,與 spec 的 default method,
  以及兩者之間那條明確界線。
- **[Module、Package 與 Program](package.zh-TW.md)**——原始碼如何組織成 module 與 package、可見性與 coherence 如何跨越
  它們,以及程式從何啟動。
- **[FFI](ffi.zh-TW.md)**——C ABI 邊界：以 `pub` 表面 export Zerg、以 `extern` import C。
- **[Process 與 I/O](io.zh-TW.md)**——有檢查的 I/O 面（stream、file、stdio、process）,以 `io` package 匯入。
