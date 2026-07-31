# Zerg 值與記憶體（Values & Memory）

一個值如何被擁有、複製與釋放——scope ownership、copy-by-value、`mut`、`del` / `defer`,以及逃出 scope 的
`Ref[T]`。屬於 [語言參考](../language.zh-TW.md) 的一部分。亦有 [English](memory.md) 版本。

無 garbage collector、無 pointer 語法。每個值都是 **scope-owned**（離開 scope 即釋放），且預設以**值傳遞**。
copy-by-value 是語意；編譯器會在安全時省略複製：

- **單一執行流程**——immutable 的值可隱形地改以 by-ref 傳遞；mutable 的則 fallback 為複製。
- **跨 coroutine**——一律複製：無共享可變狀態、無 data race；要把修改反映回去是呼叫端的責任（例如透過 channel）。
- **取值 / 回傳**——unwrap（`?`、`!`）、`match`、`return` 都是複製出來；來源永不失效。move 只是來源之後死掉時的
  隱形最佳化。

遞迴與自我參照型別不需要 pointer——直接宣告欄位(例如 `Node?`,或 `enum Expr { Num(int); Add(Expr, Expr) }`),
編譯器把那個自我參照的槽**自動裝箱在一個 refcounted cell 之後**。因此遞迴值的複製是**按參照**(refcount 共享),不是
深拷貝:複製只令該 cell 的計數遞增、而非複製整條鏈,鏈則在最後持有者的 scope 結束時釋放。這個階段有兩個 MVP 注意事項:
透過 `mut` binding 重新指派遞迴欄位而建出的 runtime **循環**會**洩漏**——尚無循環收集器(**[deviation]**);而釋放
一條長鏈會在原生 C stack 上**遞迴 O(depth)**、並可能將其溢位(**[deviation]**——即 [Conformance](../conformance.zh-TW.md)
與 [Errors](../code/errors.zh-TW.md) 所載、同一個不可回復的 stack-overflow deviation)。

**一個 `struct` 的佈局就是它的宣告。** 欄位照**宣告序**排、值 **inline** 嵌在它的擁有者裡（除了上述遞迴 auto-boxing
之外沒有間接），而且編譯器**絕不重排**——所以一個 Zerg `struct` _就是_ 一個 C `struct`、field-for-field、自然對齊
配標準 padding。這是 transpile 到 C 掉出來的，也正是為什麼 struct **預設就 FFI-ready**（見 [FFI](../runtime/ffi.zh-TW.md)）：
沒有另一套「最佳化」佈局可以 opt-out，所以 Zerg 不需要 `repr(C)` 標記。（sum type 的 payload 同樣 inline；只有它
discriminant 的確切 C 編碼是一個 deferred 的 FFI 細節。）更緊的控制——去掉 padding（**packed**）或強制更寬的
**alignment**，給封包格式與 memory-mapped 硬體用——是 niche 旋鈕，**擱置**到有具體需求為止。

mutability 屬於**實例（instance）**——也就是 binding——不是型別或任何欄位：`mut x := …` 讓整個建構出的實例
可變（每個欄位），`x := …` 則保持不可變；欄位只帶可見性（`pub` 或 private）。Zerg 沒有通用 reference；程式之間
只能透過以下方式共享儲存：

- **Mutable-ref 參數**（`mut &` 參數）——唯一「語意上真的 by-ref」：被呼叫端就地改呼叫端（`mut`）的變數。它受限於
  這次呼叫——值的位置（欄位、`return`、送 channel）都是**複製當下的值**，只能往下傳給另一個 `mut &` 參數，且不能跨
  `spawn`。**兩個 `mut &` 引數永不共享同一塊儲存**——這是被呼叫端倚賴的保證：靜態別名（`f(x, x)`）是
  **compile error**，而編譯器無法證明之處（`f(xs[i], xs[j])` 且 runtime `i == j`）該次呼叫會
  **abort**（`AliasError`）。檢查只插在「`mut &` 引數可能動態別名」的呼叫點。
- **Channel**——在 coroutine 之間以 by ref 共享，僅用於通訊。

**求值順序是左到右。** 函式引數、運算子的運算元、以及 `list`／`map` literal 或 `set(...)` constructor 的元素都
**依原始碼順序**求值、deterministic——所以副作用（一個 `mut &` 引數、一次 abort）的次序可預測，不像 C 的引數
求值順序是 unspecified。

