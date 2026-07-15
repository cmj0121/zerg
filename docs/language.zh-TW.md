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
binding；見 Values & Memory），絕不屬於欄位或型別。module 與 package 是什麼，以及可見性、coherence、entry point
如何跨越它們，見 [Modules, Packages & Programs](package.zh-TW.md)。

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

行為分成**兩層**。型別可定義 **inherent method**——自有行為，只有握著具體型別時才能用。而**抽象**一律透過
**`spec`**：一個具名的行為介面（只含 method 簽名、**永不含 field**）。滿足是 **nominal**：型別必須**明確宣告**它
實作某 `spec`，且每組 **(型別, spec) 只有一個正規 impl**。

`spec` 是抽象行為的**唯一**機制，因此它扮演三個角色——泛型參數的 **bound**、型別所 **conform** 的介面、以及
（見下）**當成型別本身**。內建行為也都是 spec、不是編譯器魔法：`Err` 就是 `Error` spec，相等、排序、雜湊、迭代、
以及 opt-in 的 cast 都是普通 stdlib spec。型別的 inherent method 不必隸屬任何 spec；**唯有 spec 所保證的，才可被抽象**。

**spec bound 就是泛型型別的完整介面。** 在泛型於 `T` 的程式碼裡，對一個 `T` 值唯一能用的操作，就是它 spec bound
所宣告的 method——它的欄位與任何 inherent method 都不可見。因此：

- **空的 `spec`** 是合法的 bound、被所有型別滿足，但它保證**零**行為：這種 `T` 只有 memory model 給的**結構能力**
  ——copy 它、`del` 它、當參數傳、存起來、送進 channel——連一個 method 都沒有。
- **`Object`** 是頂層 `spec`，被每個型別**自動實作**。它提供一組最小、**auto-derived** 的 method——`equal`、`copy`、
  `debug`……——由結構逐欄位自動生成（含 channel 則 refcount++，與 copy 規則一致）。型別可**明確覆寫**其中任何一個
  （例如不計順序的 `equal`），否則沿用衍生版本。因為每個型別都實作 `Object`，`T: Object` 這個 bound **從不縮小**
  可接受的型別集——它只是解鎖那些 method。

`spec` 也可**當型別用**，不只是 bound：spec-typed 的值可持有任何實作它的型別——heap-boxed、single-owner、
scope-owned，並**動態 dispatch**（實際要跑哪個 method，在執行期依值的真實型別決定）。這是**單向的**——一旦 boxed，
具體型別就被隱藏、無法還原（不能 downcast）。

concrete bound 的 generic 會在產出的 C 裡 **monomorphize**——編譯器為每個具體型別各生成一份特化版本——而把 `spec`
當型別用是唯一改用 dynamic dispatch 之處。

一個**實作**（型別滿足某 spec）本身不帶可見性標記：coherence 要求一組 `(型別, spec)` 到處都解析到同一個實作，
因此實作既不能被藏、也不能被複製——它的作用範圍恰好是「型別與 spec 同時可見之處」。實作是為**具體或泛型型別**寫的
（`List[T]` 可以實作 `Iterator`）；以 bound 為條件、涵蓋「所有滿足某 spec 的型別」的 blanket 實作**不提供**，以保持
解析可判定。唯一「所有型別都有」的情況是 `Object`，由編譯器 auto-derive、而非使用者手寫。

因為 spec 是 nominal，兩個各自獨立宣告的 spec 可能撞用同一個 method 名。型別仍可同時實作兩者、並各別當其一使用——
歧義只存在於「同一個值必須**同時**滿足兩者」之處（`T: X + Y` 的 bound、或型別為 `X + Y` 的值）。Zerg 在編譯期**拒絕
這個組合**，而不引入 fully-qualified 呼叫語法來消歧；要讓一個 method 被多個 spec 共用，就讓它們**源自同一個共享
spec**。spec 可跨 package 邊界實作到什麼程度、以及 coherence 如何維持全域唯一，見
[Modules, Packages & Programs](package.zh-TW.md)。

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

### 重新宣告與遮蔽（Re-declaration & shadowing）

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

### `del`——顯式提早釋放

`del name` 在 scope 結束前**撤銷該名字對其儲存的存取權**。釋放儲存只是一個*後果*：唯有被撤銷的正是**擁有權**
存取、且沒有其他 holder 時才發生；否則 `del` 只是提早結束「這個名字（或這次借用）」的存取，儲存仍歸 owner。

| `del` 的對象                 | 你擁有它嗎？ | 效果                                                                      |
| ---------------------------- | ------------ | ------------------------------------------------------------------------- |
| local、傳值參數、捕獲副本    | 是           | 最後存取 → **釋放儲存**                                                   |
| `mut` 參數（借呼叫端的變數） | 否           | 結束本次呼叫的借用 → **不釋放**；呼叫端保有                               |
| closure body 內的捕獲值      | 否           | 結束**本次 invocation** 的存取 → 不釋放；下次呼叫仍有                     |
| channel                      | refcounted   | 放掉這個 holder（refcount--）；最後 sender → **關閉**；最後 holder → 釋放 |

