# Zerg 語言參考（Language Reference）

[README](../README.zh-TW.md) 中設計原則背後的詳細語意。亦有 [English](language.md) 版本。

## 原始型別（Primitive Types）

一組精簡且固定的集合——**沒有固定寬度的整數階梯**（`i8`、`i16`……都不存在）：

| 型別    | 說明                                                       |
| ------- | ---------------------------------------------------------- |
| `bool`  | `true` / `false`                                           |
| `byte`  | unsigned 8-bit——Zerg 的 char                               |
| `rune`  | 單一個有效的 Unicode code point                            |
| `int`   | signed 64-bit 整數                                         |
| `float` | IEEE-754 double（f64）                                     |
| `str`   | immutable、null-terminated 的 Unicode（不含 embedded NUL） |
| `nil`   | `T?` 的 placeholder 值                                     |

- **整數溢位與除以零會 raise**（`OverflowError`、`DivideByZeroError`）——這是一次 **abort**、不是值（見 Null-safety
  與錯誤處理）；`int`/`byte`/`rune` 絕不環繞。
- **`float` 依 IEEE-754：** 溢位 → `±Inf`、無效運算 → `NaN`，兩者都不 raise；`NaN` 與任何值（含自己）都不相等。
- **`str` 以 `rune` 迭代、不可索引**——要原始位元組就轉 `list[byte]`（可能含 NUL 的二進位也用它，`str` 永不含 NUL）。

## 型別（Types）

宣告你自己的**積型別**（`struct`）與**和型別**（`enum`），兩者都可對 `[...]` 泛型化。

**可見性（`pub`）**——每個宣告（型別、欄位、函式）**預設 private，只在自己的 module 內可見**；在前面加 `pub`
才會匯出、供他處使用。mutability 是另一條獨立的軸、**不**在這裡宣告：它屬於**實例（instance）**（也就是
binding；見 Values & Memory），絕不屬於欄位或型別。

```text
struct Node {
    value: int,
    next:  Node?,           # 自我參照——自動 boxing（見 Values & Memory）
}

enum Either[X, Y] {         # 泛型 sum type
    Left(X),
    Right(Y),
}
```

`Either`、`Result[T]`、`T?` 並不特殊——它們是建立在 `enum` 之上的普通 stdlib 型別（見 Null-safety）。

## Spec 與 Generics（Specs & Generics）

generic 的型別參數以 **`spec`** 為 bound——`spec` 是「型別必須提供什麼」的命名介面。滿足是 **nominal**：型別必須
**明確宣告**它實作某 `spec`。

- **空的 `spec`** 被所有型別滿足——這就是「無約束 generic」的表達方式。
- **`Object`** 是內建的頂層 `spec`：每個型別（primitive 或 custom）都應支援的最小 spec 集合（細節待定）。
- `spec` 也可**當型別用**，不只是 bound：spec-typed 的值可持有任何實作它的型別——heap-boxed、single-owner、
  scope-owned，並以**動態**方式 dispatch。

concrete bound 的 generic 會在產出的 C 裡 monomorphize；把 `spec` 當型別用是唯一改用 dynamic dispatch 之處。
`Err` 就是 `Error` spec，因此任何實作 `Error` 的型別都能當 `Result` 的錯誤側（見 Null-safety）。

## 型別轉換（Type Casts）

**預設**沒有型別會隱式轉換——`int` 不是 `bool`；要轉就用 constructor 風格呼叫（`bool(8)`、`int(c)`）。primitive
之間的轉換由**編譯器內建**；使用者型別不能對 primitive 加 auto-cast。

**使用者型別**可 opt-in 一個對另一型別的 **auto-cast**，靠兩條規則保持可判定：

- **只做單步**——絕不串接（`X → Y`、`Y → Z` ⇏ `X → Z`）；單步、單一明確目標，不會出現多路徑歧義。
- **只在目標型別明確處觸發**——有型別標註的 binding（`x: X = y`）、`return`、或有型別的參數；不會在推斷型別的
  `:=` 上發生。

這正是讓一個值、一個 `Err` 或 `nil` 能在 typed binding 或 return 處直接注入 `Either`、無需明確包裝的機制
（見 Null-safety）。

## 值與記憶體（Values & Memory）

無 garbage collector、無 pointer 語法。每個值都是 **scope-owned**（離開 scope 即釋放），且預設以**值傳遞**。
copy-by-value 是語意；編譯器會在安全時省略複製：

- **單一執行流程**——immutable 的值可隱形地改以 by-ref 傳遞；mutable 的則 fallback 為複製。
- **跨 coroutine**——一律複製：無共享可變狀態、無 data race；要把修改反映回去是呼叫端的責任（例如透過 channel）。
- **取值 / 回傳**——unwrap（`?`、`!`）、`match`、`return` 都是複製出來；來源永不失效。move 只是來源之後死掉時的
  隱形最佳化。

