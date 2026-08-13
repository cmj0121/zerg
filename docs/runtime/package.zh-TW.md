# Zerg Module、Package 與 Program

Zerg 原始碼如何組織、建置與啟動。本文建立在 [語言參考](../language.zh-TW.md) 的可見性、記憶體、spec 與錯誤模型之上。
亦有 [English](package.md) 版本。

## 四層

原始碼分成四個巢狀的角色——從整個 program 一路往下到單一 file——各自只管一件事：

| 層          | 是什麼                            | 劃分的邊界                                 |
| ----------- | --------------------------------- | ------------------------------------------ |
| **program** | 以含 `main` 的 entry 檔為根的建置 | 一次執行——依賴圖的根                       |
| **package** | 一棵 module 樹                    | **散布 / 相依 / 版本** 與**對外 API** 單位 |
| **module**  | 一個目錄                          | 預設的**私有**與**命名空間**單位           |
| **file**    | 一個 module 的實體切片            | 無——同 module 的檔案共享一個命名空間       |

把封裝／命名（`module`）與散布／API（`package`）分到兩層，正是讓 `pub` 有精確意義的原因。

> **[not yet]** **package 這一層在本工具鏈中不存在**。沒有 manifest、沒有版本宣告、沒有解析器、也沒有相依下載：
> 一次建置就是一個 entry 檔，加上它的 import 在磁碟上碰得到的那些 module。下文凡是提到 package 的部分——版本、
> package DAG、單一版本選擇、以位置定義的 package-public、以及倚賴圖無環的 orphan rule——描述的都是還沒有任何
> 實作的一層。`import "name"` 在磁碟上找不到對應物時是一個硬性的建置錯誤、不是靜默——_E502 cannot resolve
> import `name` under any source root_——而且早在它被 lex 之前就報出來。
>
> **[deviation]** **module 這一層有建，但不是表中所說的私有單位**：每個 module 都被壓平進同一個命名空間，
> 而可見性只檢查了 module 所持有的一部分、不是全部——函式與 module 常數有檢查(_E301 `helper` is not a public
> member of module `lib`_,且帶位置),struct 的欄位沒有。見下方「可見性」。
>
> **[not yet]** 兩個 module 宣告同一個**公開**的 top-level 名字會被具名拒絕。**私有**的則不會:module 之外
> 碰不到它的私有名字,所以裸呼叫一定指的是呼叫端自己那一個,兩者只需要在 C 裡分得開——各自拿到一個 module
> tag,也就是它在「程式的 module 名字排序後」的位置。用排序而不是首見順序,是因為那個名字每次跑都必須一樣。
>
> 公開的那種沒有地方可以唯一。這一頁刻意拒絕全域註冊表(見下),所以公開撞名是**編譯錯誤加上 link-name
> 覆寫**——那正是 [FFI](ffi.zh-TW.md) 已經寫下的做法,而它要等 package 層存在。

### Program 與 entry point

**program 是一次建置**，不是某種特殊的 package。你把 compiler 指向一個 **entry 檔**——`zerg build --emit bin entry.zg`——它就以
這個檔為根展開建置，沿著它的 import 走遍整張依賴 DAG。

- entry 檔名**不是保留字**；由建置指令指定。語言要求的是**內容**：entry 檔必須定義一個頂層 **`main`** entry
  function——它的形貌（輸入與結果）重用已定義的模型，就在下方。
- `main` 沒標 `pub`，所以永遠不可被 import——「program 不可被依賴」這個性質免費就有了，不需要特殊的 _binary package_
  種類。**每個 package 都是可被 import 的 library。**
- **多個執行檔**只是多個 entry 檔，各自有自己的 `main`，各自把 compiler 指過去建置。
- 讓 `main` 保持薄薄的接線層是**慣例**、不是規則——你想重用或測試的邏輯本來就必須放進可 import 的 package，這自然
  把 `main` 清空。

`main` 的形狀複用了既已定義的模型：

- **輸入。** 命令列參數——程式自己的介面——以**參數**進入。天生唯讀的 OS 環境事實（環境變數、時鐘、亂數）透過
  stdlib 取得、不進簽名；它們不是可變的全域狀態。
