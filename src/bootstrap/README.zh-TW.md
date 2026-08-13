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

## 契約：三層，中間沒有灰色地帶

種子只有一件工作——建出自舉編譯器——所以它**必須**支援什麼不是品味問題，而是
`src/compiler/zergc.zg`、`src/compiler/zerg/*.zg` 以及它們 import 的 stdlib 模組
（`io`、`ascii`、`strconv`、`cli`）實際用什麼寫成。語言的每一種形式都恰好落在三層之一。

**第一層——支援。** `Zerg-boot` 子集。降階成 C，並由種子自己的單元測試涵蓋。

**第二層——`NotImplemented`。** 合法的 Zerg，但自舉原始碼不使用。**指名拒絕**、非零 exit，
而且發生在產出任何東西之前。種子不是一個小一號的 Zerg——它是**一支程式的編譯器**，其餘都是別
人的工作。

**第三層——`SystemError`。** 種子完全無法歸類的狀態：沒有降階的 AST 形狀、解析不到任何東西的
型別、從未綁定的呼叫目標。這些不是程式設計者的錯，是**種子自己的**錯——它必須這樣說出來並中止，
而不是掉進 `0`、把一支沒人寫過的程式交給 cc。第三層的存在，才讓前兩層是一個**封閉集合**，而不
是中間留著無聲縫隙的兩份清單。

`make refuse` 守住第二、三層：一個案例斷言非零 exit、預期的句子，以及那句話來自種子本身、而不是
cc 對著產生的 C。

## 第一層——種子支援什麼

每一項都被自舉鏈實際用到，這是它還留著的理由。數字是 2026-07-30 當下該鏈的程式碼行數，已排除註解
與字串字面值。

| 功能                             | 說明                                                            |
| -------------------------------- | --------------------------------------------------------------- |
| `fn`，含 `mut &` 參數            | 傳參考的接收者（296 處）                                        |
| value struct                     | 宣告、建構、欄位存取、巢狀                                      |
| enum——一般、帶 payload、**遞迴** | tagged union，自我參照自動裝箱（`Ty`、`Expr`、`Stmt`）          |
| 固有 `impl T` 與 `This`          | 值接收者，攤平成第一個參數 `this: T`（25 處）                   |
| `match`                          | arm 是運算式、以換行分隔、可解構（104 處）                      |
| `list[T]`                        | `append` / `len` / `x[i]` 讀寫、`for … in`（520 處）            |
| `str`、`byte`、**f-string**      | 串接、`str(bytes)` / `list[byte](str)`、`f"…"`（162 處）        |
| `Result[T]`、`guard`、`raise`    | driver、`io` 與 `strconv` 走的錯誤路徑                          |
| `return e if c`                  | 後置條件 return——385 處，用得最多的糖                           |
| `if` / `else`、三種 `for`        | `for cond`、裸 `for`、`for x in xs`；`break`、`continue`、`nop` |
| `import`、`pub`                  | 帶命名空間的呼叫、整程式攤平                                    |
| `__zrt_*` intrinsic              | runtime 地板，含 `__zrt_exec`（`zerg` 就是這樣呼叫 cc 的）      |

型別：`int`、`uint`、`float`、`bool`、`byte`、`str`、`nil`、`list[T]`、具名 struct 或 enum、
`Result[T]`，以及 `impl` 內的 `This`。

## 第二層——種子指名拒絕什麼

**這張表就是「語言規格為什麼不提種子」的理由。** 種子拒絕的形式不是 Zerg 的缺口——出貨的 `zerg`
降階得了它——所以 `docs/` 對這裡的任何一項都不標註,寫 Zerg 的讀者也永遠碰不到。它們是種子自己的
契約,而這裡就是記載它們的地方。

以下沒有一項出現在自舉鏈裡；每一項都是**驗證過不存在**，不是假設。以 `zerg0` 於 2026-07-31 量測。

| 形式                                                                  | 那是什麼           |
| --------------------------------------------------------------------- | ------------------ |
| coroutine：`spawn`、`chan[T]`、`select`、`<-`、`close`、`for v in ch` | 整章並行           |
| `map[K, V]`——字面值，以及複製一個                                     | 那個容器           |
| 當成值用的 **closure literal**                                        | 具名的 `fn` 值可用 |
| slicing `xs.slice(a, b)` / `xs[a..b]`                                 | 子範圍             |
| 模組層級 `const`                                                      | 函式外的 binding   |
| `#[dyn]` dispatch                                                     | 那個 decorator     |
| `unsafe`、`asm`、`ptr[T]`                                             | 通往裸機的那道門   |
| command literal `` `git status` ``                                    | 行程代換字面值     |
| `for k in m` 走訪一張 map                                             | 那個迭代           |

其餘語言有的，種子都有：`defer`、`del`、`with`、tuple 與 `t.0`、range 當值與當可迭代對象、optional
與整組 group-8 運算子、`init()`、`spec` / `impl`（含 provided method）、泛型函式定義、
`#[derive(Eq, Ord)]`、`Ref[T]`、struct 與 tuple pattern、block 當 `match` arm body、
`for c in s` 走訪一個 str 的 code point、`import … as` 改名，以及 `pub` module 常數。在其中幾項
上種子是兩個編譯器中**較寬**的那個——那是關於種子的事實，不是關於語言的。

**前端還剖析得動**不等於支援：拒絕可能落在 sema，也可能落在 emitter 門口。收窄 parser 是另一趟
獨立的工作，而且不急——真正重要的是第一層以外的東西不會抵達 C。

## 第三層——種子把自己的失敗說出來

`SystemError` 給的是前兩層都蓋不到的情況，它存在的理由是：另一個選項就是種子過去做的事——從
`switch` 掉進 `"0"`，然後 emit 出一支引用了沒人宣告的識別字的 C。接著 cc 會對著 `.zerg-cache`
底下、程式設計者打不開的檔案報一個真實的錯——一則指著錯誤程式的診斷。

| 情況                               | 必須發生什麼                            |
| ---------------------------------- | --------------------------------------- |
| 沒有降階的 AST 節點                | `SystemError: no lowering for <node>`   |
| 解析不到任何東西的型別             | `SystemError: <site> has no type`       |
| 從未綁定的呼叫目標                 | `SystemError: unresolved call <name>`   |
| emitter 從未註冊的 carrier／helper | `SystemError: <helper> was not emitted` |

規則是：**種子可以拒絕一支程式，也可以失敗——但它永遠不可以無聲地錯。**

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
