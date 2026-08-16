# Zerg 編譯診斷

編譯器回報的每一個代碼,以及它指名的規則。屬於
[語言參考](../language.zh-TW.md)的一部分。亦有 [English](diagnostics.md)。

`F` 與 `L` 代碼是**工具**對一個「已經建置得起來」的程式所說的話,由[格式化器](fmt.zh-TW.md)與
[檢查器](lint.zh-TW.md)各自帶著。

這些不是建議。撞上其中任何一條的程式不會建置成功，所以每一條都是**編譯錯誤**、建置會停下來。
它們帶代碼,是因為代碼是一條規則的**穩定身分**,而句子不是:散文會被改得更好,而釘住句子的 gate
會因此變紅。代碼依**回報它的階段**分段,那也是一次建置遇到它們的順序:

| 區段   | 階段 | 回報什麼                                      |
| ------ | ---- | --------------------------------------------- |
| `E1xx` | 詞法 | 不是 Zerg token 的文字                        |
| `E2xx` | 剖析 | 不構成 Zerg 形式的 token                      |
| `E3xx` | 檢查 | 形式成立、但意思兜不攏                        |
| `E4xx` | 產出 | 這個編譯器不會下放的形式,含 `[not yet]`       |
| `E5xx` | 建置 | 程式作為一組檔案,那是單一檔案的文字回答不了的 |
| `E6xx` | 剖析 | 又是剖析器:`E2xx` 滿了,而一段只有一百個號碼   |
| `E7xx` | 產出 | 又是產出器:`E4xx` 滿了,理由一模一樣           |

`E5xx` 是唯一不落在那個順序裡的一段。一次建置在詞法分析被 import 指到的檔案之前就先解析 import,
並在一切都產出之後才去找 `fn main`,所以驅動器自己的發現是把另外四段夾在中間,而不是坐在其中兩段之間。

`E6xx` 根本不是一個階段——它是**接著 `E2xx` 的**,而 `E7xx` 同樣是**接著 `E4xx` 的**,理由一模一樣。
一段只有一百個號碼,剖析器用掉了其中九十八個;當剖析器的拒絕全部搬到同一條回報通道上時,新到的規則在
`E2xx` 裡已經無處可去。產出器晚了一次改動跟上——它的 126 個拒絕裡有 50 個根本沒有代碼,而 `E4xx` 只剩
一個號碼——所以同樣的動作又做了一次。在新的一段接著數下去,才守得住這套機制存在的兩個性質:號碼永不重用,
而讀到號碼的人分得出是哪個階段在說話。

**一段用滿時,就為那個階段開一段接續,並把舊的一段關掉。** 一個階段有兩段開著,就是兩個地方可以配號,
而那正是一週內撞號三次的成因。關掉的做法就是下面已退場表裡 `E299` 在做的事:它從未發出過,而讓一個號碼
退場、卻不花掉它,`E2xx` 讀起來才是**滿的**,而不是還剩一格等人來拿。`E499` 是隔壁一段的同一個動作,
在產出器的號碼越過 `E498` 那天做的:它同樣從未發出過,而 `E4xx` 因此讀起來是滿的。

這也是為什麼上面那張表指名的是**階段**而不是號碼段,以及為什麼 `make error-codes-check` 是按階段回答的:

```text
error-codes-check: next free code per stage — building E513, checking E399, emitting E746,
                                              lexical E112, parser E615
```

號碼段和它們的階段都是從上面那張表讀出來的,而不是在腳本裡另存一份,所以一個階段的答案取自它**最高**
的那一段——接續的意思就是這個。因此新增一段接續的號碼就是在這裡多一列,建議會跟著那一列走。

代碼放在**訊息的最前面**、句子之前:`E109 invalid escape in a rune literal`。當一則診斷帶有位置時,
renderer 的 `error:` 會開在它前面(`error: E109 …`);還沒學會位置的拒絕則單獨印訊息——兩種情況下,
代碼都是那一行的第一個東西。

**代碼在 gate 釘住它時才存在,不會更早。** `scripts/refuse-check.sh` 與 `scripts/reject-check.sh`
都改以代碼而非句子斷言,而一個仍然釘著散文的 `zerg` 案例會具名地失敗——否則一份大部分是代碼、只剩幾句散文的
清單,從外面看起來就像已經做完了。`scripts/error-codes-check.sh` 把三份清單互相拉住:編譯器回報了什麼、哪個 gate 釘住它、
以及這張表列了什麼。問它這個問題,問出了**十三條從來沒有任何案例讓它發火的規則**;它們就放在
`reject-check.sh` 的最後一節。

一個 reject 案例只有在數個案例共用同一個代碼時才另外留下**句子**,因為那時每個案例證明的是規則指名了哪些值。
seed 全程維持句子比對:代碼是語言的契約,而 seed 是建置正式編譯器的工具、不是它的一部分
(這條線[一致性](../conformance.zh-TW.md)在拒絕標記 seed 的缺口時就畫過)。

## 目錄本身

