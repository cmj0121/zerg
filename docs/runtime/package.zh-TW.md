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
> 實作的一層。`import "name"` 在磁碟上找不到對應物時是一個硬性的建置錯誤、不是靜默——_E5002 cannot resolve
> import `name` under any source root_——而且早在它被 lex 之前就報出來。
>
> **[deviation]** **module 這一層有建，但不是表中所說的私有單位**：每個 module 都被壓平進同一個命名空間，
> 而可見性只檢查了 module 所持有的一部分、不是全部——函式與 module 常數有檢查(_E3001 `helper` is not a public
> member of module `lib`_,且帶位置),struct 的欄位沒有。見下方「可見性」。
>
> **[not yet]** 兩個 module 宣告同一個**公開**的 top-level 名字會被具名拒絕。**私有**的則不會:module 之外
> 碰不到它的私有名字,所以裸呼叫一定指的是呼叫端自己那一個,兩者只需要在 C 裡分得開——各自拿到一個 module
> tag,也就是它在「程式的 module 名字排序後」的位置。用排序而不是首見順序,是因為那個名字每次跑都必須一樣。
>
> 公開的那種沒有地方可以唯一。這一頁刻意拒絕全域註冊表(見下),所以公開撞名需要的是**編譯錯誤加上 link-name
> 覆寫**——而那個覆寫在 [FFI](ffi.zh-TW.md) 裡是一個**待決問題**,不是任一章已經寫下的做法。無論如何它都要等
> package 層:在那之前,根本沒有一個「讓名字唯一」的單位存在。

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
parse 拒絕——跟一個沒有 `fn main` 的程式走同一條分界（[Conformance](../conformance.zh-TW.md)）——即 _E3087 `print`
opens a statement at the top level, and a compiled program has nowhere to run it_。`nop` 是唯一的例外，而且其實
不算例外：它什麼都不做、也不產出值，所以「什麼都不跑」就是跑完了它。

頂層常數以**依賴序**
初始化——一個常數在任何讀它的常數之前就緒——即 reads-from 圖的拓撲序；它們之間要是形成循環，就是 compile error。
當該圖使兩個常數彼此無序（互不讀取）時，平手以**決定性**方式打破：先依**canonical module 名稱**、再依 module 內的
**原始碼順序**。這整套排序成立。

對**直接**的讀取而言,這兩件事都已經實作。初始化式指名一個宣告在它**後面**的常數時,拿到的是那個值、不是零
——`const A: int = B + 1` 寫在 `const B: int = 10` 上面,得到 `A == 11`——而循環是一個具名拒絕:
_E4068 these constants depend on each other and none can be given a value first_。

> **[deviation]** reads-from 圖是由初始化式**寫出來**的名字建的,所以一個**穿過呼叫**的讀取不是一條邊。
> `const A: str = mk()` 寫在 `const B: str = "x"` 上面、而 `mk()` 讀 `B`,會讓 `A` 持有**零值**,而且完全沒有
> 任何診斷——實測,`A` 印出來是空的。這是這條排序規則唯一的 silent-wrong,也是 `src/stdlib/log.zg` 把自己
> 初始化式會碰到的常數宣告在**它們上面**、並由 `scripts/log-check.sh` 守住那個順序的原因:語言本身不守。

一個 module 也可定義 **`init()`** 函式（**可多個**）——它**惰性**的一次性 setup。它們**恰好跑一次**，在該 module
**首次被使用時**（其後的使用略過；並行的首次使用仍只跑一次），module 內依**宣告（FIFO）順序**、跨 module 依**相依
序**（module 的 imports 先 init），在它任何自己的程式碼之前、也在 `main` 之前。
`init()` 承載多步或有副作用的啟動（開資源、註冊、seed），而不是把它
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
for T`、spec 與型別都不擁有時,會被 _E2037 `impl Speak for Dog` is in neither's module — a spec and a type
belong to whoever declared them, and an impl belongs with one of the two_ 拒絕,且帶位置。同一次建置中兩個
`impl` 給同一型別同一個方法名也會被拒絕,那是壓平命名空間自己看得到的那件較窄的事。

### Module

**一個 module 就是一個目錄**；裡面的檔案是共享同一命名空間的實體切片——檔案數量是排版、不是語意。module 是預設的
私有單位：一個未加標記的宣告在該 module 的各檔案間可見，但不越出 module（見可見性）。

import path 是一個**前綴**加一個**套件路徑**（`GRAMMAR#import-path`）。套件路徑是由套件名組成的目錄路徑；
**前綴決定它在哪個根之下**展開，而**磁碟上有什麼，永遠不會改變它在哪個根之下**：

