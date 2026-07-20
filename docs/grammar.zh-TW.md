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

| #   | Group                | 涵蓋                                                            | 狀態   |
| --- | -------------------- | --------------------------------------------------------------- | ------ |
| 1   | nop & skeleton       | `program`、`statement`、statement 分隔、`nop`                   | 已落地 |
| 2   | Lexical              | comment、identifier、keyword、newline、block                    | 已落地 |
| 3   | Literals             | `bool`、`int`（`0x`/`0o`/`0b`）、`float`、`rune`、`byte`、`str` | 已落地 |
| 4   | Bindings & Expr      | `:=`、`mut`、operator 與優先序                                  | 已落地 |
| 5   | Functions            | `fn`、參數、預設值、named argument、closure、`return`           | 已落地 |
| 6   | Control flow         | `if`、`for … in`、`match` 與 pattern                            | 已落地 |
| 7   | Types                | `struct`、`enum`、tuple、`type X = Y`、`spec`                   | 已落地 |
| 8   | Null-safety & Errors | `?` `??` `?.` `!` `raise` `guard`,與 `T?` / `Result` 兩層       | 已落地 |
| 9   | Concurrency          | `spawn`、`chan[T]()`、`ch <- v`、`<-ch`、`select`               | 已落地 |
| 10  | Modules & Programs   | `import`、`pub import`、`init()`、`pub`、`main`                 | 已落地 |
| 11  | Resource cleanup     | `defer expr`、`del name`                                        | 已落地 |
| 12  | Unsafe               | `unsafe { }`、`unsafe fn`、`ptr` / `ptr[T]`、`asm(…)`           | 已落地 |

以上各 group 皆已落地——表面文法**已完整**。raw memory 與 inline assembly 隨 group 12（`unsafe` / `ptr` /
`asm`）到來，故裸機工作（MMIO、page table、以 `asm` 發 syscall）皆可表達。唯一留白的邊界是 **依 C 符號名的 FFI
import**（命名外部 `malloc`/syscall 直接呼叫）：**待議的開放設計**——可能是薄的 linkage/stdlib 機制而非表面語法——
而非待做功能（syscall 已可透過 `asm` 發出）。**FFI export 不需任何語法**：package 的 `pub` 表面本身**就是**它的 C
ABI（見 [FFI](ffi.zh-TW.md)）。

## Group 1 — `nop` 與程式骨架

一個 Zerg 程式是一串 statement：

```text
program       ::= stmt-list
stmt-list     ::= stmt-sep* ( statement ( stmt-sep+ statement )* stmt-sep* )?
stmt-sep      ::= NEWLINE | ';'
statement     ::= simple-stmt | compound-stmt | decorated-decl
simple-stmt   ::= nop | …          # 無區塊；一行即可
compound-stmt ::= …                # 擁有一個 '{ … }' 區塊（if / for / …）
decorated-decl ::= …               # 引入名字的宣告，可選 #[…] 前綴（fn / struct / …，group 7）
nop           ::= 'nop'
```

statement 分為 **simple**（無區塊、一行即可：`nop`、binding、`return` 等）、**compound**（擁有 `{ … }` 區塊：
`if`、`for` 等），或 **declaration**（`fn`、`struct`、`enum`、`spec` 等——引入名字，可帶 `#[…]` decorator；group 7）。
`nop` 是最小的 simple statement。statement 與下一個之間以**換行**或分號 `;`
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

comment 不是 statement——`#` 一路到行尾。`##` 起始一個 **doc comment**（附著於其後的宣告），`#[` 起始一個
decorator（group 7）；Zerg **沒有 block comment**：

```text
# 整行註解
nop    # 行尾註解
## 一段 doc comment——附著於下方的宣告
fn answer() -> int { return 42 }
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
COMMENT     ::= '#' [^#[\n] [^\n]* | '#' NEWLINE  # '#' 後不接 '#' 或 '[' → line comment
DOC-COMMENT ::= '##' [^\n]*                       # doc comment；附著於其後的宣告
block      ::= '{' stmt-list '}'
```

**identifier** 以字母或 `_` 開頭，其後接字母、數字或 `_`。**保留字（keyword）**永遠不會是 identifier；完整的
保留字集合為：

```text
nop   fn     mut     pub      return   import
if    else   for     in       break    continue
match spawn  select  struct   enum     spec
chan  type   impl    package  init     extern
defer del    raise   guard    is       not
and   or     print   this     with     as
from  true   false   nil
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
              | '"""' ( ml-str-char | escape )* '"""'   # 多行；換行為字面
raw-str-lit ::= 'r' '"' raw-char* '"'
```

- **數字。** 整數為十進位或帶基底——`0x1F`、`0o17`、`0b1010`。float 有小數部分、指數，或兩者——`1.0`、`1e3`、
  `6.022e23`。數字 literal 是**未定型的**：採用其語境要求的型別（整數預設 `int`，帶小數/指數者預設 `float`）。`_`
  可**分組數字**，只允許在數字之間——`1_000_000`、`0xDE_AD_BE_EF`。正負號不屬 literal；`-5` 是對 `5` 施加一元
  減號（運算子）。
- **`rune` 與 `byte`。** **`rune`** 是單引號內的一個 Unicode code point——`'a'`、`'\n'`、`'\u{1F600}'`。
  **`byte`** 是一個 octet，加 `b` 前綴——`b'a'`、`b'\x41'`——或用 cast 寫成 `byte(0x41)`。單引號留給這兩者；字串
  用雙引號。
- **`str`、多行與 raw string。** **`str`** 用雙引號並處理 escape（`\n \t \r \0 \\ \" \'` 與 `\u{…}`）。**三引號**
  `"""…"""` 的 `str` 相同但**可跨行**——換行為字面,內部單一 `"` 或 `""` 免 escape（只有 `"""` 結束它）,適合
  SQL/JSON/文字。**raw string** 加 `r` 前綴,**不**處理任何 escape——`r"C:\tmp\new"` 是十個字面字元。`str` 不能含
  NUL,所以 `\0` 與 `\u{0}` 在 `"…"` 內非法（在 `rune` 或 `byte` 內則可）。

