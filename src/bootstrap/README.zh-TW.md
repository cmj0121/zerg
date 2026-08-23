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

| 形式                                                                  | 那是什麼                 |
| --------------------------------------------------------------------- | ------------------------ |
| coroutine：`spawn`、`chan[T]`、`select`、`<-`、`close`、`for v in ch` | 整章並行                 |
| `map[K, V]`——字面值，以及複製一個                                     | 那個容器                 |
| 當成值用的 **closure literal**                                        | 具名的 `fn` 值可用       |
| slicing `xs.slice(a, b)` / `xs[a..b]`                                 | 子範圍                   |
| 模組層級 `const`                                                      | 函式外的 binding         |
| `#[dyn]` dispatch                                                     | 那個 decorator           |
| `unsafe`、`asm`、`ptr[T]`                                             | 通往裸機的那道門         |
| command literal `` `git status` ``                                    | 行程代換字面值           |
| `for k in m` 走訪一張 map                                             | 那個迭代                 |
| `e in ValueError`——錯誤 taxonomy 的**子樹**測試                       | docs/code/errors 的 `in` |

`e is ValueError` 種子建得起來，沒有讀法的是 `in`。這兩個是不同的關係（identity 與 subtree，見
docs/code/errors.zh-TW.md），而只有其中一個在這裡——這件事值得寫下來，因為它決定了一個 corpus 案例
該怎麼寫：問 `is` 的案例兩個編譯器都能被檢驗，問 `in` 的案例只有 `zerg` 回答得了。有五條 oracle skip
就架在這一個缺口上（`error_tree`、`err_kind_subtree` 及其同類），見 `test-data/oracle-skips.txt`。

它同時也是一個**用錯句子的拒絕**。`e in ValueError` 對種子而言讀成「對一個叫 `ValueError` 的值做成員
測試」，於是那個名字以一般運算式解析、答案是 `undefined name "ValueError"`——拼錯字會拿到的那句話，
對這個運算子什麼也沒說。第二層說的是**指名拒絕**，而這不是；記在這裡而不修掉，是因為自舉源碼從來
不問這個問題。

同樣是**用錯句子的拒絕**，而且是寫 stdlib 的人真的會碰到的那一個：**`str(x)` 的 `x` 是一個型別參數**。
種子把 `fn show[T](x: T) -> str { return str(x) }` 擋下來，說的是
`cannot build a str from T; str(x) takes a scalar or a list[byte]/list[rune]`——一句在講轉換定義域的
話，講得像是程式遞了一個壞引數，但真正發生的事情是這個問題問得早了一步。
`internal/sema/strbridge.go` 檢查 `str(x)` 的方式是對引數問 `ScalarOf`，而在泛型函式體裡，引數的型別
還是 `T`；種子是**抽象地**檢查那個函式體，所以拒絕落在宣告處，不管那支函式有沒有被呼叫過。同一個
rendering 換成 `f"{x}"`，兩個編譯器都建得起來：`inferFStr` 只 synth 洞裡的運算式並回傳 `str`，把
rendering 留給 lowering，而 lowering 是在代入之後才跑的。`zerg` 兩種拼法都在代入之後才問——`show(7)`
建得起來，`show(p)` 傳一個 struct 則是指名 `P` 的 `E9059`，那才是讀者能據以行動的診斷。這個落差的代價，
是每個由種子編譯的模組都得遵守的一條規矩：`src/stdlib/testing.zg` 用插值而不用轉換
（`raise f"assert_eq failed: {a} != {b}"`），而 stdlib 裡任何一個泛型函式體只要伸手去用 `str(x)`，
壞掉的就不是一支程式，是整條自舉鏈。

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

## 已知落差

種子刻意是較窄的那個編譯器，凡是它沒有實作的，多半都會被它拒絕。這裡列的是**它沒有拒絕**的地方——它自己
沒有把程式擋下來，於是 `zerg` 成了較嚴的那一個，而 `scripts/reject-check.sh` 會把該案例標記為 `seed-gap`。

