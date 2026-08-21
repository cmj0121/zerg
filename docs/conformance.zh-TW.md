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

## 兩種 profile

語言是一個,而實作要負責的是兩個。

**core profile** 是所有意義屬於語言自己的東西:字面量、運算式、函式、控制流、型別、pattern、並行、module
與清理。任何目標平台的實作都答得出來,而 conforming implementation **必須**答。

**system profile** 是 inline assembly、raw pointer,以及裝著它們的 `unsafe` 群組——那些意義屬於**一台機器**
而不是屬於語言的形式。沒有機器可談的實作(以 VM 為目標的、或永遠不 emit 的檢查器)**可以放棄這個 profile**。
放棄不等於沉默:裡面的每個形式仍然必須**被指名拒絕**,那是其他地方一律適用的標準規則。profile 改變的只是
「那個拒絕算不算缺陷」。

實作要**說明自己主張哪些 profile**。這一個主張 core、放棄 system。主張一個 profile 不等於已經做完:凡是 `zerg`
在某個 core 形式上不足的地方,該章會用 `[not yet]` 說出來、而那個形式被指名拒絕——那是**已主張的 profile 裡的
欠債**。被放棄的 profile 沒有這種欠債,而那就是全部的差別。

## 語言 versus 這個編譯器

Zerg 是以整體來規範;出貨的編譯器實作其中一個子集。與其只描述已出貨的部分,每章都規範「意圖中的特性」並標註
缺了什麼,使規格成為穩定的目標、而缺口明確。

**預設是「這個特性可以用」。** 沒有標記的散文描述的就是 `zerg` 已依規格實作的東西——那是常態,不加註記。只有
以下幾種帶標記:

| 標記                         | 意義                                                     |
| ---------------------------- | -------------------------------------------------------- |
| **[not yet]**                | 已規範、尚未建置。使用它是一個指名該形式的乾淨編譯錯誤。 |
| **[implementation-defined]** | 規格刻意不釘死;conforming implementation 可自行選擇。    |
| **[deviation]**              | 行為**不**符合此規格;一個被追蹤的 bug。                  |

真正要分清的是後兩者。**[not yet]** 是誠實的:編譯器說出那個形式的名字然後停下。它通常以 `NotImplemented` 說,
少數幾個形式則改由一條普通的受檢規則擋下——是哪些由該章交代,重點在於「有指名」而不在措辭。**[deviation]** 則是一個編得
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
錯誤，而讀者會遇到**兩種算繪**，因為診斷抵達標準錯誤的路徑有兩條。

由 **checker** 收集的規則透過診斷清單回報，那是完整的形式：

```text
error: E3033 cannot bind str to a int binding: `x`
  --> demo.zg:2:5
   |
 2 |     x: int = "s"
   |     ^
```

被 **parser 或 emitter 拒絕**的形式則在原地停止整趟執行，它印出的是句子，以及——當該處手上有位置時——後綴；
既沒有 `error:` 前綴，底下也沒有引出的原始碼行與 caret：

```text
E9011 NotImplemented: `unsafe { … }` as an EXPRESSION — GRAMMAR makes it a block whose value the
expression takes, and this compiler builds only the module-level `unsafe { … }` GROUP
  --> demo.zg:2:7
```

兩者都是診斷，也都在用字唯一 normative 的那個意義上是 normative 的：**哪些**程式被拒絕。算繪本身則否。這個差
別值得說出來而不是藏起來，因為那正是讀者實際遇到的。第二種形狀和第一種一樣帶著碼與位置；它不帶的是原始碼行
與底下的 caret，因為拒絕是在原地停止整趟執行，而不是加入某個稍後才被算繪的清單。

**位置是後綴、不是前綴**:句子先出現,`--> file:line:col` 排在它底下那一行,其中 `line` 與 `col` 為 1-based。
編譯失敗以非零狀態結束。診斷用字非 normative——兩個實作可以用不同措辭表達同一個
拒絕——但**哪些**程式被拒絕則是 normative（見各章規則；reject list 為 normative，訊息文字則否）。`fmt` 與 `lint`
工具僅供參考，永不改變程式的意義。