`f"…"` 字串插值**不在**此處——它是 expression，留待之後的 group 及其獨立 commit。

## Group 4 — Bindings & Expressions

**binding** 引入一個名字；reassign 更新一個既有名字：

```text
binding       ::= 'mut'? bind-target ':=' expr   # bind-target：識別字或解構模式
reassign      ::= assign-target '=' expr
expr-stmt     ::= expr
lvalue        ::= identifier ( '.' identifier | '.' dec-int | '[' expr ']' )*   # '.0' = tuple 元素
assign-target ::= lvalue | '(' assign-target ( ',' assign-target )* ')'
                | type-name '{' field-target ( ',' field-target )* ( ',' '..' )? '}'
field-target  ::= identifier ( ':' assign-target )?
```

`:=` 綁定一個**全新、immutable** 的名字；`mut x := …` 使其可重綁；`=` **重新指派**一個既有的 `mut` 綁定（或
field／元素）。單獨一個 expression——一次 call，或為副作用而跑的 `match`——就是一個 statement。`:=` 可**解構**成
新名字（`(q, r) := divmod(x, y)`，group 6），而 `=` **對映到既有 lvalue**——`(a, b) = swap(a, b)`、
`Div{q, r} = divmod(x, y)`——每個葉子可以是任意 lvalue（`(a, obj.f) = …`）。

expression 是一條優先序 cascade。每個二元層級都是**左結合**；**比較是非結合**——`a < b < c` 依設計無法 parse。

| 優先序 | 運算子                                | 結合   |
| ------ | ------------------------------------- | ------ |
| 1 最高 | `.` `()` `[]`（field／call／index）   | 左     |
| 2      | `not` `~` 一元 `-` `-%`               | 右     |
| 3      | `*` `/` `%` `*%` `<<` `>>` `&`        | 左     |
| 4      | `+` `-` `+%` `-%` `\|` `^`            | 左     |
| 5      | `..` `..=`（range）                   | —      |
| 6      | `==` `!=` `<` `>` `<=` `>=` `is` `in` | 非結合 |
| 7      | `and`                                 | 左     |
| 8 最低 | `or`                                  | 左     |

`%` 後綴的 `+%` `-%` `*%` 是**回繞（wrapping）**算術運算子；`~` 是 bitwise 補數。bitwise `&` `<<` `>>` 與乘法級
同層、`\|` `^` 與加法級同層——都比比較緊一級，所以 `a & b == c` 讀作 `(a & b) == c`，避開 C 的優先序陷阱。`is`
測試 existential;`in` 測試**成員資格（membership）**。正負號是運算子,不屬 literal。**range** 比比較緊一級,所以
`v in 0..10` 讀作 `v in (0..10)`:`x..y` 是 `range(x, y)`、`x..=y` 是 `range(x, y + 1)`、`x..` 是開放 range、
`v in r` 是 `r.contains(v)` 的**語法糖**——`Range` / `contains` 機制在 stdlib。（group 8 的 `??` 仍是唯一最鬆的二元
運算子,比 `or` 更鬆。）

null-safety 與 error 運算子（`?` `??` `?.` `!`）在 group 8;postfix 三個（`?` `!` `?.`）併入上面的 `postfix`,
`??` 則在最鬆的層級。

postfix 的 `[…]` 是**索引**或**顯式型別引數**（`parse[int]("42")`、`collect[K, V](…)`），靠 **name resolution**
分辨:base 是值就是索引,是泛型 function 或型別建構子就是型別引數——一個名字只會是其一(型別與 function 不能同名,
group 7),所以**不需要 turbofish**。有逗號（`[X, Y]`）必為型別引數。這與 pattern 分辨 **variant** 或 **binding**
（group 6）是同一套 name resolution;文法帶著 scope 解析,非純 context-free。

### 複合字面量（Composite literals）

值在運算式位置**建構**——正是 group 6 那些拆解它們的 pattern 的鏡像:

```text
tuple-lit ::= '(' expr ',' expr ( ',' expr )* ')'    # (a, b)——2+ 元素
list-lit  ::= '[' ( expr ( ',' expr )* )? ']'        # [1, 2, 3];空 []
map-lit   ::= '{' map-entry ( ',' map-entry )* '}'   # {k: v, …}
          | '{' ':' '}'                               # 空 map {:}
map-entry ::= expr ':' expr
```

- **tuple `(a, b)`**——括號內 2+ 元素;單一 `(expr)` 只是分組,故無 1-tuple、無空 `()`。這正是讓 `divmod` 能
  `return (q, r)` 的關鍵。以**靜態索引**讀回元素——`t.0`、`t.1`——而非 `t[i]`:tuple 是異質的,索引須為編譯期常數
  才能得知元素型別（`a.0.1` 是 `(a.0).1`,絕非 float）。**沒有 tuple struct**——`type P = (A, B)` 是具名位置式型別,
  `struct` 則是具名欄位式。
- **list `[1, 2, 3]`**(空 `[]`),有序。在型別為 `[T; N]` 的 context 下,長度吻合的 list 字面量會 **coerce** 成陣列。
- **map `{k: v}`**(空 `{:}`)。`:` 正是分辨 `{…}` 是 **map** 還是 **block** 的依據——`k: v` 不是 statement,所以帶
  冒號的大括號無歧義;而**裸元素**的 `{…}` **恆為 block**。
- **set `set([1, 2, 3])`**(空 `set()`)——走建構子,**不用** brace 字面量,因為 `{1}` 會和單句 block 分不清。`set`
  的參數預設為 `[]`,所以 `set()` 就是空 set。
