# Zerg 語言參考（Language Reference）

[README](../README.zh-TW.md) 中設計原則背後的詳細語意。亦有 [English](language.md) 版本。

## 原始型別（Primitive Types）

一組精簡且固定的集合——三種整數寬度（signed `int`、unsigned `uint`、以及 `byte` octet），此外**沒有固定寬度階梯**
（`i8`、`i16`、`u32`……都不存在）：

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
  （見 Null-safety）；`int`/`uint`/`byte`/`rune` 絕不環繞（要環繞就用下方的 `+%`/`-%`/`*%`）。
- **`float` 依 IEEE-754：** 溢位 → `±Inf`、無效運算 → `NaN`，兩者都不 raise；`NaN` 與任何值（含自己）都不相等。
- **`str` 以 `rune` 迭代、不可索引**——想要原始位元組，就轉成 `list[byte]`
  （見 [Collection](collections.zh-TW.md)；可能含 NUL 的二進位也用它，`str` 永遠不含 NUL）。

### 整數運算（Integer operations）

- **Bitwise**——`&`、`|`、`^`、`~`（and、or、xor、complement）與位移 `<<`、`>>`，適用 `int`/`uint`/`byte`。`>>` 對 signed
  `int` 是 **arithmetic**（補符號位）、對 unsigned `uint`/`byte` 是 **logical**（補 0）——由型別的正負號決定，所以不
  需另設 logical-shift 運算子；位移量 **≥ 型別寬度**會 raise（`OverflowError`）。這些 desugar 到 spec（user type 可
  多載——見內建 spec），且 bitwise **符號**永不與邏輯**關鍵字** `not`/`and`/`or` 撞臉。
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

## 型別（Types）

宣告你自己的**積型別**（`struct`）與**和型別**（`enum`），兩者都可對 `[...]` 泛型化。

**可見性（`pub`）**——每個宣告（型別、欄位、函式）**預設 private，只在自己的 module 內可見**；在前面加 `pub`
才會匯出、供他處使用。mutability 是另一條獨立的軸、**不**在這裡宣告：它屬於**實例（instance）**（也就是
binding；見 Values & Memory），絕不屬於欄位或型別。module 與 package 是什麼，以及可見性、coherence、entry point
如何跨越它們，見 [Module、Package 與 Program](package.zh-TW.md)。

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

其實 `Either`、`Result[T]`、`T?` 並不特殊——它們就是建立在 `enum` 上面的普通 stdlib 型別（見 Null-safety）。一個 `enum`
的 **variant 隨型別的可見性**——`pub enum` 公開它的每一個 variant（可建構、可 `match`）；沒有 per-variant 的私有。

一個 **tuple**——`(int, str)`，欄位以位置存取 `.0`、`.1`——不過就是一個**匿名 `struct`**：同一個積型別，只是不具名、
供一次性的位置束用（多重回傳、`divmod -> (int, int)`）。因為匿名，它是全語言**唯一結構化定型**的形式——`(int, str)`
不管寫在哪都是同一個型別，而每個具名 `struct` 與 `enum` 仍是 **nominal**。它沿用整套積型別機制——copy-by-value、
以及編譯器的結構化 `Object` / `Ord` / `Hash` / … 衍生（見 Spec 與 Generics）——但因為沒有名字可掛，**沒有 inherent
method、也沒有自己的 `spec` impl**：一旦某個值需要行為、nominal 身分、或值得閱讀的欄位名，就改用具名 `struct`。
tuple 的結果是 **first-class**——可存、可傳、可解構——所以多重回傳不需要任何額外機制（見模式比對）。

## 模式比對（Pattern matching）

`match` 是一個 **expression**：它用 **arm**（`pattern -> result`）逐一試一個值，跑第一個命中的、產出它的 result。
每個 arm 產出**相同型別**，所以 `match` 是個值，可用於 `:=`、`return`、或引數——產出 `nil` 的 arm 讀來就是普通
statement。覆蓋算**建議、不強制**——你漏掉某個 case，頂多是個 **warning**（linter 可加嚴），**不是編譯錯誤**——
所以**新增一個 `enum` variant 永不破壞 dependent 的 `match`**。結尾的 **`_`** 收其餘；一個值落到沒有 arm 覆蓋的
`match` 會在**執行期 abort**（`MatchError`），而**多餘**的 arm（已被前面 arm 覆蓋者）同樣是 warning。

一個 **pattern** 是下列之一：**帶 payload 綁定的 variant**（`Left(v)`）——以 **copy** 綁定，一如 `?`/`return`、來源
永不失效；**literal**（`0`、`"y"`、`true`）——以 `equal` 比對；**nested** pattern（`Left(Some(v))`）；**or-pattern**
（`A | B ->`，各分支綁同名、同型）；或**萬用 `_`**，比對任何值、不綁定。

```text
msg := match ev {
    Click(p)           -> render(p)
    Key(k) | Scroll(k) -> handle(k)
    _                  -> nil
}
```

`match` 的 **pattern** 永不窺看 existential 的真實型別——spec 當型別用是單向抹除、無 downcast——它只解構 variant、比對
值，如此而已；它對 existential 唯一允許的，是布林的 **`is`** 測試（見 Spec 與 Generics），用作**條件**、絕不作為交回
具體值的綁定。一個 **product pattern** 能**依欄位**解構一個 `struct`（`Div{q, r}`）、或**依位置**解構一個 tuple（`(a, b)`），每一
部分以 copy 綁定；它在 `match` arm 與普通的 `:=` 綁定（`(q, r) := divmod(x, y)`，也就是多重回傳被消費的方式）都可用。
**guard 條件**（`Left(v) if v > 0`）仍延後。