| 寫法                      | 根                                | 落在磁碟上                                     |
| ------------------------- | --------------------------------- | ---------------------------------------------- |
| `import "io"`             | 標準函式庫                        | `<stdlib>/io.zg` 或 `<stdlib>/io/`             |
| `import "net/http"`       | 標準函式庫                        | `<stdlib>/net/http.zg` 或 `<stdlib>/net/http/` |
| `import "./http"`         | 本專案，在 **entry 檔的目錄**之下 | `<entry>/http.zg` 或 `<entry>/http/`           |
| `import "github.com/a/b"` | 遠端套件                          | **[not yet]** —— 已保留，並具名拒收            |

三個根用同一條規則展開套件路徑，所以佈局規則只有一條、不是三條。分類器是**名字文法的推論**、不是它旁邊的另一條
規則：`package-name` 是 `[a-z][a-z0-9_]*`，不能含 `.`，所以第一段含點的東西不可能是套件名，只能是 host。

**讀一個 import 就知道它指的是三者中的哪一個。** 新增、刪除或改名一個檔案，只能改變一個 import 是否**解析得到**
——永遠不會改變它指的是哪一類東西。這就是為什麼一個叫 `io.zg` 的檔案，再也搶不走旁邊每個檔案的標準函式庫 `io`。

有兩種形狀是不合法、而不是解析不到，兩者的拒收都是關於那個字串。`..` 或開頭的 `/` 是往專案外搆。同一個路徑的
第二種拼法——`.//x`、`x/`、`x/./y`——是被拒收而不是被正規化，因為把兩種拼法正規化成同一個 module，會讓 module
的身分變成算出來的而不是寫下來的；大寫落在 `package-name` 之外也是鄰近的理由，好讓會摺疊大小寫的檔案系統無法
決定一支程式建不建得起來。module 的最後一段會被綁成識別字，所以它不能是保留字。

一個名字同時解析到 `<name>.zg` 與 `<name>/` 時，**那裡沒有 module**：這一對是被拒收而不是被排序，因為任何靜默的
優先序都是讀者總有一天要問的問題。

`./` 的根是 entry 檔的目錄、而不是寫下 import 的那個檔案，所以一個 module 不管寫在哪裡都只有一種拼法——
`import "./zerg"` 從 entry 檔、從往下兩層的目錄，指的都是同一個 module。代價是搬遷：一棵帶著自己所 import 的
module 的樹，搬走時它自己的 import 需要改。

巢狀是**扁平的**：把一個目錄放在另一個底下，只是讓 import path 變長——**沒有階層式私有**，內層 module 對外層並無
特殊存取權。**module 之間的 import 循環會被拒絕**——一個在還走在下去的路上就又出現的 module，它的 `init()`
區塊與 module 常數沒有任何順序可以被備妥，而那個拒絕指名的是這個環、不是走到它的那段路。被兩個 module 各自
import 的同一個 module 也不是環——它只會被載入一次。

一個檔案 import **它自己所屬的 module** 根本不是環，而是它自己的一條拒絕：_E5015 `./greet` is the module this
file is already part of_。它有完全正常的順序；那個 import 只是沒有意義，因為一個 module 的檔案共享同一個命名
空間，那些名字本來就在 scope 裡。環所帶的那句建議——把相互遞迴的宣告放進同一個 module——正是寫下這個 import
的人已經做到的事。