- 兩條規則源自「`{` 是 block 開場」:**statement 開頭**的 `{` 是 block statement(值被丟棄);**任何以 `{` 開頭的
  運算式**——block 或 map 字面量——在 **`if`/`for`/`with`/`match` head 開頭**都要加括號。

### 字串插值與 `print`

**f-string** 是一種 primary expression——帶 `{ expr }` 洞的字串：

```text
fstr-lit    ::= 'f' '"' ( fstr-char | escape | '{{' | '}}' | interp )* '"'
interp      ::= '{' expr '='? conversion? format-spec? '}'
conversion  ::= '!' ( 'r' | 's' | 'a' )     # r = debug、s = display、a = ascii
format-spec ::= ':' fmt-char*               # 由型別的 Format protocol 解讀
print       ::= 'print' expr
```

洞是 **Python 式**：`expr`，其後選用 `=`、`!` 轉換、`:` format spec。`f"sum={x + y}"` 把每個洞經 `display()`
算出並串接——**編譯期 desugar** 成 `str` 串接，沒有 runtime format engine。

- **`{x}`** → `x.display()`。**`{x!r}`** / **`{x!s}`** / **`{x!a}`** 先轉換——`debug` / `display` / ascii。
  **`{x=}`** 自述：輸出運算式原文與 `=`，再接值（`f"{n=}"` → `n=42`）。
- **`{x:spec}`** 把 `spec` 交給型別的 **`Format`** protocol——`f"{pi:.2f}"`、`f"{n:04d}"`、`f"{p:>10}"`。spec
  **字串的意義由型別決定**（stdlib 數字/`str` 讀常見的 fill/align/sign/`#`/`0`/width/`.precision`/type）；文法
  只當它是到 `}` 為止的不透明字串。
- 純 `"…"` 是 literal（大括號是普通字元）；只有 f-string 會讀 `{…}`，而 `{{` / `}}` 寫出字面大括號。

**`print`** 把一個值的 `display()` 加換行寫到 stdout——保留字、恆在 scope、best-effort（永不 raise），所以
`print f"hello {name}"` 是最小程式。

## Group 5 — Functions

function 是 first-class value——具名宣告、匿名 expression，與一個型別：

```text
fn-decl    ::= 'pub'? 'unsafe'? 'mut'? 'fn' identifier generics? '(' param-list? ')' ret-type? block
fn-expr    ::= 'fn' '(' param-list? ')' ret-type? block          # 匿名——永不泛型、永不 unsafe
fn-type    ::= 'unsafe'? 'fn' '(' param-type-list? ')' ret-type?
ret-type   ::= '->' type
return     ::= 'return' expr?
param      ::= ( 'mut' '&' )? identifier ':' type ( '=' expr )?
param-type ::= ( 'mut' '&' )? type
```

- **宣告 vs expression。** `fn name(…) -> R { … }` 綁定一個名字；**匿名** `fn(…) -> R { … }` 是 expression
  （一個 closure）。`pub` 匯出宣告的名字，且不屬型別。
- **closure 捕獲是 immutable 的。** closure 以**複製、唯讀**捕獲值與 channel——**不能改**捕獲的變數。這是刻意的:
  value 語意下改捕獲對外本就不可見、無 GC 的 by-ref 捕獲會懸空、而 immutable 捕獲正是讓 `spawn` 的 closure
  **無 data race** 的原因。可變 closure 想做的三件事各有對應慣用法:用 `for` 迴圈**累加**
  （`for x in xs { sum = sum + x }`）、把**狀態**放進帶 `mut fn` 的 `struct`、用 `chan` 傳遞**並行**狀態。
- **Return。** `return expr` 帶值離開，單獨 `return` 則不帶值。**省略 `-> type`** 表示函式回傳 `nil`。
- **參數。** 參數**以值傳遞**（copy），可帶**預設值** `= expr`。call 端的 **named argument** 是 `name: value`
  （group 4 的 `arg` 形式）：positional 參數在前，之後任一個可具名，一旦具名其餘也須具名——這正是能跳過有預設值
  參數的方式。
- **`mut &`——可變參考。** `mut &x` 傳一個**可變參考**:callee 可改 `x`,且改動**影響 caller 的引數**。兩個控制相遇——
  **caller** 決定自己的變數是否 `mut`,**callee** 用 `mut &` 決定是否寫回——所以可見的變更需要**兩者兼具**,且引數須為
  `mut` lvalue。**呼叫端不加標記**:簽名就是契約。`mut &` 參考只在該次呼叫期間有效——不可**逃逸**（被 `spawn` 捕獲或
  存活得比呼叫久）、不可**別名**（同一次呼叫把同一變數傳給兩個 `mut &`），因而無需 borrow checker 即安全。**沒有純
  `mut x` 參數**;要本地可變 copy 就遮蔽——`mut x := x`。`mut fn` 正是方法 receiver 的 `mut &this` 情形。
- **引數慣例。** 名字在**編譯期**對照簽名解析,所以 `map` **不能** splat 成 named argument（runtime 字串 key、
  同質值型別,對上 compile-time 異質參數）。也**沒有 variadic**。多個位置引數傳 **`list`**;一包具名選項用帶 field
  預設的 **options struct**——`draw(Style(width: 2))`,即靜態版的 keyword arguments。`map` 以普通值傳遞,絕不展開
  成呼叫的具名參數。
- **型別。** function 的型別是 `fn(P…) -> R`——參數（含 `mut &`）與結果，別無其他；預設值與參數名字存在宣告裡，不在
  型別中。
- **`mut fn`（mutating method）。** `mut fn` 標記一個**方法**會就地修改其隱式 receiver `this`；呼叫端的 receiver
  須為 `mut` binding。它只在 `impl` 或 `spec` 內有意義——free function 或 closure 沒有 receiver。`mut fn` 追蹤的是
  值**自身 field** 的變動，不含對 `Ref[T]`／foreign handle 背後資源的 effect（那不需 `mut`——見 group 7）。
