# Zerg Null-safety 與錯誤處理（Null-safety & Errors）

兩層失敗——可回復的值與 abort——橋接它們的運算子（`?` `??` `?.` `!` `raise` `guard`）,以及如何依型別處理錯誤。
屬於 [語言參考](../language.zh-TW.md) 的一部分。亦有 [English](errors.md) 版本。

失敗分成**兩層**，兩層之間各只有一座橋。**可回復的失敗是值**——「不存在」與預期內的錯誤都是 sum type 的一般值，
而非魔法般的 null；這是你日常工作的那一層。**bug 則是一次 abort**——溢位、除以零、賭錯的 `!`、或一個明確的
`raise` 會 _raise_ 並 unwind stack（見下方 _Aborts_）；它們不出現在任何簽章裡，無法被檢視、也無法續算。兩層攜帶的是
**同一個 `Err`**（`Error` spec），因此在橋上乾淨地交會：**`raise`（與 `!`）把值升級成 abort，`guard` 把 abort 降級回值。**

值那一層是同一個 sum type；依慣例**左邊是值、右邊是要被傳遞的東西**：

- **`Either[X, Y]`**——一個 `X` 或一個 `Y`；兩側必須不同（`Either[T, T]` 會被拒），且若某注入可同時抵達兩側就是
  compile error（改用明確變體建構）。
- **`Result[T]`** = `Either[T, Err]`，其中 `Err` 是 `Error` spec（任何實作它的型別）。
- **`T?`** = `Either[T, nil]`；**`nil`** 是它的 placeholder 值。

**`?`——傳遞。** `x?` 拆出左值，或從所在函式**提早 return** 右值（early return 的語法糖），因此函式必須有相同的
右型別。`T?` 與 `Result` 之間沒有隱式橋接：先用 `opt.ok_or(err)` 或 `res.ok()` 轉換。

```text
fn load() -> Result[Config] {
    txt := read_file(path)?     # Result[str]；Err 會提早 return
    return parse(txt)           # parse -> Result[Config]
}
```

`?` 對每一種 carrier 都可用:外圍函式必須回答一個**右側相同**的 carrier,而右側原封不動地穿過去,所以
`Err` 在傳播後仍保有它的 kind。

**`??`——預設值。** `a ?? b`：`a` 有左值就用它，否則用 `b`（右值被丟棄）；短路、右結合可串接，適用任何 `Either`。

**`?.`——optional chain（只限 `T?`）。** `a?.b`：`a` 有值就取 `.b`，否則整串在原地短路成 `nil`（與 `?` 不同，
不會從函式 return）；用在任何非 `T?` 型別都是 compile error。若該欄位**本身就是 optional 就會壓平**——整串回答的
是那個欄位的型別，而不是巢狀的 `T??`（那不是這個語言寫得出來的型別）。

**`!`——force-unwrap（值 → abort）。** `x!` 拆出左值，否則對一個缺席的 optional **raise**——刻意的「我確定它有值」
逃生口，是從值那層跨進 abort 的一種入口。（邏輯否定用關鍵字 `not`，所以 postfix `!` 空出來給它。）

> **[not yet]** 作為獨立、可 `is` 測試的錯誤**種類**的 `UnwrapError` 尚未建置;今天該 abort 以一般訊息觸發、不在下方
> 六種分類之列。

```text
port := lookup("PORT") ?? 8080
name := env("NAME") ?? env("USER") ?? "anon"
addr := config?.server?.host ?? "localhost"
```

**`raise`——以任一 `Err` abort（值 → abort）。** `raise e` 把一個 `Err` 升級成攜帶它的 abort——是「值→abort」的
通用生產側入口，`!` 只是它的特例。**值那層**（簽章裡的 `Result` / `Either`）留給**預期、可回復**的失敗；`raise` 給
**不可回復**者——壞掉的 invariant、失敗的 assertion、一個「不可能發生」——因此不進任何簽章、只被 `guard` 攔下。
一個 **`raise e from cause`** 形式會把 `cause` 記成 `e` 的 `unwrap()`——一個 **nested** abort,把底層 `Err` 包進更高
層的一個、而不遺失它,餵的是每個 `Error` 都有的那條 cause chain;裸的 `raise e` 則原封不動攜帶 `e`。
`raise` **語句**帶有每一種 diverge 都有的**後綴 guard**——`raise e if c`,以及 `raise e from cause if c`
——它是 `if c { raise e }` 的糖,也正是格式化器的 `F401` 會把 block 形式改寫成的樣子。它只屬於語句形式:
`??` 右側的 `raise` 不接尾隨 `if`,否則那個 guard 會讀成 coalesce 的。

