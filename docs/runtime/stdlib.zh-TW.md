# Zerg 標準函式庫（Standard Library）

[English](stdlib.md) | 繁體中文

隨附的**標準函式庫套件**——以 `import "<name>"` 取得（絕非 ambient；唯一例外是 `print` 關鍵字）。Zerg 是
**zero external dependency，like Go**：每個套件都是站在自帶 runtime 上的**純 Zerg**——邏輯在語言裡，只有不可再壓縮的
syscall／硬體 leaf 在 C runtime（見 [`src/runtime`](../../src/runtime/README.md) 與 [zero-dependency 原則](ffi.zh-TW.md)）。
這裡沒有任何一個綁第三方庫。

編譯器直接提供、**免** import 的函式，見 [內建函式（Built-in Functions）](builtins.zh-TW.md)。

## 模組註解裡可執行的範例

`pub` 函式的註解可以帶範例：在普通的 `#` 註解裡寫成一組 fenced block——運算式放 ` ```zerg `，它印出什麼放
` ```output `。`make stdlib-test` 會**編譯並執行**每一組，再把實際輸出與寫下的輸出 diff，所以範例是一個被檢查的
主張，而不是一段寫下來的話。（`##` doc comment 與 `zerg doc` 仍是 **[not yet]**；這組 fence 就是它們將採用的形式。）

````text
# ```zerg
# strings.index_of("日本語", "本")
# ```
# ```output
# 3
# ```
````

> **` ```output ` 的行尾不能是空白。** 本倉庫自己的 pre-commit hook 會裁掉行尾空白，而且就在**新增該範例的那一次
> commit** 裡裁掉——所以一個最後一個字元本來就該是空白的 output block，會被無聲改寫成範例根本不會產生的樣子，
> 範例從真變成假，且沒有留下任何痕跡。`trim_left` 就是發現這件事的案例。
>
> 解法是讓**運算式**本身以一個看得見的終止符收尾，並在測試套件裡用同樣的形式斷言，讓兩邊一致：
>
> ````text
> # ```zerg
> # strings.trim_left("  hi  ") + "|"
> # ```
> # ```output
> # hi  |
> # ```
> ````
>
> 不要試著把該檔案排除在 hook 之外：檔案裡其他每一行都應該被裁，而一個需要行尾空白的範例，讀者的眼睛同樣看不見它。

## 套件

