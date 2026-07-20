# Zerg Spec 與 Generics（Specs & Generics）

Zerg 如何抽象行為——`spec` 介面、泛型 bound、spec 當型別用的 existential,以及每個型別都有的內建 spec。屬於
[語言參考](language.zh-TW.md) 的一部分。亦有 [English](specs.md) 版本。

行為分成**兩層**。型別可定義 **inherent method**——自有行為，只有握著具體型別時才能用。而**抽象**一律透過
**`spec`**：一個具名的行為介面——method 簽名（其中有些帶 **default body**，見下），外加 **associated type** 與
**associated value**（兩者見下），且**永不含 field**。滿足是 **nominal**：型別必須**明確宣告**它實作某 `spec`，且每組
**(型別, spec) 只有一個正規 impl**——**帶參數**的 spec 會把參數算進這組，所以 `Indexable[int]` 與 `Indexable[Range]`
是不同組、各有自己的正規 impl（見下方「解析帶參數的 spec」）。

`spec` 是抽象行為的**唯一**機制，因此它扮演三個角色——泛型參數的 **bound**、型別所 **conform** 的介面、以及
（見下）**當成型別本身**。內建行為也都是 spec、不是編譯器魔法：`Err` 就是 `Error` spec，相等、排序、雜湊、迭代、
以及 opt-in 的轉換都是普通 stdlib spec。型別的 inherent method 不必隸屬任何 spec；**唯有 spec 所保證的，才可被抽象**。

**spec bound 就是泛型型別的完整介面。** 在泛型於 `T` 的程式碼裡，對一個 `T` 值唯一能用的操作，就是它 spec bound
所宣告的 method——它的欄位與任何 inherent method 都不可見。因此：

- **空的 `spec`** 是合法的 bound、被所有型別滿足，但它保證**零**行為：這種 `T` 只有 memory model 給的**結構能力**
  ——copy 它、`del` 它、當參數傳、存起來、送進 channel——連一個 method 都沒有。
- **`Object`** 是頂層 `spec`，被每個型別**自動實作**。它提供一組最小的 method——`equal`、`copy`、`debug`……由結構
  逐欄位 **auto-derive**（含 `Ref` 值則 refcount++，與 copy 規則一致），外加 `display`——其預設 body 就是 `debug`
  （見 [Formatting & Text](format.zh-TW.md)）。型別可**明確覆寫**其中任何一個（例如不計順序的 `equal`），否則沿用衍生版本。因為每個型別都實作 `Object`，`T: Object` 這個 bound **從不縮小**
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
運算元正是抹除掉的具體型別，而 `is` 只測身分、絕不把它交回。兩個 boxed 值因此**永遠不能以值比較**。box 一個值是為了
動態 dispatch 它的 spec method；要比較、排序或當 key，就留著具體型別（monomorphized 的 `[T: S]` bound）。

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
歧義只存在於「同一個值必須**同時**滿足兩者」之處（`T: X + Y` 的 bound、型別為 `X + Y` 的值、或對同時實作兩者的值
做裸 `x.foo()`）。Zerg 在編譯期**拒絕它**、而不引入 fully-qualified 呼叫語法來消歧——你把靜態脈絡收窄到單一 spec
來解（單 spec 的 `[T: X]` bound、或 spec-typed 值）；要讓一個 method 被多個 spec 共用，就讓它們**源自同一個共享
spec**。spec 可跨 package 邊界實作到什麼程度、以及 coherence 如何維持全域唯一，見
[Module、Package 與 Program](package.zh-TW.md)。

**在具體值上解析出的名字必須恰指一個 method**——同一條反歧義規則,現在落在具體呼叫。**inherent method 不得與型別
所實作的任何 spec method 撞名**:在 impl 處 compile error。想給型別「自己版本的 spec method」,就**override** 它
(dispatch 仍 canonical);inherent method 是給「*不屬於*任何 spec 的行為」用的,所以撞名是誤用、不是要去排優先序。