`del` 永不懸空：撤銷一個借用不可能釋放別的名字所擁有的儲存，而 Zerg 既有規則已擋掉「owner 在 borrower 仍存活時
就釋放」（`mut` 參數受限於該次呼叫；逃逸的 closure 擁有捕獲的副本）。編譯器靜態就知道每個 `del` 是釋放還是純
撤銷——只有 channel 帶 runtime refcount。

`del` 是**流程一致的**：一個名字只要在任一路徑上被 `del`，其後**每一條**路徑都視它為已死（不引入 runtime drop
flag）。因此在 `if` 某一分支裡 `del`，匯流之後該名字即不可再用，與其他分支對稱。

`del ch` 也是**提早關閉 channel** 的直接寫法——當下放掉你對 `ch` 的持有，若你是它最後一個 sender
即關閉 channel，無需再包一層更窄的 block。

## 建構與封裝（Construction & encapsulation）

建立一個值的**唯一原語是 struct literal**——它會指名每個欄位，所以只在「每個欄位都可見」處才能用。所謂
**constructor 不是獨立特性**：它就是一個（通常 `pub` 的）associated function，內部回傳一個 literal。因此只要型別
有**任一私有欄位**，它對其 module 之外就是 **opaque**——struct literal 指不出私有欄位，外部只能透過那個 `pub` 函式
建構；該函式在型別自己的 module 內執行，能在**建構當下**就把型別的 invariant 立好。

欄位可見性是**讀與寫綁在一起的單一旋鈕**——`pub` 欄位可讀、且在 `mut` binding 下可寫；private 欄位兩者皆否。
**沒有「對外可讀、對外不可寫」的獨立軸**；更細的控制以 method 表達。

copy-by-value 重新框定了「可寫 `pub` 欄位」的意義：改它只會動到持有者**自己那份 copy**，永遠影響不到別人的值
（無 aliasing）。所以 `pub` 可變欄位**不是共享突變的隱患**。把欄位設 private 的理由，不是阻止別人改你的值——copy
已經擋掉了——而是**保護 invariant**：只有型別自己的 method 能改該欄位，於是該型別的每個值都恆為合法。沒有 invariant
的純資料型別，欄位大方公開即可；必須恆為合法的型別，則把欄位設 private，並提供：

- **讀**——一個 `pub` getter method，回傳該欄位的一份 copy（copy-by-value 下便宜）；
- **改**——一個 `pub` mutator method，吃 `mut self`，在其中重新確立 invariant。

要造「一個既有值、只改幾個欄位」的新值，用 **functional update**——`Foo{ ..base, age: 2 }` 產生一個**新**值、
`base` 原封不動——這適用於欄位可見的型別；opaque 型別則用回傳新實例的 `with`-風格 method。Zerg **沒有可變的 builder
或 cascade**：它只對公開欄位型別有用（而那種 literal 已經講完一切）、碰不到私有欄位、又會把值拖過一串無效中途狀態
——與「immutable by default」「建構當下即合法」相衝。

## 函式與閉包（Functions & Closures）

函式是**一等值（first-class value）**：它有型別，可當引數傳遞、可回傳、可存進欄位、可綁定到變數。函式型別寫成
`fn(P...) -> R`；參數的 `mut` 是**型別的一部分**，所以 `fn(mut int) -> bool` 與 `fn(int) -> bool` 是不同型別
（兩者 calling convention 不同——就地 by-ref vs 複製）。可見性**不**屬於型別：`pub` 匯出的是 top-level 函式的
**名字**，永不隨值移動，對匿名函式也無意義。

持有函式的 binding 其可變性就是一般的 per-instance 軸——`mut f := …` 可 rebind、`f := …` 不可——與上述一切正交。

**閉包的捕獲規則與 `spawn` 相同：只捕獲 immutable 值與 channel，且以複製帶入。** `mut` 變數不能被捕獲；要用先
快照成 immutable binding（`snap := n`）。捕獲是複製——捕獲的 channel 做 refcount++、其餘深拷貝——所以逃出定義
scope 的閉包帶著自己的副本，永不懸空。等價地說：

> 一個閉包就是一個 scope-owned struct，它的欄位就是它的捕獲。

因此複製、釋放、channel-refcount 全都從既有記憶體規則掉出來、無須新增；捕獲了具 send 能力的 channel 端就算一個
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

兩個經典的閉包陷阱因此在結構上被排除。loop 變數是 `mut`，所以**不能**被捕獲——每一輪都必須快照，讓每個閉包
拿到自己的值（沒有共享 loop 變數的 bug）：

```text
loop i in 0..n {
    snap := i                      # 必要——i 是 mut、不可捕獲
    spawn fn() { handle(snap) }    # 每個 coroutine 拿到自己的 0, 1, 2, …
}
```

而且因為捕獲永遠是 immutable copy，「捕獲的是變數還是值？」根本沒有可觀察的答案——被捕獲的值永不改變，這個
問題自然消失。

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
