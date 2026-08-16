# Zerg 控制流與模式比對（Control Flow & Pattern Matching）

三個控制構造——`if` / `for` statement 與 `match` expression——以及它們解構的 pattern。屬於
[語言參考](../language.zh-TW.md) 的一部分。亦有 [English](control-flow.md) 版本。

## 控制流（Control flow）

全部控制流就三個構造，依「產出什麼」區分：**`match`** 是產出一個**值**的 **expression**（模式比對）；**`for`** 是為
副作用而跑的 **statement**；**`if`** 則**兩者皆是**——既是 statement，也（在帶強制結尾 `else` 時）是 expression。**區塊**
（block）本身就是一個 expression，其值是**最後一個 statement 的值**，所以以 expression 收尾的分支會把該值帶出來。

**區塊自己就到得了值的位置**：`x := { y := 1  y + 1 }`、當作引數的區塊、`return` 之後的區塊。它的值是最後一個
statement 的值——expression statement 交出它的 expression，其他任何 statement、以及空區塊，都交出 `nil`。lexer 插入的
`;` 只**分隔** statement，並不像某些語言的結尾 `;` 那樣把值丟掉。決定一個 `{` 開的是區塊還是 **map literal** 的是那個
`:`（見[型別](../core/types.zh-TW.md)與 `GRAMMAR#map-lit`），這也正是空 map 寫成 `{:}`、而沒有 `:` 的大括號永遠是區塊
的理由。

在**一個 statement 的開頭**，同樣的大括號是一個區塊 **statement**、其值被丟棄；而在 `if` / `for` / `with` / `match`
的 head 開頭以 `{` 起始的運算式必須加括號（`E290`）。

**`if`**——作為 **statement** 時，`if cond { … }`（可接 `else` / `else if`）為副作用而跑、不產出值；條件是 `bool`
而且**沒有 truthiness**,所以放一個 optional 進去會得到 _E354 the condition of an `if` … must be bool, and this one
is int? — bind it with `if v := x { … }`, which also hands over what it holds_。帶**強制結尾 `else`** 時它反而是
**expression**（`if-expr`，一個 `primary`）：它產出**被選中分支
的區塊值**，且**每個分支必須產出相同型別**（`x := if hot { warm() } else { cool() }`）。在 statement 位置以 statement
形式為準，所以到達 `:=`、`return`、或引數的是那個值形式。**綁定形式** `if x := expr { … }`(if-let)只在 `expr`
**存在**時跑區塊——一個持有值的 optional、一個收到值的 `<-ch`(一個 `Left`)——並把**解包後的值**綁成 `x`、**僅在
then 區塊內**:`x` 不在 `else` 的作用域、也不在 `if` 之後。它是「值存在」測試的單臂 `match` 語法糖(`if v := <-ch
{ use(v) }` 只在 `Left` 時觸發),並在 **`if` 能出現的任何位置**都可用——作為 statement、作為運算式(帶尾隨 `else`)、
以及作為被回傳的 if 運算式(`return if x := opt { use(x) } else { fallback }`)。它也載得住 non-POD 的
`str?`——解包後的 `str` 僅綁在 then 區塊內。

**每個分支必須產出相同型別**,而兩個構造用同一句話說它——_E321 an `if` expression answers ONE type, and its
branches give int and float_,與 `match` 的 `E322` 並排。`nil` 分支是例外,而它其實不算例外:它沒有型別可以不一致,所以
`x: int? = if c { 1 } else { nil }` 是一個 carrier,一邊拿值、一邊拿缺席。其他每個分支都自帶型別,literal 也不
例外——一個分支不是它兄弟的 typed position,就像一個 match arm 不是下一個 arm 的一樣。

那個 `nil` 分支降階成**缺席**:填入 carrier 的動作被分配到各個分支上,每一種拼法各自進入 carrier,所以 `c` 為假時
`x: int? = if c { 1 } else { nil }` 是空的——`x ?? 99` 回答 `99`。過去它把整個三元式當成單一個 present payload 包
起來,`nil` 於是變成 present carrier 裡的一個零,缺席就這樣不見了、而且任何階段都沒有回報;鏡像的寫法(`nil` 在
**then** 分支)則是直接被拒絕——一種拼法會抱怨,另一種靜默答錯。

---

> **[not yet]** 值形式只剩一種樣子會被指名拒絕：**在運算式位置的 if-let**。
> `return if x := opt { use(x) } else { fallback }`，以及任何抵達 `:=` 或引數的 if-let，都會回報
> _E270 NotImplemented: a binding head in an `if` EXPRESSION_——所以這個階段綁定形式只是 statement，而不是上文
> 所載的「`if` 能出現的任何位置」。**`else if` 串**曾與它並列，現在不再：`x := if a { 1 } else if b { 2 } else { 3 }`
> 已建置、產出被取用的那一支，而且一型規則橫跨整串成立。

