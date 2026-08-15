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

**module 指名得到的每一種宣告都已強制。** 指名另一個 module 的 module-private **函式**是編譯錯誤，
而且帶位置——裸呼叫與具名空間的 `lib.helper()` 兩種形狀都擋——讀取它的 module-private **常數**也一樣，
同樣兩種形狀：裸名的 `FLOOR` 與具名空間的 `lib.FLOOR`。module-private 的**型別**以同樣方式被拒絕，不論寫成裸名
（`s: Secret`、`Secret(…)` 建構）或透過命名空間（`lib.Secret`）；module-private 的**欄位**在讀與寫兩邊都被拒絕，
那正是「私有欄位必須帶預設值」那條規則的另一半。而上文那句規範本身也是一條規則：**一個 `pub` 宣告指名了不是
`pub` 的型別**會在**宣告處**被拒絕，所以 `pub fn` 不能回傳、不能收下、也不能用 `pub` 欄位持有一個相依端根本
拼不出來的型別。

兩處都成立時，兩個 finding 都會回報——外洩的匯出，以及伸手進去的讀取——因為那是兩個檔案裡的兩個錯誤，一則訊息
指不到兩個地方。匯出是有辦法修的那一方：寫下 `pub` 的 module 決定把型別交出去，也只有它能把型別標成 `pub` 或
不再回傳它；讀取私有欄位的相依端兩件事都做不到。

每個 module 仍壓平進同一個命名空間——這也是兩個 module 宣告同名會相撞的原因，而那個拒絕針對的是名字、不是
可見性。

> **[deviation]** 規則比較的是**宣告被讀進來的目錄**與**進行讀取的目錄**，也就是 **module** 邊界而非 package
> 邊界。所以就編譯器而言，上文的 **package-internal** 與 **package-public** 仍是同一層：一個 `pub` 宣告，凡是
> import 它的 module 都指名得到，沒有任何東西把它收窄到某個 package 的 root 表面——因為還沒有 package 這個單位
> 可以拿來量。re-export（`import pub`）建得出一個表面；但還沒有任何規則要求一個名字必須在某個表面上。

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
> **「無遞迴傳遞」已被強制。** 一個綁定屬於**寫下該 `import` 的那個檔案所屬的 module**——粒度是 module 而不是
> 檔案，因為同一個 module 的檔案共用一個命名空間——所以一個 module 叫得出來的命名空間，就是它自己的檔案綁過的
> 那些，包含它自己的 `as` 別名，不含別的 module 的。指名一個本 module 從未 import 的 module 是帶位置的編譯錯誤，
> 而且在每一個寫得出 module 名的位置都擋：呼叫、成員讀取、`spawn` / `defer` 的 callee、建構、variant 讀取，以及
> **型別**位置——註記 `c: lib.Counter` 與簽章 `fn take(c: lib.Counter)` 一樣擋。import graph 決定的既是什麼被編進
> 建置、也是建置內部什麼名字叫得出來；而這兩個答案還要跟第三種分開：一個憑空的前綴（`bogus.f()`）在任何地方都
> 不指名任何東西，它仍然是**未定義的名字**——叫讀者去為一個根本不存在的 module 補一行 import，會比原本那個洞
> 還糟。
>
> > **[deviation]** **型別位置會把解不掉的 qualifier 丟掉。** `c: bogus.Counter` 建得起來，而且讀作 `Counter`：
> > 一個帶命名空間的型別名是取它的最後一段來解析的——也就是本章記載的那個壓平——而未知的 qualifier 在那裡被丟棄，
> > 不是被回報。所以上面那個三分的答案只有在運算式位置才完整；型別位置分得出「本 module 沒有 import 它」與
> > 「這是一個真的命名空間」，但這兩者都還分不出打錯字。
>
> **[deviation]** **成員是在全程式範圍查的。** 前綴解出來之後，`.` 之後的名字是在那唯一一個壓平的命名空間裡找的，
> 而不是在前綴所指的那個 module 裡找：於是同時 import 了 `a` 與 `b` 時，`b.helper()` 回答的是 module `a` 宣告的
> `helper`。`pub` 仍然是對**宣告**該成員的 module 檢查的——這也是為什麼一個私有成員被拒絕時，訊息指名的是它真正
> 的擁有者，而不是程式寫下的那個 module。seed 編譯器這一半是對的，它回報
> `module "b" has no public member "helper"`。
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
> **[deviation]** 被保留的是**工具鏈真正綁定的名字**，比本頁所述的 prelude 窄。`struct list`、`fn int`、
> `enum Left` 與 `spec Eq` 都在宣告處被拒絕——_E611 `list` is a prelude name — a built-in container type —
> and cannot name a struct_，並附位置——`map`、`bytearray`、`runearray`、`Either`、`Result`、`Err`、`Right`
> 與 `Into` 亦然。同一段承諾、但**這裡沒有任何東西宣告**的那些名字——`Ord`、`Hash`、`Error`、`Iterator`、
> `Iterable`、`Ref` 與 `set`——沒有被保留：程式自己的 `spec Ord` 就是唯一的 `Ord`，拒絕它等於為一個不存在的
> 功能佔住名字。每一個在被綁定的那天才加入這個集合。
>
> **函式那個位置取的集合比型別位置窄**，而 `map` 就是全部的差別：`fn map(xs, f)` 合法，集合裡其他名字都不合法。
> 型別宣告的名字落在所有這些名字被綁定的那個命名空間裡；函式的名字則落在只有**呼叫叫得出來的**那些名字所在之處
> ——而 `map[…](…)` 當建構式在兩個 compiler 裡都沒有建，所以這個名字沒有可被取走的值形式。其餘的都有：callee
> 寫成 `int`、`byte`、`bytearray` 或 `list` 會被讀成轉換，寫成 `Either`、`Result`、`Err`、`Eq`、`Into`、`Left`
> 或 `Right` 會被讀成建構，兩者都發生在去找使用者符號之前。
>
> 有兩個位置在規則之外，而且不算偏離。**方法**名字屬於它的型別、不屬於程式，所以 `impl P { fn set(v: int) }`
> 合法。**binding**——區域的，或 module 常數，在 parser 裡是同一個形式——仍然可以取 prelude 名字：在一個 scope
> 內遮蔽它，第一次使用就是一個大聲的錯誤，而那正是宣告所不是的。