- **泛型。** `fn`（及 `spec` 方法）可帶型別參數——`fn max[T: Ord](a: T, b: T) -> T`——參數可加 spec **bound**;完整
  泛型文法與 monomorphization 模型見 group 7。**匿名** `fn(…)` 永不泛型(需要型別參數就包成具名 `fn`)。

## Group 6 — Control flow & Pattern matching

`for` 是 statement；`match` 是 expression；`if` 兩者皆是（有 `else` 時為 expression）：

```text
block       ::= '{' stmt-list '}'                     # 裸 block 開一層 nested scope
with-stmt   ::= 'with' expr ( 'as' identifier )? block
if-stmt     ::= 'if' if-head block ( 'else' 'if' if-head block )* ( 'else' block )?
if-expr     ::= 'if' if-head block ( 'else' 'if' if-head block )* 'else' block   # else 必要
if-head     ::= expr | identifier ':=' expr
for-stmt    ::= 'for' block | 'for' 'mut'? identifier 'in' expr block | 'for' expr block
break       ::= 'break' ( 'if' expr )?
continue    ::= 'continue' ( 'if' expr )?
match-expr  ::= 'match' expr '{' match-arm+ '}'
match-arm   ::= ( pattern | range-arm ) ( 'if' expr )? '->' expr   # 選用 guard
range-arm   ::= range-bound ( '..' range-bound? | '..=' range-bound )   # sugar：'_ if _ in <range>'
range-bound ::= '-'? literal | identifier
pattern       ::= sub-pattern ( '|' sub-pattern )*
sub-pattern   ::= pattern-core ( 'as' identifier )?
pattern-core  ::= variant-pat | struct-pat | tuple-pat | list-pat | literal-pat | binding-pat | '_'
struct-pat    ::= type-name '{' struct-fields? '}'
struct-fields ::= field-pat ( ',' field-pat )* ( ',' '..' )? | '..'
list-pat      ::= '[' ( list-pat-elem ( ',' list-pat-elem )* )? ']'   # 至多一個 '..'
list-pat-elem ::= pattern | '..' identifier?
```

- **Block 與 `with`。** 裸 `{ … }` 開一層 **nested scope**——其 binding 與 scope-owned 值在 `}` 釋放。block 同時
  是一種 **expression**（`primary`，group 4）:它的**值 = 最後一個 statement 的值**——expr-statement 給出其 expr;
  其他 statement 或空 block 給出 `nil`。ASI `;` 只**分隔**、不丟棄值,所以 `guard { … }` 與多敘述的 `match` arm
  （`P -> { …; v }`）都能產出。**`with expr as y { … }`** 是裸 block 的 sugar：把 scoped 資源 `y` 綁進 block,並保證
  資源的 **teardown 在每條離開路徑**（正常、`return`、abort）都跑。該值實作內建 **`Scoped`** spec（其唯一方法即
  teardown）；`Ref[T]` 的 drop 已滿足它。所以 `with open(p) as f { f.read() }` ≈
  `{ f := open(p); defer f.<teardown>; … }`。當資源只為其 scope 而用（如持有的 lock），`as y` 可省。
- **`if`。** 條件是 `bool`——沒有 truthiness。**binding head** `if x := expr { … }` 只在 `expr` 存在時執行區塊
  （one-arm-`match` 的 sugar）。`else if` / `else` 照常串接。帶**必要 `else`** 的 `if` 同時是一個**運算式**
  （`x := if c { a } else { b }`）——產出被選中分支的 block 值,且每個分支須同型別;statement 位置則以 statement
  形式為準（值被丟棄）。
- **`for`。** 唯一的迴圈，三種形式：**`for { … }`** 無限（以 `break` / `return` 離開）、**`for cond { … }`** 當
  `cond`（`bool`）成立時重複、與 **`for x in it { … }`** 走訪 `Iterable`，`x` 以 copy 綁定（**`for mut x`** 就地
  綁定）。有 `mut` 或 `identifier in` 接在 `for` 後就是 iterate 形式;裸 `for expr` 則是 while 條件。沒有 C 式三段
  `for`。**`break` / `continue`** 作用於最近的迴圈；**`break if c`** 與 **`continue if c`** 是
  `if c { break }` / `if c { continue }`
  的 sugar。**沒有 loop label**——要退出外層迴圈，抽成函式並 `return`（或用 flag 搭配 `break if`）。
- **`match`。** 一個 expression：依序比對各 arm，取第一個吻合並產出，且每個 arm 產出**同一型別**——所以 `match`
  可用於 `:=`、`return` 或引數。結尾的 **`_`** 涵蓋其餘。
- **Pattern** 以 copy 解構：帶 payload 綁定的 **variant**（`Left(v)`、巢狀 `Left(Some(v))`）、**struct**
  （`Div{q, r}`）、**tuple**（`(a, b)`）、**literal**（可帶負號 `-1`,以 `equal` 比對）、單純的**綁定**名字、**or-pattern**
  （`A | B`，兩側綁同名）、或萬用字元 **`_`**。tuple 或 struct pattern 也能在 `:=` 綁定處解構——
  `(q, r) := divmod(x, y)`。
- **Guard。** arm 可在 pattern 後加 `if expr`（`Some(v) if v > 0 -> …`）——一個能看見 pattern 綁定、且必須成立的
  條件;`A | B if c` 時涵蓋整個 or-pattern。帶 guard 的 arm **不計入 exhaustiveness**（compiler 無法證明 guard 成
  立），所以該 case 仍需一個無 guard 的 arm（或 `_`）。