所以相互遞迴的型別與函式住在**同一個 module**——而這不痛，因為 module 是共享命名空間的多檔案目錄：一個 `ast`
module 可以把 `Expr`、`Stmt` 分放在不同檔案、彼此**免 import** 互相引用，編譯器 forward-declare、auto-boxing 讓遞迴有
有限 layout，與自我參照型別完全相同。當兩個分屬**不同關注點**的型別互相回指，這條禁令是個推力——用 **id 引用**打破
循環（通常是更好的設計），而不是把它們併一起（package 圖是 DAG 也是同一道理：互相依賴的 package 必須合為一個）。

無論佈局如何，唯一必須無環的是**頂層常數初始化**（見 Program 生命週期與頂層初始化）：那裡的循環沒有合法順序、是
compile error。一個型別指名另一個型別**從來不是**這種循環——只有初始器會遞迴地依賴自身值的常數才是。

> **[deviation]** **entry 檔自己的目錄不是一個 module**。與 entry 檔並列的檔案不在它的命名空間裡，也不會被編進這次
> 建置：指名該檔宣告的函式會得到 _E4016 undefined function `beside`_。「各檔案共享一個命名空間」在每個被 `import` 觸及的 module
> 都成立；以 entry 檔為根的那個 module 是例外——而 `import "./beside"` 就是抵達那個檔案的方式，跟本專案其他任何
> module 用的是同一種拼法。

**單一檔案就是一個 module**，凡是目錄能當 module 的位置它都能——`import "./sib"` 在旁邊有一個 `sib.zg` 時會
解析到那一個檔案與它的 `pub` 名字，而標準函式庫就是一個扁平目錄裡的十五個這種 module。它從前是 import 路徑
第二種未載於文件的形狀、可以用裸名抵達、因而能夠遮蔽；改成要具名索取，才讓它變得平常。

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

規則比較的是**這個 module 被哪一條 import path 抵達**——由 loader 回答、在解析出這個 module 的地方記下來，之後
按名字讀回。它不是從「檔案放在哪」算出來的：並排的 `./a` 與 `./b` 是兩個 module，標準函式庫就是一個扁平目錄裡
的十五個。

> **[deviation]** 它比較的邊界是 **module** 邊界而非 package 邊界。所以就編譯器而言，上文的
> **package-internal** 與 **package-public** 仍是同一層：一個 `pub` 宣告，凡是 import 它的 module 都指名得到，
> 沒有任何東西把它收窄到某個 package 的 root 表面——因為還沒有 package 這個單位可以拿來量。re-export
> （`import pub`）建得出一個表面；但還沒有任何規則要求一個名字必須在某個表面上。

唯一連 `pub` 都不能寫的宣告是**可變全域**——module 層級 `unsafe { … }` 分組裡的 `mut` binding，文法本身就把它定成
module-private（`GRAMMAR` group 12）。一個分組是某個 module 與它自己作者之間的協議，`pub` 會把那份協議開放給每一個
import 它的人。兩個代碼從兩側守住同一條規則：_E3056 the top-level binding `x` may not be `mut` outside a
module-level `unsafe { … }` group_，以及 _E4046 the mutable global `x` may not be `pub`_。要對外開放，就寫一個讀它
的 `pub fn`——`src/stdlib/log.zg` 是這棵樹的示範，也是出貨 stdlib 裡唯一這樣的分組。

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
> 這個三分的答案在**型別位置**同樣成立：`c: bogus.Counter` 得到的是 _undefined name `bogus`_，與運算式那一側
> 同一個發現，而不是一個被默默丟掉、只留下最後一段的 qualifier。
>
> 而且成員是**在前綴所指的那個 module 裡**查的。同時 import 了 `a` 與 `b` 時，`b.helper()` 是
> _E3084 module `b` has no `helper`_，不管 `a` 宣告得多大聲——函式、module 常數、當作建構子用的型別，以及型別
> 位置，四條路都問這一題。`pub` 是另一個問題，對**宣告**該成員的 module 檢查，這也是為什麼一個私有成員被拒絕
> 時，訊息指名的是它真正的擁有者，而不是程式寫下的那個 module。
>
> **[not yet]** 跨 module 的函式只是**呼叫目標**：`other.helper(x)` 可用，而 `f := other.helper` 會回報該 module 沒有
> 這個成員——本節承諾的一等值到 module 邊界就停住了。