「它自己」就是整件事的全部。下面其中兩項，都曾有整整一年被算成拒絕，因為種子產生的 C 是被 **clang** 擋
下的：`-Wint-conversion` 與 `-Waddress-of-temporary` 在那裡是錯誤，在 gcc 底下只是警告，於是同一個種子、
同一支程式，在 macOS 讀起來是綠的，在 Linux 是紅的。cc 的診斷代表種子已經把那支程式 emit 出去了，而那正
是這項斷言存在要抓的事。

- **一個 unit 能吐出多少 C，這裡沒有上限。** 大到足以耗盡機器的程式在這裡是被 SIGKILL 的——沒有代碼、
  沒有位置、沒有任何可讀的東西。`zerg` 超過 `$ZERG_EMIT_MAX` 就以 `E5016` 拒收，而這是種子不會補上的
  落差：種子的存在只為了建一支程式，而那支程式遠低於任何值得設的上限。
- **本檔案沒有 import 的模組，透過帶限定的 enum variant 命名時會被接受。** 檔案只 import 了 `mid`
  （而 `mid` import 了 `lib`），此時 `lib.Colour.Red` 是整個程式攤平後才可及的名字，語言拒絕它；種子
  對任何 namespace 成員都沒有執行這條規則，而 variant 現在也成為其中之一——它讀得懂
  `mod.Enum.Variant`，因為編譯器自己的診斷登錄表就是隔壁模組的一個 enum。`zerg` 會以 `E5007` 拒絕。
- **帶預設值的 `mut &` 參數會被接受，而用到該預設值的呼叫會 segfault。** GRAMMAR#param 讓 `mut &` 只在
  該次呼叫中成立，且它的引數必須是 `mut` 的 lvalue；預設值沒有任何呼叫端變數可指。種子把預設值運算式
  emit 在該放指標的位置，於是對 `fn f(a: int, mut &b: int = 0)` 呼叫 `f(5)` 會對一個字面值解參考。
  `zerg` 在宣告處就拒絕它。
- **跨越 `defer` 的 `mut &` 引數會被接受。** GRAMMAR#param 說借用不得逃逸；種子把值交給那個 deferred
  thunk，而那裡該放的是指標。`zerg` 在呼叫處拒絕它。
- **裝不進自己參數的預設值會被接受。** `fn f(a: int, b: str = 1)` 照寫出來的樣子被 emit 出去，然後由 cc
  來報那個型別。`zerg` 在宣告處就判定一個預設值。
- **會呼叫任何東西的預設值會被拒絕，而且那句話宣稱是語言禁止它。** `struct C { c: chan[int] =
chan[int]() }`——或任何不是字面值、模組常數，或它們之間算術的預設值——都會被以「_a default value must be
  a constant expression that does not reference a parameter/field_」擋下來。語言說的正好相反：一個預設值
  「是**每次建構**時求值，而不是在宣告處求一次——裡面的運算式（一個呼叫、一個對模組常數的求和）會在每一次
  省略該欄位的建構中重跑一遍」（`docs/core/types.md`，"Field defaults"），而 `zerg` 接受它。種子是把預設值
  **逐字**回填到每一個呼叫處與建構處，而且根本不會對那個運算式做**型別判定**——`checkConstDefault` 只驗形狀，
  所以一個預設值沒有被記錄的 `ExprType`，一個呼叫會以壞掉的 C 抵達 cc。因此那個拒絕對種子而言是對的、對
  Zerg 而言是錯的：一個穿著語言規則外衣的 `NotImplemented`。這也是 `src/stdlib/testing.zg` 裡的
  `Context.events` 是 `pub` 且不帶預設值的原因——模組私有欄位必須帶一個，而一個全新 channel 唯一能有的預設值
  就是一個呼叫。