**一個 spec 可以要求 super-spec。** 在 spec 名字之後，`: Bound` 指名每個 implementer **也**必須實作的一或多個
spec——`spec Ord: Eq` 讓 `impl Ord` 也要求 `impl Eq`，而 `+` 連接則要求多個（`spec Sorted: Ord + Hash`）。
super-spec 做兩件事：它是**前提**（不先實作 `Eq` 就無法實作 `Ord`），並把 super-spec 的 method **放進 `This` 的可視
範圍**、可用在該 spec 自己的 default body 裡——於是 `Ord` 的 provided body 可以在 `this` 上呼叫 `Eq` 的 method。這
正是扁平模型不得不放棄的**跨 spec default-body 重用**。super-spec 與**用處**的 bound 不同：需要同時具備多種能力、是在
**呼叫處**以組合 bound 說出——`[T: Ord + Hash]`，`+` 讀作「而且」——而 super-spec 是把一個真正的實作相依性 baked 進
spec 本身。一個只是被共用、而非被相依的能力，仍可自成一個 spec、在 bound 裡與其他並列。

## 解析帶參數的 spec（Resolving a parameterized spec）

**帶參數**的 spec 把它的參數摺進實作身分裡，所以一個型別可以同時實作多個——一個 `list` 同時實作 `Indexable[int]`
（取元素）與 `Indexable[Range]`（切片），各自保留自己的 associated `Output`：

```text
spec Indexable[K] {
    type Output
    fn index(k: K) -> Output
}
impl[T] Indexable[int]   for list[T] { type Output = T;       fn index(i: int)   -> T       { … } }
impl[T] Indexable[Range] for list[T] { type Output = list[T]; fn index(r: Range) -> list[T] { … } }
```

因為參數 `K` 出現在 method 簽名裡，一次使用 `xs[k]` **依 `k` 的型別靜態解析**——編譯器以「為 untyped literal 定型」的
同一套機制，挑出 `K` 相符的那個唯一 impl。三種結果，**沒有預設退路**：

- **恰好一個** impl 與引數型別相符 → 解析完成、免標註；
- **零個** → 普通型別錯誤；
- **兩個以上**（僅當引數是能吻合多個 `K` 的 untyped literal 時）→ **硬編譯錯、要你標註**。它**絕不**降級成警告、也不由
  預設挑一個：不像未覆蓋的 `match`（其退路是響亮的 `MatchError`），一個解析錯的 impl**沒有安全退路**——會默默跑錯的碼。

不同的具體參數彼此永不重疊，所以解析是明確定義的；唯一的問題只是「多個都命中時，這次指的是哪一個」。concrete-bound
泛型直接指名參數（`[X: Indexable[int]]`）、或當場綁定它（`[X: Indexable[K]]` 綁定 `K`），所以 **bound 永遠不歧義**
——只有「對一個有多個實作的值做裸使用」才會。要在**執行期**、而非依引數型別做選擇，就改用 `enum`。

## 型別測試（Type tests）——`is`

existential 藏起值、但沒藏起**身分**——就是上面介紹過的 `x is T` 測試。它不是 downcast、也不為語言添加任何
reinterpret（見 [型別轉換](types.zh-TW.md)）。`T` 必須實作 `x` 所定型的那個 spec，否則測試靜態上不可能、會被拒；
對一個具體型別**已知**（非 existential）的值，答案是編譯期常數。

因為 `is` 永不交出具體值，它只能驅動**控制流、不是資料存取**：你可以就「它是不是 `T`」分支，但要讀 `T` 自己的欄位，
你必須**一開始就握著具體型別**、從未 box 它。它就是個普通 `bool`——用在 `if`、搭 `not` / `and` / `or`、或當 `match`
guard——不需要任何新的 pattern 形式。它的主要用途是對**被抹除的錯誤**依型別分派（見 [Null-safety 與錯誤處理](errors.zh-TW.md)）。

## Method、`this` / `This` 與 default body

一個 **method** 是帶 **receiver** 的函式——被呼叫的那個實例，名為 **`this`**；receiver 自身的型別是 **`This`**。
`This` 在「具體型別尚未知」處指「**實作它的那個型別**」——同型別的運算元（`less(this, other: This) -> bool`）、或
**associated function** 的結果（`default() -> This`，也就是 constructor——它沒有 receiver，所以也沒有 `this`）——並在每個實作裡
解析成具體型別。**帶參數**的 spec 的參數（`Indexable[K]`）是**另一件事**：一個自由選的型別——每個 impl 固定一次、
摺進 (型別, spec) 身分裡；而 **associated type**（`type Item`，投影成 `This.Item`）是第三種、由 implementer 選的型別；
唯有 `This` 是被逼定的 self-type，永非選擇。

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

## Associated type 與 associated value