> **[deviation]** bootstrap 目前只把 **literal 的元素**依左到右排序；**函式呼叫的引數與二元運算元**沿用 C 的
> unspecified 順序，所以 `add(f(1), g(2))` 可能先算 `g(2)` 再算 `f(1)`。**短路**運算子——`and`、`or`、`??`、`?.`,
> 以及 `?` unwrap——**確實**依左到右生效：先求值左運算元，當左邊已決定結果時就跳過右邊。
> [Conformance](../conformance.zh-TW.md) 把「呼叫引數／運算元的順序」列為 implementation-defined 之點，直到預期的
> 左到右規則被強制為止。

**Reference-counted 的值**是 scope-owning 的唯一例外：型別實作 **`Ref`** 的值——內建的 **`chan`**，或 stdlib 的
**`Ref[T]`** 盒——以 **reference** 共享、而非複製。runtime 計數持有者，在**最後**一個持有者的 scope 退出時釋放；
其餘一切純 scope-owned、無 GC/refcount。複製一個值時，會對它（遞迴）包含的每個 `Ref` 值做 refcount++、深拷貝其餘
部分；`Ref` 值永遠共享、絕不被複製。(作為**實作細節**,程式產生的 runtime `str` 值在內部同樣被 refcount、並於最後
使用處釋放——表面沒有改變,但產生的字串不再洩漏。)

對**明確的** refcounted 值而言,**refcount 在構造上就是 cycle-complete**,所以不需要循環收集器、也不需要 weak
reference:`Ref[T]` 的 referent 在**盒子建構時就固定**(要指別處就建一個新的 `Ref`),而值 immutable-by-default、又是
bottom-up 建構,沒有辦法讓一個既存的 `Ref` 回頭指向後建的值——參照循環永遠形不成,所以「最後持有者釋放」永遠是完整
的。(唯一的退化個案——`chan` 把指向自己的 reference buffer 進自己——是 programmer error、不是被檢查的個案。)唯一的
例外是上面那個**自動裝箱的遞迴 cell**:因為 `mut` 遞迴欄位可被重新指派成一條 back-edge,循環*可以*在那裡形成——而且
這個階段**不被收集、會洩漏**(一個有界、已載明的 MVP 缺口,是直接允許自我參照型別的代價)。

## 複製語意 vs 參照語意

兩個名字是否共享儲存，由一條界線決定，畫在兩個互斥的類別之間：

- **值型別（value type）**——每個 scalar、一個 `struct`、一個 tuple，以及 heap 容器 `list` 與 `map`——都是
  **被複製**的。scalar、`struct`、tuple 以 inline 複製；`list` 或 `map` 則**逐元素**依同一條規則複製。所以
  值型別的兩個名字**永不 alias**：透過其一寫入，只改變那個持有者自己的 copy。
- **Reference-counted 值**——一個 `str`、一個 `chan`、一個 `Ref[T]`，以及**遞迴型別的自動裝箱子節點**——是
  **共享**的：複製會 retain 既有 cell（refcount++）而非複製它，最後持有者才釋放。所以透過**共享遞迴 tail 可達的
  一次變動，會經由該 tail 的每個持有者都看得見**。

  `list` 的 buffer 以 **copy-on-write** 實現這個複製 —— 複製時共用它，元素則由**第一個寫入**的持有者複製出來。
  這是 **[implementation detail]**，意義與 slicing 那節給的相同：沒有任何程式分辨得出來，因為複製發生在另一個
  持有者可能看見的任何寫入之前。它換到的是：把 collection 傳給只讀它的函式、或交給一個 coroutine，成本是一次
  遞增而不是整個 buffer。

複製一個複合值時，逐欄位套用這條規則——它的值型別部分被複製，而它（遞迴）包含的任何 reference-counted 部分被
retain。因為 `str` immutable、`Ref[T]` 的 referent 在建構時固定，唯一能觀察到共享變動之處，就是一個 **`mut`
遞迴** binding 的自動裝箱 cell：