- **頂層 binding 的型別標註不會拿來對照它的值。** `answer: bool = 42` 建得起來，那個全域變數就是種子隨手
  做出來的樣子；同樣的不相符若發生在**區域** binding 上，種子是會拒絕的。`zerg` 對待頂層標註的方式與對待
  區域的一致——全域採用宣告的型別，不相符在宣告處被拒絕。
- **取用了某個函式名字的模組常數會被接受。** `const f := 1` 與 `fn f()` 並存——不論原始碼順序如何——都會被
  emit 出去，然後 cc 對著產生的程式碼報 "redefinition of 'zg_f'"：兩者都攤平進同一個頂層命名空間、同一個
  C 符號。`zerg` 在常數的宣告處拒絕這個衝突。（以函式為名的**區域**變數在兩個編譯器裡都仍然合法——那是
  遮蔽，屬於一般的作用域規則。）
- **拿 optional 去 `match` 一個 range 會被接受。** `zerg` 拒絕該 arm。
- **收窄成 `byte` 參數的 `int` 會被接受。** 對 `fn take(b: byte)` 呼叫 `take(1000)` 會編成一次截斷，cc 則
  對著產生的 C 發出警告。`zerg` 拒絕它：`byte` 會**加寬**成 `int`，反方向則什麼都不成立，因為 byte 的算術
  本來就停在 `byte`，所以沒有任何地方需要那個方向。
- **宣告了兩次的欄位或 variant 會被接受。** `struct A { v: int; v: str }` 與 `enum E { X; X }` 在種子底下
  都建得起來也跑得動。`zerg` 兩者都拒絕——第二個 variant 永遠到不了，於是一個只提到第一個的 `match` 讀起來
  像是窮盡的，而那個 enum 其實有兩個。（重複的**參數**種子倒是抓得到。）
- **optional tuple `(A, B)?` 曾被 emit 成 `void`。** carrier 掃描在 tuple 的 C 型別命名之前就去問元素的
  `ctype()`，於是 `Opt[Tuple]` 沒被認成 carrier，函式便靜靜地什麼都沒回傳；cc 對著產生的 C 報
  "variable has incomplete type 'void'"。現在它被指名拒絕了——單純的 `(A, B)` 回傳可行，而 `zerg` 兩種都
  建得起來。這也是 **stdlib 不得使用 optional tuple** 的原因：它同樣由種子編譯，跟那裡什麼都不用 slicing
  是同一個道理。
- **持有東西的 tuple 不能被複製。** `t := (1, s)`（`s` 是 `str`），以及任何持有 `list` 或 `map` 的 tuple，
  都會被指名拒絕——"copying a (int, str) is not supported in Phase 1d iteration 2 (only Ref[T] and structs
  holding Refs)"。純量組成的 tuple 複製沒問題，持有同樣東西的 struct 也沒問題，所以這只針對 tuple。`zerg`
  兩種都複製得了：tuple 會拿到一份按形狀產生的 `_copy`，旁邊還有一份 `_drop`，那正是 `(int, str)` 在離開
  作用域時把它的 `str` 還回去的原因。
- **宣告了兩次的型別名稱會被接受。** `struct`、`enum` 與 `spec` 共用同一個命名空間，而且在兩個編譯器裡，
  程式的每個模組都攤平進同一個作用域——所以兩個 `enum E`、兩個 `spec T`，以及 `struct A` 與 `spec A` 並存，
  都是一個名字對上兩份宣告。種子每一種都建得起來也跑得動。`zerg` 拒絕這樣的一對，並在兩者種類不同時把那
  兩個種類講出來。
- **形狀不對的 `display` / `debug` override 會被接受。** docs/runtime/format.md 釘死了 override 的契約——
  `fn display() -> str`，只有值進去、它的文字出來——而 `zerg` 會在宣告處拒絕任何一個叫這兩個名字、卻收引數
  或回答別的東西的方法。種子沒有 rendering dispatch，所以那個方法對它而言就是個普通方法，程式照建。