## Spec 與 Generics（Specs & Generics）

行為分成**兩層**。型別可定義 **inherent method**——自有行為，只有握著具體型別時才能用。而**抽象**一律透過
**`spec`**：一個具名的行為介面——method 簽名，其中有些帶 **default body**（見下），且**永不含 field**。滿足是
**nominal**：型別必須**明確宣告**它實作某 `spec`，且每組 **(型別, spec) 只有一個正規 impl**——**帶參數**的 spec 會把
參數算進這組，所以 `Iterator[int]` 與 `Iterator[str]` 是不同組、各有自己的正規 impl（見下方「解析帶參數的 spec」）。

`spec` 是抽象行為的**唯一**機制，因此它扮演三個角色——泛型參數的 **bound**、型別所 **conform** 的介面、以及
（見下）**當成型別本身**。內建行為也都是 spec、不是編譯器魔法：`Err` 就是 `Error` spec，相等、排序、雜湊、迭代、
以及 opt-in 的轉換都是普通 stdlib spec。型別的 inherent method 不必隸屬任何 spec；**唯有 spec 所保證的，才可被抽象**。

**spec bound 就是泛型型別的完整介面。** 在泛型於 `T` 的程式碼裡，對一個 `T` 值唯一能用的操作，就是它 spec bound
所宣告的 method——它的欄位與任何 inherent method 都不可見。因此：

- **空的 `spec`** 是合法的 bound、被所有型別滿足，但它保證**零**行為：這種 `T` 只有 memory model 給的**結構能力**
  ——copy 它、`del` 它、當參數傳、存起來、送進 channel——連一個 method 都沒有。
- **`Object`** 是頂層 `spec`，被每個型別**自動實作**。它提供一組最小的 method——`equal`、`copy`、`debug`……由結構
  逐欄位 **auto-derive**（含 `Ref` 值則 refcount++，與 copy 規則一致），外加 `display`——其預設 body 就是 `debug`
  （見 Formatting & text）。型別可**明確覆寫**其中任何一個（例如不計順序的 `equal`），否則沿用衍生版本。因為每個型別都實作 `Object`，`T: Object` 這個 bound **從不縮小**
  可接受的型別集——它只是解鎖那些 method。這套 compiler 擁有的**結構化衍生**可 opt-in 延伸到 `Ord` /
  `Hash` / `Encode` / …——見 [Derive 與預設行為](derive.zh-TW.md) 參考。

`spec` 也可**當型別用**，不只是 bound：spec-typed 的值可持有任何實作它的型別——heap-boxed、single-owner、
scope-owned，並**動態 dispatch**（實際要跑哪個 method，在執行期依值的真實型別決定）。抹除是**對值單向**的——一旦
boxed，具體值就被隱藏、**永遠無法還原**（不能 downcast、不能 reinterpret；要拿到具體型別只能「一開始就留著」、無從
反抹除）。它的**身分**是另一回事：**`x is T`** 問「這個 boxed 值的具體型別是不是 `T`」、產出一個純 **`bool`**——這個
測試讀的是 box 本就帶著的 dispatch 身分，**既不還原值、也不讀結構**（見下方「型別測試」）。

在一個 boxed 值上，**unary** 操作會 dispatch 到真實型別、可用：它的 spec method，加上 `copy`（產生一個獨立的新
box——內含 `Ref` 值 refcount-bump）與 `debug`，以及結構性記憶體操作（`del`、傳參、存欄位、送 channel）。但
**binary same-type** 操作——`equal` / `==`、`Ord` 比較、以及因此的 `Hash` keying——**不可用**：它們的 `other: This`
運算元正是抹除掉的具體型別，而 `is` 只測身分、絕不把值交回來供給它。兩個 boxed 值因此**永遠不能以值比較**，與單向
抹除一致。box 一個值是為了動態 dispatch 它的 spec method；要比較、排序或當 key，就留著具體型別（monomorphized 的
`[T: S]` bound）。

同一道關卡也落在另外兩類成員上：spec 的 **associated function**（`default() -> This`、`zero()`——無 receiver，box
沒有可供分派的 _起點_）與它的**泛型 method**（vtable 每個型別一格、而非每個型別實參一格）。兩者都需要一個**具名的
具體型別**，所以在 **existential 上**各自是 compile error——這不是禁止該 spec 當型別，只是禁止那一個呼叫，和 binary
op 完全一樣。因此**沒有 object-safety gate**：一個 spec **永遠可以當型別**，box 就只提供「單憑 `this` 就能分派」的
東西——並把回傳 `This` 的結果 re-box 成同一個 spec。

concrete bound 的 generic 會在產出的 C 裡 **monomorphize**——編譯器為每個具體型別各生成一份特化版本——而把 `spec`
當型別用是唯一改用 dynamic dispatch 之處。concrete type 之間**沒有 subtyping**，所以泛型是**不變（invariant）**
的：`list[Cat]` 不是 `list[Animal]`——要抽象一整族就用 spec bound（`[T: X]`），而非 subtype 代換。

