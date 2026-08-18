# Zerg 文件

亦有 [English](README.md)。

這裡是關於**語言**的一切。專案走到哪裡看[專案 README](../README.zh-TW.md)；
實作這個語言的工具鏈是怎麼組起來的，看[下面](#如何建置工具鏈)。

## 從這裡開始

- **[語言參考](language.zh-TW.md)** —— 索引。所有章節、已分組，各附一行說明涵蓋什麼。
  如果你還不知道要看哪一章，從這裡開始。
- **[Conformance](conformance.zh-TW.md)** —— 如何閱讀本規格：狀態標記
  （`[not yet]`、`[implementation-defined]`、`[deviation]`）的意義，以及一則 diagnostic 或一次 abort
  各自承諾了什麼。讀一次就好，但它會改變你讀其餘所有內容的方式。

## 各目錄裝什麼

| 目錄       | 內容                                                                          |
| ---------- | ----------------------------------------------------------------------------- |
| `core/`    | 型別系統——型別、值與記憶體、spec 與泛型、derive、decorator                    |
| `code/`    | 寫程式——控制流、函式、錯誤處理、collection、coroutine、慣用法                 |
| `surface/` | 表面形式本身——語法糖對照表，以及形式文法                                      |
| `runtime/` | 程式與它以外的世界——模組、I/O、格式化、內建函式、標準函式庫、FFI              |
| `tooling/` | 工具鏈拿你的原始碼做什麼——fmt/lint、它回報的代碼、desugar，以及編輯器問它什麼 |

權威文法不在這裡：它是 repo 根目錄的 [`GRAMMAR`](../GRAMMAR)。
[`surface/grammar.zh-TW.md`](surface/grammar.zh-TW.md) 是它的散文伴讀。

## 如何建置工具鏈

`make` 會建出**三個**編譯器，只留下最後一個。被丟掉的那兩個，正是讓第三個「是」一個
self-hosted 編譯器、而不只是宣稱自己是的東西。

```text
   src/bootstrap/*.go  ──go build──►  bin/zerg0           種子（SEED），以 Go 寫成
                                          │
                          src/compiler/*.zg│build
                                          ▼
                                   bin/.zerg-stage1       中繼品，以 Zerg 寫成
                                          │
                          src/compiler/*.zg│build
                                          ▼
                                     bin/zerg             真正出貨的編譯器
```

| 步驟 | 執行什麼                                       | 留下什麼                         |
| ---- | ---------------------------------------------- | -------------------------------- |
| 0    | `./scripts/gen-version.sh`                     | `VERSION` 生成 `zerg/version.zg` |
| 1    | 在 `src/bootstrap/` 跑 `go build -o bin/zerg0` | `bin/zerg0` —— Go 種子           |
| 2    | `zerg0 build src/compiler/zergc.zg`            | `bin/.zerg-stage1` —— 中繼品     |
| 3    | `.zerg-stage1 build --emit bin src/compiler/…` | `bin/zerg`，之後刪掉 stage 1     |

**種子不在交付路徑上。** 使用者實際跑的那個 binary 是步驟 3 產出的，而步驟 3 是由一個以 Zerg 寫成的
編譯器跑的——所以種子只需要好到能建出「能建出真品的東西」，而且每一次 `make` 都在走 self-host 這條路，
而不是把它留給一個沒人會跑的獨立指令。一個無法重現自己的編譯器過不了步驟 3。

**三者各自回答不同的問題。**

| 編譯器         | 語言 | 它的工作                                                      | 它的契約           |
| -------------- | ---- | ------------------------------------------------------------- | ------------------ |
| `zerg0`        | Go   | 建出 self-hosting 編譯器，僅此而已                            | 種子自己的 README  |
| `.zerg-stage1` | Zerg | 建出 `zerg`——為了一個指令而存在，跑完就刪                     | 沒有；它從不被安裝 |
| `zerg`         | Zerg | 全部：`build`、`test`、`fmt`、`lint`、`desugar`、`doc`、`lsp` | 本規格             |

種子只懂 **`Zerg-boot`**——`src/compiler/` 實際用到的那一小片語言。之外的形式會被**具名拒絕**而不是
被誤編譯；而寫 Zerg 的讀者永遠不會碰到那個子集：這些章節裡的每一個標記講的都是 `zerg`，也就是 `make`
留在 `bin/` 的那一個。

**在 `zerg` 內部，一份原始碼走這麼遠**，種子跑的也是同一條管線：

```text
hello.zg → lex → parse → check → 產生 C（C17）→ cc → ./hello
```

一個程式是**逐模組**編譯的——一個檔案，或一個目錄模組的數個檔案——各自成為一個 object，最後一次連結
把它們兜起來。這就是 `-j` 平行化的單位，也是 `.zerg-cache/` 以內容為鍵所快取的單位，所以改一個模組
只會重編一個模組。

**是什麼讓這條鏈保持誠實**，每一項都是 `make help` 裡的一個 target：

| Gate            | 問的問題                                                |
| --------------- | ------------------------------------------------------- |
| `make build`    | 編譯器還能不能重現自己——步驟 2 與 3 本身就是證明        |
| `make fixpoint` | 對自己的原始碼，它每次都產出**同樣的 C**                |
| `make oracle`   | 種子與 `zerg` 對一個合法程式的看法一致                  |
| `make corpus`   | corpus 裡的每個案例都印出它該印的                       |
| `make refuse`   | `zerg` 還沒實作的形式由編譯器具名拒絕，而不是由 `cc` 報 |
| `make test`     | 整塊看板，依序跑一遍                                    |

**runtime**（`src/runtime/csrc`，C）與**標準函式庫**（`src/stdlib`，架在該 runtime 上的純 Zerg）是被
編進「編譯器所編譯的程式」裡，而不是編進編譯器裡——見[模組](runtime/package.zh-TW.md)與
[標準函式庫](runtime/stdlib.zh-TW.md)。這一節的深入版本：種子看
[`src/bootstrap/README.zh-TW.md`](../src/bootstrap/README.zh-TW.md)，出貨的編譯器看
[`src/compiler/README.zh-TW.md`](../src/compiler/README.zh-TW.md)。

## 怎麼找

每一章都是一對——`X.md` 與 `X.zh-TW.md`——並排放在同一個目錄，因為兩者是一起寫、一起改的。

```sh
rg "copy-by-value" docs/            # 中英一起搜
rg "copy-by-value" --glob '!*.zh-TW.md' docs/   # 只搜英文
rg "spec" docs/core/                # 只搜某個主題
```

## 新增一章

中英兩份、同一個目錄，並在 [`language.zh-TW.md`](language.zh-TW.md) 的章節表裡、它所屬的
那一組加上一列。

這個 repo 引用到的每一條路徑——包括寫在原始碼註解裡、任何 markdown 工具都看不見的那些
`docs/…md` 純文字提及——都由 `make docs-links` 檢查，而它在 CI 裡跑。搬動或改名一個頁面時，
它就是告訴你漏了什麼的東西。