### Prelude 與 std（The prelude & std）

**prelude 不是被 import 的**——它的名字是 **built into the toolchain**，從一開始就綁在每個 module 裡，正如 primitive
關鍵字。它裝的是語言本身倚賴的東西：運算子 desugar 的目標型別（`Either`、`Result`、`T?`、`nil`）、built-in spec
（`Eq`、`Ord`、`Hash`、`Error`、`Iterator`／`Iterable`、`Ref`，以及運算子 spec——見
[Spec 與 Generics](../core/specs.zh-TW.md)），外加少數泛用型別——`list`、`map`、`set` 容器
（見 [Collection](../code/collections.zh-TW.md)）與 `Ref[T]` 資源盒。`display` / `debug` 根本不在裡面：它們是內建的
值渲染、不是 spec method（見 [格式化](format.zh-TW.md)）。primitives——`bool`、`int`、`str`……——與 `chan`、以及
`defer`／`print` 構造同樣是 grammar 與 runtime，不是被 import 的名字。這些名字是**保留字**：宣告不得 shadow 或
重宣告它們，所以那些 desugar 到它們的運算子永遠不會被從語言底下抽走。

其餘一切都是**標準函式庫**——一個普通 package，只有一點不同：**std 隨 toolchain 出貨**，所以它的版本就是編譯器的
版本、你從不把它列為相依。它像一般 package 一樣顯式 import：`io`、`math`、更多 collection，以及讀取唯讀 OS 狀態的
ambient-OS 函式（`env`、時鐘、亂數）。

因為 prelude 是 built-in、而不是隱式 import，「無 ambient import」就毫無例外地成立。

> **[not yet]** prelude 承諾的 built-in spec 中，只有 **`Eq`** 與 **`Into[T]`** 存在。`Ord`、`Hash`、`Error`、
> `Iterator` / `Iterable`、`Ref` 與運算子 spec 都沒有宣告，所以 `impl Ord for P` 會回報程式中沒有任何東西以那個名字
> 宣告過 spec。`set` 與 `Ref[T]` 同樣不存在——現有的容器就是 `list` 與 `map`。
>
> **[deviation]** 被保留的是**工具鏈真正綁定的名字**，比本頁所述的 prelude 窄。`struct list`、`fn int`、
> `enum Left` 與 `spec Eq` 都在宣告處被拒絕——_E2061 `list` is a prelude name — a built-in container type —
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
一個 module——`src/stdlib` 正是這樣，一個扁平目錄裡十五個彼此獨立的 module。因此放在那裡的 `strings_test.zg` 就是
`strings.zg` 一個檔案的測試，package 就是這一對；而一個沒有同名鄰居可指的測試檔，仍然屬於**目錄**，一如既往。

代價是：在一個**本身就是單一 module** 的目錄裡，名字對上該 module 某個檔案的 `*_test.zg` 只會拿到那個檔案——`a.zg`
與 `b.zg` 同目錄時，`a_test.zg` 看不到 `b.zg`。這是一個大聲的失敗（在用到該名字那一行報未宣告），不會是安靜的；解法
是把測試檔以 module 為名，而不是以它其中一個檔案為名。換來的是這條規則**單調**：新增一個測試檔，永遠不會改變另一個
package 是用哪些檔案建起來的。

測試檔由 **build 工具依慣例**辨識（例如 `*_test.zg` 檔名），只在 test build 納入、normal build 一律排除——所以即使
測試檔放在 root module、即使標了 `pub`，也留在對外 API 之外（見下方「排除」）。一如 entry 檔，語言本身不賦予檔名
任何意義，是工具賦予的。

### `zerg test`

`zerg test <path>` 會走訪一條路徑找出底下的 package，把每個都編譯——它自己的原始碼、它的測試檔，加上一個產生出來
的 driver，所以白箱測試不需要 import 也不需要 `pub` 就摸得到 module 的內部——然後跑它找到的每一個 `#[test]`，以
`ok` / `FAIL` / `SKIP` / `STUCK` / `CRASH` 依檔案分組回報，skip 與 timeout 與 pass、fail 分開計數；只要有任何一項
不成立就以非零結束。

