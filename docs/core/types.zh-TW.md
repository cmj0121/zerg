# Zerg 型別（Types）

原始純量、你宣告的 `struct` / `enum` / tuple、一個值如何建構,以及一種型別如何轉換成另一種。屬於
[語言參考](../language.zh-TW.md) 的一部分。亦有 [English](types.md) 版本。

## 原始型別（Primitive Types）

一組精簡且固定的集合——三種整數寬度（signed `int`、unsigned `uint`、以及 `byte` octet）。此外的**固定寬度階梯**
（`i8`、`i16`、`u32`、`f64`……）是一組 **stdlib 型別、不是新語法**——型別名不過就是一個 identifier，所以像 `u32`
這樣的寬度只是多加一個 stdlib 型別、完全不動語法：

| 型別    | 說明                                                       |
| ------- | ---------------------------------------------------------- |
| `bool`  | `true` / `false`                                           |
| `byte`  | unsigned 8-bit——Zerg 的 char                               |
| `rune`  | 單一個有效的 Unicode code point                            |
| `int`   | signed 64-bit 整數                                         |
| `uint`  | unsigned 64-bit 整數                                       |
| `float` | IEEE-754 double（f64）                                     |
| `str`   | immutable、null-terminated 的 Unicode（不含 embedded NUL） |
| `nil`   | `T?` 的 placeholder 值                                     |

- **整數溢位與除以零會 raise**（`OverflowError`、`DivideByZeroError`）——這是一次 **abort**、不是值
  （見 [Null-safety 與錯誤處理](../code/errors.zh-TW.md)）；`int`/`uint`/`byte`/`rune` 絕不環繞（要環繞就用下方的 `+%`/`-%`/`*%`）。
- **`float` 依 IEEE-754：** 溢位 → `±Inf`、無效運算 → `NaN`，兩者都不 raise；`NaN` 與任何值（含自己）都不相等。
- **`str` 以 `rune` 迭代、不可索引**——想要原始位元組，就轉成 `list[byte]`
  （見 [Collection](../code/collections.zh-TW.md)；可能含 NUL 的二進位也用它，`str` 永遠不含 NUL）。

### 整數運算（Integer operations）

- **Bitwise**——`&`、`|`、`^`、`~`（and、or、xor、complement）與位移 `<<`、`>>`，適用 `int`/`uint`/`byte`。`>>` 對 signed
  `int` 是 **arithmetic**（補符號位）、對 unsigned `uint`/`byte` 是 **logical**（補 0）——由型別的正負號決定，所以不
  需另設 logical-shift 運算子；位移量 **≥ 型別寬度**會 raise（`OverflowError`）。這些 desugar 到 spec（user type 可
  多載——見 [內建 spec](specs.zh-TW.md)），且 bitwise **符號**永不與邏輯**關鍵字** `not`/`and`/`or` 撞臉。
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
  所以 `7.5 // 2.0` 是 `3`、`-7.5 // 2.0` 是 `-4`，不需要 `int(...)` 來回一趟。`/` 完全不變、維持型別驅動
  ——`int / int` 是 `int`、`float / float` 是 `float`——因為已具型別的 `int` 值永遠不會隱式變成 `float`
  （見下方「數值字面值」）。`//` **不會**開啟註解——Zerg 的註解以 `#` 起始。

> **[deviation]** 讓 `/` 與 `%` 成為 Euclidean 的那個修正是**無條件**產生的,並未在兩個運算元都可證明非負時
> elide——上面說的「最常見情況零成本」是意圖中的 codegen、不是今天的。語意不受影響:那是成本、不是錯的答案。

### 有型別的位置（Typed positions）

底下有好幾條規則講的都是**一個值遇上一個宣告型別** —— 什麼放得進去、字面量採用什麼、什麼會被轉換。它們需要的是同
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

**一個位置最多發生一次轉換。** 底下的規則若要把一個值轉成宣告的型別,每個位置只走一步;一個值跨過兩個位置,就可以
在各自的位置各轉一次。

**carrier 不會終結一個位置——它把位置往內移一層。** 當宣告的型別是 `T?`、`Result[T]` 或 `Either[X, Y]` 時,真正跟
值相遇的是它的 **payload**,而那個 payload 就是同一個位置:`x: int? = e` 把 `e` 放在 binding 的位置上、對著 `int`;
`return Left(e)` 則把它放在 `return` 的位置上。底下每一條規則在那裡讀到的都是 `T`,永遠不是外面那層 wrapper。