- **巢狀深度超過 `zerg` 轉譯上限會被接受。** `zerg` 拒絕運算式、block 或型別巢狀超過 200 層的程式
  （docs/conformance.md）——它一邊數自己的遞迴，一邊量測每一棵完成的運算式樹的深度，後者才抓得到那種
  **扁平**的鏈（`1 + 1 + … + 1`、一長串方法鏈）：它在迴圈裡剖析，parser 從來沒有更深過。它的 emitter 還會
  數第三次，數的是它**走過**的那棵樹，而不是程式寫出來的那棵，這才抓得到原始碼從未寫出的深度——被回填進
  呼叫處的預設引數，就把兩者疊在一起。種子在 Go 那個可增長的 stack 上剖析，上述每一種形狀、在每一個位置
  它都接受。這個落差在收窄的方向上是無害的——種子唯一的輸入，也就是編譯器自己的原始碼，只巢狀五層——但它
  仍然是落差：種子完全不強制任何上限，而夠深的程式最終會是一次 Go 的 stack 上限 panic，而不是一則診斷。
- **`Either.Left(v)` 讀不出來；裸的 `Left(v)` 才讀得出來。** `Either` 的兩側是某個內建型別的 variant，而
  不是某個宣告出來的 enum 的，所以種子那條 enum 命名空間的路徑找不到 `Either`，回報的是名稱未定義。
  `zerg` 要求限定的那種形式（variant 一律透過它的型別具名，內建型別也不例外），這也是這裡用到它的三支語料
  程式被以該理由跳過的原因。
- **`#[obj]` 是個未知的 decorator。** `zerg` 會把它展開成一個裝著函式值的伴生 struct 與一層泛型包裝；種子
  只讀 `#[derive(…)]`，別的都不讀。它的展開需要 closure capture，而種子同樣沒有，所以那一對裡手寫的那一半
  在這裡也一併被拒絕。
- **會捕獲的 closure 會被拒絕。** `zerg` 把 lambda 的捕獲提升成一份 per-site 的環境 struct，並透過 fn 值
  自己的 env 欄位交給那次呼叫；種子則把會捕獲的 lambda 擋下來——「a closure used as a value is not yet
  supported」——在這一項上它是較窄的那個編譯器。
- **enum 上的 `#[derive(S)]`，若 `S` 是你自己寫的 spec，會被拒絕。** `derive` 中負責轉交的那一半——每個 arm
  把呼叫交給自己的 payload——是一套對任何 spec 都成立的改寫，所以 `zerg` 會把它 derive 出來。種子則兩半都
  只認一組欽定的名單，直接把程式擋下；在這一項上它是較窄的那個編譯器，而不是錯的那個。
- **`spec` 裡的關聯型別或關聯值會被接受。** GRAMMAR#spec-member 推導得出的只有必要簽章與 provided
  method，別無其他——spec 承載的是行為。種子讀到其中的 `type Item` 與 `BITS: int` 會照樣走下去；`zerg`
  兩者都指名拒絕。（兩者皆非的成員，像 `SIZE := 4096`，種子倒是會擋。）
- **把型別引數明寫出來的呼叫，在它的多引數形狀下會被接受。** `pairup[str, int]("k", 9)` 在這裡建得起來；
  `zerg` 拒絕它，因為 GRAMMAR 讓後置的 `[ … ]` 一律是索引，而泛型是從它的引數取得型別。單引數的那種形狀
  （`id[int](7)`）種子倒是會擋，只是理由是它自己的。