一個**實作**（型別滿足某 spec）本身不帶可見性標記：coherence 要求一組 `(型別, spec)`（含參數）到處都解析到同一個實作，
因此實作既不能被藏、也不能被複製——它的作用範圍恰好是「型別與 spec 同時可見之處」。實作是為**具體或泛型型別**寫的
（`list[T]` 可以實作 `Iterator`）；以 bound 為條件、涵蓋「所有滿足某 spec 的型別」的 blanket 實作**不提供**，以保持
解析可判定。唯一「所有型別都有」的情況是 `Object`，由編譯器 auto-derive、而非使用者手寫。

因為 spec 是 nominal，兩個各自獨立宣告的 spec 可能撞用同一個 method 名。型別仍可同時實作兩者、並各別當其一使用——
歧義只存在於「同一個值必須**同時**滿足兩者」之處（`T: X + Y` 的 bound、或型別為 `X + Y` 的值）。Zerg 在編譯期**拒絕
這個組合**，而不引入 fully-qualified 呼叫語法來消歧；要讓一個 method 被多個 spec 共用，就讓它們**源自同一個共享
spec**。spec 可跨 package 邊界實作到什麼程度、以及 coherence 如何維持全域唯一，見
[Module、Package 與 Program](package.zh-TW.md)。

**在具體值上解析出的名字必須恰指一個 method**——同一條反歧義規則,現在落在具體呼叫。**inherent method 不得與型別
所實作的任何 spec method 撞名**:在 impl 處 compile error。想給型別「自己版本的 spec method」,就**override** 它
(dispatch 仍 canonical);inherent method 是給「*不屬於*任何 spec 的行為」用的,所以撞名是誤用、不是要去排優先序。
而當一個型別實作了兩個撞名的 spec,實作本身沒問題——但裸的 **`x.foo()` 就有歧義、被拒**;你把靜態脈絡收窄到單一
spec 來解(單 spec 的 `[T: X]` bound、或 spec-typed 值),**絕不靠 qualify 呼叫**——正如上面 `T: X + Y` bound 被拒。

**spec 是扁平的——沒有 super-spec。** 一個 `spec` 絕不要求另一個；需要同時具備多種能力，是在**用處**以組合 bound
說出——`[T: Ord + Hash]`，`+` 讀作「而且」——絕不把它 baked 成一個 spec 的 supertrait。別的語言倚賴的那一條階層
`Ord: Eq`，在這裡是多餘的：相等性來自普世的 `Object`、恆在，無須被要求。implied bound 與跨 spec 的 default-body
重用是刻意放棄的——被多個 spec 共用的能力，就自成一個 spec、在 bound 裡與它們並列。

### 解析帶參數的 spec（Resolving a parameterized spec）

因為帶參數 spec 的參數是實作身分的一部分，一個型別可以同時實作 `Iterator[int]` 與 `Iterator[str]`。編譯器接著用
「為 untyped literal 定型」的同一套機制**解析某次使用指的是哪一個**：它挑出**唯一**能讓該值的所有約束——**包含它在
後續 body 裡如何被使用**——都通過型別檢查的候選。

```text
for x in y {          # y 同時實作 Iterator[int] 與 Iterator[str]
    z := 10           # untyped literal → int
    print x + z       # x + int 只有在 x 為 int 時才通過 → 選 Iterator[int]
}
```

三種結果，比照 literal 定型、但**沒有預設退路**：

- **恰好一個**候選通過 → 解析完成、免標註；
- **零個** → 普通型別錯誤（就像對只實作 `str` 的 iterator 寫 `x + str`）；
- **兩個以上** → **硬編譯錯、要你標註**（`for x: int in y`）。它**絕不**降級成警告、也不由預設挑一個：不像未覆蓋的
  `match`（其退路是響亮的 `MatchError`），一個解析錯的實作**沒有安全退路**——會默默跑錯的碼。

不同的具體參數彼此永不重疊，所以解析是明確定義的；唯一的問題只是「多個都命中時，這次指的是哪一個」。因此慣例仍是
**一型別一實作**——多個是 power tool，任何 body 定不出來的使用都要標註。concrete-bound 泛型直接指名參數
（`[I: Iterator[int]]`）、或當場綁定它（`[I: Iterator[T]]` 把 `T` 綁成該 iterator 的 element），所以 **bound 永遠不
歧義**——只有「對一個有多個實作的值做裸使用」才會。

### 型別測試（Type tests）——`is`

existential 藏起值、但沒藏起**身分**：**`x is T`** 問「這個 boxed 值的具體型別是不是 `T`」、產出一個 **`bool`**。它是
純查詢——讀每個 existential box 本就帶著的 dispatch 身分，**不還原任何值、不讀任何欄位**——所以它不是 downcast、也不
為語言添加任何 reinterpret（見型別轉換）。`T` 必須實作 `x` 所定型的那個 spec，否則測試靜態上不可能、會被拒；對一個
具體型別**已知**（非 existential）的值，答案是編譯期常數。

因為 `is` 永不交出具體值，它只能驅動**控制流、不是資料存取**：你可以就「它是不是 `T`」分支，但要讀 `T` 自己的欄位，
你必須**一開始就握著具體型別**、從未 box 它。它就是個普通 `bool`——用在 `if`、搭 `not` / `and` / `or`、或當 `match`
guard——不需要任何新的 pattern 形式。它的主要用途是對**被抹除的錯誤**依型別分派（見 Null-safety 與錯誤處理）。

### Method、`this` / `This` 與 default body