### 測試與可見性（Testing & visibility）

測試就是普通程式碼、套同一套可見性規則——隱私**沒有測試專用後門**。這決定了測試該放哪：

- **白箱**——要驗一個 module 的 `module-private` 內部，就把測試放進**該 module 的一個檔案**；共享命名空間，它直接
  看得到內部，免 import、無特殊存取。
- **黑箱**——要驗某個 API，就 import 它：sibling module 看得到 package-internal（`pub`）表面，而一個依賴此 package
  的獨立 package 看到的正是 package-public 表面——真正的外部視角。

白箱擺法在目錄的**兩種形狀**下都成立，因為 test build 解析一個測試檔屬於哪個 package，用的就是解析 import 的規則：
**先具體、後一般**。`module_at` 先回答單一的 `<name>.zg` 檔、再回答目錄，所以一個 `.zg` 檔放在鄰居旁邊，它本身就是
一個 module——`src/stdlib` 正是這樣，一個扁平目錄裡十七個彼此獨立的 module。因此放在那裡的 `strings_test.zg` 就是
`strings.zg` 一個檔案的測試，package 就是這一對；而一個沒有同名鄰居可指的測試檔，仍然屬於**目錄**，一如既往。

代價是：在一個**本身就是單一 module** 的目錄裡，名字對上該 module 某個檔案的 `*_test.zg` 只會拿到那個檔案——`a.zg`
與 `b.zg` 同目錄時，`a_test.zg` 看不到 `b.zg`。這是一個大聲的失敗（在用到該名字那一行報未宣告），不會是安靜的；解法
是把測試檔以 module 為名，而不是以它其中一個檔案為名。換來的是這條規則**單調**：新增一個測試檔，永遠不會改變另一個
package 是用哪些檔案建起來的。