- **裸的 variant 會被當成值接受。** GRAMMAR 說 enum 把自己的名字放進值的命名空間，而不是把 variant 放進
  去，所以一個 variant 要透過它的 enum 才到得了——`Color.Red`。種子讀得懂那種形式，卻不要求它，於是單獨的
  `Red` 在這裡仍然解析得出來；`zerg` 指名拒絕它。種子自己的原始碼不需要為此遷移，理由與「它的落差就是它
  自己的契約」相同：對於遵守規則的程式，它是 oracle，而不是規則的執法者。（在 pattern 位置上沒有落差，也
  沒有規則要執行：裸名在兩個編譯器裡都是綁定，GRAMMAR#pattern 說的就是這件事。）
- **拿 `This` 當一份宣告的名字會被接受。** `This` 是 self 型別，每個 `impl` 都寫得到它，卻沒有人宣告它，
  所以它與 `this` 一樣是保留的——但它是唯一一個被 lexer 讀成普通識別字的保留字，而種子對關鍵字表以外的名字
  沒有任何規則。於是 `struct This`、`fn This()`、`type This = int`、一個參數，以及一個 `enum` variant，
  在這裡全都建得起來，而 `zerg` 一一指名拒絕。（小寫的 `this` 種子倒是會拒絕，因為那一個確實是關鍵字
  token。）
- **目標帶著型別引數的 `impl` 會被接受，而且什麼都沒實作。** `impl Size for list[int] { … }` 與固有的
  `impl Box[int] { … }` 在這裡都建得起來：種子把整條 `GRAMMAR#impl-decl` 都剖析下來，連 `impl` 自己的
  `generics?` 也在內，接著把那個 block 掛到任何呼叫都找不到的地方，於是 `xs.size()` 的回答是「list 沒有
  這個名字的方法」——一則落在**使用處**的拒絕，離那份從未被擋下的宣告只有一步。`zerg` 拒絕的是宣告本身，
  並把形式與它的位置說出來。（帶參數的 `impl[T] Spec for list[T]` 種子倒是會擋，只是理由是它自己的：它把
  讀到的參數丟了，於是目標裡的 `T` 解析不到任何東西，答案是 `unknown type "T"`，而不是一句關於形式的話。）
- **不是 UTF-8 的原始檔會被接受。** `GRAMMAR#letter` 說原始碼是 UTF-8，所以一個夾著孤零零 `0xFF` 的檔案
  不是 Zerg 原始檔；種子從讀檔到 emit 都是以位元組為單位，沒有任何 str 不變量會被違反，於是那個位元組一路
  穿過 lexer 進到字串字面值，再以 `"\377"` 出現在 C 裡。`zerg` 是把檔案讀成一個 `str`，讀不成就拒絕並把
  路徑講出來——編碼是唯一一處它是較嚴編譯器的地方，而理由不是它多加了一條規則，是它有一個型別。
- **頂層的敘述會被接受，然後被丟掉。** `program ::= stmt-list`（`GRAMMAR#program`）是 Zerg 的 script
  mode，所以頂層的 `print 999`、`if …` 或 `for …` 都合乎文法——而一支編譯出來的程式沒有地方跑它們，因為
  `main` 之外只住著在它之前就備妥的不可變狀態（docs/runtime/package.md）。種子把每一個都剖析進
  `file.Items`，既不降階也不提一句，於是程式建得起來，什麼也不印。`zerg` 在它被寫下的那一行指名拒絕它，
  只有 `nop` 例外。這是 `zerg` **加上**的規則，而不是種子丟掉的規則，也是這裡的常態方向。
- **一次轉換只折疊寫出來的字面值，透過名字抵達它的已知值會被留到執行期。** docs/core/types.md 說
  `byte(300)` 在編譯期就被指出來，理由是「這個值是已知的」，而語言所謂的已知就是 const-expr——一個字面
  值、初始式是字面值的繫結、一個 `const`，以及它們之上的運算子。`zerg` 問的正是這個問題（填充計數
  `[v; N]` 問的也是同一個），所以 `big := 300; byte(big)`、`const N := 300; byte(N)` 與 `byte(N * 3)`
  都是編譯錯誤。種子只折疊字面值本身：這三個它都建得起來，並在它們執行到的地方 raise `OverflowError`。
  `reject-check.sh` 裡有三個案例帶著這個標記。範圍的另一端兩個編譯器停在同一個位置——一次呼叫與一個
  `mut` 繫結對兩者都不是常數——所以這個落差是區間的中段，不是它的盡頭。