- **結果。** `main` 回傳 `Result[nil]`，所以退出複用錯誤模型：`Left` 以 `0` 退出，`Right(err)` 把 `err` 以
  `Kind: message` 印到 stderr 並以 `1` 退出，未被攔截的 **abort** 則 unwind main stack、在 stderr 留下自己的
  一行後同樣以 `1` 退出。退出能分辨的是成功與失敗——狀態 `0` 對上狀態 `1` 加一則訊息——僅止於此：從 `main`
  回傳的 `Right` 走的是 abort 所用的同一個 root handler，所以兩者是同一個狀態、同一行 stderr，`Kind:` 前綴也
  分不出它們（runtime fault 與被 force 的 `Err` 用同一個形狀回報，`raise "msg"` 則是一行裸訊息）。`?` 可以
  直接用在 `main` 裡。

### Program 生命週期與頂層初始化

`main` 的 body 是**整個 program 的根 scope**：它一回傳，底下所有 scope-owned 的東西就會被釋放，任何還在跑的 coroutine
就地被拋棄（沒有 join——要是某個 coroutine 必須先跑完，就用 channel 觀察到它完成、再讓 main 退；見
[Coroutines 與 Channels](../code/coroutine.zh-TW.md)）。

`main` 之外只住著**不可變的頂層狀態**——常數、函式、型別與 spec——在 `main` 執行前備妥。

這句話講的是**什麼東西在哪裡跑**，所以它同時決定了一個 grammar 允許、而本節從來沒說過的形式：**寫在頂層的
statement**。`GRAMMAR#program` 推導得出它——`program ::= stmt-list` 就是 Zerg 的 **script mode**，而 grammar 正是
用 `nop` 程式為這個語言開場——所以它是合法語法，編譯器會把它整句讀完。但**編譯出來**的程式沒有任何一刻可以跑它：
執行從 `main` 開始，上面的一切都是在那之前備妥的狀態。因此它會被**具名拒絕、並帶位置**，而且是由 build 而不是由
parse 拒絕——跟一個沒有 `fn main` 的程式走同一條分界（[Conformance](../conformance.zh-TW.md)）。`nop` 是唯一的例
外，而且其實不算例外：它什麼都不做、也不產出值，所以「什麼都不跑」就是跑完了它。

頂層常數以**依賴序**
初始化——一個常數在任何讀它的常數之前就緒——即 reads-from 圖的拓撲序；它們之間要是形成循環，就是 compile error。
當該圖使兩個常數彼此無序（互不讀取）時，平手以**決定性**方式打破：先依**canonical module 名稱**、再依 module 內的
**原始碼順序**。這整套排序——拓撲序加上「module 名稱再原始碼順序」的 tie-break——成立。

這兩件事都已經實作。初始化式讀到一個宣告在它**後面**的常數時,拿到的是那個值、不是零——`const A: int = B + 1`
寫在 `const B: int = 10` 上面,得到 `A == 11`——而循環是一個具名拒絕:
_these constants depend on each other and none can be given a value first_。

一個 module 也可定義 **`init()`** 函式（**可多個**）——它**惰性**的一次性 setup。它們**恰好跑一次**，在該 module
**首次被使用時**（其後的使用略過；並行的首次使用仍只跑一次），module 內依**宣告（FIFO）順序**、跨 module 依**相依
序**（module 的 imports 先 init），在它任何自己的程式碼之前、也在 `main` 之前。每個 `init()` **恰好一次、依 FIFO
順序、在 `main` 之前**執行。`init()` 承載多步或有副作用的啟動（開資源、註冊、seed），而不是把它
藏進 constant 的 initializer，並備妥該 module 的 immutable 狀態。仍**沒有可變全域**：共享的可變狀態以值傳遞或走
channel，絕不透過 module 層級的變數——頂層 binding 在 module 層級 `unsafe { … }` 分組外不得為 `mut`，而在分組
**裡面**的那個是 **module-private** 的，永遠不是 `pub`（見可見性）。

若某個 `init()` **abort**,該 abort 從觸發它的**首次使用點**往外傳——可在那裡用 `guard` 接住,否則就像任何未接的
abort 一樣 crash 那條 stack(主 stack 結束程式、coroutine 只結束自己)。該 module 於是**中毒(poisoned)**:`init()`
**不重跑**(恰好一次即使失敗也成立,所以副作用不重複),而其後每次使用都**以同一個快取的錯誤再度 abort**。一個
半初始化的 module 永不會變成可用,並行的首次使用也全都看到那同一個失敗。

