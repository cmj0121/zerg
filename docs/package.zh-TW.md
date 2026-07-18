# Zerg Module、Package 與 Program

Zerg 原始碼如何組織、建置與啟動。本文建立在 [語言參考](language.zh-TW.md) 的可見性、記憶體、spec 與錯誤模型之上。
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

### Program 與 entry point

**program 是一次建置**，不是某種特殊的 package。你把 compiler 指向一個 **entry 檔**——`zerg entry.zg`——它就以
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
- **結果。** `main` 回傳 `Result[nil]`，所以退出複用錯誤模型：`Left` 以 `0` 退出，`Right(err)` 把 `err` 印到 stderr
  並以非零碼退出，未被攔截的 **abort** 則 unwind main stack 而 crash。預期失敗（`Right`）與 bug（abort）維持兩種
  不同的退出，而且 `?` 可以直接用在 `main` 裡。

### Program 生命週期與頂層初始化

`main` 的 body 是**整個 program 的根 scope**：它一回傳，底下所有 scope-owned 的東西就會被釋放，任何還在跑的 coroutine
就地被拋棄（沒有 join——要是某個 coroutine 必須先跑完，就用 channel 觀察到它完成、再讓 main 退；見
[Coroutines 與 Channels](coroutine.zh-TW.md)）。

`main` 之外只住著**不可變的頂層狀態**——常數、函式、型別與 spec——在 `main` 執行前備妥。頂層常數以**依賴序**初始化；
它們之間要是形成循環，就是 compile error。

一個 module 也可定義 **`init()`** 函式（**可多個**）——它**惰性**的一次性 setup。它們**恰好跑一次**，在該 module
**首次被使用時**（其後的使用略過；並行的首次使用仍只跑一次），依**相依序**（module 的 imports 先 init），在它任何
自己的程式碼之前。`init()` 承載多步或有副作用的啟動（開資源、註冊、seed），而不是把它藏進 constant 的 initializer，
並備妥該 module 的 immutable 狀態。仍**沒有可變全域**：共享的可變狀態以值傳遞或走 channel，絕不透過 module 層級的變數。

若某個 `init()` **abort**,該 abort 從觸發它的**首次使用點**往外傳——可在那裡用 `guard` 接住,否則就像任何未接的
abort 一樣 crash 那條 stack(主 stack 結束程式、coroutine 只結束自己)。該 module 於是**中毒(poisoned)**:`init()`
**不重跑**(恰好一次即使失敗也成立,所以副作用不重複),而其後每次使用都**以同一個快取的錯誤再度 abort**。一個
半初始化的 module 永不會變成可用,並行的首次使用也全都看到那同一個失敗。

### Package

**package** 是一棵 module 樹，也是**散布、相依與版本**的單位——你發佈、依賴、釘版本的那個東西。package 形成一張
**依賴 DAG**（directed acyclic graph，有向無環圖——相依永不繞回）：package 之間的循環會被拒絕。

一次建置在整張圖裡對每個 package **只選一個版本**——同一個 package 絕不會在一個 program 裡出現兩種版本——因此一個
package 的型別在全程式裡保有單一身分。

### Coherence 與 orphan rule

一個 `spec` 的實作是**全域唯一**的：整個 program 裡，任何一組 `(型別, spec)` 只有一個正規實作。這由 **orphan rule**
在地強制——一個實作必須住在**定義該型別的 package**、或**定義該 spec 的 package**。

這仍讓你能**替 import 進來的型別加上新行為**：定義你自己的 spec、為那個外來型別實作它——spec 是你擁有的，orphan
rule 就滿足了。給別人的型別加一項能力是**一等、日常的動作**，不是變通。這條規則唯一擋掉的組合是**外來型別 × 外來
spec**——替別人的型別實作別人的 spec、兩者你都不擁有；這種較罕見的情況，就用你自己擁有的 **newtype**（單欄位
struct，搭配 opt-in 的 auto-cast 減少包裝摩擦）把型別包一層，在包裝上實作該 spec。

coherence **不需要全域註冊表**——orphan rule 加上**無環**的 package 圖就保證了它。要 author 一組 `(型別, spec)` 的
實作，一個 package 必須能同時指名兩者；而因為依賴圖是 DAG，兩個擁有者 package 中至多只有一個能依賴（因而指名）
另一個，任何第三方 package 也無法在不擁有其一的情況下同時指名兩者。所以該實作要是存在，就由構造保證唯一。單一版本
選擇正是讓「一型別、一實作」有明確定義的前提。

### Module

**一個 module 就是一個目錄**；裡面的檔案是共享同一命名空間的實體切片——檔案數量是排版、不是語意。module 是預設的
私有單位：一個未加標記的宣告在該 module 的各檔案間可見，但不越出 module（見可見性）。

巢狀是**扁平的**：把一個目錄放在另一個底下，只是讓 import path 變長——**沒有階層式私有**，內層 module 對外層並無
特殊存取權。**module 之間的 import 循環會被拒絕。**

