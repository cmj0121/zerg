# 自舉的 Zerg 編譯器（Self-hosting Zerg compiler）

[English](README.md) | 繁體中文

用 Zerg 寫成的 Zerg 編譯器。它在既有的 runtime 地基上做 lex、parse、emit C，然後呼叫 `cc`
——與 Go bootstrap 跑的是同一條管線，只是用這個語言自己重新表達一次。這裡的編譯器才是最終出貨的
那一個；Go 種子存在的目的只是把它建起來。

## 已確認的決定

1. **Emit 目標——轉譯成 C，重用 runtime。** emitter 降到 C（與 Go bootstrap 產生的同一種 C）
   然後交給 `cc`，整份重用 `src/runtime/csrc`。這個階段不做原生／組語 codegen。
2. **driver 透過一個新的 runtime `exec` leaf 呼叫 `cc`。** 今天的 runtime 沒有子行程原語。我們
   加入 `zrt_exec`（一層 OS syscall 地基：`posix_spawn`/`execvp` 之後 `waitpid`，零第三方依賴），
   以 `__zrt_exec` intrinsic 曝露出來，再包成 `os.run` / process 這個 stdlib 模組。這同時補上了
   規格裡「command literal — not yet」的缺口。
3. **涵蓋範圍——先做 `Zerg-boot` 子集，再逐步長大。** Milestone 1 只需要支援「編譯器自己的原始碼
   所使用的那個語言子集」。每一個 pass 都以「與 Go bootstrap 的 `--emit` 輸出做 diff」來驗證。
   等 fixpoint 成立之後，涵蓋範圍才往完整語言成長。

## 目錄結構

```text
src/compiler/
  zergc.zg        # driver：參數解析、模組載入、呼叫 cc
  zerg/           # 編譯器函式庫——一個目錄模組，共用同一個 scope
    token.zg      # Kind enum + Token 型別
    lexer.zg      # 原始碼文字 -> token 串（可要求保留註解）
    ast.zg        # 遞迴的 AST 節點型別（enum payload）
    parser.zg     # token -> AST
    emit.zg       # AST -> C，含 emit 所需的最小型別檢查
    fmt.zg        # token -> 標準形式的原始碼
    lint.zg       # AST -> 發現
```

## 怎麼使用

```sh
zerg build <file.zg>    # entry 宣告 `main` 時產生程式，否則產生 object
zerg build -j8 app.zg   # 同上，並同時編八個單元
zerg build --emit c <file.zg>      # 停在 C；`tokens` 與 `ast` 同理
zerg fmt <file.zg>...   # 就地把原始碼重寫成標準形式
zerg lint <file.zg>...  # 回報未使用的 import 與死掉的私有程式碼；有發現則非零結束
zerg --help             # 命令、旗標，以及下面那些環境變數
```

編譯器自己解析 `import`，而且從任何目錄都能運作。它去哪裡找，先由環境變數回答，最後才是 repo
內的佈局——所以一份 checkout 不需要任何設定，而一次安裝也不需要 checkout：

| 變數           | 是什麼              | 預設值                        |
| -------------- | ------------------- | ----------------------------- |
| `ZERG_ROOT`    | 安裝的根目錄        | 目前的目錄                    |
| `ZERG_RUNTIME` | runtime 的 C 原始碼 | `$ZERG_ROOT/src/runtime/csrc` |
| `ZERG_STDLIB`  | 標準函式庫          | `$ZERG_ROOT/src/stdlib`       |
| `ZERG_CACHE`   | build cache         | `$ZERG_ROOT/.zerg-cache`      |

一個 import 先對進入點檔案自己的目錄解析，然後才是標準函式庫；而一個模組要嘛是 `<name>.zg`，
要嘛是一個**目錄**——目錄裡的原始碼以排序後的順序讀取。之所以排序，是因為產生出來的 C 不可以
取決於檔案系統交還給你的順序。

