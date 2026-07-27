# Zerg

[English](README.md) | 繁體中文

> 想到什麼就寫什麼——做一件事，只有一種、也是唯一一種方法。

Zerg 是一門**編譯式、通用型程式語言**。編譯器會把你的 Zerg 原始碼轉譯成 **C**（預設 **C17**，不行就 fallback 到
**C99**），再交給 C 編譯器（`cc`）做出原生執行檔。程式寫得快、讀得懂、直白到不能再直白。

> **專案狀態——Phase-1 MVP。** Zerg 仍在早期 bootstrap 階段。規格書所定義的*語言*，刻意比目前 _bootstrap 編譯器_
> 已實作的還大，因此規格中每個特性都帶有狀態標記——**implemented**、**not yet** 或 **implementation-defined**。
> 重點缺口見 **[狀態與限制](#狀態與限制)**，逐項細節見 **[語言規格（Language Specification）](docs/language.zh-TW.md)**。

## 設計原則（Design Principles）

| 原則             | 說明                                                                                        |
| ---------------- | ------------------------------------------------------------------------------------------- |
| small and crisp  | 最精簡的語法                                                                                |
| safe by default  | 除非明確標記 `mut` / `pub`，否則預設 immutable 且 private                                   |
| null-safe        | 以 optional 取代 null；沒有那個造成十億美元損失的錯誤                                       |
| concurrent       | 內建 coroutine 與 channel（本階段為 cooperative 的 **N:1** 排程）                           |
| procedural-first | 直白、由上而下的控制流程                                                                    |
| scope-owned      | 無 tracing GC——值在離開 scope 時釋放；recursive 型別與字串採                                |
|                  | reference counting                                                                          |
| strongly typed   | 在編譯期就抓出錯誤                                                                          |
| explicit casts   | 預設無隱式轉換；值以 re-construction（`T(x)`）轉換                                          |
| copy-by-value    | value 型別在指派時複製；reference-counted 的值則共享                                        |
| zero-dependency  | like Go——不依賴任何第三方庫。**runtime**（由 spec 與其 C 實作共同框定）是唯一碰 OS 的底層； |
|                  | **stdlib** 是站在其上的純 Zerg，屬實作細節，只受其 interface 約束                           |

完整語意——primitive 與使用者型別、型別轉換、記憶體模型、並行、null-safety——見
**[語言規格（Language Specification）](docs/language.zh-TW.md)**，另有配套章節：**[Module、Package 與
Program](docs/package.zh-TW.md)**、**[Coroutines 與 Channels](docs/coroutine.zh-TW.md)**、
**[文法（Grammar）](docs/grammar.zh-TW.md)**、**[語法糖（Syntax Sugar）](docs/syntax-sugar.zh-TW.md)**、
**[Collection](docs/collections.zh-TW.md)**、**[Derive 與預設行為](docs/derive.zh-TW.md)**、
**[Process 與 I/O](docs/io.zh-TW.md)**、與 **[FFI](docs/ffi.zh-TW.md)**。

## 快速上手（Quickstart）

先建置 bootstrap 工具鏈（需要 Go ≥ 1.26 與一個 C 編譯器），再編譯一支程式：

```sh
make                        # ./bin/zerg0（Go 種子），再由它建出 ./bin/zerg
cat > hello.zg <<'ZG'
fn main() {
    print "hello, world"
}
ZG
./bin/zerg0 build hello.zg  # 產生 C，再呼叫 cc → ./hello
./hello                     # hello, world
```

`make` 會建出兩個編譯器。`zerg0` 是以 Go 實作的種子——quickstart 用的就是它，今天整條工具鏈也仍由它承擔。
`zerg` 是種子從 [`src/compiler/`](src/compiler) 建出的自舉編譯器；它能編譯自己，但還不是你日常會拿起的那把
工具（需要顯式列出原始檔，且以 repo 根目錄為基準解析 runtime）。這個名字保留給它，因為那是它要去的地方。

單一的 `zerg` 工具預計帶著你需要的子指令：

| 指令                | 作用                                 |
| ------------------- | ------------------------------------ |
| `zerg build <file>` | 把一支 `.zg` 程式編成原生 binary     |
| `zerg fmt <file>`   | 把原始碼格式化成唯一的正規風格       |
| `zerg lint <file>`  | 回報未使用的 import 與死掉的私有宣告 |
| `zerg test <file>`  | 執行程式裡的 `#[test]` 函式          |

`zerg build … --emit c`（或 `tokens` / `ast`）會印出中間形式，而不是實際建置。

## 小核心，一點糖

表面大多是**疊在小核心上的糖**——one way to do it、沒有隱藏：

```text
break if done                   # → if done { break }
with open(path) as f { … }      # scoped 資源，每條離開路徑都 teardown
print f"{count} × {ratio:.2f}"  # Python 式插值 → str 串接

#[derive(Eq, Ord)]              # compiler 依結構代寫 impl
struct Point { x: int; y: int }
```

每個寫法都 desugar 回核心——完整列表見 **[語法糖（Syntax Sugar）](docs/syntax-sugar.zh-TW.md)**。

控制流保持扁平：`break` / `continue` 只作用於最近的 `for`，且**沒有 loop label**——要離開外層迴圈，抽成函式並 `return`。

## 內建函式（Built-in functions）

一組**固定**的、編譯器內建的函式——免 `import`：

| 內建                                      | 作用                                                                       |
| ----------------------------------------- | -------------------------------------------------------------------------- |
| `Ref(x)` / `deref(r)`                     | 建立 / 讀取 reference-counted box                                          |
| `int` `uint` `float` `bool` `byte` `rune` | 原生型別轉換 `T(x)`；`int("…")` 另會解析十進位字串                         |
| `str(bytes)`、`list[byte](s)`             | str ⇄ list 的橋接（另有 `runes`）                                          |
| `ValueError` … `KeyError`                 | 建出該固定 kind 的 `Err`                                                   |
| `addr` `ptr` `ptr[T]` `uint(p)`           | raw pointer 運算——**僅限 `unsafe`**（加上 `.load` / `.store` / `.offset`） |

`print` 是**關鍵字**、不是函式；`list.len()` 這類是**方法**。完整細節見 **[內建函式](docs/builtins.zh-TW.md)**。

## 標準函式庫（Standard library）

站在自帶 runtime 上的純 Zerg 套件（零外部依賴），以 `import "<name>"` 取得：

| 套件          | 提供                                 |
| ------------- | ------------------------------------ |
| **`io`**      | stdout 寫入、整檔與 stdin 讀／寫     |
| **`fs`**      | `exists` / `remove`                  |
| **`os`**      | `env`、`exit`、`platform`、`arch`    |
| **`strings`** | `split` / `join`、搜尋、trim、大小寫 |
| **`ascii`**   | tokeniser 用的位元組分類             |
| **`strconv`** | base-N `parse_int` / `to_string`     |
| **`time`**    | `now`（牆鐘）、`monotonic`           |
| **`math`**    | 數值輔助、`sqrt` / `pow`、`pi` / `e` |
| **`rand`**    | 確定性、非密碼學產生器               |
| **`atomic`**  | 安全的共享可變原語                   |
| **`testing`** | `assert` / `assert_eq` / `assert_ne` |

完整目錄與簽名見 **[標準函式庫](docs/stdlib.zh-TW.md)**。

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
                     │  (C17 → C99)              │
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

Zerg 是 Phase-1 MVP。bootstrap 編譯器已能編譯非平凡的程式——它做 lex、parse、type-check、把 generic
monomorphize，並在 hosted runtime 上產出 C，也已 self-host 自己前端所需的原語（字串掃描、讀檔、整數解析）。
規格書定義整個語言並標註每項的狀態；新手該知道的重點缺口：

**已實作（Implemented）。** value 與 reference 型別、struct、帶 payload 的 enum、generics +
monomorphization、帶 provided method 的 `spec` / `impl`、`derive(Eq, Ord)`、pattern matching、帶
`?` / `??` / `!` / `guard` 的 optional、固定的內建 error taxonomy、recursive 型別（auto-boxed、
reference-counted）、closure（含捕捉）、coroutine + channel + `select`（cooperative 的 **N:1** 排程）、
帶 `pub` 可見性與 `init()` 的 module、`unsafe` 下的 raw pointer 與 inline assembly，以及 `build` / `fmt`
/ `lint` / `test` 工具。

**尚未實作（Not yet，規格中已定義並標註）。** 溢位／除零會 trap 的算術與 wrapping 的 `+%` 系列運算子
（目前算術降成純 C）；`Eq` / `Ord` 以外的完整 `derive` 集（`Hash` / `Encode` / `Decode`）；`set[T]`；
`list` / `map` 的相等比較；command literal（`` `git status` ``）；非-error 型別的 `is` 測試；可搶占的
**M:N** 排程器；`Reader` / `stdin` I/O 介面；generic type alias；以及規格狀態標記所追蹤的一批較小的形式。

**已知偏差（規格對照目前行為記錄的 bug）。** 有少數可觀察行為尚未符合意圖語意——bootstrap 目前 emit `-std=c11`
而非規格所定的 C17 預設 / C99 fallback；call 引數與運算元的左到右求值順序尚未強制；以及 named integer 型別的
超範圍字面量尚未被拒絕。每一項都在規格對應處標註。

## DDD（Dream-Driven Development）

功能由作者的夢想與需求驅動——僅此而已。
