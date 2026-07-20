# Zerg 型別（Types）

原始純量、你宣告的 `struct` / `enum` / tuple、一個值如何建構,以及一種型別如何轉換成另一種。屬於
[語言參考](language.zh-TW.md) 的一部分。亦有 [English](types.md) 版本。

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
  （見 [Null-safety 與錯誤處理](errors.zh-TW.md)）；`int`/`uint`/`byte`/`rune` 絕不環繞（要環繞就用下方的 `+%`/`-%`/`*%`）。
- **`float` 依 IEEE-754：** 溢位 → `±Inf`、無效運算 → `NaN`，兩者都不 raise；`NaN` 與任何值（含自己）都不相等。
- **`str` 以 `rune` 迭代、不可索引**——想要原始位元組，就轉成 `list[byte]`
  （見 [Collection](collections.zh-TW.md)；可能含 NUL 的二進位也用它，`str` 永遠不含 NUL）。

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

### 數值字面量（Numeric literals）

數值字面量是 **untyped** 的——它採用 context 要求的型別（typed binding `x: uint = 5`、typed 參數、`return`、或與它
運算的另一個 typed 值），在**編譯期**檢查。無 context 時，整數字面量預設為 `int`、帶小數/指數的字面量（`1.0`、`1e3`）
預設為 `float`；非十進位 `0x…` / `0o…` / `0b…` 也是普通整數字面量。

- 字面量**放不進**要求的型別 → **compile error**（`byte = 300`、`uint = -1`、超過 i64 的 `int` 字面量）——不是
  runtime overflow。
- **整數與 float 分開**：整數字面量絕不變成 `float`；要 float 就寫 `1.0` 或 `float(1)`（沒有隱式 int→float，那會
  悄悄失精度）。

## 使用者定義型別（User-Defined Types）

宣告你自己的**積型別**（`struct`）與**和型別**（`enum`），兩者都可對 `[...]` 泛型化。

**可見性（`pub`）**——每個宣告（型別、欄位、函式）**預設 private，只在自己的 module 內可見**；在前面加 `pub`
才會匯出、供他處使用。mutability 是另一條獨立的軸、**不**在這裡宣告：它屬於**實例（instance）**（也就是
binding；見 Values & Memory），絕不屬於欄位或型別。module 與 package 是什麼，以及可見性、coherence、entry point
如何跨越它們，見 [Module、Package 與 Program](package.zh-TW.md)。

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

其實 `Either`、`Result[T]`、`T?` 並不特殊——它們就是建立在 `enum` 上面的普通 stdlib 型別（見 [Null-safety 與錯誤處理](errors.zh-TW.md)）。一個 `enum`
的 **variant 隨型別的可見性**——`pub enum` 公開它的每一個 variant（可建構、可 `match`）；沒有 per-variant 的私有。

一個 `enum` 的 **discriminant 對「fieldless enum」與「payload enum」行為不同**——分界在於是否*每一個* variant 都無
欄位。一個 **fieldless** `enum` 可以給某個 variant 明確的 `= <discriminant>`——一個 **compile-time 常數整數**、
在各 variant 間互異（未指定者為前一個 `+ 1`、從 `0` 起算）——使它成為 **C 式整數 enum**：`variant = <int>`。這種
enum 有**原生、C 相容的整數 repr**（依一條 default 規則以 `int` 為底、不需標註）；`int(v)` **讀**出該值，
`E.of(n) -> E?` **反轉**回來（未知的 `n` 給 `nil`、絕不變成錯的 variant）。要指定寬度就用 opt-in layout 裝飾器
`#[repr]`；序列化/wire 形式則是 `Encode` / `Decode` impl、絕不是裝飾器。

一個 **payload** `enum`（任一 variant 帶欄位）則保持其 **tag opaque、只可 match**——不允許 `= 5`，你 `match` 的是
variant、絕不是 tag。要把這種 variant 綁定某個特定整數，就寫**顯式轉換**：一個從 variant 到數字的 `match`，再一個
回來、且**帶驗證**。這又是*convert by re-construction, never reinterpret*——數字是從 variant **建**出來的、不是把
tag 的 bytes 重讀——而且它天然吸收 baked-in 值給不了的不連續值、別名與版本演進。