遞迴與自我參照型別不需要 pointer——直接宣告欄位（例如 `Node?` → `Node`），編譯器**自動**插入 heap 間接；這類值
一樣是 scope-owned 且 copy-by-value。

mutability 屬於**實例（instance）**——也就是 binding——不是型別或任何欄位：`mut x := …` 讓整個建構出的實例
可變（每個欄位），`x := …` 則保持不可變；欄位只帶可見性（`pub` 或 private）。Zerg 沒有通用 reference；程式之間
只能透過以下方式共享儲存：

- **Mutable-ref 參數**（`mut` 參數）——唯一「語意上真的 by-ref」：被呼叫端就地改呼叫端（`mut`）的變數。它受限於
  這次呼叫——值的位置（欄位、`return`、送 channel）都是**複製當下的值**，只能往下傳給另一個 `mut` 參數，且不能跨
  `spawn`。同一塊儲存不得在一次呼叫裡當兩次 `mut` 參數：靜態別名（`f(x, x)`）是 compile error；執行期索引別名歸
  呼叫端。
- **Channel**——在 coroutine 之間以 by ref 共享，僅用於通訊。

channel 是 scope-owning 的**唯一例外**：因天生跨 coroutine 共享，runtime 對它 **reference-count**，在最後一個
持有者的 scope 退出時釋放——其餘一切純 scope-owned、無 GC/refcount。複製一個值時，會對它（遞迴）包含的每個
channel 做 refcount++、深拷貝其餘部分；channel 永遠共享、絕不被複製。

## 並行（Concurrency）

Zerg 的並行**只有 coroutine 與 channel**：`spawn`（Go 的 `go`）跑在 **M:N scheduler** 上，fire-and-forget、無
join/handle，只捕獲 **immutable 值與 channel**。channel 是唯一 reference-counted 的 by-ref 管道——payload 複製、在
最後一個 sender 離場時**自動 close**、以 **`Result[T]`** 接收（`Right` = 已關，攜帶崩潰 `Err` 或 `Closed` 哨兵）、
並用 **`select`** 多路等待。

完整模型——buffering、receive/close 語意、directional 端、`select`、deadlock——見
**[Coroutines 與 Channels](coroutine.zh-TW.md)** 參考文件。

## Null-safety 與錯誤處理

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
是從值那層跨進 abort 的一種入口（`x!` 就是 unwrap-or-`raise UnwrapError`）。（邏輯否定用關鍵字 `not`，所以 postfix
`!` 空出來給它。）

```text
port := lookup("PORT") ?? 8080
name := env("NAME") ?? env("USER") ?? "anon"
addr := config?.server?.host ?? "localhost"
```

**`raise`——以任一 `Err` abort（值 → abort）。** `raise e` 把一個 `Err` 升級成攜帶它的 abort——是「值→abort」的
通用生產側入口，`!` 只是它的特例。**值那層**（簽章裡的 `Result` / `Either`）留給**預期、可回復**的失敗；`raise` 給
**不可回復**者——壞掉的 invariant、失敗的 assertion、一個「不可能發生」——因此不進任何簽章、只被 `guard` 攔下。

**自訂 error 型別。** 任何實作 **`Error`** spec（`message() -> str`、`unwrap() -> Err?`、`code() -> byte?`——見內建
spec）的型別都是 `Err`：它可放進 `Result` 的右側，**也**可被 `raise`，而 `guard` 會把它還原成 `Right(e)`、
message／cause／code 完整。單一錯誤用 `struct`、一個家族用 `enum`——同一個值服務兩層，由 bridge 轉換。

**Aborts。** 一次 abort——`OverflowError`、`DivideByZeroError`、`UnwrapError`、或任何你 `raise` 的 `Err`——代表
**bug**，不是預期內的失敗。它**不可被當控制流攔截**：沒有 `try`/`catch`、不能檢視是「哪一種」abort、也不能回到
出錯處續算。語意上它是一次**會執行 scope 清理的 stack unwind**——從 raise 點到它停下之處，每一層 scope 都按序
釋放、所持 channel 的 refcount 遞減，與正常的 scope 結束完全相同；絕不是裸的 `abort()`。unwind 抵達某條 stack
頂端就讓那條 stack crash：主 stack 結束整個程式，coroutine 的 stack 只結束該 coroutine（`spawn` 是
fire-and-forget——見 Concurrency）。

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

`guard` 是從 abort 那層回到值的唯一路徑，與 `!` 這個唯一入口對稱——一旦 `guard`，abort 就成了普通 `Result`，用
既有的 `?` / `??` / `match` 處理，沒有獨立的 handler、也沒有 `recover` 建構。它在 coroutine 裡**沒有特殊意義**：
用 `guard` 包住的 coroutine body 就只是一個產出 `Result[T]` 的函式，要回報就跟其他值一樣送進 channel。
