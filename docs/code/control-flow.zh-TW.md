# Zerg 控制流與模式比對（Control Flow & Pattern Matching）

三個控制構造——`if` / `for` statement 與 `match` expression——以及它們解構的 pattern。屬於
[語言參考](../language.zh-TW.md) 的一部分。亦有 [English](control-flow.md) 版本。

## 控制流（Control flow）

全部控制流就三個構造，依「產出什麼」區分：**`match`** 是產出一個**值**的 **expression**（模式比對）；**`for`** 是為
副作用而跑的 **statement**；**`if`** 則**兩者皆是**——既是 statement，也（在帶強制結尾 `else` 時）是 expression。**區塊**
（block）本身就是一個 expression，其值是**最後一個 statement 的值**，所以以 expression 收尾的分支會把該值帶出來。

**`if`**——作為 **statement** 時，`if cond { … }`（可接 `else` / `else if`）為副作用而跑、不產出值；條件是 `bool`
（沒有 truthiness）。帶**強制結尾 `else`** 時它反而是 **expression**（`if-expr`，一個 `primary`）：它產出**被選中分支
的區塊值**，且**每個分支必須產出相同型別**（`x := if hot { warm() } else { cool() }`）。在 statement 位置以 statement
形式為準，所以到達 `:=`、`return`、或引數的是那個值形式。**綁定形式** `if x := expr { … }`(if-let)只在 `expr`
**存在**時跑區塊——一個持有值的 optional、一個收到值的 `<-ch`(一個 `Left`)——並把**解包後的值**綁成 `x`、**僅在
then 區塊內**:`x` 不在 `else` 的作用域、也不在 `if` 之後。它是「值存在」測試的單臂 `match` 語法糖(`if v := <-ch
{ use(v) }` 只在 `Left` 時觸發),並在 **`if` 能出現的任何位置**都可用——作為 statement、作為運算式(帶尾隨 `else`)、
以及作為被回傳的 if 運算式(`return if x := opt { use(x) } else { fallback }`)。if-let 綁定形式是
**[implemented]**,包含 non-POD 的 `str?`——解包後的 `str` 僅綁在 then 區塊內。

**`for`**——唯一的迴圈關鍵字、三種形式：**`for { … }`** 無窮（用 `break` / `return` 離開）、
**`for x in it { … }`** 走訪 `it: Iterable`，每一輪以 **copy** 綁定 `x`（**`for mut x`** 就地綁定，僅當 `it` 為
`mut`；迭代協定——`StopIteration` 乾淨結束、其他 error re-raise——見 [迭代](../core/specs.zh-TW.md)）、以及 **`for cond { … }`**
即 **while** 形式——當 `cond`（一個 `bool`）成立時反覆執行。**沒有 `while` 關鍵字**（裸 `for cond` 就是 while 迴圈）、
也**沒有 C 式三段 `for`**。無窮形式、while 形式、以及 `for x in it` 走訪一個 **range**、一個 **`list`**、一個
**`map`**（綁每個 **key**）或一個 **`str`**（綁每個 **`rune`**）都是 **[implemented]**；**`for mut x` 走訪 non-POD
元素**——一個 `list[str]`、或遞迴／裝箱型別的元素——是 **[not yet]**。把 **range 當成值**、以及用 **`v in range`**
測試成員關係（`x in 0..n` → `bool`）都是 **[implemented]**。

**`break` / `continue`** 作用於**最內層的 `for`**；**沒有 label**（要跳出外層就把內層抽成函式再 `return`）。語法糖
**`break if cond`** / **`continue if cond`** 完全等於 `if cond { break }` / `if cond { continue }`，兩者皆
**[implemented]**：

```text
for {
    line := <-input ?? break       # 收到 channel 關閉為止
    continue if line.empty()       # 跳過空行
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

## 模式比對（Pattern matching）

`match` 是一個 **expression**：它用 **arm**（`pattern => result`，arm 分隔符 `=>` 刻意與引入函式回傳型別的 `->`
區分）逐一試一個值，跑第一個命中的、產出它的 result。
每個 arm 產出**相同型別**，所以 `match` 是個值，可用於 `:=`、`return`、或引數——產出 `nil` 的 arm 讀來就是普通
statement。覆蓋是**必需**的——漏掉某個 case 的 `match` 是**編譯錯誤**（所以**新增一個 dependent 的 `match` 未處理的
`enum` variant 會讓建置失敗**，在編譯期抓到、而非默默放過）。帶 guard 或 range 的 arm（見下）**不**計入覆蓋——編譯器
無法證明 guard 成立——所以該 case 仍需要一個**無 guard** 的 arm 或結尾的 **`_`**。既然每個值都已被靜態覆蓋，
`MatchError` 只是那個殘餘 guard-gap 的執行期後備；而**多餘**的 arm（已被前面 arm 覆蓋者）是 warning。

一個 **pattern** 是下列之一：**帶 payload 綁定的 variant**（`Left(v)`）——以 **copy** 綁定，一如 `?`/`return`、來源
永不失效；**literal**（`0`、`"y"`、`true`、或負數 literal）——以值比對；**nested** pattern（`Left(Some(v))`）；
或**萬用 `_`**，比對任何值、不綁定。這些連同下面的 **product pattern** 都是 **[implemented]**。一個**帶綁定的
or-pattern**（`A(x) | B(x) =>`，各分支綁同名、同型）與一個 **list pattern**（`[h, ..t]`）是 **[not yet]**：`GRAMMAR`
兩者皆導得出——list pattern 連型別檢查都過——但今天 code generator 會拒絕它們,所以兩者都先別用。

```text
msg := match ev {
    Click(p)  => render(p)
    Scroll(d) => scroll(d)
    _         => nil
}
```

> **註。** 對**巢狀** payload 的 exhaustiveness 檢查目前弱於完整覆蓋：compiler 證明頂層 variant 已覆蓋，但不總是
> 證明每一種巢狀組合，所以一個巢狀 case 可能編譯通過、落到 `MatchError` 後備，而一個完全精確的檢查器會要求再多一個
> arm。

`match` 的 **pattern** 永不窺看 existential 的真實型別——spec 當型別用是單向抹除、無 downcast——它只解構 variant、比對
值，如此而已；它對 existential 唯一允許的，是布林的 **`is`** 測試（見 [Spec 與 Generics](../core/specs.zh-TW.md)），用作**條件**、絕不作為交回
具體值的綁定。一個 **product pattern** 能**依欄位**解構一個 `struct`（`Div{q, r}`）、或**依位置**解構一個 tuple（`(a, b)`），每一
部分以 copy 綁定；它在 `match` arm 與普通的 `:=` 綁定（`(q, r) := divmod(x, y)`，也就是多重回傳被消費的方式）都可用；
product pattern 是 **[implemented]**。**guard 條件**是 **[implemented]**：一個 arm 可在 pattern 之後帶一個
**`if expr`**（`Left(v) if v > 0`），它也必須成立該 arm 才觸發；guard 看得到 pattern 的**綁定**，而在 `A | B if c`
上（待 or-pattern 落地——見上）涵蓋**整個 or-pattern**。帶 guard 的 arm **不**計入 exhaustiveness，所以帶 guard 的
case 仍需要一個無 guard 的 arm 或 `_`。一個 **range arm**（`200..300 =>`、`400..=499 =>`、`500.. =>`）是 match 專屬的
語法糖，等同 `_ if _ in <range>`——它以**range 包含關係**比對（不是值相等）、**不綁定**任何值、同樣不計入覆蓋（要綁值
就寫 `x if x in <range>`）；它是 **[implemented]**。