- **任何兩個純量之間的轉換都會被接受，不論是哪一對。** docs/core/types.md 列出了 `T(x)` 有的那些配對、
  並說「沒有其他」——`int` 是每一對都踩著的中樞——但種子是按**形狀**降階一次轉換的，一個類別加一個寬度，
  而形狀對每一對都有答案。於是 `byte` 上的 `float(b)`、`rune(b)`、`uint(b)`，以及 `byte(3.5)`、
  `uint(3.5)`、`rune(65.5)` 與 `int(1.9)` 在這裡全都建得起來。`zerg` 逐一拒絕：來源是 `float` 就是一個
  要用動詞寫出來的決定（`E3090`，`math.trunc` 與它的三個兄弟），其他不在表上的配對則是把穿過 `int` 的兩
  步寫成了一步（`E3091`）。`reject-check.sh` 裡有十七個案例帶著這個標記，而這一章是 `zerg` 比較嚴、而不是
  反過來的地方。（種子自己的原始碼不需要遷移：`src/stdlib` 裡已經沒有任何地方寫出表外的配對，那也正是
  兩個編譯器能建出同一套標準函式庫的原因。）
- **兩個模組各自宣告一個同名的 `pub` 函式會被接受，而其中一個勝出。** 一個公開名字沒有可以在其中保持唯一
  的 package（[package](../../docs/runtime/package.md)），所以 `zerg` 按名字拒絕這一對（`E9081`），並說出
  要同時留下兩個需要什麼——也就是 [ffi](../../docs/runtime/ffi.md) 規定的 link-name 覆寫。種子和 `zerg`
  一樣把每個模組攤平成同一個命名空間，卻只對**私有**的那一對問這個問題，並用模組替它加標記；公開的那一對
  以單一個 mangled 符號抵達 C，第二份定義就這樣取代了第一份。`reject-check.sh` 裡有一個案例帶著這個標記。
- **沒有上界的 inclusive range 會被接受，而寫著它的那個 arm 永遠不會成立。** `GRAMMAR#range-arm` 規定
  `..=` 的上界是必要的，而剖析器把缺席的那個讀成 `nil`——那也是一支程式寫得出來的東西，`1..=nil`。`zerg`
  不論它從哪裡來都拒絕這個形式（`E9102`）。種子把缺席的上界讀成 0，於是那個 arm 對每一個值都是假的，`match`
  就一聲不響地落到它的 catch-all。`reject-check.sh` 裡有一個案例帶著這個標記。
- **拿一個 `spec` 當 struct 欄位的型別會被接受。** 一個 spec 是一個界限、也是一個介面，不是某個值的型別
  （[specs](../../docs/core/specs.md)），所以 `zerg` 在每一個寫得出型別的位置都拒絕它（`E9048`）。種子只
  對參數和結果問這個問題，不對欄位問，於是 `pub v: Tag` 宣告了一個沒有任何東西給得出表示法的欄位，而程式
  建得起來。`reject-check.sh` 裡有一個案例帶著這個標記。
- **除以常數 `0` 會被接受，並在執行期 raise。** `x := 1 / 0` 是編譯器算得出來的值，所以 `zerg` 在那個除法
  處就作答，而不是把程式留著自己走到那裡——與在具型別的位置折疊一個字面值是同一套道理。種子在這裡什麼都不
  折疊，直接 emit 那個除法，接著由它的 runtime 檢查 raise 出來。兩者最後都拒絕了這支程式；只是其中一個是
  在程式跑起來之前做的。
