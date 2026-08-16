# Zerg 函式與閉包（Functions & Closures）

函式作為一等值、它的型別追蹤什麼與不追蹤什麼、預設參數與具名引數,以及閉包捕獲。屬於
[語言參考](../language.zh-TW.md) 的一部分。亦有 [English](functions.md) 版本。

函式是**一等值（first-class value）**：它有型別，可當引數傳遞、可回傳、可存進欄位、可綁定到變數。這**跨模組**也
成立——透過另一個模組指名的函式就是一個普通的值:`f := other.helper` 綁定它,接著 `f(x)` 呼叫它,與本地函式完全一樣。
一個 **generic** 函式
**在實例化之前不是一等值**:未實例化的 generic 名字本身不是值——唯有它的型別引數在使用點被固定後才成為值。
函式型別寫成 `fn(P...) -> R`；參數的 `mut &` 是**型別的一部分**，所以 `fn(mut &int) -> bool` 與 `fn(int) -> bool` 是不同型別
（兩者 calling convention 不同——就地 by-ref vs 複製）。可見性**不**屬於型別：`pub` 匯出的是 top-level 函式的
**名字**，永不隨值移動，對匿名函式也無意義。

匿名函式**可以省略參數型別,連帶省略回傳型別**——它們來自這個匿名函式被檢查時所對照的函式型別,也就是
[carve-out (c)](../core/types.zh-TW.md)。每一個有型別的位置都供得出一個:宣告過型別的 binding、引數、
`return`、struct 欄位、參數的預設值。

```zerg
    apply(fn (x) { return x + 1 }, 41)          # x 是 int,答案是 int
    g: fn (int) -> int = fn (x) { return x * 2 }
```

寫出來的型別**贏**——`fn (x: str)` 放在 `fn (int) -> …` 的位置是一個指名兩個型別的型別錯誤,而不是被悄悄
覆蓋掉的註記。而一個**遇不到**這種位置的 closure 無處可取,那是錯誤、不是猜測:`f := fn (x) { … }` 會報
_E385 the closure parameter `x` has no type, and this position gives it none_。

> **[not yet]** 這個值停在模組邊界上。透過另一個模組指名的函式只是一個**呼叫目標**：`text.make(1)` 編得過，而
> 寫在它上一行的 `f := text.make` 會報 _E388 module `text` has no `make`_——解析限定名字的成員查找寫在呼叫那條
> 路上，裸名字那條路從來沒學會它。所以跨模組的函式可以被呼叫，卻不能被綁定、傳遞或存起來。
>
> **[not yet]** 共用 indexed-callee 形狀的兩個形式仍未建置:透過容器裡的函式**值**呼叫 `fs[0](x)`,以及 optional
> method call `p?.m(…)`。第三個已經離開這個語言:使用點的顯式型別引數——`id[int](7)`——不再是一個形式,因為
> postfix 的中括號一律是索引（見[文法](../surface/grammar.zh-TW.md)）,而且它會被指名拒絕——_E275 `id[int](…)`
> writes a call's type arguments, and a postfix `[ … ]` is an index_。型別引數從引數型別推論,是今天實例化一個
> generic 的唯一途徑。
>
> **[not yet]** `mut &` 的區分在語言裡是真的，卻寫不出來。帶著它的函式**型別**會被讀完、然後被指名拒絕：
> `f: fn(mut &int) = bump` 報 _E286 NotImplemented: a `mut &` parameter in a function type_，並帶上那個前綴
> 所在的位置。拒絕的理由就是值那一側早已說過的同一條：在這個編譯器裡，被持有的函式是一個裸指標，而呼叫端是從
> 被呼叫者的**名字**讀出 `mut &` 的，值沒有名字（`E469`）——所以 `fn(mut &int) -> bool` 沒有寫法，兩個型別的
> 相異只留在紙上。

