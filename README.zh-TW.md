# Zerg

[English](README.md) | 繁體中文

[![CI](https://github.com/cmj0121/zerg/actions/workflows/ci.yml/badge.svg)](https://github.com/cmj0121/zerg/actions/workflows/ci.yml)
[![version](https://img.shields.io/badge/version-0.1.0-blue)](VERSION)

> 想到什麼就寫什麼——做一件事，只有一種、也是唯一一種方法。

Zerg 是一門**編譯式、通用型程式語言**。編譯器把你的原始碼轉譯成 **C**（**C17**；`ZERG_CSTD` 指定時可用 **C99** /
**C11**），再交給 C 編譯器（`cc`）做出原生執行檔。

```text
hello.zg → lexer → parser → type check → C codegen → C17 → cc → ./hello
```

> **Phase-1 MVP。** 規格所定義的語言，刻意比編譯器今天已建置的還大；不足之處見 [狀態](#狀態)。

## 快速上手（Quickstart）

建置需要 Go ≥ 1.26 與一個 C 編譯器。

```sh
make                       # ./bin/zerg0（Go 種子）→ ./bin/zerg，你實際使用的編譯器
cat > hello.zg <<'ZG'
fn main() {
    print "hello, world"
}
ZG
./bin/zerg build hello.zg  # 產生 C，再呼叫 cc → ./hello
./hello                    # hello, world
```

`make` 會建出兩個編譯器，你用的是第二個。`zerg0` 是以 Go 實作的種子，已被裁減到只剩一個工作：建出編譯器。
`zerg` 就是那個編譯器——以 Zerg 寫成、位於 [`src/compiler/`](src/compiler)，並且由它自己編譯。

| 指令                  | 作用                                                  |
| --------------------- | ----------------------------------------------------- |
| `zerg build <file>`   | 編譯——entry 宣告 `main` 時產生執行檔，否則產生 object |
| `zerg fmt <file>`     | 把原始碼改寫成唯一的正規風格                          |
| `zerg lint <file>`    | 回報未使用的 import 與死掉的私有宣告                  |
| `zerg desugar <file>` | 把原始碼改寫成它的 sugar 所代表的 core 形式           |
| `zerg lsp`            | language server，走 stdio（JSON-RPC）                 |

`--emit` 則是停在某個階段：`tokens`、`ast`、`check`（只出診斷）、`c`、`lib`（object）、`bin`（執行檔）。程式是逐
模組建置的——`-j`
可同時編譯多個單元，結果以內容為鍵快取在 `.zerg-cache/`，所以只改一個模組的重建就只重編那一個模組。

**`-o` 指定的就是要寫出的檔案**，每個階段都一樣——`--emit c f.zg -o f.c` 寫出 `f.c`，
`--emit lib f.zg -o out.o` 寫出的就是 `out.o`。各階段不同的只有沒給 `-o` 時的**預設值**：

| 階段                 | 沒給 `-o` 時                                          |
| -------------------- | ----------------------------------------------------- |
| `tokens`、`ast`、`c` | stdout，所以這些階段仍可接管線——`--emit c f.zg > f.c` |
| `--emit check`       | 什麼都沒有——它只產生診斷，不產生任何檔案              |
| `--emit lib`         | 原始碼名稱加上 `.o`——`f.zg` 得到 `f.o`                |
| `--emit bin`         | 原始碼名稱——`f.zg` 得到 `f`                           |

它以前在每個階段的意思都不一樣：`--emit lib` 是把 `.o` 接在拿到的任何字串後面，所以 `-o out.o` 寫出 `out.o.o`；
而 `--emit c`、`--emit tokens` 與 `--emit ast` 則直接丟掉這個旗標一律寫到 stdout——要求寫檔的建置什麼檔案都沒
拿到，而且回傳 0。

## 這門語言

| 原則             | 意思                                                                  |
| ---------------- | --------------------------------------------------------------------- |
| small and crisp  | 最精簡的語法——小核心，加上會 desugar 回核心的糖                       |
| safe by default  | 除非明確標記 `mut` / `pub`，否則預設 immutable 且 private             |
| null-safe        | 以 optional 取代 null；沒有那個造成十億美元損失的錯誤                 |
| strongly typed   | 編譯期就抓出錯誤；無隱式轉換——值以 `T(x)` 重新建構的方式轉換          |
| copy-by-value    | value 型別在指派時複製；reference-counted 的值則共享                  |
| scope-owned      | 無 tracing GC——值在離開 scope 時釋放；recursive 型別與字串採 refcount |
| procedural-first | 直白、由上而下的控制流程——沒有 loop label、沒有隱藏的 dispatch        |
| concurrent       | 內建 coroutine 與 channel，跑在 **M:N** 排程器上                      |
| zero-dependency  | like Go——編譯後的程式不連結任何第三方庫                               |

```zerg
break if done                   # → if done { break }
print f"{count} items"          # 插值 → str 串接

#[derive(Eq)]                   # compiler 依結構代寫 impl
struct Point { x: int; y: int }
```

Zero-dependency 分兩層。**runtime**——透過平台 C 函式庫碰 OS、別無其他的那一小塊 C 底層——由 spec 與其實作共同
框定。**標準函式庫**（`src/stdlib/*.zg`）是站在該底層上的**純 Zerg**，只受其 interface 約束：`io.read_file`
走的是 runtime 的 syscall leaf 迴圈，`math.sqrt` 是純 Zerg 演算法，絕非綁 libm。今天可正常 import 的套件——`io`、
`fs`、`os`、`strings`、`ascii`、`cli`、`strconv`、`time`、`math`、`rand`、`testing`——以 `import "<name>"` 取得。

## 文件

**[`docs/README.zh-TW.md`](docs/README.zh-TW.md)** 是入口：先讀哪一章、每個目錄裝什麼、規格該怎麼讀。
語法的權威在 [`GRAMMAR`](GRAMMAR)，語意的權威在 [`docs/`](docs) 底下的各章。

**[`FUTURE.zh-TW.md`](FUTURE.zh-TW.md)** 是另外一半：語言決定**不要**的東西，以及每個案子要重新打開的門檻。
裡面沒有一項屬於規格。

## 狀態

出貨的編譯器是 **`zerg`**——以 Zerg 寫成、由自己編譯；**`zerg0`** 是 Go 主導的種子，唯一的工作是建置它。
以下每一項狀態聲明、以及規格裡的每一個標記，講的都是 `zerg`，也就是 `make` 之後放進 `bin/` 的那一個。
種子較窄的子集是它自己的契約，記在 [`src/bootstrap/README.md`](src/bootstrap/README.md)，寫 Zerg 的讀者
永遠碰不到。

**契約。** 一個形式不是被正確降階、就是在編譯期被**指名**拒絕。它絕不崩潰、絕不靜默給錯答案，也絕不由 C
編譯器或 linker 對著沒人寫過的產生碼報錯。規格標為 **[not yet]** 的特性，使用它會 raise `NotImplemented`
然後停下。

**編譯器沒做到的地方，規格會說**——在該特性所屬的章節裡標上 **[deviation]**。動筆前最該先知道的是**靜默的**那些:
程式拿到一個答案,而沒有任何診斷。

| 靜默偏差                                            | 章節                                    |
| --------------------------------------------------- | --------------------------------------- |
| `list` 在被迭代時並沒有被凍結——`for` 中 append 有效 | [集合](docs/code/collections.zh-TW.md)  |
| 進入 carrier 的 refcount 值永遠不會被釋放——會漏     | [值與記憶體](docs/core/memory.zh-TW.md) |
| `list` 的 fill 形式 `[v; N]` 每個元素各求值一次 `v` | [集合](docs/code/collections.zh-TW.md)  |

另有兩項是結構性的，執行中的程式感受得到：排程器是**協作式、非搶佔式**，一條 CPU-bound 的 coroutine 在自己
park 之前會一直佔住一個 worker（[coroutine](docs/code/coroutine.zh-TW.md)）；模組可見性只對函式與模組常數
強制，型別與欄位尚未（[模組](docs/runtime/package.zh-TW.md)）。

其餘的一切——什麼已建置、什麼被指名拒絕、還有哪些偏差——都標在規格對應的位置。讓這些標記保持誠實的關卡，
就是 `make help` 列出的那些 target；**`make ci`** 跑整塊板，而 `make gates` 是用來擋下「只是掛在板上、
其實沒人跑」的關卡。

## 授權（License）

Zerg 採**分層授權**，判準只有一個：這段程式碼會不會進到你出貨的執行檔裡？

| 部分                          | 授權             | 意思                               |
| ----------------------------- | ---------------- | ---------------------------------- |
| runtime、標準函式庫、examples | MIT              | 會連進你的程式——你想怎麼出貨都可以 |
| 編譯器（self-hosted 與 seed） | GPL-3.0-or-later | 改了工具鏈再散布，要以相同授權釋出 |
| 規格與 `GRAMMAR`              | CC-BY-SA-4.0     | 可引用、翻譯、另行實作——需標示出處 |

**你用 Zerg 寫的程式是你的。** 編譯器的授權管不到它的產出，而真正被連進你執行檔的 runtime 是 MIT。完整安排見
**[LICENSE](LICENSE)**，包括它**不**授予的東西：名字。

## DDD（Dream-Driven Development）

功能由作者的夢想與需求驅動——僅此而已。
