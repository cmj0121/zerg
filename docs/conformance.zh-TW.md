# Conformance 與規格慣例

[English](conformance.md) | 繁體中文

本章定義如何閱讀 Zerg 規格：各文件的 normative 範圍為何、用來標示「語言」與「目前 bootstrap 編譯器」落差的狀態
標記，以及每個其他章節都倚賴的可觀察契約（診斷、runtime abort、undefined behavior）。

## 何者為 normative

- **[`GRAMMAR`](../GRAMMAR)**（repo 根目錄，W3C 式 EBNF）是 Zerg **語法**的 normative 定義。`GRAMMAR` 推導不出
  的構造就不是 Zerg 程式。
- `docs/` 下的規格章節對**語義**為 normative——static（typing、name resolution、coherence、visibility）與
  dynamic（求值、記憶體、並行、錯誤）——並以引用 `GRAMMAR` 取代重述語法。
- 關於**參考 bootstrap** 如何把 Zerg 降到 C 的註記——其 C ABI、name mangling 與記憶體佈局——為 **informative**：
  它們記錄的是某一個實作，對 conforming implementation 沒有約束力。
- 英文文本具權威；`*.zh-TW.md` 版本是與之 lockstep 的翻譯，本身不帶獨立的 normative 效力。

一個 **conforming implementation** 會接受每個「特性標為 implemented 且格式良好」的程式、依所述規則拒絕每個格式
不良的程式，並重現規格所定的**可觀察行為**——程式輸出、exit 狀態、診斷——除非該行為明確標為
implementation-defined。conforming implementation 不必產出 C，也不必與參考編譯器的產碼、mangling 或記憶體佈局
相同。

## 語言 versus 這個 bootstrap

Zerg 是以整體來規範；Phase-1 bootstrap 實作其中一個子集。與其只描述已出貨的部分，每章都規範「意圖中的特性」並
標註其目前狀態，使規格成為穩定的目標、而缺口明確。每個特性帶有下列之一：

| 標記                         | 意義                                                   |
| ---------------------------- | ------------------------------------------------------ |
| **[implemented]**            | 種子（`zerg0`）已依規格實作。                          |
| **[not yet: Phase N]**       | 已規範、尚未建置。今天使用它會是一個乾淨的編譯錯誤。   |
| **[implementation-defined]** | 規格刻意不釘死；conforming implementation 可自行選擇。 |
| **[deviation]**              | 某個實作目前行為**不**符合此規格；一個被追蹤的 bug。   |

**一個標記指的是哪個編譯器。** 有兩個：`zerg0`，Go 主導的種子，唯一的工作是建置編譯器；以及 `zerg`，出貨的自舉
編譯器。本規格中的標記以**種子**為量測基準，逐特性標註的也是它的子集；出貨的 `zerg` 自己的子集則記載於
[`src/compiler/README.md`](../src/compiler/README.md)。因此一個標為 **[implemented]** 的特性，`zerg` 可能還不接受。

種子**不再一律是兩者中較寬的那個**。它在語言的大部分較寬——closure 與函式值、`map[K, V]`、`impl` 方法、
`Ref[T]`、`match` arm body 可以是區塊——但在**並行上大多較窄**：`zerg` 會降階 directional channel 端、方法與帶命
名空間的 `spawn` 被呼叫者、以及種子拒絕的 stdlib timer；接收運算子 `?` 則反過來，種子有做而 `zerg` 拒絕。
`is` 型別測試現在**兩邊一模一樣**：對 `Err` 測一個 error kind，其餘一律指名拒絕。
凡差異對程式有影響之處，該章會講明它在談哪一個編譯器；而 **[deviation]** 在主體不是種子時會指名主體。

有些特性曾被規範、在種子裡建成，之後在種子被削減到只剩單一職責時**移除**——closure 與函式值、`map[K, V]`、
`#[dyn]` dispatch、以及 `unsafe` 指標與 inline assembly。它們重新被標為 **[not yet]**：種子會以一則診斷拒絕它們，
而那正是這個標記所承諾的。

**並行完全屬於 `zerg`。** 種子一項都不降階——`spawn`、`chan[T]`、`select`、送出、接收、`close`
與 `for v in ch` 全部指名拒絕，非零 exit、不產出任何 C。這不是一個被追蹤的缺口，而是種子的**契約**
（見 [`src/bootstrap/README.md`](../src/bootstrap/README.md)）：種子支援自舉編譯器原始碼實際用到的
那一片語言，其餘一律拒絕。而自舉鏈裡完全沒有並行。