`zerg/` 這一層模組子目錄——原本的提案認為它是多餘的——實際上是被建 M1 時發現的兩個 bootstrap
事實逼出來的：(1) `import "x"` 解析到的是一個**目錄模組**，它的多個檔案會攤平進同一個共用
scope，所以 `token.zg`/`lexer.zg`/`parser.zg` 可以共用 `Kind` 與 AST 的那些 enum；但
(2) **enum 的 variant 跨不過模組邊界**（`token.Fn` 會被拒絕）。因此函式庫必須住在**一個**目錄
模組（`zerg/`）裡讓那些檔案共用 enum，而 `zergc.zg` 只是一層薄薄的 driver，永遠只呼叫該模組的
`pub` 函式——從不自己建構 variant。當初 `src/compiler/zerg/` 這個直覺是對的。

## 一次 build 是怎麼組起來的

一個程式是**逐單元**編譯的。一個單元就是一個模組——一個檔案，或一個目錄模組的那幾個檔案（它們
共用 scope，不能拆開）——每個單元各自成為一個 object，最後由一次連結把它們湊起來。其他的一切都
建立在這個切分上：

- **分離編譯。** 一個單元宣告整個程式，但只定義它自己的模組，所以兩個共用同一個 import 的模組
  可以並排連結。整程式一次 emit 做不到這件事：每個 object 都會帶一份共用模組的副本。
- **快取。** 一個單元的 key 是「它產生的那份 C」的 hash——那已經把它自己的原始碼、它看得到的
  一切、以及產生它的那個編譯器全都摺進去了，因為上述任何一項改變都會改變那份 C。用內容而不是
  時間戳，而這之所以安全，正是因為 fixpoint 證明了 emit 是決定性的。改一個註解不會重編任何
  東西：註解到不了產生出來的 C。runtime 的 translation unit 也以同樣方式快取。
- **平行。** 單元一旦 emit 完就彼此不相依，所以 `-j` 可以同時編好幾個。平行來自 OS 行程而不是
  coroutine：runtime 的排程器是協作式 N:1，所以 coroutine 給的是並行，不是 CPU 平行。

用它自己建自己，六個單元：`-j1` 是 1.28 秒，`-j4` 是 0.56 秒，什麼都沒改時是 0.33 秒。

## 語料庫（corpus）

`test-data/` 屬於這個編譯器。它描述的是**語言**——那正是 `zerg` 正在長成的東西——所以它跟隨
語言，而不是種子；[`src/bootstrap/`](../bootstrap) 裡的 Go 種子用單元測試涵蓋它自己那份狹窄的
契約，一個字都不讀這裡。

```sh
make corpus     # 先建 zerg，然後拿它跑過 test-data/codegen/
make refuse     # 每一支必須被拒絕的程式，都要由編譯器指名拒絕
```

每個案例是一支 `.zg` 程式，旁邊放著它必須印出的 stdout。Makefile 的 `CORPUS_PASS` 是 `zerg`
今天做對的那一組，而且是**閘門**：一個案例掉出這組就是 regression，會讓 target 失敗。其餘案例
只回報、不強制——剩下的八個需要泛型**函式**定義、把泛型型別參數當成欄位型別、`derive`、spec
bound 或 `#[dyn]`，而自舉編譯器目前一個都沒有。每一個都是**指名**拒絕——`gen_struct` 回答的是
_no type named `T` (field `Box.val`)_——而不是誤譯。

`make refuse` 是同一件事的另一面。這裡每一道閘門問的都是工具鏈**建得出什麼**，而一則拒絕真正
需要被釘住的性質，不是「壞程式會失敗」——它一直都會——而是**誰**說的。編譯器照樣 emit 出去的
程式會走到 cc，由 cc 對著 `.zerg-cache` 底下的產生碼、在一個程式設計者打不開的行號上報一個真實
的錯。所以 `scripts/refuse-check.sh` 的每個案例都斷言三件事：非零 exit、預期的句子，以及輸出裡
**沒有**那個 cache。

每個開始通過的案例，就是一次修正或一個功能落地，然後它會被搬進那份清單。

**emit 是端到端驗證的，不是逐位元相同。** 要在約 9.5k 行的規模上重現 Go 種子確切的 C 排版與
命名，代價遠高於它的價值，所以標準是**功能等價**：產生的 C 必須編得過，而程式必須印出語料庫
所說的東西。決定性（反正 fixpoint 本來就要求）是讓這件事穩定的原因；逐位元相同的 C 明確不是
目標。

## 把 emit 長到自我編譯的子集（M3 → M5）