| 代碼   | 規則                                                                                                  |
| ------ | ----------------------------------------------------------------------------------------------------- |
| `E101` | 字串字面量在行尾之前沒有閉合                                                                          |
| `E102` | rune 字面量是空的——它恰好裝一個字元                                                                   |
| `E103` | rune 字面量恰好裝一個字元，而這個裝了更多                                                             |
| `E104` | 這個字元不屬於任何 Zerg token                                                                         |
| `E105` | 三引號字串沒有被閉合                                                                                  |
| `E106` | raw 字串在這一行沒有閉合引號                                                                          |
| `E107` | 命令字面量沒有閉合的反引號                                                                            |
| `E108` | 帶進位前綴的數字，前綴後緊接著就要有一位數字                                                          |
| `E109` | 字串／rune／byte 字面量裡有無效的跳脫序列                                                             |
| `E110` | 字串字面量裡不能有 NUL                                                                                |
| `E111` | `…` 不是 UTF-8 文字,而 Zerg 原始檔就是 UTF-8 文字                                                     |
| `E201` | `close` 是關鍵字,不是 select arm head                                                                 |
| `E202` | 沒有 arm 的 select——它等不到任何東西                                                                  |
| `E203` | 不是 send、receive 或 `_` 的 select arm head                                                          |
| `E204` | expected `…`, found `…`                                                                               |
| `E205` | expected a newline or `;` to separate statements, found `…`                                           |
| `E206` | `Either[…, …]` has the same type on both sides                                                        |
| `E207` | 參數化的 `…[…]` 作為 …——**[not yet]**                                                                 |
| `E208` | `#[derive(…)]` has no declaration under it                                                            |
| `E210` | 帶 BODY 的 `spec` 成員——**[not yet]**                                                                 |
| `E211` | associated value 不是 `spec` 的成員                                                                   |
| `E212` | 泛型 enum `…[…]`——**[not yet]**                                                                       |
| `E213` | an enum discriminant is distinct across variants, and `… = …` repeats one already given               |
| `E214` | 在 variant 帶 payload 的 enum 上寫判別值 `… = …`——它的 tag 是不透明的                                 |
| `E215` | 泛型 struct `…[…]`——**[not yet]**                                                                     |
| `E217` | decorator `#[…]`——**[not yet]**                                                                       |
| `E218` | `impl` 裡的 associated value 綁定 `… := …`——**[not yet]**                                             |
| `E219` | `…` 作為 `impl` 的項目——**[not yet]**                                                                 |
| `E221` | struct pattern `…{…}`——**[not yet]**                                                                  |
| `E222` | 呼叫 …——**[not yet]**                                                                                 |
| `E223` | 具名引數 `…:`——**[not yet]**                                                                          |
| `E224` | `unsafe { … }` 作為運算式——**[not yet]**                                                              |
| `E225` | f-string 的 ':spec' 格式規格——**[not yet]**                                                           |
| `E226` | f-string 的 '!r' / '!s' / '!a' 轉換——**[not yet]**                                                    |
| `E227` | f-string 的 '{expr=}' 自述形式——**[not yet]**                                                         |
| `E230` | associated type 不是 `spec` 的成員                                                                    |
| `E231` | `impl` 裡的 associated type 綁定 `type … = …`——**[not yet]**                                          |
| `E232` | `match` arm 裡的 tuple pattern——**[not yet]**                                                         |
| `E233` | 陣列型別 `[T; N]`——**[not yet]**                                                                      |
| `E234` | `match` arm 裡的 `as` 綁定——**[not yet]**                                                             |
| `E235` | 會內插的 command literal——**[not yet]**                                                               |
| `E236` | command literal——**[not yet]**                                                                        |
| `E238` | 解構綁定 `(a, b) := …`——**[not yet]**                                                                 |
| `E239` | 沒有下界的 range——**[not yet]**                                                                       |
| `E240` | `match` arm 裡的 list pattern——**[not yet]**                                                          |
| `E241` | or-pattern——**[not yet]**                                                                             |
| `E242` | `for mut v in …`——**[not yet]**                                                                       |
| `E243` | `match` arm 裡的 struct pattern `…{…}`——**[not yet]**                                                 |
| `E244` | 這個程式的巢狀超過 … 層                                                                               |
| `E245` | `…` 是保留字,不能用來命名…                                                                            |
| `E246` | tuple 型別要有兩個以上的元素                                                                          |
| `E247` | `pub import` 不是一個形式                                                                             |
| `E248` | `pub` 不放在 `init()` 上                                                                              |
| `E249` | `pub` 不放在 `impl` 區塊上                                                                            |
| `E250` | decorator 領在宣告前面、`pub` 坐在它裡面:寫 `#[…] pub fn …`,不是 `pub #[…]`                           |
| `E251` | 自由函式不會是 `mut fn`                                                                               |
| `E252` | `pub` 不放在 `unsafe { … }` 群組上                                                                    |
| `E253` | module 層的 `unsafe { … }` 群組不巢狀                                                                 |
| `E254` | module 層的 `unsafe { … }` 群組裝的是宣告                                                             |
| `E255` | `pub` 綁在一個宣告上,而語句沒有宣告                                                                   |
| `E256` | 這個 module 層的 `unsafe { … }` 群組沒有被閉合                                                        |
| `E257` | `…` 是保留字,不能用來命名一個繫結                                                                     |
| `E258` | `…(…)` 轉換一個值,而這裡一個也沒給                                                                    |
| `E259` | `…(…)` 轉換一個值,而這裡給了 …                                                                        |
| `E260` | `list[T](…)` 轉換一個值,而這裡一個也沒給                                                              |
| `E261` | `list[T](…)` 轉換一個值,而這裡給了 …                                                                  |
| `E262` | match arm 的 guard 放在 `=>` 之前                                                                     |
| `E263` | 參數是 `mut &` 或什麼都不是                                                                           |
| `E264` | 獨立的 `unsafe fn` 宣告 —— **[not yet]**                                                              |
| `E265` | associated type 投影 `….…` —— **[not yet]**                                                           |
| `E266` | 值 generic 參數 `…: …` —— **[not yet]**                                                               |
| `E267` | import 路徑是一個字串                                                                                 |
| `E268` | `…[…]` 後面沒有呼叫 —— **[not yet]**                                                                  |
| `E269` | 分支超過一個語句的 `if` 運算式 —— **[not yet]**                                                       |
| `E270` | `if` 運算式裡的繫結頭 —— **[not yet]**                                                                |
| `E271` | `asm(…)` —— **[not yet]**                                                                             |
| `E272` | `…(…)` 轉換一個值,而這裡一個也沒給                                                                    |
| `E273` | `…(…)` 轉換一個值,而這裡給了 …                                                                        |
| `E275` | 呼叫寫明型別引數,而 postfix `[ … ]` 是索引                                                            |
| `E276` | `spec` 成員既不是簽章也不是 provided method                                                           |
| `E277` | `impl` 既不在 spec 的 module、也不在型別的                                                            |
| `E278` | enum 上的 `#[derive(S)]`,而某個方法收 `This`                                                          |
| `E279` | enum 上的 `#[derive(S)]`,而某個 variant 不帶值                                                        |
| `E280` | `#[obj]` 標在不是 `spec` 的東西上                                                                     |
| `E281` | `#[obj]` 遇上 `mut fn`——被包起來的值是複本                                                            |
| `E282` | `#[obj]` 遇上收 `This` 的方法——object 已經忘了自己的型別                                              |
| `E283` | `#[derive(…)]` 標在沒有結構可讀的東西上                                                               |
| `E284` | `??` 右側的 diverge 帶了尾隨的 `if` guard                                                             |
| `E285` | closure 參數上的預設值 —— **[not yet]**                                                               |
| `E286` | 函式型別裡的 `mut &` 參數 —— **[not yet]**                                                            |
| `E287` | `spec` 裡的 `unsafe` 簽章 —— **[not yet]**                                                            |
| `E291` | `impl` 自己帶著型別參數 `[…]` —— **[not yet]**                                                        |
| `E292` | 標在 `…[…]` 上的 `impl` —— 目標帶了型別引數 —— **[not yet]**                                          |
| `E288` | 1-tuple `( e, )`——單一 `( expr )` 只是分組                                                            |
| `E289` | 收尾的 `)`、`]` 或 `}` 前的尾隨逗號                                                                   |
| `E290` | `if`/`for`/`with`/`match` head 開頭的 `{` 開頭運算式                                                  |
| `E293` | `…` 是保留字,不能用來命名欄位                                                                         |
| `E294` | `.` 後面要的是欄位名稱(或 tuple 索引),卻遇到 `…`                                                      |
| `E295` | `del …` 指的名字這支程式沒有宣告過                                                                    |
| `E296` | `del …` 指到的是函式、struct、enum 或 variant —— 都不是繫結                                           |
| `E297` | `…` 在 del 之後仍被使用                                                                               |
| `E298` | `…` 在某些路徑上於 del 之後仍被使用                                                                   |
| `E301` | `…` 不是 module `…` 的公開成員                                                                        |
| `E302` | `…` 不是一個位置,而賦值需要一個                                                                       |
| `E303` | 不能對 `…` 賦值:它是 module `const`,而常數永遠不被寫入                                                |
| `E304` | 對非純量做 `type … = …`——**[not yet]**                                                                |
| `E305` | 不能對 `…` 賦值:它是 module 繫結,而最上層是不可變的                                                   |
| `E306` | 不能對 `this` 賦值:方法的接收者是一份複本,寫穿它的形式是 `mut fn`                                     |
| `E307` | 不能對 `…` 賦值:它是不可變的                                                                          |
| `E308` | 不能穿過 `…` 賦值:它是不可變的                                                                        |
| `E309` | `…` 的參數 `…` 是 `mut &`,不能有預設值                                                                |
| `E310` | `…` 的 `…` 預設值是 …,而參數是 …                                                                      |
| `E311` | `…` 帶 …,而這裡 … …                                                                                   |
| `E312` | `…` 的第 … 個引數是 `mut &`,不能跨過 `…`:借用不可被捕捉                                               |
| `E313` | 不能穿過 … 儲存                                                                                       |
| `E314` | 沒有名為 `…` 的 spec                                                                                  |
| `E315` | `…` 由 … 參數化,而這個 `impl` 給了 … 個型別引數                                                       |
| `E316` | `…` 擴充 `…`,而這個程式裡沒有任何宣告用這個名字宣告 spec                                              |
| `E317` | `….…` 不符合 `…` 的要求:…                                                                             |
| `E318` | `…` 沒有實作 `…`,而 `…` 要求它                                                                        |
| `E319` | 整數字面量 `…` 裝不進 `int`                                                                           |
| `E320` | `str` 不可索引                                                                                        |
| `E321` | `if` 運算式只回答一個型別,而它的分支給了 … 與 …                                                       |
| `E322` | `match` 只回答一個型別,而它的 arm 給了 … 與 …                                                         |
| `E323` | … 借用 …,那是一個值而不是一個位置                                                                     |
| `E324` | … 寫回 `this`,而外圍方法以值持有它的接收者                                                            |
| `E325` | … 寫回 `…`,而它不是 `mut`                                                                             |
| `E326` | `…` 在同一次呼叫裡被交給 `…` 的兩個 `mut &` 參數                                                      |
| `E327` | `…` 接受 …,而這裡給了 …                                                                               |
| `E328` | `…` 需要 …,而這裡給了 …                                                                               |
| `E329` | 這個 list 字面量的第 … 個元素是 …,而這裡給了 …                                                        |
| `E330` | `…` 不是一個 … 裝得下的值                                                                             |
| `E331` | 這裡除以常數 `0`                                                                                      |
| `E332` | 這個運算式的值超過 `int` 裝得下的範圍,所以無法拿去對 … 衡量                                           |
| `E333` | 這個函式的答案是 …,而這裡給了 …                                                                       |
| `E335` | 不能把 … 綁到 … 的繫結:`…`                                                                            |
| `E336` | 繫結 `…` 給的是 …,它本身沒有型別                                                                      |
| `E337` | `type … = …` 沒有指到任何型別                                                                         |
| `E338` | struct 欄位或 enum payload 是 …,而這裡給了 …                                                          |
| `E339` | 不能把 … 賦給 …,它裝的是 …                                                                            |
| `E340` | `…` 的第 … 個引數是 …,而這裡給了 …                                                                    |
| `E341` | optional 不是 `…` 的運算元                                                                            |
| `E342` | 運算子 `…` 在 … 與 … 上沒有意義                                                                       |
| `E343` | 運算子 `…` 取 bool 運算元,而這兩個是 … 與 …                                                           |
| `E344` | 運算子 `…` 取 int 運算元,而這兩個是 … 與 …                                                            |
| `E345` | 運算子 `…` 取數值運算元,而這兩個是 … 與 …                                                             |
| `E346` | 運算子 `…` 排序兩個數或兩個 str,而這兩個是 … 與 …                                                     |
| `E347` | 不能拿 variant 和數字比較——variant 是它那個 enum 的值                                                 |
| `E348` | 不能比較 … 與 …——它們是不同種類的值                                                                   |
| `E349` | 運算子 `…` 在 … 上沒有意義                                                                            |
| `E350` | 運算子 `not` 取一個 bool 運算元,而這個是 …                                                            |
| `E351` | 運算子 `-` 取一個數值運算元,而這個是 …                                                                |
| `E352` | 運算子 `~` 取一個 int 運算元,而這個是 …                                                               |
| `E353` | 運算子 `…` 一邊是 …、另一邊是 …,而運算子的兩個運算元必須已經是同一個型別                              |
| `E354` | … 的條件是一個 optional,而條件是 bool——用 `if v := x { … }` 把它綁起來                                |
| `E355` | … 的條件必須是 bool,而 Zerg 沒有 truthiness                                                           |
| `E356` | `…` 重新繫結了一個 `const`                                                                            |
| `E357` | `const …` 遮蔽了這裡已經看得見的繫結                                                                  |
| `E358` | 最上層繫結 `…` 不能在 module 層的群組之外標 `mut`                                                     |
| `E359` | `….…()` 把值渲染成文字,所以除了被呼叫的那個值之外不取任何引數                                         |
| `E360` | `….…()` 把值渲染成文字,所以它是一般的 `fn`、不是 `mut fn`                                             |
| `E361` | `….…()` 回答這個值顯示成的 `str`                                                                      |
| `E362` | `…` 被宣告了兩次,其中一次是 generic                                                                   |
| `E363` | `…` 同時被宣告為 generic 與一般函式                                                                   |
| `E364` | `This` 是自身型別,而 … 在 `impl` 之外                                                                 |
| `E365` | `…` 宣告了兩個名為 `…` 的參數                                                                         |
| `E366` | `…(…)` 轉換一個值                                                                                     |
| `E367` | `…(…)` 不解析 `str`                                                                                   |
| `E369` | `…` 裝的是 …,而 … 不可呼叫                                                                            |
| `E370` | `…` 需要 … 的值(…):只有 `T?` 欄位有隱含預設值,而它是 `nil`                                            |
| `E371` | `this` 是方法的接收者,而這個函式沒有                                                                  |
| `E372` | 未定義的名字 `…`                                                                                      |
| `E374` | slice 的界是 int,而這個是 …                                                                           |
| `E375` | list 的索引是 int,而這個是 …                                                                          |
| `E376` | … 上沒有欄位 `…`                                                                                      |
| `E377` | `.…` 讀一個 tuple 元素,而 … 不是 tuple                                                                |
| `E378` | … 個元素的 tuple 沒有 `.…`                                                                            |
| `E379` | `for … in` 走訪 list、map、str、range 或 channel,而 … 不可迭代                                        |
| `E380` | raise 帶一個 `Err`,或一則用來建它的訊息                                                               |
| `E381` | `…` 被宣告了兩次,一次是一種宣告、一次是另一種                                                         |
| `E382` | `…` 被宣告了兩次、而且是同一種——每個 module 都攤平進同一個命名空間                                    |
| `E383` | variant 透過 enum 指名,而這個是裸的                                                                   |
| `E384` | `Either` 的一邊透過型別指名,而這個是裸的                                                              |
| `E385` | closure 參數沒有型別,而它的位置也沒給它一個                                                           |
| `E386` | 透過 function value 的呼叫給錯了引數個數                                                              |
| `E387` | `…` 宣告在 module 層級的 `unsafe { … }` group 裡,而這裡是安全程式碼                                   |
| `E388` | module `…` 沒有 `…` 這個成員                                                                          |
| `E389` | 這個名字已經被別的東西佔住了 — `import` 綁進唯一的 value 命名空間                                     |
| `E390` | 這個位置要一個值,而給它的是 nil                                                                       |
| `E391` | `…` 在頂層開了一個 statement,而編譯出來的程式沒有地方跑它                                             |
| `E392` | 不能對 `…` 做 `…`:只有 `mut` 的 collection 能改動它的元素                                             |
| `E393` | 不能 `…` `…`:collection 在自己的 `for` 迴圈裡對結構性改動是凍結的                                     |
| `E394` | `float` 上的 `…(…)` —— 寫出動詞:`math.trunc` / `floor` / `ceil` / `round`                             |
| `E395` | 一次轉換只有一步:`…` -> `…` 是 `…` -> `int` -> `…`,所以要寫成兩步                                     |
| `E396` | `…` 不是編譯器 primitive —— `__zrt_…` 這個集合是封閉的                                                |
| `E397` | 編譯器 primitive `…` 收 …,而這裡給了 …                                                                |
| `E398` | 編譯器 primitive `…` 的第 … 個運算元是 …,而這裡給了 …                                                 |
| `E401` | `break` / `continue` 在它所屬的迴圈之外                                                               |
| `E402` | `raise … from` 的 cause 不是 `Err`                                                                    |
| `E403` | 跳出 `guard` block —— **[not yet]**                                                                   |
| `E404` | optional 的 channel——`nil` 會同時是值與結束                                                           |
| `E405` | `…(…)` names one side of an `Either`, which holds exactly one value                                   |
| `E406` | `?.` reads through an optional, and … is not one                                                      |
| `E407` | `int(v)` 讀判別值,而 enum `…` 帶 payload,所以它的 tag 是不透明的                                      |
| `E408` | `?` early-returns the RIGHT of …, so the enclosing function must answer a carrier with the same right |
| `E409` | 泛型 METHOD `….…[…]`——**[not yet]**                                                                   |
| `E410` | `…` has been instantiated … times and is still asking for more                                        |
| `E411` | the type parameter `…` of `…` is not decided by this call                                             |
| `E412` | `…` does not implement `…`, which `…`'s type parameter `…` is bounded by                              |
| `E413` | raw pointer 內建 `…`——**[not yet]**                                                                   |
| `E414` | 編譯期內建 `…[T]`——**[not yet]**                                                                      |
| `E415` | 對內建型別 `…` 做 `impl`——**[not yet]**                                                               |
| `E416` | 把 `spec` `…` 當成型別使用（…）——**[not yet]**                                                        |
| `E417` | `str(…)` over a list bridges bytes or code points, and this is …                                      |
| `E418` | `…(…)` converts a value, and … may not have one                                                       |
| `E419` | an enum converts to `int`                                                                             |
| `E420` | `….of(n)` 反推判別值,而 enum `…` 帶 payload,所以它的 tag 是不透明的                                   |
| `E421` | `[…]` indexes a value, and … may not have one                                                         |
| `E422` | `…` 會 MUTATE 它的 list，而 `…` 是一個值、不是一個位置——**[not yet]**                                 |
| `E423` | 開放式 range 在這裡沒有上界——**[not yet]**                                                            |
| `E424` | `….…(…)` 是 associated function——**[not yet]**                                                        |
| `E425` | undefined function `…`                                                                                |
| `E426` | `…` has … fields and this gives …                                                                     |
| `E427` | variant pattern `…` cannot match a subject of type …                                                  |
| `E428` | non-exhaustive match                                                                                  |
| `E430` | `…` 在 … 上需要一個 `Eq`——預設沒有結構相等                                                            |
| `E431` | 型別為 … 的 map key——**[not yet]**                                                                    |
| `E432` | `…` is declared … and the value is …                                                                  |
| `E433` | `print` needs a value, and … may not have one                                                         |
| `E434` | 對 … 做 `if … := …`——**[not yet]**                                                                    |
| `E435` | `…` is declared to answer …, and its body falls off the end                                           |
| `E436` | `#[derive(…)]`——**[not yet]**                                                                         |
| `E437` | cannot derive `…`                                                                                     |
| `E438` | 對 `…` 做 `#[derive(Eq)]`——**[not yet]**                                                              |
| `E444` | list 方法 `…`——**[not yet]**                                                                          |
| `E445` | 容器上的結構相等 —— **[not yet]**                                                                     |
| `E446` | refcount 盒子 `Ref(x)` / `deref(r)`——**[not yet]**                                                    |
| `E449` | 把 … 算繪成文字——**[not yet]**                                                                        |
| `E451` | `…` 宣告了兩次 `…`                                                                                    |
| `E452` | `…` 落在一組以值宣告的循環裡                                                                          |
| `E453` | `…` 宣告了兩個名為 `…` 的 …                                                                           |
| `E454` | 這個運算式串接超過 … 層                                                                               |
| `E455` | `…(…)` 轉換一個純量,而 … 不是                                                                         |
| `E456` | `…` 不是 `…` 的 variant                                                                               |
| `E457` | `…` 是 `…` 的 variant,不是 `…` 的                                                                     |
| `E458` | 這個 catch-all arm 讓後面的 arm 到不了                                                                |
| `E459` | `…(…)` 說的是一個值落在 `Either` 的哪一邊,所以需要一個被宣告的 `Either`                               |
| `E460` | … 是身分而不是值,語言沒有給它相等                                                                     |
| `E461` | 同一個型別上的第二個 `impl Into[…]` —— **[not yet]**                                                  |
| `E462` | 對元素沒有 `==` 的 list 做 `in` —— **[not yet]**                                                      |
| `E463` | 對 list、map、range 或 error kind 以外的東西做 `in` —— **[not yet]**                                  |
| `E464` | `into` 是 `Into` spec 的方法,而沒有任何內建型別實作它                                                 |
| `E465` | `…` 屬於固定寬度階梯 —— **[not yet]**                                                                 |
| `E466` | 內建的 `set` —— **[not yet]**                                                                         |
| `E467` | 非窮盡的 match:少一個 catch-all `_` arm                                                               |
| `E468` | 在宣告要回答 … 的函式裡,`return` 沒有帶值                                                             |
| `E469` | … 是 `mut &`,而函式**值**不能帶著它 —— **[not yet]**                                                  |
| `E470` | 對 CHANNEL 做 `del …` —— **[not yet]**                                                                |
| `E471` | `…[…](…)` 當作建構子 —— **[not yet]**                                                                 |
| `E472` | `nil` 當作 `match` pattern —— **[not yet]**                                                           |
| `E473` | … 可能不裝任何值,所以 `…` 沒有東西可比                                                                |
| `E474` | `….…` 的 discriminant 不是編譯期常數                                                                  |
| `E475` | fill count 是編譯期常數,而 … 不是                                                                     |
| `E476` | fill count 是要複製幾份,而 `…` 是負數                                                                 |
| `E477` | range arm 的 bound 是編譯期常數,而 `…` 不是                                                           |
| `E478` | `…` 需要一個 channel,而 … 不是                                                                        |
| `E479` | map 項目是 `key: value`,而這個沒有 `:`                                                                |
| `E480` | …的值沒有這個編譯器能命名的型別 — **[not yet]**                                                       |
| `E481` | `…` 重新綁定 `match` arm 的 pattern 已經綁住的名字 — **[not yet]**                                    |
| `E482` | `…` 的欄位 `…` 是 module-private,所以必須帶預設值                                                     |
| `E483` | 欄位 `…` 的預設值讀了欄位 `…`——**[not yet]**                                                          |
| `E484` | 可變全域 `…` 不可以是 `pub`                                                                           |
| `E485` | import 循環：`…` -> `…` -> `…`                                                                        |
| `E486` | 解構賦值 `(a, b) = …` — **[not yet]**                                                                 |
| `E487` | `…` 只能用在後面的 `struct`、`enum` 或 `spec` 上,而後面是 `…`                                         |
| `E488` | `unsafe fn(…)` 型別 — **[not yet]**                                                                   |
| `E489` | 在 `….…` 上的 `impl` — 帶點的目標 — **[not yet]**                                                     |
| `E490` | 一個 `impl` 的 spec 是以裸的 `type-name` 指名的,而 `….…` 是透過 import 取得的                         |
| `E491` | 泛型的 `type …[…] = …` — **[not yet]**                                                                |
| `E492` | variant payload 裡的子 pattern — **[not yet]**                                                        |
| `E493` | 把 range 當成值使用 — **[not yet]**                                                                   |
| `E494` | `is …` 指名了一個內建的 error kind — **[not yet]**                                                    |
| `E495` | 一個 decorator 至少要有一個項目,而 `#[]` 沒有指名任何可套用的東西                                     |
| `E496` | decorator `#[sealed]` — 保留 — **[not yet]**                                                          |
| `E497` | 一個 `#[derive]` 要指名它要產生的 spec                                                                |
| `E498` | channel 是雙向、receive-only 或 send-only                                                             |
| `E501` | 這個進入點檔案沒有宣告 `fn main`                                                                      |
| `E502` | 在任何 source root 下都無法解析 import `…`                                                            |
| `E503` | 不能在 send-only 的 `…` 上接收                                                                        |
| `E504` | 不能在 receive-only 的 `…` 上送出                                                                     |
| `E505` | 不能關閉 receive-only 的 channel `…`                                                                  |
| `E506` | channel 方向只能收窄：`…` 不能填進 `…`                                                                |
| `E507` | `…` 是這次建置編進來、而本 module 沒有 import 的 module                                               |
| `E508` | `…` 不是 module `…` 的公開型別                                                                        |
| `E509` | `…` 是 module-private,而 … 在一個 `pub` 宣告上                                                        |
| `E510` | `…` 不是 `…` 的公開欄位,而 `…` 是 module `…` 宣告的                                                   |
| `E511` | module `atomic` 有出貨卻無法 import — **[not yet]**                                                   |
| `E512` | `…` 指名一個測試檔,而一般建置一個都不編                                                               |
| `E601` | `…` 需要一個名字,而 `…` 不是                                                                          |
| `E602` | 前綴 `<-` 是 channel 方向:只有 `<-chan[T]` 是型別                                                     |
| `E603` | `impl` 裡宣告前的 `mut` 標記的是 `mut fn` 方法,而這個不是 `fn`                                        |
| `E604` | `is` 的右邊要的是型別名字                                                                             |
| `E605` | `…` 是 statement,而這裡要的是 expression — **[not yet]**                                              |
| `E606` | `…` 不是這個編譯器讀得懂的 expression — **[not yet]**                                                 |
| `E607` | match arm 的 body 是 expression,而這個是 statement — **[not yet]**                                    |
| `E608` | f-string 的字面文字格式不良                                                                           |
| `E609` | f-string 的洞裡裝了不只一個 expression                                                                |
| `E610` | `…` 不能命名 struct/enum/spec/type alias — 宣告出來的型別名稱以大寫字母開頭                           |
| `E611` | `…` 是 prelude 名稱 — … — 不能用來命名…                                                               |
| `E612` | `…` 綁的是它後面那個 …,而 statement 不是                                                              |
| `E613` | 一個項目上出現第二個 decorator — 併進它的逗號列表                                                     |
| `E614` | `#[allow]` 要點名它壓下的 lint 代碼,而這個一個都沒點                                                  |
| `E701` | `…` 收的是 … 或 …,而這個裸值兩邊都不是                                                                |
| `E702` | … 上沒有欄位 `…`(optional chain `?.…`)                                                                |
| `E703` | 在 … 上用 `?` — 它拆的是 carrier 的 Left — **[not yet]**                                              |
| `E704` | `?` 往外傳的 right,外層函式回答不了                                                                   |
| `E705` | 兩個模組都定義了 `…`,而且至少一個是 `pub` — **[not yet]**                                             |
| `E706` | `…` 和 `…` 都定義了 `…` — 全部攤平成一個命名空間 — **[not yet]**                                      |
| `E707` | 沒有名為 `…` 的型別(…)                                                                                |
| `E708` | 在 … 上用 `!` — 它強拆 Result[T] 或 T? — **[not yet]**                                                |
| `E709` | 在 … 上用 `??` — 它的左邊要是 Result[T] 或 T? — **[not yet]**                                         |
| `E710` | `is …` 的左邊要一個 Err,拿到的是 …                                                                    |
| `E711` | `in …` 的左邊要一個 Err,拿到的是 …                                                                    |
| `E712` | `list[…](…)` 把 `str` 轉成它的 bytes,而這個是 …                                                       |
| `E713` | `list[…](…)` — `str` 只橋接到它的 bytes 或它的 code point                                             |
| `E714` | 把 … 印成文字 — enum 沒有變體的名字可用 — **[not yet]**                                               |
| `E715` | `[…]` 索引的是 list 或 map,而這個是 …                                                                 |
| `E716` | Err 上的方法 `…` 不收參數 — **[not yet]**                                                             |
| `E717` | Err 上的方法 `…` — `Error` 介面只有三個名字 — **[not yet]**                                           |
| `E718` | `ok_or` 要一個用來回答缺席的錯誤,而這裡一個也沒給 — **[not yet]**                                     |
| `E719` | `ok_or` 只收一個用來回答缺席的錯誤 — **[not yet]**                                                    |
| `E720` | `ok_or` 用 `Err` 回答缺席,而這個是 … — **[not yet]**                                                  |
| `E721` | `ok` 忘掉 Right,而且不收參數 — **[not yet]**                                                          |
| `E722` | … 上的方法 `…` — carrier 只回答 `ok_or` 與 `ok` — **[not yet]**                                       |
| `E723` | `….…(…)` — enum 型別只回答 `of(n)` 與它自己的變體 — **[not yet]**                                     |
| `E724` | `….of(n)` 收一個整數 — 要反查的判別值                                                                 |
| `E725` | … 上的方法 `…` — **[not yet]**                                                                        |
| `E726` | `…` 可以被測試,但不能被建構                                                                           |
| `E727` | 沒有名為 `…` 的型別可以建構                                                                           |
| `E728` | 沒有名為 `…` 的變體 — 建構子 pattern 指名的是主體的變體                                               |
| `E729` | match 不窮盡:少了 Left 或 Right 的情況                                                                |
| `E730` | match 不窮盡:少了 `true` 或 `false` 的情況                                                            |
| `E731` | 在 Result[T] 上用 pattern `…` — 它只有 Left 與 Right — **[not yet]**                                  |
| `E732` | 這些常數互相依賴,沒有一個能先取得值                                                                   |
| `E733` | `fn main() -> …` — 進入點只回答無、int 或 Result[nil] — **[not yet]**                                 |
| `E734` | 在用到並行的程式裡寫 main(args) — **[not yet]**                                                       |
| `E735` | closure 捕捉了 `…`,而這個編譯器算不出它裝了什麼                                                       |
| `E736` | 對函式、方法、帶命名空間的函式以外的東西做 … — **[not yet]**                                          |
| `E737` | 對 `…` 做 … — 它不是函式、方法或帶命名空間的函式 — **[not yet]**                                      |
| `E738` | `len` 不收參數,而這裡給了 …                                                                           |
| `E739` | `has` 問的是一個 key,而這裡給了 …                                                                     |
| `E740` | map 的方法 `…` — **[not yet]**                                                                        |
| `E741` | Err 上的欄位 `…` — 它只有 `msg` 與 `kind` — **[not yet]**                                             |
| `E742` | `…` 有 … 個型別參數,而這裡給了 …                                                                      |
| `E743` | 沒有上界的 `..=` 不是 range — **[not yet]**                                                           |
| `E744` | 對 `…` 做 `spawn`/`defer`,但它是一個「持有」函式的繫結 — **[not yet]**                                |
| `E745` | `…` 在這個檔案裡被宣告了兩次 — 一個 scope 只宣告一次同名的東西                                        |

