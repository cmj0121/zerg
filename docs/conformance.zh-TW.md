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

## 語言 versus 這個編譯器

Zerg 是以整體來規範;出貨的編譯器實作其中一個子集。與其只描述已出貨的部分,每章都規範「意圖中的特性」並標註
缺了什麼,使規格成為穩定的目標、而缺口明確。

**預設是「這個特性可以用」。** 沒有標記的散文描述的就是 `zerg` 已依規格實作的東西——那是常態,不加註記。只有
以下幾種帶標記:

| 標記                         | 意義                                                  |
| ---------------------------- | ----------------------------------------------------- |
| **[not yet]**                | 已規範、尚未建置。使用它會 raise `NotImplemented`。   |
| **[implementation-defined]** | 規格刻意不釘死;conforming implementation 可自行選擇。 |
| **[deviation]**              | 行為**不**符合此規格;一個被追蹤的 bug。               |

真正要分清的是後兩者。**[not yet]** 是誠實的:編譯器說出那個形式的名字然後停下。**[deviation]** 則是一個編得
過、但行為與這裡所寫不同的程式——而本專案的常規是「一個形式不是被實作、就是被指名拒絕,絕不靜默地錯」,所以
deviation 是一個欠著修的 bug,不是一個被記載下來的狀態。

**以哪個編譯器為準。** 標記以 **`zerg`** 為量測基準——那是自舉、實際出貨、`make` 之後放進 `bin/` 的那一個。
另一個 `zerg0` 是 Go 主導的種子,唯一的工作是建置 `zerg`;它支援的是更窄的一片——編譯器自己原始碼所用到的
那部分——其餘一律指名拒絕。**種子的缺口不在這裡標註。** 它們不是語言的缺口,寫 Zerg 的讀者也永遠碰不到;
它們列在 [`src/bootstrap/README.md`](../src/bootstrap/README.md),那是種子自己的契約。

沒有標記的小節繼承其所屬特性的標記;段落可以自行覆寫。**[deviation]** 一定同時說明規範的行為、以及實作實際
做了什麼。

## 診斷契約

格式良好的程式會編譯成功；格式不良的會被拒絕、伴隨一個或多個**診斷**，且不產生輸出 binary。每個診斷寫到標準
錯誤，形式為

```text
file:line:col: message
```

其中 `line` 與 `col` 為 1-based。編譯失敗以非零狀態結束。診斷用字非 normative——兩個實作可以用不同措辭表達同一個
拒絕——但**哪些**程式被拒絕則是 normative（見各章規則；reject list 為 normative，訊息文字則否）。`fmt` 與 `lint`
工具僅供參考，永不改變程式的意義。

診斷之後**可以**附上它所指的原始碼行、以及一個標記該行上何處的 caret；`zerg` 會算繪一個。conforming 實作不必
如此，其形狀也非 normative。一個格式不良的程式**應該**在一次執行中報出所有找得到的診斷，而不是停在第一個 ——
`zerg` 對它所檢查的規則就是如此。

> **[deviation]** `file:line:col` 前綴出現在 `zerg` **檢查**的規則上，而在它**拒絕**的形式上缺席：來自 parser
> 或 emitter 的 `NotImplemented` 仍然只報形式名稱、不報位置。`zerg` 記錄的位置是**逐語句**的，所以欄位指的是
> 語句的起點；當訊息引用了該行上的某個 token 時，caret 會收斂到那個 token。

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

## 參考實作 emit 出來的 C

參考實作把 Zerg 下降成 C，再交給 C 編譯器（`cc`）。依[何者為 normative](#何者為-normative)，這是一則實作註記
而非對 conforming implementation 的要求——它不約束一個直接產生機器碼的實作，對 Zerg 程式而言也不可觀察。之所以
寫下來，是因為 dialect 是本專案在首頁上作出的宣稱，而一個沒有任何章節寫明的編譯器旗標數字，就是會漂走的那種數字。

dialect 是 **C17**。`ZERG_CSTD` 為需要的建置指定另一個——`c99` 與 `c11` 是 runtime 另外兩個寫得能編譯的 dialect，
而 build cache 以 dialect 作為 object 的 key 的一部分，兩者不會把彼此的 object 交給對方。

> **[not yet]** **fallback 不是自動的**。原意是：一個做不到 C17 的 `cc` 應被退回 C99；但沒有建置任何探測，所以這個
> 退回是建置用 `ZERG_CSTD=c99` **主動要求**的，而不是編譯器自己發現的。兩種 dialect 都在 CI 上編譯並執行。

## Undefined 與 implementation-defined behavior

規格精確地使用這些術語：

- **Undefined behavior（UB）**——規格對結果不作任何要求。conforming 程式必須避免它；conforming implementation 則
  可做任何事，包含崩潰。Zerg 的設計目標是**從 safe code 無法觸及任何 UB**；凡 bootstrap 目前仍容許 UB 之處，該章
  會標為 **[deviation]**（例如 coroutine 的 stack overflow 今天是一次硬體 fault、而非乾淨的
  `StackOverflowError`——見 [Errors](code/errors.zh-TW.md)）。
- **Implementation-defined**——結果是實作所記錄的一組選項之一，但規格不釘死。conforming 程式不應依賴特定選擇。
  目前的 implementation-defined 點（各於其章節詳述）包含：call 引數與運算元的求值順序（[Memory Model](core/memory.zh-TW.md)
  ——規格意圖的左到右順序**[not yet]** 尚未強制）；`select` 在多個就緒 arm 間的勝出 arm（[Coroutines](code/coroutine.zh-TW.md)）；
  浮點渲染的精度與拼法（[Format](runtime/format.zh-TW.md)）；以及超出「送出→接收 happens-before」保證之外的任何 coroutine 排序
  （[Coroutines](code/coroutine.zh-TW.md)）。

規格既未要求、也未標為 implementation-defined 的任何事物，皆為 unspecified、可能變動；請勿倚賴。