**內建的錯誤分類是一棵樹。** 這個階段提供**固定的六種**錯誤,而其中有些**是另一些的一種**:

```text
ValueError            這個值不是這裡能接受的
├── OverflowError     ……因為它超出了範圍          int(u)、byte(300)、a + b
└── EncodingError     ……因為它不是合法的文字      str 橋接到不合法的 UTF-8
IOError               外界不配合
IndexError            索引落在容器外
KeyError              沒有任何 map 持有這個 key
```

**`is` 會觀察這棵樹。** `e is ValueError` 對一個 `OverflowError` 為真,所以處理者可以**粗略地接**(「值不對,我不在
乎為什麼」)或**精確地接**,而粗略的那個不是謊話。Zerg 沒有繼承,這也不是繼承:這棵樹是內建 kind 之間一個固定的關
係,而且**只往上讀**——每一個 `OverflowError` 都是 `ValueError`,而手寫 raise 出來的 `ValueError` 不是
`OverflowError`。

**cause 不是 parent,而 `is` 看不見 cause。** `raise X from y` 記的是「什麼導致了 `X`」;要問那個,用的是
`unwrap()`。這兩個關係是刻意分開的:分類的連結是 **is-a**,而 cause 是**底下的另一個錯誤**,一個同時回答兩者的述詞
等於兩者都分不出來——`raise IOError("outer") from ValueError("inner")` 會變得跟一個本來就是 `ValueError` 的錯誤無從
區別。

你**從中挑選**;**自訂**錯誤型別(一個實作 **`Error`** spec
——`message() -> str`、`unwrap() -> Err?`、`code() -> byte?`,見 [內建 spec](../core/specs.zh-TW.md)——的 `struct` / `enum`)
**尚未支援**。每一種都是完整的 `Err`:帶訊息建構(`raise ValueError("bad input")`)、放進 `Result` 右側、**也**可被
`raise`、讀 `err.message()`、用 `err is ValueError` 測試,而 `guard` 會把它還原成 `Right(err)`、message／cause／code
完整。runtime **自身的內建失敗也 raise 對應的種類**,使 library 與 runtime 的錯誤共用一套詞彙:整數解析失敗是
`ValueError`(超出範圍→`OverflowError`)、一次 checked 收窄轉換是 `OverflowError`、I/O 失敗是 `IOError`、對無效
UTF-8 的 `str` 橋接是 `EncodingError`、越界索引是 `IndexError`、缺少的 `map` 鍵是 `KeyError`。`Result` / `Either`
攜帶它們,`?` / `??` / `guard` 原封不動地穿引;對具體 `Either[T, Kind]` 的 `match` 則分辨其種類。abort 契約本身——
寫到 stderr 的訊息、exit 狀態 1、`Kind: message` 那一行——見 [Conformance](../conformance.zh-TW.md)。

**Aborts。** 一次 abort——一個內建 runtime fault 或任何你 `raise` 的 `Err`——代表 **bug**，不是預期內的失敗。本章
用到的 fault 名稱裡,今天有十個具現化成可 `is` 測試的**種類**:`ValueError`、`OverflowError`、`IOError`、
`EncodingError`、`IndexError`、`KeyError`、`DivideByZeroError`，再加上並行那章指名的三個——`SendOnClosedError`、
`DeadlockError` 與 `StopIteration`。其餘在語言表面還**叫不出名字**:`UnwrapError`、`MatchError` 與 `AliasError`
是 **[not yet]**——寫 `err is AliasError` 在**兩個編譯器**裡都是一則乾淨、指名的編譯錯誤（那個名字不在這十個之
列）——而它們的 abort 也不帶獨立具現化種類、只有一般訊息。

