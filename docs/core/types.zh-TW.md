# Zerg 型別（Types）

原始純量、你宣告的 `struct` / `enum` / tuple、一個值如何建構,以及一種型別如何轉換成另一種。屬於
[語言參考](../language.zh-TW.md) 的一部分。亦有 [English](types.md) 版本。

## 原始型別（Primitive Types）

一組精簡且固定的集合——三種整數寬度（signed `int`、unsigned `uint`、以及 `byte` octet）。此外的**固定寬度階梯**
（`i8`、`i16`、`u32`、`f64`……）是一組 **stdlib 型別、不是新語法**——型別名不過就是一個 identifier，所以像 `u32`
這樣的寬度只是多加一個 stdlib 型別、完全不動語法：

| 型別    | 說明                                           |
| ------- | ---------------------------------------------- |
| `bool`  | `true` / `false`                               |
| `byte`  | unsigned 8-bit——Zerg 的 char                   |
| `rune`  | 單一個有效的 Unicode code point                |
| `int`   | signed 64-bit 整數                             |
| `uint`  | unsigned 64-bit 整數                           |
| `float` | IEEE-754 double（f64）                         |
| `str`   | immutable 的 Unicode 文字（不含 embedded NUL） |

`nil` 不是一個自己的型別——它是 `T?` 的 placeholder 值（[Null-safety 與錯誤處理](../code/errors.zh-TW.md));
而 `str` 在記憶體裡以 NUL 結尾,是 C 邊界的事（[FFI](../runtime/ffi.zh-TW.md)),不是這個型別的性質。

有兩種形式會**答出 `nil`**、而它們的外表看不出來:沒有 `-> type` 的 `fn`（`GRAMMAR#fn-decl`),以及最後一個
statement 不是 expression、或根本沒有 statement 的 **block**（`GRAMMAR#block`）。它們答出來的是一個「不存在」,
而**不存在不是任何位置握得住的值**——它沒有寬度、沒有儲存、也沒有 rendering。所以 `x := f()` 與 `z := { nop }`
會被帶位置拒絕,`print f()`、f-string 的 `{f()}` 與 `str(f())` 也一樣;由它組成的**容器**同樣如此:`[f()]` 與
`(f(), 1)` 往下一層仍然是儲存。真正接受「不存在」的位置是 `T?`,也就是 `z: int? = { nop }` 寫的那件事——一個
carrier 把位置往內移並包起來,正是[型別系統](type-system.zh-TW.md)說「位置可以 wrap」的那條規則。

> **[not yet]** `zerg` 沒有固定寬度階梯的任何一部分:`i8` … `i64`、`u8` … `u64`、`f32` 與 `f64` 被規範為 stdlib
> 型別,而沒有任何 stdlib 宣告過其中一個。它是**具名**被拒的——一個寬度不過是普通的 identifier、不是關鍵字,所以
> 拒絕曾經是 _undefined function `i32`_,任何拼錯的呼叫都會拿到的那句話,讀者被告知的是自己的名字不存在。**seed**
> 建得起也跑得動它們,這使本章成為唯一一處 seed 比較寬的地方。

- **整數溢位與除以零會 raise**（`OverflowError`、`DivideByZeroError`）——這是一次 **abort**、不是值
  （見 [Null-safety 與錯誤處理](../code/errors.zh-TW.md)）；`int`/`uint`/`byte`/`rune` 絕不環繞
  （要環繞就用下方的 `+%`/`-%`/`*%`）。
- **`float` 依 IEEE-754：** 溢位 → `±Inf`、無效運算 → `NaN`，兩者都不 raise；`NaN` 與任何值（含自己）都不相等。
- **`str` 以 `rune` 迭代、不可索引**——想要原始位元組就用 **`bytearray(s)`**（可能含 NUL 的二進位也用它，
  `str` 永遠不含 NUL），要碼位就用 **`runearray(s)`**。兩者各自命名它建出來的 list——`bytearray` **就是**
  `list[byte]`、`runearray` **就是** `list[rune]`，同一個型別的較短名字，處處可與展開寫法互換，**不是**
  strong typedef（見 [Collection](../code/collections.zh-TW.md)）。
- **`rune` 的值域不是一個區間**，這讓它成為唯一以**述詞**（predicate）而非上下界界定的 scalar：一個碼位是
  `0..=0x10FFFF` **扣掉** UTF-16 的 surrogate 區間 `0xD800..=0xDFFF`，那些不是字元。所以 `rune(0xD800)` 會
  raise `OverflowError`，即使那個數字綽綽有餘地放得進這個型別的 32 bit；而 `r: rune = 0xD800` 是同一條規則
  對一個已知值給出的編譯錯誤。這也是 `rune` 不屬於下方 fixed-width ladder 的原因：`i32` 是一個區間，`rune`
  不是。

### 整數運算（Integer operations）

- **Bitwise**——`&`、`|`、`^`、`~`（and、or、xor、complement）與位移 `<<`、`>>`，適用 `int`/`uint`/`byte`。`>>` 對 signed
  `int` 是 **arithmetic**（補符號位）、對 unsigned `uint`/`byte` 是 **logical**（補 0）——由型別的正負號決定，所以不
  需另設 logical-shift 運算子；位移量**超出型別寬度**——負數、或 ≥ 寬度——會 raise（`OverflowError`）。
  這些 desugar 到 spec（user type 可多載——見 [內建 spec](specs.zh-TW.md)），且 bitwise **符號**永不與邏輯
  **關鍵字** `not`/`and`/`or` 撞臉。