測試檔由 **build 工具依慣例**辨識（例如 `*_test.zg` 檔名），只在 test build 納入、normal build 一律排除。因此測試的
宣告永遠到不了 shipped artifact 或 package 的公開表面——即使測試檔放在 root module、即使標了 `pub`，也留在對外 API
之外。一如 entry 檔，語言本身不賦予檔名任何意義，是工具賦予的。

> **[not yet]** `zerg test` 目前是一個**骨架**。它會走訪一條路徑找出 `*_test.zg`,把每個 package 編譯
> ——它自己的原始碼、它的測試檔,加上一個產生出來的 driver,所以白箱測試不需要 import 也不需要 `pub`
> 就摸得到 module 的內部——然後把找到的每個 `#[test]` 跑起來,以檔案分組回報
> `ok` / `FAIL` / `SKIP` / `STUCK` / `CRASH`,skip 與 timeout 都跟 pass、fail 分開計數,只要有一個
> 沒過就以非零狀態結束。什麼都沒找到的一次執行會把這件事說出來。
>
> **一個 package 就是測試檔所指名的那個 module**,以先具體後一般解析:`strings_test.zg` 旁邊有
> `strings.zg`,package 就是這一對(再加上該目錄自己的 `fixtures_test.zg`,如果有的話,以及 driver);
> 指不到這種鄰居的測試檔,則以目錄為 package。因此一個目錄可能同時有好幾個 package,各有自己的 driver
> 與自己的 process。
>
> **`--only <name>`** 只跑名字**以 `<name>` 開頭**的測試——寫完整名字就選一個,寫字首就選一整族。它在
> 任何東西被產生之前就先套用,所以沒被選上的測試不會被編譯,它的 fixture 也不會被建起來。一個什麼都沒
> 選到的 filter 以 `2` 結束,而不是回報一次「什麼都沒有」的綠燈。
>
> **`--timeout <seconds>`** 是單一測試在執行被放棄之前可以花的時間,預設 `60`。超過的測試回報 `STUCK`
> 並單獨計數,而且執行會繼續往後面的測試走:timeout 不是斷言失敗,因為根本沒有任何事情被判定。少了它,
> 卡住的測試和單純很慢的測試分不出來,而卡住那個會把整次執行一起帶走——直到 CI 自己的 timeout 砍掉這個
> job,而 log 裡沒有任何一行說得出是哪個測試幹的。
>
> **兩條路徑,而報告會說走的是哪一條。** 一個測試以 **coroutine** 的形式、包在 `guard` 裡、在同一個 process
> 中執行——這同時接住了「斷言不成立」與「未捕捉的 abort」,因為 `guard` 兩者都接得住,而沒有 `guard` 的
> coroutine 死掉時也只死自己。coroutine 接不住的是把 **process** 本身結束掉的測試:stack overflow,或
> `os.exit`。所以一次執行結束時仍然沒有結果的測試,會各自在**自己的 process** 裡重跑一次——正好是剩下那些,
> 因為結果只在本體返回之後才寫下——這才把「死掉」歸到造成它的那個測試身上。這件事發生時,報告會用一行
> `NOTE` 說出來;一次悄悄換了策略的執行,是一次沒有人能解讀結果的執行。
>
> 一個 `#[test]` 不回傳,參數則是**沒有**、**一個 `testing.Context`**(以型別辨識而非以參數名字辨識),或是它
> 需要的 **fixture**(以名字比對);driver 依簽章寫出對應的呼叫。Context **傳值**,而該共享的東西照樣共享——它唯一的欄位是一個 channel,而 channel 是
> `Ref` 值,複製即共享——上頭有 `ctx.name()`、`ctx.log(msg)`(只在測試失敗時顯示)、`ctx.skip(reason)` 與
> `ctx.fatal(msg)`。後兩者以 `raise` 解開、把理由留在 context 上,所以沒有任何一方需要比對訊息字串才能分辨
> skip 與 fail。一項主張是 `assert cond`,那個關鍵字(見 [Grammar](../surface/grammar.zh-TW.md) group 8),
> 它自己寫訊息、不需要 context 給任何東西;留給 `ctx.log` 的是一則**領域註記**——關於 fixture、而不是關於運算式
> 的事。`testing.assert_raises` 維持自由函式,因為它不是斷言:它問的是一個**已經結束**的呼叫 raise 了什麼。
>
> **Fixture。** 一個測試**宣告它需要什麼**,框架建置一次、交給它,再拆掉。`#[fixture]` 是一個把自己的測試當作
> **continuation** 收下的函式:
>
> ```zerg
> #[fixture]
> fn db(use: fn (Conn)) {
>     c := connect("postgres://tmp/test")
>     defer c.close()
>     use(c)
> }
>
> #[fixture]
> fn schema(db: Conn, use: fn (Schema)) {
>     s := make_schema(db)
>     defer drop_schema(s)
>     use(s)
> }
> ```
>
> `use: fn (T)` **以型別辨識**,而且一次是兩件事:測試執行所在的 continuation,以及這個 fixture **產出什麼**的宣告。
> 其餘每個參數都是**以名字比對**到另一個 fixture 的相依——測試對 fixture、fixture 對 fixture,都是同一條規則。
> teardown 就是 `defer`,語言自己的慣用法,不需要 runner 提供任何東西。
>
> 測試用同樣的方式解析自己的參數:`testing.Context` **以型別**,fixture **以名字**,沒有參數則直接呼叫。
>
> ```zerg
> #[test]
> fn test_insert(schema: Schema) { … }
>
> #[test]
> fn test_query(db: Conn, ctx: testing.Context) { … }
>
> #[test]
> fn test_pure() { … }
> ```
>
> 框架**以巢狀組合**——`db(fn (c) { … schema(c, fn (s) { … }) })`——所以相依順序、teardown 順序（內層的 `defer`
> 先觸發）與「只在需要時才建」,全都是「呼叫寫在哪裡」的結果。執行期沒有拓撲排序,也沒有 teardown 登記表;沒有任何
> 測試經過的那一層,根本不會被生成。
>
> **一切都在任何東西執行之前先解析完。** 指名不到 fixture 的參數、型別不是該 fixture 產出物的參數,以及 fixture
> 之間的**環**,都會**帶位置**、對整棵樹一次報完,然後這次執行以 `2` 結束,什麼都沒跑。
>
> **建不起來的 fixture 會讓每個需要它的測試 FAIL**——`FAIL test_query`,底下寫著
> _fixture `db` could not be built: …_,而整次執行以非零結束:跑不成的測試絕不可以看起來像通過的測試。若 raise
> 是在它底下每個測試都已經各自有交代之後才到,壞掉的是 **teardown**,這個失敗就記在 fixture 身上,而不是記在誰的
> 測試上。
>
> **fixture 住在哪裡,以及誰繼承它。** fixture 宣告在 `*_test.zg` 裡,所以到不了任何出貨建置,而它服務**自己這個
> 目錄**的測試。要連**底下**的目錄一起服務的,就寫進 **`fixtures_test.zg`**——祖先目錄唯一會往下傳的檔案,傳給它
> 底下的**每一層**,而不只是下一層。這是 pytest 的 `conftest.py` 模型,而固定的檔名承擔了兩件事:一般的
> `*_test.zg` 是它所屬模組的**一個檔案**,會讀該模組的私有名字,把它帶進底下的目錄等於放進一個那些名字不存在的
> scope;而祖先自己的測試否則會在每個後代目錄各跑一次。寫在 `fixtures_test.zg` 裡的 `#[test]` 只在它自己的目錄
> 跑一次,和其他測試一樣。祖先是從 `zerg test` **被給定的**那個路徑算起。
>
> 被繼承的檔案在這次執行期間會被**複製進該套件**,因為模組就是**目錄**——生成的 driver 寫在那裡也是同一個理由。
> 複製是逐位元組的,所以裡面的診斷仍指向正確的**行**,而它會隨執行結束一起被移除。因此套件無法**遮蔽**一個被繼承
> 的 fixture:同一個 scope 裡兩個同名宣告,就以它本來的樣子——衝突——被拒絕。
>
> **範圍是套件,不是 session。** `pkg/sub` 與 `pkg/sub2` 各自建置一份被繼承 fixture 的**自己的**實例,因為各自是
> 一個 driver、一個行程。這是 pytest 的 `scope="package"`。session 範圍是刻意不要的,而且本來也到不了:`E705`
> 會拒絕兩個模組同時定義一個 `pub` 名字,所以整棵樹一個 driver 蓋不出來,而活著的值也跨不過行程邊界。
>
> fixture 的值抵達測試的方式,和任何值抵達一次呼叫的方式一樣——**傳值**——而測試主體跑在 `spawn` 的另一側,所以
> 裡面的 `Ref`（channel、盒裝 handle）是共享的,其餘是複製的。測試是**序列**執行的;`ctx.parallel()` 是
> **[not yet]**,等它落地之後,共用同一個 fixture 的測試會共用它的同一個實例。
>
> **fallback 會重建它需要的東西。** 當一個測試把行程結束掉、剩下的以一個行程一個測試重跑時,那些行程只會進入
> 「它被要求的那個測試所在」的層——所以它會把那個測試的 fixture 重新立起來,而且不會多立別的。
>
> **產生出來的 driver、以及被繼承的 fixture 檔的副本,在被讀完之後就立刻從磁碟上消失**,早於這個建置能夠
> emit、編譯、連結或回報任何東西——所以一次失敗的執行,留下的原始碼目錄跟它進來時一模一樣。這件事最要緊的
> 地方也正是最看不見的地方:編譯器是用**列目錄**的方式解析標準函式庫的,一個被留在 `src/stdlib` 的 driver,
> 之後每一次建置都會讀到它。
>
> **還沒建**的是這之後的每一件事:doc comment(`##`)、把 doc example 當測試跑、benchmark,以及
> 同時跑兩個測試。失敗的斷言會 `raise`,而 raise 是控制流,所以它自己就會從測試本體解開出去。
>
> **斷言**那一側已經補上,而且它是一個**關鍵字**、不是函式庫:`assert cond` raise 出 `AssertionError`,帶著
> 檔案、行號、主張本身的原始文字,以及比較拆開後每個運算元當時的值。`testing.assert_raises` 交回一次 `guard`
> 包住的呼叫所 raise 的 `Err`,是唯一留下的輔助函式。仍然沒有的是**複合值**的渲染,所以兩個 list 的
> `assert xs == ys` 是 `E445`,關於 list 的主張只能透過某個把它縮成純量的東西去做。
>
> **排除**已經建好。一般建置一個 `*_test.zg` 都不編——檔名在讀取 module 目錄的地方比對,兩個編譯器都是——
> 所以測試宣告的任何東西都到不了出貨產物、也不會加入 module 的表面,而它重複的名字或一個根本不能 parse 的
> 檔案,對一般建置毫無代價。指名它的某個宣告會得到 _E388 module `lib` has no `only_in_test`_,指名那個檔案
> 則是 _E512 `lib/lib_test` names a test file, and a normal build compiles none_,兩者都帶位置。E388 不會
> 進一步說「有個測試檔宣告了它」:那個事實屬於 loader,而回報的規則在 checker 裡。
>
> 測試檔從任何地方都 import 不得,包含另一個測試檔——白箱測試共享它所屬 module 的命名空間、完全不需要 import
> 就摸得到內部,這正是為什麼 `zerg test` 是「多編幾個檔案」的問題,而不是「放寬某條可見性規則」的問題。

### Target 條件式檔案

平台與架構差異用**同一套方式**處理——在**檔案層級、由 build 工具依慣例**——而不是語言內的 `#ifdef` / `cfg` 構造
（那會讓程式碎裂、違背 `small and crisp`）。一個 module 保留**各 target 的檔案**（像 `_linux` / `_darwin` 的名稱後
綴，與 `_test` 慣例並列），build 只納入符合所選 target 的那些；語言本身維持 **target-agnostic**、不賦予檔名任何意義。
確切的 target 命名與匹配方案屬 build 工具細節，**延後**。

> **[not yet]** 沒有任何後綴被辨識。module 目錄裡每個 `.zg` 都會被編進建置，所以同時放著 `plat_linux.zg` 與
> `plat_darwin.zg` 的 module 就是同一個名字宣告了兩次，會以碰撞被拒——這比挑錯一個要清楚，但仍然不是這個功能。