**`StopIteration` 可測試，卻無法建構。** 它是唯一一個程式可以放在 `is` 右邊、卻**不可以**呼叫的名字:
`raise StopIteration("…")` 在**兩個編譯器**裡都是編譯錯誤。channel 的乾淨關閉以它作為**種類**、而非訊息字串
攜帶——這正是 consumer 不必比對字串就能分辨乾淨結束與崩潰的原因;一個能 raise 這個哨兵的 sender 恰恰會破壞這件事
（見 [Concurrency](coroutine.zh-TW.md)）。`StackOverflowError` 則是 **[deviation]**（見下）。
abort **不可被當控制流攔截**：沒有 `try`/`catch`、不能檢視是「哪一種」abort、也不能回到出錯處續算。語意上它是一次
**會執行 scope 清理的 stack unwind**——從 raise 點到它停下之處，每一層 scope 都**先跑
它的 `defer`**、再按序釋放，其 `Ref` 值（channel 與 `Ref[T]`）的 refcount 遞減，與正常的 scope 結束完全相同；絕不是
裸的 `abort()`。unwind 抵達某條 stack
頂端就讓那條 stack crash：主 stack 結束整個程式，coroutine 的 stack 只結束該 coroutine（`spawn` 是
fire-and-forget——見 [Concurrency](coroutine.zh-TW.md)）。

一次在**另一個 abort 已經在 unwind 時**才觸發的 abort——某個 `defer`、或某個 `Ref` 的 `drop` 自己 abort——**絕不
放棄那次 unwind**:其餘的 `defer` 照跑,所以**清理永不被略過**。兩個錯誤以**與 `raise e from cause` 相同的 nesting**
合併——較晚的 abort 往外傳、把還在飛的那個記成它的 `unwrap()` cause——所以兩者皆不遺失、consumer 讀得到整條鏈。
沒有哪個錯誤會無聲勝出,也沒有另一個 _suppressed_ 槽要去查。

一個 **`StackOverflowError`** 是 Zerg 自己的安全網、不是 OS 的：runtime **擁有每一條 stack**——主 stack 與每條
coroutine 的——並**自己檢查呼叫深度**，在一個呼叫將超出 stack 的當下就 raise 這個 abort（一次會跑 `defer` 的乾淨
unwind），所以失控的遞迴**永不**變成 C 的 stack smash。Zerg **不做 tail-call 優化**——`for` 才是迴圈、有界 stack
就夠——因此無界遞迴是一個確定的 `StackOverflowError`、絕不是無聲的卡死。

> **[deviation]** bootstrap 尚未擁有或深度檢查 stack;stack 溢位是一次無法回復的 `SIGSEGV` / stack-smash、會終止
> 行程而**不跑** `defer`,而非乾淨的 `StackOverflowError` unwind（見 [Conformance](../conformance.zh-TW.md) 的
> runtime-abort deviation）。意圖中的安全網成立;這個階段尚未建置。

一個 **`DeadlockError`**——每個 coroutine 都阻塞、無法再前進——現在已是規格所要求的那次乾淨 abort:它 unwind、跑
pending `defer`，`guard` 也攔得住。它在 `main` 的 coroutine 上 raise，而且**每一次**偵測都會重新 raise、不是只有
一次，所以一個原封不動重試的 `guard` 會把 deadlock 變成 livelock;兩者為何都是刻意的，見
[Concurrency](coroutine.zh-TW.md)。

**`guard`——把 abort 降級成值（abort → 值）。** `guard { … }` 執行一個區塊，並把其中任何 abort 具現化成 `Err`，
因此整個運算式恆為 **`Result[T]`**（`T` = 區塊的值型別）：正常結果 `v` 變成 `Left(v)`，攜帶 `err` 的 abort 變成
`Right(err)`。

```text
n := guard { parse_int(untrusted) } ?? 0    # 內部溢位變成 Right(err)；?? 再給預設值

fn read_config(s: str) -> Result[Config] {
    return guard { risky_parse(s) }         # 內部的 abort 降級成 Right(err)
}
```