examples 的子集（純量、函式、if / for、對 int 的 match）已經端到端完成。編譯器自己的原始碼需要
的遠不止這些，所以 emit 一個功能一個功能地長，每一個都先用一支端到端的測試程式驗證過，才做下
一個。Go bootstrap 產生的那些 C 形狀（也就是目標）是：

| 功能               | C 形狀                                                            | 需要 runtime？ |
| ------------------ | ----------------------------------------------------------------- | -------------- |
| struct             | `typedef struct {…} zg_T;` 值；`(zg_T){a,b}`；`p.zg_f`            | 否             |
| 帶 payload 的 enum | `{int32_t tag; union{ struct{…} Var; } u;}`；`.tag` / `.u.Var.fN` | 否             |
| 遞迴的 enum        | payload 欄位變成 `void*` ref-box                                  | 是             |
| list[T]            | `zrt_list` + `zrt_list_init/push/len/at`；for-in 是索引迴圈       | 是             |
| str 建構／運算     | `list[byte](s)` / `str(bs)` / `+` / `==`                          | 是             |

**leak-style 的 emit 是那個簡化。** 一個自舉編譯器是批次工具：它編一次然後結束，所以它從來不需要
free。因此 emit **重用 runtime 的資料結構原語**（`zrt_list_*`、用 `zrt_ref_alloc` /
`zrt_ref_payload` 做裝箱），但**跳過整套記憶體管理紀律**——那套紀律 Go 的 emit 是穿過每一個函式
去織的——沒有 `zrt_scope_mark` / `zrt_defer` / `zrt_unwind_to`，沒有 per-type 的 copy / drop /
release，ref-box 配置了就不釋放，list 帶一個 `{NULL,NULL}` 的元素 vtable。作業系統會在結束時
回收。這仍然遵守「emit C、重用 runtime」這個決定——runtime 的資料結構確實被重用了——同時砍掉了
Go emit 大部分的複雜度。決定性（M5 唯一需要的性質）不受洩漏影響。

**而那個理由已經超出它自己的範圍了。** 它推論的是**一個**程式：這個編譯器，編譯它自己，然後結束。
但這是**出貨用的後端**：同一份 emit 編譯的是每一個有人用 Zerg 寫出來的程式，而那些程式沒有一個
答應過自己是批次工具。一個用 `zerg build` 建出來的 Zerg 服務，會漏掉它格式化過的每個字串、建過的
每個 list，只要它還在跑就一直漏。語言裡沒有任何一句話這樣說，工具鏈也不會警告。

它之所以一直沒被量到，是因為唯一的 sanitizer gate 是 `make sanitize-conc`，而在 ASan 的 fiber
標註落地之前，LeakSanitizer 掃的是錯的範圍找 root，於是什麼都不報。第一次誠實的執行才把它叫出來。

每個擁有者今天做到哪裡，還剩下什麼：

| 擁有者         | 今天                                                   | 還剩下什麼         |
| -------------- | ------------------------------------------------------ | ------------------ |
| `chan`         | binding，以及沒被 bind 的 handle                       | ——                 |
| `list` / `map` | binding、參數、元素 vtable、rvalue 暫存值              | ——                 |
| `str`          | refcount cell；binding、參數、每一次 join              | ——                 |
| struct         | `zg_drop_<T>` 就寫在 `zg_copy_<T>` 旁邊，走同一組欄位  | ——                 |
| carrier        | copy 加 drop；binding、參數、元素 vtable               | `Either` 的 Right  |
| enum           | `zg_drop_<E>` 就寫在 `zg_copy_<E>` 旁邊，逐個 variant  | ——                 |
| ref-box        | cell 的 drop 就是 enum 自己的 drop                     | 改成迭代式的鏈拆解 |
| fn value       | 捕獲環境是一個 cell；一組 `zg_*_fnptr`                 | ——                 |
| tuple          | `_drop` 就寫在 `_copy` 旁邊，按形狀產生，含元素 vtable | ——                 |
| assignment     | enum 與 carrier 的舊值會被 drop                        | 其他每一種擁有型別 |
| 以上全部       | 在宣告處註冊，靠 unwind 還回去                         | ——                 |