> **[deviation]** 初始化是**及早的，不是惰性的**。程式中每一個 `init()` 都在 `main` 的第一個敘述之前執行，而不是
> 在擁有它的那個 module 首次被使用時。「恰好一次」成立，順序也成立：一個 module 的 imports 先備妥，然後才輪到它
> 自己，而它自己的各個區塊依 FIFO 執行。不成立的是「首次被使用時」——一個執行從未碰到的 module，它的 `init()`
> 照樣會跑。
>
> **[not yet]** **中毒（poisoning）。** abort 的 `init()` 在主 stack 上直接結束程式；沒有快取的錯誤、沒有後續使用
> 時的再度 abort，也沒有可供 `guard` 的首次使用點——因為那個呼叫根本不在使用點上。

### Package

**package** 是一棵 module 樹，也是**散布、相依與版本**的單位——你發佈、依賴、釘版本的那個東西。package 形成一張
**依賴 DAG**（directed acyclic graph，有向無環圖——相依永不繞回）：package 之間的循環會被拒絕。

一次建置在整張圖裡對每個 package **只選一個版本**——同一個 package 絕不會在一個 program 裡出現兩種版本——因此一個
package 的型別在全程式裡保有單一身分。

> **[not yet]** 全部——見「四層」下的標記。沒有 package，就沒有版本、package 之間沒有圖，也沒有東西可選。

### Coherence 與 orphan rule

一個 `spec` 的實作是**全域唯一**的：整個 program 裡，任何一組 `(型別, spec)` 只有一個正規實作。這由 **orphan rule**
在地強制——一個實作必須住在**定義該型別的 package**、或**定義該 spec 的 package**。

這仍讓你能**替 import 進來的型別加上新行為**：定義你自己的 spec、為那個外來型別實作它——spec 是你擁有的，orphan
rule 就滿足了。給別人的型別加一項能力是**一等、日常的動作**，不是變通。這條規則唯一擋掉的組合是**外來型別 × 外來
spec**——替別人的型別實作別人的 spec、兩者你都不擁有；這種較罕見的情況，就用你自己擁有的 **newtype**（單欄位
struct：建構把它包起來，要拿回內裡走寫出來的 accessor——沒有東西會自己 cast）把型別包一層，在包裝上實作該 spec。

coherence **不需要全域註冊表**——orphan rule 加上**無環**的 package 圖就保證了它。要 author 一組 `(型別, spec)` 的
實作，一個 package 必須能同時指名兩者；而因為依賴圖是 DAG，兩個擁有者 package 中至多只有一個能依賴（因而指名）
另一個，任何第三方 package 也無法在不擁有其一的情況下同時指名兩者。所以該實作要是存在，就由構造保證唯一。單一版本
選擇正是讓「一型別、一實作」有明確定義的前提。

orphan rule 有被強制,而且是以 module、而不是以上文推理所依據的 package 為單位:第三個 module 寫 `impl Spec
for T`、spec 與型別都不擁有時,會被 _E277 `impl Speak for Dog` is in neither's module — a spec and a type
belong to whoever declared them, and an impl belongs with one of the two_ 拒絕,且帶位置。同一次建置中兩個
`impl` 給同一型別同一個方法名也會被拒絕,那是壓平命名空間自己看得到的那件較窄的事。

### Module

**一個 module 就是一個目錄**；裡面的檔案是共享同一命名空間的實體切片——檔案數量是排版、不是語意。module 是預設的
私有單位：一個未加標記的宣告在該 module 的各檔案間可見，但不越出 module（見可見性）。

import path 的解析**先看 importer 旁邊**——也就是寫下該 `import` 的那個檔案所在的目錄——接著才是 entry 檔旁邊，
再來是標準函式庫，先命中者勝。因此一個 module 可以把自己的相依帶著走：`api/` 底下可以放它所 import 的 `api/util/`，
整組搬到別處時兩者一起搬。（seed 編譯器只搜尋 entry 檔的目錄，會拒絕以這種方式抵達的 module。）

巢狀是**扁平的**：把一個目錄放在另一個底下，只是讓 import path 變長——**沒有階層式私有**，內層 module 對外層並無
特殊存取權。**module 之間的 import 循環會被拒絕**——一個在還走在下去的路上就又出現的 module，它的 `init()`
區塊與 module 常數沒有任何順序可以被備妥，而那個拒絕指名的是這個環、不是走到它的那段路。被兩個 module 各自
import 的同一個 module 不是環；一個 module import **它自己**，則是同一條規則的單節點情形。