- **Range arm。** **僅 match 專用**的 `200..300 ->` / `400..=499 ->` / `500.. ->` 是 guard 的**語法糖**——
  `_ if _ in <range>`——所以以 **containment**（`..` 運算子）比對,非 `equal`,並繼承 guard 的 exhaustiveness（不算
  涵蓋）。它**不綁定**;要用該值就寫顯式 `x if x in <range>`。bound 為編譯期常數;range arm 由其 `..` 辨識。
- **Rest 與部分。** **struct pattern 必須列全 field**,否則以 `..` 結尾——`Div{q, r}`（全）、`Div{q, ..}`（略過
  其餘）、`Div{..}`（任意）。預設列全表示加一個 field 會**弄壞**舊 pattern,逼你檢視。**list pattern** 比對 list——
  `[a, b]`、`[head, ..tail]`、`[..init, last]`、`[]`——**至多一個 `..`**;`..name` 把略過的一段綁成 list,裸 `..`
  丟棄（struct 的 `..` 只忽略、不綁）。pattern 位置的 `..` 是 **rest**,與值層的 range `..` 不同。
- **variant 或 binding** 由 **name resolution** 決定:裸名字在 scope 內解析到已知 type/variant 就是 variant,否則是
  新的 binding。名字**大小寫自由**,所以靠解析、非大小寫——與 postfix `[…]` 用的是同一套 name resolution（group 4）。
- **`as` 綁定。** `pattern as name` 在 pattern 繼續解構的同時,把**整個**被比對的值綁到 `name`——`Move{x, y} as m`、
  `[first, ..] as all`、巢狀 `Some(inner as v)`。讀法同 `with` / `import`:`<東西> as <名字>`。在 or-pattern 上,`as`
  綁最近的 alternative（`A | B as m` 即 `A | (B as m)`）;兩側都要綁就寫 `A as m | B as m`。

## Group 7 — Types & Declarations

自 group 5 起被引用的 type 表達式，以及引入型別與行為的宣告：

```text
type        ::= base-type '?'?
base-type   ::= type-name type-args? ( '.' identifier )*   # 'I.Item' 投影;可鏈式 'I.Item.Sub'
              | tuple-type | array-type | chan-type | fn-type | ptr-type   # ptr-type：group 12（unsafe）
type-args   ::= '[' type ( ',' type )* ']'
array-type  ::= '[' type ';' const-expr ']'   # N 是 const-expr
struct-decl ::= 'pub'? 'struct' identifier generics? '{' field-list? '}'
enum-decl   ::= 'pub'? 'enum' identifier generics? '{' variant-list? '}'
type-decl   ::= 'pub'? 'type' identifier generics? '=' type
const-decl  ::= 'pub'? 'const' identifier ( ':' type )? '=' const-expr
const-expr  ::= expr                          # 可於編譯期摺疊（語意限制）
spec-decl   ::= 'pub'? 'spec' identifier generics? ( ':' bound )? '{' spec-member* '}'
impl-decl   ::= 'impl' generics? type-name type-args? 'for' type '{' impl-item* '}'  # spec impl
              | 'impl' generics? type '{' impl-item* '}'                             # inherent
impl-item   ::= fn-decl | assoc-bind | const-decl   # 方法／關聯函式、assoc type 綁定，或 assoc const
assoc-bind  ::= 'type' identifier '=' type    # 'type Item = int' 滿足 spec 的 assoc type
spec-member ::= fn-sig | fn-decl | assoc-type | assoc-const
assoc-type  ::= 'type' identifier ( ':' bound )?   # 'type Item'（可加 bound）
assoc-const ::= 'const' identifier ':' type ( '=' const-expr )?   # 必要，或帶預設值
generics    ::= '[' type-param ( ',' type-param )* ']'
type-param  ::= identifier ( ':' bound )?     # 選用的 spec bound
bound       ::= type-name ( '+' type-name )*  # spec 的合取
decorated-decl ::= decorator* declaration   # decorator 前綴可領任何宣告（group 1）
decorator   ::= '#[' deco-item ( ',' deco-item )* ']'
deco-item   ::= identifier ( '(' deco-arg ( ',' deco-arg )* ')' )?
deco-arg    ::= type-name | const-expr        # derive(Encode, Decode)、align(16)、align(SIZE*2)
```

- **Type 表達式。** 一個**名字**加選用**型別引數**（`int`、`User`、`list[int]`、`Either[A, B]`）；一個 **tuple
  type** `(A, B)`；一個**陣列** `[T; N]`（`;` 的另一用途）；一個**通道** `chan[T]`，帶 Go 式方向——`<-chan[T]`
  為 receive-only（receiver）、`chan[T]<-` 為 send-only（sender）；或一個**函式型別** `fn(P…) -> R`
  （group 5）。結尾的 **`?`** 使任何型別成為 **optional**——`str?`。型別名只是 identifier,所以內建**數值**型別集合
  （`int`、`uint`、`float`,以及任何固定寬度 `i32`/`u8`/`f64`/…）是 **stdlib** 的決定,非文法。
- **`struct` / `enum`。** 具名、定型 field 的**乘積**，或每個 variant 帶選用 payload 的**和**——`Circle(float)`、
  `Rect(float, float)`。field 與 variant 的分隔**完全比照 statement**（換行，或單行用 `;`）——兩者之間**沒有
  `,`**；payload `(A, B)` 內的 `,` 才是一般清單。兩者皆可泛型——`enum Either[X, Y] { … }`。
- **Enum 判別值。** 當**每個** variant 皆無 payload 時,variant 可帶顯式整數**判別值**——`enum Status { Ok = 200;
NotFound = 404 }`——一個 C-style enum,值可觀察（`int(Status.Ok)` 取值、`Status.from(200) -> Status?` 反轉）。
  值為編譯期常數、彼此相異;未指定者 = 前一個 `+ 1`（從 `0` 起）。**payload** enum 的 tag 維持 **opaque**（只能
  match），不得帶判別值。底層由一條預設規則為 `int`——特定寬度是 opt-in 的 layout decorator（`#[repr]`），而
  **wire 格式是 `Encode`/`Decode` impl**,絕非 decorator。
