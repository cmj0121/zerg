# The Zerg bootstrap seed

[English](README.md) | 繁體中文

一個以 Go 實作的 Zerg 編譯器，如今唯一的職責是**建置 `src/compiler/` 裡的自舉編譯器**。它是種子，不是產品：當
`zerg` 能編譯自己之後，使用者工具鏈該做的事（`fmt`、`lint`、`test`、值得一讀的診斷訊息）都屬於那個以 Zerg 寫成
的編譯器，種子只保留第一次建置所需的部分。

這個唯一目的就是設計準則。種子只支援 `Zerg-boot` 子集——自舉原始碼實際用到的那一小片語言——別無其他。超出子集的
程式會以一行訊息與非零 exit code 被拒絕；它絕不會被靜默誤編譯。

## 用法

```sh
zerg0 build <file.zg>              # 編譯並連結成執行檔（預設 --emit bin）
zerg0 build --emit c <file.zg>     # 只到產生 C translation unit 為止
zerg0 build -o out --keep-c f.zg   # 指定輸出路徑，並保留產生的 .c
```

`build` 是唯一的子命令。失敗時在 stderr 印出 `file:line:col: message` 並以非零值結束——足以定位問題，不多說。

## 自舉鏈

```text
make build                        # 或者手動跑它的三個步驟：
go build -o bin/zerg0 ./cmd/zerg  # 1. Go 種子——叫 zerg0，不叫 zerg
zerg0 build src/compiler/zergc.zg -o bin/.zerg-stage1
                                  # 2. 種子建出一個「中繼」編譯器
zerg build --emit bin -o bin/zerg src/compiler/zergc.zg
                                  # 3. 由中繼建出實際出貨的 zerg
```

出貨的編譯器是由「以 Zerg 寫成的編譯器」建出的，不是由種子——種子只需要造出一個夠好的中繼。這讓種子離開
交付路徑，也讓每一次建置都走過自舉路徑。在那之後，種子只在「機器上還沒有 `zerg`」時，才需要重新推導出它。

## 種子支援什麼

以下每一項都被自舉原始碼（`src/compiler/zergc.zg`、`src/compiler/zerg/*.zg`）或它匯入的 stdlib 模組（`io`、
`ascii`、`strconv`、`cli`）實際用到——這就是它們還在的理由。

| 特性                            | 說明                                                  |
| ------------------------------- | ----------------------------------------------------- |
| value struct                    | 宣告、建構、欄位存取、巢狀                            |
| enum——plain 與 payload          | tagged union；變體以**裸名**建構                      |
| 遞迴 enum                       | 自我指涉的 payload，自動 boxing（`Expr`、`Stmt`）     |
| `match`                         | 運算式 arm、以換行分隔，支援解構                      |
| `list[T]`                       | `append` / `len` / `x[i]` 讀寫、`for … in`            |
| `str` 與 `byte`                 | 串接、`str(bytes)` / `list[byte](str)`、f-string      |
| `Result[nil]`、`guard`、`raise` | driver 與 `strconv` 使用的錯誤路徑                    |
| `mut &` 參數                    | by-reference 接收者（`fn at(mut &l: Lex, …)`）        |
| 泛型                            | 編譯期單態化                                          |
| tuple、optional、`defer`        | 保留：成本低，且從子集內仍可觸及                      |
| `spec` / `impl` method          | 靜態分派                                              |
| 模組                            | `import "path"`、模組限定呼叫、whole-program 攤平     |
| `__zrt_*` intrinsic             | runtime 底層，含 `__zrt_exec`（`zerg` 藉此執行 `cc`） |

## 種子能 lower 的文法

`Zerg-boot` 以 W3C-style EBNF 表示，沿用完整 [`GRAMMAR`](../../GRAMMAR) 的標記法與 production 名稱。這是**後端
視角**：C emitter 有對應 lowering 的部分。前端解析得比這更多——下面列的才是真正抵達 C 的。

