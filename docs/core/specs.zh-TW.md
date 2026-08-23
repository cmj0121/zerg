# Zerg Spec 與 Generics（Specs & Generics）

Zerg 如何抽象行為——`spec` 介面、泛型 bound、spec 當型別用的 existential,以及每個型別都有的內建 spec。屬於
[語言參考](../language.zh-TW.md) 的一部分。亦有 [English](specs.md) 版本。

行為分成**兩層**。型別可定義 **inherent method**——自有行為，只有握著具體型別時才能用。而**抽象**一律透過
**`spec`**：一個具名的行為介面——method 簽名，其中有些帶 **default body**（見下）——且**永不含 field**、
**永不含 associated type**、**永不含 associated value**（見下節）。滿足是 **nominal**：型別必須**明確宣告**它實作某 `spec`，且每組
**(型別, spec) 只有一個正規 impl**——**帶參數**的 spec 會把參數算進這組，所以 `Indexable[int, T]` 與 `Indexable[Range, list[T]]`
是不同組、各有自己的正規 impl（見下方「解析帶參數的 spec」）。

`spec` 是抽象行為的**唯一**機制，因此它扮演三個角色——泛型參數的 **bound**、型別所 **conform** 的介面、以及
（見下）**當成型別本身**。內建行為也都是 spec、不是編譯器魔法：`Err` 就是 `Error` spec，相等、排序、雜湊、迭代、
以及 opt-in 的轉換都是普通 stdlib spec。型別的 inherent method 不必隸屬任何 spec；**唯有 spec 所保證的，才可被抽象**。

**spec bound 就是泛型型別的完整介面。** 在泛型於 `T` 的程式碼裡，對一個 `T` 值唯一能用的操作，就是它 spec bound
所宣告的 method——它的欄位與任何 inherent method 都不可見。因此：

- **空的 `spec`** 是合法的 bound、被所有型別滿足，但它保證**零**行為：這種 `T` 只有 memory model 給的**結構能力**
  ——copy 它、`del` 它、當參數傳、存起來、送進 channel——連一個 method 都沒有。
- **相等、排序與雜湊都是 opt-in、絕非自動。** 沒有**自動實作的 `Object` spec**、也沒有隱式的 `==`。一個型別只有透過
  **`#[derive(Eq)]`** 或手寫 `impl Eq` 才取得結構化相等（`==` / `!=`）,透過 `derive(Ord)` 取得全序,透過
  `derive(Hash)` 取得雜湊（兩者皆 **[not yet]**）;比較兩個沒有 `Eq` impl 的型別的值是編譯錯誤。上面那些結構性
  記憶體操作是例外,因為那些是表徵的性質、不是 spec 抽象的行為。撐起 `derive` 的那套 compiler 擁有的
  **結構化衍生**（`Eq` 在一個 `struct` 與一個無欄位 `enum` 上已建置;
  `Ord`、`Hash`、`Encode`、`Decode`,以及帶 payload 的 `enum` 上的 `Eq`,皆為 **[not yet]**）
  見 [Derive 與預設行為](derive.zh-TW.md) 參考。

`spec` 也可**當型別用**，不只是 bound：spec-typed 的值可持有任何實作它的型別——heap-boxed、single-owner、
scope-owned，並**動態 dispatch**（實際要跑哪個 method，在執行期依值的真實型別決定）。抹除是**對值單向**的——一旦
boxed，具體值就被隱藏、**永遠無法還原**（不能 downcast、不能 reinterpret；要拿到具體型別只能「一開始就留著」、無從
反抹除）。它的**身分**是另一回事：**`x is T`** 問「這個 boxed 值的具體型別是不是 `T`」、產出一個純 **`bool`**，
答案是從 box 本就帶著的 dispatch 身分讀出來的（見下方「型別測試」）。

在一個 boxed 值上，**unary** 操作會 dispatch 到真實型別、可用：它的 spec method，加上 `copy`（產生一個獨立的新
box——內含 `Ref` 值 refcount-bump）與 `debug`，以及結構性記憶體操作（`del`、傳參、存欄位、送 channel）。但
**binary same-type** 操作——`Eq` 的 `==` / `!=`、`Ord` 比較、以及因此的 `Hash` keying——**不可用**：它們的 `other: This`
運算元正是抹除掉的具體型別，而 `is` 只測身分、絕不把它交回。兩個 boxed 值因此**永遠不能以值比較**。box 一個值是為了
動態 dispatch 它的 spec method；要比較、排序或當 key，就留著具體型別（monomorphized 的 `[T: S]` bound）。

