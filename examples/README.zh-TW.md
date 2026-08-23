# Zerg by example

[English](README.md) | 繁體中文

三十三支程式，附閱讀順序。每一支都由 **`make examples` 建起來並執行**，所以這裡沒有「以前能動」的
片段——那兩支**應該被拒收**的，也被釘在它們必須被拒收的那句話上。

```sh
make                            # ./bin/zerg
./bin/zerg build examples/00_hello.zg -o /tmp/hello && /tmp/hello
```

如果你還沒寫過任何 Zerg，先讀 [Getting started](../docs/getting-started.zh-TW.md)：它把 `hello.zg`
帶到一支多檔案的程式，然後把你交回這裡。

## 從這裡開始——語言，依序

名字裡的數字**就是**順序。每一支都是一支會印東西的完整程式，而且各自比前一支多一個概念。

| 範例                                   | 它展示什麼                                  |
| -------------------------------------- | ------------------------------------------- |
| [`00_hello.zg`](00_hello.zg)           | `fn main`，以及 `print` 是關鍵字而不是函式  |
| [`01_bindings.zg`](01_bindings.zg)     | `:=` 建立繫結；不寫 `mut` 的繫結是不可變的  |
| [`02_arithmetic.zg`](02_arithmetic.zg) | 整數運算子，以及誰綁得比誰緊                |
| [`03_floats.zg`](03_floats.zg)         | `float`，以及為什麼 `1 / 2` 不是 `0.5`      |
| [`04_booleans.zg`](04_booleans.zg)     | 比較，以及寫成單字的 `and` / `or` / `not`   |
| [`05_bitwise.zg`](05_bitwise.zg)       | 位元運算子——整數有，浮點沒有                |
| [`06_functions.zg`](06_functions.zg)   | 參數、回傳型別，以及呼叫                    |
| [`07_match.zg`](07_match.zg)           | 對值與範圍做 `match`，以及它為什麼必須窮盡  |
| [`08_loops.zg`](08_loops.zg)           | `for` 的三種形狀——條件、範圍、走訪一個 list |
| [`09_recursion.zg`](09_recursion.zg)   | 函式呼叫自己，以及 stack 在哪裡結束         |
| [`10_fizzbuzz.zg`](10_fizzbuzz.zg)     | 收尾之作：迴圈、條件與 `print` 合在一起     |

## 讓它是一個語言、而不是一台計算機的那些部分

| 範例                                     | 它展示什麼                                               |
| ---------------------------------------- | -------------------------------------------------------- |
| [`11_coroutines.zg`](11_coroutines.zg)   | `spawn`，以及用 channel 看著你 spawn 出去的東西          |
| [`12_actor.zg`](12_actor.zg)             | 可變狀態由單一 coroutine 擁有，只能用訊息抵達            |
| [`13_cancel.zg`](13_cancel.zg)           | timeout 與 cancellation 都是 channel，而 `select` 負責等 |
| [`14_optional.zg`](14_optional.zg)       | `T?`，以及「值在不在」的四種問法                         |
| [`15_conversions.zg`](15_conversions.zg) | `T(x)`——沒有隱式轉換，所以每一次轉換都被寫出來           |
| [`16_text.zg`](16_text.zg)               | `str` 是 UTF-8，以及那讓「一個字元」是什麼               |
| [`17_arithmetic.zg`](17_arithmetic.zg)   | 整數運算是**受檢的**：溢位會 raise，不會回捲             |
| [`18_scoped.zg`](18_scoped.zg)           | 什麼會釋放一個值，以及誰決定何時                         |
| [`19_environment.zg`](19_environment.zg) | 環境變數：到處可讀，只在啟動時可寫                       |
| [`20_typedefs.zg`](20_typedefs.zg)       | `type X = Y`:一個誰都遇不到的身分,加上 Y 的運算子        |

## 模組這一層

第二支程式最先撞到的就是這裡，而它也是規格裡單行說明最少的一段。以下每一個都是一個**目錄**：一個
entry 檔，加上它所 import 的模組。

| 範例                                | 它展示什麼                                                       |
| ----------------------------------- | ---------------------------------------------------------------- |
| [`modules/`](modules)               | 一支兩個模組的程式——一個 entry 檔與並排的目錄模組                |
| [`1g/visible/`](1g/visible)         | 一個 `pub` 表面**確實**能跨過模組邊界抵達什麼                    |
| [`1g/pubconst/`](1g/pubconst)       | `pub COUNT := 3` 是真正的成員；模組繫結不需要 `const`            |
| [`1g/modconst/`](1g/modconst)       | 模組常數是同一個物件，不管在哪裡讀都一樣                         |
| [`1g/shapedconst/`](1g/shapedconst) | 有拼出型別的模組常數——tuple、optional                            |
| [`1g/modtype/`](1g/modtype)         | 透過 import 抵達的型別，同時也是它的建構子                       |
| [`1g/init/`](1g/init)               | 被 import 的模組，它的 `init()` 在 `main` 第一行之前跑一次       |
| [`1g/initorder/`](1g/initorder)     | 兩個互不相干的常數 —— 打破平手的是模組**名稱**，不是 import 順序 |
| [`1g/stdlibwins/`](1g/stdlibwins)   | 裸名字永遠是標準函式庫,即使旁邊就有一個同名的專案模組            |
| [`1g/reexport/`](1g/reexport)       | `import pub`——一個模組把另一個模組的名字放到自己的表面上         |
| [`1g/spec/`](1g/spec)               | 在一個模組宣告、在另一個模組實作的 `spec`                        |
| [`1g/strings/`](1g/strings)         | 標準函式庫的 `strings`，從頭到尾走一遍                           |
| [`1g/outputorder/`](1g/outputorder) | `print` 與 `io.println` 依寫下的順序抵達 stdout                  |
| [`1g/testfile/`](1g/testfile)       | 一次正常建置會編什麼，又把什麼留在地上                           |

### 那兩支必須被拒收的

一個範例是對語言的一個主張，而**否定的**那種——_這支程式不合法_——是「建起來並執行」的迴圈檢查不
了的：它只能回報建置失敗，而打錯字也是那樣。所以這兩支被釘在它們必須被拒收的那句話上，就像
`make reject` 對待它自己的案例：

| 範例                            | 被什麼拒收                             |
| ------------------------------- | -------------------------------------- |
| [`1g/private/`](1g/private)     | `… is not a public member of module …` |
| [`1g/privconst/`](1g/privconst) | 同一條規則，用在模組**常數**上         |

如果你編了這兩支之一而得到一個錯誤，那個錯誤**就是**這個範例。

## 接下來去哪

- **[Getting started](../docs/getting-started.zh-TW.md)** —— `hello.zg` 到一支多檔案的程式
- **[語言參考](../docs/language.zh-TW.md)** —— 每一章，以及每一章決定了什麼
- **[Conformance](../docs/conformance.zh-TW.md)** —— 怎麼讀規格的狀態標記。等你想知道「為什麼這個
  還沒建」再讀，不用一開始就讀。
