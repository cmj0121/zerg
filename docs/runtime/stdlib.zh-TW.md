# Zerg 標準函式庫（Standard Library）

[English](stdlib.md) | 繁體中文

隨附的**標準函式庫套件**——以 `import "<name>"` 取得（絕非 ambient；唯一例外是 `print` 關鍵字）。Zerg 是
**zero external dependency，like Go**：每個套件都是站在自帶 runtime 上的**純 Zerg**——邏輯在語言裡，只有不可再壓縮的
syscall／硬體 leaf 在 C runtime（見 [`src/runtime`](../../src/runtime/README.md) 與 [zero-dependency 原則](ffi.zh-TW.md)）。
這裡沒有任何一個綁第三方庫。

編譯器直接提供、**免** import 的函式，見 [內建函式（Built-in Functions）](builtins.zh-TW.md)。

## 套件

| 套件                  | Import             | 提供                               |
| --------------------- | ------------------ | ---------------------------------- |
| [`io`](#io)           | `import "io"`      | 標準串流輸出，以及整檔讀／寫       |
| [`fs`](#fs)           | `import "fs"`      | 檔案系統結構——存在與否、刪除       |
| [`os`](#os)           | `import "os"`      | 環境變數、程序結束、目標平台／架構 |
| [`strings`](#strings) | `import "strings"` | 內建 `str` 上的文字工具            |
| [`ascii`](#ascii)     | `import "ascii"`   | tokeniser 用的單位元組 ASCII 分類  |
| [`strconv`](#strconv) | `import "strconv"` | 任意 base 的數字文字轉換           |
| [`time`](#time)       | `import "time"`    | 時鐘，以及以 channel 呈現的 timer  |
| [`math`](#math)       | `import "math"`    | 數值輔助與純 Zerg transcendentals  |
| [`rand`](#rand)       | `import "rand"`    | 確定性、非密碼學的產生器           |
| [`sha256`](#sha256)   | `import "sha256"`  | FIPS 180-4 摘要,用來命名與驗完整性 |
| [`cli`](#cli)         | `import "cli"`     | 宣告式的命令列，以及它算繪的 help  |
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
| `ewrite(s: str) -> Result[nil]`           | 把 `s` 寫到 stderr，無結尾換行             |
| `eprintln(s: str) -> Result[nil]`         | 把 `s` 寫到 stderr，含結尾換行             |
| `write_int(n: int) -> Result[nil]`        | 把 `n` 的十進位文字寫到 stdout             |
| `read_file(path: str) -> list[byte]`      | 讀整個檔案的位元組（raise `IOError`）      |
| `read_stdin() -> list[byte]`              | 讀取整個標準輸入（fd 0）至 EOF             |
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
| `count(s: str, sub: str) -> int`       | `sub` 的非重疊出現次數                     |
| `replace(s: str, old: str, new: str)`  | 把每個 `old` 換成 `new`                    |
| `trim_prefix(s: str, prefix: str)`     | 去掉一個前綴 `prefix`，否則原樣            |
| `trim_suffix(s: str, suffix: str)`     | 去掉一個後綴 `suffix`，否則原樣            |
| `fields(s: str) -> list[str]`          | 依空白區段切分，無空片段                   |

`count` 與 `replace` 對空的 needle raise `ValueError`，與 `split` 一致。

## `ascii`

單位元組 **ASCII** 分類——tokenise ASCII 原始碼的誠實工具（此處 byte ≥ 128 一律不算字母／數字／空白）。述詞集
對應 C 的 `<ctype.h>`；`fold_upper` / `fold_lower` 是 `strings` 大小寫折疊的單位元組版本；`digit_val` / `hex_val`
把數字位元組映成其值（否則 `-1`），供手寫數字掃描。每個函式都是純值運算——不配置記憶體。

| 函式                          | 摘要                                               |
| ----------------------------- | -------------------------------------------------- |
| `is_digit(b: byte) -> bool`   | `b` 是否為 `'0'..'9'`                              |
| `is_alpha(b: byte) -> bool`   | `b` 是否為 `'A'..'Z'` 或 `'a'..'z'`                |
| `is_alnum(b: byte) -> bool`   | `b` 是否為字母或十進位數字                         |
| `is_hex_digit(b: byte)`       | `b` 是否為 `'0'..'9'` / `'a'..'f'` / `'A'..'F'`    |
| `is_upper(b: byte) -> bool`   | `b` 是否為 `'A'..'Z'`                              |
| `is_lower(b: byte) -> bool`   | `b` 是否為 `'a'..'z'`                              |
| `is_space(b: byte) -> bool`   | `b` 是否為 ASCII 空白（tab..CR、space）——C isspace |
| `fold_upper(b: byte) -> byte` | 把 ASCII 小寫字母折成大寫（否則原樣）              |
| `fold_lower(b: byte) -> byte` | 把 ASCII 大寫字母折成小寫（否則原樣）              |
| `digit_val(b: byte) -> int`   | 十進位數字的值 `0..9`，否則 `-1`                   |
| `hex_val(b: byte) -> int`     | 十六進位數字（不分大小寫）的值 `0..15`，否則 `-1`  |

## `strconv`

任意 **base** 2..36 的數字文字轉換（數字為 `'0'..'9'` 接 `'a'..'z'`，輸入不分大小寫）——這是內建轉換未涵蓋的
層：`int(s)` / `uint(s)` / `float(s)` 只解析十進位、`str(n)` 只輸出十進位。用來手動讀 `0x…` / `0b…` 字面量或
輸出 hex dump。base 非法、位數超出 base、或字串格式錯誤 raise `ValueError`；目標型別的 overflow 本階段**不**另行
診斷（請解析有界文字）。

| 函式                                    | 摘要                                    |
| --------------------------------------- | --------------------------------------- |
| `parse_int(s: str, base: int) -> int`   | `base` 的有號整數，可帶 `+`/`-`         |
| `parse_uint(s: str, base: int) -> uint` | `base` 的無號整數（可填滿最高位）       |
| `to_string(n: int, base: int) -> str`   | 以 `base` 輸出 `n`，小寫，INT_MIN-safe  |
| `parse_bool(s: str) -> bool`            | `"true"` / `"false"`，否則 `ValueError` |

## `sha256`

FIPS 180-4 規範的 SHA-256,以純 Zerg 寫成、只用 `uint` 與位元運算子——沒有 libcrypto,也沒有 runtime leaf。
`zerg build` 用它為每一個被快取的 object 命名。

| 函式                           | 摘要                         |
| ------------------------------ | ---------------------------- |
| `sum(data: list[byte])`        | 32 bytes 的摘要              |
| `hex(data: list[byte]) -> str` | 同一個摘要,64 個小寫十六進位 |

它**不是 constant-time**,也不宣稱是。用它來「以內容為某個東西命名」、察覺檔案變了、或當 cache key;不要用它
檢查密碼。`make sha256` 拿標準的 known-answer vectors 與系統工具(隨機輸入)來釘它——oracle 對它無效,因為兩個
編譯器跑的是同一份原始碼。

## `time`

時鐘與 timer。`now` 是日期；`monotonic` 只有作為**差值**（經過時間）才有意義，且永不倒退。**timer 就是一條
channel**——`after` 與 `ticker` 回傳 receive-only channel，所以對它們的一條 `select` arm 就是 timeout 或一次 tick，
不需要任何新語法（見 [Coroutines](../code/coroutine.zh-TW.md)）。duration 的單位是**奈秒**，與 `monotonic` 的讀數
同單位；`<= 0` 的 duration 會立刻觸發。

| 函式                       | 摘要                                           |
| -------------------------- | ---------------------------------------------- |
| `now() -> int`             | 牆鐘時間，Unix epoch 起算的整數秒              |
| `monotonic() -> int`       | 單調時鐘讀數（奈秒；請取差值）                 |
| `after(d) -> <-chan[int]`  | `d` 奈秒過後送出一個值，僅一次                 |
| `ticker(d) -> <-chan[int]` | 每 `d` 奈秒送出一個值；channel 只裝**一** tick |

送出的值是 **timer 觸發當下的 monotonic 讀數**，不是佔位符：一次 tick 可能比它觸發的時刻晚任意久才送達，而這個讀數
正是「在乎的接收者」用來判斷自己遲了多少的依據。接收者跟不上的 `ticker` 會**停在 send 上**、而不是把 tick 排隊起來，
所以慢的 consumer 是讓 ticker 變慢，而不是累積一個永遠追不完的 backlog。

**成本，以及唯一缺的那件事。** 每一個活著的 timer 都是**一條帶著自己 256KB stack 的 coroutine**，所以放在迴圈裡
的 `after` 會每一輪配置一個。而且**沒有 stop**：一次 sleep 無法取消，所以 `ticker` 的 coroutine 會活到程式結束
——請把它放在程式頂端，不要放在迴圈裡。

## `math`

primitive 上的數值輔助，加上**純 Zerg** transcendentals（數值演算法，絕非綁 libm）。domain 錯誤（如 `sqrt` 負數）
會 raise `ValueError`，`guard` 可降級。

| 函式                                  | 摘要                                       |
| ------------------------------------- | ------------------------------------------ |
| `abs(x: int) -> int`                  | 整數絕對值                                 |
| `fabs(x: float) -> float`             | 浮點絕對值                                 |
| `min(a: int, b: int) -> int`          | 兩整數的較小者                             |
| `max(a: int, b: int) -> int`          | 兩整數的較大者                             |
| `sqrt(x: float) -> float`             | 平方根（Newton's method）；負數 raise      |
| `pow(base: float, exp: int) -> float` | 整數指數（by squaring）                    |
| `trunc(x: float) -> int`              | 去掉小數部分，朝零                         |
| `floor(x: float) -> int`              | 不大於 `x` 的最大整數                      |
| `ceil(x: float) -> int`               | 不小於 `x` 的最小整數                      |
| `round(x: float) -> int`              | 最近整數，逢半朝遠離零                     |
| `pi() -> float`                       | π（以函式提供；grammar 無 value constant） |
| `e() -> float`                        | 尤拉數                                     |

**取整的那四個回答一個 `int`，而那正是它們存在的理由。** `float` 上的 `int(x)` 被拒絕——丟掉小數是一個決定，而
且有四個都說得通的答案（見[型別](../core/types.zh-TW.md)）——所以這四個就是做出那個決定的動詞。一個回傳
`float` 的動詞，會讓呼叫端手上仍握著那個它本來就是為了完成而呼叫動詞的轉換。一個 `int` 裝不下的量會 raise
`OverflowError`，和其他每一個會失敗的轉換一樣可以用 `guard` 降級；目標更窄時就是動詞再加上轉換，
`byte(math.trunc(x))`。

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

## `cli`

命令列是**宣告**出來的，不是手工 parse：一個 `Command` 宣告它的引數、子命令，以及各自要跑的函式，而那份宣告就是
命令列唯一的描述——parser 讀它、help 算繪器讀它、驗證器也讀它。`zerg` 自己的命令列就是用它宣告的
（見 [`src/compiler/zergc.zg`](../../src/compiler/zergc.zg)）。

| 函式                                                    | 摘要                                |
| ------------------------------------------------------- | ----------------------------------- |
| `command(name: str, about: str = "") -> Command`        | 一個命令，root 或子命令             |
| `argument(short, long, help, fallback) -> Argument`     | 帶值的選項                          |
| `flag(short, long, help) -> Argument`                   | 只有「在」與「不在」的選項          |
| `positional(name, help) -> Argument`                    | 位置引數                            |
| `Command.opt` / `.required` / `.flag` / `.pos`          | 就地宣告引數，不必先建一個          |
| `Command.add(a)` / `.sub(c)` / `.run(f)`                | 掛上引數、子命令、它的函式          |
| `Command.version` / `.usage` / `.epilog` / `.no_help`   | `--help` 與 `--version` 說什麼      |
| `Command.exec(args: list[str]) -> int`                  | parse、dispatch，並回答程序的狀態碼 |
| `Ctx.has` / `.get` / `.all` / `.int_of` / `.args`       | 讀出這次 parse 的結果               |
| `Argument.required` / `.repeated` / `.env` / `.section` | 收窄一個已宣告的引數                |

## `atomic`

跨 coroutine 安全共享可變狀態的方式（GRAMMAR group 10）：以 immutable 的 `:=` 綁定持有一個 `Atomic[int]` cell，
其內容透過 sequentially-consistent 運算變動。MVP：僅 `int`。

> **[not yet]** 這個模組會出貨，但**無法 import**。`Atomic[T]` 是 generic struct，而 generic struct 是本編譯器
> 拒絕的形式之一，所以 `import "atomic"` 本身就會回報 `NotImplemented: a generic struct`Atomic[…]``。下表的
簽章另外還提到 `Ref[T]`，那個型別也不存在。它是十二個模組中唯一無法乾淨 import 的一個。

| 函式                                                           | 摘要                                |
| -------------------------------------------------------------- | ----------------------------------- |
| `atomic(v: int) -> Ref[int]`                                   | 建立持有 `v` 的共享 cell            |
| `load(a: Ref[int]) -> int`                                     | 讀取 cell                           |
| `store(a: Ref[int], v: int) -> int`                            | 寫入 `v` 並回傳                     |
| `swap(a: Ref[int], v: int) -> int`                             | 寫入 `v`，回傳先前值                |
| `fetch_add(a: Ref[int], n: int) -> int`                        | 加 `n`，回傳先前值                  |
| `compare_swap(a: Ref[int], expect: int, desired: int) -> bool` | CAS：等於 `expect` 才設為 `desired` |

## `testing`

`#[test]` 函式用的斷言輔助。**[not yet]**——目前沒有任何編譯器會產生測試 binary。滿足的斷言是 `nil`；違反的會 `raise`，讓外圍
`guard` 接住，或帶訊息 abort。它 raise 的是**沒有種類**的 `Err`——標準函式庫中唯一如此的模組，而且是刻意的：一次失敗的斷言是
一個不成立的程式主張，不是函式無法接受的值，內建 taxonomy 沒有對應的種類。

| 函式                                          | 摘要              |
| --------------------------------------------- | ----------------- |
| `assert(cond: bool) -> Result[nil]`           | `cond` 成立時通過 |
| `assert_eq[T: Eq](a: T, b: T) -> Result[nil]` | `a == b` 時通過   |
| `assert_ne[T: Eq](a: T, b: T) -> Result[nil]` | `a != b` 時通過   |