```text
mut a := [1, 2, 3]                # 值型別
b := a                            # 複製——b 是獨立的 list
a[0] = 9                          # a 是 [9, 2, 3]；b 仍是 [1, 2, 3]——無 alias

struct Node { value: int; next: Node? }
mut n := Node(value: 1, next: Node(value: 2, next: nil))
m := n                            # struct 被複製；它裝箱的 `next` tail 是 refcount-shared
n.next!.value = 99                # 觸及共享的 tail——m.next!.value 也讀到 99
```

## 釋放順序（Drop order）

離開 scope 時，local 依**建構的逆序**釋放——最後建構的最先釋放——於是拆解鏡射建構。順序在
**聚合體內部**也被釘住：一個 `struct` 的欄位與一個 `enum` payload 的槽依**宣告的逆序**釋放。一個
`defer` 在 block 退出時、於**每一條**路徑上執行，**包含 abort-unwind 路徑**；一個 block 內多個 `defer` 以
**後登記先跑（LIFO）**執行，與同一逆序的 scope-owned 釋放及 `Ref` drop 交錯。

## `Ref[T]`——逃出自身 scope 的資源

多數清理只是記憶體，離開 scope 時就自動釋放。若一個**資源的釋放不屬於這種自動釋放**——foreign handle（見
[FFI](../runtime/ffi.zh-TW.md)）、任何必須**恰好關閉一次**者——且它必須**逃出開啟它的 scope**（被 return、存進欄位、送過
channel），就用 **`Ref[T]`** 持有：一個 reference-counted 的資源盒，攜帶該值與一個 `drop` 動作。因為它以 **by-ref** 複製，
每份 copy 都指向**同一個**資源，`drop` 在最後一個持有者的 scope 退出時（或明確 `del`）**跑一次**。這正是裸的
copy-by-value handle 給不了的保證——一個普通 handle 的兩份 copy 會各自試圖釋放那唯一的資源。**唯有資源逃出
scope 時**才用 `Ref[T]`；侷限在單一 scope 的資源要用 `defer`（見下）。

### `mut`管的是自有 field——handle 背後的狀態是 effect

`mut`（以及 `mut fn` 方法）追蹤的是值**自身擁有的 Zerg field** 的變動。`Ref[T]` _背後_ 的狀態——foreign
handle 的內部狀態：OS 檔案的 position、socket、資料庫 cursor——**不屬於 Zerg 值的 bytes**。它屬於那個資源，透過一個
by-ref 複製從不重製、且建構時即固定的 handle 取得。碰它是**未追蹤的 effect**（如同任何 I/O），**不是 mutation**
——因此推進它的方法**不需 `mut`**，其 receiver 可為 **immutable**：一個 immutable `File` 仍可 `read()` 並推進其
cursor，正如 C 的 `const FILE*` 仍可 `fread`。

這是個實際的**建模選擇**。想要你**擁有**的可變狀態 → 放 plain field，用 `mut` binding 上的 `mut fn` 去改
（受追蹤，並適用上述 no-aliasing 保證）；想要一個內部狀態由**外部端擁有**的資源 → 放 `Ref[T]` 之後——immutable
handle 搭配 effectful 方法，免 `mut`。分界問題與 `defer`-vs-`Ref[T]` 相同：**你改的是自己的 bytes，還是伸手去碰
handle 擁有的狀態？** Zerg 對自有 field **沒有 interior mutability**——那個維持 refcount cycle-free 的「預設
immutable、`Ref` 的 referent 建構時固定」，也讓 immutable binding 誠實地保持不可變。

## 重新宣告與遮蔽（Re-declaration & shadowing）

一個名字可以**重新宣告**——在同一個 block 或巢狀 block 皆可——新的 binding 在型別與可變性上都可與舊的不同。
重新宣告的語意是 **declare-del-declare**：先計算右側（因此它可讀到*舊*的 binding），接著把舊 binding `del` 掉
（見下），再把名字重新綁定。

```text
x := read()          # immutable
x := parse(x)        # 右側讀到舊 x；舊 x 被 del；名字重新綁到新值
mut x := x           # 再次遮蔽——這次可變，並以前一份 copy 為初值
```

因為舊 binding 在右側算完的當下就死亡，`x := transform(x)` 不需複製——來源已被證明死亡，move 最佳化即生效、
直接重用舊的儲存。

## `del`——顯式提早釋放