concurrency corpus 現在是 **0 筆洩漏報告**（從 39 筆），而 `scripts/sanitize-conc.sh` 已經打開
`detect_leaks=1`——那裡再出現洩漏就是 regression，不是已知欠債。`str` 那一列本來不是「多 emit
一些程式碼」就能解決的，因為 literal 是靜態儲存、concat 回傳的是 `malloc`，在執行期一個 `char*`
沒辦法告訴你它拿的是哪一種；它換成了 refcount cell，並把 literal emit 成 IMMORTAL cell，讓
refcount 的兩半在它身上都是 no-op。

最後一列就是把 abort 那條路關掉的東西。釋放現在是**在 binding 宣告的地方註冊**，靠 unwind 到一個
mark 還回去——那本來就是其他每一條出口都會走的同一條路，包括 runtime 自己展開的那次 abort。
`c_release_from` 原本的論證（defer 會握著一個可能先結束的區塊裡的 C 區域變數位址）並不成立：
`zrt_unwind_abort` 是先 unwind 才 `longjmp`，所以 frame 還活著；而任何會註冊東西的區塊都會拿自己
的 mark、在自己結束的地方 unwind。

**還沒被量到的**是 corpus 的其餘部分。`sanitize-conc` 跑的是 17 個 concurrency case；我對另外 48
個做了一次性的掃描，13 個裡面總共 47 筆，而且都是 concurrency case 碰不到的類別——一連串的
rvalue index、map 暫存值、expression 裡的 `str(bytes)`，以及 ref-box 的遞迴型別。

`make mem-check` 是第一個能跑在別處的 gate。它建起寫在 `scripts/mem-check.sh` 裡面的 9 支程式，
每一支各跑 5 輪與 200 輪，連結的是取代 `alloc.c` 的計數配置器，並要求兩次的存活筆數相等——所以它
既不需要 LeakSanitizer 也不需要私有 corpus，在 macOS 上、在 fork 上都跑得起來。ref-box、carrier
與 closure 環境就是它被寫出來要抓的那三個，而它在三個關掉之前都是紅的。它自己聲明的限制是：一個
**有界**的洩漏——每支程式一筆，或每個位置一筆而不是每次建構一筆——在兩個輪數下是同一個數字，它看
不見。

**通往 M5 的增量階梯**（每一階都端到端測過，然後才 commit）：

1. struct——宣告、建構、欄位存取、`mut &` 參數、欄位變更 _（不需 runtime）_
2. 帶 payload 的 enum——tagged union、建構、match 解構 _（不需 runtime）_
3. 遞迴的 enum——`void*` ref-box，leak-style _（需 runtime）_
4. list[T] + for-in——`zrt_list`、索引迴圈、依元素型別單型化 _（需 runtime）_
5. str 建構／運算——`list[byte]` ↔ `str`、`+`、`==` _（需 runtime）_
6. 泛型／單型化——每一個在原始碼中用到的實例化各產生一份具體的 emit
7. import——把攤平後的多檔模組 emit 成一個 translation unit

當七項全部到位、而且功能程式的語料庫加上 stdlib 都能編譯並跑出一致結果時，編譯器就可以試著編譯
它自己的原始碼，M5 於是開始。

## 自舉的證明

編譯器能重現它自己，才讓「自舉」是一個主張而不只是一句描述，而 `make build` 就是檢查它的地方：
種子建出一個中間產物，中間產物建出最終出貨的編譯器，而一個無法重現自己的編譯器過不了這一關。
`make corpus` 與 `make lint` 是疊在上面的檢查。

## 把 bootstrap 縮到最小（M6）

一旦 fixpoint 成立，Go bootstrap 就只需要編譯 `src/compiler/*.zg` 這些原始碼（以及它們 import
的 `io` / `ascii` / `strconv` / `cli`）**實際用到**的那個 `Zerg-boot` 子集。每一次移除都由
`make build` 自己把關：種子建出中間產物，中間產物建出出貨的編譯器，而一個弄丟了編譯器所需東西的
種子過不了這一關。`make corpus` 與 `make lint` 是疊在上面的檢查。

**Zerg-boot 子集**（最小的 bootstrap **必須**保留的東西）：

- 宣告：`fn`（含 `mut &` 參考參數）、`struct`、`enum`（含自我遞迴）、`#[derive(Eq)]`、
  `import`、`pub`