- **Field 可見性與預設值。** field 加 `pub` 開放外部 **instance 存取**（讀/寫 `u.field`）;非 `pub` field 為模組
  私有,且**必須有 default**。**沒有 zero value**——非 optional 且無 default 的 field **建構時必填**;唯一的隱含
  default 是 `T?` field 的 `nil`（其自然的「不存在」狀態）。故 `struct Config { host: str; port: int? = 8080;
tags: str? }` → `Config(host: "x")` 得 `port = 8080`、`tags = nil`,而省略 `host` 為錯。
- **建構（Construction）。** 型別名同時是它的**建構子**——`User(id: 1, name: "x")` 建一個 struct（field 依宣告順序
  成為參數，positional 先、named 後）、`Circle(3.0)` 建一個 enum variant。這個名字與 function **共用 value 命名
  空間**,所以型別和 function 不能同名（Zerg 無 overloading）。field-wise `T(...)` 是**公開且基元**;自訂 constructor
  是具名關聯函式（inherent `impl`）,內部經 `T(...)` 建。**`#[sealed]`** 把 `T(...)` 降為模組私有,外部須改走公開的
  自訂 constructor——模組內部仍以 `T(...)` 建。
- **`type X = Y`。** 一個**強 typedef**——全新、獨立的型別，非透明別名；可泛型。
- **編譯期常數（`const`）。** `const NAME: T = expr` 命名一個**編譯期摺疊**、**無 runtime 儲存**的值——與 module-level
  `:=`（不可變但於 init 求值）不同。`const` 可用於任何需要編譯期值之處——陣列長度 `[T; N]`、enum discriminant、
  decorator 引數——**也可當作一般值**（傳遞、比較、以 equality 於 pattern 比對）。型別可選；裸數值 const 維持 untyped
  並採用其使用點型別。它**非 lvalue**（不可 `=`、不可 `del`），且**雙向 shadow-proof**——無綁定可遮蔽 `const`，
  `const` 亦不可遮蔽既有可見綁定，故其名字在整個 scope 內只代表一個值。`const-expr` 是受限的 `expr`：literal、其他
  const、discriminant 與運算子；**尚無 function call**（故 `sizeof`/`len` 不是 const-expr）。`const` 可宣告於 module
  層級、局部，或在 `spec`/`impl` 作為 **associated const**（`const BITS: int`），即 associated type 的值對應物。
- **泛型與 bound。** 參數列 `[T, …]` 可對各參數加 spec 約束——`[T: Ord]`、`+` 合取 `[K: Hash + Eq, V]`。同一套
  `bound` 也是 spec 的 **super-spec**(`spec Ord: Eq`——`impl Ord` 便連帶需要 `impl Eq`,且 `Ord` body 可對 `This`
  呼叫 `Eq`)。`impl` 自身的型別參數放在 **`impl` 之後**——`impl[T] Summable for list[T]`——故 `T` 可用於目標型別。
  泛型 **monomorphize**:每個相異型別引數各生一份特化的 C 函式,所以 bound 是承重的(它指名要特化的 impl,泛型碼裡
  `a < b` 也需提供 `<` 的那個 bound),且泛型函式在實例化前不是一等值。健全性靠 **coherence**(全程式一個
  `impl Spec for Type`)與 **orphan rule**(須擁有 spec 或 type 之一);泛型一律 **invariant**。`#[dyn]` 改為產生
  一份共享的 witness-table body(以 size 換 speed),compiler 也能標出實例化膨脹。呼叫端顯式型別引數寫作 `f[T]`（靠
  name resolution 與索引區分——group 4）;const generic 延後。**沒有 disjunction bound**（`T: A | B`）——body 無從
  得知 `T` 有哪些方法,無法 monomorphize。要接受多種型別就**參數化一個 spec**、一型別一 impl:`spec Indexable[K]`
  搭配 `impl Indexable[int]`（元素）與 `impl Indexable[Range]`（slice）——`xs[k]` 便依 `k` 的型別靜態分派,各 impl
  保有自己的 associated `Output`——或用 `enum` 做 runtime 選擇。
- **`spec`。** 行為介面：成員為**必要**（只有簽名、無 body）、**提供**（完整方法）,或 **associated type**
  （`type Item`——由 `impl` 填入、函數性決定、每個 impl 一個）。方法**不宣告 receiver**——`this` 在方法內為隱式,
  透過被呼叫的 instance 取得；若 `fn` 用到 `this` 卻無 instance 綁定則為編譯錯誤。self 型別是 **`This`**。
  `impl … for …` 由手寫為某型別提供 spec 的方法(及其 `type Item = …` 綁定)。
- **Inherent `impl`。** 無 `for` 的 `impl User { … }` 加入**不綁任何 spec** 的方法——named constructor
  `User.from_json(…)`（關聯函式,不用 `this`,以 `Type.f(…)` 呼叫）或私有方法 `u.recompute()`（用 `this`,以
  `x.f(…)` 呼叫）。一個型別上所有方法/關聯函式共用一個命名空間,不論 inherent 或來自 spec,**重名即錯**。
- **Associated type。** 它讓**單輸出**的 protocol 良定義:`for x in it` 只有一種元素型別,因為 `Iterator` 的 `Item`
  由 impl 固定,而非像 generic `Iterable[T]` 那樣由使用端選。用型別位置的**投影**引用——`I.Item`
  （`fn collect[I: Iterator](it: I) -> list[I.Item]`）,當被投影型別本身有 associated type 時**可鏈式**——
  `I.Item.Sub`。impl 以 `type Item = int` 提供它。spec 的 associated
  **const** 是其值對應物——`const BITS: int` 為必要，由 impl 以 `const BITS = 32` 提供。