**一個函式的型別就是它的輸入／輸出契約，僅此而已。** 它揭露參數——`mut &` 標出唯一的「引數層 effect」:寫回呼叫端的
可變參考——以及結果，可回復的失敗會以 `Result` / `Either` 顯示在那裡。它**不追蹤任何其他 effect**：一個函式有沒有做
I/O、讀 ambient 狀態（clock、randomness、`env`）、或可能 **abort**，都不出現在簽章裡。I/O 只能透過檔案的 `import`
看到；而 abort 幾乎在每個運算式都可能發生（一次溢位、一個壞 index），標它就是每條簽章上的噪音。「引數改動」與
「可回復錯誤」以外的 effect **刻意不追蹤**——Zerg 在這裡是 procedural-first——而非漏寫。

持有函式的 binding 其可變性就是一般的 per-instance 軸——`mut f := …` 可 rebind、`f := …` 不可——與上述一切正交。

## 命名：一個屬性，與它的兩種寫入

這是**慣例**，不是編譯器強制的規則——除了 `GRAMMAR` 之外，語言對識別字沒有意見。之所以寫下來，是因為它在這棵樹裡
處處成立卻從未被記載，而一個查不到的慣例，下一位貢獻者只會不小心破壞它。

> **`xxx` 讀、`set_xxx` 寫、`del_xxx` 移除。**

讓它可判定的測試，也是它之所以是一條短規則而非一種品味的理由：

> **這三件套適用於「屬性」——有 getter，而且名字取自那個東西、不是取自那個動作。**

這句話的兩半都在做事。

- **`os.env` 有 getter**，而且是名詞：`env(key)` 命名的是它回答的東西。所以環境是一個屬性，它的寫入就是 `set_env`
  與 `del_env`——不是 `put_env`、不是 `unset_env`、也不是 `env_set`。
- **`log` 刻意沒有 getter** 來取得已安裝的 logger。`current()` 曾被提出並被拒絕：它讀起來像是對一個承受不起的 cell
  做飛行中重設。沒有 getter 就沒有屬性，所以 `install` 命名的是一個**動作**，不會變成 `set_logger`。
- **`atomic.load` / `atomic.store` 命名的是動作，不是東西。** 這裡 cell 的內容沒有名字——沒有 `atomic.value`——所以
  沒有 `xxx` 可以讓 `set_` 加在前面，這一對也就沿用了各語言 atomic 共通的詞彙。名字是動詞的 getter 不是屬性存取器。

**不在涵蓋範圍內**的，而且是刻意的：回傳修改後副本、而非寫入 receiver 的 **builder** 方法（`log.Logger.level(l)`、
`cli.Command.version(v)`）以它所填的欄位命名，不加前綴。它不是 setter——receiver 沒有變——把它叫成 `set_level`
等於宣稱了一個並未發生的改動。

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

  > **[not yet]** **讀取前面參數**的預設值——`fn g(a: int, b: int = a * 2)`——是唯一沒做出來的形狀。預設值在
  > **呼叫端**被展開，而被呼叫者的參數名字在那裡不在 scope 內，所以那次呼叫會報 _E372 undefined name `a`_，
  > 而不是求值 `a * 2`。其餘每種預設值都如規格 lower，純常數與計算出來的運算式一樣：`b: int = 1 + 2`、
  > `b: int = side()` 與 `greeting: str = "a" + "b"` 都在呼叫處、每次求值。
  >
  > **[not yet]** **匿名**函式參數上的預設值沒做。`GRAMMAR` 推導得出它——
  > `closure-param ::= ( 'mut' '&' )? identifier ( ':' type )? ( '=' expr )?`，尾巴的 `( '=' expr )?` 與宣告
  > 裡的參數同一條——而 `f := fn (x: int = 5) -> int { … }` 報 _E285 NotImplemented: a default on the closure
  > parameter `x`_，並帶上位置。理由就是上面那一條：預設值是在**呼叫端**、從被呼叫者的**宣告**展開出來的，而
  > 閉包是透過一個**值**取得的，那個值沒有帶著任何宣告可讀。每次呼叫都把引數傳進去。