- 敘述：`x := e` / `x: T = e` / `mut` / `const` 綁定，對名稱／欄位／索引 lvalue 的賦值、
  `print`、`return`（含 `return e if c`）、`if` / `else if` / `else`、`for cond` / `for` /
  `for x in xs`、`break`、`continue`、`nop`、`guard`、`raise`
- 運算式：int / float / str / bool / byte 字面值、`nil`、識別字、一元／二元運算子、呼叫、
  欄位存取、索引、方法呼叫、list 字面值 `[]`、轉換（`int`/`byte`/`str`/`list[T]`(x)）、
  `match`（字面值／綁定／萬用／建構子 pattern，可選的 guard）、if 運算式
- 型別：`int`、`float`、`str`、`bool`、`byte`、`nil`、`list[T]`、具名的 struct/enum、
  `Result[T]`、`impl` 內的 `This`
- 帶**值接收器**的固有 `impl T { … }` 方法：parser 會把整個區塊攤平成帶有 `this: T` 第一參數的
  普通函式，而 C 名稱是 `zg_<T>_<name>`，不是自由函式拿到的那種扁平 `zg_<name>`。`mut fn`
  （可變接收器）**不在**子集內——改用 `mut &` 參數，或回傳一個新值，而後者本來就是可串接的
  builder 在做的事。
- 隨附 stdlib 所降到的那些 `__zrt_*` runtime intrinsic

**可剝除的**（不在子集內——自舉的原始碼從不使用）：closure／第一級函式；coroutine
（`spawn` / `chan` / `select`）；`map[K,V]`；`spec`（因而 `impl Spec for T`）與泛型**函式**
定義；`unsafe` / `asm` / `ptr`；command literal；`with` / `defer` / `del`；`Result` 以外的
optional（`T?` / `??` / `!`）。非 build 的子命令（`fmt` / `lint` / `test`）同樣被丟掉——最小的
種子只有 `zerg build`；自舉的編譯器之後可以用 Zerg 把那些工具重新實作一次。

**f-string 在 `F405` 落地時離開了那份清單。** 自舉的原始碼現在會用到它們——`zerg fmt` 會寫出
它們——所以種子必須能 lex 與 parse `f"…"` 才建得出 stage 1。它本來就做得到；改變的是這件事現在
變成承重的，而不再只是順帶。出貨的編譯器只接受單純的 hole：沒有 `:spec`、沒有 `!r`/`!s`/`!a`、
沒有 `{x=}`，也沒有會內插的命令形式，每一種都是**指名**拒絕，而不是沉默略過。它在 parser 裡就
desugar 成這個形式本來被定義成的那條 `+` 鏈，所以 AST 與 emitter 對 f-string 一無所知。

## 出貨的編譯器接受什麼

上面那份 Zerg-boot 清單回答的是另一個問題。它說的是**種子**為了建出 stage 1 必須保留什麼；
它沒有說 `zerg` 自己理解什麼，而這兩者一直在拉開。出貨的編譯器在那個子集之外還接受：

| 形式                              | 註                                      |
| --------------------------------- | --------------------------------------- |
| `a..b` / `a..=b`、`for i in a..b` | 作為 `for` 的可迭代對象；當成值會被拒絕 |
| `init()`                          | 可以多個，各跑一次，依宣告順序          |
| 模組層級的 `const`                | 一個 C global，在任何 `init()` 之前賦值 |
| `(a, b)` 與 `t.0`                 | 每一種相異的 shape 一個 carrier struct  |
| `map[K,V]`、`{k: v}`、`{:}`       | POD 的 key 與 value                     |
| `defer f(args)`                   | 在所在區塊的出口，引數以值捕獲          |

並行在這裡是完整的，而且這是這個編譯器目前**比較寬**的地方：`chan[T](cap)`、`ch <- v`、真正
回傳 `Result[T]` 的 `<-ch`、`close(ch)` 與 `defer close(ch)`、四種 arm 形狀俱全且 arm body 可以
是敘述的 `select`、`for v in ch`、方向端 `<-chan[T]` / `chan[T]<-`、被呼叫者為方法或帶命名空間
函式的 `spawn`，以及 stdlib 的 timer。channel 在這裡也是一等值——放進 struct 欄位、當成 enum
payload 攜帶、送進另一條 channel——而且 payload 在送出當下深拷貝，所以 receiver 絕不會共享
sender 的緩衝區。

