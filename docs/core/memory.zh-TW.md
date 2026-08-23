# Zerg 值與記憶體（Values & Memory）

一個值如何被擁有、複製與釋放——scope ownership、copy-by-value、`mut`、`del` / `defer`,以及逃出 scope 的
`Ref[T]`。屬於 [語言參考](../language.zh-TW.md) 的一部分。亦有 [English](memory.md) 版本。

無 garbage collector、無 pointer 語法。每個值都是 **scope-owned**（離開 scope 即釋放），且預設以**值傳遞**。
copy-by-value 是語意；編譯器會在安全時省略複製：

**指派會把舊的值還回去。** 寫過一個 binding 或一個欄位，會釋放它原本持有的東西，而新值在釋放**之前**就先被
建好——`s = s + x` 與 `xs = [xs[0], xs[2]]` 都會讀到它們正要取代的東西，所以順序是規則的一部分、不是最佳化。
`for … in` 會複製它所走訪的集合，並在每一條離開路徑上釋放那份複本，`break` 與 abort 都算。

`make mem-check` 逐一量這些：同一支程式跑 5 輪與 200 輪，存活的配置數必須相等，所以每輪留下的值會顯示為一個
會成長的差值。

- **單一執行流程**——immutable 的值可隱形地改以 by-ref 傳遞；mutable 的則 fallback 為複製。
- **跨 coroutine**——一律複製：無共享可變狀態、無 data race；要把修改反映回去是呼叫端的責任（例如透過 channel）。
- **取值 / 回傳**——unwrap（`?`、`!`）、`match`、`return` 都是複製出來；來源永不失效。move 只是來源之後死掉時的
  隱形最佳化。

遞迴與自我參照型別不需要 pointer——直接宣告欄位(例如 `Node?`,或 `enum Expr { Num(int); Add(Expr, Expr) }`),
編譯器把那個自我參照的槽**自動裝箱在一個 refcounted cell 之後**。因此遞迴值的複製是**按參照**(refcount 共享),不是
深拷貝:複製只令該 cell 的計數遞增、而非複製整條鏈,鏈則在最後持有者的 scope 結束時釋放。

釋放是精確的。cell 的 drop 就是那個 enum 自己的 drop,binding 在宣告處登記它,而指派會在**具現化新值之後**才把舊值
還回去。以計數 allocator 實測:200 輪各建一條 2000 節點的 `enum L { Nil; Cons(int, L) }` 再丟掉,結束時存活的配置數
與 5 輪完全相同(`make mem-check`)。

