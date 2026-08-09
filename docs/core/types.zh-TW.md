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

> **[not yet]** 固定寬度階梯一個都不存在:`i8` … `i64`、`u8` … `u64`、`f32` 與 `f64` 被規範為 stdlib 型別,而沒有任何
> stdlib 宣告過其中一個。因為一個寬度不過是普通的 identifier、不是關鍵字,連拒絕都不是具名的——`i32(x)` 報的是
> _undefined function `i32`_,任何拼錯的呼叫都會拿到的那句話,所以讀者被告知的是這個名字不存在,而不是這道階梯尚未
> 建置。

- **整數溢位與除以零會 raise**（`OverflowError`、`DivideByZeroError`）——這是一次 **abort**、不是值
  （見 [Null-safety 與錯誤處理](../code/errors.zh-TW.md)）；`int`/`uint`/`byte`/`rune` 絕不環繞
  （要環繞就用下方的 `+%`/`-%`/`*%`）。
- **`float` 依 IEEE-754：** 溢位 → `±Inf`、無效運算 → `NaN`，兩者都不 raise；`NaN` 與任何值（含自己）都不相等。
- **`str` 以 `rune` 迭代、不可索引**——想要原始位元組，就轉成 `list[byte]`
  （見 [Collection](../code/collections.zh-TW.md)；可能含 NUL 的二進位也用它，`str` 永遠不含 NUL）。
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
- **`int`/`uint` 混合絕不隱式**——`int + uint` 是 compile error（無隱式轉換，也順帶避開 C 的 signed/unsigned 比較
  地雷）；顯式 cast 一側（`int(u) + i`）。
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
`return Left(e)` 則把它放在 `return` 的位置上。底下每一條規則在那裡讀到的都是 `T`,永遠不是外面那層 wrapper。

這份清單是一次一個靜默 miscompile 長出來的——每一個編譯器還沒被告知的位置,都是一個被靜默放進不合身型別裡的值,
carrier 的情形也在內(`x: float? = i` 印出 `5`;`Left(300)` 放進 `Result[byte]` 靜靜被截斷)。這份清單是契約:一個
新的語法形式欠一個「這是不是有型別的位置」的答案,而答案屬於這裡。現在每一個位置都會拒絕別的型別的值,並說出它在
哪裡拒絕。

**放不進去的字面量在每一個位置都被拒絕,包含另一個運算元。** `b: byte = 1` 之後 `b + 300` 是編譯錯誤:`300` 不是
`byte` 的值,所以它不採用,剩下的就是一個 `byte` 對著一個 `int`——兩個型別,沒有運算式。它以前會把整個運算式改定型
為 `int` 並印出 `301`,而那讓運算元這個位置成為唯一一個「字面量逃掉了那個本來要拿來量它的範圍」的地方。

### 數值字面量（Numeric literals）

數值字面量是 **untyped** 的——它採用 context 要求的型別,在上面**任何一個有型別的位置**,並在**編譯期**檢查。
無 context 時,整數字面量預設為 `int`、帶小數/指數的字面量（`1.0`、`1e3`）預設為 `float`；
非十進位 `0x…` / `0o…` / `0b…` 也是普通整數字面量。