`del name` 在 scope 結束前**撤銷該名字對其儲存的存取權**。釋放儲存只是一個*後果*：唯有被撤銷的正是**擁有權**
存取、且沒有其他 holder 時才發生；否則 `del` 只是提早結束「這個名字（或這次借用）」的存取，儲存仍歸 owner。

| `del` 的對象                   | 你擁有它嗎？ | 效果                                                               |
| ------------------------------ | ------------ | ------------------------------------------------------------------ |
| local、傳值參數、捕獲副本      | 是           | 最後存取 → **釋放儲存**                                            |
| `mut &` 參數（借呼叫端的變數） | 否           | 結束本次呼叫的借用 → **不釋放**；呼叫端保有                        |
| closure body 內的捕獲值        | 否           | 結束**本次 invocation** 的存取 → 不釋放；下次呼叫仍有              |
| channel、`Ref[T]`              | refcounted   | 撤銷名字**並**放掉這個 holder（refcount--）；最後一個跑 **`drop`** |

> **狀態。** `del` 一個 `Ref` 值——一個 `chan` 或一個 `Ref[T]`——放掉一個 holder（並在最後一個跑 `drop`）是
> 可用。`del` 一個**擁有**的值——一個 local `struct`、`list`、或 `map`——以**提早**釋放其儲存是
> **[not yet]**：今天這樣的 `del` 會撤銷名字的存取，但儲存是在一般的 scope 退出時回收、而非在 `del` 當下。所以
> 上表「釋放儲存」那一列，對擁有值而言是預期行為、尚非 bootstrap 現況。

`del` 永不懸空：撤銷一個借用不可能釋放別的名字所擁有的儲存，而 Zerg 既有規則已擋掉「owner 在 borrower 仍存活時
就釋放」（`mut &` 參數受限於該次呼叫；逃逸的 closure 擁有捕獲的副本）。編譯器靜態就知道每個 `del` 是釋放還是純
撤銷——只有 `Ref` 值（channel 與 `Ref[T]`）帶 runtime refcount。

`del` 是**流程一致的**：一個名字只要在任一路徑上被 `del`，其後**每一條**路徑都視它為已死（不引入 runtime drop
flag）。因此在 `if` 某一分支裡 `del`，匯流之後該名字即不可再用，與其他分支對稱。

**channel 也不例外。** `del ch` 放掉你的持有*並且*撤銷名字，所以 `ch` 之後就不能用了——後續的 `ch <- v` 或 `<-ch`
都是編譯錯誤（_`ch` is used after del_）。因此它**不是**用來通知「沒有更多值」的方法：要在保留 handle 的情況下結束
一條 stream，用 channel 專屬的敘述 **`close(ch)`**；要靠 scope 結束它，就讓該 binding 的 scope 離開去歸還它所持有
的東西。兩者都在 [Coroutines](../code/coroutine.zh-TW.md)。當你連**名字**也用完了，才用 `del ch`。

## `defer`——在 block 退出時清理

`defer expr` 安排 `expr` 在所在 **block** 退出時執行——**每一條**離開路徑都跑，**包含 abort unwind**。它是「綁在
scope 上的副作用」的 procedural 工具——放鎖、flush buffer、關閉 scope-local 資源——完全不需要型別：

```text
{
    lock.acquire()
    defer lock.release()     # 每一種離開都會跑——正常、提早 return、或 risky() 內部 abort
    risky()
}
```

一個 block 內多個 `defer` 以**後登記先跑**執行，與 scope-owned 的釋放及 `Ref` 的 drop 交錯、依建構的逆序進行，於是
拆解正好鏡射建構。三者共用一條軸——清理**何時**觸發：`del` **當下**撤銷一個名字；`defer` 在**本 block** 退出時
觸發；`Ref[T]` 的 drop 在**最後一個持有者**退出時觸發。分界只有一個問題——資源會不會逃出它的 scope？**不會 →
`defer`；會 → `Ref[T]`。**

一個 **`with` block** 把這種資源綁進一段語彙區間、在 block 退出時跑它的釋放。今天它僅限於 **Ref-bearing** 資源
（一個 `chan` 或一個 `Ref[T]`）；`with` 施於一個一般的 `Scoped` 值是 **[not yet]**。