> **[implementation-defined]** **一條鏈能被釋放到多長。** 拆解每個節點吃掉一個 C stack frame,所以深度由它跑在
> 哪條 stack 上決定:在預設 8 MiB 主 stack 上實測,**6 萬個節點跑得完,7 萬個不行** —— 約每個節點 128 bytes。超過
> 之後行程帶著名字死去(`StackOverflowError: stack overflow`、狀態碼 1),而不是一個裸的 SIGSEGV;但那是一份診斷、
> 不是一次復原:釋放跑在 scope 結束與 abort unwind 的路徑上,所以沒有 `guard` 抓得到它,它之後的 `defer` 也不會跑。
> 深到那個程度的**結構**,該用語言為它準備的形狀 —— 一個依位置索引的 `list` 的節點,而不是一條鏈 —— 直到那扇
> [門](../../FUTURE.zh-TW.md#一次迭代式的鏈拆解)打開為止。

---

> **[not yet]** 遞迴 **`struct`** 根本宣告不出來,所以上面那條界限不可能經由它到達。
> `struct Node { value: int; next: Node? }` 會被拒絕、報 _E4026 `Node` is part of a cycle of by-value declarations —
> a type holding itself, however indirectly, has no size_:算大小這件事跑在宣告圖上、早於任何裝箱決定,所以那個自我
> 參照的槽從來沒拿到那個會給它一個大小的 cell。建得起來的是遞迴 **`enum`** 那一半,它的裝箱與 refcount 共享如上
> 所述。下面〈複製語意 vs 參照語意〉用到的那個 `Node`——它正是唯一能
> 觀察到共享變動之處——是規範中的形式,今天編不過。它同時還帶著第二個未建置的形式:那些**具名引數**
> (`Node(value: 1, …)`)是 `E9010`,因為這裡的引數依位置綁定(見[型別](types.zh-TW.md))。

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

> **[not yet]** 沒有執行期的 `AliasError`,也沒有任何一種執行期檢查:編譯器是**靜態而保守地**判定別名的,兩個取自
> 同一個變數的 `mut &` 引數一律被拒絕,不管索引說了什麼。所以連可證明互異的 `two(xs[0], xs[1])` 也會被直接拒絕、報
> _E3025 `xs` is given to two `mut &` parameters of `two` in one call — a borrow may not alias, which is what keeps it
> safe without a borrow checker_。被呼叫端倚賴的那個保證確實成立,而它成立的方式是**拒絕合法的程式**:規範中的規則
> 接受這次呼叫,只有在索引真的相遇時才 abort。

**求值順序是左到右。** 函式引數、運算子的運算元、以及 `list`／`map` literal 或 `set(...)` constructor 的元素都
**依原始碼順序**求值、deterministic——所以副作用（一個 `mut &` 引數、一次 abort）的次序可預測，不像 C 的引數
求值順序是 unspecified。

二元運算元、呼叫的引數、方法呼叫的 receiver 與引數、以及 struct 建構的欄位，凡順序可觀察處皆已**排序**：第一個
之後只要有任何運算元可能執行程式碼，整份清單就會依原始碼順序求值進 temporary。不可能執行程式碼的運算元——literal,
或單純的名稱讀取——留在原處，所以常見的 `f(g())` 與 `x + 1` 完全未變。**短路**運算子——`and`、`or`、`??`、`?.`,
以及 `?` unwrap——是更強意義的左到右：當左邊已決定結果時，右邊被**跳過**。

**enum variant 的 payload**(`E.V(f(1), g(2))`)以及透過**函式值**的呼叫,適用同一條規則 —— 它們是最後兩種把運算
元交給單一 C 構造、沿用 C 答案的形式,而 [`1g/evalorder`](../../examples/1g/evalorder) 就是釘住它們的案例。有兩個
位置**刻意不**排序,兩個編譯器皆然:內建的 intrinsic 與內建的錯誤建構式。它們都無法被「不知道自己是由哪個 C 編譯器
建出來」的程式分辨,而這條界線在兩邊畫在同一個位置,因為 `make oracle` 會比對它們。

把某個運算元讀**不只一次**的形式，適用同一條規則，只有觸發條件不同。`v in lo..hi` 就是那一個：成員測試是界限比較，
所以它在每個界都指名 `v`——而上面那種 run 之所以能豁免第一個運算元，是因為沒有東西排在它前面，這一個不能，因為 `v`
的第二次讀取排在兩個界之後。所以 subject 只被求值**一次**、在任一界之前，兩個界再依原始碼順序接在它後面：
`f() in 1..10` 剛好呼叫 `f()` 一次。

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
  `list` 的 buffer 以 **copy-on-write** 實現這個複製 —— 複製時共用它，元素則由**第一個寫入**的持有者複製出來。
  這是**實作細節**：沒有任何程式分辨得出來，因為複製發生在另一個持有者可能看見的任何寫入之前。它換到的是：把
  collection 傳給只讀它的函式、或交給一個 coroutine，成本是一次遞增而不是整個 buffer。
- **Reference-counted 值**——一個 `str`、一個 `chan`、一個 `Ref[T]`，以及**遞迴型別的自動裝箱子節點**——是
  **共享**的：複製會 retain 既有 cell（refcount++）而非複製它，最後持有者才釋放。所以透過**共享遞迴 tail 可達的
  一次變動，會經由該 tail 的每個持有者都看得見**。

carrier 擁有**它當下持有的那一側**,而它的 drop 與 copy 各自為每個擁有東西的側邊帶一條 arm:tag 說 Left 就走
Left 的、說 Right 就走 Right 的。optional 沒有 Right,只拿到 Left 那一條。`Result[T]` 的 Right 是一個 `Err`,那份
儲存屬於 runtime,所以兩條都不需要。

binding 因此可以像其他每一個擁有型別那樣登記 drop:`got := <-c` 在 scope 結束時釋放它的 payload;被當成引數傳遞、
被 return 出去、放在 struct 欄位或 `list[T?]` 元素裡的 carrier 也一樣;還有 `if v := <-c { … }` retain 進 binding
的那個值。以計數 allocator、200 輪對 5 輪實測(`make mem-check`)。

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

**指派**也是一次釋放：寫過一個擁有東西的 binding 會釋放它原本持有的值，而新值一定在舊值被釋放**之前**就先建好
——`s = s + x` 要讀 `s` 才做得出自己的右手邊。

每一個擁有東西的型別都這麼做，而這個問題就是 `c_drop_fn` 回答的那一個,不是一份「哪些型別」的第二清單。
它從前只有遞迴 `enum` 與 carrier:指派覆蓋一個 `str`、`list`、`map`、tuple、struct 或**被持有的函式** binding
會**丟棄**舊值,寫過去一個**欄位**、以及 `for … in` 走訪一個 **map** 時複製出來的那份集合也一樣。六種形狀、
同一組配對裡缺的同一半,而 `make mem-check` 現在每一種都有案例——`assign_list_literal`、`assign_str`、
`assign_list_value`、`assign_field`、`assign_fn_value`、`forin_map_copy`。

**list literal** 曾有自己的一條路徑,而它輸了兩次:它先把目的地重新初始化、再把元素推進去,於是舊 buffer 被
丟棄,而且一個會讀到自己所取代之物的 literal 讀到的是被清空的 list——`xs = [xs[0], xs[2]]` 讓一支正確的程式
raise _IndexError: index out of range_。它現在建在目的地旁邊再搬進去,就是其他每個擁有型別走的那條
materialise-release-store。

**force-unwrap** 抄出來的那份 payload 也曾在那份清單上,而它量起來是乾淨的:迴圈裡 `p: str? = s` 之後 `q!`,
每輪不留下任何東西(`force_unwrap_copy`)。

> **tuple 在 scope 結束時**本來也在那份名單上,現在不在了。它曾經有 copy helper 而完全沒有 drop,所以
> `t := (1, s)` retain 了那個 `str`、卻沒有任何地方把它還回去;現在它的 `_copy` 旁邊有一份 `_drop`,而
> `make mem-check` 的 `tuple_heap` 會把它數出來。缺的那一半也正是 `(int, str)?` 根本編不起來的原因——
> carrier 是從 drop 那個問題決定要 emit 什麼,它的呼叫端卻是從 copy 那個問題決定要叫什麼名字,於是那個被兩
> 邊答得不一樣的型別,在 C 裡被叫了名字、卻沒有任何地方宣告過它。

**`spawn` 捕獲的值屬於那個 coroutine**,不屬於發起它的 scope:環境為每一個捕獲值取得自己的一份 reference,並把它
**交給** coroutine,由後者的 by-value 參數在函式體返回時還回去。那是每個捕獲值、在每一條退出路徑上各還一次,
包含 abort-unwind 那條。

**一個從來沒跑過的 coroutine 也會還**,而且還的是另一份清單。`main` 一 return 程式就結束,還排在隊上的東西就留在
原地——所以一個 scheduler 始終沒輪到的 `spawn`,沒有任何傳值參數可以把東西交還給它:那塊環境連同一份屬於它自己的
teardown 一起交給 runtime,而 scheduler 在每個 worker 都停下之後跑它一次。兩份 teardown 互斥:函式體的 `defer` 從
它的 trampoline 開始的那一瞬間就擁有那塊環境,而 runtime 在同一瞬間放掉它的指標,所以兩者恰好只有一個會跑。
`make mem-check` 的 `spawn_unstarted` 守著它。

一個**已經開始、而且停泊著**的 coroutine 不屬於這個情形,也不會被釋放:要把它的捕獲值還回去,就得展開一條語言明說
「就地放生」的 stack。

## `Ref[T]`——逃出自身 scope 的資源

> **[not yet]** 這個編譯器裡沒有 `Ref[T]`。`Ref(5)` 會被指名拒絕——
> _NotImplemented: a refcounted box `Ref(x)` / `deref(r)` — this compiler has no `Ref[T]` type_——所以本節、它底下
> 那個 `mut` 與 effect 的區分、以及本頁其他每一處提到的 `Ref[T]`,講的都是一個沒有東西建得出來的型別。**機制**是建
> 好而且可用的:`Ref` spec 有一個實作者,也就是內建的 `chan`,它以參照共享、被計數、並在最後一個持有者的 scope 退出
> 時關閉,完全如規範所述。缺的是第二個實作者——那個承載任意值與一段使用者所寫 `drop` 的 stdlib 盒子——所以一個必須
> 逃出自身 scope 的資源,今天得到的不是本節給的答案,而是根本沒有答案。

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

`const` 不參與這件事：它在兩個方向上都是遮蔽免疫的（[`GRAMMAR`](../../GRAMMAR) 第 4 組），所以重新宣告既不能拿走
`const` 的名字，也不能對任何可見 binding 已持有的名字鑄造一個 `const`——同一個 block 也包括在內。

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

> **狀態。** 上表最後一列正是 `zerg` 完全走不到的那一列。`del` 一個 `Ref` 值在兩半上都是 **[not yet]**：對
> channel 做 `del ch` 會被具名拒絕（_E9066 NotImplemented: `del ch` on a CHANNEL_，訊息要你改寫 `close(ch)`），
> 而這裡根本沒有 `Ref[T]` 型別可 `del`——光是提到 `Ref` 就被拒絕（`E9058`）。編譯器對 channel 做的事是在其 binding
> 的 scope 結束處歸還它，所以 holder 仍會被放掉、最後一個仍會跑 `drop`；缺的是**提早**具名說出這件事的能力。
>
> ---
>
> `del` 一個**擁有**的值——一個 local `struct`、`list`、或 `map`——以**提早**釋放其儲存是 **[not yet]**，理由是
> 同一件事的另一面：今天這樣的 `del` 會撤銷名字的存取，但儲存是在一般的 scope 退出時回收、而非在 `del` 當下。所
> 以上表「釋放儲存」那一列，對擁有值而言是預期行為、尚非 bootstrap 現況。

`del` 永不懸空：撤銷一個借用不可能釋放別的名字所擁有的儲存，而 Zerg 既有規則已擋掉「owner 在 borrower 仍存活時
就釋放」（`mut &` 參數受限於該次呼叫；逃逸的 closure 擁有捕獲的副本）。編譯器靜態就知道每個 `del` 是釋放還是純
撤銷——只有 `Ref` 值（channel 與 `Ref[T]`）帶 runtime refcount。

`del` 是**流程一致的**：一個名字只要在任一路徑上被 `del`，其後**每一條**路徑都視它為已死（不引入 runtime drop
flag）。因此在 `if` 某一分支裡 `del`，匯流之後該名字即不可再用，與其他分支對稱。

**channel 也不例外。** `del ch` 放掉你的持有*並且*撤銷名字，所以 `ch` 之後就不能用了——後續的 `ch <- v` 或 `<-ch`
都是編譯錯誤（_`ch` is used after del_）。因此它**不是**用來通知「沒有更多值」的方法：要在保留 handle 的情況下結束
一條 stream，用 channel 專屬的敘述 **`close(ch)`**；要靠 scope 結束它，就讓該 binding 的 scope 離開去歸還它所持有
的東西。兩者都在 [Coroutines](../code/coroutine.zh-TW.md)。當你連**名字**也用完了，才用 `del ch`。

> **[not yet]** 上一段是規格所訂的規則；`del ch` 本身會被拒絕（`E9066`，見上方狀態註）。其中已經成立的那一半是那
> 個建議：`close(ch)` 結束一條 stream、scope 離開歸還持有，那兩件才是今天的程式寫得出來的。

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

三者共用一條軸——清理**何時**觸發：`del` **當下**撤銷一個名字；`defer` 在**本 block** 退出時觸發（順序見「釋放
順序」）；`Ref[T]` 的 drop 在**最後一個持有者**退出時觸發。分界只有一個問題——資源會不會逃出它的 scope？
**不會 → `defer`；會 → `Ref[T]`。**

一個 **`with` block** 把這種資源綁進一段語彙區間——而它是那個本來就會這麼做的裸 block 的**純語法** sugar:
`with acquire() as y { … }` 就是 `{ y := acquire(); … }`,而無名的 `with e { … }` 仍然會綁定,綁到一個只有編譯器
會寫的名字上,因為 `e; …` 會讓值死在該敘述而不是 `}`。它**不引入第四種機制**:跑釋放的是上面那條軸線,未變,
而那條軸線本來就涵蓋每一條離開路徑,包括 abort。

一個資源如果它的釋放是**某人必須記得呼叫的方法**,那它根本不是 `with` 的案例——它是一個 `defer`,寫出來,
寫在 `with` 剛剛打開的那個 block 裡。

`with` **已經實作**,而且就是上面那條展開式、沒有別的——它本身不帶任何 teardown。`examples/18_scoped.zg`
就是隨貨附上的示範。
