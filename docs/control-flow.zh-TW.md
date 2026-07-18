# Zerg 控制流與模式比對（Control Flow & Pattern Matching）

三個控制構造——`if` / `for` statement 與 `match` expression——以及它們解構的 pattern。屬於
[語言參考](language.zh-TW.md) 的一部分。亦有 [English](control-flow.md) 版本。

## 控制流（Control flow）

全部控制流就三個構造，依「產出什麼」區分：**`match`** 產出一個**值**（模式比對）；**`if`** 與 **`for`** 是為副作用
而跑的 **statement**。分支要拿到值一律用 `match`（或 `??` / `?.`）——`if` 永不產出值，所以一個選擇只用一種方式產出
值，不是兩種。

**`if`**——`if cond { … }`，可接 `else` / `else if`；條件是 `bool`（沒有 truthiness）。**綁定形式**
`if x := expr { … }` 只在 `expr` 命中 pattern `x` 時跑區塊——「值存在」測試的單臂 `match` 語法糖
（`if v := <-ch { use(v) }` 只在 `Left` 時觸發）。

**`for`**——唯一的迴圈關鍵字、兩種形式：**`for { … }`** 無窮（用 `break` / `return` 離開）、以及
**`for x in it { … }`** 走訪 `it: Iterable`，每一輪以 **copy** 綁定 `x`（**`for mut x`** 就地綁定，僅當 `it` 為
`mut`；迭代協定——`StopIteration` 乾淨結束、其他 error re-raise——見 [迭代](specs.zh-TW.md)）。**沒有 `while`、也沒有 C 式三段
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
值，如此而已；它對 existential 唯一允許的，是布林的 **`is`** 測試（見 [Spec 與 Generics](specs.zh-TW.md)），用作**條件**、絕不作為交回
具體值的綁定。一個 **product pattern** 能**依欄位**解構一個 `struct`（`Div{q, r}`）、或**依位置**解構一個 tuple（`(a, b)`），每一
部分以 copy 綁定；它在 `match` arm 與普通的 `:=` 綁定（`(q, r) := divmod(x, y)`，也就是多重回傳被消費的方式）都可用。
**guard 條件**（`Left(v) if v > 0`）仍延後。