這個理由值得寫下來，因為它就是整份契約的論據：**一個去降階自己並非權威的章節的編譯器，就是第二
個會不合的實作**——而本規格在並行這一章需要收掉的每一個落差，都是那種不合。拒絕不會不合。出貨的
編譯器做了什麼，見 [Coroutines](code/coroutine.zh-TW.md)——它做了全部。

有一項屬於**兩個編譯器都沒有**：並行程式裡的 **`main(args)`**，因為 scheduler 的進入點 shim 只吃
無參數的函式指標。而接收運算子 **`?`** 是種子仍然**比較寬**的地方——它穿引 `Result[T]`，`zerg`
指名拒絕那一半。

沒有標記的小節沿用其外層特性的標記；段落可用自己的標記覆寫。**[deviation]** 一律同時陳述「規格所定行為」與
「該實作實際的作法」。

## 診斷契約

格式良好的程式會編譯成功；格式不良的會被拒絕、伴隨一個或多個**診斷**，且不產生輸出 binary。每個診斷寫到標準
錯誤，形式為

```text
file:line:col: message
```

其中 `line` 與 `col` 為 1-based。編譯失敗以非零狀態結束。診斷用字非 normative——兩個實作可以用不同措辭表達同一個
拒絕——但**哪些**程式被拒絕則是 normative（見各章規則；reject list 為 normative，訊息文字則否）。`fmt` 與 `lint`
工具僅供參考，永不改變程式的意義。

## Runtime abort 契約

一個**未捕捉的錯誤**會確定性地結束程式：一個 `raise` 未被捕捉而抵達 `main`、對缺席 optional 的 force `!` 失敗，或
一個沒有 `guard`/`?` 復原的內建 runtime fault（見 [Errors](code/errors.zh-TW.md)）。abort 時 runtime：

1. 把錯誤訊息寫到**標準錯誤**，後接一個換行；
2. 執行被展開路徑上待決的 `defer`（與正常 return 路徑用的是同一個 cleanup stack）；並
3. 以 exit 狀態 **1** 終止行程。

一個內建錯誤的訊息形式為 `Kind: text`（例如 `IndexError: list index out of range`）。確切的 `text` 非 normative；
taxonomy 錯誤的 `Kind:` 前綴則是。內建錯誤種類與哪些操作會引發它們見 [Errors](code/errors.zh-TW.md)。

> **[deviation]** runtime 無法攔截的硬體 fault——今天是 coroutine stack 溢出越過其 guard page，或 `main` 未受保護
> 的原生 stack——會以 signal 終止行程、不執行 `defer`，而非乾淨的 `StackOverflowError` abort。見 [Errors](code/errors.zh-TW.md)。

## Undefined 與 implementation-defined behavior

規格精確地使用這些術語：

- **Undefined behavior（UB）**——規格對結果不作任何要求。conforming 程式必須避免它；conforming implementation 則
  可做任何事，包含崩潰。Zerg 的設計目標是**從 safe code 無法觸及任何 UB**；凡 bootstrap 目前仍容許 UB 之處，該章
  會標為 **[deviation]**（例如整數溢位與除以零今天降成純 C，而非 trap——見 [Types](core/types.zh-TW.md)）。
- **Implementation-defined**——結果是實作所記錄的一組選項之一，但規格不釘死。conforming 程式不應依賴特定選擇。
  目前的 implementation-defined 點（各於其章節詳述）包含：call 引數與運算元的求值順序（[Memory Model](core/memory.zh-TW.md)
  ——規格意圖的左到右順序**[not yet]** 尚未強制）；`select` 在多個就緒 arm 間的勝出 arm（[Coroutines](code/coroutine.zh-TW.md)）；
  浮點渲染的精度與拼法（[Format](runtime/format.zh-TW.md)）；以及超出「送出→接收 happens-before」保證之外的任何 coroutine 排序
  （[Coroutines](code/coroutine.zh-TW.md)）。

規格既未要求、也未標為 implementation-defined 的任何事物，皆為 unspecified、可能變動；請勿倚賴。
