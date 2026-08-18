# 自舉的 Zerg 編譯器（Self-hosting Zerg compiler）

[English](README.md) | 繁體中文

用 Zerg 寫成的 Zerg 編譯器。它在既有的 runtime 地基上做 lex、parse、emit C，然後呼叫 `cc`
——與 Go bootstrap 跑的是同一條管線，只是用這個語言自己重新表達一次。這裡的編譯器才是最終出貨的
那一個；Go 種子存在的目的只是把它建起來。

## 已確認的決定

1. **Emit 目標——轉譯成 C，重用 runtime。** emitter 降到 C（與 Go bootstrap 產生的同一種 C）
   然後交給 `cc`，整份重用 `src/runtime/csrc`。這個階段不做原生／組語 codegen。
2. **driver 透過一個 runtime `exec` leaf 呼叫 `cc`。** `zrt_exec` 是一層 OS syscall 地基——
   `posix_spawn`/`execvp` 之後 `waitpid`，零第三方依賴——以 `__zrt_exec` intrinsic 曝露出來，
   再包進 `os`。它**沒有**補上規格裡 command literal 的缺口：`` `…` `` 至今仍是指名拒絕
   （`E9020`）。
3. **涵蓋範圍——先做 `Zerg-boot` 子集，再往外長。** 編譯器得先編得動自己的原始碼，才談得上編別
   的；而那個子集就是今天種子的契約
   （[`src/bootstrap/README.zh-TW.md`](../bootstrap/README.zh-TW.md)）。`zerg` 在那之**外**還
   接受什麼，見[下文](#出貨的編譯器接受什麼)。

## 目錄結構

```text
src/compiler/
  zergc.zg        # 宣告出來的命令列：`main`、`root`、版本橫幅
  cmd/            # 每個子命令「做什麼」——一個目錄模組
    cmd.zg        # 這個模組自己的標頭，不宣告任何東西（Go 的 `doc.go`）
    build.zg      # `zerg build`——pipeline，以及產物寫到哪裡
    test.zg       # `zerg test`——執行、每個 package 一個行程、回報
    test_pkg.zg   #   哪些目錄有測試，以及各自由哪些檔案組成
    test_fixture.zg #  一次 package 執行的計畫：fixture、測試、順序
    test_driver.zg  #  那份計畫被編譯成的 driver 原始碼
    fmt.zg        # `zerg fmt`
    desugar.zg    # `zerg desugar`
    lint.zg       # `zerg lint`
    lsp_cmd.zg    # `zerg lsp`（不是 `lsp.zg`：那會遮蔽 `import "lsp"`）
    diag.zg       # 共用：詞法關卡與診斷算繪
    source.zg     # 共用：讀一份原始碼，並解析它 import 什麼
    layout.zg     # 共用：東西在哪裡，以及 cc 用什麼參數呼叫
    unit.zg       # 共用：一個 unit、它的快取 object，以及連結
  zerg/           # 編譯器函式庫——一個目錄模組，共用同一個 scope
    rule.zg       # 每一個診斷的代碼，以單一 enum 宣告一次
    token.zg      # Kind enum + Token 型別
    lexer.zg      # 原始碼文字 -> token 串（可要求保留註解）
    ast.zg        # 遞迴的 AST 節點型別（enum payload）
    parser.zg     # token -> AST
    check.zg      # 一支「程式」必須滿足的規則，與產生它的那段程式碼分開
    generic.zg    # 單型化——每一個實例化各產生一份，做法是代換
    emit.zg       # AST -> C，含 emit 所需的最小型別檢查
    fmt.zg        # token -> 標準形式的原始碼
    desugar.zg    # token -> 那些 sugar 被定義成的 core 形式
    lint.zg       # AST -> 發現
    version.zg    # 由 ./scripts/gen-version.sh 從 VERSION 產生
  lsp/
    server.zg     # language server——自成一個模組
```

`cmd` 裡標成**共用**的那四個檔案之所以在那裡，是因為不只一個子命令會用到它們，而且「是哪些」是在 call graph 上量出來的，
不是猜的：`diag`、`layout`、`unit` 是 `build` 與 `test` 的（`lint` 也讀前兩個），`source` 則是這三個再加上 `lsp`。
它們放在命令旁邊而不是放進其中任何一個裡面——一個目錄就是一個模組，所以沒有任何東西為了共用而變成 `pub`，
也就沒有任何東西變成 `pub` 之後可能跟第二個模組相撞（`E9081`）。

## 怎麼使用

```sh
zerg build <file.zg>    # entry 宣告 `main` 時產生程式，否則產生 object
zerg build -j8 app.zg   # 同上，並同時編八個單元
zerg build --emit c <file.zg>      # 停在 C；`tokens` 與 `ast` 同理
zerg build --emit check <file.zg>  # 只出診斷——不產生 C，也不產生任何檔案
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
| `ZERG_CSTD`    | cc 用的 C 方言      | `c17`                         |

一個 import 先對進入點檔案自己的目錄解析，然後才是標準函式庫；而一個模組要嘛是 `<name>.zg`，
要嘛是一個**目錄**——目錄裡的原始碼以排序後的順序讀取。之所以排序，是因為產生出來的 C 不可以
取決於檔案系統交還給你的順序。

`zerg/` 這一層模組子目錄——原本的提案認為它是多餘的——實際上是被建 M1 時發現的兩個 bootstrap
事實逼出來的：(1) `import "x"` 解析到的是一個**目錄模組**，它的多個檔案會攤平進同一個共用
scope，所以 `token.zg`/`lexer.zg`/`parser.zg` 可以共用 `Kind` 與 AST 的那些 enum；但
(2) **enum 的 variant 跨不過模組邊界**（`token.Fn` 會被拒絕）。因此函式庫必須住在**一個**目錄
模組（`zerg/`）裡讓那些檔案共用 enum，而 driver——`zergc.zg` 與 `cmd` 模組——永遠只呼叫該模組的
`pub` 函式，從不自己建構 variant。當初 `src/compiler/zerg/` 這個直覺是對的。

## 一次 build 是怎麼組起來的

一個程式是**逐單元**編譯的。一個單元就是一個模組——一個檔案，或一個目錄模組的那幾個檔案（它們
共用 scope，不能拆開）——每個單元各自成為一個 object，最後由一次連結把它們湊起來。其他的一切都
建立在這個切分上：

- **分離編譯。** 一個單元宣告整個程式，但只定義它自己的模組，所以兩個共用同一個 import 的模組
  可以並排連結。整程式一次 emit 做不到這件事：每個 object 都會帶一份共用模組的副本。
- **快取。** 一個單元的 key 是對「它產生的那份 C，**加上會拿去餵的那個 `cc` 與 C 方言**」取
  `sha256`——那份 C 已經把它自己的原始碼、它看得到的一切、以及產生它的那個編譯器摺進去了，但摺
  不進那兩個位在它下游的輸入。用內容而不是時間戳，而這之所以安全，正是因為 fixpoint 證明了 emit
  是決定性的。改一個註解不會重編任何東西：註解到不了產生出來的 C。`make cache-key-check` 是那道
  閘門，而它之所以存在，是因為 `cc` 那一半從快取寫出來的那天起就一直缺著——換一個編譯器，讀回來
  的是前一個編譯器建的 object，而且回報成功。
- **平行。** 單元一旦 emit 完就彼此不相依，所以 `-j` 可以同時編好幾個。平行來自 OS 行程而不是
  coroutine：排程器是 M:N 但**協作式**的，一條 CPU-bound 的 coroutine 會一直佔住它的 worker，
  所以它買到的是並行，不是更短的編譯時間。

用它自己建自己是**十個單元**——進入點、`cmd/`、`zerg/`、`lsp/`，加上它們用到的六個 stdlib 模組。
量三次的結果是：冷建 `-j1` 約 7 秒、`-j4` 約 6 秒，而 `-j8` 落在 `-j4` 的執行間浮動範圍內，什麼
都沒改時約 5 秒。這些數字之所以這麼接近，正是重點：`zerg/` 是一個目錄模組、因此是一個單元，而它
就是編譯器的大部分，所以 `-j` 不可能低過它。在這裡買到時間的是快取，不是平行。

## 語料庫（corpus）

`test-data/` 屬於這個編譯器。它描述的是**語言**——那正是 `zerg` 正在長成的東西——所以它跟隨
語言，而不是種子；[`src/bootstrap/`](../bootstrap) 裡的 Go 種子用單元測試涵蓋它自己那份狹窄的
契約，一個字都不讀這裡。

```sh
make corpus     # 先建 zerg，然後拿它跑過 test-data/codegen/
make refuse     # 每一個這個編譯器還沒建出來的形式，都是指名拒絕、不是 emit 出去
make reject     # 每一支不是 Zerg 的程式都被拒絕——由編譯器拒絕，不是由 cc
```

每個案例是一支 `.zg` 程式，旁邊放著它必須印出的 stdout。`mk/gates.mk` 的 `CORPUS_PASS` 是 `zerg`
今天做對的那一組，而且是**閘門**：一個案例掉出這組就是 regression，會讓 target 失敗。其餘的由
`CORPUS_SKIP` 擋著，而把一個名字從裡面刪掉，**就是**那個名字所等的功能的閘門。

還在等的有六個，每一個都是**指名**拒絕、不是誤譯——`gen_struct` 回答的是
_E9004 NotImplemented: a generic struct `Box[…]` — this compiler erases type parameters, and a
field names one_——它們等的是泛型的 `struct` 或 `enum`、`#[dyn]`，以及「無欄位 enum 的 `Eq`」以外
的 `derive`。另外兩個，`spec_bound` 與 `gen_identity`，今天建得起來、也印得出該印的東西：是這份
清單還沒跟上。

## 一支程式必須是什麼，以及誰說了算

種子有一個語意分析 pass；這個編譯器當初是在沒有那個 pass 的情況下寫成的，而它生命中的大部分時間，
沒有任何東西問過一支程式是否合式。`x := 1` 後面接 `x = 2` 編得過也跑得動。`1 + "s"` 變成 C 的指標
運算，印出一個位址。`b: bool = 1` 印出 `true`，因為降階之後兩者都是 `int64_t`。一個連 C 都看得出
來的型別錯誤會走到 **cc**，由它對著 `.zerg-cache` 底下的產生碼報出來。

`check.zg` 收著那些規則——可變性、一個區塊裡一個綁定、bool 條件（每一種在問問題的形式，包括
match arm 的 guard）、運算元型別，以及一個值會進入的四個槽：宣告、賦值、對照簽章的 `return`、
對照參數的引數，再加上一次呼叫的引數個數。它們是一個**檔案**而不是一個 pass，因為它們需要的知識
在 emitter 裡已經有了：`c_infer` 會把每個運算式定型，環境會追蹤每個綁定。今天另立一個 pass 等於
第二次走訪與第二份推論，而會漂走的正是第二份。把它們從呼叫它們的 emit 裡集中出來，是為了讓這組
規則可以被當成一組讀，也是為了讓日後把它們抬升成一個真正的 pass 是搬移而不是重寫——那件事在 AST
學會攜帶原始碼位置時就得發生。

型別以 `ty_eq`（在 `ast.zg`）比較，它是在 `Ty` enum 上做結構性比較——不是用 `ty_name`，後者是診斷
用的**拼法**，會把 `TUnknown`、`TTuple` 與 `TMap` collapse 成同一個名字。「合得進去」不等於相等，
所以 `chk_fits` 在上面另有自己的結構：一個會把拿到的東西重新塑形的槽從來不算不匹配，而一個 list
在它的元素合得進去時就合得進另一個 list。

兩種訊息，差別在於壽命：

| 訊息              | 意思                               | 住在       |
| ----------------- | ---------------------------------- | ---------- |
| `NotImplemented:` | 這個編譯器還沒建出來的形式         | `emit.zg`  |
| 一個普通的句子    | 一支不是 Zerg 的程式——語言給的答案 | `check.zg` |

`make refuse` 與 `make reject` 是兩道閘門，一欄一道。

`make refuse` 是同一件事的另一面。這裡每一道閘門問的都是工具鏈**建得出什麼**，而一則拒絕真正
需要被釘住的性質，不是「壞程式會失敗」——它一直都會——而是**誰**說的。編譯器照樣 emit 出去的
程式會走到 cc，由 cc 對著 `.zerg-cache` 底下的產生碼、在一個程式設計者打不開的行號上報一個真實
的錯。所以 `scripts/refuse-check.sh` 的每個案例都斷言三件事：非零 exit、預期的句子，以及輸出裡
**沒有**那個 cache。

**emit 是端到端驗證的，不是逐位元相同。** 要在 18k 行 Zerg 程式碼（連註解算 35k 行）的規模上
重現 Go 種子確切的 C 排版與命名，代價遠高於它的價值，所以標準是**功能等價**：產生的 C 必須編
得過，而程式必須印出語料庫所說的東西。決定性（反正 fixpoint 本來就要求）是讓這件事穩定的原因；
逐位元相同的 C 明確不是目標。

## 哪些構造需要 runtime

`需要 runtime？` 那一欄就是軸線，而它決定了 `src/runtime/csrc` 得裝多少 C：一個構造的 C 只是一個
**形狀**時就不需要 runtime，而當它的 C 需要一段**生命週期**——一個堆積上的盒子、一段可成長的緩衝
區、一個 refcount——它就得伸手去拿 `zrt_*`，也就不可能只是一個形狀。

| 功能               | C 形狀                                                            | 需要 runtime？ |
| ------------------ | ----------------------------------------------------------------- | -------------- |
| struct             | `typedef struct {…} zg_T;` 值；`(zg_T){a,b}`；`p.zg_f`            | 否             |
| 帶 payload 的 enum | `{int32_t tag; union{ struct{…} Var; } u;}`；`.tag` / `.u.Var.fN` | 否             |
| 遞迴的 enum        | payload 欄位變成 `void*` ref-box                                  | 是             |
| list[T]            | `zrt_list` + `zrt_list_init/push/len/at`；for-in 是索引迴圈       | 是             |
| str 建構／運算     | `list[byte](s)` / `str(bs)` / `+` / `==`                          | 是             |

## 拆解，以及那個必須收回的論證

emit **重用 runtime 的資料結構原語**（`zrt_list_*`、用 `zrt_ref_alloc` / `zrt_ref_payload` 做
裝箱）。它原本跳過的是圍繞著這些原語的整套記憶體管理紀律——沒有 `zrt_scope_mark` / `zrt_defer` /
`zrt_unwind_to`，沒有 per-type 的 copy / drop / release——理由是：一個自舉編譯器是批次工具，它編
一次然後結束，所以它從來不需要 free。

**而那個論證推論的是一個程式。** 這是**出貨用的後端**，同一份 emit 編譯的是每一個有人用 Zerg 寫
出來的程式，而那些程式沒有一個答應過自己是批次工具。一個 Zerg 服務會漏掉它格式化過的每個字串、
建過的每個 list，只要它還在跑就一直漏；語言裡沒有任何一句話這樣說，工具鏈也不會警告。它之所以
一直沒被量到，是因為當時唯一的 sanitizer gate 是 `make sanitize-conc`，而在 ASan 的 fiber 標註
落地之前，LeakSanitizer 掃的是錯的範圍找 root，於是什麼都不報。第一次誠實的執行才把它叫出來。

所以那套紀律現在是會被 emit 出來的。每個擁有者今天做到哪裡，還剩下什麼：

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

## 自舉的證明

編譯器能重現它自己，才讓「自舉」是一個主張而不只是一句描述，而 `make build` 就是檢查它的地方：
種子建出一個中間產物，中間產物建出最終出貨的編譯器，而一個無法重現自己的編譯器過不了這一關。
`make corpus` 與 `make lint` 是疊在上面的檢查。

## 出貨的編譯器接受什麼

`Zerg-boot`——**種子**為了建出 stage 1 必須保留的那個子集——只寫在一個地方：
[`src/bootstrap/README.zh-TW.md`](../bootstrap/README.zh-TW.md)，第一層是它支援什麼，第二層是它
指名拒絕什麼。`zerg` 在那個子集之**外**還接受什麼：

| 形式                              | 註                                      |
| --------------------------------- | --------------------------------------- |
| `a..b` / `a..=b`、`for i in a..b` | 作為 `for` 的可迭代對象；當成值會被拒絕 |
| `init()`                          | 可以多個，各跑一次，依宣告順序          |
| 模組層級的 `const`                | 一個 C global，在任何 `init()` 之前賦值 |
| `spec S { … }`                    | 整塊吃下；`impl S for T` 本來就能動     |
| `(a, b)` 與 `t.0`                 | 每一種相異的 shape 一個 carrier struct  |
| `map[K,V]`、`{k: v}`、`{:}`       | POD 的 key 與 value                     |
| `defer f(args)`                   | 在所在區塊的出口，引數以值捕獲          |
| `fn f[T](…)`                      | 單型化——每一個實例化各產生一份 emit     |

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

`f"…"` 只接受單純的 hole，而 `:spec`、`!r`/`!s`/`!a`、`{x=}` 與會內插的命令形式，每一種都是**指名**
拒絕，不是沉默略過（[編譯診斷](../../docs/tooling/diagnostics.zh-TW.md)）。它**在 parser 裡**就
desugar 成這個形式本來被定義成的那條 `+` 鏈——這既是 AST 與 emitter 對 f-string 一無所知的原因，
也是種子只要能 lex 與 parse 它就建得出 stage 1 的原因。

仍然缺少的，而且每一個都是**指名**拒絕、不是誤譯：`Ref[T]`（`E9058`）、泛型的 `struct` 或
`enum`——也就是被欄位或 payload 指名的型別參數（`E9004` / `E9003`；而讓 `import "atomic"` 變成
`E9104` 的正是 `Atomic[T]`）、具名引數的建構 `T(a: 1)`（`E9010`），以及 command literal
（`E9020`）。

## 效能還剩下什麼

平行與快取都[已經建好了](#一次-build-是怎麼組起來的)。還開著的只有兩件事，寫下來是為了不讓它們
被當成新點子重新發明一次：

- **私有的改動仍然會重編一個模組的相依者。** key 是整份產生出來的 C，所以任何碰得到它的編輯都會
  讓下游全部失效。把模組**導出的表面**分開 hash，才能讓只改內文的改動停在真正被改動的那一個單元
  （Go export-data 的交換條件）。它不會讓 `-j` 變快——沒有東西能把 `zerg/` 拆開——但它會讓快取在
  「一個人真的會做的那種編輯」上命中。
- **前端無法在單一行程內平行化**，理由與 `-j` 要散到多個行程上[相同](#一次-build-是怎麼組起來的)。
  coroutine 在這裡只有作為編排層才有用——一個餵給有上限行程池的工作佇列。