**`for`**——唯一的迴圈關鍵字、三種形式：**`for { … }`** 無窮（用 `break` / `return` 離開）、
**`for x in it { … }`** 走訪 `it: Iterable`，每一輪以 **copy** 綁定 `x`（**`for mut x`** 就地綁定，僅當 `it` 為
`mut`；迭代協定——`StopIteration` 乾淨結束、其他 error re-raise——見 [迭代](../core/specs.zh-TW.md)）、以及 **`for cond { … }`**
即 **while** 形式——當 `cond`（一個 `bool`）成立時反覆執行。**沒有 `while` 關鍵字**（裸 `for cond` 就是 while 迴圈）、
也**沒有 C 式三段 `for`**。無窮形式、while 形式、以及 `for x in it` 走訪一個 **range**、一個 **`list`**、一個
**`map`**（綁每個 **key**）都可用。走訪一個 **`str`** 會綁每個 **`rune`**——是 code point 而不是 byte;
要走 byte 就用 `bytearray(s)`。**`for mut x`**（把改過的元素寫回原槽的可變迴圈綁定）是 **[not yet]**（`E242`）。用
**`v in range`** 測試成員關係（`x in 0..n` → `bool`）可用。把 **range 當成值**用在別處則是 **[not yet]**——這個形式
會被指名拒絕、帶位置（`E493`）；range 今天只存在於「`for` 走訪的東西」、「`match` arm 包含的東西」與「`in`
拿來測的東西」裡。

**`break` / `continue`** 作用於**最內層的 `for`**；**沒有 label**（要跳出外層就把內層抽成函式再 `return`）。語法糖
**`break if cond`** / **`continue if cond`** 完全等於 `if cond { break }` / `if cond { continue }`。同一個
後綴 `if` 也載得住 `return` 與 `raise`：

```text
for {
    line := <-input ?? break       # 收到 channel 關閉為止
    continue if line == ""         # 跳過空行
    break if line == "quit"        # 遇到 sentinel 就停

    handle(line)
}
```

**`return`**帶同一個後綴 `if`：**`return expr if cond`** 是 `if cond { return expr }` 的語法糖（**`return if cond`**
則是不帶值的提早退出）——條件為**假**時控制**落穿**（fall through）繼續往下（`return MAX if v > MAX`）。留意消歧義：
條件後接一個**區塊**的前導 `if` 反而是**被 return 出去的 if-expression**（`return if c { a } else { b }` 產出一個值）；
conditional-return 的 `if` 取的是**裸條件、沒有區塊**。

`for` 是 statement——不產出值；要組結果就鏈一個 iterator adapter（`map` / `filter` / `fold`）或 append 進另一個
collection（[Collections](collections.zh-TW.md)），不要 break-with-value。

> **[not yet]** iterator adapter 尚未建置：`map`、`filter` 與 `fold` 三者都是 _E444 NotImplemented: the list
> method `…` — this compiler has `len` and `append`_，所以這個階段要把結果帶出迴圈，唯一的辦法是 append 進另一個
> collection。

## 模式比對（Pattern matching）

`match` 是一個 **expression**：它用 **arm**（`pattern => result`，arm 分隔符 `=>` 刻意與引入函式回傳型別的 `->`
區分）逐一試一個值，跑第一個命中的、產出它的 result。arm 的 body 是一個**運算式**（`GRAMMAR#match-arm`），而區塊
**就是**運算式——所以 `pattern => { … }` 可以裝好幾個 statement 並且照樣產出值，它的值就是該區塊最後一個 statement
的值。arm 的整個 body **不能**是一個 statement,因為那樣 arm 就沒有東西可以產出了:`1 => print "one"` 是
_E605 NotImplemented: `print` is a statement, and an expression is wanted here_(arm 裡的 `return` 也是同一則),
而一次 reassignment 或一次 send 是 _E607_、訊息會指名它遇到的是哪一種。加上大括號,arm 就有了一個會產出值的區塊。
每個 arm 產出**相同型別**(_E322 a `match` answers ONE type, and its arms give … and …_),所以 `match` 是個值,可
用於 `:=`、`return`、或引數——產出 `nil` 的 arm 讀來就是普通 statement。覆蓋是**必需**的——漏掉某個 case 的
`match` 是 _E428 non-exhaustive match: missing variant …_（所以**新增一個 dependent 的 `match` 未處理的
`enum` variant 會讓建置失敗**，在編譯期抓到、而非默默放過）。帶 guard 或 range 的 arm（見下）**不**計入覆蓋——編譯器
無法證明 guard 成立——所以該 case 仍需要一個**無 guard** 的 arm 或結尾的 **`_`**。既然每個值都已被靜態覆蓋，
`MatchError` 只是那個殘餘 guard-gap 的執行期後備；而**多餘**的 arm（已被前面 arm 覆蓋者）是 warning。

> **[not yet]** **多餘 arm 的 warning** 尚未建置：一個已被前面 arm 覆蓋的 arm 什麼都不會產生——沒有 warning、也
> 沒有提示——而且它會以「沒有任何值到得了的 arm」留在 emit 出來的程式碼裡。反方向的覆蓋，也就是沒有任何 arm
> 處理的 case，是有檢查的，而且是 error。