診斷之後**可以**附上它所指的原始碼行、以及一個標記該行上何處的 caret；`zerg` 對第一種形狀會算繪，第二種則不
會。conforming 實作不必如此，其形狀也非 normative——normative 的只有「有給位置時,它要指出檔案、行與欄」。一個
格式不良的程式**應該**在一次執行中報出所有找得到的診斷，而不是停在第一個 —— `zerg` 對它所檢查的規則就是如
此；而一個拒絕會在第一個就結束整趟執行，那正是兩種形狀的另一半意義。

診斷只能說出**原始碼裡有的東西**。實作可以綁定屬於自己的名字——`zerg` 就會，為的是把 `assert` 的運算元留住，
好讓失敗時報出條件當下真正看到的值——但沒有任何規則可以引用其中之一：讀者被告知一個在他們打得開的檔案裡都
找不到的綁定，等於拿到一個名字卻無處可去。這就是下方那條「絕不由生成的、沒人寫過的程式碼來回答」的常設規則
再往裡一層——一個沒人打得開的位置，和一個沒人找得到的名字。`scripts/reject-check.sh` 與
`scripts/refuse-check.sh` 對它們持有的每一個案例都斷言這一點。

> 每一則診斷都欠一個位置與一個碼，而決定它有沒有帶著兩者的是**通道**，不是規則屬於檢查還是拒絕。每一個回答
> **程式**問題的階段都有通道——lexer.zg 的 `bad`、parser.zg 的 `p_diag`、check.zg 的 `chk_at`、emit.zg 的
> `c_diag`——每一條都把碼當成引數收下、位置自己讀，所以一處不可能帶了其中一個卻忘掉另一個。
>
> ---
>
> 今日量測。**parser 這一半做完了。** 它 **103** 處 raise 中，除了一處以外都走它的通道。九條原本完全沒有碼的規則——包括萬用兜
> 底 _`X` is not an expression this compiler reads_——拿到了 `E2054`–`E2059`，每一條各配一個 gate 案例與一列
> catalogue。剩下一處 raise 刻意兩者皆無：`p_impossible`，那是任何程式都到不了的分支，給它一個碼等於給出一個沒
> 有任何案例能斷言的身分。那次改動看得見的形狀是：`scripts/reject-check.sh` 退掉 **31** 個 `no-place` 標記，而
> `reject-fuzz` 的 `write-immutable` 上限，也就是 parser 最後一處沒有位置的拒絕，降到了零。
>
> **emitter 這一半也做完了。** 它原本有 **126** 個 raise 語句，其中 **76** 個以碼開頭、**13** 個接上位置；如今
> 的 **123** 個全部走 `c_diag` / `c_diag_at`，少掉的三個都是不再 raise 的規則——struct 與 enum 現在各自記錄自己
> 的位置，所以重複宣告和另外四種宣告一樣走檢查通道；而「subject 拿不到的 variant」那兩條（`E4030`、`E4031`）也
> 因為同一個理由改成由檢查通道記錄。四十三條原本沒有碼的規則各自拿到一個碼，每一條各
> 配一個 gate 案例與一列 catalogue；emitter 的號碼段用滿後，最後一個號碼未發出就退場,和 parser 當初關掉自己那
> 段的做法一模一樣——兩者都發生在這份目錄已經不再使用的三位數編號底下。**這裡沒有任何一處 raise 是例外。** 曾有兩處被寫成 ICE，理由是「唯一能走到它的形式已被 parser 擋下」，而
> 兩個理由都錯了——`p_builtin_type_ctor` 把六個名字排除在 `E2035` 之外，其中四個不是保留字，所以
> `fn set[T](…)` 與 `set[int, str](1)` 走得到那條 arity 規則；而 `1..=nil` 是把裸 `..=` 從前留下的形狀親手寫出
> 來。一條不可達的規則必須被證明不可達，而這兩條都沒有。這次改動看得見的形狀是：再退掉 **18** 個 `no-place`
> 標記，而 `scripts/refuse-check.sh` 的 `place` 標記整個消失了——那裡的每一個 `zerg` 案例現在都被斷言帶著位置，
> 因為已經沒有可以不帶的案例了。
>
> **lexer 這一半也做完了，它曾是兩處。** _f-string: unterminated literal_ 與 _f-string: a bare '}' is not
> text_ 從 `scan_fstring` 裡 raise 一句白話——沒有 `error:` 行、沒有碼、沒有位置——而那個函式回的是 Token，它
> 其他每一處拒絕早就走 `bad` 出去了。現在它們是 `E1012` 與 `E1013`，而它們當初 raise 的理由比它們更早退場：
> Illegal token 曾經沒有人讀，而 `lex_diags` 是獨立的一個 pass，把每一個 Illegal token 連同它記下的碼與位置
> 一起回報。`E1013` 指的是那個**大括號**，不是含著它的字面值——這裡其他拒絕都指向字面值的開頭，因為那就是出
> 錯的全部，而這一條有一個字元可以指名。
>
> `scripts/error-codes-check.sh` 靠比對三個集合是看不見一條沒有碼的規則的：它比對的是已經存在的碼與 gate、
> catalogue 三者，而一條沒有碼的規則在三者裡都不存在。它改用另一個問題同時看見 parser 與 emitter 這兩半——這兩
> 個檔案裡若有一個 `raise` 自己寫訊息、而不是向通道要一則，就會被指名報出來——這個斷言正是讓通道不會被一處一處
> 繞過去的東西。它是一道棘輪、不是一個證明：它看見的是字串**字面量**，所以先把訊息接到一個變數上就會通過，而且
> 它必須放行 `raise anything(…)`，因為這兩個檔案裡的每一處 raise 的都是一個呼叫。
>
> ---
>
> **檢查的規則並不豁免**，這正是舊文字說反了的地方，而原本被指名的那兩條都已經搬走了。常數環（`E4068`）回報時
> 沒有位置也沒有碼；如今它以自己的碼開頭，並指向第一個拿不到值的常數。`E3078`——一個名字被宣告兩次——原本是有些
> 宣告形式帶位置、有些不帶：重複的 `type A = …` 帶位置，重複的 `struct` 不帶，因為 struct 與 enum 是在任何東西
> 記下位置之前就被登記的。兩者如今都帶著宣告自己的行號，而那條在 raise 與記錄之間做選擇的通道，也隨著它存在的
> 理由一起消失了。
>
> 還有兩條曾經在這份名單上、現在不在了：`` `x` is used after del `` 與它 on-some-paths 的手足，如今是 `E2052`
> 與 `E2053`。規則本身沒有任何改變——它們從 `raise` 搬到了檢查通道，而那正是唯一決定這個問題的東西，
> 這一搬就是整個修正。
>
> `zerg` 記錄的位置是**逐語句**的，所以欄位指的是語句的起點；當訊息引用了該行上的某個 token 時，caret 會收斂到
> 那個 token。跑在**宣告**上的規則——欄位的預設值、重複的 variant 名字、被宣告兩次的方法——改為帶宣告自己的位
> 置，因為它們跑在任何語句被產出之前，那時標記還沒有東西可指。