所以相互遞迴的型別與函式住在**同一個 module**——而這不痛，因為 module 是共享命名空間的多檔案目錄：一個 `ast`
module 可以把 `Expr`、`Stmt` 分放在不同檔案、彼此**免 import** 互相引用，編譯器 forward-declare、auto-boxing 讓遞迴有
有限 layout，與自我參照型別完全相同。當兩個分屬**不同關注點**的型別互相回指，這條禁令是個推力——用 **id 引用**打破
循環（通常是更好的設計），而不是把它們併一起（package 圖是 DAG 也是同一道理：互相依賴的 package 必須合為一個）。

無論佈局如何，唯一必須無環的是**頂層常數初始化**（見 Program 生命週期與頂層初始化）：那裡的循環沒有合法順序、是
compile error。一個型別指名另一個型別**從來不是**這種循環——只有初始器會遞迴地依賴自身值的常數才是。

> **[deviation]** **entry 檔自己的目錄不是一個 module**。與 entry 檔並列的檔案不在它的命名空間裡，也不會被編進這次
> 建置：指名該檔宣告的函式會得到 `undefined function`。「各檔案共享一個命名空間」在每個被 `import` 觸及的 module
> 都成立；以 entry 檔為根的那個 module 是例外。
>
> **[deviation]** **單一檔案** import 得起來。`import "sib"` 在旁邊有一個 `sib.zg` 時,會解析到那一個檔案與它的
> `pub` 名字,即使這裡的 module 是目錄、而 `E502` 自己的句子也這麼說——_a module is a directory of `.zg` files
> beside the importer or in the standard library_。於是 import 路徑多了第二種未載於文件的形狀,而那則本該教會讀者
> 第一種的診斷,否認第二種存在。

### 可見性——如何把宣告公開

每個宣告都從 **module-private** 起步。`pub` 是唯一的可見性標記，而且只有一個意思——**露給這個 package 的其餘部分**
——它本身**永遠**到不了世界。所以有三個範圍、卻只有一個關鍵字：

- **module-private**（預設）——只在定義它的 module 內可見（跨其各檔案，file 不劃邊界）。
- **package-internal**——一個 `pub` 宣告；同 package 的其他 module 可指名它。
- **package-public**——不是標記、而是一個**位置**：一個 package 的對外 API，就等於它 **root module**（package
  module 樹的頂層目錄）的 `pub` 表面。宣告唯有出現在那裡，才能被依賴者看到。

要把內層型別露給依賴者，就由 root module **re-export** 它——把一個 package-internal 的名字、或整個 module，拉上
root 自己的公開表面。re-export 是建立 package 公開表面的唯一機制：root 不點名，任何東西都出不了 package，因此重整內層
module 永不擾動對外契約。宣告不能比它所指名的型別更外露——一個 package-public 的函式不能收受或回傳一個
**本身不在公開表面上**的型別（無論是 module-private、還是 package-internal 但從未被 re-export），因為依賴者
根本無法指名那個型別。一個型別的 **`pub` method 會隨它一起走**：一旦型別抵達公開表面，它的 `pub` method 也能
被依賴者呼叫——method 上的可見性讀法與 function 完全相同。

> **[deviation]** **函式與 module 常數已強制，其餘尚未。** 指名另一個 module 的 module-private **函式**是編譯錯誤，
> 而且帶位置——裸呼叫與具名空間的 `lib.helper()` 兩種形狀都擋——讀取它的 module-private **常數**也一樣，
> 同樣兩種形狀：裸名的 `FLOOR` 與具名空間的 `lib.FLOOR`。module-private 的**型別**與**欄位**目前仍可跨界讀取：一個
> `pub fn` 可以回傳私有 struct，相依端也讀得到它的私有欄位，沒有任何 finding。每個 module 仍壓平進同一個命名空間
> ——這也是兩個 module 宣告同名會相撞的原因，而那個拒絕針對的是名字、不是可見性。規則比較的是**宣告被讀進來的
> 目錄**與**進行讀取的目錄**，也就是 module 邊界而非 package 邊界；上文的 **package-internal** 與
> **package-public** 仍然需要先有 package 才談得上。

唯一連 `pub` 都不能寫的宣告是**可變全域**——module 層級 `unsafe { … }` 分組裡的 `mut` binding，文法本身就把它定成
module-private（`GRAMMAR` group 12）。一個分組是某個 module 與它自己作者之間的協議，`pub` 會把那份協議開放給每一個
import 它的人；它在宣告處就被拒絕，並帶位置。要對外開放，就寫一個讀它的 `pub fn`。