| 套件                  | Import             | 提供                                   |
| --------------------- | ------------------ | -------------------------------------- |
| [`io`](#io)           | `import "io"`      | 標準串流輸出，以及整檔讀／寫           |
| [`fs`](#fs)           | `import "fs"`      | 檔案系統結構——存在與否、刪除           |
| [`os`](#os)           | `import "os"`      | 環境變數讀寫、程序結束、目標平台／架構 |
| [`strings`](#strings) | `import "strings"` | 內建 `str` 上的文字工具                |
| [`ascii`](#ascii)     | `import "ascii"`   | tokeniser 用的單位元組 ASCII 分類      |
| [`strconv`](#strconv) | `import "strconv"` | 任意 base 的數字文字轉換               |
| [`json`](#json)       | `import "json"`    | JSON 的讀與寫,只有一份跳脫實作         |
| [`log`](#log)         | `import "log"`     | 結構化 logging,寫成鏈式 builder        |
| [`time`](#time)       | `import "time"`    | 時鐘，以及以 channel 呈現的 timer      |
| [`math`](#math)       | `import "math"`    | 數值輔助與純 Zerg transcendentals      |
| [`rand`](#rand)       | `import "rand"`    | 確定性、非密碼學的產生器               |
| [`sha256`](#sha256)   | `import "sha256"`  | FIPS 180-4 摘要,用來命名與驗完整性     |
| [`cli`](#cli)         | `import "cli"`     | 宣告式的命令列，以及它算繪的 help      |
| [`atomic`](#atomic)   | `import "atomic"`  | 安全的共享可變原語                     |
| [`testing`](#testing) | `import "testing"` | `#[test]` 要而語言不給的東西           |

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
`fn main(args: list[str])` 取得，不在這裡。`run` 是唯一會啟動**另一個**程序的葉子——argv 直接交給 OS，沒有 shell
也沒有 pipe，回來的是 exit status（死於信號時為 128+信號，無法執行時為 127）。有 shell 也有 pipe 的命令字面量見
[Process 與 I/O](io.zh-TW.md)，仍是 **[not yet]**。

| 函式                            | 摘要                                           |
| ------------------------------- | ---------------------------------------------- |
| `env(key: str) -> str?`         | 環境變數的值，未設為 `nil`                     |
| `set_env(key: str, value: str)` | 把 `key` 設為 `value`，取代原有的值            |
| `del_env(key: str) -> bool`     | 移除 `key`；回答它原本**在不在**               |
| `exit(code: int)`               | 以 `code` 結束程序（不返回）                   |
| `run(argv: list[str])`          | 跑 `argv[0]`（找 PATH）並等待，`-> int` 狀態碼 |
| `platform() -> str`             | 目標 OS——`"linux"`、`"darwin"`、`"windows"`、… |
| `arch() -> str`                 | 目標 CPU——`"arm64"`、`"x86_64"`、…             |
| `isatty(fd: int) -> bool`       | 這個描述子是不是終端機（0 入、1 出、2 錯）     |

這三個環境函式就是
[`xxx` / `set_xxx` / `del_xxx`](../code/functions.zh-TW.md#命名一個屬性與它的兩種寫入) 三件套：`env` 讀、
`set_env` 寫、`del_env` 移除。**`del_env` 回答那個 key 原本在不在**，而這正是呼叫端無法自己查出來的一件事——先
`env` 再 `del_env` 是兩次詢問，中間有一個窗口，而 C 的 `unsetenv` 不論如何都回報成功。**`set_env` 在主機不接受的
名字上 raise `ValueError`**（空的、或含有 `=`）；`del_env` 則是 **total** 的，因為一個不可能存在的名字本來就沒被
設過，`false` 是真的答案而不是缺席的答案。

> **在啟動時設好環境，在任何 coroutine 被 spawn 之前。** 這不是慣例——這是這兩個函式**唯一**安全的用法，因為它們
> 改動的是 **C runtime 的狀態，而不是這個語言的**。POSIX 的 `environ` 上沒有鎖：`setenv` 可能重新配置整個陣列並
> 釋放舊的，而 `getenv` 交回的是指進去的指標，所以一次寫入與另一個 coroutine 的 `os.env` 相撞，就是 libc 內部的
> use-after-free。Zerg 會跑真正的 OS worker 執行緒（[Coroutine](../code/coroutine.zh-TW.md)），兩個 coroutine
> 常常就是兩條執行緒，因此「在我機器上會動」什麼也證明不了。
>
> 這讓它與 [`log`](#log) 記載的共享狀態危害**在類別上就不同**。logger 的那個 cell 是本專案自己的狀態，總有一天
> 一個 atomic 可以關掉那個競爭；`environ` 不是我們的，這裡做再多也無法讓它安全——沒有什麼修正可以等，只有一個
> 該呼叫它的時機。
>
> 編譯器**不會**強制它，而且也無法誠實地強制：workers 在 `main` 的第一行之前就存在了，所以一條以「到目前為止有沒有
> spawn 過」為準的規則，會在一個已經有十六條執行緒的程序裡回報安全，也會在每一個 `#[test]` 裡拒絕這個寫入（一個
> test 就是一個 coroutine）。

**`isatty` 只關於裝置，別無其他。** 用它來挑**算繪方式**——在終端機上加色、進 pipe 就不加——而永遠不要用它挑
**格式**：輸出的形狀會因為被重導而改變，就沒辦法用同一種方式讀第二次，而一台機器上是 JSON、另一台是欄位的
log 檔比兩者都糟。[`log`](#log) 走的正是這條線——顏色問它，而只有 `ZERG_LOG` 決定格式。沒有開啟的描述子不是
終端機，所以它回 `false` 而不是 raise；它是 total 的,這在「abort 等於為了跳脫碼而死」的路徑上很重要。

## `strings`

內建 `str` 上的文字工具。每個函式先把 str 解碼成位元組、在位元組層級運算、再重組回 `str`——無外部綁定。位元組層級
搜尋是 **UTF-8 正確**的（UTF-8 自我同步，合法的 needle 只會在 code-point 邊界匹配）；`index_of` 回傳**位元組**
offset，與 Go 的 `strings.Index` 一致。大小寫折疊**僅限 ASCII**——非 ASCII 位元組原樣通過。空的 `split` 分隔字串、
或負的 `repeat` 次數，會 raise `ValueError`。

| 函式                                   | 摘要                                                      |
| -------------------------------------- | --------------------------------------------------------- |
| `has_prefix(s: str, prefix: str)`      | `s` 是否以 `prefix` 開頭（`-> bool`）                     |
| `has_suffix(s: str, suffix: str)`      | `s` 是否以 `suffix` 結尾（`-> bool`）                     |
| `contains(s: str, sub: str) -> bool`   | `sub` 是否出現在 `s` 中                                   |
| `index_of(s: str, sub: str) -> int`    | 首個 `sub` 的位元組 offset，找不到回 `-1`                 |
| `split(s: str, sep: str) -> list[str]` | 各 `sep` 之間的片段（N 個 sep → N+1 片段）                |
| `join(parts: list[str], sep: str)`     | 以 `sep` 串接 `parts`（`-> str`）                         |
| `repeat(s: str, count: int) -> str`    | 把 `s` 串接 `count` 次                                    |
| `trim(s: str) -> str`                  | 去除前後的 ASCII 空白                                     |
| `to_upper(s: str) -> str`              | 把 ASCII 小寫字母折成大寫                                 |
| `to_lower(s: str) -> str`              | 把 ASCII 大寫字母折成小寫                                 |
| `count(s: str, sub: str) -> int`       | `sub` 的非重疊出現次數                                    |
| `replace(s: str, old: str, new: str)`  | 把每個 `old` 換成 `new`                                   |
| `trim_prefix(s: str, prefix: str)`     | 去掉一個前綴 `prefix`，否則原樣                           |
| `trim_suffix(s: str, suffix: str)`     | 去掉一個後綴 `suffix`，否則原樣                           |
| `fields(s: str) -> list[str]`          | 依空白區段切分，無空片段                                  |
| `last_index_of(s: str, sub: str)`      | **最後**一個 `sub` 的位元組 offset，否則 `-1`（`-> int`） |
| `trim_left(s: str) -> str`             | 只去除前導的 ASCII 空白                                   |
| `trim_right(s: str) -> str`            | 只去除尾端的 ASCII 空白                                   |
| `equal_fold(a: str, b: str) -> bool`   | 忽略 ASCII 大小寫的相等，不另建字串                       |
| `pad_start(s, width: int, fill: str)`  | 左側補到至少 `width` 個位元組（`-> str`）                 |
| `pad_end(s, width: int, fill: str)`    | 右側補到至少 `width` 個位元組（`-> str`）                 |

`count` 與 `replace` 對空的 needle raise `ValueError`，與 `split` 一致。`pad_start` / `pad_end` 對不是恰好一個
位元組的 `fill` raise `ValueError`——多位元組的填充字元無法落在位元組寬度上而不把一個 code point 切成兩半——而
`s` 已達該寬度時原樣回傳。`fill` 在**檢查寬度之前**先被驗證，所以即使這次呼叫根本不會補任何東西，壞的 `fill`
一樣被拒。

空的 needle 是**找得到**而不是找不到，位置在各函式搜尋的那一端：`index_of` 答 `0`、`contains` 答 `true`、
`last_index_of` 答字串的位元組長度——最後一個空 needle 就在最末一個位元組之後。拒絕它的是 `split`、`count`
與 `replace` 三個，理由是零寬度的匹配永遠不會讓它們前進。

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

## `json`

JSON 的讀與寫。它**刻意只有一份實作**:language server 與 logger 都會寫 JSON,而兩份跳脫實作會漂移——會漂移的
那一份,正好是沒人在讀 transcript 的那一份。在它有第二個呼叫者之前,它住在 `src/compiler/lsp/json.zg`。

一個值是 `Val`,而 object 是 **`list[Field]`** 而不是 map。list 會保留欄位被放進去的順序,所以輸出的位元組只是
值的函數——這正是讓 transcript 可以 diff、讓 log 行可以 grep 的性質。`Val` 的 variant 不是 public(enum 的
variant 無法在它的 module 之外建構),所以進去的路是 constructor、出來的路是 accessor。沒有 `fields()`:
`list[Field]` 不是 variant,所以呼叫端自己寫 `mut fs: list[json.Field] = []`。

| 函式                                                  | 摘要                                 |
| ----------------------------------------------------- | ------------------------------------ |
| `encode(v: Val) -> str`                               | 值寫成 JSON 文字——單行,欄位照原順序  |
| `decode(s: str) -> Val`                               | JSON 文字讀成值;格式錯誤會 **raise** |
| `get(v: Val, key: str) -> Val`                        | `key` 上的值,不存在則 `Null`         |
| `walk(v, a, b) -> Val` / `walk3(v, a, b, c)`          | 一次走兩層或三層 key                 |
| `has(v: Val, key: str) -> bool`                       | 存在,而且不是 `null`                 |
| `as_str` / `as_int` / `as_list`                       | 裡面的東西,否則 `""` / `0` / `[]`    |
| `is_null(v: Val) -> bool`                             | 這個值是 JSON `null`                 |
| `null()` / `of_str(s)` / `of_list(xs)` / `of_obj(fs)` | 建一個 `Val`                         |
| `put(fs, k, v)` / `put_str` / `put_int` / `put_bool`  | 往 `list[Field]` 追加一個欄位        |

**accessor 都是 TOTAL 的。** 跟數字要字串會得到 `""`,跟非 object 要 key 會得到 `Null`——這對「輸入來自另一個
程式」是刻意的:形狀不對的欄位應該得到預設值與一個回覆,而不是一個把整個 session 帶走的 abort。真正致命的缺欄位,
呼叫端自己問 `has`。有一個後果要知道:key 存在但值是 `null` 會被讀成**不存在**,因為 `has` 是寫在 `get` 上的。

**數字是整數。** `Val` 沒有 float variant,所以 `decode("1.5")` 是 `Int(1)`——小數與指數會被吃掉並**丟棄**,
不是拒絕。這是 language server 需要的形狀,至今沒變;寫在這裡是因為沒被告知的讀者,只會從一個錯的答案得知。

**`encode` 只跳脫 JSON 保留的東西。** 兩個分隔符、五個短跳脫,以及 `0x20` 以下的控制位元組寫成 `\u00XX`。
`0x20` 以上一律原樣通過,這正是 UTF-8 得以原封不動的原因:多位元組的碼點本來就是合法的 JSON 文字。所以先
`decode` 再 `encode` 不是逐位元組相同——`"\u00e9"` 會變回那個字元——而先 `encode` 再 `decode` 是穩定的。

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

## `log`

結構化 logging，寫成一條**鏈式 builder**：

```zerg
log.info().str("file", path).int("line", n).msg("compiling")
```

這個形狀不是流行，而是**在這個語言裡**唯一行得通的那一個。沒有 varargs，所以一個欄位就是一次呼叫；沒有 `any`，
所以 `str` 收 `str`、`int` 收 `int`；沒有 generic struct，所以 builder 是一個具體型別。任何其他形狀都至少要等其中兩件。

它同時也是**延遲求值**的答案。等級關掉時 `log.debug()` 回傳一個**死的** entry：每個欄位方法立刻返回、什麼都不格式化、
什麼都不配置，`msg` 也不寫。呼叫端交出的是有型別的值，而不是自己先組好的字串——所以被關掉的那一行本來要做的工，
是「從未發生」而不是「做完丟掉」。

還是要付的是**引數的求值**：`.str("dump", expensive())` 裡的 `expensive()` 兩種情況都會跑，因為 Zerg 在呼叫前先求值。
這正是 `enabled` 必須公開的原因：

```zerg
if log.enabled(log.Level.DEBUG) {
 log.debug().str("dump", expensive()).msg("state")
}
```

### 兩個介面

有一個**全域 logger**，用一個函式設定、不需要任何管線；也有一個**建構子**讓你自己持有並傳遞。它們不是兩份實作：
全域的那個**就是**一個 instance，放在這個模組自己的 cell 裡——所以每個欄位方法、每次等級判斷、每個 writer 都只存在一份。

| 函式                                   | 摘要                                          |
| -------------------------------------- | --------------------------------------------- |
| `new() -> Logger`                      | 依環境設定、寫到標準錯誤的 logger             |
| `install(lg: Logger)`                  | 換掉全域 logger——唯一的一次變更               |
| `parse_level(s: str) -> Level?`        | 依名字（`debug`）讀一個等級，不認得就是 `nil` |
| `enabled(lvl: Level) -> bool`          | 全域在 `lvl` 會不會寫                         |
| `at_level(lvl)`、`trace()` … `fatal()` | 在全域 logger 上開始一行——六個等級都有        |
| `to_stderr() -> Sink`                  | 預設目的地——每行一次 write 到 fd 2            |
| `to_chan(ch: chan[str]) -> Sink`       | 每一行寫完後當成值送進 channel                |

`Logger` 有 `level(l)`、`format(f)`、`colour(on)`、`to(sk)`、`with_str(k, v)`、`with_int(k, v)` 與
`enabled(l)`，每一個都回傳**複本**——交給元件的 logger 沒辦法反過來改呼叫者的——再加上 `at_level(l)` 與等級方法。
`Entry` 有 `str`、`int`、`bool`、`dur`、`err`，以及終結用的 `msg`。

**沒有 `Logger.debug()`，原因是一條語言規則。** `display` 與 `debug` 是每個值都有的兩種算繪
（見 [Formatting](format.zh-TW.md)），所以叫這兩個名字的方法必須回傳「這個值顯示成的 `str`」——`E361` 會拒絕一個叫
`debug` 的等級方法。這條規則只管**方法**，所以上面那個自由函式 `log.debug()` 用的就是等級本來的名字、而且被接受；
在 instance 上第六個等級寫成 `lg.at_level(log.Level.DEBUG)`。它叫 `at_level` 而不是 `at`，是因為自由的 `pub at`
會與編譯器自己的 lexer 撞上 `E705`——那裡有一個 module 私有的 `at`，而 `pub` 名字沒有 package 可以讓它唯一。
`parse_level` 不叫 `parse` 是同一條規則的另一面：這裡的 `pub parse` 會跟任何 import `log` 的程式裡那個 module
私有的 `parse` 相撞。

**只有一個會改狀態的函式，而且它收下一整個 logger。** `set_level` / `set_format` / `set_colour` / `set_sink`
這一家是被**刪掉**而不是改名的：模組本來就有四個純 builder，所以那些 setter 只是把同一件事再說一次，順便把共享狀態
改了四次——而一次就夠。

```zerg
log.install(log.new().level(log.Level.DEBUG).format(log.Format.JSON))
```

它叫 `install` 而不是 `level`，因為 `level` 已經表示*衍生一個在這個等級的 logger*，而同一個詞不能在 instance 上是純的、
在模組上卻有副作用。`enabled` 之所以兩邊都有，正是因為它兩邊的意思一樣。

**沒有 `current()`。** 從已安裝的 logger 衍生會讀起來像「執行中重新設定」，而這個 cell 還不適合那樣用
（見[設定是啟動時的動作](#設定是啟動時的動作)）。要還原預設就是 `log.install(log.new())`——cell 在宣告處就是用同一個
公開建構子初始化的，所以不需要把它讀回來，測試套件也正是靠這一點把自己隔離開。

**`log.new()` 是模組外唯一能造出 `Logger` 的方法。** 每個欄位都帶預設值（module 私有欄位必須帶，`E482`），
所以 `Logger()` 不管模組願不願意都存在——而它的預設值指名的是 module 私有的 const，所以外面寫 `log.Logger()` 得到的是
`E301`，而不是一個會默默忽略環境變數的第二個建構子。

### 設定是啟動時的動作

全域 logger 住在標準函式庫唯一一個 module 層級的 `unsafe { … }` group 裡，完整的 pattern 就寫在 `log.zg` 那個 group
上方——這裡只是摘要。語言本身把關的是四條規則裡的第一條，而且只有那一條：group 之外的頂層 `mut` 是 `E358`，
group 之內的 `pub` 是 `E484`——那是同一條規則的兩個代碼。所以「用函式設定」不是建議做法，而是語言允許的唯一做法；
形狀的其餘部分由讀這個模組原始碼的 `scripts/log-check.sh` 把關。

它的代價，不打折地說：`Logger` 帶著一個 `list` 與一個 `Sink`，所以安裝一個是好幾次機器寫入而不是一次，而在 `install`
執行期間發生的讀取就是 data race。**規則是啟動時設定一次、由單一 coroutine 設定，然後只讀**——這是一條規則，不是保證。
需要在程式執行中改變的東西，答案是 `log.new()`：instance 是值，而交給元件的值不會跟任何人競爭。安裝兩次是合法的，
最後一次贏，而且沒有任何地方會說出來。

### 等級

`TRACE` `DEBUG` `INFO` `WARN` `ERROR` `FATAL` 與 `OFF`，是一個 **enum** 的 variant。它們原本是 `int` 常數，而型別
能做到的正是 `int` 做不到的：`log.new().level(2)` 與 `log.new().level(99)` 兩者都合法而且什麼都不會說；現在 `E340`
會擋下把 `int` 放進 `Level` 的位置、也會擋下把 `Level` 放進 `int` 的位置，`E347` 則擋下拿 variant 跟數字比較。

更深的收穫是每個算繪函式都是**窮盡的 `match`**：新增一個等級就不可能不替它排序、命名、上色——`E428` 會指名被忘掉的
那一條 arm。`int` 版本只會印出空白，然後什麼都不說。

**`OFF` 根本不在那個順序裡。** 它是「什麼都不收」的門檻：設到它的 logger 什麼都不寫，包括寫*在* `OFF` 上的那一行。
順序本身是一個私有函式，而 enum 的宣告順序刻意**不是**契約——模組裡沒有任何地方讀 discriminant，所以把 variant
依字母排序不會改變任何程式過濾掉什麼。

**`ZERG_LOG_LEVEL` 決定等級，依名字。** 是 `ZERG_LOG_LEVEL=debug`，永遠不是 `=1`：數字會把模組拒絕查閱的
discriminant 釘死。不認得的名字就是 `INFO`，理由跟不認得的 `ZERG_LOG` 就是 `pretty` 一樣。`log.parse_level(s)`
是同一個讀取器，公開出來給有自己旗標的程式用。

`fatal` 寫完它那一行之後**以 1 結束程式**。它不是 panic——Zerg 用 `raise` 說那件事，而一個跟 error taxonomy 競爭的
logger 會讓程式有兩種互不相干的結束方式。而且**即使在那一行不會被寫出的等級**它也照樣結束：把 logger 調安靜改變的是
「回報了什麼」，永遠不是「做了什麼」。

### 一行就是一次 write

整行（含換行）交給單一一次 `__zrt_write`。這不是細節：`zrt_report` 曾經把 prefix 與訊息拆成兩次 write，而一支壓力測試
在 24000 行裡找到 **830 行**帶著另一種 kind 的訊息。`scripts/log-check.sh` 是靠**讀原始碼**釘住它的：剛好一個
`io.ewrite(line)`、沒有 `eprintln`（那是兩次 write）。它**不是**用壓力測試釘住的，而且說明了原因：同一支腳本曾把
`emit` 改成兩次 write，24 條 coroutine 寫 12000 行也撕不出來——coroutine 是協作式的，兩次相鄰的 write 之間沒有
停泊點。那支程式仍然會跑，只負責它能負責的宣稱：每一行都到了，而且是完整的。

不加引號就有歧義的值——空字串，或帶有空白、引號、反斜線、`=`、控制字元的——會走 `json.encode`，也就是這棵樹裡唯一的
跳脫實作。所以值裡的換行是被跳脫而不是被寫出來的，一筆記錄仍然是一行。

### 兩種格式，以及各由什麼決定

**預設是 `pretty`**，而除了 `Logger.format` 之外，只有 `ZERG_LOG=json` 會換掉它。這跟預設是 JSON 的 zerolog 不同，
理由是另一端坐著誰：一支沒被設定過的程式，就是有人正在跑的那一支。

```console
$ ./myprog
2026-08-15T10:22:31Z INF compiling  file=a.zg line=12

$ ZERG_LOG=json ./myprog
{"t":"2026-08-15T10:22:31Z","l":"info","msg":"compiling","file":"a.zg","line":12}
```

**顏色跟著 `isatty`，格式不跟。** 顏色是一種算繪——它關於裝置，而「這是不是一個裝置」正是
[`os.isatty`](#os) 回答的問題，所以在終端機上有顏色、進 pipe 就沒有。格式是一個關於「這輸出是給誰看的」的語意
選擇，而一支「輸出被重導時形狀就會變」的程式，它的 log 沒辦法用同一種方式讀第二次。`NO_COLOR` 會蓋過終端機，
而且是靠它的**存在**——任何值都算，包括空字串。`ZERG_LOG` 認不得的值一律當 `pretty`，而不是報錯：一個因為變數
拼錯就不肯啟動的 logger，會為了一件跟它無關的事把程式帶走。

只有**等級**會上色。那是讀的人在掃視的欄位，而替訊息或值上色會跟它們本身的內容打架。JSON 行則完全不上色——
在一個給機器 parse 的欄位裡放跳脫碼是損毀，不是裝飾。

JSON 行是透過 [`json`](#json) 組出來的，不是手工拼的,這正是那個模組要從 language server 裡拉出來的原因:
整棵樹只有一份跳脫實作,引號、換行與 tab 就只有一個地方要弄對。它固定的三個 key——`t`、`l`、`msg`——依這個
順序排在最前面。

### 目的地

`Sink` 是一個**帶著 mode 的值**，不是 spec 也不是 closure：spec 需要 `#[dyn]`（non-`#[dyn]` 的 provided method 有一個
已知延後的缺口），而 closure 只要提到被 import 的模組就是 `E735`。`to_chan` 是讓 logger 可測的關鍵——`write(2)` 寫出去
就讀不回來了，所以這個模組自己的 suite 是把那些位元組收下來斷言的。channel sink 需要事先有足夠的容量，因為送進滿的
channel 會把送出的 coroutine 停住。

## `time`

時鐘、日曆與 timer。`now` 是日期；`monotonic` 只有作為**差值**（經過時間）才有意義，且永不倒退。**timer 就是一條
channel**——`after` 與 `ticker` 回傳 receive-only channel，所以對它們的一條 `select` arm 就是 timeout 或一次 tick，
不需要任何新語法（見 [Coroutines](../code/coroutine.zh-TW.md)）。duration 的單位是**奈秒**，與 `monotonic` 的讀數
同單位；`<= 0` 的 duration 會立刻觸發。

| 函式                       | 摘要                                           |
| -------------------------- | ---------------------------------------------- |
| `now() -> int`             | 牆鐘時間，Unix epoch 起算的整數秒              |
| `monotonic() -> int`       | 單調時鐘讀數（奈秒；請取差值）                 |
| `utc(t: int) -> Date`      | 把 Unix 秒數拆成 UTC 的日曆欄位                |
| `rfc3339(t: int) -> str`   | 同一個時刻，寫成 `2025-08-15T10:22:31Z`        |
| `duration(ns: int) -> str` | 人讀得懂的奈秒數——`1.5s`、`250ms`              |
| `after(d) -> <-chan[int]`  | `d` 奈秒過後送出一個值，僅一次                 |
| `ticker(d) -> <-chan[int]` | 每 `d` 奈秒送出一個值；channel 只裝**一** tick |

**`Date` 只有 UTC**，這是決定而不是缺口：本地時間需要時區資料庫，那是一個 host 上的檔案，而 zero-external-dependency
的 stdlib 不會去讀它。欄位是 `year`、`month`（1..12）、`day`（1..31）、`hour`、`minute`、`second`。轉換用的是
civil-from-days 演算法——精確、沒有月份表、沒有閏年分支——而且**1970 之前也對**，因為 Zerg 的除法朝負無窮取整
（`-1 / 86400` 是 `-1`，不是 `0`），取模的正負號跟著除數走。

`rfc3339` 只有秒的精度，因為 `now()` 就只有這麼多。年份落在 `0000`–`9999` 之外時，有幾位就印幾位、負數帶一個前導
`-`——那**已經不是 RFC 3339**，而之所以優於截斷，是因為差這麼遠的時鐘是一個「讀的人必須看得見」的 bug。

`duration` 會挑「還留得下整數部分」的最大單位（`s`、`ms`、`µs`、`ns`），再給最多三位小數、去掉尾端的零，而且是
**截斷不是四捨五入**——所以讀起來低於某個門檻的 duration，真的就低於它。最大單位是秒：沒有分鐘，因為 `1.5m` 會被
讀成 milli 什麼的。

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

> **[not yet]** 這個模組會出貨，但**無法 import**，而且它是十五個模組中唯一如此的一個。`Atomic[T]` 是 generic
> struct，而 generic struct 是本編譯器尚未建出的形式，所以 `import "atomic"` 會在提出請求的那一行被具名拒絕
> ——_E511 the module `atomic` ships and cannot be imported_，並附位置。下表的簽章另外還提到 `Ref[T]`，那個型別
> 也不存在。在這件事落地之前，跨 coroutine 的共享狀態請走 channel。
>
> 它留在表中、而不是被移出出貨集合，是因為本編譯器解析標準函式庫的方式是**列出它的目錄**：一個被移出
> `src/stdlib/` 的模組同時也離開了 `zerg fmt --check` 與其餘的 self-source 集合，會在 generics 到來之前無人閱讀
> 地爛掉。而那樣留在 import 處的會是 _E502 cannot resolve import `atomic`_——一句關於一個明明就在那裡的模組的話。

| 函式                                                           | 摘要                                |
| -------------------------------------------------------------- | ----------------------------------- |
| `atomic(v: int) -> Ref[int]`                                   | 建立持有 `v` 的共享 cell            |
| `load(a: Ref[int]) -> int`                                     | 讀取 cell                           |
| `store(a: Ref[int], v: int) -> int`                            | 寫入 `v` 並回傳                     |
| `swap(a: Ref[int], v: int) -> int`                             | 寫入 `v`，回傳先前值                |
| `fetch_add(a: Ref[int], n: int) -> int`                        | 加 `n`，回傳先前值                  |
| `compare_swap(a: Ref[int], expect: int, desired: int) -> bool` | CAS：等於 `expect` 才設為 `desired` |

## `testing`

一個 `#[test]` 函式所需要、而**語言本身不給**的東西,供 `zerg test` 建置並執行——那個指令走到哪裡,見
[模組、套件與程式](package.zh-TW.md)。

**斷言不在這裡。** `assert cond` 是關鍵字（見 [Grammar](../surface/grammar.zh-TW.md) group 8）:訊息由編譯器寫,
而它說得出三件函式永遠說不出的事——主張寫在哪個檔案哪一行、主張本身的原始文字、以及比較拆開後每個運算元當時的
值。Zerg 沒有 `__FILE__`、也沒有呼叫端歸屬,而條件抵達輔助函式時已經是一個形狀被編譯掉的 `bool`;`assert_eq`
是靠在呼叫端把運算元拆開才買回其中兩個值的。那就是它存在過的理由,也是它不必再存在的理由。

失敗的主張 raise 的是 `AssertionError`,而且沒有別的東西會 raise 它——這正是 `zerg test` 能把它報成**失敗**、
而把其他抵達測試本體頂端的東西報成**崩潰**的原因。

| 函式                                    | 摘要                                         |
| --------------------------------------- | -------------------------------------------- |
| `assert_raises[T](r: Result[T]) -> Err` | 交回一次 `guard` 包住的呼叫所 raise 的 `Err` |

`assert_raises` 不是斷言,所以它仍是函式:它問的是一個**已經結束**的呼叫 raise 了什麼。它拿的是**呼叫當下寫的那個
`guard`**,並把錯誤交回來,所以種類是用語言自己的 `is` 去問,而不是傳進去——在 Zerg 裡型別不是值,
`assert_raises(f, ValueError)` 根本寫不出來:

```text
e := testing.assert_raises(guard { strings.split("a,b", "") })
assert e is ValueError
```

> 用 **closure**——`assert_raises(fn () { strings.split("a,b", "") })`——讀起來更好，但編不過：closure 主體
> 只要提到被 import 的模組就是 _E735 a closure captures `strings`_，因為 namespace 是自由名稱，而 capture 得給它
> 一個型別。每一個透過 `import` 取用自己模組的測試都是這個形狀，所以能服務它們的是 `guard`。

### 執行中的測試說了什麼

`Context` 是測試對 runner 說話的那條 channel,它上面的每個方法都是一則訊息、而不是一項主張。

| 方法                               | 摘要                                    |
| ---------------------------------- | --------------------------------------- |
| `name() -> str`                    | 這個測試自己的名字,報告就是這樣印它     |
| `log(msg: str)`                    | **只在**這個測試失敗時才顯示的一則註記  |
| `skip(reason: str) -> Result[nil]` | 這個測試在這裡不適用;它不是失敗         |
| `fatal(msg: str) -> Result[nil]`   | 現在就停,判定失敗——再走下去只會製造雜訊 |

```text
ctx.log("row 42")
assert row.id == 42
```

**鏈式脈絡沒了。** `ctx.str("file", p).int("row", i).assert(ok, "…")` 之所以存在,是因為一個只會說
`assertion failed` 的斷言需要一個地方掛上那些能讓它可讀的事實——而現在 `assert` 是關鍵字,它的終端根本寫不出來。
除了那些值以外,一條鏈真正的用途是一則**領域註記**:關於 fixture、而不是關於運算式的事。`log` 本來就是那個,而且
做得更好:只在失敗時顯示、掛在測試上而不是掛在單一斷言上,而且不需要終端就已經是完整的。