> **[deviation]** 這份清單是契約;編譯器是一個一個到達它的,而每一個它還沒被告知的位置,都是一個被靜默放進不合身型
> 別裡的值。這份清單之所以存在,正是因為那件事反覆發生:它本來是括號裡的四個例子,而那四個一次一個 miscompile 地長
> 成了十四個。一個新的語法形式欠一個「這是不是有型別的位置」的答案,而那個答案屬於這裡,不屬於第一個注意到它的規
> 則。
>
> carrier 那句話是同一個故事的裡面那一半:編譯器先把 **wrapper** 裝好,再走另一條路把 payload 降下去,而那條路上
> 沒有掛任何規則。`x: float? = i`(`i` 是 `int` 值)印出 `5`,而 `i = 300` 的 `Left(i)` 放進 `Result[byte]` 靜靜地
> 被截斷——正是同一組規則在上面一層早就拒絕的那兩個錯誤。

### 數值字面量（Numeric literals）

數值字面量是 **untyped** 的——它採用 context 要求的型別，在上面**任何一個有型別的位置**，並在**編譯期**檢查。無 context 時，整數字面量預設為 `int`、帶小數/指數的字面量（`1.0`、`1e3`）
預設為 `float`；非十進位 `0x…` / `0o…` / `0b…` 也是普通整數字面量。

- 字面量**放不進**要求的型別 → **compile error**（`byte = 300`、`uint = -1`、超過 i64 的 `int` 字面量）——不是
  runtime overflow。這是 [`Into`](#into--自己會發生的那個轉換) 的**常數**那一半:目標的型別是已
  知的、值也是已知的,所以答案現在就是已知的。

- **有型別的 `float` context 接受一個 untyped 整數字面量**:`x: float = 1` 合法,而 `x: float = i`(`i` 是 `int`
  值)同樣合法——前者是採用、後者是轉換,而 `int → float` 正是內建 `Into` 之一。它們的差別在於答案**何時**得出,而
  不在於允不允許:字面量在編譯期定案,值在執行期。帶小數或指數的字面量(`1.0`、`1e3`)從一開始就是 `float`、絕不
  是 `int`。

- **字面量是採用,值是轉換,而這兩者值得分清楚。** `b: byte = 5` 寫下一個 byte,完全沒有轉換;`b: byte = n`(`n`
  是 `int` 值)寫下的是那個轉換,而它可能 raise。所以 `b: byte = 300` 是 **compile error**——已知這個常數放不進去
  ——而 `n == 300` 時的 `b: byte = n` 是執行期的 **`OverflowError`**。同一條規則,兩個時刻。

  每一個這樣的轉換都是一個 **lint** 發現(`L5xx`),因為讀 `xs: list[byte] = [1, 2]` 的人應該在頁面上看見 byte,
  而不是從宣告去推斷它。

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
以及編譯器的結構化 `Eq` / `Ord` / `Hash` / … 衍生（見 [Spec 與 Generics](specs.zh-TW.md)）——但因為沒有名字可掛，**沒有 inherent
method、也沒有自己的 `spec` impl**：一旦某個值需要行為、nominal 身分、或值得閱讀的欄位名，就改用具名 `struct`。
tuple 的結果是 **first-class**——可存、可傳、可解構——所以多重回傳不需要任何額外機制（見 [模式比對](../code/control-flow.zh-TW.md)）。

**`type X = Y`** 定義一個**全新、獨立的型別**——不是透明 alias。`X` 承接 `Y` 的表示與實作（它的欄位或 variant、
以及它的 `spec` impl,現在 `This` = `X`),但是一個**獨立身分**:`X` 與 `Y` 是**不同型別、即使結構完全相同**,而且
兩者間**不能 cast**——要轉換就 **re-construction**(`X(y)` / `Y(x)`),與任何轉換一樣。一個**單型**的 `type X = Y`
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

轉換**預設顯式**——`int` 不是 `bool`；要轉就用 constructor 風格呼叫建一個（`bool(8)`、`int(c)`）。primitive 之間的
轉換由**編譯器內建**；使用者型別不能對 primitive 加。

**窄化一個 primitive** 可能丟失值，因此比照算術檢查：

- 整數轉換的值**放不進**目標就 raise（`OverflowError`）——`byte(300)`、`uint(-5)`、對超過 i64 的 `uint` 做
  `int(u)`。**checked** 版是 `guard { byte(x) }` → `Result`；要**截斷**到低位就先遮罩——`byte(x & 0xFF)` 一定 fit、
  所以不會 raise。saturating 延後。
- **`float` → 整數**捨去小數（`int(3.7)` 是 `3`——是本意、非 bug），但當整數部分**超出範圍**、或 float 是
  `NaN` / `±Inf` 時 raise。

### `Into` —— 自己會發生的那個轉換