> **被 guard 的區塊有兩條限制，而且都是大聲的。** 從區塊裡 `return`、`break` 或 `continue` > **離開**會被拒絕：handler 在區塊前推入、區塊後彈出，中途跳走會把 frame 帶走、
> 把 handler 留在上面。區塊的值若是**在區塊內綁定的名字**也被拒絕，因為 C 規定在 `setjmp`
> 與落點之間被修改的自動變數，除非是 `volatile` 否則其值不確定（C99 7.13.2.1）——把區塊的
> 值寫成一次呼叫或一個常值，那本來也是日常寫法。
>
> **[not yet]** 上面 `read_config` 那個範例需要 `Result[T]` 能在**簽章**裡存活，而出貨的
> `zerg` 會把它抹掉——guard 本身沒問題，是回傳型別不行。在那件事落地前，把 `Result` 直接
> 交給呼叫端的 `??` / `match`，而不是回傳它。

`Result` **恆被壓平**：因為被 raise 的錯誤本身就是 `Err`，對一個已經產出 `Result[U]` 的區塊 `guard`，結果仍是
`Result[U]`——內部 abort 與回傳的 `Right(err)` 收斂成同一個 `Right(err)`。`guard` 只攔**當前 stack** 上的 abort；
在區塊內 `spawn` 出去的 coroutine 有自己的 stack，不受影響。

`guard` 是從 abort 那層回到值的唯一路徑，與 `raise`／`!` 這些入口對稱——一旦 `guard`，abort 就成了普通 `Result`，用
既有的 `?` / `??` / `match` 處理，沒有獨立的 handler、也沒有 `recover` 建構。它在 coroutine 裡**沒有特殊意義**：
用 `guard` 包住的 coroutine body 就只是一個產出 `Result[T]` 的函式，要回報就跟其他值一樣送進 channel。

## 依型別處理錯誤——`is`

兩層送出的是**同一個被抹除的 `Err`**——值那層的 `Right(err)` 與 `guard` 具現化的 abort 無從分辨——所以同一套機制就
能對兩者分派。要對**某一種**錯誤動作，就用 **`is`** 測它的型別（見 [型別測試](../core/specs.zh-TW.md)）：

```text
match guard { work() } {
    Left(v)  => use(v)
    Right(e) => {
        if e is IOError { rebuild() }           # 就分類種類分支
        else if e is OverflowError { alert(e) }  # 內建 abort，被 guard 具現化
        else { report(e.message()) }            # 其餘——catch-all 必備
    }
}
```

`is` 只產出 `bool`，所以一個分支能用 **`Error` 介面**（`message` / `code` / `unwrap`）、但**碰不到具體型別自己的欄位**
——值已被抹除、永不重新建構。這個階段 `is` 實作**於錯誤分類**——那六種內建錯誤,以及任何被
`guard` 具現化的內建 abort;對**非錯誤**型別的一般存在性測試 `x is T` 是 **[not yet]**。這裡可達的錯誤集合在覆蓋上被
視為**開放**,所以 `is` 串永遠無法窮盡:**catch-all 必備**。未命中的錯誤會像任何未覆蓋的 `match` 一樣 abort——但
`MatchError` 是 **[not yet]** 的具現化種類,且因為最後一個 `match` arm 一律無條件,compiler 今天永不 emit 一個(catch-all
的要求是靜態規則、不是 runtime `MatchError`)。

這把錯誤處理依「你握不握有**封閉**集合」分成兩路。需要以值決定錯誤**種類**時,就讓它保持具體——一個
**`Either[T, ValueError]`**(從不抹除),其右側由 `match` 以值讀出。只認得少數幾種時,收下被抹除的 **`Result[T]`**、用
`is` 分派並留 catch-all。於是回傳型別本身就是契約:抹除的 `Result` 說「分支、用 `Error` 介面」,具體的 `Either` 說
「這是我確切的錯誤種類」。(把多種錯誤聚成一個封閉 sum 的自訂 error `enum`,與上面的自訂 error 型別一同延後。)
