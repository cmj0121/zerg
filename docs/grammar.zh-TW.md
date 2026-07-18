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
| 5   | Functions       | `fn`、參數、預設值、named argument、closure、`return`           | 已落地 |
| 6   | Control flow    | `if`、`for … in`、`match` 與 pattern                            | 已落地 |
| 7   | Types           | `struct`、`enum`、tuple、`type X = Y`、`spec`                   | 已落地 |

其後是次要 group：error operator（`?` `??` `?.` `!` `raise` `guard`）、concurrency
（`spawn` / `chan` / `select` / `<-`）、module（`import` / `pub` / `package` / `init`）、FFI
（`extern "C"`），以及 `defer` / `del`。

## Group 1 — `nop` 與程式骨架

一個 Zerg 程式是一串 statement：

```text
program       ::= stmt-list
stmt-list     ::= stmt-sep* ( statement ( stmt-sep+ statement )* stmt-sep* )?
stmt-sep      ::= NEWLINE | ';'
statement     ::= simple-stmt | compound-stmt
simple-stmt   ::= nop | …          # 無區塊；一行即可
compound-stmt ::= …                # 擁有一個 '{ … }' 區塊（if / for / fn / struct / …）
nop           ::= 'nop'
```

statement 分為 **simple**（無區塊、一行即可：`nop`、binding、`return` 等）或 **compound**（擁有 `{ … }` 區塊：
`if`、`for`、`fn`、`struct` 等）。`nop` 是最小的 simple statement。statement 與下一個之間以**換行**或分號 `;`
分隔。兩者都**文法合法**，但 **formatter 會正規化**：把一行多
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

comment 不是 statement——`#` 一路到行尾，且 Zerg **沒有 block comment**（唯一例外是 `#[`，它起始一個 decorator，
見 group 7）：

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
type  impl   package init     extern   defer
del   raise  guard   is       not      and
or    print  this    true     false    nil
```

（`derive` 不是關鍵字——它是 `#[derive(…)]` 裡的 decorator 名稱。）

**block** 以大括號包住一串 statement——之後的 group 會把它掛在 function、loop 或 conditional 的主體上。block
內的 statement 沿用與頂層相同的分隔規則，所以空的 block 用 placeholder 寫成：`{ nop }`。

**換行與 ASI。** 換行由 lexer 實現為 `;` 分隔符（automatic `;` insertion）：遇行尾，若該行最後一個 token 能
**結束一個項目**——identifier、literal、`)`、`]`、`}`、`?`、`_`、`this`，或 `return` / `break` / `continue` /
`nop`——就補一個 `;`。在未閉合的 `(` 或 `[` 之內則不補，故運算式或型別可於其中跨行（續行時把運算子放在行尾）。這
一條規則讓 **statement、struct field、enum variant、match arm** 共用同一個換行分隔符。`,` 則用來分隔**值清單**
的元素——argument、tuple、generic、variant payload，以及 struct pattern/literal 的 field（composite，如 Go 的
`Point{X: 1, Y: 2}`）。

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

## Group 5 — Functions

function 是 first-class value——具名宣告、匿名 expression，與一個型別：

```text
fn-decl    ::= 'pub'? 'mut'? 'fn' identifier '(' param-list? ')' ret-type? block
fn-expr    ::= 'fn' '(' param-list? ')' ret-type? block
fn-type    ::= 'fn' '(' param-type-list? ')' ret-type?
ret-type   ::= '->' type
return     ::= 'return' expr?
param      ::= 'mut'? identifier ':' type ( '=' expr )?
param-type ::= 'mut'? type
```

- **宣告 vs expression。** `fn name(…) -> R { … }` 綁定一個名字；**匿名** `fn(…) -> R { … }` 是 expression
  （一個 closure）。`pub` 匯出宣告的名字，且不屬型別。
- **Return。** `return expr` 帶值離開，單獨 `return` 則不帶值。**省略 `-> type`** 表示函式回傳 `nil`。
- **參數。** 參數是 `name: type`，可加 `mut`（by-ref、in-place——屬型別的一部分），亦可帶**預設值** `= expr`。
  call 端的 **named argument** 是 `name: value`（group 4 的 `arg` 形式）：positional 參數在前，之後任一個可具名，
  一旦具名其餘也須具名——這正是能跳過有預設值參數的方式。
- **型別。** function 的型別是 `fn(P…) -> R`——參數（含 `mut`）與結果，別無其他；預設值與參數名字存在宣告裡，不在
  型別中。
- **`mut fn`（mutating method）。** `mut fn` 標記一個**方法**會就地修改其隱式 receiver `this`；呼叫端的 receiver
  須為 `mut` binding。它只在 `impl` 或 `spec` 內有意義——free function 或 closure 沒有 receiver。`mut fn` 追蹤的是
  值**自身 field** 的變動，不含對 `Ref[T]`／foreign handle 背後資源的 effect（那不需 `mut`——見 group 7）。

## Group 6 — Control flow & Pattern matching

`if` 與 `for` 是 statement；`match` 是 expression：