一個 **method** 是帶 **receiver** 的函式——被呼叫的那個實例，名為 **`this`**；receiver 自身的型別是 **`This`**。
`This` 在「具體型別尚未知」處指「**實作它的那個型別**」——同型別的運算元（`less(this, other: This) -> bool`）、或
**associated function** 的結果（`default() -> This`，也就是 constructor——它沒有 receiver，所以也沒有 `this`）——並在每個實作裡
解析成具體型別。generic `spec` 參數（`Iterator[T]`）是**另一件事**：一個自由選的型別（element、異型別運算元）；
`This` 則是被逼定的 self-type，永非選擇。

spec 的 method 分兩種：

- **required**——只有簽名、無 body；每個 implementer 都必須供給。
- **provided**——簽名**帶 default body**，用 required（及其他 spec）method 作用在 `this` 上定義、碰不到 field。
  implementer **沿用**它、或以特化版**覆寫**（例如更快的 `contains`）；覆寫仍須維持慣常語意，且 `(型別, spec)` 的
  實作無論如何都保持 canonical。

於是一個只有 1 個 required method 的 spec，能免費給 implementer 一堆衍生 method——`Iterator` 由 `next` 衍生
`map`、`filter`、`count`……——而「spec bound 就是完整介面」這條規則便讓它們**全部**（required 與 provided）都能對
被 bound 的 `T` 呼叫。這些 provided default 都是**行為性**的——寫在 method 上、不碰 fields；另一個由 compiler
讀取型別結構來生成 impl 的**結構性**層，見 [Derive 與預設行為](derive.zh-TW.md) 參考。

一個 method 或 function 可帶**自己的型別參數**、疊加在 receiver 上面：`map[U](this, f: fn(T) -> U)` 在 spec 的
`T` 與 receiver 的 `This` 之外再加一個 `U`，每個具體組合各 **monomorphize** 一份。provided method 也能泛型——這
正是讓 adapter 能改變 element 型別（`T` → `U`）的關鍵。

**dispatch 一致。** 每個 spec method，不論 required 或 provided，都解析到該型別的 **canonical impl**——有覆寫用
覆寫、否則用 default。所以一個 default body 呼叫另一個 spec method 時，會叫到型別的覆寫（用 `next` 定義的 default
`count`，會用被覆寫的 `next`）；**default 沒有靜態分派的例外**。機制沿用既有——concrete-bound generic
**monomorphize** 到實際 impl，spec 當型別用則經 **vtable** 分派到實際 impl。

### 型別常數（Type constants）

一個 **`const`** 可以隸屬於某**型別**，與它的 method 並列宣告，並以 `Type.NAME` 讀取（在型別內是 `This.NAME`）。
它的值是一個**常數運算式**——literal、另一個 `const`、或它們的摺疊算術——compiler **在編譯期直接代入**、不執行任何
程式碼、**無副作用**（比 top-level `const` 更嚴，後者可在 `main` 前計算一次）。它可以型別為 `This`，讓型別擁有自己的
canonical 值（`const ORIGIN: This = Point{ x: 0, y: 0 }`）。身為編譯期常數，`int` 型別的那種**在任何需要編譯期
常數之處都能用**——包括定長陣列的大小（`[byte; Buffer.SIZE]`，見 [Collections](collections.zh-TW.md)）。可見性就是
一般的 `pub` / private 旋鈕，如同 field 或 method。

`const` 隸屬於**具體型別、絕不在 `spec` 裡**：spec 抽象的是**行為**，而一個 const——已摺疊、具體、沒有東西可分派
——不是行為。一個 spec 必須保證的「每型別的*值*」因此是一個 **method**，即無 receiver 的 associated function
`fn max() -> This`、而非 const——泛型端以 `T.max()` 取用。

### 內建 spec（Built-in specs）

多數是 **opt-in**——型別實作它才取得——除了 `Object`
**為每個型別 auto-derive** 的那組（皆可 override）：

| `Object` method | 驅動                 | 說明                                                  |
| --------------- | -------------------- | ----------------------------------------------------- |
| `copy`          | copy-by-value        | 由記憶體模型**強制**——永不缺席                        |
| `equal`         | `==` / `!=`          | **結構性**；channel 或 `fn` 以 identity 比            |
| `debug`         | logging、stderr      | 開發者取向；**auto-derive**、可 override              |
| `display`       | `f"…"`、對使用者顯示 | 給人看；**預設 body 就是 `debug`**、要漂亮就 override |

Zerg **不設兩值之間的 instance-identity 測試**：copy-by-value 下值的副本本就是不同 instance、且無 aliasing，
「同一個 instance？」只對 channel 有意義——太 narrow、不值得一個運算子。相等永遠是**結構性**的 `equal`。**`is`**
關鍵字問的是另一件事——existential 上的**型別身分**測試 `x is T`（見型別測試）——「這裡 box 的是哪個具體型別？」，
而非「這兩個是不是同一個值？」。

**Opt-in**——實作該 spec 才取得能力；泛型 bound 以它把關：

- **`Ord`**——`<` `<=` `>` `>=`、sort、min/max：一個 **total** order，與 `equal` 一致。`str` 依 **code point
  字典序**排序（＝ byte 序，因其 UTF-8 有效——非 locale collation，那是另一個 stdlib 功能）；`float` **不**實作。