### 匯入與引用

引用另一個宣告一律**顯式**——無 wildcard、無遞迴傳遞（import 一個 package 只給你它的公開表面，絕不給你它自己
import 的那些 package），也沒有 ambient import。每個名字要嘛是宣告的、import 的，要嘛是 toolchain **built-in**——
primitive 關鍵字與 prelude（見 Prelude 與 std）。要 import 什麼，取決於距離：

- **同一 module**（同目錄）——無須 import；一個 module 的各檔案共享一個命名空間。
- **同 package 的其他 module**——import 那個 sibling module，即可指名它的 package-internal（`pub`）宣告。其中一個
  具名函式是**一等值**、不只是呼叫目標:`f := other.helper` 綁定它、`f(x)` 呼叫它(見 [函式與閉包](../code/functions.zh-TW.md))。
- **別的 package**——import 該 package，只看得到它 root 的公開表面；相依套件的內層 module **不可達**，依賴者只拿得到
  那個公開表面。

因為每個相依都被寫下來，import graph 是顯式的——這正是 module 與 package **循環得以被拒絕**的前提。

> **狀態。** 這些小節描述的表面今日已接好：**字串路徑 import**（`import "util/text"`）、**括號 import
> 群組**（`import ( … )`）、**`as` 改名**（`import "a/text" as at`，綁的是整個命名空間——它的函式與它的 module
> 常數一樣可達），以及**一層 `import pub` re-export** 到 root module 的公開表面。（re-export 只有一層：
> `import pub` 把所命名的 module 露到本 module 表面；它不會遞迴地 re-export 那個 module 自己 re-export 的東西。）
>
> import 所引入的**綁定**已被強制：受詞前綴必須是這次建置裡某個 `import` 真的綁過的命名空間，所以憑空捏造的
> `bogus.f()`、或把路徑片段當成名字用（`import "util/text"` 之下寫 `util.f()`）都會被當成未定義的名字拒絕，並附
> 位置；兩個綁定相撞的 import，以及被某個頂層宣告先佔走的綁定，同樣被拒絕，而 `as` 就是這兩者的解法。
>
> **[deviation]** **綁定是整個建置的，成員也是。** 同一個壓平的兩半，而且兩半都被程式看得見：
>
> - **「無遞迴傳遞」未被強制。** 在場的命名空間是這次建置裡每個 module 綁過的每一個命名空間——包含別的 module 的
>   `as` 別名——所以本 module 從未 import 的 module，它仍然指名得到。import graph 決定的是什麼被**編進**建置，
>   不是建置內部什麼名字叫得出來。
> - **成員是在全程式範圍查的。** 前綴解出來之後，`.` 之後的名字是在那唯一一個壓平的命名空間裡找的，而不是在前綴
>   所指的那個 module 裡找：於是同時 import 了 `a` 與 `b` 時，`b.helper()` 回答的是 module `a` 宣告的 `helper`。
>   `pub` 仍然是對**宣告**該成員的 module 檢查的——這也是為什麼一個私有成員被拒絕時，訊息指名的是它真正的擁有者，
>   而不是程式寫下的那個 module。seed 編譯器這一半是對的，它回報 `module "b" has no public member "helper"`。
>
> **[not yet]** 跨 module 的函式只是**呼叫目標**：`other.helper(x)` 可用，而 `f := other.helper` 會回報該 module 沒有
> 這個成員——本節承諾的一等值到 module 邊界就停住了。

### Prelude 與 std（The prelude & std）

**prelude 不是被 import 的**——它的名字是 **built into the toolchain**，從一開始就綁在每個 module 裡，正如 primitive
關鍵字。它裝的是語言本身倚賴的東西：運算子 desugar 的目標型別（`Either`、`Result`、`T?`、`nil`）、built-in spec
（`Eq`、`Ord`、`Hash`、`Error`、`Iterator`／`Iterable`、`Ref`，以及運算子 spec——見 [Spec 與 Generics](../core/specs.zh-TW.md)；
**沒有 `Object` spec**——相等與排序是經 `derive(Eq)` / `derive(Ord)` opt-in，而 `display` / `debug` 是內建的值渲染、
非 spec method，見 [格式化](format.zh-TW.md)），
外加少數泛用型別——`list`、`map`、`set` 容器（見 [Collection](../code/collections.zh-TW.md)）與 `Ref[T]` 資源盒。
（primitives——`bool`、`int`、`str`……——與 `chan`、以及 `defer`／`print` 構造同樣是 grammar 與 runtime，不是被
import 的名字。）這些名字是
**保留字**：宣告不得 shadow 或重宣告它們，所以那些 desugar 到它們的
運算子永遠不會被從語言底下抽走。