它們在檔案被**讀進來**的當下就報告，早於掃描它的 import——掃描 import 會 parse，而一個拿到
讀不懂的文字的 parser，只能說出不真實的話。它以前說的正是這種話：`` `b'b` is not an
expression this compiler reads ``，指錯層級、指錯問題，而且印出來的是那個人寫的東西的碎片。

`E108` 以前根本沒有訊息。`0x` 被降到 C 的 `0x`，cc 把它讀成零，於是一個格式不良的字面量
編譯成功、程式回答 0。它說**緊接著**，是因為第一位數字屬於前綴自己的產生式：`0x_1F` 在 `0x`
後面確實有數字，卻仍然不是一個數——分組用的 `_` 位在兩位數字之間，而它左邊一位也沒有。

`E274` 也曾在其中，現已**退場**。它報告的是 pattern 位置上的裸名字——「`Zzz` 是某個 enum 的
variant，而 pattern 要透過 enum 指名」——判準卻是名字的第一個字母，而那個 parser 什麼都還沒
解析、也不知道有哪些 enum。於是它會對一個根本沒宣告 enum 的程式開火，句子裡指名的那個 enum
並不存在。pattern 位置上的裸名字**永遠**是一個新的 binding（[Grammar](../surface/grammar.zh-TW.md)），
不看大小寫；而這條規則原本想擋的錯——兩個 variant 沒帶 enum 寫出來——改由 `E458` 回答：第一個
arm 綁住了全部，它下面的 arm 都到不了。這個號碼不再重用。