spec 的成員不只有 method。一個 spec 可以宣告 **associated type**——`type Item`（也可加 bound，`type Item: Ord`）
——一個由每個 impl 以 `type Item = int` 填入的型別。它由 **implementer 依函數關係決定**：每個 impl 只有一個 `Item`、
絕不逐次使用時另選。在型別位置以**投影**引用它——`I.Item`，可鏈式成 `I.Item.Sub`。

這正是讓「迭代」這類單一輸出協定**良好定義**的關鍵：`for x in it` 只有**一個**元素型別，因為 `Iterator.Item` 每個
impl 固定——而非逐次使用時另選，像帶參數的 `Iterable[T]` 會允許的那樣（那個替代方案是刻意被否決的）。對比很精確
——**associated type** 是每個 impl 一個輸出（`Iterator`），**參數**是每個引數型別一個 impl（`Indexable[K]`，見上）。

一個 spec 也可以要求 **associated value**——每個 impl 供給的一個編譯期值：`BITS: int` 要求它，impl 以
`BITS := 32`（一個 **`val-bind`**）供給。它是摺疊後的常數，泛型端以 `T.BITS` 取用——是型別常數（見下）在 spec 層的
形式。與 associated **function**（`fn max() -> This`，它會*執行*以產生值）不同，associated **value** 在編譯期被*摺疊*
；一個由 spec 保證的常數用值形式，一個需計算的則用 function 形式。

## 型別常數（Type constants）

一個**型別常數**是每型別的編譯期值，宣告在型別的 `impl` 裡、作為一個 **`val-bind`**——`NAME := <const-expr>`
——並以 `Type.NAME` 讀取（在型別內是 `This.NAME`）。它的初始式是一個**常數運算式**——literal、另一個常數、或它們的
摺疊算術——compiler **在編譯期直接代入**、不執行任何程式碼、**無副作用**（摺疊是隱式的、與 `const` 關鍵字無關，後者
只標記一個防遮蔽的 binding）。因為建構就是一次普通的**呼叫**——`Point(x: 0, y: 0)`、**絕非**大括號語法——所以型別
能給自己 canonical 值：`ORIGIN := Point(x: 0, y: 0)`，以 `Point.ORIGIN` 取用。身為編譯期常數，`int` 型別的那種**在
任何需要編譯期常數之處都能用**——包括定長陣列的大小（`[byte; Buffer.SIZE]`，見 [Collections](collections.zh-TW.md)）。
可見性就是一般的 `pub` / private 旋鈕，如同 field 或 method。

型別常數是 associated-value 機制（見上）的 **impl 端**：一個純粹的 `NAME := …` 是沒有 spec 要求的 `val-bind`，而一個
spec 的 `BITS: int` 是同一個、被 spec *要求*的 `val-bind`。所以一個 spec 必須保證的「每型別的*值*」是一個
**associated value**——`MAX: This` 被要求、每個 impl 供給 `MAX := …`、泛型端讀 `T.MAX`——**而非**無 receiver 的
function。

## 內建 spec（Built-in specs）

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

- **`Ord`**——一個與 `equal` 一致的 **total** order,由**單一必需的 `less`**(`<`)定義;`<=` `>` `>=` 與 sort 都由它
  配 `equal` 導出,而 `min` / `max` / `clamp` 是建在 `Ord` bound 上的普通 stdlib helper——**沒有三路 `Ordering`**
  值,只有 `less` 與 `equal`。`str` 依 **code point 字典序**排序(＝ byte 序,因其 UTF-8 有效——非 locale
  collation,那是另一個 stdlib 功能);`float` 同時退出 `Ord` 與 `Hash`(理由見下)。
- **`Hash`**——`map` / `set` 的 key，`equal ⇒ same hash`。`str` 不可變、是天然的 key。
- **`Iterator`** / **`Iterable`**——迭代協定（見下方 **迭代**）。
- **`Error`（`Err`）**——錯誤層：`message() -> str`、`unwrap() -> Err?`（底層 cause、無則 `nil`）、
  `code() -> byte?`（可選小碼）。
- **`Add` / `Sub` / `Mul` / `Div` / … 與 bitwise `BitAnd` / `BitOr` / `BitXor` / `Not` / `Shl` /
  `Shr`**——值運算子（`+ - * / %`、`& | ^ ~ << >>`、indexing…）：運算子多載，見下。`str` 實作 `Add`，所以 `+` 會
  **串接**成新字串（見 [Collection](collections.zh-TW.md)）。
