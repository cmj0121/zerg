# Zerg

[English](README.md) | 繁體中文

> 想到什麼就寫什麼——做一件事，只有一種、也是唯一一種方法。

Zerg 是一門**編譯式、通用型程式語言**。編譯器會把你的 Zerg 原始碼轉譯成 **C**（**C17**；`ZERG_CSTD` 指定時可用
**C99** / **C11**），再交給 C 編譯器（`cc`）做出原生執行檔。程式寫得快、讀得懂、直白到不能再直白。

> **專案狀態——Phase-1 MVP。** Zerg 仍在早期 bootstrap 階段。規格書所定義的*語言*，刻意比目前 _bootstrap 編譯器_
> 已實作的還大，因此規格中每個特性都帶有狀態標記——**implemented**、**not yet** 或 **implementation-defined**。
> 重點缺口見 **[狀態與限制](#狀態與限制)**，逐項細節見 **[語言規格（Language Specification）](docs/language.zh-TW.md)**。

## 授權(License)

Zerg 採**分層授權**,判準只有一個:這段程式碼會不會進到你出貨的執行檔裡?

| 部分                          | 授權             | 意思                                 |
| ----------------------------- | ---------------- | ------------------------------------ |
| runtime、標準函式庫、examples | MIT              | 會連進你的程式 —— 你想怎麼出貨都可以 |
| 編譯器(self-hosted 與 seed)   | GPL-3.0-or-later | 改了工具鏈再散布,要以相同授權釋出    |
| 規格與 `GRAMMAR`              | CC-BY-SA-4.0     | 可引用、翻譯、另行實作 —— 需標示出處 |

**你用 Zerg 寫的程式是你的。** 編譯器的授權管不到它的產出,而真正被連進你執行檔的 runtime 是
MIT。完整安排見 **[LICENSE](LICENSE)**,包括它**不**授予的東西:名字。

## 設計原則（Design Principles）

| 原則             | 說明                                                                                        |
| ---------------- | ------------------------------------------------------------------------------------------- |
| small and crisp  | 最精簡的語法                                                                                |
| safe by default  | 除非明確標記 `mut` / `pub`，否則預設 immutable 且 private                                   |
| null-safe        | 以 optional 取代 null；沒有那個造成十億美元損失的錯誤                                       |
| concurrent       | 內建 coroutine 與 channel（本階段為 cooperative、非搶佔的 **M:N** 排程）                    |
| procedural-first | 直白、由上而下的控制流程                                                                    |
| scope-owned      | 無 tracing GC——值在離開 scope 時釋放；recursive 型別與字串採 reference counting             |
| strongly typed   | 在編譯期就抓出錯誤                                                                          |
| explicit casts   | 預設無隱式轉換；值以 re-construction（`T(x)`）轉換                                          |
| copy-by-value    | value 型別在指派時複製；reference-counted 的值則共享                                        |
| zero-dependency  | like Go——不依賴任何第三方庫。**runtime**（由 spec 與其 C 實作共同框定）是唯一碰 OS 的底層； |
|                  | **stdlib** 是站在其上的純 Zerg，屬實作細節，只受其 interface 約束                           |

完整語意——primitive 與使用者型別、型別轉換、記憶體模型、並行、null-safety——見
**[語言規格（Language Specification）](docs/language.zh-TW.md)**，另有配套章節：**[Module、Package 與
Program](docs/runtime/package.zh-TW.md)**、**[Coroutines 與 Channels](docs/code/coroutine.zh-TW.md)**、
**[文法（Grammar）](docs/surface/grammar.zh-TW.md)**、**[語法糖（Syntax Sugar）](docs/surface/syntax-sugar.zh-TW.md)**、
**[Collection](docs/code/collections.zh-TW.md)**、**[Derive 與預設行為](docs/core/derive.zh-TW.md)**、
**[Process 與 I/O](docs/runtime/io.zh-TW.md)**、與 **[FFI](docs/runtime/ffi.zh-TW.md)**。

## 快速上手（Quickstart）

先建置 bootstrap 工具鏈（需要 Go ≥ 1.26 與一個 C 編譯器），再編譯一支程式：

```sh
make                                 # ./bin/zerg0（Go 種子），再建出 ./bin/zerg
cat > hello.zg <<'ZG'
fn main() {
    print "hello, world"
}
ZG
./bin/zerg build --emit bin hello.zg # 產生 C，再呼叫 cc → ./hello
./hello                              # hello, world
```

`make` 會建出兩個編譯器，你用的是第二個。`zerg0` 是以 Go 實作的種子，已被裁減到只剩一個工作：建出編譯器。
`zerg` 就是那個編譯器——以 Zerg 寫成，位於 [`src/compiler/`](src/compiler)，並且由它自己編譯（種子只建出一個
中繼，再由中繼建出最終出貨的那個）。

| 指令                        | 作用                                          |
| --------------------------- | --------------------------------------------- |
| `zerg build <file>`         | 把一個模組編成 object（`--emit lib`，預設）   |
| `zerg build --emit bin <f>` | 連結成一支程式                                |
| `zerg fmt <file>`           | 把原始碼改寫成唯一的正規風格                  |
| `zerg lint <file>`          | 回報未使用的 import 與死掉的私有宣告          |
| `zerg desugar <file>`       | 把 source 改寫成它的 sugar 所代表的 core 形式 |
| `zerg lsp`                  | language server,走 stdio(JSON-RPC）           |

`--emit` 另外接受 `tokens`、`ast`、`c`，印出中間形式而不產生檔案。程式是逐模組建置的：`-j` 可同時編譯多個
單元，結果以內容為鍵快取在 `.zerg-cache/`，所以只改一個模組的重建就只重編那一個模組。

## 小核心，一點糖

表面大多是**疊在小核心上的糖**——one way to do it、沒有隱藏：

```text
break if done                   # → if done { break }
with open(path) as f { … }      # scoped 資源，每條離開路徑都 teardown
print f"{count} × {ratio:.2f}"  # Python 式插值 → str 串接

#[derive(Eq, Ord)]              # compiler 依結構代寫 impl
struct Point { x: int; y: int }
```

每個寫法都 desugar 回核心——完整列表見 **[語法糖（Syntax Sugar）](docs/surface/syntax-sugar.zh-TW.md)**。

控制流保持扁平：`break` / `continue` 只作用於最近的 `for`，且**沒有 loop label**——要離開外層迴圈，抽成函式並 `return`。

## 內建函式（Built-in functions）

一組**固定**的、編譯器內建的函式——免 `import`：

| 內建                                      | 作用                                                     |
| ----------------------------------------- | -------------------------------------------------------- |
| `Ref(x)` / `deref(r)`                     | 建立 / 讀取 reference-counted box                        |
| `int` `uint` `float` `bool` `byte` `rune` | 原生型別轉換 `T(x)`；`int("…")` 另會解析十進位字串       |
| `str(bytes)`、`list[byte](s)`             | str ⇄ list 的橋接（另有 `runes`）                        |
| `ValueError` … `KeyError`                 | 建出該固定 kind 的 `Err`                                 |
| `addr` `ptr` `ptr[T]` `uint(p)`           | raw pointer 運算——已寫入規格，**目前兩個編譯器都不支援** |

`print` 是**關鍵字**、不是函式；`list.len()` 這類是**方法**。完整細節見 **[內建函式](docs/runtime/builtins.zh-TW.md)**。

## 標準函式庫（Standard library）

站在自帶 runtime 上的純 Zerg 套件（零外部依賴），以 `import "<name>"` 取得：

| 套件          | 提供                                         |
| ------------- | -------------------------------------------- |
| **`io`**      | stdout 寫入、整檔與 stdin 讀／寫             |
| **`fs`**      | `exists` / `remove`                          |
| **`os`**      | `env`、`exit`、`platform`、`arch`、`run`     |
| **`strings`** | `split` / `join`、搜尋、trim、大小寫         |
| **`ascii`**   | tokeniser 用的位元組分類                     |
| **`cli`**     | 選項解析與據以產生的 `--help`                |
| **`strconv`** | base-N `parse_int` / `to_string`             |
| **`time`**    | `now`、`monotonic`、`after` / `ticker` timer |
| **`math`**    | 數值輔助、`sqrt` / `pow`、`pi` / `e`         |
| **`rand`**    | 確定性、非密碼學產生器                       |
| **`atomic`**  | 安全的共享可變原語                           |
| **`testing`** | `assert` / `assert_eq` / `assert_ne`         |

完整目錄與簽名見 **[標準函式庫](docs/runtime/stdlib.zh-TW.md)**。

## 編譯流程（Compile Flow）

```text
┌──────────────────┐
│  Zerg source     │
│  (.zg)           │
└────────┬─────────┘
         │
         ▼
┌────────────────────────── Zerg compiler ───────────────────────────┐
│                                                                    │
│  ┌─────────┐    ┌─────────┐    ┌────────────┐    ┌─────────────┐   │
│  │  lexer  │──> │ parser  │──> │ type check │──> │  C codegen  │   │
│  └─────────┘    └─────────┘    └────────────┘    └─────────────┘   │
│  └───────────────── frontend ──────────────┘     └── backend ──┘   │
└─────────────────────────────────┬──────────────────────────────────┘
                                  │
                                  ▼
                     ┌───────────────────────────┐
                     │  C source code            │
                     │  (C17, or C99 / C11)      │
                     └─────────────┬─────────────┘
                                  │
                                  ▼
                     ┌───────────────────────────┐
                     │  C compiler (cc)          │
                     └─────────────┬─────────────┘
                                  │
                                  ▼
                     ┌───────────────────────────┐
                     │  native executable        │
                     └───────────────────────────┘
```

Bootstrap 編譯器：以 **Go** 撰寫，刻意保持最小化。它產出 C 並外呼 `cc`；一個以 C 寫成的小型 hosted runtime
提供排程器、channel、reference counting，以及字串／collection 的原語。

**Zero-dependency，兩層。** 編譯後的程式不連結任何第三方庫。**runtime**——透過平台 C 函式庫
（libc / libSystem）碰 OS、別無其他的那一小塊 C 底層——由 **spec 與其實作共同框定**（語意，加上編譯器所依賴的
具體 layout 與 ABI）。**標準函式庫**（`src/stdlib/*.zg`）是站在該底層上的**純 Zerg**，屬實作細節，**只受其
interface 約束**——所以 `io.read_file` 走的是 runtime 的 syscall leaf 迴圈、`math.sqrt` 是純 Zerg 演算法，絕非
綁 libc / libm。詳見 [`src/runtime/README.md`](src/runtime/README.md)。

## 狀態與限制

Zerg 處於 Phase-1 MVP。出貨的編譯器是 **`zerg`**——以 Zerg 寫成、由自己編譯;**`zerg0`** 是 Go 主導的種子,
唯一的工作是建置它。以下每一項狀態聲明、以及規格裡的每一個標記,講的都是 **`zerg`**,也就是 `make` 之後放進
`bin/` 的那一個。種子較窄的子集是它自己的契約,記在
[`src/bootstrap/README.md`](src/bootstrap/README.md),寫 Zerg 的讀者永遠碰不到。

**契約。** 一個形式不是被正確降階、就是在編譯期被**指名**拒絕。它絕不崩潰、絕不靜默給錯答案,也絕不由 C
編譯器或 linker 對著沒人寫過的產生碼報錯。規格標為 **[not yet]** 的特性,使用它會 raise `NotImplemented`
然後停下。

**已建置的部分。** struct 與 enum(payload、遞迴,以及可觀察的 discriminant)、帶窮盡性檢查的 `match`、
`list[T]` 與 `map[K, V]`、字串與 byte、`mut &` 參數、帶 `?` / `??` / `?.` / `!` 的 optional、完整的
value tier(`Either[X, Y]` / `Result[T]` / `Left` / `Right`)、`guard` / `raise` 與 cause 串接、`defer`
與 `del`、range、f-string、inherent `impl` 與 `spec` / `impl Spec for T`、帶 `pub` 與 `init()` 的模組
——包含模組常數,其 `pub` 在裸名與限定名兩種寫法上都跨邊界強制——型別引數由呼叫端解出的泛型**函式**、
`#[derive(Eq)]` 以及它為 struct 或無 payload enum 寫出的 `==`、list slicing、對 `str` 的 rune 迭代、
帶檢查的整數算術與並列的 `%` 後綴 wrapping 形式、`print`／f-string hole／`str(…)` 三者都會走的
`display` / `debug` override、module 層級 `unsafe { … }` 分組裡的可變 global,
以及整章並行——`spawn`、`chan[T]`、有向端點、`close`、`select` 與 `for select`(含非阻塞的 `_` arm),
還有 `time.after` / `time.ticker`。

**尚未實作(每一項都被指名拒絕)。** 泛型 `struct`、`enum` 或 method,指名兩個 spec 的 bound
(`T: A + B`),generic type alias,以及呼叫端的顯式型別引數(`id[int](7)`);payload enum 上的 `derive`,
以及 `Ord` / `Hash` / `Encode` / `Decode`;`spec` 的 provided method;會捕獲的 closure;呼叫端的具名引數;
`set[T]`;定長陣列 `[T; N]`;`list` / `map` 的相等比較;tuple、struct 與 list pattern、or-pattern 與
解構繫結;block 當成表達式,連帶當 `match` arm body;f-string 的轉換(`!r` / `!s` / `!a`)、format spec
與 `{x=}`;複合值在沒有宣告 override 時會退回的結構化渲染;`Ref[T]` 與 `atomic` 模組;command literal;
獨立的 `unsafe fn`、裸指標與內嵌組語;非錯誤型別的 `is` 測試;`Reader` I/O 介面;以及 `zerg test` runner。

**已知偏差（規格對照目前行為記錄的 bug）。** 其中三項是**靜默的**——程式編得過、答案是錯的——這幾項最該先知道：
`str` 字面值的 `match` arm 永遠不成立；if-expression 不檢查各分支型別是否一致；以及 `byte` 上的 `~` 給出未遮罩的
64-bit 補數。

其餘是結構性的：仍有一部分拒絕不帶位置——被檢查的規則會報 `file:line:col`、原始行與 caret，已經學會位置的拒絕
也會，但只說出形式名字的 parser 層 `raise` 仍佔較大的一半（`make reject-fuzz` 會數它們）；模組可見性只對函式
與模組常數強制、型別與欄位尚未——一個模組仍讀得到另一個模組的私有 struct 與其私有欄位；頂層常數以原始碼順序
初始化，所以前向參照讀到 0；call 引數與運算元的左到右求值順序尚未強制；排程器是**協作式、非搶佔式**——在一條 coroutine
自己 park 之前沒有東西能把它從 worker 上拿下來，所以一個 CPU-bound 的 coroutine 會佔住一條，數量到達 worker
數就讓整個程式停擺。每一項都在規格對應處標註。

## DDD（Dream-Driven Development）

功能由作者的夢想與需求驅動——僅此而已。
