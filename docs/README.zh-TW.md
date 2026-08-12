# Zerg 文件

亦有 [English](README.md)。

這裡是關於**語言**的一切。工具鏈是什麼、怎麼建置、專案走到哪裡，看
[專案 README](../README.zh-TW.md)。

## 從這裡開始

- **[語言參考](language.zh-TW.md)** —— 索引。所有章節、已分組，各附一行說明涵蓋什麼。
  如果你還不知道要看哪一章，從這裡開始。
- **[Conformance](conformance.zh-TW.md)** —— 如何閱讀本規格：狀態標記
  （`[not yet]`、`[implementation-defined]`、`[deviation]`）的意義，以及一則 diagnostic 或一次 abort
  各自承諾了什麼。讀一次就好，但它會改變你讀其餘所有內容的方式。

## 各目錄裝什麼

| 目錄       | 內容                                                             |
| ---------- | ---------------------------------------------------------------- |
| `core/`    | 型別系統——型別、值與記憶體、spec 與泛型、derive、decorator       |
| `code/`    | 寫程式——控制流、函式、錯誤處理、collection、coroutine、慣用法    |
| `surface/` | 表面形式本身——語法糖對照表，以及形式文法                         |
| `runtime/` | 程式與它以外的世界——模組、I/O、格式化、內建函式、標準函式庫、FFI |
| `tooling/` | 工具鏈對你的原始碼做什麼——格式化器與檢查器的規則                 |

權威文法不在這裡：它是 repo 根目錄的 [`GRAMMAR`](../GRAMMAR)。
[`surface/grammar.zh-TW.md`](surface/grammar.zh-TW.md) 是它的散文伴讀。

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