本專案要求自己遵守的規則比上面幾段更強，而且值得單獨寫出來——因為它是本規格裡每一個發現被衡量時所用的尺：

**一個形式要嘛被正確下降、要嘛被具名拒絕。** 它永遠不是崩潰、永遠不是靜默的錯誤答案，也永遠不是 C 編譯器或
linker 對著沒人寫過的產生碼所報的錯。

> **[deviation]** 在一個**沒有人實例化的 template** 裡，一個形式兩者皆非。這個編譯器強制的每一條規則，都由「把
> body **下降**」那趟走訪驅動，而 template 在那趟走訪之前就被移除了——只有呼叫端要求的 specialization 會被下降
> ——所以沒有任何呼叫抵達的 `fn f[T](xs: list[T], v: T) { xs.append(v) }` 安靜地編得過，同一個 body 去寫一個
> immutable binding 也一樣，而那是 `E3006`、一條在其他每個地方都被強制的規則。seed 兩個都會診斷，因為它的語意
> pass 走的是**宣告**而不是下降。這是欠一次的一個缺口，不是任何單一規則的性質。要補上它，body 必須對型別參數的
> **bound** 檢查、而不是對具體型別——`T: Show` 上的 `x.show()` 在 `T` 還不是某個具體型別以前沒有 method 可解析，
> 而下降只定義在具體型別上——那是這個編譯器還沒有的檢查器。

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
> 或 `precision` 超過 **4096** 會被拒絕而不是照做：兩者都是被格式化的文字要求實作產出的**大小**，而沒有界的
> 那一個是披著渲染外衣的記憶體請求。**float** 另外最多渲染 **100** 位小數,那是對位數而不是對欄位的界,所以
> 是 float 自己的。`type` 字母對每一種渲染都是封閉集合
> ([文字與格式化](runtime/format.zh-TW.md))；落在集合外的字母以同樣的方式被拒。
>
> 這些今天全都搆不到。唯一會要求 width 或 precision 的表面形式是 **f-string hole 裡的 format spec**,而它是
> `[not yet]`:每一個 `{x:…}`(包含 `{x:.2f}`)都回報 _E9012 NotImplemented: an f-string ':spec' format spec_。
> 三個上限實作在 runtime 裡,而出貨的編譯器不會發出任何抵達它們的呼叫,所以這一段記載的是一份程式還觀察不到的契約。