**package 就是測試檔所指名的那個 module**，依上面「先具體、後一般」那條規則，再加上該目錄自己的
`fixtures_test.zg`（若有）與 driver。因此同一個目錄可以放好幾個 package，各有自己的 driver、自己的行程。

**而一個「本身就是某個 module」的測試 package，就是那個 module。** `src/stdlib/strings_test.zg` 形成的 package
是用 `strings.zg` 建的，所以同一支程式裡任何其他地方的 `import "strings"`，解析到的是**這個 package 的原始碼**，
而不是把該 module 的檔案再載入一次。沒有這條規則，那個 module 會在程式裡出現兩次，建置會停在
_E4073 `get` is declared twice in this file_，指著一份沒有人動過的原始碼。沒有任何刻意的安排才到得了它：一份 suite
會 import `testing`，`testing` 透過 `log` 輸出一則 note，而 `log` 用 `json` 編碼——所以 `testing` 依賴閉包裡的那些
stdlib module，恰好就是有 suite 的那幾個。這與「一個 module 無論被幾個人 import 都只載入一次」是同一套機制，不是
額外栓在 loader 上的一條測試檔專屬規則。

**路徑可以是單一 `.zg` 檔**，此時跑的是**那個檔案所在的 package**——祖先從該檔案的目錄算起，與直接給目錄時一致。
單獨建置那個測試檔等於什麼都沒建，所以檔案是用來**選一個 package**，而不是給一份檔案清單；要挑單一測試，工具是
`--only`。

**結束碼分得出「搜了但什麼都沒找到」。** `0` 跑到的測試全部通過或跳過、`1` 有測試失敗或逾時、`2` 這個命令執行不了
（路徑不存在、`--only` 什麼都沒對到、fixture 參數解析不到東西）、`3` 搜尋跑完了而搜到的範圍裡沒有測試。`3` 沒有併進
`2`，因為讀者下一步不同：`2` 是改命令列、`3` 是去看那棵樹。搜不到東西時 stderr 也會說，但光一句話會讓 CI 那一行
永遠是綠的，而讀 CI 那一行的是一支 shell。

**`#[test]` 寫在哪裡就在哪裡被找到**，不限於 `*_test.zg`——所以一個目錄裡唯一的 `#[test]` 就算寫在普通 module 檔
裡，它也是一個測試 package，而 `zerg lint` 仍然會警告（**L601**）這樣的測試會**跟著出貨**
（見 [Decorator](../core/decorators.zh-TW.md)）。它不新增任何 package **形狀**——一個落單的 `#[test]` 所屬的
package，就是原本就會編到它所在檔案的那一個——所以上面那條單調規則不受影響。

裡面的那個主張也一樣會出貨，並得到第二個 finding：`*_test.zg` 之外的 `assert` 會拿到 **L602**。沒有任何旗標能把
它剝掉，所以寫在出貨程式碼裡的一個主張，可以讓一支正在執行的程式 abort——而它不是作者本來想要的那個檢查的較弱版本，
是**較不具體**的版本：它只說得出「這個主張是假的」。`raise ValueError("xs must be non-empty") if xs.len() == 0`
兩件事都說得出來。

**`#[test]` 不回傳任何東西**（見 [Decorator](../core/decorators.zh-TW.md)），而拒絕一個宣告出來的回傳型別發生在
編譯任何東西之前：`#[test] fn t() -> bool { return false }` 曾經被回報成 `ok`。它是被拒絕而不是被 lint，因為 lint
以 0 結束，而那一輪會繼續說 `ok`。

**`--only <name>`** 只跑名字**開頭是** `<name>` 的測試——完整名字選一個測試，字首選一整族。它在產生任何東西之前就
套用，所以沒被選到的測試不會被編、它的 fixture 也不會被建。一個什麼都沒選到的過濾器以 `2` 結束，而不是回報一輪
綠色的「什麼都沒有」。