- 一個**具名引數**以參數名字傳入（`loud: true`）——這正是讓你能**跳過中間的預設參數**的關鍵。規則就是慣例那套：
  positional 引數由左往右填、任何參數都可改用具名、有預設的可省略，而且**一旦具名，其後全部都要具名**（具名之後
  不能再回到 positional）。

  > **[not yet]** 具名引數完全沒做。`greet("Sam", loud: true)` 報 _E223 NotImplemented: the named argument
  > `loud:` — this compiler binds arguments by position only_，機制的其餘部分也跟著沒有：沒有辦法跳過中間那個
  > 有預設的參數，「一旦具名，其後全部都要具名」這條規則也沒有東西可管。一次呼叫由左往右填它的參數，有預設的
  > 參數只能從引數列的**尾端**省略。

因為參數可以用名字挑選，**名字就成了函式契約的一部分**——改名會弄壞呼叫者，就跟改型別一樣。但預設與名字都不進
_型別_：`fn(str, str, bool) -> str` 是型別，預設住在宣告裡、名字住在參數列——與「型別就是輸入／輸出契約、僅此
而已」一致。兩者都是**呼叫端 sugar**：跨越 C ABI（見 [FFI](../runtime/ffi.zh-TW.md)）時，export 的函式全 positional、無預設。

**variadic** 參數刻意**不提供**——改為顯式傳 `list[T]`（`sum(xs: list[int])`，呼叫 `sum([1, 2, 3])`）。這讓呼叫
模型與 C ABI 都保持扁平，也符合 formatting 已採的 no-variadics 立場；`print` 保持是內建構造、不是使用者可定義的
variadic。

**閉包的捕獲規則與 `spawn` 相同：只捕獲 immutable 值與 channel，且以複製帶入。** 捕獲的名字會變成閉包隨身帶著的一
個 per-site 環境。捕獲一個 **`mut`** binding 與其他捕獲一樣,拿到的是**寫下閉包那一刻**該 binding 持有的值——之後
對那個 binding 的寫入,透過閉包看不到;那正是「複製帶入」的意思,也正是這兩種情況不需要兩條規則的原因。複製是
**語意**、不總是機制:單純的 scalar 確實被複製,而 channel 做 refcount++、non-POD 值(`list` / `map` / `str`、
`Ref`、裝箱值)則是**被 retain** 進那個環境、而非急切深拷貝。無論走哪一條,逃出定義 scope 的閉包都帶著自己的捕獲、
永不懸空。

那個環境是 **refcounted** 的,不是 scope-owned——這是上面那條複製規則唯一由實作、而非由 scope 來實現的地方。閉包
可能活得比造出它的 scope 久,所以 fn value 算它環境的一個 holder,最後一個 holder 釋放它,並依宣告的逆序拆解捕獲,
與其他任何聚合體一樣。被當成值持有的具名函式根本沒有環境,對這條規則零成本。

因為每個捕獲都是 immutable,retain 或 clone 都不可觀察。等價地說:

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
（`for mut x` 這種就地形式是 `mut`，而 `mut` 也一樣是複製帶入——拿到的是寫下閉包那一刻的值）：

```text
for x in xs {
    work := fn () { handle(x) }    # 每個 closure 持有自己這一輪的值
    work()
}
```

> **[not yet]** 這個迴圈的 coroutine 寫法用不了。closure **literal** 不在 `spawn` 的三種 callee 形式之列——
> `spawn fn () { … }()` 是 _E222 NotImplemented: calling fn-expr_——而對上面那個**具名** closure 寫
> `spawn work()` 是 _E744_,帶著位置:這兩個關鍵字都降階成一個 C thunk,而 thunk 的 body 指名的是一個符號,
> 函式值沒有符號。(它以前會把 `zg_work()` 寫進那個 thunk 裡,建置死在 `cc`——那正是總則明文禁止的結局。)
> 行得通的寫法是 `spawn handle(x)`,它在 `spawn` 當下對引數取快照,以另一條路徑拿到同樣的逐輪值。見
> [Coroutines 與 Channels](coroutine.zh-TW.md)。
