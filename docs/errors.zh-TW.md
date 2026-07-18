# Zerg Null-safety 與錯誤處理（Null-safety & Errors）

兩層失敗——可回復的值與 abort——橋接它們的運算子（`?` `??` `?.` `!` `raise` `guard`）,以及如何依型別處理錯誤。
屬於 [語言參考](language.zh-TW.md) 的一部分。亦有 [English](errors.md) 版本。

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

**`??`——預設值。** `a ?? b`：`a` 有左值就用它，否則用 `b`（右值被丟棄）；短路、右結合可串接，適用任何 `Either`。

**`?.`——optional chain（只限 `T?`）。** `a?.b`：`a` 有值就取 `.b`，否則整串在原地短路成 `nil`（與 `?` 不同，
不會從函式 return）；用在任何非 `T?` 型別都是 compile error。

**`!`——force-unwrap（值 → abort）。** `x!` 拆出左值，否則 **raise** `UnwrapError`——刻意的「我確定它有值」逃生口，
是從值那層跨進 abort 的一種入口。（邏輯否定用關鍵字 `not`，所以 postfix
`!` 空出來給它。）

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

**自訂 error 型別。** 任何實作 **`Error`** spec（`message() -> str`、`unwrap() -> Err?`、`code() -> byte?`——見內建
spec）的型別都是 `Err`：它可放進 `Result` 的右側，**也**可被 `raise`，而 `guard` 會把它還原成 `Right(e)`、
message／cause／code 完整。單一錯誤用 `struct`、一個家族用 `enum`——同一個值服務兩層，由 bridge 轉換。

**Aborts。** 一次 abort——內建的（`OverflowError`、`DivideByZeroError`、`UnwrapError`、`MatchError`、`IndexError`、
`KeyError`、`AliasError`、`StackOverflowError`、`SendOnClosedError`、`DeadlockError`）或任何你 `raise` 的
`Err`——代表 **bug**，不是預期內的失敗。它**不可被當控制流攔截**：沒有 `try`/`catch`、不能檢視是「哪一種」abort、也不能回到
出錯處續算。語意上它是一次**會執行 scope 清理的 stack unwind**——從 raise 點到它停下之處，每一層 scope 都**先跑
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

**`guard`——把 abort 降級成值（abort → 值）。** `guard { … }` 執行一個區塊，並把其中任何 abort 具現化成 `Err`，
因此整個運算式恆為 **`Result[T]`**（`T` = 區塊的值型別）：正常結果 `v` 變成 `Left(v)`，攜帶 `err` 的 abort 變成
`Right(err)`。

```text
n := guard { parse_int(untrusted) } ?? 0    # 內部溢位變成 Right(err)；?? 再給預設值

fn read_config(s: str) -> Result[Config] {
    return guard { risky_parse(s) }         # 內部的 abort 降級成 Right(err)
}
```

`Result` **恆被壓平**：因為被 raise 的錯誤本身就是 `Err`，對一個已經產出 `Result[U]` 的區塊 `guard`，結果仍是
`Result[U]`——內部 abort 與回傳的 `Right(err)` 收斂成同一個 `Right(err)`。`guard` 只攔**當前 stack** 上的 abort；
在區塊內 `spawn` 出去的 coroutine 有自己的 stack，不受影響。

`guard` 是從 abort 那層回到值的唯一路徑，與 `raise`／`!` 這些入口對稱——一旦 `guard`，abort 就成了普通 `Result`，用
既有的 `?` / `??` / `match` 處理，沒有獨立的 handler、也沒有 `recover` 建構。它在 coroutine 裡**沒有特殊意義**：
用 `guard` 包住的 coroutine body 就只是一個產出 `Result[T]` 的函式，要回報就跟其他值一樣送進 channel。

## 依型別處理錯誤——`is`

兩層送出的是**同一個被抹除的 `Err`**——值那層的 `Right(err)` 與 `guard` 具現化的 abort 無從分辨——所以同一套機制就
能對兩者分派。要對**某一種**錯誤動作，就用 **`is`** 測它的型別（見 [型別測試](specs.zh-TW.md)）：

```text
match guard { work() } {
    Left(v)  -> use(v)
    Right(e) -> {
        if e is NotFound { rebuild() }          # 就具體型別分支
        else if e is Overflow { alert(e) }      # 內建 abort，被 guard 具現化
        else { report(e.message()) }            # 其餘——catch-all 必備
    }
}
```

`is` 只產出 `bool`，所以一個分支能用 **`Error` 介面**（`message` / `code` / `unwrap`）、但**碰不到具體型別自己的欄位**
——值已被抹除、永不重新建構。這裡可達的錯誤集合是**開放**的（任何 `raise`、任何內建 abort、任何 library `Err`），
所以 `is` 串永遠無法窮盡：**catch-all 必備**，未命中的錯誤會像任何未覆蓋的 `match` 一樣 abort（`MatchError`）。

這把錯誤處理依「你握不握有**封閉**集合」分成兩路。需要錯誤的**資料**時，就讓它保持具體——一個
**`Either[T, MyErrorEnum]`**（從不抹除），其 variant 由 `match` 以值讀出、帶 payload 與覆蓋警告。集合**開放**、或你只
認得少數幾種時，收下被抹除的 **`Result[T]`**、用 `is` 分派並留 catch-all。於是回傳型別本身就是契約：抹除的 `Result`
說「分支、用 `Error` 介面」，具體的 `Either` 說「這是我完整的錯誤分類，含資料」。