## Runtime abort 契約

一個**未捕捉的錯誤**會確定性地結束程式：一個 `raise` 未被捕捉而抵達 `main`、對缺席 optional 的 force `!` 失敗，或
一個沒有 `guard`/`?` 復原的內建 runtime fault（見 [Errors](code/errors.zh-TW.md)）。abort 時 runtime：

1. 把描述該錯誤的**一行**寫到**標準錯誤**，後接一個換行；
2. 執行被展開路徑上待決的 `defer`（與正常 return 路徑用的是同一個 cleanup stack）；並
3. 以 exit 狀態 **1** 終止行程。

一個 **taxonomy** 錯誤寫出的那一行形式為 `Kind: text`，其中 `text` 是該錯誤的 `message()`——例如
`IndexError: index out of range`。確切的 `text` 非 normative；`Kind:` 前綴則是。它屬於**那一行**、不屬於訊息：
`message()` 只回答 `text` 本身，而前綴會對**任何**被 raise 的 taxonomy `Err` 渲染——程式自己寫的
`raise ValueError("bad input")` 與 runtime 自己引發的 fault 報出同一種形狀。**不帶**種類的錯誤（一個裸的
`raise "…"` 建出來的那種）則只寫它的訊息。內建錯誤種類與哪些操作會引發它們見 [Errors](code/errors.zh-TW.md)。

> **[deviation]** stack 溢位——coroutine 越過其 guard page，或 `main` 越過其原生 stack——如今會帶著名字死去：
> runtime 的 fault handler 將 `StackOverflowError: stack overflow` 寫到標準錯誤、並以 exit 狀態 **1** 終止，
> 即上述契約的第 1、3 步。仍偏離的是第 2 步：出錯的 stack 已耗盡、無法從 signal handler 展開，所以待決的
> `defer` 被**跳過**、不執行；而且與一般 abort（coroutine 會把它包住）不同，溢位無論發生在哪裡都結束整個行程。
> handler 不認得的 fault 會交還給 runtime 之前持有該 signal 的那個 action（sanitizer 的 handler，或預設處置），
> 因此它仍以其本來的 signal 死去、該 handler 的診斷完整保留。它真正宣稱的兩個窗口**各為一頁**——coroutine 的
> guard page 恰好一頁，以及 `main` stack 下界之下的那一頁——這也正是它可能誤命名的全部範圍：落在 `main`
> 下方那一頁的存取會被讀成溢位。見 [Errors](code/errors.zh-TW.md)。

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
  會標為 **[deviation]**（例如 stack overflow 是一次硬體 fault，runtime 將其命名為 `StackOverflowError`
  並以 exit 1 結束，而非一次會跑待決 `defer` 的乾淨 unwind——見 [Errors](code/errors.zh-TW.md)）。
- **Implementation-defined**——結果是實作所記錄的一組選項之一，但規格不釘死。conforming 程式不應依賴特定選擇。
  目前的 implementation-defined 點（各於其章節詳述）包含：`select` 在多個就緒 arm 間的勝出 arm（[Coroutines](code/coroutine.zh-TW.md)）；
  浮點渲染的精度與拼法（[Format](runtime/format.zh-TW.md)）；以及超出「送出→接收 happens-before」保證之外的任何 coroutine 排序
  （[Coroutines](code/coroutine.zh-TW.md)）。

規格既未要求、也未標為 implementation-defined 的任何事物，皆為 unspecified、可能變動；請勿倚賴。