- **`Hash`**——`map` / `set` 的 key，`equal ⇒ same hash`。`str` 不可變、是天然的 key；`float` **不**實作。
- **`Iterator`** / **`Iterable`**——迭代協定（見下方 **迭代**）。
- **`Error`（`Err`）**——錯誤層：`message() -> str`、`unwrap() -> Err?`（底層 cause、無則 `nil`）、
  `code() -> byte?`（可選小碼）。
- **`Add` / `Sub` / `Mul` / `Div` / … 與 bitwise `BitAnd` / `BitOr` / `BitXor` / `Not` / `Shl` /
  `Shr`**——值運算子（`+ - * / %`、`& | ^ ~ << >>`、indexing…）：運算子多載，見下。`str` 實作 `Add`，所以 `+` 會
  **串接**成新字串（見 [Collection](collections.zh-TW.md)）。
- **cast spec**——opt-in 自動轉換：single-step、於明確目標（見型別轉換）。

**`Ref`——copy-by-ref（sealed）。** 與上面每個 spec 不同，實作它不加行為——它改變值的**表徵（representation）**。`Ref`
型別是 **reference-counted**：複製是對共享計數 ++、而非深拷貝，它的 `drop(this)` 在最後一個持有者的 scope 退出時
**跑一次**。編譯器提供計數與 by-ref 複製；只有 `drop` 的內容由使用者寫。`Ref` 是 **sealed** 的——唯二實作者是內建的
**`chan`**（其 `drop` 即 close）與 stdlib 的 **`Ref[T]`** 資源盒（見 Values & Memory）。一般程式碼**使用 `Ref[T]`、
絕不實作 `Ref`**——所以「這個值是否以 reference 共享？」始終有明確答案：只有 `chan` 與 `Ref[T]` 是。

**運算子 desugar 到 spec**，所以 user type 可以靠實作對應 spec 來多載值運算子——`==` / `<` 已經走 `equal` / `Ord`。
多載必須維持**慣常**語意（`+` 不是加法就是濫用，違背 `small and crisp`）。**邏輯運算子都是關鍵字**——`not`
（一元），以及**會短路的** `and` / `or`——只作用於 `bool`、回傳 `bool`（不吃 truthiness；要判斷就 `bool(x)`）：`and`
在左側為 `false` 時跳過右側、`or` 在左側為 `true` 時跳過右側；logical xor 就是 `a != b`（**沒有** `xor` 關鍵字——它
無法短路，是普通運算、不是關鍵字）。這些、以及 null-safety 運算子（`?`、`??`、`?.`、`!`），都是**固定構造——永不
可多載**；bitwise 符號（`& | ^ ~`，見整數運算）永不與它們撞臉。

`float` 退出 `Ord` 與 `Hash`——`NaN` 破壞全序與 `equal ⇒ hash`——所以 `float` 永遠不是排序集合的元素、也不是 key，
而一個**含** `float` 的複合型別會透明地繼承這點：它 auto-derived 的 `equal` 用 `==` 比那個欄位，所以對 `NaN`
**非自反**，`Ord`/`Hash` 也**不會白得**。要讓這種型別當 key 或可排序，作者得**顯式實作**、並處理 `float` 的兩個
陷阱：`Hash` 需要一個**自反**的 `equal` 並 canonicalize **`±0.0`**（相等、故必須同 hash）；`Ord` 需要一個
**total order**（IEEE `totalOrder`，`NaN` 排到端點）。一個 stdlib 的 total-order／hashable `float` wrapper 延後。

**迭代。** 一個 **`Iterator[T]`** 有 `next() -> Result[T]`——`Left(v)` 是下一個元素，`Right(StopIteration)`
表示結束（**`StopIteration`** 是內建的 `Err`）。一個 **`Iterable[T]`** 有 `iter()`、產生一個全新的
`Iterator[T]`。`for x in X` 需 `X: Iterable`：它把 `x` 綁到每個 `Left`，**在 `Right(StopIteration)` 乾淨結束**，
而**對任何其他 `Right(err)` 則 raise**——迭代中途的失敗絕不被靜默吞掉（要檢視就手動 `next()` 再 `guard`）。因為
`<-ch` 本就回 `Result[T]`，**channel 就是一個 `Iterator`**：`for v in ch` 會 drain 它，在乾淨關閉時結束、並把
producer 的崩潰重新 raise。`Iterator` 也 trivially 是 `Iterable`，所以 **lazy adapters**（`map`、`filter`、
`take`、`zip`…）就是實作 `Iterator` 的普通 stdlib 迭代器、可鏈式——每個回傳一個**具體 adapter 型別**（`map`
回傳 `Map[This, U]`，它自身實作 `Iterator[U]`、存著來源與 closure），所以整條鏈全程 **monomorphize**、不 box。
`for mut x in X` 把每個元素綁成就地的
`mut`——僅當 `X` 為 `mut`。

## 控制流（Control flow）

全部控制流就三個構造，依「產出什麼」區分：**`match`** 產出一個**值**（模式比對）；**`if`** 與 **`for`** 是為副作用
而跑的 **statement**。分支要拿到值一律用 `match`（或 `??` / `?.`）——`if` 永不產出值，所以一個選擇只用一種方式產出
值，不是兩種。

**`if`**——`if cond { … }`，可接 `else` / `else if`；條件是 `bool`（沒有 truthiness）。**綁定形式**
`if x := expr { … }` 只在 `expr` 命中 pattern `x` 時跑區塊——「值存在」測試的單臂 `match` 語法糖
（`if v := <-ch { use(v) }` 只在 `Left` 時觸發）。