- **一份 `pub` 宣告可以提到模組私有的型別。** `pub fn make() -> Secret` 與模組私有的 `struct Secret` 並存
  會被接受，於是依賴者拿得到一個它根本拼不出名字的型別的值——「一份宣告的可見度，永遠不能高於它提到的
  型別」（docs/runtime/package.md）沒有被強制。參數，以及型別是私有的 `pub` 欄位，情況也一樣。`zerg` 在
  宣告處拒絕它，因為那才是有一行可以改的一方。私有型別上的 `pub` **method** 也一樣——那是同一句話讀在
  receiver 上，而規格說「一個型別的 `pub` method 隨它一起走」，正是這一點讓它是同一條規則、而不是另一項
  客套。在鄰近的那條規則上，種子反而是較嚴的那個編譯器——它從被寫
  出來的那天起就拒絕透過命名空間提到的模組私有型別（`lib.Secret`），而且只拒絕限定的那種拼法，因為它根本
  不會把型別攤平進 importer 的命名空間。
- **模組私有的欄位，從另一個模組讀得到，也寫得進去。** 種子會要求 GRAMMAR#field 規定私有欄位必須帶的那個
  預設值——好讓外部程式碼不必提到一個它不該讀的值就能建構該型別——然後又讓那個值被讀出來。`zerg` 對讀與寫
  一視同仁地拒絕，落在使用處。
- **import 沒有遞移性，而種子不強制這件事。** 兩個編譯器都把命名空間綁進同一個程式層級的空間，所以只要
  建置過程碰得到某個模組，它過去就是每個模組都叫得出名字的模組：只 import 了 `mid` 的 `main` 仍然寫得出
  `lib.make()`。`zerg` 現在會記錄每個綁定是由哪個模組**寫下**的，並拒絕本模組沒有 import 的命名空間——同時
  仍然把它與憑空杜撰的前綴分開，後者在兩個編譯器裡都是未定義名稱。
- **宣告出來的型別名稱不必以大寫字母開頭。** `struct _Box`、`struct __Box` 與 `struct lower` 在這裡都建得
  起來也跑得動。`zerg` 在宣告處逐一拒絕（`E2060`）：第一個字母的大小寫，正是它用來分辨「建構」與「呼叫」、
  分辨模組限定與關聯型別投影的依據，而後兩者是由 **parser** 判定的——它什麼都還沒解析，也沒有表可查——所以
  一個小寫的型別名稱會變成在一個位置合法、在三個位置被讀錯。GRAMMAR#type-ident 導出這條規則。種子改用符號
  表解析名稱，因此那個字母對它從來沒有意義。`reject-check.sh` 裡有三個 case 帶著這個標記。
- **編譯器 primitive 的運算元型別沒有被檢查。** 機制其實在那裡——`internal/sema/infer.go` 裡的
  `unaryIntrinsic(n, Float, Int)` 就指名了引數型別——但它不會觸發，所以 `__zrt_trunc(true)` 建得起來、印出
  `1`，而 `__zrt_trunc("hello")` 則被 emit 出去，讓 cc 對著一個沒有人寫過的暫存 C 檔提出抱怨。`zerg` 在呼叫
  處就回答這兩者（`E3094`）：primitive 是按**名字**降階成一個帶有真實簽章的 C 函式，所以錯的運算元不是變成
  cc 的診斷，就是在 C 悄悄替你轉型的地方變成一個安靜的錯答案。`reject-check.sh` 裡有兩個 case 帶著這個標記。
- **`#[test]` 函式不會被型別檢查。** 種子在 `sema.Check` 跑之前，就把每個 `#[test]` 從項目清單裡剝掉
  （`internal/build/build.go` 的 `dropTestItems`），所以 `#[test] fn t() { x: int = "no" }` 建得起來也跑得
  動，呼叫一個根本不存在的函式也一樣。`zerg` 把 `#[test]` 當成一個普通宣告上的普通 decorator，函式本體與
  其他函式一視同仁地檢查：一個編不過的測試就是編譯錯誤，在一般建置裡如此，在 `zerg test` 底下也如此。種子
  這樣剝掉對種子而言並沒有錯——它讓一般建置產生的 C 不論有沒有 `#[test]` 都逐位元組相同——但這也代表：唯一
  會跑測試的那個編譯器，也是唯一會看測試一眼的那個。
