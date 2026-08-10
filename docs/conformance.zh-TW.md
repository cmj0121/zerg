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

本專案要求自己遵守的規則比上面幾段更強，而且值得單獨寫出來——因為它是本規格裡每一個發現被衡量時所用的尺：

**一個形式要嘛被正確下降、要嘛被具名拒絕。** 它永遠不是崩潰、永遠不是靜默的錯誤答案，也永遠不是 C 編譯器或
linker 對著沒人寫過的產生碼所報的錯。

有一個推論值得寫在這裡，因為沒有任何單一章節擁有它：沒有 `fn main` 的程式在文法上是合法的——
`program ::= stmt-list`，也就是 grammar 開場的那個 `nop` 程式——所以拒絕它的是**建置**。`--emit bin` 會在任何
東西碰到 cc 或 linker 之前，帶著位置報告進入點檔案沒有宣告 `fn main`（規則見
[Packages & Programs](runtime/package.zh-TW.md)）；同一份原始碼用 `--emit lib` 則建成 object 檔，那正是一個
module 的用途。

> **[implementation-defined]** **巢狀深度是一個 translation limit。** 程式巢狀超過 **200** 層時會被拒絕，並
> 給出一個說出上限與位置的診斷，而不是繼續解析、直到它所站的原生堆疊溢出為止。這個上限在**樹被建起來的地方**
> 執行，而且用兩種方式，因為巢狀是以兩條路徑抵達 parser 的：它數自己的遞迴——每一層巢狀的運算式、block 或
> 型別算一層，那是 `(((…)))` 的代價——並且量每一棵**建完的運算式樹**有多深，那是遞迴看不見的形狀，因為一條
> 扁平的鏈（`1 + 1 + … + 1`、一長串方法呼叫）在 parser 裡是用**迴圈**讀的，它把樹加深卻不會把 parser 加深。
> 於是一份程式**寫下**的運算式沒有任何一棵深過 200 層，之後每一個走訪它的階段——checker、linter、language
> server、泛型實例化跑的代換——都是繼承這個上限，而不是再數一次。
>
> 這個上限管的是 implementation 必須**走訪**的那棵樹，而那不一定是程式寫出來的：一個省略掉的預設引數會在呼叫
> 點被補回去，所以一個 190 層的預設值被補進一個 190 層的呼叫裡，就是一趟 380 層的走訪——而原始碼裡沒有任何一
> 個運算式說出這個深度。emitter 自己數深度正是為了這件事，並且以一句談「鏈」而不是談「巢狀」的訊息拒絕它。無
> 論程式以哪種方式到達 200 層——寫出來的或組合出來的——答案都是一個拒絕。
>
> 這個數字是量出來的餘裕，不是語言規則：不設限的 parser 在 8 MB 堆疊上大約 485 層巢狀括號時死於 `SIGSEGV`，
> 運算式走訪在大約 310 個方法環節（530 個扁平 `+` 項）時耗盡，代換階段則在大約 400 時耗盡，而整個 repository
> 裡最深的實際巢狀是五層。conforming implementation 可以另定上限；ISO C 自己也只承諾 63 層括號運算式。

---

> **[implementation-defined]** **format spec 的 width 與 precision 是翻譯上限。** `width` 超過 **4096**
> 或 `precision` 超過 **100** 會被拒絕而不是照做：兩者都是被格式化的文字要求實作產出的**大小**，而沒有界的
> 那一個是披著渲染外衣的記憶體請求。`type` 字母對每一種渲染都是封閉集合
> ([文字與格式化](runtime/format.zh-TW.md))；落在集合外的字母以同樣的方式被拒。

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
而 build cache 以 **dialect 與解析後的 `cc`**——一次編譯中,emitted C 沒有替它們代言的那兩個輸入——作為
object 的 key,所以兩種 dialect、兩個編譯器,都不會把彼此的 object 交給對方。

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