**`for`**——唯一的迴圈關鍵字、兩種形式：**`for { … }`** 無窮（用 `break` / `return` 離開）、以及
**`for x in it { … }`** 走訪 `it: Iterable`，每一輪以 **copy** 綁定 `x`（**`for mut x`** 就地綁定，僅當 `it` 為
`mut`；迭代協定——`StopIteration` 乾淨結束、其他 error re-raise——見「迭代」）。**沒有 `while`、也沒有 C 式三段
`for`**：條件迴圈就寫 `for { … break if done }`。

**`break` / `continue`** 作用於**最內層的 `for`**；**沒有 label**（要跳出外層就把內層抽成函式再 `return`）。語法糖
**`break if cond`** / **`continue if cond`** 完全等於 `if cond { break }` / `if cond { continue }`：

```text
for {
    line := <-input ?? break       # 收到 channel 關閉為止
    continue if line.empty()       # 跳過空行
    break if line == "quit"        # 遇到 sentinel 就停
    handle(line)
}
```

`for` 是 statement——不產出值；要組結果就鏈一個 iterator adapter（`map` / `filter` / `fold`）或 append 進另一個
collection（[Collections](collections.zh-TW.md)），不要 break-with-value。

## 格式化與文字（Formatting & text）

每個值都有兩種渲染，兩者都是 **`Object` method**（不需 opt-in 任何 `spec`）：

- **`debug() -> str`**——**開發者**視圖：**auto-derive**、結構化生成（sum 則先 tag 再 payload）、可 override。
  logging、`stderr`、abort backtrace 印的就是它——機械、不憑空猜文案。
- **`display() -> str`**——**給人看**的視圖；它的**預設 body 就是 `debug()`**，所以永遠存在。override 它來決定終端
  使用者該怎麼讀這個值（金額、日期）；compiler 永不衍生語意化渲染，所以 `display` 只能 override。

**內插——`f"…"`。** 裸 `"…"` 是字面量（大括號是普通字元）。**`f`-string** 內嵌 `{ expr }`，透過 `display()` 渲染
再串接——`f"sum={x + y}"`——在**編譯期 desugar** 成 `str` 串接（Collections），不需 variadic、無 runtime 格式
引擎；`f"{x.debug()}"` 嵌開發者視圖。**指示子** `f"{x:>.2f}"`（width/precision/進位/對齊）**延後**到一個獨立的
per-type **format 協定**、而非 `display` 的參數。

**`print`** 把 `x.display()` 加換行寫到 stdout——一個**保留字**、永遠在 scope 內、免 import，所以最小的程式就是
`print f"hello {name}"`。它**盡力而為**（寫入錯誤被丟掉、不 raise），所以不需 `?`；有檢查的完整 I/O 面是要 import
的 `io` package（見 [Process & I/O](io.zh-TW.md)）。

## 型別轉換（Type Conversion）

Zerg **以重新建構（re-construction）轉換、絕不以重新詮釋（reinterpret）**——一次轉換 `T(x)` 是*用 `x` 的值建一個
新的 `T`*、如同 constructor；**沒有 C 式的 cast** 把一種型別的位元當成另一種來看（reinterpret），也不提供。三種
型別操作因此清楚分開：**建**一個新值（`T(x)`，這裡）、**測**一個 existential 的身分（`x is T` → `bool`，見型別測試）、
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
（見 Null-safety）——仍是建構出目標值，絕非 reinterpret。

## 值與記憶體（Values & Memory）

無 garbage collector、無 pointer 語法。每個值都是 **scope-owned**（離開 scope 即釋放），且預設以**值傳遞**。
copy-by-value 是語意；編譯器會在安全時省略複製：

- **單一執行流程**——immutable 的值可隱形地改以 by-ref 傳遞；mutable 的則 fallback 為複製。
- **跨 coroutine**——一律複製：無共享可變狀態、無 data race；要把修改反映回去是呼叫端的責任（例如透過 channel）。
- **取值 / 回傳**——unwrap（`?`、`!`）、`match`、`return` 都是複製出來；來源永不失效。move 只是來源之後死掉時的
  隱形最佳化。

遞迴與自我參照型別不需要 pointer——直接宣告欄位（例如 `Node?` → `Node`），編譯器**自動**插入 heap 間接；這類值
一樣是 scope-owned 且 copy-by-value。

**一個 `struct` 的佈局就是它的宣告。** 欄位照**宣告序**排、值 **inline** 嵌在它的擁有者裡（除了上述遞迴 auto-boxing
之外沒有間接），而且編譯器**絕不重排**——所以一個 Zerg `struct` _就是_ 一個 C `struct`、field-for-field、自然對齊
配標準 padding。這是 transpile 到 C 掉出來的，也正是為什麼 struct **預設就 FFI-ready**（見 [FFI](ffi.zh-TW.md)）：
沒有另一套「最佳化」佈局可以 opt-out，所以 Zerg 不需要 `repr(C)` 標記。（sum type 的 payload 同樣 inline；只有它
discriminant 的確切 C 編碼是一個 deferred 的 FFI 細節。）更緊的控制——去掉 padding（**packed**）或強制更寬的
**alignment**，給封包格式與 memory-mapped 硬體用——是 niche 旋鈕，**擱置**到有具體需求為止。