```ebnf
program        ::= stmt-list
statement      ::= simple-stmt | compound-stmt | decorated-decl
simple-stmt    ::= nop | binding | reassign | print | return | raise | break | continue
                 | del | defer | expr-stmt
compound-stmt  ::= block | if-stmt | for-stmt | with-stmt
decorated-decl ::= decorator* declaration
declaration    ::= fn-decl | struct-decl | enum-decl | spec-decl | impl-decl | import-decl

binding        ::= 'mut'? identifier ( ':' type )? ( ':=' | '=' ) expr
reassign       ::= lvalue '=' expr
lvalue         ::= identifier ( '.' identifier | '[' expr ']' )*

fn-decl        ::= 'pub'? 'fn' identifier type-params? '(' param-list? ')' ( '->' type )? block
param          ::= ( 'mut' '&' )? identifier ':' type
struct-decl    ::= 'struct' identifier '{' ( identifier ':' type stmt-sep )* '}'
enum-decl      ::= 'enum' identifier '{' ( variant stmt-sep )* '}'
variant        ::= identifier ( '(' type ( ',' type )* ')' )?
import-decl    ::= 'import' str-lit

type           ::= 'int' | 'uint' | 'float' | 'bool' | 'byte' | 'str' | 'nil'
                 | identifier | 'list' '[' type ']' | 'Result' '[' type ']' | type '?'

expr           ::= or-expr | coalesce-expr | range-expr | match-expr | guard-expr | if-expr
primary        ::= literal | fstr-lit | fcmd-lit | cmd-lit | identifier | list-lit
                 | tuple-lit | block | '(' expr ')'
postfix        ::= '.' identifier | '.' dec-int | '[' expr ']' | '(' arg-list? ')'
                 | '?' | '!' | '?.' identifier
match-expr     ::= 'match' expr '{' ( pattern '=>' expr stmt-sep )* '}'
pattern        ::= identifier ( '(' identifier ( ',' identifier )* ')' )? | '_'
```

有兩處與完整文法允許的寫法不同，自舉原始碼即依此撰寫：enum 變體以**裸名**建構（`EBinary("or", a, b)`，而非
`Expr.EBinary(…)`），且 `match` arm 與 enum 變體之間以**換行**分隔，不是逗號。

## 種子不支援什麼

以下都因自舉原始碼完全沒用到而被移除。每一項都會大聲拒絕——一則診斷加非零 exit，絕不靜默丟棄語法。

| 已移除              | 現在的行為                                       |
| ------------------- | ------------------------------------------------ |
| `map[K, V]`         | 拒絕                                             |
| 閉包                | `a closure used as a value is not yet supported` |
| channel             | 拒絕                                             |
| `spawn`、`select`   | `statement not supported by the bootstrap seed`  |
| `#[dyn]` 動態分派   | `#[dyn] is not yet supported`                    |
| `zerg test` 後端    | 移除——執行 Zerg 測試是 Zerg 工具鏈的職責         |
| `--emit tokens/ast` | 移除——只留 `--emit c` 與連結出的執行檔           |

前端仍會**解析**其中部分語法；拒絕發生在後端被要求 lower 它的時候。收窄 parser 是另一輪的工作。

## 佈局

```text
src/bootstrap/
  cmd/zerg/        # 只做 build 的 driver：旗標、呼叫 cc、exit code
  internal/
    token/         # token 種類與其拼法
    lexer/         # 原始碼文字 -> token
    parser/        # token -> AST
    ast/           # AST 節點型別
    sema/          # 名稱解析與型別檢查
    types/         # sema 所依據的型別表示
    module/        # import 解析、whole-program 攤平
    mono/          # 單態化（泛型、遞迴 enum boxing）
    emit/          # AST -> C，以及 driver 據以連結的 runtime manifest
    build/         # 一次呼叫走完管線：load -> sema -> mono -> emit
    diag/          # 診斷
```

## 修改種子時

讓一項改動可以放心進行的不變量：**自舉原始碼所產生的 C 不得變動**。若那是真正的死碼移除，前後產生的 C 會逐位元組
相同；若不相同，那個差異就是這項改動真正的影響半徑。

```sh
zerg0 build --emit c src/compiler/zergc.zg > after.c  # 與改動前的擷取結果比對
go build ./... && go test ./...                       # 種子自己的測試（只有單元測試）
make build                                            # 而且自舉鏈仍能閉合
```

種子只由單元測試覆蓋。`test-data/` 語料描述的是語言，因此歸自舉編譯器所有，由 `make corpus` 執行——見
[`src/compiler/README.md`](../compiler/README.md)。

想把語言覆蓋率加回這裡，幾乎總是錯的選擇：自舉編譯器才是語言生長的地方。種子只需要好到足以建出它。