- **`assert` 不是種子認得的字，而它說出來的話點錯了 token。** `assert cond` 是出貨語言的一個敘述
  （`GRAMMAR#assert-stmt`），也是 `zerg` 獨有的關鍵字。種子的 lexer 把 `assert` 讀成普通識別字，於是整條
  敘述變成兩個接連的運算式，回來的是 _expected a newline or ';' to separate statements, found an
  identifier_——指著**條件**、而不是指著那個字——一句讀起來像「少了分號」的話，而少分號正是它唯一不是的
  問題。靠這個形狀辨認它:欄位號差了那個字加一個空格的寬度。`src/stdlib` 底下刻意沒有任何一條，因為那棵樹是
  種子編的；但它 raise 的那個**錯誤種類**（`AssertionError`，11）在這裡照樣鏡射，因為種類編號是與 runtime
  共用的 ABI，一份停在 10 的表會讓後來的種類佔走 11。
- **module 層的 `unsafe { … }` 群組不是種子讀得懂的形式，所以種子建的程式 `import "log"` 會失敗。** 它跟這裡
  其他每一條都相反:不是種子接受了 `zerg` 拒絕的東西,而是種子拒絕了 `zerg` 接受的東西——而且那個拒絕是標準
  函式庫裡的一個 parse 錯誤,跟提出 import 的那支程式本身無關。回來的是 _module "log": log.zg failed to
  parse: expected an expression, found 'unsafe'_,指在 import 的那一行,而不是指在那個群組。

  那個群組就是 `log` 的全域 logger,而它沒有別的寫法:module 狀態是不可變的（`E3056`）,而一個系統層級的
  logger 依定義就是 module 狀態。`src/stdlib` 其餘部分遵守的那條規則——待在種子讀得懂的子集裡,這也是那裡
  沒有任何 `assert` 的原因——被剛好一個模組刻意打破,而且只有那一個。其他每個 stdlib 模組仍然兩個編譯器都
  建得起來,而種子自己編的東西沒有一個 import 它。

  因此用 `bin/zerg0` 建的程式沒辦法 log——而且因為 `testing` 會 import `log`(好讓 `ctx.log` 的註記就是
  其他每一行同樣的那種行),它也沒辦法 import `testing`。兩者都不構成代價:種子只有 `build` 一個命令,
  所以它從來不跑測試,而它看得到的 `#[test]` 也在檢查前就被剝掉了(見上一條)。用 `bin/zerg` 建的兩件事
  都做得到——而除了自舉本身,這條工具鏈產出的每一支程式都是後者。

- **凍結只看得見裸名字。** 在 `for x in xs` 裡面,種子和 `zerg` 一樣拒絕 `xs.append(v)` 與 `xs = [9]`,而除此之外
  什麼都不拒絕。同一種結構改動的另外三種寫法 —— 穿過 **path**(`for x in p.xs { p.xs.append(v) }`)、穿過字面值
  **索引**(`xs[0]`),以及把被走訪的集合**交給**一個 `mut &` 參數 —— 在種子底下全都編得過,而且真的會把正在被走訪
  的集合長大或重新綁定。

  這對 bootstrap 沒有代價:種子會編譯的原始碼裡沒有一份寫了這幾種形式,而一個「種子接受、`zerg` 拒絕」的程式永遠
  不會被 `make oracle` 拿去比對 —— 它只跑種子建得起來的東西。`scripts/reject-check.sh` 裡那四個 case 各帶一個
  `seed-gap` 標記,所以種子學會這條規則的那一天,gate 會說出來,而這一條也就跟著移除。

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