mutability 屬於**實例（instance）**——也就是 binding——不是型別或任何欄位：`mut x := …` 讓整個建構出的實例
可變（每個欄位），`x := …` 則保持不可變；欄位只帶可見性（`pub` 或 private）。Zerg 沒有通用 reference；程式之間
只能透過以下方式共享儲存：

- **Mutable-ref 參數**（`mut` 參數）——唯一「語意上真的 by-ref」：被呼叫端就地改呼叫端（`mut`）的變數。它受限於
  這次呼叫——值的位置（欄位、`return`、送 channel）都是**複製當下的值**，只能往下傳給另一個 `mut` 參數，且不能跨
  `spawn`。**兩個 `mut` 引數永不共享同一塊儲存**——這是被呼叫端倚賴的保證：靜態別名（`f(x, x)`）是
  **compile error**，而編譯器無法證明之處（`f(mut xs[i], mut xs[j])` 且 runtime `i == j`）該次呼叫會
  **abort**（`AliasError`）。檢查只插在「mut 引數可能動態別名」的呼叫點。
- **Channel**——在 coroutine 之間以 by ref 共享，僅用於通訊。

**求值順序是左到右。** 函式引數、運算子的運算元、以及 `list`／`map`／`set` literal 的元素都**依原始碼順序**求值、
deterministic——不像 C 的引數求值順序是 unspecified。所以副作用（一個 `mut` 引數、一次 abort）的次序可預測；
`and`／`or` 的短路就是這條規則加上「跳過右運算元」（見內建 spec）。

**Reference-counted 的值**是 scope-owning 的唯一例外：型別實作 **`Ref`** 的值——內建的 **`chan`**，或 stdlib 的
**`Ref[T]`** 盒——以 **reference** 共享、而非複製。runtime 計數持有者，在**最後**一個持有者的 scope 退出時釋放；
其餘一切純 scope-owned、無 GC/refcount。複製一個值時，會對它（遞迴）包含的每個 `Ref` 值做 refcount++、深拷貝其餘
部分；`Ref` 值永遠共享、絕不被複製。

**refcount 在構造上就是 cycle-complete**，所以不需要循環收集器、也不需要 weak reference：`Ref[T]` 的 referent
在**盒子建構時就固定**（要指別處就建一個新的 `Ref`），而值 immutable-by-default、又是 bottom-up 建構，沒有辦法讓
一個既存的 `Ref` 回頭指向後建的值——參照循環永遠形不成，所以「最後持有者釋放」永遠是完整的。（唯一的退化個案
——`chan` 把指向自己的 reference buffer 進自己——是 programmer error、不是被檢查的個案。）

### `Ref[T]`——逃出自身 scope 的資源

多數清理只是記憶體，離開 scope 時就自動釋放。若一個**資源的釋放不屬於這種自動釋放**——foreign handle（見
[FFI](ffi.zh-TW.md)）、任何必須**恰好關閉一次**者——且它必須**逃出開啟它的 scope**（被 return、存進欄位、送過
channel），就用 **`Ref[T]`** 持有：一個 reference-counted 的資源盒，攜帶該值與一個 `drop` 動作。因為它以 **by-ref** 複製，
每份 copy 都指向**同一個**資源，`drop` 在最後一個持有者的 scope 退出時（或明確 `del`）**跑一次**。這正是裸的
copy-by-value handle 給不了的保證——一個普通 handle 的兩份 copy 會各自試圖釋放那唯一的資源。**唯有資源逃出
scope 時**才用 `Ref[T]`；侷限在單一 scope 的資源要用 `defer`（見下）。

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

| `del` 的對象                 | 你擁有它嗎？ | 效果                                                                         |
| ---------------------------- | ------------ | ---------------------------------------------------------------------------- |
| local、傳值參數、捕獲副本    | 是           | 最後存取 → **釋放儲存**                                                      |
| `mut` 參數（借呼叫端的變數） | 否           | 結束本次呼叫的借用 → **不釋放**；呼叫端保有                                  |
| closure body 內的捕獲值      | 否           | 結束**本次 invocation** 的存取 → 不釋放；下次呼叫仍有                        |
| channel、`Ref[T]`            | refcounted   | 放掉這個 holder（refcount--）；最後 holder 跑 **`drop`**（channel 即 close） |

`del` 永不懸空：撤銷一個借用不可能釋放別的名字所擁有的儲存，而 Zerg 既有規則已擋掉「owner 在 borrower 仍存活時
就釋放」（`mut` 參數受限於該次呼叫；逃逸的 closure 擁有捕獲的副本）。編譯器靜態就知道每個 `del` 是釋放還是純
撤銷——只有 `Ref` 值（channel 與 `Ref[T]`）帶 runtime refcount。

`del` 是**流程一致的**：一個名字只要在任一路徑上被 `del`，其後**每一條**路徑都視它為已死（不引入 runtime drop
flag）。因此在 `if` 某一分支裡 `del`，匯流之後該名字即不可再用，與其他分支對稱。

`del ch` 也是**提早關閉 channel** 的直接寫法——當下放掉你對 `ch` 的持有，若你是它最後一個 sender，
就會關閉 channel，無需再包一層更窄的 block。

### `defer`——在 block 退出時清理

`defer stmt` 安排 `stmt` 在所在 **block** 退出時執行——**每一條**離開路徑都跑，**包含 abort unwind**。它是「綁在
scope 上的副作用」的 procedural 工具——放鎖、flush buffer、關閉 scope-local 資源——完全不需要型別：