一個 **pattern** 是下列之一：**帶 payload 綁定的 variant**（`Left(v)`）——以 **copy** 綁定，一如 `?`/`return`、來源
永不失效；**literal**（`0`、`"y"`、`true`、或負數 literal）——以值比對；**nested** pattern（`Left(Some(v))`）；
或**萬用 `_`**，比對任何值、不綁定。這些連同下面的 **product pattern**、以及一個 **range** arm（`1..=2 =>`，以
containment 比對）都會觸發。一個 **or-pattern**（`A | B =>`，以及各分支綁同名同型的綁定形式
`A(x) | B(x) =>`）與一個 **list pattern**（`[h, ..t]`）是 **[not yet]**：`GRAMMAR` 兩者皆導得出。

**`str` literal** 的 arm 比的是**文字**,走的是 expression 的 `==` 所用的同一個 `strcmp`。它曾被降階成**指標**
比較,所以 `match s { "y" => 1  _ => -1 }` 在 `s == "y"` 時回答 `-1`——而且無聲,因為結尾的 `_` 吸收掉每一次落空,
且兩個相等的 literal 不保證共用儲存。

> **[not yet]** 上面三種 pattern 是在 **parser** 裡就被拒絕的,所以它們沒有一種抵達得了檢查器或 emitter,也因此每
> 一種都指名自己:
>
> - **nested pattern**——`Left(Some(v))`，還有 `L(0)`——是 _E492 NotImplemented: a sub-pattern inside a variant
>   payload_,所以 payload 位置只收一個綁定名字或 `_`,每個 pattern 都只有一層深;
> - **or-pattern** 是 _E241 NotImplemented: an or-pattern_——否則那裡的 `|` 會被讀成位元運算子,把 `1 | 2 =>`
>   折成 `3 =>`、兩側都不中,那正是編譯器最不該給的靜默錯答案。`zerg fmt` 會改寫唯一有可用寫法的那個情況(連續整數
>   收成 range `1..=2`,規則 `F408`);
> - **list pattern** 是 _E240 NotImplemented: a list pattern in a `match` arm_——改用索引與切片來解構一個 list。
>
> 在 parse 就拒絕,也把意圖中那個檢查器唯一的軟處掏空了:對**巢狀** payload 的 exhaustiveness 本來會弱於完整覆蓋
> ——證明頂層 variant 已覆蓋、卻不總是證明每一種巢狀組合。現在已經沒有巢狀 case 可以讓它弱了。

```text
msg := match ev {
    Event.Click(p)  => render(p)
    Event.Scroll(d) => scroll(d)
    _               => nil
}
```

`match` 的 **pattern** 永不窺看 existential 的真實型別——spec 當型別用是單向抹除、無 downcast——它只解構 variant、比對
值，如此而已；它對 existential 唯一允許的，是布林的 **`is`** 測試（見 [Spec 與 Generics](../core/specs.zh-TW.md)），用作**條件**、絕不作為交回
具體值的綁定。一個 **product pattern** 能**依欄位**解構一個 `struct`（`Div{q, r}`）、或**依位置**解構一個 tuple（`(a, b)`），每一
部分以 copy 綁定；它在 `match` arm 與普通的 `:=` 綁定（`(q, r) := divmod(x, y)`，也就是多重回傳被消費的方式）都可用；
product pattern 是 **[not yet]**:用 `.0` / `.1` 與欄位存取來解構。它的四種樣子各自被自己的名字拒絕——綁定位置是
`E238` 與 `E221`、arm 裡是 `E232` 與 `E243`——所以 tuple 與 struct 在訊息裡分得開,而不是共用一句。**guard 條件**可用:一個 arm 可在 pattern 之後帶一個
**`if expr`**（`Left(v) if v > 0`），它也必須成立該 arm 才觸發；guard 看得到 pattern 的**綁定**，而在 `A | B if c`
上（待 or-pattern 落地——見上）涵蓋**整個 or-pattern**。帶 guard 的 arm **不**計入 exhaustiveness，所以帶 guard 的
case 仍需要一個無 guard 的 arm 或 `_`。一個 **range arm**（`200..300 =>`、`400..=499 =>`、`500.. =>`）是 match 專屬的
語法糖，等同 `_ if _ in <range>`——它以**range 包含關係**比對（不是值相等）、**不綁定**任何值、同樣不計入覆蓋（要綁值
就寫 `x if x in <range>`）。它的 **bound 是編譯期常數**:一個 literal，或一個常數的**名字**
（[`GRAMMAR#range-bound`](../../GRAMMAR)），也就是任何初始式編譯器折得動的 `:=` 或 `const` 綁定——所以 `LO..HI`
與 `MID..=HI` 讀起來就是它們指的那些數字，而由其他常數搭出來的 bound（`const MID := LO + 50`）與直接寫死的一樣好。
一個**不是**常數的名字——執行期才讀到的值、`mut` 綁定——會報在要它的那個 arm 上，而不是報在該綁定自己那行；而
**呼叫**根本不是 bound，因為該產生式導不出呼叫，所以 `f()..300` 由 parser 擋下。