**`--timeout <seconds>`** 是一個測試在被放棄等待前可以跑多久，預設 `60`。超過的測試回報 `STUCK` 並單獨計數，而這
一輪會繼續往下跑：逾時不是一個沒通過的斷言，因為什麼都還沒被決定。沒有它，卡住的測試與慢的測試無從分辨，而卡住的
那個會把整輪拖著走，直到 CI 自己的 timeout 砍掉這份工作、且沒有任何東西說得出是哪個測試幹的。

**兩條路徑，而報告會說是哪一條。** 一個測試在一個行程裡、以 **coroutine** 的身分、包在 `guard` 內執行——這能接住
一個不成立的斷言，也能接住一次沒被接住的 abort。coroutine 接不住的，是一個把**行程**結束掉的測試：stack overflow，
或 `os.exit`。所以在這一輪結束時仍然沒有結果的測試，會**各自用一個行程**重跑——剛好就是剩下的那些，因為結果只在函式
體回傳之後才寫下——這才把那次死亡歸給造成它的測試。發生時報告會在一行 `NOTE` 上說明；一輪安靜換了策略的執行，是
一輪沒有人能解讀其結果的執行。

#### 一個測試宣告它需要什麼

`#[test]` 的參數、以及 `#[fixture]` 的參數，都依 [Decorator](../core/decorators.zh-TW.md) 所述的規則解析——
`testing.Context` 依型別、fixture 依名字、`fn (T)` 這個 continuation 宣告 fixture 產出什麼。留給本章的是 runner
拿它們做了什麼。context 以**值**傳入，而真正重要的東西本來就共享：它唯一的欄位是一個 channel，而 channel 是 `Ref`
值，所以複本共享它；它帶了什麼在 [標準函式庫](stdlib.zh-TW.md)。一個主張是 `assert cond` 這個關鍵字
（[Grammar](../surface/grammar.zh-TW.md)，group 8），它自己寫出訊息，不需要 context 給它任何東西。teardown 是
`defer`，語言自己的慣用法，所以 runner 不必為它準備任何東西。

```text
#[fixture]
fn db(use: fn (Conn)) {
    c := connect("postgres://tmp/test")
    defer c.close()
    use(c)
}

#[test]
fn test_query(db: Conn, ctx: testing.Context) { … }
```

框架**以巢狀組合**——`db(fn (c) { … schema(c, fn (s) { … }) })`——所以相依順序、teardown 順序（內層的 `defer` 先
觸發）與「只在需要時才建」全都是「呼叫寫在哪裡」的結果。執行期沒有拓撲排序、沒有 teardown 登記簿，而沒有任何測試
會經過的那一層根本不會被產生出來。

**所有東西都在任何東西執行之前解析完。** 指不到任何 fixture 的參數、型別不是該 fixture 產出物的參數，以及 fixture
之間的**環**，都會帶**位置**回報，而且是整棵樹一次報完，然後這一輪以 `2` 結束、什麼都沒執行。

**一個建不起來的 fixture 會讓每一個需要它的測試失敗**——`FAIL test_query`，底下附 _fixture `db` could not be
built: …_，而這一輪以非零結束：一個根本沒能跑的測試，絕不可以看起來像通過了。如果那次 raise 是在它底下每個測試都
已經各自有了答案之後才到的，壞掉的就是 **teardown**，於是失敗記在那個 fixture 上，而不是記在任何人的測試上。

#### fixture 住在哪裡，以及誰繼承得到

一個 fixture 服務**它自己那個目錄**的測試。要一併服務**底下**目錄的，放進 **`fixtures_test.zg`**——祖先唯一能往下
貢獻的那個檔案，而且是往下貢獻給**每一層**、不只下一層。這就是 pytest 的 `conftest.py` 模型，而這個固定檔名有兩層
承重：一個普通的 `*_test.zg` 是它所屬 module 的檔案、會讀那個 module 的私有名字，把它帶進下面的目錄就是把它放進
那些名字不存在的 scope；另外，祖先自己的測試否則會在每個後代目錄各跑一次。祖先是從 `zerg test` **被給定的**那個
路徑算起。

