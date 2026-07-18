# Zerg 值與記憶體（Values & Memory）

一個值如何被擁有、複製與釋放——scope ownership、copy-by-value、`mut`、`del` / `defer`,以及逃出 scope 的
`Ref[T]`。屬於 [語言參考](language.zh-TW.md) 的一部分。亦有 [English](memory.md) 版本。

無 garbage collector、無 pointer 語法。每個值都是 **scope-owned**（離開 scope 即釋放），且預設以**值傳遞**。
copy-by-value 是語意；編譯器會在安全時省略複製：

- **單一執行流程**——immutable 的值可隱形地改以 by-ref 傳遞；mutable 的則 fallback 為複製。
- **跨 coroutine**——一律複製：無共享可變狀態、無 data race；要把修改反映回去是呼叫端的責任（例如透過 channel）。
- **取值 / 回傳**——unwrap（`?`、`!`）、`match`、`return` 都是複製出來；來源永不失效。move 只是來源之後死掉時的
  隱形最佳化。

遞迴與自我參照型別不需要 pointer——直接宣告欄位（例如 `Node?` → `Node`），編譯器**自動**插入 heap 間接；這類值
一樣是 scope-owned 且 copy-by-value。

**一個 `struct` 的佈局就是它的宣告。** 欄位照**宣告序**排、值 **inline** 嵌在它的擁有者裡（除了上述遞迴 auto-boxing
之外沒有間接），而且編譯器**絕不重排**——所以一個 Zerg `struct` _就是_ 一個 C `struct`、field-for-field、自然對齊
配標準 padding。這是 transpile 到 C 掉出來的，也正是為什麼 struct **預設就 FFI-ready**（見 [FFI](ffi.zh-TW.md)）：
沒有另一套「最佳化」佈局可以 opt-out，所以 Zerg 不需要 `repr(C)` 標記。（sum type 的 payload 同樣 inline；只有它
discriminant 的確切 C 編碼是一個 deferred 的 FFI 細節。）更緊的控制——去掉 padding（**packed**）或強制更寬的
**alignment**，給封包格式與 memory-mapped 硬體用——是 niche 旋鈕，**擱置**到有具體需求為止。

mutability 屬於**實例（instance）**——也就是 binding——不是型別或任何欄位：`mut x := …` 讓整個建構出的實例
可變（每個欄位），`x := …` 則保持不可變；欄位只帶可見性（`pub` 或 private）。Zerg 沒有通用 reference；程式之間
只能透過以下方式共享儲存：

- **Mutable-ref 參數**（`mut` 參數）——唯一「語意上真的 by-ref」：被呼叫端就地改呼叫端（`mut`）的變數。它受限於
  這次呼叫——值的位置（欄位、`return`、送 channel）都是**複製當下的值**，只能往下傳給另一個 `mut` 參數，且不能跨
  `spawn`。**兩個 `mut` 引數永不共享同一塊儲存**——這是被呼叫端倚賴的保證：靜態別名（`f(x, x)`）是
  **compile error**，而編譯器無法證明之處（`f(mut xs[i], mut xs[j])` 且 runtime `i == j`）該次呼叫會
  **abort**（`AliasError`）。檢查只插在「mut 引數可能動態別名」的呼叫點。
- **Channel**——在 coroutine 之間以 by ref 共享，僅用於通訊。

**求值順序是左到右。** 函式引數、運算子的運算元、以及 `list`／`map`／`set` literal 的元素都**依原始碼順序**求值、
deterministic——不像 C 的引數求值順序是 unspecified。所以副作用（一個 `mut` 引數、一次 abort）的次序可預測；
`and`／`or` 的短路就是這條規則加上「跳過右運算元」（見 [內建 spec](specs.zh-TW.md)）。

**Reference-counted 的值**是 scope-owning 的唯一例外：型別實作 **`Ref`** 的值——內建的 **`chan`**，或 stdlib 的
**`Ref[T]`** 盒——以 **reference** 共享、而非複製。runtime 計數持有者，在**最後**一個持有者的 scope 退出時釋放；
其餘一切純 scope-owned、無 GC/refcount。複製一個值時，會對它（遞迴）包含的每個 `Ref` 值做 refcount++、深拷貝其餘
部分；`Ref` 值永遠共享、絕不被複製。

**refcount 在構造上就是 cycle-complete**，所以不需要循環收集器、也不需要 weak reference：`Ref[T]` 的 referent
在**盒子建構時就固定**（要指別處就建一個新的 `Ref`），而值 immutable-by-default、又是 bottom-up 建構，沒有辦法讓
一個既存的 `Ref` 回頭指向後建的值——參照循環永遠形不成，所以「最後持有者釋放」永遠是完整的。（唯一的退化個案
——`chan` 把指向自己的 reference buffer 進自己——是 programmer error、不是被檢查的個案。）

## `Ref[T]`——逃出自身 scope 的資源

多數清理只是記憶體，離開 scope 時就自動釋放。若一個**資源的釋放不屬於這種自動釋放**——foreign handle（見
[FFI](ffi.zh-TW.md)）、任何必須**恰好關閉一次**者——且它必須**逃出開啟它的 scope**（被 return、存進欄位、送過
channel），就用 **`Ref[T]`** 持有：一個 reference-counted 的資源盒，攜帶該值與一個 `drop` 動作。因為它以 **by-ref** 複製，
每份 copy 都指向**同一個**資源，`drop` 在最後一個持有者的 scope 退出時（或明確 `del`）**跑一次**。這正是裸的
copy-by-value handle 給不了的保證——一個普通 handle 的兩份 copy 會各自試圖釋放那唯一的資源。**唯有資源逃出
scope 時**才用 `Ref[T]`；侷限在單一 scope 的資源要用 `defer`（見下）。

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

| `del` 的對象                 | 你擁有它嗎？ | 效果                                                                         |
| ---------------------------- | ------------ | ---------------------------------------------------------------------------- |
| local、傳值參數、捕獲副本    | 是           | 最後存取 → **釋放儲存**                                                      |
| `mut` 參數（借呼叫端的變數） | 否           | 結束本次呼叫的借用 → **不釋放**；呼叫端保有                                  |
| closure body 內的捕獲值      | 否           | 結束**本次 invocation** 的存取 → 不釋放；下次呼叫仍有                        |
| channel、`Ref[T]`            | refcounted   | 放掉這個 holder（refcount--）；最後 holder 跑 **`drop`**（channel 即 close） |

`del` 永不懸空：撤銷一個借用不可能釋放別的名字所擁有的儲存，而 Zerg 既有規則已擋掉「owner 在 borrower 仍存活時
就釋放」（`mut` 參數受限於該次呼叫；逃逸的 closure 擁有捕獲的副本）。編譯器靜態就知道每個 `del` 是釋放還是純
撤銷——只有 `Ref` 值（channel 與 `Ref[T]`）帶 runtime refcount。

`del` 是**流程一致的**：一個名字只要在任一路徑上被 `del`，其後**每一條**路徑都視它為已死（不引入 runtime drop
flag）。因此在 `if` 某一分支裡 `del`，匯流之後該名字即不可再用，與其他分支對稱。

`del ch` 也是**提早關閉 channel** 的直接寫法——當下放掉你對 `ch` 的持有，若你是它最後一個 sender，
就會關閉 channel，無需再包一層更窄的 block。

## `defer`——在 block 退出時清理

`defer stmt` 安排 `stmt` 在所在 **block** 退出時執行——**每一條**離開路徑都跑，**包含 abort unwind**。它是「綁在
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