```text
if-stmt     ::= 'if' if-head block ( 'else' 'if' if-head block )* ( 'else' block )?
if-head     ::= expr | identifier ':=' expr
for-stmt    ::= 'for' block | 'for' 'mut'? identifier 'in' expr block
break       ::= 'break' ( 'if' expr )?
continue    ::= 'continue' ( 'if' expr )?
match-expr  ::= 'match' expr '{' match-arm+ '}'
match-arm   ::= pattern '->' expr
pattern     ::= sub-pattern ( '|' sub-pattern )*
sub-pattern ::= variant-pat | struct-pat | tuple-pat | literal-pat | binding-pat | '_'
```

- **`if`。** 條件是 `bool`——沒有 truthiness。**binding head** `if x := expr { … }` 只在 `expr` 存在時執行區塊
  （one-arm-`match` 的 sugar）。`else if` / `else` 照常串接。
- **`for`。** 唯一的迴圈，兩種形式：**`for { … }`** 無限（以 `break` / `return` 離開），與 **`for x in it { … }`**
  走訪 `Iterable`，`x` 以 copy 綁定（**`for mut x`** 就地綁定）。沒有 `while`、沒有 C 式 `for`。**`break` /
  `continue`** 作用於最近的迴圈；**`break if c`** 與 **`continue if c`** 是 `if c { break }` / `if c { continue }`
  的 sugar。
- **`match`。** 一個 expression：依序比對各 arm，取第一個吻合並產出，且每個 arm 產出**同一型別**——所以 `match`
  可用於 `:=`、`return` 或引數。結尾的 **`_`** 涵蓋其餘。
- **Pattern** 以 copy 解構：帶 payload 綁定的 **variant**（`Left(v)`、巢狀 `Left(Some(v))`）、**struct**
  （`Div{q, r}`）、**tuple**（`(a, b)`）、**literal**（以 `equal` 比對）、單純的**綁定**名字、**or-pattern**
  （`A | B`，兩側綁同名）、或萬用字元 **`_`**。tuple 或 struct pattern 也能在 `:=` 綁定處解構——
  `(q, r) := divmod(x, y)`。guard 條件（`Left(v) if v > 0`）暫緩。

## Group 7 — Types & Declarations

自 group 5 起被引用的 type 表達式，以及引入型別與行為的宣告：

```text
type        ::= base-type '?'?
base-type   ::= type-name type-args? | tuple-type | array-type | chan-type | fn-type
type-args   ::= '[' type ( ',' type )* ']'
array-type  ::= '[' type ';' expr ']'
struct-decl ::= decorator* 'pub'? 'struct' identifier generics? '{' field-list? '}'
enum-decl   ::= decorator* 'pub'? 'enum' identifier generics? '{' variant-list? '}'
type-decl   ::= 'pub'? 'type' identifier generics? '=' type
spec-decl   ::= 'pub'? 'spec' identifier generics? '{' spec-member* '}'
impl-decl   ::= 'impl' type-name generics? 'for' type '{' fn-decl* '}'
decorator   ::= '#[' deco-item ( ',' deco-item )* ']'
deco-item   ::= identifier ( '(' deco-arg ( ',' deco-arg )* ')' )?
```

- **Type 表達式。** 一個**名字**加選用**型別引數**（`int`、`User`、`list[int]`、`Either[A, B]`）；一個 **tuple
  type** `(A, B)`；一個**陣列** `[T; N]`（`;` 的另一用途）；一個**通道** `chan[T]`，帶 Go 式方向——`<-chan[T]`
  為 receive-only（receiver）、`chan[T]<-` 為 send-only（sender）；或一個**函式型別** `fn(P…) -> R`
  （group 5）。結尾的 **`?`** 使任何型別成為 **optional**——`str?`。
- **`struct` / `enum`。** 具名、定型 field 的**乘積**，或每個 variant 帶選用 payload 的**和**——`Circle(float)`、
  `Rect(float, float)`。field 與 variant 的分隔**完全比照 statement**（換行，或單行用 `;`）——兩者之間**沒有
  `,`**；payload `(A, B)` 內的 `,` 才是一般清單。兩者皆可泛型——`enum Either[X, Y] { … }`。
- **`type X = Y`。** 一個**強 typedef**——全新、獨立的型別，非透明別名；可泛型。
- **`spec`。** 行為介面：成員為**必要**（只有簽名、無 body）或**提供**（完整方法）。方法**不宣告 receiver**——
  `this` 在方法內為隱式，透過被呼叫的 instance 取得；若 `fn` 用到 `this` 卻無 instance 綁定則為編譯錯誤。self
  型別是 **`This`**。`impl … for …` 由手寫為某型別提供 spec 的方法。
- **Decorator 與 `#[derive(…)]`。** **decorator** `#[…]` 是掛在後續宣告上的 compiler 指令。在 `struct`/`enum` 上
  的 `#[derive(Encode, Decode)]` 請 compiler 讀該型別的**結構**、**生成**所列 spec 的 canonical impl。decorator 是
  **固定、compiler 擁有**的集合——使用者不可自訂（Zerg 無 macro）；其他指令（layout、FFI…）日後於此加入。`#[` 是唯一
  不算註解的 `#`——lexer peek 一字元即分辨。

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