- **Wrapping**——`+`、`-`、`*` 溢位 raise；**`%` 後綴**的 `+%`、`-%`、`*%`（及一元 `-%`）改為 **mod 2^n 環繞**——供
  hashing、checksum、bit-mixing 這類「刻意繞回」的場景。**checked** 版已經是 `guard { a + b }` → `Result`（不需
  `checked_*`）；**saturating** 延後。
- **`int` 與 `uint` 是兩個型別,絕不相混**——`int + uint` 是 compile error,而且**不是特例**:運算子的兩個運算元
  必須已經是同一個型別,不論哪一對(見下)。這一對值得單獨點名,是因為 C 的答案才是地雷——在那裡有號運算元會轉成
  無號,所以 `-1 < 1u` 是 false。顯式 cast 一側:`int(u) + i`。
- **除法與餘數**——`/` 與 `%` 採 **Euclidean** 定義：餘數**恆為非負**（`0 ≤ a % b < |b|`），且
  `a == (a / b) * b + a % b` 對任何正負號都成立，所以 `a % n`（n>0）對任何 `b` 都是合法的 index/bucket。這是數學上
  canonical 的 `div`/`mod`、而非 C 那種號隨被除數的 truncation；compiler 只在**運算元可能為負**時補小修正，**兩者
  皆非負時完全 elide**（最常見、零成本）。`a / 0` 與 `a % 0` raise `DivideByZeroError`，`INT_MIN / -1` 溢位
  （`OverflowError`）；truncating 與 flooring 變體屬 stdlib（延後）。
- **`//` 的結果恆為 `int`**——`a // b` 就是同一個 Euclidean 除法，只是換一種寫法讓讀者一眼看出結果是整數、
  不必先看運算元。兩個整數時它**就是** `/`：語言只有**一條**整數除法規則，再加一條對負除數行為不同的
  規則會是陷阱而非功能。兩個 `float` 時它以 double 相除，再經過 `int(x)` 用的同一道範圍檢查落回 `int`，
  所以 `7.5 // 2.0` 是 `3`、`-7.5 // 2.0` 是 `-4`，不需要 `int(...)` 來回一趟（`7 // 2.0` 也一樣——untyped 的
  `7` 從它的運算元採用 `float`）。`/` 完全不變、維持型別驅動
  ——`int / int` 是 `int`、`float / float` 是 `float`——因為已具型別的 `int` 值永遠不會隱式變成 `float`
  （見下方「數值字面值」）。`//` **不會**開啟註解——Zerg 的註解以 `#` 起始。

> **[not yet]** bitwise 運算子並不 desugar 到任何 user type 能實作的東西。`BitAnd`、`BitOr`、`BitXor`、`Not`、`Shl`
> 與 `Shr` 這些 spec 在任何地方都沒有被宣告,所以指名其中一個會報 _error: no spec named `BitAnd`_——那是「沒有人寫
> 過這個 spec」的普通訊息——而複合值上的 `&` 沒有任何路徑通往使用者寫的 body。運算子本身在 `int` / `uint` / `byte`
> 上是內建的、如規範般運作;缺的是這道 desugar 存在的目的:多載（見 [Spec 與 Generics](specs.zh-TW.md)）。
>
> **[deviation]** 讓 `/` 與 `%` 成為 Euclidean 的那個修正是**無條件**產生的,並未在兩個運算元都可證明非負時
> elide——上面說的「最常見情況零成本」是意圖中的 codegen、不是今天的。語意不受影響:那是成本、不是錯的答案。

### 有型別的位置（Typed positions）

底下有好幾條規則講的都是**一個值遇上一個宣告型別** —— 什麼放得進去、字面量採用什麼、什麼會被拒絕。它們需要的是同
一個問題的答案,所以那個答案只講一次、講在這裡,其他規則引用它:**一個有型別的位置**,就是語言已經知道要什麼型別的
地方。

它們是這些,而且這份清單是**窮盡的**:

| 位置                      | 例子                           |
| ------------------------- | ------------------------------ |
| 有型別的 binding          | `x: byte = e`                  |
| assignment                | `x = e`（`x` 已宣告）          |
| `return`                  | `return e`                     |
| 引數                      | `f(e)`                         |
| 參數的預設值              | `fn f(x: byte = e)`            |
| 欄位的預設值              | `struct P { x: byte = e }`     |
| struct 字面量的欄位       | `P(e)`                         |
| enum variant 的 payload   | `Shape.Line(e)`                |
| list 字面量的元素         | `xs: list[byte] = [e]`         |
| map 字面量的 key 與 value | `{e: e}`                       |
| 寫進 map                  | `m[k] = e`                     |
| 容器方法的引數            | `xs.append(e)`                 |
| channel send              | `ch <- e`,以及 `select` 的 arm |
| `??` 的 fallback          | `x ?? e`                       |
| 另一個運算元              | `a + e`                        |

位置是**結構性**的,不是語法上的:它取決於這個運算式**對它外面那個構造來說是什麼**,而不是它怎麼被寫出來。**分組用
的括號不是位置** —— `(e)` 跟 `e` 是同一個位置 —— 這正是「用位置陳述的規則」不會被多打幾個括號繞過的原因。

**一個位置可以把值包起來;絕不改值的型別。** 位置替值建的東西是 carrier(下一段)或 spec 的盒子
([Spec 與 Generics](specs.zh-TW.md))——裡面的值保持自己的型別。放不進位置的值會被拒絕,修法是**寫出來**的轉換
`T(x)`(下方「型別轉換」)。