### 已退場的代碼

**退場的號碼永不重用。** 代碼是一個穩定的身分，重用一個號碼會讓舊版建置印出的訊息指向它從未
指過的東西——使用者回報、日誌、去年開的 bug，全都被悄悄改派給另一條規則。所以退場的代碼會離開
上面那張表、改列在這裡，而它所在的區段繼續從自己的最高水位往上數。

這也是這份目錄可以**被查詢**的原因。`make error-codes-check` 會報出**每個區段的下一個可用
代碼**，那正是任何要新增規則的人手上的問題——包括兩個平行工作的 agent，牠們一週之內就在
`E387`、`E477` 與 `E288`/`E289` 上撞了三次，因為當時唯一的問法是用眼睛掃過整張表。這個答案
只在水位以下每個號碼都有交代時才靠得住，所以閘門就是這樣要求每個區段的：既不在上表、也不在
這裡的號碼是一個**缺口**，而缺口就是一個別人可能在不知情下重新發出的代碼。

| 代碼   | 為什麼退場                                                          |
| ------ | ------------------------------------------------------------------- |
| `E209` | 沒有型別的 closure 參數——這個形式已經建好，拒絕也就跟著走了         |
| `E216` | struct 欄位的預設值——已建好                                         |
| `E220` | 巢狀的 `{ … }` block 當作 statement——已建好                         |
| `E228` | 代碼轉換時被蓋了兩次，從沒有任何 case 走到的那個位置移除            |
| `E229` | 代碼轉換時被蓋了兩次，從沒有任何 case 走到的那個位置移除            |
| `E237` | `with` block——已建好，並由一個 example 接替了那條拒絕               |
| `E274` | pattern 位置上的裸名字，用第一個字母判斷——見上；改由 `E458` 回答    |
| `E334` | 區域繫結的標註沒有指到型別——同一條規則現在管四個位置，那條是 `E707` |
| `E368` | `…` 不是泛型——報告它的那個分支離開了，代碼也跟著離開                |
| `E373` | 同一個名字既是模組常數又是函式——這條規則是 `E381` 的                |
| `E429` | closure 捕捉某個名字——已建好                                        |
| `E439` | 代碼轉換時被蓋了兩次，從沒有任何 case 走到的那個位置移除            |
| `E440` | 代碼轉換時被蓋了兩次，從沒有任何 case 走到的那個位置移除            |
| `E441` | 代碼轉換時被蓋了兩次，從沒有任何 case 走到的那個位置移除            |
| `E442` | 代碼轉換時被蓋了兩次，從沒有任何 case 走到的那個位置移除            |
| `E443` | 代碼轉換時被蓋了兩次，從沒有任何 case 走到的那個位置移除            |
| `E447` | 從未發出：為 `E4xx` 編號的那次轉換跳過了它                          |
| `E448` | 來自 `Ord` 的排序——它指的那條規則是 checker 的，而且一直都是        |
| `E450` | 型別上沒有 `…` 這個欄位——與它下面保留號碼的那一列是同一條規則       |
| `E299` | 從未發出：`E2xx` 收在 `E298`，剖析器的號碼接在 `E6xx`               |
| `E499` | 從未發出：`E4xx` 收在 `E498`，產出器的號碼接在 `E7xx`               |
