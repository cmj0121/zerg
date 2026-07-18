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
| 2   | Lexical         | comment、identifier、keyword、newline、block                    | 已落地 |
| 3   | Literals        | `bool`、`int`（`0x`/`0o`/`0b`）、`float`、`rune`、`byte`、`str` | 已落地 |
| 4   | Bindings & Expr | `:=`、`mut`、operator 與優先序                                  | 已落地 |
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

statement 與下一個之間以**換行**或分號 `;` 分隔。兩者都**文法合法**，但 **formatter 會正規化**：把一行多
statement 拆成一行一個——所以 canonical Zerg **一行剛好一個 statement**，`;` 幾乎不會留存於格式化後的原始碼。
（`;` 也出現在 array 型別與字面量 `[T; N]` 這個無關位置，formatter 會保留。）文法定義的第一個 statement 是
**`nop`**：**空 statement** 的 placeholder。它什麼都不做、也不產出值；在「需要一個 statement、但不想做任何
事」的地方頂替：

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

## Group 2 — Lexical

原始碼是 UTF-8。水平空白（space、tab）分隔 token；換行只作為 statement 分隔符（group 1）才有意義。lexical
group 界定「token 是什麼」：

```text
letter     ::= [a-zA-Z]
digit      ::= [0-9]
identifier ::= ( letter | '_' ) ( letter | digit | '_' )*
NEWLINE    ::= '\n'
WS         ::= ( ' ' | '\t' )+
COMMENT    ::= '#' [^\n]*
block      ::= '{' stmt-list '}'
```

**identifier** 以字母或 `_` 開頭，其後接字母、數字或 `_`。**保留字（keyword）**永遠不會是 identifier；完整的
保留字集合為：

```text
nop   fn     mut     pub      return   import
if    else   for     in       break    continue
match spawn  select  struct   enum     spec
type  impl   derive  package  init     extern
defer del    raise   guard    is       not
and   or     print   true     false    nil
```

**block** 以大括號包住一串 statement——之後的 group 會把它掛在 function、loop 或 conditional 的主體上。block
內的 statement 沿用與頂層相同的分隔規則，所以空的 block 用 placeholder 寫成：`{ nop }`。

## Group 3 — Literals

literal 表示一個常數值：

```text
literal     ::= bool-lit | nil-lit | float-lit | int-lit
              | rune-lit | byte-lit | str-lit | raw-str-lit
bool-lit    ::= 'true' | 'false'
nil-lit     ::= 'nil'
int-lit     ::= dec-int | hex-int | oct-int | bin-int
float-lit   ::= dec-int '.' dec-int exponent? | dec-int exponent
rune-lit    ::= "'" ( rune-char | escape ) "'"
byte-lit    ::= 'b' "'" ( byte-char | byte-escape ) "'"
str-lit     ::= '"' ( str-char | escape )* '"'
raw-str-lit ::= 'r' '"' raw-char* '"'
```

- **數字。** 整數為十進位或帶基底——`0x1F`、`0o17`、`0b1010`。float 有小數部分、指數，或兩者——`1.0`、`1e3`、
  `6.022e23`。數字 literal 是**未定型的**：採用其語境要求的型別（整數預設 `int`，帶小數/指數者預設 `float`）。`_`
  可**分組數字**，只允許在數字之間——`1_000_000`、`0xDE_AD_BE_EF`。正負號不屬 literal；`-5` 是對 `5` 施加一元
  減號（運算子）。
- **`rune` 與 `byte`。** **`rune`** 是單引號內的一個 Unicode code point——`'a'`、`'\n'`、`'\u{1F600}'`。
  **`byte`** 是一個 octet，加 `b` 前綴——`b'a'`、`b'\x41'`——或用 cast 寫成 `byte(0x41)`。單引號留給這兩者；字串
  用雙引號。
- **`str` 與 raw string。** **`str`** 用雙引號並處理 escape（`\n \t \r \0 \\ \" \'` 與 `\u{…}`）。**raw
  string** 加 `r` 前綴，**不**處理任何 escape——`r"C:\tmp\new"` 是十個字面字元。`str` 不能含 NUL，所以 `\0` 與
  `\u{0}` 在 `"…"` 內非法（在 `rune` 或 `byte` 內則可）。

`f"…"` 字串插值**不在**此處——它是 expression，留待之後的 group 及其獨立 commit。

## Group 4 — Bindings & Expressions

**binding** 引入一個名字；reassign 更新一個既有名字：

```text
binding   ::= 'mut'? identifier ':=' expr
reassign  ::= lvalue '=' expr
expr-stmt ::= expr
lvalue    ::= identifier ( '.' identifier | '[' expr ']' )*
```

`:=` 綁定一個**全新、immutable** 的名字；`mut x := …` 使其可重綁；`=` **重新指派**一個既有的 `mut` 綁定（或
field／元素）。單獨一個 expression——一次 call，或為副作用而跑的 `match`——就是一個 statement。（在 `:=` 處解構
pattern，如 `(q, r) := divmod(x, y)`，隨 group 6 的 pattern 一起到來。）

expression 是一條優先序 cascade。每個二元層級都是**左結合**；**比較是非結合**——`a < b < c` 依設計無法 parse。

| 優先序 | 運算子                              | 結合   |
| ------ | ----------------------------------- | ------ |
| 1 最高 | `.` `()` `[]`（field／call／index） | 左     |
| 2      | `not` `~` 一元 `-` `-%`             | 右     |
| 3      | `*` `/` `%` `*%` `<<` `>>` `&`      | 左     |
| 4      | `+` `-` `+%` `-%` `\|` `^`          | 左     |
| 5      | `==` `!=` `<` `>` `<=` `>=` `is`    | 非結合 |
| 6      | `and`                               | 左     |
| 7 最低 | `or`                                | 左     |

`%` 後綴的 `+%` `-%` `*%` 是**回繞（wrapping）**算術運算子；`~` 是 bitwise 補數。bitwise `&` `<<` `>>` 與乘法級
同層、`\|` `^` 與加法級同層——都比比較緊一級，所以 `a & b == c` 讀作 `(a & b) == c`，避開 C 的優先序陷阱。`is`
以一個 spec 或 variant 名字測試 existential（完整形式見 group 6–7）。正負號是運算子，不屬 literal。

null-safety 與 error 運算子（`?` `??` `?.` `!`）**不在**此處——歸 error group。

### 字串插值與 `print`

**f-string** 是一種 primary expression——帶 `{ expr }` 洞的字串：

```text
fstr-lit ::= 'f' '"' ( fstr-char | escape | '{{' | '}}' | interp )* '"'
interp   ::= '{' expr '}'
print    ::= 'print' expr
```

`f"sum={x + y}"` 把每個洞經 `display()` 算出並串接——它在**編譯期 desugar** 成 `str` 串接，沒有 runtime
format engine。純 `"…"` 是 literal（大括號是普通字元）；只有 f-string 會讀 `{…}`，而 `{{` / `}}` 寫出字面
大括號。像 `f"{x:>.2f}"` 這種 format specifier **deferred** 到獨立的 per-type format protocol。**`print`**
把一個值的 `display()` 加換行寫到 stdout——保留字、恆在 scope、best-effort（永不 raise），所以
`print f"hello {name}"` 是最小程式。

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