```text
{
    lock.acquire()
    defer lock.release()     # 每一種離開都會跑——正常、提早 return、或 risky() 內部 abort
    risky()
}
```

一個 block 內多個 `defer` 以**後登記先跑**執行，與 scope-owned 的釋放及 `Ref` 的 drop 交錯、依建構的逆序進行，於是
拆解正好鏡射建構。三者共用一條軸——清理**何時**觸發：`del` **當下**撤銷一個名字；`defer` 在**本 block** 退出時
觸發；`Ref[T]` 的 drop 在**最後一個持有者**退出時觸發。分界只有一個問題——資源會不會逃出它的 scope？**不會 →
`defer`；會 → `Ref[T]`。**

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
- **改**——一個 `pub` mutator method，吃 `mut this`，在其中重新確立 invariant。

要造「一個既有值、只改幾個欄位」的新值，用 **functional update**——`Foo{ ..base, age: 2 }` 產生一個**新**值、
`base` 原封不動——這適用於欄位可見的型別；opaque 型別則用回傳新實例的 `with`-風格 method。Zerg **沒有可變的 builder
或 cascade**：它只對公開欄位型別有用（而那種 literal 已經講完一切）、碰不到私有欄位、又會把值拖過一串無效中途狀態
——與「immutable by default」「建構當下即合法」相衝。

## 函式與閉包（Functions & Closures）

函式是**一等值（first-class value）**：它有型別，可當引數傳遞、可回傳、可存進欄位、可綁定到變數。函式型別寫成
`fn(P...) -> R`；參數的 `mut` 是**型別的一部分**，所以 `fn(mut int) -> bool` 與 `fn(int) -> bool` 是不同型別
（兩者 calling convention 不同——就地 by-ref vs 複製）。可見性**不**屬於型別：`pub` 匯出的是 top-level 函式的
**名字**，永不隨值移動，對匿名函式也無意義。

**一個函式的型別就是它的輸入／輸出契約，僅此而已。** 它揭露參數——`mut` 標出唯一的「引數層 effect」：就地改動那個
引數——以及結果，可回復的失敗會以 `Result` / `Either` 顯示在那裡。它**不追蹤任何其他 effect**：一個函式有沒有做
I/O、讀 ambient 狀態（clock、randomness、`env`）、或可能 **abort**，都不出現在簽章裡。I/O 只能透過檔案的 `import`
看到；而 abort 幾乎在每個運算式都可能發生（一次溢位、一個壞 index），標它就是每條簽章上的噪音。「引數改動」與
「可回復錯誤」以外的 effect **刻意不追蹤**——Zerg 在這裡是 procedural-first——而非漏寫。

持有函式的 binding 其可變性就是一般的 per-instance 軸——`mut f := …` 可 rebind、`f := …` 不可——與上述一切正交。

### 預設參數與具名引數

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
- 一個**具名引數**以參數名字傳入（`loud: true`）——這正是讓你能**跳過中間的預設參數**的關鍵。規則就是慣例那套：
  positional 引數由左往右填、任何參數都可改用具名、有預設的可省略，而且**一旦具名，其後全部都要具名**（具名之後
  不能再回到 positional）。

因為參數可以用名字挑選，**名字就成了函式契約的一部分**——改名會弄壞呼叫者，就跟改型別一樣。但預設與名字都不進
_型別_：`fn(str, str, bool) -> str` 是型別，預設住在宣告裡、名字住在參數列——與「型別就是輸入／輸出契約、僅此
而已」一致。兩者都是**呼叫端 sugar**：跨越 C ABI（見 [FFI](ffi.zh-TW.md)）時，export 的函式全 positional、無預設。

**variadic** 參數刻意**不提供**——改為顯式傳 `list[T]`（`sum(xs: list[int])`，呼叫 `sum([1, 2, 3])`）。這讓呼叫
模型與 C ABI 都保持扁平，也符合 formatting 已採的 no-variadics 立場；`print` 保持是內建構造、不是使用者可定義的
variadic。

**閉包的捕獲規則與 `spawn` 相同：只捕獲 immutable 值與 channel，且以複製帶入。** `mut` 變數不能被捕獲；要用先
快照成 immutable binding（`snap := n`）。捕獲是複製——捕獲的 channel 做 refcount++、其餘深拷貝——所以逃出定義
scope 的閉包帶著自己的副本，永不懸空。等價地說：

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

## 並行（Concurrency）

Zerg 的並行**只有 coroutine 與 channel**：`spawn`（Go 的 `go`）跑在 **M:N scheduler** 上，fire-and-forget、無
join/handle，只捕獲 **immutable 值與 channel**。channel 是 reference-counted 的 by-ref **管道**（一個為通訊而生的
`Ref` 型別；`Ref[T]` 是它持有資源的手足——見 Values & Memory）——payload 複製、在最後一個 sender 離場時
**自動 close**、以 **`Result[T]`** 接收（`Right` = 已關，攜帶崩潰 `Err` 或 `StopIteration` 哨兵）、
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
fire-and-forget——見 Concurrency）。

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

### 依型別處理錯誤——`is`

兩層送出的是**同一個被抹除的 `Err`**——值那層的 `Right(err)` 與 `guard` 具現化的 abort 無從分辨——所以同一套機制就
能對兩者分派。要對**某一種**錯誤動作，就用 **`is`** 測它的型別（見型別測試）：

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
