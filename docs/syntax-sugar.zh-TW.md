# Zerg 語法糖（Syntax Sugar）

Zerg 保持一個**精簡核心**,在其上疊了幾個方便的表面寫法——每個都是 desugar 回核心的**語法糖**,語意上沒有新東西要
學。本頁把它們收在一處;各主題的完整說明見 [語言參考](language.zh-TW.md)。亦有 [English](syntax-sugar.md) 版本。

## 已落地的語法糖

| 語法糖                                | Desugar 成                                                    |
| ------------------------------------- | ------------------------------------------------------------- |
| `break if c` / `continue if c`        | `if c { break }` / `if c { continue }`                        |
| `if x := e { … }`                     | 對 `e` 的 one-arm `match`——`x` 存在時才執行區塊               |
| `with e as y { … }`                   | `{ y := e; defer y 的 Scoped teardown; … }`(每條離開路徑都跑) |
| `f"…{x}…"`                            | 編譯期把各段 `str` 串接,每個洞 `x.display()`                  |
| `f"{x!r}"` / `f"{x=}"`                | `f"{x.debug()}"` / 原文 `x=` 再接值                           |
| `f"{x:spec}"`                         | 經 `Format` protocol 呼叫 `x.format(spec)`                    |
| `a + b`、`a == b`、`a[i]`、`-a`、…    | 該運算子的 spec 方法——`a.add(b)`、`a.equal(b)`、…             |
| `for x in it { … }`                   | 對 `it` 的迭代協定(以 `StopIteration` 收尾)                   |
| `x..y`                                | builtin `range(x, y)`——半開區間值                             |
| `(a, b) := e` / `P{x, y} := e`        | 解構 product/tuple 回傳,各部分**以 copy** 綁定                |
| `f(x: 1)`(named)/ `p: T = e`(default) | 呼叫端改寫為 positional;default `e` 每次呼叫時求值            |
| `print x`                             | best-effort 把 `x.display()` 加換行寫到 stdout                |
| `e?`                                  | 取出 `Left`,否則從函式提前 return 那個 `Right`                |
| `a ?? b` / `a?.m` / `e!`              | default;optional chain 成 `nil`;force-unwrap 否則 raise       |
| `del ch`                              | 現在就 drop 這個持有者——若是最後 sender 便關閉 channel        |

## 刻意**不是**語法糖的

為了讓核心誠實,有些看起來像糖的其實是獨立的東西,不是改寫:

- **`type X = Y`** 是**強 typedef**——全新、獨立的型別,非透明別名。
- **`+%` / `-%` / `*%`** 是**獨立的回繞運算子**,不是 `+` / `-` / `*` 的修飾。
- **`#[derive(X)]`** 是 compiler 的**程式碼產生器**,以 blessed spec 為鍵、讀型別結構產出 impl——**不是**空 `impl`
  的語法糖（見 [Derive & Default Behavior](derive.zh-TW.md)）。
- **`#[…]` decorator** 是**固定、compiler 擁有**的指令集,不是使用者可自訂的 macro——Zerg **沒有 macro**,所以沒有任何
  使用者語法糖能改寫你的程式。
