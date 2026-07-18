# Zerg 文法（Grammar）

語言的形式表面文法——什麼在語法上是合法的，與程式的語意無關。權威的 production 定義在根目錄的
[`GRAMMAR`](../GRAMMAR) 檔，本頁是它的散文說明。屬於 [語言參考](language.zh-TW.md) 的一部分。亦有
[English](grammar.md) 版本。

## 文法怎麼寫

`GRAMMAR` 是一份純文字檔，以 **W3C-style EBNF** 撰寫，用 `#` 行註解——與 Zerg 原始碼相同的註解語法。這套
notation 很小：

| 形式         | 意義                                        |
| ------------ | ------------------------------------------- |
| `name ::= …` | 一條 production；`name` 是 non-terminal     |
| `'text'`     | literal terminal，逐字比對                  |
| `A B`        | `A` 後接 `B`                                |
| `A \| B`     | `A` 或 `B`                                  |
| `( A )`      | 分組                                        |
| `A?`         | 零或一個 `A`                                |
| `A*`         | 零或多個 `A`                                |
| `A+`         | 一或多個 `A`                                |
| `[a-z]`      | 範圍內的一個字元                            |
| `[^x]`       | 除 `x` 外的任何字元                         |
| `UPPER`      | 一個 lexical token（於 Lexical group 定義） |

文法是**一次一個 group、由核心到次要**逐步建立——每個 group 是一次聚焦的 commit。`GRAMMAR` 一段一段長大，
[nvim 工具](#編輯器工具editor-tooling) 也隨之成長。

## Group 列表

| #   | Group           | 涵蓋                                                            | 狀態   |
| --- | --------------- | --------------------------------------------------------------- | ------ |
| 1   | nop & skeleton  | `program`、`statement`、statement 分隔、`nop`                   | 已落地 |
| 2   | Lexical         | comment、identifier、keyword、newline、block                    | 規劃中 |
| 3   | Literals        | `bool`、`int`（`0x`/`0o`/`0b`）、`float`、`rune`、`byte`、`str` | 規劃中 |
| 4   | Bindings & Expr | `:=`、`mut`、operator 與優先序                                  | 規劃中 |
| 5   | Functions       | `fn`、參數、預設值、named argument、closure、`return`           | 規劃中 |
| 6   | Control flow    | `if`、`for … in`、`match` 與 pattern                            | 規劃中 |
| 7   | Types           | `struct`、`enum`、tuple、`type X = Y`、`spec`                   | 規劃中 |

其後是次要 group：error operator（`?` `??` `?.` `!` `raise` `guard`）、concurrency
（`spawn` / `chan` / `select` / `<-`）、module（`import` / `pub` / `package` / `init`）、FFI
（`extern "C"`），以及 `defer` / `del`。

## Group 1 — `nop` 與程式骨架

一個 Zerg 程式是一串 statement：

```text
program   ::= stmt-list
stmt-list ::= stmt-sep* ( statement ( stmt-sep+ statement )* stmt-sep* )?
stmt-sep  ::= NEWLINE | ';'
statement ::= nop
nop       ::= 'nop'
```

statement 之間以**換行**或分號 `;` 分隔——一行一個 statement 是常態，`;` 讓多個 statement 擠在同一行。文法定義
的第一個 statement 是 **`nop`**：**空 statement** 的 placeholder。它什麼都不做、也不產出值；在「需要一個
statement、但不想做任何事」的地方頂替：

```text
fn noop() { nop }        # 空的函式體

for {
    nop                  # 空的迴圈體——空轉，由他處中斷
}
```

之後每個 group 都會為 `statement` 增添一種形式（binding、expression statement、declaration……）；`nop` 始終是
那個永遠可用、永遠無作用的 statement。

comment 不是 statement——`#` 一路到行尾，且 Zerg **沒有 block comment**：

```text
# 整行註解
nop    # 行尾註解
```

## 編輯器工具（Editor tooling）

Neovim 的語法高亮放在 [`editors/nvim/`](../editors/nvim)，是經典的 Vim syntax 檔：

| 檔案                | 角色                                |
| ------------------- | ----------------------------------- |
| `ftdetect/zerg.vim` | 將 `*.zg` 辨識為 `zerg` filetype    |
| `ftplugin/zerg.vim` | buffer 慣例：`#` 註解、4-space 縮排 |
| `syntax/zerg.vim`   | 高亮規則                            |

最快的方式是 **`make install`**，它會把檔案 symlink 進你的 nvim 設定（`$XDG_CONFIG_HOME/nvim`，預設
`~/.config/nvim`）；`make uninstall` 則移除。因為是 symlink，高亮會跟著這份 checkout 走。或者，把
`editors/nvim/` 目錄加進 `runtimepath`。無論哪種方式，高亮都跟著 `GRAMMAR`：只涵蓋已落地的 group，並隨每個新
group 成長。