- 字面量**放不進**要求的型別 → **compile error**（`byte = 300`、`uint = -1`、超過 i64 的 `int` 字面量）——不是
  runtime overflow。它是寫出來的轉換的常數雙生(`byte(300)`,
  [型別轉換](#into--一個普通的轉換-spec)):目標的型別是已知的、值也是已知的,所以答案現在就是已知的。

- **有型別的 `float` context 接受一個 untyped 整數字面量——而且只接受字面量**:`x: float = 1` 合法(字面量採
  用),而 `x: float = i`(`i` 是 `int` 值)是一次**轉換**、要寫出來——`x: float = float(i)`。帶小數或指數的字面
  量(`1.0`、`1e3`)從一開始就是 `float`、絕不是 `int`。

- **字面量是採用,值是轉換,而這兩者值得分清楚。** `b: byte = 5` 寫下一個 byte,完全沒有轉換;而
  `b: byte = 300` 是 **compile error**——已知這個常數放不進去。`b: byte = n`(`n` 是 `int` 值)是一次**轉換**,
  而轉換要寫出來:`b: byte = byte(n)`,它在執行期可能 raise `OverflowError`。採用在編譯期定案;寫出來的轉換才會
  執行。

  偏離字面量預設的採用是一個 **lint** 發現(`L502`),因為讀 `xs: list[byte] = [1, 2]` 的人應該在頁面上看見
  byte,而不是從宣告去推斷它。這個 finding 會指名每一個字面量,並給出讓型別現形的寫法——`float` 用 `1.0`,而
  型別本身沒有字面量形式時則用 `byte(1)`。

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

**遞迴與自我參照型別**可直接運作——一個 `struct Node { next: Node? }`、一個 `enum Expr { Num(int); Add(Expr,
Expr) }`——**不需 pointer**:編譯器把那個自我參照的槽自動裝箱在一個 refcounted cell 之後,所以這種值的複製是**按
參照**(refcount 共享),不是深拷貝。它的 MVP 限制(以 `mut` 建出的循環會洩漏;長鏈以 O(depth) 釋放)見
[值與記憶體](memory.zh-TW.md)。

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
在各 variant 間互異（未指定者為前一個 `+ 1`、從 `0` 起算）——使它成為 **C 式整數 enum**：`variant = <int>`。這種
enum 有**原生、C 相容的整數 repr**（依一條 default 規則以 `int` 為底、不需標註）;**enum 名稱是一個值命名空間**
——`Color.Green` 指名該 variant、`Color.of(n)` 由數字反轉回來——其中 `int(v)` **讀**出 discriminant、
`E.of(n) -> E?` **反轉**回來(未知的 `n` 給 `nil`、絕不變成錯的 variant)。要指定寬度就用 opt-in layout 裝飾器
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

> **[not yet]** 這段文字說 tuple 免費就有的那兩件事,一件都沒建。tuple 上的 `==` 會被指名拒絕——上面的
> 組成繼承規則已是規格,而無名形式上的衍生尚未建置(出貨的訊息仍把原因怪在沒有宣告可掛上)。
> **解構**被拒絕得更早一步、在逗號上——`a, b := two()` 報
> _NotImplemented: `,` is not an expression this compiler reads_——所以 tuple 的結果如規範般可存、可傳,但只能用
> `.0` / `.1` 讀回來。

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

> **[deviation]** bootstrap 只對**純量**底型 `Y` 實作 `type X = Y`,而新型別**不**繼承 `Y` 的算術或 `spec` impl——
> 一個 `Celsius = int` 不先 `int(c)` 就不接受 `+`,與上面的繼承規則相反——且 `type Name = str` 目前被**拒絕**。意圖
> 中的語意(一個沿用 `Y` 整個表示與 impl 的全新身分)成立;bootstrap 這個階段只涵蓋純量、無 impl 的情形。

## 建構與封裝（Construction & encapsulation）

建立一個值的**唯一原語是 struct literal**——它會指名每個欄位，所以只在「每個欄位都可見」處才能用。所謂
**constructor 不是獨立特性**：它就是一個（通常 `pub` 的）associated function，內部回傳一個 literal。因此只要型別
有**任一私有欄位**，它對其 module 之外就是 **opaque**——struct literal 指不出私有欄位，外部只能透過那個 `pub` 函式
建構；該函式在型別自己的 module 內執行，能在**建構當下**就把型別的 invariant 立好。

> **[not yet]** struct literal **只依位置**綁定,所以那個指名欄位的形式並不存在:`P(a: 1, b: 2)` 報
> _NotImplemented: the named argument `a:` — this compiler binds arguments by position only_
> （見 [函式與 Closure](../code/functions.zh-TW.md)）。`P(1, 2)` 建出同一個值,所以建構本身不受影響;缺的是這一節
> 用來陳述自己規則的那個寫法——「它會指名每個欄位」正是私有欄位之所以 opaque 的推導起點——而下面的
> `Foo(age: 2, name: base.name)` 寫的是編譯器讀不了的形式。

欄位可見性是**讀與寫綁在一起的單一旋鈕**——`pub` 欄位可讀、且在 `mut` binding 下可寫；private 欄位兩者皆否。
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
- **剩下的是語言沒有的那種轉換**:`impl Into[Meters] for Feet`,以寫出來的 `x.into()` 呼叫。內建型別上的 `into`
  會被指名拒絕,並說出該改寫什麼。
- **泛型程式碼以它為 bound**——`fn f[T: Into[Meters]](x: T)` 可以呼叫 `x.into()`,目標由 bound 定死。**引數
  是 bound 的一部分**:一個實作了 `Into[Feet]` 的型別並不滿足 `Into[Meters]`。
- **一步,絕不串接**——`X → Y` 和 `Y → Z` 不會給你 `X → Z`。寫兩步,或自己宣告 `X → Z`。

> **[not yet]** **super-spec** 仍然會丟掉它的引數:`spec Ord: Eq[int]` 會被指名拒絕。bound 的引數只需要跟 impl
> 的**比對**,那正是這個編譯器現在做的;而 super 的引數必須先**代入**被指名 spec 自己的參數,它的簽章才能被比較,
> 而那個代入還不存在。

**運算子的兩個運算元必須已經是同一個型別。** untyped literal 會採用另一個運算元——上方的「另一個運算元」位置——
所以 `1.5 + 1` 是兩個 `float`。兩個**已定型**、型別不同的運算元是編譯錯誤,不論哪一對:`i + f` 和 `i + u` 是同一
個錯、同一個修法——把一側寫成 cast(`float(i) + f`、`int(u) + i`)。每一對都是同一條規則;沒有東西被 promote,也
沒有任何目標會被往下推進運算式。

**`T(x)` 接受的轉換**就是這些,沒有別的。它們不是 `Into` impl,從來也不是:`T(x)` 是一個內建形式,而這是它有答案
的那些對。

| from   | to      | 會 raise | 說明                              |
| ------ | ------- | -------- | --------------------------------- |
| `byte` | `int`   | 否       | 每一個 byte 都是一個 int          |
| `rune` | `int`   | 否       | 每一個 code point 都是一個 int    |
| `int`  | `float` | 否       | 不會失敗;超過 2^53 可能失精度     |
| `int`  | `byte`  | 是       | 超出範圍 → `OverflowError`        |
| `int`  | `rune`  | 是       | 不是 code point → `OverflowError` |
| `int`  | `uint`  | 是       | 負數 → `OverflowError`            |
| `uint` | `int`   | 是       | 超過有號數上限 → `OverflowError`  |

`float → int` 不在表上:丟掉小數是一個決定,所以它有自己的寫法——`int(x)`,或用 `//` 那個本來就落在整數的除法。
`byte → float` 也不在:那會是 `byte → int → float`,而一步就是一次轉換的定義——寫成兩步。

**任何型別轉成文字不在這張表上**,因為那不是這個意義下的型別間轉換:`str(x)` 透過 `display` 渲染一個值,而每個型
別都有 `display`。

**編譯器算得出來的轉換就會被算出來。** `byte(300)` 是良構的 —— 然後它以**常數**的身分失敗:值是已知的,轉換已知
會 raise,於是在編譯期報出來而不是留到執行期。可達性不參與其中;`if false { b := byte(300) }` 是同一個錯誤。它也
穿過 monomorphize 之後的泛型呼叫:對 `fn id[T](x: T) -> T` 而言,`byte(id(300))` 是同一個已知常數,在同一個編譯期
被拒絕。

**偏離字面量預設的採用是一個 lint finding**(`L502`)——`1.5 + 1` 會被報而 `1.5 + 1.0` 不會。它是建議而不是語言
規則:`1` 和 `1.0` 在紙面上就該是不同的型別,讀者不必從周圍推一個字面量是什麼。

> **[deviation]** 在這個編譯器裡,一個型別只能有**一個** `Into`,不能有好幾個。方法是用**名字**當 key 的,所以第二個
> `impl Into[…] for X` 會跟第一個相撞、並被具名拒絕。要做到好幾個,需要把 spec 方法改成用 (spec, **型別引數**) 當
> key——那正是上面那個 bound 也需要的同一件事,也正是能讓手寫的 `x.into()` 說出它指的是哪一個的東西。

一個值、一個 `Err` 或 `nil` 在有型別的位置進入 `Either`,是**包裹**規則在運作、不是轉換
（見 [Null-safety 與錯誤處理](../code/errors.zh-TW.md)):carrier 建在值的外面,值在裡面保持自己的型別——仍然是
建構,絕不是 reinterpret。
