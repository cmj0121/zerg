# Zerg 標準函式庫（Standard Library）

[English](stdlib.md) | 繁體中文

隨附的**標準函式庫套件**——以 `import "<name>"` 取得（絕非 ambient；唯一例外是 `print` 關鍵字）。Zerg 是
**zero external dependency，like Go**：每個套件都是站在自帶 runtime 上的**純 Zerg**——邏輯在語言裡，只有不可再壓縮的
syscall／硬體 leaf 在 C runtime（見 [`src/runtime`](../src/runtime/README.md) 與 [zero-dependency 原則](ffi.zh-TW.md)）。
這裡沒有任何一個綁第三方庫。

編譯器直接提供、**免** import 的函式，見 [內建函式（Built-in Functions）](builtins.zh-TW.md)。

## 套件

| 套件                  | Import             | 提供                               |
| --------------------- | ------------------ | ---------------------------------- |
| [`io`](#io)           | `import "io"`      | 標準串流輸出，以及整檔讀／寫       |
| [`fs`](#fs)           | `import "fs"`      | 檔案系統結構——存在與否、刪除       |
| [`os`](#os)           | `import "os"`      | 環境變數、程序結束、目標平台／架構 |
| [`strings`](#strings) | `import "strings"` | 內建 `str` 上的文字工具            |
| [`time`](#time)       | `import "time"`    | 牆鐘與單調時鐘                     |
| [`math`](#math)       | `import "math"`    | 數值輔助與純 Zerg transcendentals  |
| [`rand`](#rand)       | `import "rand"`    | 確定性、非密碼學的產生器           |
| [`atomic`](#atomic)   | `import "atomic"`  | 安全的共享可變原語                 |
| [`testing`](#testing) | `import "testing"` | `#[test]` 函式用的斷言輔助         |

## `io`

讀寫外部世界。寫入回 `Result[nil]`（失敗是一個值）；整檔操作失敗 raise `IOError`，`guard { … }` 可降級成
`Result`。完整的 `Reader` / `Writer` 串流表面規範於 [Process 與 I/O](io.zh-TW.md)，但**尚未**實作——以下是已接線的
leaf。

| 函式                                      | 摘要                                       |
| ----------------------------------------- | ------------------------------------------ |
| `write(s: str) -> Result[nil]`            | 把 `s` 寫到 stdout，無結尾換行             |
| `println(s: str) -> Result[nil]`          | 把 `s` 寫到 stdout，含結尾換行             |
| `write_int(n: int) -> Result[nil]`        | 把 `n` 的十進位文字寫到 stdout             |
| `read_file(path: str) -> list[byte]`      | 讀整個檔案的位元組（raise `IOError`）      |
| `write_file(path: str, data: list[byte])` | 建立/截斷並寫入整個檔案（raise `IOError`） |

## `fs`

檔案系統**結構**——檔案的*內容*用 `io.read_file` / `io.write_file`。

| 函式                        | 摘要                                  |
| --------------------------- | ------------------------------------- |
| `exists(path: str) -> bool` | `path` 是否存在檔案或目錄             |
| `remove(path: str)`         | 刪除檔案（raise `IOError`；僅限檔案） |

## `os`

程序與平台資訊。`platform` / `arch` 在**編譯期**決定，因此指的是 binary 建置的目標。程式自己的引數由
`fn main(args: list[str])` 取得，不在這裡。

| 函式                    | 摘要                                           |
| ----------------------- | ---------------------------------------------- |
| `env(key: str) -> str?` | 環境變數的值，未設為 `nil`                     |
| `exit(code: int)`       | 以 `code` 結束程序（不返回）                   |
| `platform() -> str`     | 目標 OS——`"linux"`、`"darwin"`、`"windows"`、… |
| `arch() -> str`         | 目標 CPU——`"arm64"`、`"x86_64"`、…             |

## `strings`

內建 `str` 上的文字工具。每個函式先把 str 解碼成位元組、在位元組層級運算、再重組回 `str`——無外部綁定。位元組層級
搜尋是 **UTF-8 正確**的（UTF-8 自我同步，合法的 needle 只會在 code-point 邊界匹配）；`index_of` 回傳**位元組**
offset，與 Go 的 `strings.Index` 一致。大小寫折疊**僅限 ASCII**——非 ASCII 位元組原樣通過。空的 `split` 分隔字串、
或負的 `repeat` 次數，會 raise `ValueError`。

| 函式                                   | 摘要                                       |
| -------------------------------------- | ------------------------------------------ |
| `has_prefix(s: str, prefix: str)`      | `s` 是否以 `prefix` 開頭（`-> bool`）      |
| `has_suffix(s: str, suffix: str)`      | `s` 是否以 `suffix` 結尾（`-> bool`）      |
| `contains(s: str, sub: str) -> bool`   | `sub` 是否出現在 `s` 中                    |
| `index_of(s: str, sub: str) -> int`    | 首個 `sub` 的位元組 offset，找不到回 `-1`  |
| `split(s: str, sep: str) -> list[str]` | 各 `sep` 之間的片段（N 個 sep → N+1 片段） |
| `join(parts: list[str], sep: str)`     | 以 `sep` 串接 `parts`（`-> str`）          |
| `repeat(s: str, count: int) -> str`    | 把 `s` 串接 `count` 次                     |
| `trim(s: str) -> str`                  | 去除前後的 ASCII 空白                      |
| `to_upper(s: str) -> str`              | 把 ASCII 小寫字母折成大寫                  |
| `to_lower(s: str) -> str`              | 把 ASCII 大寫字母折成小寫                  |

## `time`

時鐘。`now` 是日期；`monotonic` 只有作為**差值**（經過時間）才有意義，且永不倒退。

| 函式                 | 摘要                              |
| -------------------- | --------------------------------- |
| `now() -> int`       | 牆鐘時間，Unix epoch 起算的整數秒 |
| `monotonic() -> int` | 單調時鐘讀數（奈秒；請取差值）    |

## `math`

primitive 上的數值輔助，加上**純 Zerg** transcendentals（數值演算法，絕非綁 libm）。domain 錯誤（如 `sqrt` 負數）
會 raise，`guard` 可降級。

| 函式                                  | 摘要                                       |
| ------------------------------------- | ------------------------------------------ |
| `abs(x: int) -> int`                  | 整數絕對值                                 |
| `fabs(x: float) -> float`             | 浮點絕對值                                 |
| `min(a: int, b: int) -> int`          | 兩整數的較小者                             |
| `max(a: int, b: int) -> int`          | 兩整數的較大者                             |
| `sqrt(x: float) -> float`             | 平方根（Newton's method）；負數 raise      |
| `pow(base: float, exp: int) -> float` | 整數指數（by squaring）                    |
| `pi() -> float`                       | π（以函式提供；grammar 無 value constant） |
| `e() -> float`                        | 尤拉數                                     |

## `rand`

快速、確定性、**非密碼學**的產生器（xorshift64\*）。狀態是呼叫者持有的一個 `uint`；每次抽取都透過 `mut &`
就地推進——無隱藏 global。**不適用**金鑰或 token。

| 函式                                 | 摘要                         |
| ------------------------------------ | ---------------------------- |
| `seed(n: uint) -> uint`              | 由種子建立產生器狀態         |
| `next(mut &g: uint) -> uint`         | 就地推進 `g`，回傳下一個值   |
| `below(mut &g: uint, n: int) -> int` | 推進 `g`，回傳 `[0, n)` 的值 |

```text
mut g := rand.seed(42)
x := rand.next(g)        # g 推進
d := rand.below(g, 6)    # g 推進；d 落在 [0, 6)
```

## `atomic`

跨 coroutine 安全共享可變狀態的方式（GRAMMAR group 10）：以 immutable 的 `:=` 綁定持有一個 `Atomic[int]` cell，
其內容透過 sequentially-consistent 運算變動。MVP：僅 `int`。

| 函式                                                           | 摘要                                |
| -------------------------------------------------------------- | ----------------------------------- |
| `atomic(v: int) -> Ref[int]`                                   | 建立持有 `v` 的共享 cell            |
| `load(a: Ref[int]) -> int`                                     | 讀取 cell                           |
| `store(a: Ref[int], v: int) -> int`                            | 寫入 `v` 並回傳                     |
| `swap(a: Ref[int], v: int) -> int`                             | 寫入 `v`，回傳先前值                |
| `fetch_add(a: Ref[int], n: int) -> int`                        | 加 `n`，回傳先前值                  |
| `compare_swap(a: Ref[int], expect: int, desired: int) -> bool` | CAS：等於 `expect` 才設為 `desired` |

## `testing`

`#[test]` 函式用的斷言輔助（[`zerg test`](package.zh-TW.md)）。滿足的斷言是 `nil`；違反的會 `raise`，讓外圍
`guard` 接住，或帶訊息 abort。

| 函式                                          | 摘要              |
| --------------------------------------------- | ----------------- |
| `assert(cond: bool) -> Result[nil]`           | `cond` 成立時通過 |
| `assert_eq[T: Eq](a: T, b: T) -> Result[nil]` | `a == b` 時通過   |
| `assert_ne[T: Eq](a: T, b: T) -> Result[nil]` | `a != b` 時通過   |