- **Decorator 與 `#[derive(…)]`。** **decorator** `#[…]` 是 compiler 指令；其 `decorator*` 前綴可領**任何宣告**
  （`decorated-decl`，group 1）並綁定之。哪個 decorator 能用在哪種宣告是**語意**規則——`struct`/`enum` 上的
  `#[derive(Encode, Decode)]` 請 compiler 讀該型別的**結構**、**生成**所列 spec 的 canonical impl（見
  [Derive & Default Behavior](derive.md)）；logging decorator 則會掛在 `fn` 上。decorator 是**固定、compiler
  擁有**的集合——使用者不可自訂（Zerg 無 macro）；其他指令（layout、FFI…）日後於此加入。`#[` 是唯一不算註解的
  `#`——lexer peek 一字元即分辨。

## Group 8 — Null-safety & Errors

失敗分**兩層**。**可回復**失敗是 sum type 的普通值——`Either[X, Y]`、`Result[T]` = `Either[T, Err]`、以及
`T?` = `Either[T, nil]`（placeholder 為 `nil`）。**bug** 是**abort**,會 unwind stack（跑 `defer`）。六個運算子在兩層
間搭橋:

```text
coalesce-expr ::= or-expr ( '??' coalesce-rhs )?
coalesce-rhs  ::= coalesce-expr | diverge
diverge       ::= 'break' | 'continue' | return | raise
raise         ::= 'raise' expr ( 'from' expr )?
guard-expr    ::= 'guard' block
postfix       += '?' | '!' | '?.' identifier
```

- **`x?`**——**propagate**:取出 `Left`,否則從所在函式**提前 return** 那個 `Right`。
- **`a ?? b`**——**default**:`a` 有 `Left` 就用它,否則用 `b`;最鬆的二元、**右結合**、短路。右側也可改為**diverge**——
  `x ?? break`、`v ?? return nil`、`p ?? raise e`——因為 `break` / `continue` / `return` / `raise` 從不產值。
- **`a?.b`**——**optional chain**（僅 `T?`）:`a` 存在時讀 `.b`,否則就地短路成 `nil`(不像 `?` 會從函式 return)。
- **`x!`**——**force-unwrap**:取出 `Left`,否則 **raise `UnwrapError`**（value→abort 的逃生口）。邏輯否定是關鍵字
  `not`,所以 postfix `!` 空著可用。
- **`raise e`**——攜帶 `Err` 的 **abort**(value→abort);**`raise e from c`** 把 `c` 記為 `e` 的 cause。
- **`guard { … }`**——把區塊內任何 abort **降級**回值,產出 `Result[T]`(abort→value)。它是從 abort 層回來的唯一途徑,
  guard 過的 abort 就是普通 `Result`,用同一套 `?` / `??` / `match` 處理。

## Group 9 — Concurrency

並行**只有 coroutine + channel**（CSP）——無共享可變狀態、無 lock、無 join/handle。

```text
spawn-stmt  ::= 'spawn' expr
send-stmt   ::= expr '<-' expr
chan-new    ::= 'chan' '[' type ']' '(' expr? ')'
recv-base   ::= '<-' recv-base | primary
select-stmt ::= 'select' '{' select-arm+ '}'
select-arm  ::= recv-arm | send-arm | 'done' '->' expr | '_' '->' expr
recv-arm    ::= ( ( identifier | '_' ) ':=' )? '<-' expr '->' expr
send-arm    ::= expr '<-' expr '->' expr
```

- **`spawn f(args)`** 啟動一個 **fire-and-forget** coroutine（Go 的 `go`）——無 handle、無 join;只能透過 channel
  觀察。capture 限 immutable 值與 channel。
- **`chan[T](cap?)`** 建立 channel——容量 `0`（預設）是無緩衝 **rendezvous**。裸 `chan[T]` 為雙向,可窄化成
  `<-chan[T]` / `chan[T]<-`（或用值 `ch.recv` / `ch.send`）。
- **`ch <- v`** 送出（無值;對已關閉 channel 會 abort）。**`<-ch`** 接收,產出 `Result[T]`——`Right` 表示已關閉且排空
  （攜 `StopIteration` 或 crash `Err`）。receive 先綁定,故 `(<-ch)?`、`<-ch!`、`<-ch ?? d` 與 group-8 運算子組合。
- **`select { … }`** 是唯一的多路等待:跑第一個 ready 的 arm（公平 tie）。**`done`** 在所有被監看的 receive channel
  都關閉時觸發一次;**`_`** 在此刻無 arm ready 時觸發（non-blocking）——兩者皆**contextual**,只在 select-arm 開頭
  特殊。**沒有明確 close**（channel 在最後 sender 離開時自動關）、**沒有 `yield`**。

## Group 10 — Modules & Programs

源碼巢狀為 **program › package › module（一個目錄）› file**。

```text
import-stmt ::= 'pub'? 'import' import-path ( 'as' identifier )?
import-path ::= identifier ( '/' identifier )*
init-decl   ::= 'init' '(' ')' block
```

- **`pub`**（已是每個宣告的前綴）是唯一的可見性標記：普通宣告是 **module-private**,`pub` 對**同 package 其餘**
  公開,而 package 的公開 API 是其**根 module** 的 `pub` 表面。
- **`import path`** 綁定一個 **namespace**——路徑的**末段**（`import util/text` 綁 `text`）,以 `.` 存取:
  `text.split(…)`。**`as`** 改名（`import a/text as at`）,兩個末段同名的 import 便靠它並存;與本地名字衝突即錯,
  用 `as` 解。**沒有 selective（`from … import`）或 glob import**——要 unqualified 使用某成員,就本地綁定
  （`split := text.split`）,因為函式是值。**`pub import`** 把該 namespace **re-export** 到本 module 表面——根
  module 用來組出 package 公開 API 的唯一機制。