**carrier 不會終結一個位置——它把位置往內移一層。** 當宣告的型別是 `T?`、`Result[T]` 或 `Either[X, Y]` 時,真正跟
值相遇的是它的 **payload**,而那個 payload 就是同一個位置:`x: int? = e` 把 `e` 放在 binding 的位置上、對著 `int`;
`return Either.Left(e)` 則把它放在 `return` 的位置上。底下每一條規則在那裡讀到的都是 `T`,永遠不是外面那層 wrapper。

這份清單是一次一個靜默 miscompile 長出來的——每一個編譯器還沒被告知的位置,都是一個被靜默放進不合身型別裡的值,
carrier 的情形也在內(`x: float? = i` 印出 `5`;`Left(300)` 放進 `Result[byte]` 靜靜被截斷)。這份清單是契約:一個
新的語法形式欠一個「這是不是有型別的位置」的答案,而答案屬於這裡。現在每一個位置都會拒絕別的型別的值,並說出它在
哪裡拒絕。

**放不進去的字面量在每一個位置都被拒絕,包含另一個運算元。** `b: byte = 1` 之後 `b + 300` 是編譯錯誤:`300` 不是
`byte` 的值,所以它不採用,剩下的就是一個 `byte` 對著一個 `int`——兩個型別,沒有運算式。它以前會把整個運算式改定型
為 `int` 並印出 `301`,而那讓運算元這個位置成為唯一一個「字面量逃掉了那個本來要拿來量它的範圍」的地方。

### 型別從哪裡流進來——四個 carve-out

型別是**由下往上**算出來的:一個運算式的型別來自它的組成。有四個 carve-out——**而且只有這四個**——讓宣告的
型別往另一個方向流,流進一個無法為自己發言的運算式:

- **(a) 字面量的型別。** **未定型字面量**採用它所落位置的型別,並**在該型別中計算**(見下方「數值字面量」)。
- **(b) 複合值的型別。** **沒有元素能為自己發言的複合字面量**——`[]`、`{:}`,以及在 `list` 與 `[T; N]` 陣列
  之間做選擇的填充形式 `[v; N]`。
- **(c) closure 的參數型別。** closure **省略的 `: type`**,取自它被檢查所對的那個函式型別;從未遇到期望型別
  的省略型 closure 是錯誤。
- **(d) carrier 的 payload 型別。** 值、`Err` 或 `nil` 進入 `T?`、`Result[T]`、`Either[X, Y]`——payload 是同一個
  位置,只是往內一層。

**值泛型**也不是第五個:函式的 `N` 是**從**引數型別推出來的（`fn sum[N: int](xs: [int; N])`）,跑的方向與
這裡其他所有東西相同。

**只有四個**這件事是被檢查的、不是被宣稱的:`make layering` 把 seed 的雙向 checker 綁在這份清單上——它把
期望型別推進去的節點種類就是這些、沒有別的——並要求 `zerg` 的推導家族完全不接受期望型別。要多一個
carve-out,gate 一定會把它指名出來。

轉換**不是第五個 carve-out**:這四個決定的是運算式**還沒有的**型別,而轉換改的是它已經有的型別。

### 數值字面量（Numeric literals）

數值字面量是 **untyped** 的——它採用它的 **position** 要求的型別,在上面任何一個有型別的位置,並在**編譯期**檢查。
沒有 position 要求時,整數字面量預設為 `int`、帶小數/指數的字面量（`1.0`、`1e3`）預設為 `float`；
非十進位 `0x…` / `0o…` / `0b…` 也是普通整數字面量。