所以相互遞迴的型別與函式住在**同一個 module**——而這不痛，因為 module 是共享命名空間的多檔案目錄：一個 `ast`
module 可以把 `Expr`、`Stmt` 分放在不同檔案、彼此**免 import** 互相引用，編譯器 forward-declare、auto-boxing 讓遞迴有
有限 layout，與自我參照型別完全相同。當兩個分屬**不同關注點**的型別互相回指，這條禁令是個推力——用 **id 引用**打破
循環（通常是更好的設計），而不是把它們併一起（package 圖是 DAG 也是同一道理：互相依賴的 package 必須合為一個）。

無論佈局如何，唯一必須無環的是**頂層常數初始化**（見 Program 生命週期與頂層初始化）：那裡的循環沒有合法順序、是
compile error。一個型別指名另一個型別**從來不是**這種循環——只有初始器會遞迴地依賴自身值的常數才是。

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

### 匯入與引用

引用另一個宣告一律**顯式**——無 wildcard、無遞迴傳遞（import 一個 package 只給你它的公開表面，絕不給你它自己
import 的那些 package），也沒有 ambient import。每個名字要嘛是宣告的、import 的，要嘛是 toolchain **built-in**——
primitive 關鍵字與 prelude（見 Prelude 與 std）。要 import 什麼，取決於距離：

- **同一 module**（同目錄）——無須 import；一個 module 的各檔案共享一個命名空間。
- **同 package 的其他 module**——import 那個 sibling module，即可指名它的 package-internal（`pub`）宣告。
- **別的 package**——import 該 package，只看得到它 root 的公開表面；相依套件的內層 module **不可達**，依賴者只拿得到
  那個公開表面。

因為每個相依都被寫下來，import graph 是顯式的——這正是 module 與 package **循環得以被拒絕**的前提。

### Prelude 與 std（The prelude & std）

**prelude 不是被 import 的**——它的名字是 **built into the toolchain**，從一開始就綁在每個 module 裡，正如 primitive
關鍵字。它裝的是語言本身倚賴的東西：運算子 desugar 的目標型別（`Either`、`Result`、`T?`、`nil`）、built-in spec
（`Object`、`Error`、`Ord`、`Hash`、`Iterator`／`Iterable`、`Ref`、運算子 spec——見 [Spec 與 Generics](specs.zh-TW.md)），
外加少數泛用型別——`list`、`map`、`set` 容器（見 [Collection](collections.zh-TW.md)）與 `Ref[T]` 資源盒。
（primitives——`bool`、`int`、`str`……——與 `chan`、以及 `defer`／`print` 構造同樣是 grammar 與 runtime，不是被
import 的名字。）這些名字是
**保留字**：宣告不得 shadow 或重宣告它們，所以那些 desugar 到它們的
運算子永遠不會被從語言底下抽走。

其餘一切都是**標準函式庫**——一個普通 package，只有一點不同：**std 隨 toolchain 出貨**，所以它的版本就是編譯器的
版本、你從不把它列為相依。它像一般 package 一樣顯式 import：`io`、`math`、更多 collection，以及讀取唯讀 OS 狀態的
ambient-OS 函式（`env`、時鐘、亂數）。

因為 prelude 是 built-in、而不是隱式 import，「無 ambient import」就毫無例外地成立。

### 測試與可見性（Testing & visibility）

測試就是普通程式碼、套同一套可見性規則——隱私**沒有測試專用後門**。這決定了測試該放哪：

- **白箱**——要驗一個 module 的 `module-private` 內部，就把測試放進**該 module 的一個檔案**；共享命名空間，它直接
  看得到內部，免 import、無特殊存取。
- **黑箱**——要驗某個 API，就 import 它：sibling module 看得到 package-internal（`pub`）表面，而一個依賴此 package
  的獨立 package 看到的正是 package-public 表面——真正的外部視角。

測試檔由 **build 工具依慣例**辨識（例如 `*_test.zg` 檔名），只在 test build 納入、normal build 一律排除。因此測試的
宣告永遠到不了 shipped artifact 或 package 的公開表面——即使測試檔放在 root module、即使標了 `pub`，也留在對外 API
之外。一如 entry 檔，語言本身不賦予檔名任何意義，是工具賦予的。

### Target 條件式檔案

平台與架構差異用**同一套方式**處理——在**檔案層級、由 build 工具依慣例**——而不是語言內的 `#ifdef` / `cfg` 構造
（那會讓程式碎裂、違背 `small and crisp`）。一個 module 保留**各 target 的檔案**（像 `_linux` / `_darwin` 的名稱後
綴，與 `_test` 慣例並列），build 只納入符合所選 target 的那些；語言本身維持 **target-agnostic**、不賦予檔名任何意義。
確切的 target 命名與匹配方案屬 build 工具細節，**延後**。