- **`init()`** 是 module 的**惰性**初始化,首次使用時執行。一個 module 可宣告**多個** `init()`;它們依**宣告
  （FIFO）順序**執行,每個**恰好一次**。**safe 程式碼無可變全域**:頂層 binding 不可 `mut`——頂層 `:=` 是**不可變
  的模組常數**,於 init 時求值。唯一例外是 **`unsafe mut`** 全域(group 12);要安全共享可變全域狀態,用不可變 `:=`
  持有 stdlib **`Atomic[T]`**。
- **program** 是以入口檔為根的 build,其入口檔定義頂層 `fn main(…) -> Result[nil]`;`main` 是普通函式(非保留)。
  **`package`** 是 distribution/versioning 單位——由 build tool 選定的目錄樹,**無 in-source `package` 宣告**。

## Group 11 — Resource cleanup（`defer` 與 `del`）

三個構造共用一條軸——清理**何時**觸發。

```text
defer-stmt ::= 'defer' expr
del-stmt   ::= 'del' identifier
```

- **`defer expr`** 在**所在 block 退出**時執行 `expr`,**每一條離開路徑**都跑——正常、`return`、或 abort unwind——
  以後登記先跑的順序。它是「綁在 scope 上副作用」的 procedural 工具（放鎖、flush buffer、關 scope-local 資源）。
- **`del name`** **當下**撤銷該名字對其儲存的存取;唯有被撤銷的是**擁有權**存取且無其他 holder 時才釋放儲存。對
  `Ref[T]` / `chan` 則是放掉一個 refcount——**`del ch`** 若你是最後 sender 便關閉 channel。
- 軸上第三點——**`Ref[T]` drop** 於最後 holder 的 scope 退出時——不是 statement,由 scope ownership 掉出來。分界是
  `defer` vs `Ref[T]`:資源會逃出其 scope 嗎?不會 → `defer`;會 → `Ref[T]`。

## Group 12 — Unsafe（raw pointer 與 inline assembly）

通往裸機的唯一門。這裡的一切**只在 `unsafe` 內合法**；安全世界（`Ref[T]`、`mut &`、無 mutable globals、受檢
`T?`）不受影響。

```text
unsafe-expr ::= 'unsafe' block             # block-expression；unsafe 操作僅此合法
global-mut  ::= 'unsafe' 'mut' identifier ( ':' type )? ':=' expr   # module-level 可變全域
fn-decl     ::= 'pub'? 'unsafe'? 'mut'? 'fn' …    # 'unsafe fn'——整個 body 皆 unsafe
ptr-type    ::= 'ptr' ( '[' type ']' )?    # 'ptr' = 原始位址；'ptr[T]' = 具型別指標
asm-expr    ::= 'asm' '(' str-lit ( ',' asm-operand )* ')'
asm-operand ::= 'in' '(' str-lit ')' expr | 'out' '(' str-lit ')' lvalue
              | 'inout' '(' str-lit ')' lvalue | 'clobber' '(' str-lit ( ',' str-lit )* ')'
```

- **`unsafe { … }`。** 一個 **block-expression**（yields 區塊值），其內 raw 操作合法；其外皆為編譯錯誤。`unsafe`
  是**信任邊界**——編譯器對其內容不作記憶體安全保證，由作者背書。**`unsafe fn`** 整個 body 皆 unsafe，且只能從
  另一個 `unsafe` context **呼叫**。
- **全域可變狀態（`unsafe mut`）。** _無可變全域_（group 10）的唯一例外——`unsafe mut NAME := …` 宣告一個
  module-level 可變變數，即裸機逃生口（page table、allocator cursor）。無 `static` 關鍵字；`unsafe` 前綴即標記。它是
  **module-private**（不可 `pub`），故一切存取都經其 module 的函式，且宣告與每次讀寫都只在 `unsafe` 內合法。優先用
  **安全**替代——不可變 `:=` 持有 stdlib **`Atomic[T]`**——跨核共享可變全域而無需 `unsafe`（綁定不可變、Atomic 內部可變）。
  **atomics 是 stdlib、非文法**：`Atomic[T]` 提供 `load` / `store` / `fetch_add` / `compare_exchange` 與 memory-ordering 參數。
- **Raw pointer（`ptr` / `ptr[T]`）。** `ptr` 是平台字寬的原始**位址**（C 的 `void*` / `uintptr`）；`ptr[T]` 把該
  位址定型到 pointee `T`（同寬——`[T]` 只為 load/store/offset 提供型別）。因 `T` 為任意型別，**函式指標**免費得到
  ——`ptr[fn(int) -> nil]`（interrupt vector）——`ptr[ptr[T]]` 與裸 `ptr` 亦然。`ptr` **本就可空**（位址 `0`）且與
  `T?` **正交**——**無 `ptr[T]?`**；以 `p == 0` 檢查。**型別**可出現在簽名/欄位（描述指標形狀資料），但每個**操作**
  ——`addr(x)`、`p.load()`、`p.store(v)`、`p.offset(n)`、cast、volatile/atomic——都是 **unsafe stdlib intrinsic**
  （非文法），只在 `unsafe` 內合法。**無 `*`/`&` 運算子**；中括號型別讓指標與 `list[T]` / `Ref[T]` / `chan[T]` 一致。
- **Inline assembly（`asm`）。** 一個 template 字串（多行可用三引號）加上把 Zerg 值綁到暫存器/constraint 的 operand。
  `out` / `inout` / `clobber` 為 **contextual**（只在 asm operand list 內特殊；`in` 本就是 keyword）。constraint 字串
  （`"rax"`、`"r"`、`"m"` …）在此不透明——其意義屬目標 backend。`asm` 僅限 `unsafe`；**syscall** 由此發出。

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