- 字面量**放不進**要求的型別 → **compile error**（`byte = 300`、`uint = -1`、超過 i64 的 `int` 字面量）——不是
  runtime overflow。它是寫出來的轉換的常數雙生(`byte(300)`,
  [型別轉換](#into--一個普通的轉換-spec)):目標的型別是已知的、值也是已知的,所以答案現在就是已知的。

- **有型別的 `float` context 接受一個 untyped 整數字面量——而且只接受字面量**:`x: float = 1` 合法(字面量採
  用),而 `x: float = i`(`i` 是 `int` 值)是一次**轉換**、要寫出來——`x: float = float(i)`。帶小數或指數的字面
  量(`1.0`、`1e3`)從一開始就是 `float`、絕不是 `int`。

- **字面量是採用,值是轉換,而這兩者值得分清楚。** `b: byte = 5` 寫下一個 byte,完全沒有轉換;而
  `b: byte = n`(`n` 是 `int` 值)是一次**轉換**,而轉換要寫出來:`b: byte = byte(n)`,它在執行期可能 raise
  `OverflowError`。採用在編譯期定案;寫出來的轉換才會執行。

  偏離字面量預設的採用是一個 **lint** 發現(`L502`)——`1.5 + 1` 會被報而 `1.5 + 1.0` 不會——因為讀
  `xs: list[byte] = [1, 2]` 的人應該在頁面上看見 byte,而不是從宣告去推斷它。它是建議而不是語言規則。這個
  finding 會指名每一個字面量,並給出讓型別現形的寫法——`float` 用 `1.0`,而型別本身沒有字面量形式時則用
  `byte(1)`。

- **一個由字面量組成的運算式,本身就是一個字面量。** `100 + 100` 裡沒有任何東西自帶型別,所以整個運算式一起採
  用:`x: byte = 100 + 100` 是 byte 算術,答案 `200`。每個組成都在運算子執行**之前**先對著目標型別量——
  `x: byte = 300 - 100` 被拒,指名的是 `300`,不是它會變成的 `200`——而之後的算術就是目標型別自己的算術,所以
  `x: byte = 200 + 100` 同樣被拒。`float` 目標會讓運算子成為 float 運算子:**`x: float = 1 / 2` 是 `0.5`**,
  因為兩個字面量在 `/` 執行之前就已經是 float 了。

  除以常數 `0` 在任何地方都是編譯錯誤,不論是否可達——與「放不進去的字面量」是同一個論證,只是發生在那個「不需
  要任何型別出錯就會失敗」的運算子上。

- **界限由 position 決定。** 一個整數字面量在沒有東西要求別的時候對著 `int` 量,在 `uint` position 上則對著
  `uint` 量——所以 `u: uint = 18446744073709551615` 就是那個值、不是錯誤,而 `x := 18446744073709551615` 與
  `int(18446744073709551615)` 仍然被拒。**兩個**界限都超出的字面量,不論在什麼 position 都是編譯錯誤:那不是這
  台機器裝得下的數。

## 使用者定義型別（User-Defined Types）

宣告你自己的**積型別**（`struct`）與**和型別**（`enum`），兩者都可對 `[...]` 泛型化。

**宣告出來的型別，名字以大寫字母開頭**（[GRAMMAR#type-ident](../../GRAMMAR)），而且這是規則、不是慣例：第一個
字母的大小寫，就是這個語言分開它那兩個命名空間的全部依據。`Point(1, 2)` 是建構、`point(1, 2)` 是呼叫；
`cli.Opt` 是模組限定、`It.Item` 是關聯型別投影。這些都在任何名字被解析之前就要判定，所以 `struct lower`——
或者 `struct _Box`，因為 `_` 沒有大小寫、也就不屬於任何一個命名空間——會在宣告處被拒絕，回報為 `E610`。
**使用**的位置不受同一條限制：內建型別名稱（`int`、`str`、`list`、各個定寬成員）都是小寫，而且沒有任何宣告
會引入它們。

**可見性（`pub`）**——每個宣告（型別、欄位、函式）**預設 private，只在自己的 module 內可見**；在前面加 `pub`
才會匯出、供他處使用。mutability 是另一條獨立的軸、**不**在這裡宣告：它屬於**實例（instance）**（也就是
binding；見 Values & Memory），絕不屬於欄位或型別。module 與 package 是什麼，以及可見性、coherence、entry point
如何跨越它們，見 [Module、Package 與 Program](../runtime/package.zh-TW.md)。

```text
struct Node {
    value: int
    next:  Node?            # 自我參照——自動 boxing（見 Values & Memory）
}

enum Either[X, Y] {         # 泛型 sum type
    Left(X)
    Right(Y)
}
```

> **[not yet]** 上面那一段的兩個宣告都編不過。遞迴 `struct` 是 `E452`(見下),而**泛型 `enum`** 是
> _E212 NotImplemented: a generic enum `Either[…]` — this compiler erases type parameters, and a variant's
> payload names one_;泛型 `struct` 因同樣的理由是 `E215`。這段顯示的是規範中的形狀,兩者都在等泛型型別。

**遞迴與自我參照型別**可直接運作——一個 `struct Node { next: Node? }`、一個 `enum Expr { Num(int); Add(Expr,
Expr) }`——**不需 pointer**:編譯器把那個自我參照的槽自動裝箱在一個 refcounted cell 之後,所以這種值的複製是**按
參照**(refcount 共享),不是深拷貝。它不做的是釋放那條鏈,那是[值與記憶體](memory.zh-TW.md)自己那條 deviation。

> **[not yet]** 遞迴 **`struct`** 宣告不出來。上面寫的那個 `Node` 會被拒絕、報 _`Node` is part of a cycle of
> by-value declarations — a type holding itself, however indirectly, has no size_:算大小這件事跑在宣告圖上、早於
> 任何裝箱決定,所以那個自我參照的槽從來沒拿到這段文字答應它的 cell。能運作的是遞迴 **`enum`** 那一半,它的 payload
> 就是編譯器裝箱的那個槽——這也是為什麼 `Expr` 建得起來而 `Node` 建不起來,以及為什麼
> [值與記憶體](memory.zh-TW.md) 裡同一個例子同樣編不過。

其實 `Either`、`Result[T]`、`T?` 並不特殊——它們就是建立在 `enum` 上面的普通 stdlib 型別
（見 [Null-safety 與錯誤處理](../code/errors.zh-TW.md)）。一個 `enum`
的 **variant 隨型別的可見性**——`pub enum` 公開它的每一個 variant（可建構、可 `match`）；沒有 per-variant 的私有。

一個 `enum` 的 **discriminant 對「fieldless enum」與「payload enum」行為不同**——分界在於是否*每一個* variant 都無
欄位。一個 **fieldless** `enum` 可以給某個 variant 明確的 `= <discriminant>`——一個 **compile-time 常數整數**、
在各 variant 間互異（未指定者為前一個 `+ 1`、從 `0` 起算）——使它成為 **C 式整數 enum**：`variant = <int>`。它與
陣列長度是**同一個編譯期常數**，所以 `A = BASE`、`A = 2 + 3` 與 `A = BASE * 2 - 1` 都是這個形式，而函式呼叫不
是；摺不出來的那個是**在該 variant 上**報錯。這種
enum 有**原生、C 相容的整數 repr**（依一條 default 規則以 `int` 為底、不需標註）;**enum 名稱是一個值命名空間**
——`Color.Green` 指名該 variant、`Color.of(n)` 由數字反轉回來——其中 `int(v)` **讀**出 discriminant、
`E.of(n) -> E?` **反轉**回來(未知的 `n` 給 `nil`、絕不變成錯的 variant)。

那個命名空間是 **enum 自己的**,兩個 enum 各自宣告一個 `Red` 是可以的:有 `enum Colour { Red; Green }` 與
`enum Signal { Amber; Red }` 時,`Colour.Red` 與 `Signal.Red` 是兩個剛好拼法相同的不同 variant,各有自己的
discriminant。帶限定的名字是**在它指名的那個 enum 裡面**解析的,所以指到該 enum 沒有宣告的名字會是
_E457 `Apple` is a variant of `Fruit`, not of `Colour`_——一句關於那一行上的 enum 的話,並且帶位置。

> **[deviation]** 在這個編譯器裡,**裸的** variant 名字不是一個值:`c := Red` 會是 _E383 `Red` is a variant of
> `Colour`, and a variant is named through its enum_,而 [Grammar](../surface/grammar.zh-TW.md) 說裸名字只要
> 解析得到一個 variant 就是那個 variant。當兩個 enum 都宣告了這個名字,那句話裡建議的寫法會是其中第一個
> ——它是兩種可行寫法之一,未必是你要的那一個。

要指定寬度就用 opt-in layout 裝飾器
`#[repr]`（**[not yet]**——今天保留且會大聲拒絕,見 [Decorator](decorators.zh-TW.md)）;序列化/wire 形式則是
`Encode` / `Decode` impl（**[not yet]**）、絕不是裝飾器。

一個 **payload** `enum`（任一 variant 帶欄位）則保持其 **tag opaque、只可 match**——不允許 `= 5`，你 `match` 的是
variant、絕不是 tag。要把這種 variant 綁定某個特定整數，就寫**顯式轉換**：一個從 variant 到數字的 `match`，再一個
回來、且**帶驗證**。這又是*convert by re-construction, never reinterpret*——數字是從 variant **建**出來的、不是把
tag 的 bytes 重讀——而且它天然吸收 baked-in 值給不了的不連續值、別名與版本演進。

一個 **tuple**——`(int, str)`，欄位以位置存取 `.0`、`.1`——不過就是一個**匿名 `struct`**：同一個積型別，只是不具名、
供一次性的位置束用（多重回傳、`divmod -> (int, int)`）。因為匿名，它是全語言**唯一結構化定型**的形式——`(int, str)`
不管寫在哪都是同一個型別，而每個具名 `struct` 與 `enum` 仍是 **nominal**。它沿用整套積型別機制——copy-by-value、
以及結構層的一條規則:**具名型別自己 opt-in;無名形式繼承它組成的 opt-in。** 一個 tuple(array 也一樣)在**每個
組成**都有 `Eq` / `Ord` / `Hash` 時就有——不需要宣告,因為無名形式沒有宣告點,而它的組成已經各自 opt-in 過了
（見 [Spec 與 Generics](specs.zh-TW.md)）。它不能帶的是**自己的**行為——沒有 inherent method、沒有手寫的
`spec` impl：一旦某個值需要行為、nominal 身分、或值得閱讀的欄位名，就改用具名 `struct`。
tuple 的結果是 **first-class**——可存、可傳、可解構——所以多重回傳不需要任何額外機制
（見 [模式比對](../code/control-flow.zh-TW.md)）。

> **[not yet]** 這段文字說 tuple 免費就有的那兩件事,一件都沒建。tuple 上的 `==` 是
> _E445 NotImplemented: `==` on a `(int, int)` — structural equality over a container is unbuilt, and a
> container has no declaration to derive it on_:上面的組成繼承規則已是規格,而缺的是無名形式上的那個衍生。
> **解構**被拒絕得更早一步、在逗號上——`a, b := two()` 報 _E205 expected a newline or `;` to separate
> statements, found `,`_,在該指名形式的地方指名了標點(加了括號的 `(a, b) := two()` 則說得出來,是 `E238`)。
> 無論哪一種,tuple 的結果如規範般可存、可傳,但只能用 `.0` / `.1` 讀回來。

**`type X = Y`** 定義一個**全新、獨立的型別**——不是透明 alias。`X` 承接 `Y` 的表示與實作（它的欄位或 variant、
以及它的 `spec` impl,現在 `This` = `X`),但是一個**獨立身分**:`X` 與 `Y` 是**不同型別、即使結構完全相同**,而且
兩者間**不能 cast**——要轉換就 **re-construction**(`X(y)` / `Y(x)`),與任何轉換一樣。有一項繼承是刻意不給的:
`X` **不**承接 `Y` 的 `Into` impl——`X` 能轉換成什麼,是 `X` 自己的宣告。一個**單型**的 `type X = Y`
在 runtime **降低成 `Y`**——區別**只在編譯期**,所以 `Celsius = int` 不花任何成本(無 box、無包裝),而一個 `Celsius`
沒有明確的 `int(c)` / `Celsius(x)` 就永遠不是 `int`。一個**泛型** alias `type X[T] = …` 這個階段**尚未支援**(會被
解析、但被拒絕)。這是 **strong-typedef** 工具——一個 `UserId`,行為像 `int`、卻永遠不能被當作一個裸 `int` 或
`ProductId` 傳進去——並與單欄位 struct 的 **newtype** 有別:newtype 是把值*包*進一個新欄位、配全新 impl,而非沿用整個
形狀。prelude 的 **`Result[T]`** 與 **`T?`** 是它在 `Either` 上、由 compiler 提供的泛型形式(內建,而非你目前能用泛型
`type` 自己寫出的東西),這也是為什麼它們彼此不同、要用 `ok_or` / `ok` 顯式跨越。

> **[deviation]** `type X = Y` 只對**純量**底型 `Y` 實作,而新型別**不**繼承 `Y` 的算術或 `spec` impl——一個
> `Celsius = int` 不先 `int(c)` 就不接受 `+`,與上面的繼承規則相反。其餘一律具名拒絕:_E304 NotImplemented:
> `type Name = str` over a non-scalar — this compiler builds a strong typedef over a scalar, where the new
> name costs nothing at runtime; a `str`, a container or a struct underneath needs the copy and drop rules
> to follow the name_。意圖中的語意(一個沿用 `Y` 整個表示與 impl 的全新身分)成立;建出來的只有純量、無 impl
> 的情形。

## 建構與封裝（Construction & encapsulation）

建立一個值的**唯一原語是 struct literal**——它會指名每個欄位，所以只在「每個欄位都可見」處才能用。所謂
**constructor 不是獨立特性**：它就是一個（通常 `pub` 的）associated function，內部回傳一個 literal；該函式在型別
自己的 module 內執行，能在**建構當下**就把型別的 invariant 立好（**[not yet]**——associated function 會被指名
拒絕,_E424 `User.from_id(…)` is an associated function_,所以本節推理所依據的那種「立 invariant 的 constructor」
今天要寫成自由函式）。**私有欄位是外部永遠指不出的欄位**：它必須帶預設值
（見下），所以外部的建構把它省略掉、由宣告決定它的值。要讓 literal 本身在 module 之外不可用，那是 `#[sealed]`
decorator 的職責——**[not yet]**，所以今天只要型別可及，literal 就可及。

> **[not yet]** struct literal **只依位置**綁定,所以那個指名欄位的形式並不存在:`P(a: 1, b: 2)` 報
> _NotImplemented: the named argument `a:` — this compiler binds arguments by position only_
> （見 [函式與 Closure](../code/functions.zh-TW.md)）。`P(1, 2)` 建出同一個值,所以建構本身不受影響;缺的是這一節
> 用來陳述自己規則的那個寫法——「它會指名每個欄位」正是「私有欄位是外部指不出的欄位」的推導起點——而下面的
> `Foo(age: 2, name: base.name)` 寫的是編譯器讀不了的形式。

### 欄位預設值（Field defaults）

欄位可以宣告**預設值**——`h: int = 4`——而預設值正是讓該欄位的 constructor 參數可以被**省略**的東西：對
`Box(w, h)` 而言，`Box(1)` 建出的值與 `Box(1, 4)` 相同。這就是[函式參數預設值](../code/functions.zh-TW.md)本來
就遵循的規則，套用在依欄位的 constructor 上；而且兩個方向都照著走：回填是從已寫出的參數尾端開始，所以預設值只讓
**那一個**欄位可省略、不會讓它前面的欄位也可省略；而預設值是**每次建構各求值一次**，不是在宣告處只算一次——裡面
若寫了運算式（一個呼叫、幾個 module 常數的和），每一次省略該欄位的建構都會再跑一次。

**沒有零值（zero value）**。因此沒有預設值的非 optional 欄位在建構時是**必填**的，少給就是錯誤、並且會指名該欄位。
**唯一的隱含預設值**是 `T?` 欄位的 `nil`，那是它天生的「不存在」狀態——`T?` 不必寫 `=` 就可以省略。

兩半在可見性上會合：**非 `pub` 欄位是 module-private，而且必須帶預設值**。依欄位的 constructor 是公開的，所以
「沒有預設值的欄位」就是每次建構都得供值的欄位——而外部無法為一個自己讀不到的欄位供值。沒有預設值的私有欄位會在
該欄位自己的宣告處被拒絕（`E482`），並指名該欄位。

> **[not yet]** 讀取**另一個欄位**的預設值——`struct P { pub a: int; pub b: int = a * 2 }`——是唯一未實作的形狀，
> 而它與[函式與 Closure](../code/functions.zh-TW.md) 裡「參數預設值讀取前一個參數」是同一個形狀、同一個理由：
> 預設值是在**建構處**才被具體化的，而欄位在那裡不是作用域中的名字，所以 `a` 會解析到別的同名東西。它會報
> _NotImplemented: the default on field `b` of `P` reads the field `a`_，並附上該欄位的位置。

欄位可見性是**讀與寫綁在一起的單一旋鈕**——`pub` 欄位可讀、且在 `mut` binding 下可寫；private 欄位兩者皆否，
而從另一個 module 指名一個 private 欄位在兩個方向上都是編譯錯誤，並附上位置（見
[Module、Package 與程式](../runtime/package.zh-TW.md)）。
**沒有「對外可讀、對外不可寫」的獨立軸**；更細的控制以 method 表達。

copy-by-value 重新框定了「可寫 `pub` 欄位」的意義：改它只會動到持有者**自己那份 copy**，永遠影響不到別人的值
（無 aliasing）。所以 `pub` 可變欄位**不是共享突變的隱患**。把欄位設 private 的理由，不是阻止別人改你的值——copy
已經擋掉了——而是**保護 invariant**：只有型別自己的 method 能改該欄位，於是該型別的每個值都恆為合法。沒有 invariant
的純資料型別，欄位大方公開即可；必須恆為合法的型別，則把欄位設 private，並提供：

- **讀**——一個 `pub` getter method，回傳該欄位的一份 copy（copy-by-value 下便宜）；
- **改**——一個 `pub mut fn` mutator（其 receiver 是 `This`、就地 mutate——`this` 不是參數），在其中重新確立
  invariant。

要造「一個既有值、只改幾個欄位」的新值，就呼叫 constructor、帶上要改的欄位、其餘顯式複製——`Foo(age: 2,
name: base.name)` 產生一個**新**值、`base` 原封不動——這適用於欄位可見的型別；opaque 型別則用回傳新實例的
`with`-風格 method。一個能替你複製未變欄位的 spread / `..base` 簡寫**尚未在語法中**——那是另一個未定的設計問題、
不是你今天能寫的糖。Zerg **沒有可變的 builder 或 cascade**：它只對公開欄位型別有用（而那種 constructor 呼叫已經講
完一切）、碰不到私有欄位、又會把值拖過一串無效中途狀態——與「immutable by default」「建構當下即合法」相衝。

## 型別轉換（Type Conversion）

Zerg **以重新建構（re-construction）轉換、絕不以重新詮釋（reinterpret）**——一次轉換 `T(x)` 是*用 `x` 的值建一個
新的 `T`*、如同 constructor；**沒有 C 式的 cast** 把一種型別的位元當成另一種來看（reinterpret），也不提供。三種
型別操作因此清楚分開：**建**一個新值（`T(x)`，這裡）、**測**一個 existential 的身分（`x is T` → `bool`，見
[型別測試](specs.zh-TW.md)——這個階段 **[not yet]** 支援非錯誤型別,今天只有錯誤分類可 `is` 測試）、以及**絕不**
把一種型別的儲存重新詮釋成另一種。

轉換**預設顯式**——`int` 不是 `bool`；要轉就用 constructor 風格呼叫建一個（`bool(8)`、`int(c)`）。`bool(x)` 作用
在數字上回答的是 `x != 0`——truthiness 本來要問的那個問題被寫了出來,所以它永遠不會沒寫就進到條件裡。primitive
之間的轉換由**編譯器內建**；使用者型別不能對 primitive 加。

**窄化一個 primitive** 可能丟失值，因此比照算術檢查：

- 整數轉換的值**放不進**目標就 raise（`OverflowError`）——`byte(300)`、`uint(-5)`、對超過 i64 的 `uint` 做
  `int(u)`。**checked** 版是 `guard { byte(x) }` → `Result`；要**截斷**到低位就先遮罩——`byte(x & 0xFF)` 一定 fit、
  所以不會 raise。saturating 延後。
- **`float` → 整數**捨去小數（`int(3.7)` 是 `3`——是本意、非 bug），但當整數部分**超出範圍**、或 float 是
  `NaN` / `±Inf` 時 raise。

### `Into` —— 一個普通的轉換 spec

轉換是**寫出來的**。`T(x)` 在 scalar 之間轉換;使用者型別經由 constructor 或具名函式轉換;沒有東西會自己轉換——
**position 只會把值包起來,絕不改值的型別**(見上方「有型別的位置」)。`Into` 不是語言替你執行的機制:它是**替
「可轉換」命名的 spec**,讓泛型程式碼可以要求這個能力——

```text
spec Into[T] {
    fn into() -> T
}
```

- **型別靠實作它來加入。沒有任何內建型別加入**,而理由是分開的兩個。數字之間,轉換是寫出來的 `T(x)`——那就是上面
  那條規則的全部,而一個並列的 `.into()` 會需要 position 說出它指的是哪個目標,那是 [型別系統](type-system.zh-TW.md)
  在同一句話裡禁止的。而轉成文字則根本沒有東西可加入:`display` 是內建的值**渲染**、不是 spec
  (見 [Format](../runtime/format.zh-TW.md)),所以 `str(x)` 對每個型別都有答案——想要文字的泛型完全不需要 bound。
  (對**複合值**是 **[not yet]**:`struct` 上的 `str(P(7))` 是 _E449 NotImplemented: rendering a P as text — a
  composite needs the structural `Display` this compiler does not generate_,而泛型在 monomorphize 之後撞到的
  是同一個拒絕。成立的是「不需要 bound」這條規則;撐在它後面的那個渲染,對複合值尚未建置,一如
  [Spec 與 Generics](specs.zh-TW.md) 所標。)
- **剩下的是語言沒有的那種轉換**:`impl Into[Meters] for Feet`,以寫出來的 `x.into()` 呼叫。內建型別上的 `into`
  會被指名拒絕,並說出該改寫什麼。
- **泛型程式碼以它為 bound**——`fn f[T: Into[Meters]](x: T)` 可以呼叫 `x.into()`,目標由 bound 定死。**引數
  是 bound 的一部分**:一個實作了 `Into[Feet]` 的型別並不滿足 `Into[Meters]`。
- **一步,絕不串接**——`X → Y` 和 `Y → Z` 不會給你 `X → Z`。寫兩步,或自己宣告 `X → Z`。

**super-spec** 也帶著它的引數:`spec Keyed: Into[str]` 說的是 `Keyed` 在 `str` 上擴充 `Into`,所以一個
`impl Keyed` 欠的是把 `Into` 自己的參數換成 `str` 之後的那些簽章。bound 的引數是拿去跟 impl **比對**;super 的引數
則是**代入**被指名 spec 的參數——兩件不同的事,做在不同的地方。(`Eq` 不帶參數,所以
[Spec 與泛型](specs.zh-TW.md) 為 `Ord` 寫的 super-spec 就是裸的 `spec Ord: Eq`。)

**運算子的兩個運算元必須已經是同一個型別。** untyped literal 會採用另一個運算元——上方的「另一個運算元」位置——
所以 `1.5 + 1` 是兩個 `float`。兩個**已定型**、型別不同的運算元是編譯錯誤,不論哪一對:`i + f` 和 `i + u` 是同一
個錯、同一個修法——把一側寫成 cast(`float(i) + f`、`int(u) + i`)。每一對都是同一條規則;沒有東西被 promote,也
沒有任何目標會被往下推進運算式。

**`T(x)` 接受的轉換**就是這些,沒有別的。它們不是 `Into` impl,從來也不是:`T(x)` 是一個內建形式,而這是它有答案
的那些對。

| from    | to                               | 會 raise   | 說明                                         |
| ------- | -------------------------------- | ---------- | -------------------------------------------- |
| `byte`  | `int`                            | 否         | 每一個 byte 都是一個 int                     |
| `rune`  | `int`                            | 否         | 每一個 code point 都是一個 int               |
| `int`   | `float`                          | 否         | 不會失敗;超過 2^53 可能失精度                |
| `int`   | `byte`                           | 是         | 超出範圍 → `OverflowError`                   |
| `int`   | `rune`                           | 是         | 不是 code point → `OverflowError`            |
| `int`   | `uint`                           | 是         | 負數 → `OverflowError`                       |
| `uint`  | `int`                            | 是         | 超過有號數上限 → `OverflowError`             |
| `str`   | `int` / `uint` / `float`         | 是         | **解析**文字——`ValueError` / `OverflowError` |
| `float` | `int` / `byte` / `uint` / `rune` | **被拒絕** | `E394`——丟掉小數是一個決定;寫出動詞          |

**這張表的形狀是一個中樞,而中樞是 `int`。** 每一組被接受的配對都有一側是 `int`,那正是「一步」規則畫成的圖。所
以不在表上的配對就不是這個語言擁有的轉換,而不在表上只有兩種方式。

**`byte → float` 不在**,因為那會是 `byte → int → float`,而一步就是一次轉換的定義:寫成兩步,`float(int(b))`。
兩個數字之間每一組缺席的配對都是同一句話換上別的名字——`byte → rune`、`byte → uint`、`rune → uint`——每一組都是
`E395`,而它會把它要的那兩步印出來。

**`float` 作為來源之所以不在,理由不同**,而且它是這裡唯一的不對稱:丟掉小數不是缺了一步,而是一個**決定**,並且
有四個都說得通的答案。所以語言拒絕替程式做這個決定,由程式用一個動詞寫出來——`math.trunc`、`math.floor`、
`math.ceil` 或 `math.round`,每一個都回答一個 `int`,因此它們是整個轉換而不是其中一半——或者用 `//`,那個本來就
落在 `int` 的除法。`float` 上的 `int(x)` 是 `E394`,而它會指名該寫哪個動詞;目標更窄時就是動詞再加上轉換,
`byte(math.trunc(x))`。一個 `int` 裝不下的量會 raise `OverflowError`,和其他每一個會失敗的轉換一樣(見
[標準函式庫](../runtime/stdlib.zh-TW.md))。

**`bool(x)` 和 `str(x)` 不在這張表上**,這也是上面那一列點名四個目標、而不是每一個目標的原因。兩者都不是這個意
義下的轉換:`bool(3.5)` 是上面說的那個零值測試,答 `true`,而 `str(x)` 是透過 `display` 渲染一個值,每個型別都有
`display`。兩者都沒有丟掉任何小數,所以兩者都沒有決定要做。

`int("42")` 以自己的一列進了表,因為它是穿著同一種寫法的另一種運算:它**解析**數字的文字,而不是重新建構一個值,
而且只有 `int`、`uint` 和 `float` 這麼做(見[內建函式](../runtime/builtins.zh-TW.md))。

**編譯器算得出來的轉換就會被算出來。** `byte(300)` 是良構的 —— 然後它以**常數**的身分失敗:值是已知的,轉換已知
會 raise,於是在編譯期報出來而不是留到執行期。可達性不參與其中;`if false { b := byte(300) }` 是同一個錯誤。

**「已知」是一個概念,而它就是 const-expr。** 一個字面量、一個初始值是 const-expr 的繫結、一個 `const`,以及它們
之上的運算子:`300`、`200 + 100`、對 `big := 300` 而言的 `big`、對 `const N := 100` 而言的 `N` 與 `N * 3`。這和
填充計數 `[v; N]` 要求的是同一個概念,而且是刻意的——這份文件裡一句話有兩種讀法,正是一個語言對同一個問題長出兩
個答案的方式。

**它在一次呼叫處停下,在 `mut` 繫結處也停下。** `byte(f(300))` 在它執行的地方 raise,不論 `f` 是普通函式還是泛
型:一次呼叫不是常數形式,而上面 enum discriminant 那條規則用的正是同一句話。`mut` 繫結被排除的理由是它自己的:它在繫結與轉換之間可以被寫入,所
以它在那裡持有的並不是它在這裡持有的。

**這兩個位置的差別在於拿一個未知的值怎麼辦,而不在於什麼算未知。** 填充計數非要一個數不可,所以它被拒絕
(`E475`);而轉換一個值本來就是一次普通的轉換,所以它在執行的地方被檢查。

**它穿得過 monomorphization。** 一個泛型的本體是以它的**特化**被檢查的——`fn hold[T](v: T)` 裡的 `y: T = 300`
以 `byte` 呼叫時會被拒絕,而且會指名那個 byte——因為代換發生在常數規則之前,不是之後。型別引數正是讓範圍這個問題
問得出口的東西,而到那時它已經是已知的。

> **[deviation]** 在這個編譯器裡,一個型別只能有**一個** `Into`,不能有好幾個——_E461 NotImplemented: a second
> `impl Into[…] for Feet` — this compiler keys a method by its NAME, so one type carries one `into`; the
> language allows several, and reaching that needs the method keyed by the spec and its arguments_。那正是
> 上面那個 bound 也需要的同一件事,也正是能讓手寫的 `x.into()` 說出它指的是哪一個的東西。

一個值、一個 `Err` 或 `nil` 在有型別的位置進入 `Either`,是**包裹**規則在運作、不是轉換
（見 [Null-safety 與錯誤處理](../code/errors.zh-TW.md)):carrier 建在值的外面,值在裡面保持自己的型別——仍然是
建構,絕不是 reinterpret。
