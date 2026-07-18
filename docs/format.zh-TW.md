# Zerg 格式化與文字（Formatting & Text）

每個值如何渲染——`debug` / `display` 這兩個 `Object` method、`f"…"` 內插,以及 `print` 關鍵字。屬於
[語言參考](language.zh-TW.md) 的一部分。亦有 [English](format.md) 版本。

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