- **cast spec**——opt-in 自動轉換：single-step、於明確目標（見 [型別轉換](types.zh-TW.md)）。

**`Ref`——copy-by-ref（sealed）。** 與上面每個 spec 不同，實作它不加行為——它改變值的**表徵（representation）**。`Ref`
型別是 **reference-counted**：複製是對共享計數 ++、而非深拷貝，它的 `drop(this)` 在最後一個持有者的 scope 退出時
**跑一次**。編譯器提供計數與 by-ref 複製；只有 `drop` 的內容由使用者寫。`Ref` 是 **sealed** 的——唯二實作者是內建的
**`chan`**（其 `drop` 即 close）與 stdlib 的 **`Ref[T]`** 資源盒（見 [值與記憶體](memory.zh-TW.md)）。一般程式碼**使用 `Ref[T]`、
絕不實作 `Ref`**——所以「這個值是否以 reference 共享？」始終有明確答案：只有 `chan` 與 `Ref[T]` 是。

**運算子 desugar 到 spec**，所以 user type 可以靠實作對應 spec 來多載值運算子——`==` / `<` 已經走 `equal` / `Ord`。
多載必須維持**慣常**語意（`+` 不是加法就是濫用，違背 `small and crisp`）。**邏輯運算子都是關鍵字**——`not`
（一元），以及**會短路的** `and` / `or`——只作用於 `bool`、回傳 `bool`（不吃 truthiness；要判斷就 `bool(x)`）：`and`
在左側為 `false` 時跳過右側、`or` 在左側為 `true` 時跳過右側；logical xor 就是 `a != b`（**沒有** `xor` 關鍵字——它
無法短路，是普通運算、不是關鍵字）。這些、以及 null-safety 運算子（`?`、`??`、`?.`、`!`），都是**固定構造——永不
可多載**；bitwise 符號（`& | ^ ~`，見 [整數運算](types.zh-TW.md)）永不與它們撞臉。

`float` 退出 `Ord` 與 `Hash`——`NaN` 破壞全序與 `equal ⇒ hash`——所以 `float` 永遠不是排序集合的元素、也不是 key，
而一個**含** `float` 的複合型別會透明地繼承這點：它 auto-derived 的 `equal` 用 `==` 比那個欄位，所以對 `NaN`
**非自反**，`Ord`/`Hash` 也**不會白得**。要讓這種型別當 key 或可排序，作者得**顯式實作**、並處理 `float` 的兩個
陷阱：`Hash` 需要一個**自反**的 `equal` 並 canonicalize **`±0.0`**（相等、故必須同 hash）；`Ord` 需要一個
**total order**（IEEE `totalOrder`，`NaN` 排到端點）。一個 stdlib 的 total-order／hashable `float` wrapper 延後。

**迭代。** 一個 **`Iterator`** 有一個 associated **`type Item`** 與 `next() -> Result[Item]`——`Left(v)` 是下一個
元素，`Right(StopIteration)` 表示結束（**`StopIteration`** 是內建的 `Err`）。一個 **`Iterable`** 同樣有一個
`type Item` 與 `iter()`、產生一個全新的、`Item` 相符的 `Iterator`。因為 `Item` 是 **associated type**——每個 impl
固定、不是逐次使用時另選的參數——`for x in X` 只有**一個**元素型別：它需 `X: Iterable`、把 `x: X.Item` 綁到每個
`Left`，**在 `Right(StopIteration)` 乾淨結束**，而**對任何其他 `Right(err)` 則 raise**——迭代中途的失敗絕不被靜默
吞掉（要檢視就手動 `next()` 再 `guard`）。因為 `<-ch` 本就回 `Result[T]`，**channel 就是一個 `Iterator`**、`Item = T`：
`for v in ch` 會 drain 它，在乾淨關閉時結束、並把 producer 的崩潰重新 raise。`Iterator` 也 trivially 是 `Iterable`，
所以 **lazy adapters**（`map`、`filter`、`take`、`zip`…）就是實作 `Iterator` 的普通 stdlib 迭代器、可鏈式——每個回傳
一個**具體 adapter 型別**（`map` 回傳 `Map[This, U]`、其 `Item = U`，存著來源與 closure），所以整條鏈全程
**monomorphize**、不 box。`for mut x in X` 把每個元素綁成就地的 `mut`——僅當 `X` 為 `mut`。