null safety 在這裡是完整的：`T?` 是一個有自己 carrier 的型別、`nil` 是它的缺席值，而 GRAMMAR
group 8 的四個運算子都讀得懂它——`??`（右結合、短路、右側可以是發散敘述）、`?.`（欄位本身是
optional 時會壓平）、`!`，以及 `?`（把缺席從一個結果載得住它的函式提早 return 出去）。宣告會補上
建構時省略的部分：`T?` 欄位補 `nil`，其餘則指名報錯。

仍然缺少的，而且每一個都是**指名**拒絕、不是誤譯：`Ref[T]`（它會一併帶走 `std/atomic`）、對
**`Result[T]`** 的 `?`（它需要 `Result[T]` 能存活於簽章，與上面 `T?` 那一半不同）、`match` arm
body 用區塊、泛型**函式**定義、把泛型型別參數當成欄位型別、具名引數的 struct 建構 `T(a: 1)`，以及
command literal。

## 效能：平行與快取（M7）

一層 fixpoint 之後才做的效能層。正確性與決定性的 fixpoint 優先（M1–M5）；M7 只在上面加速度，
所以下面那些使能條件會提早設計進去，而排程器／快取本身最後才落地。

### 平行來自 OS 行程，不是 coroutine

runtime 是一個協作式的 **N:1** 排程器：`spawn`／channel 在單一 OS thread 上給的是並行，不是
CPU 平行（先佔式的 **M:N** 是「not yet」）。所以行程內的 coroutine 沒辦法加速 CPU-bound 的編譯
工作。真正的平行是散到多個行程上的，而 driver 就是那個編排者：

- **`cc` 的呼叫**——同時跑多個 `cc` 行程。牆鐘時間的大宗落在 C 後端，所以 driver 生出一個有上限
  的 pool 並回收它們。最大、最便宜的一筆收益。
- **前端／每單元**——每個模組一個 worker 編譯器行程。`lex`/`parse`/`emit` 每個模組都是純的、
  獨立的，所以它以 `make -j` 的方式散到多個行程上。

coroutine／channel 仍然作為**編排**層有用——一個餵給有上限行程池的工作佇列——而不是作為計算的
平行。排程走的是模組載入器早就算好的**模組相依 DAG**（import 邊 + init 計畫）：拓撲地抽乾
ready-set，葉子優先。等 M:N 排程器落地之後，前端也可以在單一行程內平行化。

### 以內容定址的快取，每模組一份

- **單元**——模組（本來就是編譯／相依的單位）。
- **Key**——一份 `sha256`，涵蓋：模組原始碼、它所 import 的那些模組的公開介面 hash、目標旗標，
  以及編譯器自身的版本。少了任何一項都有拿到過期結果的風險。
- **兩層產物**——`.zg → .c`（這個編譯器）與 `.c → .o`（透過 `cc`）。因為 emit 是決定性的，
  相同的 `.c` 會產生相同的 `.o`，所以「快取 `.c` 加上一個內容定址的 `.o` 儲存」就涵蓋了整條管線。
- **介面 vs 實作**——把模組導出的表面分開 hash，可以讓私有內文的改動不必重編它的相依者
  （Go export-data 的做法）。MVP 可以先 hash 整份原始碼，之後再細化。

### 共用的前置條件

- **決定性的 emit**——穩定的順序，沒有 map 迭代的隨機性，輸出裡沒有時間戳。這是 M5 fixpoint
  **與**一份可靠快取共同要求的——同一個性質同時服務兩者，所以它不是額外的工作。
- **`zrt_mkdir` runtime leaf**——今天的 runtime 沒有 `mkdir`（只有
  open/read/close/open_write/write_bytes/exists/remove）。快取目錄
  （`$XDG_CACHE_HOME/zerg/` 或 `.zerg-cache/`）需要它；在 M0 與 `zrt_exec` 一起加。

### 設計時就放進去的使能條件（在 M1–M4 期間落地，不是延後）

- 前端的 pass 保持純粹且以模組隔離（M2/M3）
- emit 是決定性的（M3——本來就是 fixpoint 的前置條件）
- driver 從一開始就是一個「每單元 shell-out」的編排者（M4）
- `zrt_exec` 支援並行的子行程與多路 `waitpid`（M0）