一個 **tuple**——`(int, str)`，欄位以位置存取 `.0`、`.1`——不過就是一個**匿名 `struct`**：同一個積型別，只是不具名、
供一次性的位置束用（多重回傳、`divmod -> (int, int)`）。因為匿名，它是全語言**唯一結構化定型**的形式——`(int, str)`
不管寫在哪都是同一個型別，而每個具名 `struct` 與 `enum` 仍是 **nominal**。它沿用整套積型別機制——copy-by-value、
以及編譯器的結構化 `Object` / `Ord` / `Hash` / … 衍生（見 [Spec 與 Generics](specs.zh-TW.md)）——但因為沒有名字可掛，**沒有 inherent
method、也沒有自己的 `spec` impl**：一旦某個值需要行為、nominal 身分、或值得閱讀的欄位名，就改用具名 `struct`。
tuple 的結果是 **first-class**——可存、可傳、可解構——所以多重回傳不需要任何額外機制（見 [模式比對](control-flow.zh-TW.md)）。

**`type X = Y`** 定義一個**全新、獨立的型別**——不是透明 alias。`X` 承接 `Y` 的表示與實作（它的欄位或 variant、
以及它的 `spec` impl,現在 `This` = `X`),但是一個**獨立身分**:`X` 與 `Y` 是**不同型別、即使結構完全相同**,而且
兩者間**不能 cast**——要轉換就 **re-construction**(`X(y)` / `Y(x)`),與任何轉換一樣。它可以泛型
(`type Result[T] = Either[T, Err]`)。這是 **strong-typedef** 工具——一個 `UserId`,行為像 `int`、卻永遠不能被當作
一個裸 `int` 或 `ProductId` 傳進去——並與單欄位 struct 的 **newtype** 有別:newtype 是把值*包*進一個新欄位、配全新
impl,而非沿用整個形狀。prelude 的 **`Result[T]`** 與 **`T?`** 正是它在 `Either` 上的實例,這也是為什麼它們彼此不同、
要用 `ok_or` / `ok` 顯式跨越。

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
型別操作因此清楚分開：**建**一個新值（`T(x)`，這裡）、**測**一個 existential 的身分（`x is T` → `bool`，見 [型別測試](specs.zh-TW.md)）、
以及**絕不**把一種型別的儲存重新詮釋成另一種。

轉換**預設顯式**——`int` 不是 `bool`；要轉就用 constructor 風格呼叫建一個（`bool(8)`、`int(c)`）。primitive 之間的
轉換由**編譯器內建**；使用者型別不能對 primitive 加。

**窄化一個 primitive** 可能丟失值，因此比照算術檢查：

- 整數轉換的值**放不進**目標就 raise（`OverflowError`）——`byte(300)`、`uint(-5)`、對超過 i64 的 `uint` 做
  `int(u)`。**checked** 版是 `guard { byte(x) }` → `Result`；要**截斷**到低位就先遮罩——`byte(x & 0xFF)` 一定 fit、
  所以不會 raise。saturating 延後。
- **`float` → 整數**捨去小數（`int(3.7)` 是 `3`——是本意、非 bug），但當整數部分**超出範圍**、或 float 是
  `NaN` / `±Inf` 時 raise。

**使用者型別**可 opt-in 一個對另一型別的**自動重新建構**，靠兩條規則保持可判定：

- **只做單步**——絕不串接（`X → Y`、`Y → Z` ⇏ `X → Z`）；單步、單一明確目標，不會出現多路徑歧義。
- **只在目標型別明確處觸發**——有型別標註的 binding（`x: X = y`）、`return`、或有型別的參數；不會在推斷型別的
  `:=` 上發生。

這正是讓一個值、一個 `Err` 或 `nil` 能在 typed binding 或 return 處直接注入 `Either`、無需明確包裝的機制
（見 [Null-safety 與錯誤處理](errors.zh-TW.md)）——仍是建構出目標值，絕非 reinterpret。