被繼承的檔案在這一輪期間會被**複製進該 package**，因為 module 就是一個**目錄**——與那個產生出來的 driver 寫在那裡
是同一個理由。複製是逐位元組的，所以裡面的診斷會指到正確的行。因此一個 package 無法**遮蔽**一個繼承來的 fixture：
同一個 scope 裡一個名字兩份宣告，會被當作它本來就是的那種碰撞而拒絕。

**scope 是 package，不是 session。** `pkg/sub` 與 `pkg/sub2` 各自建一份繼承來的 fixture 的**自己的**實例，因為各是
一個 driver、一個行程——就是 pytest 的 `scope="package"`。session scope 是刻意不要的，而且本來也到不了：`E9081` 拒絕
兩個 module 都定義同一個 `pub` 名字，所以一個橫跨整棵樹的 driver 根本建不起來，而一個活的值也不會跨過行程邊界。
同理，當某個測試把行程結束掉、剩下的以一個行程一個測試重跑時，那些行程各自只進入它被指派的那個測試所在的那些層。

那個產生出來的 driver、以及繼承 fixture 檔案的複本，在**被讀取之後立刻**從磁碟上移除，早於建置能 emit、編譯、連結
或回報任何東西——所以一輪失敗的執行，留下的原始碼目錄與它來的時候一模一樣。這在最看不見的地方最重要：編譯器是靠
**列出** `src/stdlib` 來解析標準函式庫的，而留在那裡的一個 driver 就是之後每一次建置都會讀到的檔案。

#### 排除

一般建置一個 `*_test.zg` 都不編——檔名在讀取 module 目錄的地方比對，兩個編譯器都是——所以測試宣告的任何東西都到不了
出貨產物、也不會加入 module 的表面，而它重複的名字或一個根本不能 parse 的檔案，對一般建置毫無代價。指名它的某個宣告
會得到 _E3084 module `lib` has no `only_in_test`_，指名那個檔案則是 _E5011 `lib/lib_test` names a test file, and a
normal build compiles none_，兩者都帶位置。E3084 不會進一步說「有個測試檔宣告了它」：那個事實屬於 loader，而回報的
規則在 checker 裡。

測試檔從任何地方都 import 不得，包含另一個測試檔——白箱測試共享它所屬 module 的命名空間、完全不需要 import 就摸得到
內部，這正是為什麼 `zerg test` 是「多編幾個檔案」的問題，而不是「放寬某條可見性規則」的問題。

> **[not yet]** 上述之外還有四件事：doc comment（`##`）、把 doc 範例當測試跑、benchmark，以及**同時跑兩個測試**
> ——測試是循序的，`ctx.parallel()` 還沒建，等它落地時，共用同一個 fixture 的測試將共用同一份實例。
>
> 已經建好的部分裡唯一的缺口是對**複合型別**的主張：`assert xs == ys` 比較兩個 list 是 `E9057`，所以這類主張要透過
> 某個能把它化約成 scalar 的東西來寫（[Spec 與泛型](../core/specs.zh-TW.md)）。

### Target 條件式檔案

平台與架構差異用**同一套方式**處理——在**檔案層級、由 build 工具依慣例**——而不是語言內的 `#ifdef` / `cfg` 構造
（那會讓程式碎裂、違背 `small and crisp`）。一個 module 保留**各 target 的檔案**（像 `_linux` / `_darwin` 的名稱後
綴，與 `_test` 慣例並列），build 只納入符合所選 target 的那些；語言本身維持 **target-agnostic**、不賦予檔名任何意義。
確切的 target 命名與匹配方案屬 build 工具細節，**延後**。

> **[not yet]** 沒有任何後綴被辨識。module 目錄裡每個 `.zg` 都會被編進建置，所以同時放著 `plat_linux.zg` 與
> `plat_darwin.zg` 的 module 就是同一個名字宣告了兩次，會以碰撞被拒——這比挑錯一個要清楚，但仍然不是這個功能。
