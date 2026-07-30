# Zerg 格式化與文字（Formatting & Text）

每個值如何渲染——內建的 `display` / `debug` 渲染、`f"…"` 內插,以及 `print` 關鍵字。屬於
[語言參考](../language.zh-TW.md) 的一部分。亦有 [English](format.md) 版本。

每個值都有兩種渲染。它們是**內建的值渲染**、不是任何 `Object` spec 的 method——Zerg 沒有 auto-implement 的
`Object` spec（[Spec 與 Generics](../core/specs.zh-TW.md)）——所以 `display` 與 `debug` 在每個值上都可用、不需 opt-in 任何
東西：

- **`debug`——開發者視圖。** **結構化**渲染（sum 則先 tag 再 payload）、可 override。logging、`stderr`、abort
  backtrace 用的就是它——機械、不憑空猜文案。
- **`display`——給人看的視圖。** 它的**預設就是 `debug`**，所以永遠存在。override 它來決定終端使用者該怎麼讀這個值
  （金額、日期）；compiler 永不衍生語意化渲染，所以有意義的 `display` 只能 override。

> **狀態。** 渲染一個**純量**或一個 **`str`**——透過純 `{x}` 洞、`print`，或 f-string——可用。
> **複合值的結構化渲染**（`struct`、`list`、`map`）為 **[not yet]**：今日格式洞裡的複合值會在**編譯期被拒絕**，
> 所以「每個值都能渲染」對純量與字串現已成立，複合值則待結構化 `debug` 落地。因此結構化 `debug` 字串的確切拼法
> **尚未被釘定**（[not yet]）。

**內插——`f"…"`。** 裸 `"…"` 是字面量（大括號是普通字元）。**`f`-string** 內嵌 `{ expr }`，透過 `display` 渲染
再串接——`f"sum={x + y}"`——在**編譯期 desugar** 成 `str` 串接（Collections），不需 variadic、無 runtime 格式
引擎。洞是 **Python 形狀**——`{ expr =? !conv? :spec? }`：

- **`{x}`** 用 `display`；**轉換**可先換視圖——**`!r`** 用開發者 `debug`、**`!s`** 用 `display`、**`!a`** 用
  ASCII-escaped 的 debug。`f"{x!r}"` 把 `x` 以 `debug` 渲染。三者皆為 **[not yet]**——洞裡的轉換會被指名拒絕。
- **`{x=}`** 自述：印出運算式原文、`=`，再接值——`f"{n=}"` → `n=42`（可與其餘組合：`f"{n=:04d}"`）。**[not yet]**
  ——已被解析，但此階段在**程式碼生成時被拒絕**。
- **`{x:spec}`** 把 spec 字串交給型別的 **`Format`** 協定——`f"{pi:.2f}"`、`f"{n:04d}"`、`f"{p:>10}"`。這是
  **per-type 協定**、非 `display` 參數：語言只固定 `:spec` 的**語法**（到 `}` 為止的不透明文字）；一個 spec 的**意義**
  由型別自定——stdlib 數字與 `str` 讀常見的 `[[fill]align][sign][#][0][width][.precision][type]`，比照 Python。對
  format spec 為 **[not yet]**——洞裡的 spec 會被指名拒絕。
  > **[implementation-defined]** 浮點渲染——預設的 `%g` 式（6 位有效數字）以及 `NaN`、`Inf`／`-Inf`、`-0.0` 的
  > 拼法——規格不釘定；conforming 實作各自載明。切勿依賴確切的浮點拼法。

**`print`** 把一個值的 `display` 渲染加換行寫到 stdout——一個**保留字**、永遠在 scope 內、免 import，所以最小的
程式就是 `print f"hello {name}"`。它**盡力而為**（寫入錯誤被丟掉、不 raise），所以不需 `?`；有檢查的完整 I/O 面是
要 import 的 `io` package（見 [Process & I/O](io.zh-TW.md)）。