`T(x)` 是你**寫出來**的轉換。`Into` 是在**目標型別已經知道的地方**發生的那一個 —— 也就是在一個
[有型別的位置](#有型別的位置typed-positions) —— 而它不過就是編譯器幫你寫了 `.into()`:

```text
x: float = 1.5 + 1          # 你寫的
x: float = 1.5 + 1.into()   # 它的意思
```

**`Into` 是一個普通的 spec**,所以一個型別靠實作它來加入,而內建型別已經實作了:

```text
spec Into[T] {
    fn into() -> T
}
```

- **它可能 raise,而由 caller 處理。** 縮小會掉值 —— `int → byte` 不可能總是成功 —— 所以內建的數值實作 raise
  `OverflowError`,跟 `byte(300)` 今天 raise 的是同一個失敗、同一個名字。使用者的實作 raise 什麼由它自己的理由決
  定;`ValueError` 是自然的那個,而 `OverflowError` 是它的一種。
- **一步,絕不串接** —— `X → Y` 和 `Y → Z` 不會給你 `X → Z`。寫兩步,或自己宣告 `X → Z`。
- **每個位置一步。** 一個值跨過兩個位置就可以在各自的位置各轉一次 —— 這正是 `demo: Z = x + y` 對 `x: X`、`y: Y`
  合法的原因:`x` 在運算元位置到達 `Y`,而那個和在 binding 位置到達 `Z`。那是兩個位置,不是一條 chain。
- **`.into()` 需要一個目標。** `x := 1.into()` 是編譯錯誤 —— 那裡沒有任何東西能說出是哪一個 `Into`。手寫的
  `.into()` 合法的地方,恰好就是編譯器會幫你寫的地方。

**一個運算式的型別,只由它的運算元決定** —— 永遠不由它要被指派給誰決定。所以是兩個運算元先談攏,然後結果才去見宣
告的型別:

- 型別**相同**的運算元維持那個型別,什麼都不轉。
- **不同**的取**兩者都能一步到達的最大型別** —— 最大指的是值集合包含另一個的那個,而那也正是不會失敗的方向。
  `1.5 + 1` 是 `float`,因為 `int → float` 存在而 `float → int` 不存在。
- 如果沒有這樣的型別,或有兩個而彼此互不包含,這個運算式的型別就是 **undetermined** —— 編譯錯誤,轉換必須寫出來。

因為目標永遠不往下推,`demo: Z = x + (y + z)` 可能是錯誤而 `demo: Z = x + y + z` 不是:括號改變了哪兩個運算元先相
遇,而每一次相遇都是各自解析的。

內建的實作就是這些,沒有別的 —— 不在表上的一對,用 `T(x)` 轉:

| from   | to      | 會 raise | 說明                               |
| ------ | ------- | -------- | ---------------------------------- |
| `byte` | `int`   | 否       | 每一個 byte 都是一個 int           |
| `rune` | `int`   | 否       | 每一個 code point 都是一個 int     |
| `int`  | `float` | 否       | 不會失敗;可能失精度,而 `L5xx` 會說 |
| `int`  | `byte`  | 是       | 超出範圍 → `OverflowError`         |
| `int`  | `rune`  | 是       | 不是 code point → `OverflowError`  |
| `int`  | `uint`  | 是       | 負數 → `OverflowError`             |
| `uint` | `int`   | 是       | 超過有號數上限 → `OverflowError`   |

**`int` 與 `uint` 不混用**,而這是掉出來的結果、不是一條自己的規則:兩個方向都存在,但兩者的值集合互不包含,所以
`i + u` 沒有最大型別,是 undetermined。把其中一邊轉掉 —— `int(u) + i` 或 `u + uint(i)`。

沒有 `float → int`:丟掉小數是一個決定,所以要寫出來(`int(x)`,或用 `//` 那個本來就給整數的除法)。也沒有
`byte → float` —— 那會是 `byte → int → float`,正是一步規則禁止的那條 chain。

**編譯器算得出來的轉換就會被算出來。** `x: byte = 300` 是良構的 —— `int → byte` 存在,所以型別檢查過 —— 然後它以
**常數**的身分失敗:值是已知的,轉換已知會 raise,於是在編譯期報出來而不是留到執行期。可達性不參與其中;
`if false { b: byte = 300 }` 是同一個錯誤。

**每一次隱式轉換都是一個 lint finding**(`L5xx`),含字面量 —— 所以 `1.5 + 1` 會被報而 `1.5 + 1.0` 不會。它是建議
而不是語言規則:重點在於 `1` 和 `1.0` 在紙面上就該是不同的型別,讀者不必從周圍推一個字面量是什麼。

> **[deviation]** `L5xx` 這一族**尚未實作**。上面每一個轉換都會發生;沒有任何一個會被回報。`Into` 的 spec 那一面
> 也還沒有:`impl Into[T] for S` 不被接受,所以內建矩陣就是它的全部,而手寫的 `x.into()` 不是這個編譯器認得的呼叫。

這也是讓一個值、一個 `Err` 或 `nil` 能在有型別的位置直接注入 `Either`、無需明確包裝的機制
（見 [Null-safety 與錯誤處理](../code/errors.zh-TW.md)）——仍是建構出目標值，絕非 reinterpret。