其餘一切都是**標準函式庫**——一個普通 package，只有一點不同：**std 隨 toolchain 出貨**，所以它的版本就是編譯器的
版本、你從不把它列為相依。它像一般 package 一樣顯式 import：`io`、`math`、更多 collection，以及讀取唯讀 OS 狀態的
ambient-OS 函式（`env`、時鐘、亂數）。

因為 prelude 是 built-in、而不是隱式 import，「無 ambient import」就毫無例外地成立。

> **[not yet]** prelude 承諾的 built-in spec 中，只有 **`Eq`** 與 **`Into[T]`** 存在。`Ord`、`Hash`、`Error`、
> `Iterator` / `Iterable`、`Ref` 與運算子 spec 都沒有宣告，所以 `impl Ord for P` 會回報程式中沒有任何東西以那個名字
> 宣告過 spec。`set` 與 `Ref[T]` 同樣不存在——現有的容器就是 `list` 與 `map`。
>
> **[deviation]** prelude 的名字**沒有被保留**。程式可以宣告 `struct list`、`struct Result`、`struct Ref` 或
> `spec Eq`，沒有一個會被拒絕——所以那些運算子 desugar 的目標，確實可以被從語言底下抽走。

### 測試與可見性（Testing & visibility）

測試就是普通程式碼、套同一套可見性規則——隱私**沒有測試專用後門**。這決定了測試該放哪：

- **白箱**——要驗一個 module 的 `module-private` 內部，就把測試放進**該 module 的一個檔案**；共享命名空間，它直接
  看得到內部，免 import、無特殊存取。
- **黑箱**——要驗某個 API，就 import 它：sibling module 看得到 package-internal（`pub`）表面，而一個依賴此 package
  的獨立 package 看到的正是 package-public 表面——真正的外部視角。

測試檔由 **build 工具依慣例**辨識（例如 `*_test.zg` 檔名），只在 test build 納入、normal build 一律排除。因此測試的
宣告永遠到不了 shipped artifact 或 package 的公開表面——即使測試檔放在 root module、即使標了 `pub`，也留在對外 API
之外。一如 entry 檔，語言本身不賦予檔名任何意義，是工具賦予的。

> **[not yet]** **這個慣例沒有任何一部分被辨識。** 沒有 `zerg test` 指令,而 `*_test.zg` 也沒有被排除在任何
> 東西之外:它跟 module 裡其他每個檔案一樣被編進一般建置,所以它的宣告**確實**進得了出貨產物,`pub` 的那些
> **確實**加入了 module 的表面——`lib.only_in_test()` 在一支從未要求測試的程式裡解析得到也跑得動。它重複的
> 名字會與同 module 的兄弟撞名,語法壞掉的那一個會讓一般建置失敗。所以上面的白箱／黑箱位置目前只是「檔案該放
> 哪」、不是「怎麼跑起來」,而那個檔案在等待期間並不是惰性的。在那之前,`testing` 模組的 `assert` 系列可以從
> 一般程式呼叫。

### Target 條件式檔案

平台與架構差異用**同一套方式**處理——在**檔案層級、由 build 工具依慣例**——而不是語言內的 `#ifdef` / `cfg` 構造
（那會讓程式碎裂、違背 `small and crisp`）。一個 module 保留**各 target 的檔案**（像 `_linux` / `_darwin` 的名稱後
綴，與 `_test` 慣例並列），build 只納入符合所選 target 的那些；語言本身維持 **target-agnostic**、不賦予檔名任何意義。
確切的 target 命名與匹配方案屬 build 工具細節，**延後**。

> **[not yet]** 沒有任何後綴被辨識。module 目錄裡每個 `.zg` 都會被編進建置，所以同時放著 `plat_linux.zg` 與
> `plat_darwin.zg` 的 module 就是同一個名字宣告了兩次，會以碰撞被拒——這比挑錯一個要清楚，但仍然不是這個功能。