同一道關卡也落在另外兩類成員上：spec 的 **associated function**（`default() -> This`、`zero()`——無 receiver，box
沒有可供分派的 _起點_）與它的**泛型 method**（vtable 每個型別一格、而非每個型別實參一格）。兩者都需要一個**具名的
具體型別**，所以在 **existential 上**各自是 compile error——這不是禁止該 spec 當型別，只是禁止那一個呼叫，和 binary
op 完全一樣。因此**沒有 object-safety gate**：一個 spec **永遠可以當型別**，box 就只提供「單憑 `this` 就能分派」的
東西——並把回傳 `This` 的結果 re-box 成同一個 spec。

> **[not yet]** `spec` 根本不能**當型別用**,所以上面那三段——heap-boxed 的 existential、它的動態 dispatch、以及逐個
> 成員交代 box 提供什麼、不提供什麼——講的是一套沒有程式到得了的機制。`fn go(g: Greet)` 會被拒絕、報
> _E9048 NotImplemented: the `spec` `Greet` used as a TYPE (parameter `g` of `go`) — a spec is a bound and an interface
> here, not yet a value's type; take the concrete type, or a generic parameter bounded by it_。`spec` 在這裡只扮演
> 它三個角色中的兩個;[語言參考](../language.zh-TW.md) 概覽裡的同一個主張因同一個原因尚未建置,而下面那段 codegen
> 裡屬於動態 dispatch 的那一半,沒有任何東西可以分派。
>
> 程式**到得了**的是下面的 [`#[obj]`](#obj把一個-spec-的方法當成值持有):同一個 existential,只是編碼成
> 「一個裝著函式值、捕捉了實作者的 struct」,而不是「一個帶 vtable 的 boxed pointer」。它提供本節說 box 提供的東西、
> 拒絕本節說 box 服務不了的成員——所以**機制在這裡,不在的是那個「型別」**。

concrete bound 的 generic 會在產出的 C 裡 **monomorphize**——編譯器為每個具體型別各生成一份特化版本——而把 `spec`
當型別用是唯一改用 dynamic dispatch 之處。concrete type 之間**沒有 subtyping**，所以泛型是**不變（invariant）**
的：`list[Cat]` 不是 `list[Animal]`——要抽象一整族就用 spec bound（`[T: X]`），而非 subtype 代換。

**型別引數由呼叫端解出**,而且是結構性的:宣告為 `list[T]` 的參數收到 `list[int]` 就決定了 `T`,所以 `max(a, b)`
不必寫 `[int]`。沒有任何引數提到的型別參數是編譯錯誤而非猜測;而 **bound 在 instantiation 時檢查**——那是具體型別
第一次存在、可供檢查的地方。

> **[not yet]** 泛型 **`fn`** 已實作,指名一個以上 spec 的 bound 也已實作——`T: Eq + Show` 是一個連言,而沒被滿足的
> 那個 spec 就是拒絕訊息會指名的那個。泛型 **`struct`**、泛型 **`enum`** 與泛型 **method** 各自仍被指名拒絕。

一個**實作**（型別滿足某 spec）本身不帶可見性標記：coherence 要求一組 `(型別, spec)`（含參數）到處都解析到同一個實作，
因此實作既不能被藏、也不能被複製——它的作用範圍恰好是「型別與 spec 同時可見之處」。實作是為**具體或泛型型別**寫的
——`list[T]` 可以實作 `Iterator`。

> **[not yet]** 目標**帶著型別引數**的 `impl` 是 _E9038 NotImplemented: an `impl` on `list[int]` — a type
> ARGUMENT on the target: this compiler keys an implementation by the target's bare name, so every
> instantiation of `list` would share one_,而且 `GRAMMAR#impl-decl` 為它推導的兩種形狀都是:帶參數的
> `impl[T] Spec for list[T]` 以及完全具體的
> `impl Spec for list[int]`。所以沒有任何實作能掛到容器型別上,上一行說的「`list[T]` 可以實作 `Iterator`」是規範,
> 而不是 `zerg` 已經建好的東西。它需要的是「目標每個實例化各自 monomorphize 出一個實作」,而這個編譯器唯一會
> monomorphize 的是泛型 `fn`。可用的形狀,是標在本程式宣告的 `struct` 或 `enum` 上的 `impl`。

以 bound 為條件、涵蓋「所有滿足某 spec 的型別」的 **blanket 實作不提供**,以保持解析可判定;也**沒有「所有型別都
有」的實作**,包含上面那個 per-type opt-in 的 `Eq`。

> **[not yet]** **一個方法以它的名字為鍵**,而這一件事就是上面那條規則的三個部分都在等的東西。一個型別的方法共用
> 同一個命名空間——spec 宣告的與 inherent 的一視同仁——所以讓第二個 `impl X for A` 成為錯誤的不是 `(spec, 型別)`
> 這組鍵,而是那次相撞;而兩個**不同的** spec 剛好宣告了同名方法時也會相撞:`impl Show for P` 旁邊放
> `impl Tag for P`、兩者各有一個 `label`,得到的是 _E4025 `P` declares `label` twice_。這裡沒有任何東西是無聲地錯
> ——每一種情形都帶著位置具名拒收——也沒有任何不該解析得到的東西解析得到。缺的是那把鍵:在一個方法以「宣告它的
> spec 與該 spec 的引數」為鍵之前,`(spec, 型別)` 沒有更細的東西可以拿來當鍵。[型別](types.zh-TW.md)裡第二個
> `Into` 需要的是同一把鍵,而那是一件工作、不是兩件。
>
> 這條規則有兩個鄰居不在等它。**orphan** 那一半往內落了一層——一個 `impl` 屬於 spec 的模組或型別的模組,因為模組是
> 這個實作唯一有的範圍,而且沒有 package 層可供這條規則伸到。而鍵的**實例化**那一半根本走不到:帶型別引數的目標在
> 上面就被拒絕了,所以 `impl X for list[int]` / `impl X for list[str]` 兩者中的**第一個**就已被拒絕,而兩者都不曾被
> 當成鍵。鍵不是過度近似,而是從來沒被查閱過。

## `#[obj]`——把一個 spec 的方法當成值持有

**`spec` 永遠是 bound、不是型別**(見上),所以一個值不能被 spec 定型。`#[obj]` 就是你仍然想要異質集合時
寫的東西:標在 spec 上,它生出一個 companion **函式值 struct**——一個方法一個欄位——加上一個**泛型包裝**,
把任何實作者變成它。

```zerg
#[obj]
spec Draw { fn draw() -> str }
```

就是:

```zerg
struct DrawObj { pub draw: fn () -> str }
fn draw_obj[T: Draw](v: T) -> DrawObj {
    return DrawObj(fn () -> str { return v.draw() })
}
```

分成兩段 fence 而不是一段,因為它們是同一組宣告的兩種寫法、不是一支同時裝著兩者的程式:寫在一起是
_E3078 `DrawObj` is declared twice_。

**開放性來自包裝點**,不是來自執行期的任何東西:`draw_obj` 針對每個實作者 monomorphize,回來的東西只有一個
型別。所以 `list[DrawObj]` 是異質的,而且**沒有 vtable、沒有任何值帶 header、也沒有 downcast**——你可以呼叫
spec 宣告的東西,不能問裡面裝的是什麼。需要問的時候,答案是 `enum` 加 `match`。

有三種形狀會被**拒絕**,判準與委派式 `derive` 相同——**那個改寫存不存在**:

- **`mut fn`**:被包起來的值是複本,寫穿它會改到沒有人讀得到的東西。這裡的 object 是不可變的;
- 收 **`This`** 的方法:它需要 object 已經忘掉的那個型別——那個形狀是 `enum` 上的 `#[derive(S)]` 在做的;
- **不是 spec** 的東西:沒有方法可以持有。

> **[not yet]** 型別同時實作兩者是做不到的。當兩個 spec 各自宣告 `go` 時,`impl A for P` 與 `impl B for P` 會在
> **第二個宣告**處被拒絕——_E4025 `P` declares `go` twice — every method on a type shares one namespace, spec or
> inherent alike_——所以下面那個「把靜態脈絡收窄到單一 spec」的解法沒有程式可以套用。這條拒絕就是讓 derived 與
> 手寫 `Eq` 不能並存的同一條,只是多管了一格。

因為 spec 是 nominal，兩個各自獨立宣告的 spec 可能撞用同一個 method 名。型別仍可同時實作兩者、並各別當其一使用——
歧義只存在於「同一個值必須**同時**滿足兩者」之處（`T: X + Y` 的 bound、型別為 `X + Y` 的值、或對同時實作兩者的值
做裸 `x.foo()`）。Zerg 在編譯期**拒絕它**、而不引入 fully-qualified 呼叫語法來消歧——你把靜態脈絡收窄到單一 spec
來解（單 spec 的 `[T: X]` bound、或 spec-typed 值）；要讓一個 method 被多個 spec 共用，就讓它們**源自同一個共享
spec**。spec 可跨 package 邊界實作到什麼程度、以及 coherence 如何維持全域唯一，見
[Module、Package 與 Program](../runtime/package.zh-TW.md)。

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

**帶參數**的 spec 把它的參數摺進實作身分裡，所以一個型別可以同時實作多個——一個 `list` 同時實作 `Indexable[int, T]`
（取元素）與 `Indexable[Range, list[T]]`（切片），各自把輸出型別放在第二個參數：

```text
spec Indexable[K, V] {
    fn index(k: K) -> V
}
impl[T] Indexable[int, T]         for list[T] { fn index(i: int)   -> T       { … } }
impl[T] Indexable[Range, list[T]] for list[T] { fn index(r: Range) -> list[T] { … } }
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

> **[not yet]** 一個帶參數的 spec 只能在**一個**引數上被實作,不能同時在好幾個上——而那正是本節存在的全部意義。
> `impl Ix[int] for C` 與 `impl Ix[str] for C` 並列會被拒絕、報 _E4025 `C` declares `ix` twice — every method on a type
> shares one namespace, spec or inherent alike, and a type has one canonical implementation of a spec_:method 是以
> **名字**為鍵的,所以第二個 impl 的 `ix` 與第一個相撞,而不是被那個本來就該區分它們的引數分辨開來。上面那組
> `Indexable[int, T]` / `Indexable[Range, list[T]]` 因此宣告不出來,它所餵養的三種結果解析也沒有東西可解析。這與
> [型別](types.zh-TW.md#into--一個普通的轉換-spec) 裡「每個型別只能有一個 `Into`」是同一個根因。

## 型別測試（Type tests）——`is`

existential 藏起值、但沒藏起**身分**——就是上面介紹過的 `x is T` 測試。它不是 downcast、也不為語言添加任何
reinterpret（見 [型別轉換](types.zh-TW.md)）。`T` 必須實作 `x` 所定型的那個 spec，否則測試靜態上不可能、會被拒；
對一個具體型別**已知**（非 existential）的值，答案是編譯期常數。

因為 `is` 永不交出具體值，它只能驅動**控制流、不是資料存取**：你可以就「它是不是 `T`」分支，但要讀 `T` 自己的欄位，
你必須**一開始就握著具體型別**、從未 box 它。它就是個普通 `bool`——用在 `if`、搭 `not` / `and` / `or`、或當 `match`
guard——不需要任何新的 pattern 形式。它的主要用途是對**被抹除的錯誤**依型別分派
(見 [Null-safety 與錯誤處理](../code/errors.zh-TW.md))
——而**這個階段那也是唯一已實作的用途**:`is` 可用於內建的錯誤分類,而對**非錯誤**型別的一般
存在性測試 `x is T` 是 **[not yet]**:_E9078 NotImplemented: `is P` — an `is` test names one of the built-in
error kinds here, and `P` is not one; GRAMMAR#cmp-expr takes any `type-name`, so this is a narrower test
than the grammar writes_。

## Method、`this` / `This` 與 default body

一個 **method** 是帶 **receiver** 的函式——被呼叫的那個實例，名為 **`this`**；receiver 自身的型別是 **`This`**。
`This` 在「具體型別尚未知」處指「**實作它的那個型別**」——同型別的運算元（`less(this, other: This) -> bool`）、或
**associated function** 的結果（`default() -> This`，也就是 constructor——它沒有 receiver，所以也沒有 `this`）——並在每個實作裡
解析成具體型別。**帶參數**的 spec 的參數（`Indexable[K, V]`）是**另一件事**：一個自由選的型別——每個 impl 固定
一次、摺進 (型別, spec) 身分裡；沒有第三種由 implementer 選的型別（見下節）；
唯有 `This` 是被逼定的 self-type，永非選擇。

spec 的 method 分兩種：

- **required**——只有簽名、無 body；每個 implementer 都必須供給。
- **provided**——簽名**帶 default body**，用 required（及其他 spec）method 作用在 `this` 上定義、碰不到 field。
  implementer **沿用**它、或以特化版**覆寫**（例如更快的 `contains`）；覆寫仍須維持慣常語意，且 `(型別, spec)` 的
  實作無論如何都保持 canonical。

> **[not yet]** 一個帶 **body** 的 `spec` 成員會在**宣告處**被拒絕,而不只是在呼叫處:
> _E9002 NotImplemented: a `spec` member with a BODY — a provided method's body is read and dropped here, so nothing in
> it is checked and it is not the method that runs; declare the signature and write the body in each `impl`_。
> 所以在這個編譯器裡,一個 `spec` 只有 required method,implementer 什麼都沒沿用到,而下面那套「免費得到一堆衍生
> method」的經濟——`Iterator` 由 `next` 發放 `map` / `filter` / `count`——底下沒有任何機制。這道拒絕在形式被寫出來
> 的那一點就指名了它,所以沒有任何程式走到 dispatch 這個問題。
>
> **[not yet]** 一個簽章可以是 **`unsafe`** 的——`GRAMMAR` 推導出 `fn-sig ::= 'unsafe'? 'mut'? 'fn' …`，所以
> `spec` 裡的 `unsafe fn peek() -> int` 就是一個成員——而這個編譯器沒有建出它。它會被讀到簽章結束、然後被指名
> 拒絕：_E9036 NotImplemented: the `unsafe` `spec` signature `peek`_，並帶上位置。理由與獨立的 `unsafe fn`
> （`E9027`）相同：這個關鍵字標出的信任邊界並未被強制（見 [FFI](../runtime/ffi.zh-TW.md)），而把簽章當成安全的
> 來讀，等於抹掉 `unsafe` 唯一說的那件事。至於**完全不**開啟任何成員的東西——`spec` 內文裡的 `unsafe { … }` 也
> 在其中——仍然拿到 `E2036`。

於是一個只有 1 個 required method 的 spec，能免費給 implementer 一堆衍生 method——`Iterator` 由 `next` 衍生
`map`、`filter`、`count`……——而「spec bound 就是完整介面」這條規則便讓它們**全部**（required 與 provided）都能對
被 bound 的 `T` 呼叫。這些 provided default 都是**行為性**的——寫在 method 上、不碰 fields；另一個由 compiler
讀取型別結構來生成 impl 的**結構性**層，見 [Derive 與預設行為](derive.zh-TW.md) 參考。

一個 method 或 function 可帶**自己的型別參數**、疊加在 receiver 上面：`map[U](this, f: fn(T) -> U)` 在 spec 的
`T` 與 receiver 的 `This` 之外再加一個 `U`，每個具體組合各 **monomorphize** 一份。provided method 也能泛型——這
正是讓 adapter 能改變 element 型別（`T` → `U`）的關鍵。

**dispatch 一致。** 每個 spec method，不論 required 或 provided，都解析到該型別的 **canonical impl**——有覆寫用
覆寫、否則用 default。所以一個 default body 呼叫另一個 spec method 時，會叫到型別的覆寫（用 `next` 定義的 default
`count`，會用被覆寫的 `next`）；**default 沒有靜態分派的例外**，而機制就是上面已經定義過的那一套。這對**直接在
具體值上呼叫**也成立（**[not yet]**——provided method 在其宣告處就被拒絕,見上）:`c.provided()` 有覆寫就跑該型別
的**覆寫**、否則跑 spec 的 **default body**——不需要裝箱,所以 provided method 並不侷限於動態分派那條路徑。

## Associated type 與 associated value

spec 承載**行為,別的都不承載**。它不宣告 **associated type**——由每個 impl 填入的型別 `type Item`,投影成
`This.Item`——也不宣告 **associated value**——每個 impl 供給的編譯期常數,`BITS: int` 要求、`BITS := 32` 供給。
兩者都不是這裡的成員種類,也都不是文法 derive 得出的語法:**皆被具名拒絕**。associated type 是由 **impl** 選定的
輸出,所以要檢查一處 `This.Item` 的使用,必須先拿到 impl 才知道那個運算式的型別——型別往回流進推導。參數化的
spec 把同一件事往前講:associated type 是每個 impl 一個輸出,參數則是每個引數一個 impl(`Indexable[K, V]`,見上)。

代價落在**單一輸出的協定**上。`Iterable[T]` 可以在不同 `T` 上有多個 impl,固定的 `Item` 不行,所以釘死元素型別
的是 **coherence**——每個型別至多一個這種 impl,而 compiler 對其他每一組 (型別, spec) 本來就在檢查它。

每個 impl 一份的**常數**寫成普通的**型別常數**（見下）——一個型別給自己的值,沒有 spec 要求它。

## 型別常數（Type constants）

**型別常數**是每個型別一份的編譯期值,宣告在該型別的 `impl` 內、寫成 **`val-bind`**——`NAME := <const-expr>`
——以 `Type.NAME` 讀取（型別內部用 `This.NAME`）。它的初始化式是**常數運算式**——字面量、其他常數,或它們摺疊後的
算術——由編譯器在**編譯期代換**,不執行任何程式碼、也沒有副作用（摺疊是隱含的,與 `const` 關鍵字無關,後者只標記
shadow-proof 綁定）。因為建構就是一次普通的**呼叫**,一個型別可以給自己正規值:`ORIGIN := Point(x: 0, y: 0)`,
以 `Point.ORIGIN` 取用。身為編譯期常數,`int` 型的那種**可用於任何需要編譯期常數的位置**——包含固定陣列長度
（`[byte; Buffer.SIZE]`,見 [Collection](../code/collections.zh-TW.md)）。可見性用一般的 `pub` / private。

它與離開本章的 **associated value** 共用一條 production,而兩者的差別正是它留下來的理由:型別常數**不指向任何
spec**。它是一個型別給自己的值,沒有 spec 要求它,**也沒有 spec 能要求它**——一個想要求它的 spec 就又回到「由
impl 選定的輸出」。必須**摺疊**的值用常數形式,必須**執行**的用 associated fn（`fn max() -> This`）。

> **[not yet]** `impl` 內的 `NAME := 32` 會報 _E9006 NotImplemented: an associated value binding `BITS := …` in
> an `impl`_,所以 `Type.NAME` 什麼都沒指名、`Point.ORIGIN` 宣告不出來。原本要由型別常數供給的固定陣列長度,
> 改寫成 module 層的常數。

## 內建 spec（Built-in specs）

每個內建行為都是一個 spec、**靠實作（或 derive）它才取得**——沒有自動實作的頂層 spec、也沒有隱式的。通用的結構性
操作（`copy`、`del`、傳參、存起來、送進 channel）屬於**記憶體模型**、不屬於任何 spec bound（見
[值與記憶體](memory.zh-TW.md)）;其中 `copy` 對每個型別都被強制、永不缺席。`debug` / `display`——開發者取向與給人看
的文字渲染,`display` 預設為 `debug`——屬於 [Formatting & Text](../runtime/format.zh-TW.md);它們的**結構化 auto-derive** 是
**[not yet]**。其餘一切都是型別 **opt-in** 的 spec、由泛型 bound 把關:

- **`Eq`**——結構化相等,驅動 `==` / `!=`,靠 `#[derive(Eq)]` 或手寫 `impl Eq` 取得;channel 或 `fn` 欄位以 identity
  比較。它**同時要求** `eq` 與 `ne`——只給其中一個的 impl 會得到
  _E3017 `P` does not implement `ne`, which `Eq` requires_——因為 `!=` 是被分派的,不是靠對 `==` 取反導出的。
  一個**沒有 `Eq` impl 的型別不能被比較**——對它用 `==` 是編譯錯誤、絕非靜默的結構化 default。

  > **[not yet]** 一個**容器**根本取得不到它,而這正是那條規則從一個它答不上來的方向被碰到:兩個 `list`、兩個
  > `map` 或兩個 tuple 的 `xs == ys` 是 _E9057 NotImplemented: `==` on a `list[int]` — structural equality over
  > a container is unbuilt, and a container has no declaration to derive it on_。無名形式該有的東西在
  > 〈型別〉的「由組成部分繼承」規則底下——一個 tuple 恰在它每個部分都有 `Eq` 時有 `Eq`——而沒建出來的正是那個
  > 推導。在那之前,請比較你真正想比較的那些元素。這與 [格式化](../runtime/format.zh-TW.md) 回報成 `E9059` 的是
  > 同一個洞,只差一個運算子。

Zerg **不設兩值之間的 instance-identity 測試**：copy-by-value 下值的副本本就是不同 instance、且無 aliasing，
「同一個 instance？」只對 channel 有意義——太 narrow、不值得一個運算子。相等在型別 opt-in 之處是**結構性**的 `Eq`。
**`is`** 關鍵字問的是另一件事——existential 上的**型別身分**測試 `x is T`（見型別測試）——「這裡 box 的是哪個具體
型別？」，而非「這兩個是不是同一個值？」。

> **[not yet]** 本節所描述的內建 spec 裡,恰好只有兩個被宣告出來:上面的 **`Eq`**,以及 **`Into[T]`**
> （見 [型別轉換](types.zh-TW.md#into--一個普通的轉換-spec)）。`Ord`、`Hash`、`Error`、`Iterator` / `Iterable`、
> sealed 的 `Ref`,以及每一個運算子 spec——`Add`、`Sub`、`Mul`、`Div`、`BitAnd`、`BitOr`、`BitXor`、`Not`、`Shl`、
> `Shr`——根本不以宣告的形式存在,所以它們指名不了:`impl Ord for P` 報 _error: E3013 no spec named `Ord`_,也就是
> 「沒有人寫過這個 spec」的普通訊息,而 `impl BitAnd for P` 報的也是同一句。**使用**那一側則是被運算子、而不是被
> spec 擋下:`P(1) < P(2)` 報的是 _E3044 operator `<` orders two numbers or two strs, and these are P and P_,
> 它指名的是運算元型別,對缺席的 `Ord` 隻字未提。其中好幾個所描述的**行為**是內建的、不經那個
> spec 也到得了——`int` 上的 `<`、`str` 的 `+` 串接、`Err` 所指的錯誤分類以及它回答的 `message()` / `unwrap()`、
> `chan` 的 refcounted 關閉——但它們由編譯器
> 擁有,使用者型別加入不了——不論那個型別上有沒有 `#[derive(Eq)]`。從這裡到本章結束的每一句,都是對著這個缺口
> 所寫的規範。

其餘的 spec 同樣是 **opt-in**——實作（或 derive）該 spec 才取得能力；泛型 bound 以它把關：

- **`Ord`**——一個與 `Eq` 一致的 **total** order,由**單一必需的 `less`**(`<`)定義、並要求 `Eq` 為 super-spec
  (`spec Ord: Eq`);`<=` `>` `>=` 與 sort 都由它配 `Eq` 導出,而 `min` / `max` / `clamp` 是建在 `Ord` bound 上的普通
  stdlib helper——**沒有三路 `Ordering`** 值,只有 `less` 與 `Eq`。`str` 依 **code point 字典序**排序(＝ byte 序,因其
  UTF-8 有效——非 locale collation,那是另一個 stdlib 功能);`float` 同時退出 `Ord` 與 `Hash`(理由見下)。
- **`Hash`**——`map` / `set` 的 key，`equal ⇒ same hash`。`str` 不可變、是天然的 key。**[not yet]**
- **`Iterator`** / **`Iterable`**——迭代協定（見下方 **迭代**）。
- **`Error`（`Err`）**——錯誤層：`message() -> str`、`unwrap() -> Err?`（底層 cause、無則 `nil`）、
  `code() -> byte?`（可選小碼）。
- **`Add` / `Sub` / `Mul` / `Div` / … 與 bitwise `BitAnd` / `BitOr` / `BitXor` / `Not` / `Shl` /
  `Shr`**——值運算子（`+ - * / %`、`& | ^ ~ << >>`、indexing…）：運算子多載，見下。`str` 實作 `Add`，所以 `+` 會
  **串接**成新字串（見 [Collection](../code/collections.zh-TW.md)）。
- **`Into[T]`**——轉換 spec:型別宣告它能轉成什麼,泛型程式碼以它為 bound;轉換永遠**寫出來**、絕不由
  position 套用。它**不出貨任何內建 impl**——數字之間的轉換是 `T(x)`,轉成文字是 `str(x)`,而後者每個型別都
  透過 `display` 有答案（見 [型別轉換](types.zh-TW.md#into--一個普通的轉換-spec)）。

**`Ref`——copy-by-ref（sealed）。** 與上面每個 spec 不同，實作它不加行為——它改變值的**表徵（representation）**。`Ref`
型別是 **reference-counted**：複製是對共享計數 ++、而非深拷貝，它的 `drop(this)` 在最後一個持有者的 scope 退出時
**跑一次**。編譯器提供計數與 by-ref 複製；只有 `drop` 的內容由使用者寫。`Ref` 是 **sealed** 的——唯二實作者是內建的
**`chan`**（其 `drop` 即 close）與 stdlib 的 **`Ref[T]`** 資源盒（見 [值與記憶體](memory.zh-TW.md)）。
一般程式碼**使用 `Ref[T]`、
絕不實作 `Ref`**——所以「這個值是否以 reference 共享？」始終有明確答案：只有 `chan` 與 `Ref[T]` 是。

**運算子 desugar 到 spec**，所以 user type 可以靠實作對應 spec 來多載值運算子——`==` / `!=` 已經走 `Eq` 的 `eq` /
`ne`，`<` 走 `Ord`。
多載必須維持**慣常**語意（`+` 不是加法就是濫用，違背 `small and crisp`）。**邏輯運算子都是關鍵字**——`not`
（一元），以及**會短路的** `and` / `or`——只作用於 `bool`、回傳 `bool`（不吃 truthiness；要判斷就 `bool(x)`）：`and`
在左側為 `false` 時跳過右側、`or` 在左側為 `true` 時跳過右側；logical xor 就是 `a != b`（**沒有** `xor` 關鍵字——它
無法短路，是普通運算、不是關鍵字）。這些、以及 null-safety 運算子（`?`、`??`、`?.`、`!`），都是**固定構造——永不
可多載**；bitwise 符號（`& | ^ ~`，見 [整數運算](types.zh-TW.md)）永不與它們撞臉。

`float` 退出 `Ord` 與 `Hash`——`NaN` 破壞全序與 `equal ⇒ hash`——所以 `float` 永遠不是排序集合的元素、也不是 key，
而一個**含** `float` 的複合型別會透明地繼承這點：一個 **derive 出的 `Eq`** 用 `==` 比那個欄位，所以對 `NaN`
**非自反**，該型別也**得不到** `Ord`/`Hash`。要讓這種型別當 key 或可排序，作者得**顯式實作**、並處理 `float` 的兩個
陷阱：`Hash` 需要一個**自反**的 `eq` 並 canonicalize **`±0.0`**（相等、故必須同 hash）；`Ord` 需要一個
**total order**（IEEE `totalOrder`，`NaN` 排到端點）。一個 stdlib 的 total-order／hashable `float` wrapper 延後。

**迭代。** 一個 **`Iterator[T]`** 有 `next() -> Result[T]`——`Left(v)` 是下一個元素，`Right(StopIteration)` 表示
結束（**`StopIteration`** 是內建的 `Err`）。一個 **`Iterable[T]`** 有 `iter()`、產生一個全新的 `Iterator[T]`。
元素型別由 **coherence** 釘死、而不是由某種成員種類：一個型別至多宣告**一個** `Iterable[T]` impl，所以
`for x in X` 仍然只有**一個**元素型別——它需 `X: Iterable[T]`、把 `x: T` 綁到每個 `Left`，
**在 `Right(StopIteration)` 乾淨結束**，而**對任何其他 `Right(err)` 則 raise**——迭代中途的失敗絕不被靜默
吞掉（要檢視就手動 `next()` 再 `guard`）。因為 `<-ch` 本就回 `Result[T]`，**channel 就是一個 `Iterator[T]`**：
`for v in ch` 會 drain 它，在乾淨關閉時結束、並把 producer 的崩潰重新 raise。`Iterator[T]` 也 trivially 是
`Iterable[T]`，所以 **lazy adapters**（`map`、`filter`、`take`、`zip`…）就是普通的 stdlib 迭代器、可鏈式——每個
回傳一個**具體 adapter 型別**（`map` 回傳身為 `Iterator[U]` 的 `Map[This, U]`，存著來源與 closure），所以整條鏈全程
**monomorphize**、不 box。`for mut x in X` 把每個元素綁成就地的 `mut`——僅當 `X` 為 `mut`。
