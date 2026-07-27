# Zerg 函式與閉包（Functions & Closures）

函式作為一等值、它的型別追蹤什麼與不追蹤什麼、預設參數與具名引數,以及閉包捕獲。屬於
[語言參考](language.zh-TW.md) 的一部分。亦有 [English](functions.md) 版本。

函式是**一等值（first-class value）**：它有型別，可當引數傳遞、可回傳、可存進欄位、可綁定到變數。這**跨模組**也
成立——透過另一個模組指名的函式就是一個普通的值:`f := other.helper` 綁定它,接著 `f(x)` 呼叫它,與本地函式完全一樣。
把一個**裸的 top-level 函式**綁成值、以及**跨模組**這麼做,都是 **[implemented]**。一個 **generic** 函式
**在實例化之前不是一等值**:未實例化的 generic 名字本身不是值——唯有它的型別引數在使用點被固定後才成為值。
函式型別寫成 `fn(P...) -> R`；參數的 `mut &` 是**型別的一部分**，所以 `fn(mut &int) -> bool` 與 `fn(int) -> bool` 是不同型別
（兩者 calling convention 不同——就地 by-ref vs 複製）。可見性**不**屬於型別：`pub` 匯出的是 top-level 函式的
**名字**，永不隨值移動，對匿名函式也無意義。

**一個函式的型別就是它的輸入／輸出契約，僅此而已。** 它揭露參數——`mut &` 標出唯一的「引數層 effect」:寫回呼叫端的
可變參考——以及結果，可回復的失敗會以 `Result` / `Either` 顯示在那裡。它**不追蹤任何其他 effect**：一個函式有沒有做
I/O、讀 ambient 狀態（clock、randomness、`env`）、或可能 **abort**，都不出現在簽章裡。I/O 只能透過檔案的 `import`
看到；而 abort 幾乎在每個運算式都可能發生（一次溢位、一個壞 index），標它就是每條簽章上的噪音。「引數改動」與
「可回復錯誤」以外的 effect **刻意不追蹤**——Zerg 在這裡是 procedural-first——而非漏寫。

持有函式的 binding 其可變性就是一般的 per-instance 軸——`mut f := …` 可 rebind、`f := …` 不可——與上述一切正交。

## 預設參數與具名引數

Zerg **沒有 overloading**——一個名字就是一個函式——所以 overloading 通常換來的彈性，改由**呼叫端**提供：**預設
參數**與**具名引數**，兩者合起來就是「這個輸入可選」的正牌講法。

```text
fn greet(name: str, greeting: str = "Hello", loud: bool = false) -> str { … }

greet("Sam")                 # greeting = "Hello"、loud = false
greet("Sam", loud: true)     # greeting 用預設；loud 具名給
greet("Sam", "Hi", true)     # 全 positional
```

- 一個參數可宣告**預設值**——引數被省略時呼叫端所用的運算式。它在**每次被用到時於呼叫端求值**、絕不是在定義處
  求值一次，所以沒有 shared-mutable-default 的陷阱；它是普通運算式，且可讀取前面的參數（求值由左往右）。沒有預設
  的參數仍是**必填**。

  > **[deviation]** 今天只有**自足的簡單常數**預設（一個 literal，如 `443` 或 `"Hello"`）會被正確 lower。一個
  > **非平凡**的預設運算式——由運算子或呼叫組成者，例如 `greeting: str = "a" + "b"`——目前會被**錯誤處理**,而非
  > 如規格所述每次呼叫求值。在修好之前,預設值請保持為簡單常數。

- 一個**具名引數**以參數名字傳入（`loud: true`）——這正是讓你能**跳過中間的預設參數**的關鍵。規則就是慣例那套：
  positional 引數由左往右填、任何參數都可改用具名、有預設的可省略，而且**一旦具名，其後全部都要具名**（具名之後
  不能再回到 positional）。

因為參數可以用名字挑選，**名字就成了函式契約的一部分**——改名會弄壞呼叫者，就跟改型別一樣。但預設與名字都不進
_型別_：`fn(str, str, bool) -> str` 是型別，預設住在宣告裡、名字住在參數列——與「型別就是輸入／輸出契約、僅此
而已」一致。兩者都是**呼叫端 sugar**：跨越 C ABI（見 [FFI](ffi.zh-TW.md)）時，export 的函式全 positional、無預設。

**variadic** 參數刻意**不提供**——改為顯式傳 `list[T]`（`sum(xs: list[int])`，呼叫 `sum([1, 2, 3])`）。這讓呼叫
模型與 C ABI 都保持扁平，也符合 formatting 已採的 no-variadics 立場；`print` 保持是內建構造、不是使用者可定義的
variadic。

**閉包的捕獲規則與 `spawn` 相同：只捕獲 immutable 值與 channel，且以複製帶入。** 捕獲一個 **immutable** 值——一個
單純的 scalar,或一個 **non-POD** 值(一個 `list` / `map` / `str`、一個 `Ref`、或一個裝箱值)——是 **[not yet]**,
連同 closure 與函式值的其餘部分,種子都已不再建置；
捕獲一個 **`mut`** binding 是 **[not yet]**——先把它快照成 immutable binding（`snap := n`）。捕獲在語意上是
**複製**——捕獲的 channel 做 refcount++,而一個 **non-POD 的 immutable 值**是**被 retain 進閉包的 refcounted 環境**、
而非急切深拷貝,單純的 scalar 則直接複製——所以逃出定義 scope 的閉包帶著自己的捕獲、永不懸空。因為每個捕獲都是
immutable,retain 或 clone 都不可觀察。等價地說:

> 一個閉包就是一個 scope-owned struct，它的欄位就是它的捕獲。

所以複製、釋放、channel-refcount 全都是既有記憶體規則自然帶出來的、不用另外加；捕獲了具 send 能力的 channel 端就算一個
holder，所以活著的閉包會撐住該 channel 的 send 側（見 [Coroutines 與 Channels](coroutine.zh-TW.md) 的
send-coverage 不變式）。

「捕獲 immutable 值」不等於「不能用 `mut`」：閉包 body 內的**區域**變動不受限制——你只是不能變動**被捕獲**的狀態。

```text
base := load_cfg()                 # immutable
apply := fn(req: Request) -> Reply {
    mut acc := base                # 區域可變工作副本，以捕獲值為初值
    acc = merge(acc, req)          # 動的是 local，不是捕獲值——沒問題
    return build(acc)
}
```

兩個經典的閉包陷阱因此在結構上被排除。plain `for x in xs` 的變數是**每一輪一個全新的不可變 binding**（該元素的
一份 copy），而 capture 是複製值——所以捕獲它的閉包保有**自己這一輪的值**，沒有共享 loop 變數的 bug、也不需快照
（`for mut x` 這種就地形式是 `mut`，所以跟任何 `mut` 一樣不可捕獲——先快照）：

```text
for x in xs {
    spawn fn() { handle(x) }       # 每個 coroutine 拿到自己這一輪的值
}
```

而且因為捕獲永遠是 immutable copy，「捕獲的是變數還是值？」根本沒有可觀察的答案——被捕獲的值永不改變，這個
問題自然消失。
