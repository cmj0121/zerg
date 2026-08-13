# Zerg Language Server

`zerg lsp`——編譯器回答編輯器的問題,而不是回答 shell 的。屬於[語言參考](../language.zh-TW.md)的一部分。亦有
[English](lsp.md) 版本。

```sh
zerg lsp        # 在 stdin/stdout 上講 JSON-RPC 2.0;由編輯器啟動與關閉
```

## 主張

language server **不是一個新程式**。它就是已經存在的那個編譯器,被問了另一個問題:不是*「把這個 lower 成 C」*,而是
_「這個 buffer 現在有什麼問題」_。後者編譯器一直都在回答——`emit_files_diag` 正是 `zerg build` 呼叫的東西——所以這裡
交付的是把答案送到人正在看的地方的那段管線。

這同時也是不變式,而且它是被**強制**的,不是被宣稱的:

> **如果 server 和 `zerg build` 對一個程式的看法不同,錯的是 server。** 它沒有自己的分析。

`make lsp` 就是這句話變成的 gate。它為每個 example 與 40 個 corpus 程式在 stdio 上跑一次真的 session,把 server 發布
的東西held 到 `zerg build` 與 `zerg lint` 對同一個檔案說的話——error 對上會為此拒絕的那個命令,information 對上會回報
它的那個命令。這是 `make oracle` 的論證套用在第二個前端上。

除此之外它還帶了**十個 protocol case**,而且每一個都曾經是壞的:exit status、shutdown 之後的回覆、空的變更、增量變
更、完整變更、`$/` notification 對比 `$/` request、格式錯誤的 frame、字串 id、一行 CJK 之後的 UTF-16 欄位,以及大於
runtime bounded leaf 一次讀取量的 body。那是另一種、也更安靜的失敗——buffer 被弄壞的編輯器,或一個在乾等的 client,
什麼都不會說。

## 它住在哪裡

`src/compiler/lsp/`——自成一個 module,像任何其他消費者一樣跨 `pub` 邊界 import `src/compiler/zerg/`,由 `zergc.zg`
裡多一行 `.sub(lsp_cmd())` 接上。

**一個 binary,不是兩個。** 子命令讓編譯器與 server 之間的版本歪斜**物理上不可能**——它們是同一個檔案——而且編輯器不
需要 PATH 上多任何東西。

**獨立 module,而不是在 `src/compiler/zerg/` 裡多幾個檔案。** 目錄就是隱私單位,所以放進那個 module 裡,server 就能碰
到每一個 private 宣告,並且會像這棵樹裡每個能糾纏的東西一樣糾纏起來。強迫它走 `pub`,才換得到日後把它拆出去的選項。

**它不解析 `import`。** driver 已經知道 module 住在哪——環境變數、安裝根目錄、然後是 checkout——所以 `serve` 收一個
**函式**:給它一個 path 與一個 buffer 的文字,拿回那個 buffer 所屬的整個程式,其中該 buffer 的文字取代磁碟上的內容。
module 擁有協定;driver 擁有檔案系統。

## 已經做好的

| 請求                                                          | 由誰回答                                           |
| ------------------------------------------------------------- | -------------------------------------------------- |
| `initialize` / `shutdown` / `exit`                            | session 本身                                       |
| `textDocument/didOpen` · `didChange` · `didSave` · `didClose` | 全文同步                                           |
| `textDocument/publishDiagnostics`                             | `lex_diags`、`emit_files_diag`、`lint_conversions` |
| `textDocument/formatting`                                     | `fmt_src_off`——`zerg fmt` 呼叫的同一個函式         |
| `textDocument/codeAction`                                     | 一則 finding 帶著的 `fix`,包成一個 quick fix       |
| `textDocument/documentSymbol`                                 | `file_symbols`——被剖析的檔案裡的宣告               |

其他每一個請求都會收到 **method-not-found 錯誤**,而不是沉默。一個在等永遠不會來的回覆的 client 會停止送下一個請求,
然後編輯器就靜掉了,什麼也沒說。

**session 是一台狀態機,而 exit status 是它的一部分。** `shutdown` 之後 server 只接受 `exit`;之後才到的 request 會收
到 `InvalidRequest`,因為在等回覆的 client 會停止送下一個。`shutdown` 後的 `exit` 以 **0** 結束,沒有 `shutdown` 的
`exit`——或者標準輸入就這樣結束了——以 **1** 結束。永遠回 0 的 server,是在告訴 client 每一次崩潰都是乾淨關閉。

**同步是全文的,而不是全文的變更會被拒絕。** `textDocumentSync: 1` 表示 client 送整份文件,所以帶 `range` 的變更是
這個 server 從來沒要求過的增量編輯,把它的 `text` 套上去會把整份文件換成它自己的一個片段。**空的**
`contentChanges` 會讓 buffer 保持原狀,而不是換成空字串——那個旗標與空字串並不重複:client 把檔案清空時送的是 `""`。

**診斷是對整個程式檢查的**,不是只對 buffer。一個 import 了別的 module 的檔案必須連同那個 module 一起檢查,否則它借來
的每個名字都會讀成 undefined——會在正確的程式碼底下畫線的 server,是人會關掉的那種。

**兩種嚴重度,來自兩個地方。** **error** 是 `emit_files_diag` 回報、`zerg build` 會為此拒絕的東西。`L5xx` conversion
findings 是關於**合法**程式的——一個取了紙面上看不出來之型別的字面值——所以它們以 **information** 抵達。把一個能動的
程式塗成紅色的 server,是在教它的使用者忽略紅色。

**代碼是以代碼的身分傳遞的。** `Diag` 用一個自己的欄位帶著規則的身分——`E307`、`L502`——所以 server 送出 LSP 的
`Diagnostic.code`,編輯器可以據此過濾、分組與連結。那正是這一頁講的規則套用在它自己身上:只拼在句子裡的代碼,是每個
讀者都得再解析一次出來的東西,而 server 去解析它就會是一份語言事實的第二份拷貝。沒有代碼的 finding 會省略這個欄位,
而不是送一個空的。

**abort 沒有位置。** parse error 與 `NotImplemented` refusal 都是被 `raise` 的句子,而編譯器兩者都沒有帶地點——所以它
們以檔案頂端一個零寬度的 range 落地,帶著編譯器自己的話。不是 1:1 上那個字:畫在 `fn` 底下的線是在說 `fn` 錯了,而關
於這類 finding 唯一確定的事就是沒人知道它在哪。

## 位置

編譯器回答的是 1-based 的行與 1-based 的**位元組**欄,標記一個東西開始的地方。LSP 要的是 0-based 的行、以 **UTF-16
code unit** 計的 0-based character,以及一個 **range**。這道轉換的兩半都是 server 的,而且兩者都不是可選的:

- **單位**,因為位元組欄與 UTF-16 欄只有在一行全是 ASCII 時才一致,而這棵樹自己的 source 到處是 em-dash;
- **range**,因為 `Diag` 沒有結束位置。加一個欄位然後填進起始位置,會是一個其實是點、只是換了個名字的 span,所以結束
  是從**原始碼**導出的:位置上的那個 identifier,或者一個字元。

## Neovim

`make -C editors install` 會 symlink 語法檔,再加三個:

- `ftplugin/zerg.lua`——為 `.zg` buffer 啟動 server;
- `lua/zerg/lsp.lua`——`vim.lsp.start`(nvim 0.8+),不需要 plugin manager,也不需要 `nvim-lspconfig`;
- `lua/zerg/health.lua`——`:checkhealth zerg` 跑的東西。

```lua
vim.g.zerg_lsp = false            -- 不要啟動 server
vim.g.zerg_lsp_cmd = { 'zerg', 'lsp' }
vim.g.zerg_format_on_save = true  -- 每次寫入都跑 zerg fmt
vim.g.zerg_diagnostic = false     -- 診斷怎麼畫,交還給 nvim 自己的設定
```

當 `zerg` 不在 `PATH` 上時它**安靜地**回答。在一個沒有建好 toolchain 的 checkout 裡,每開一個 `.zg` 就報錯的 server,
是人會停用而且再也不會啟用的那種。

quick fix 不需要任何設定——`vim.lsp.buf.code_action()` 是 nvim 自己的,而 server 宣告自己是 `quickfix` provider,所以
即使 client 只要求這一種 kind 也拿得到。nvim 預設把它綁在 `gra`,把大綱綁在 `gO`。

### ftplugin 做了什麼,以及每個數字為什麼都是編譯器的

`ftplugin/zerg.vim` 是「在完全沒裝 toolchain 時也必須成立」的編輯行為,所以它不問任何正在跑的 `zerg`。它做的是陳述
編譯器擁有的事實——而 `make editor-align` 把其中每一條都held 回它的來源。

| 設定                       | 是什麼                                     | held 到什麼      |
| -------------------------- | ------------------------------------------ | ---------------- |
| `noexpandtab`、`tabstop=4` | 一層一個 tab,顯示四欄                      | `F101`、`F403`   |
| `colorcolumn=81`           | `F403` 換行預算之後的第一欄                | `fmt_wrap_max()` |
| `foldexpr` / `indentexpr`  | 一行所觸及的最低分隔符深度                 | 同一個掃描器     |
| `makeprg` / `errorformat`  | `:make` 跑 `--emit c`,並讀得懂兩種診斷形狀 | 編譯器自己的輸出 |

**摺疊與縮排是同一條規則,問了兩次。** 一行的層級是它觸及的最低分隔符深度——這讓一個區塊被摺起來時,包住它的兩行都
留在畫面上(上面的 `fn f() {` 與下面的 `}`),也讓 `}` 在打出來的當下就自己 dedent。差別在分隔符:摺疊只數大括號,
因為一個被拆行的參數列不是一個 fold;縮排數 `(`、`[`、`{` 三者,因為 `F403` 與 `F404` 在三者裡面都會縮排。

兩者原本都不存在。`indentexpr` 是空的,`autoindent`、`smartindent`、`cindent` 也都是,所以 `fn f() {` 之後按 `<CR>`,
游標停在第 1 欄,每一層都是人自己按出來的 tab——然後 formatter 在下次寫入時把它整理好,也就是說這個檔案只有在工具跑過
之後才是對的。現在它是對的這件事,是用 `gg=G` 掃過整個 repo 檢查的:對 formatter 寫出來的每一份原始碼重新縮排,必須
什麼都不改——而找出它真的改了的那兩種情況(一條被拆行的 `+` 鏈,與一行以 `# >>>` 結尾的 doctest 註解),就是這條規則
被塑造出來的過程。

**`:make` 是 quickfix list 裡的編譯器**,它值得跟 language server 並存,因為兩者的失敗方式不同:一個 buffer abort 時,
server 只能在檔案頂端發佈一則 finding,而 `:make` 仍然帶著編譯器自己的句子,以及(有的話)它的位置。

```vim
:make | copen           " 編譯這個 buffer,把它說的話列出來
:ZergFmt                " 需要時才跑 zerg fmt
:checkhealth zerg       " 為什麼什麼都沒發生
```

`:ZergFmt` 存在,是因為 `gq` 碰不到這個 server:nvim 只會為宣告了 `textDocument/rangeFormatting` 的 server 接上
`formatexpr`,而這一個只宣告整份文件的格式化——而且是對的,`zerg fmt` 讀的是一整份原始碼,沒有「只格式化一半」這個
概念。

`:checkhealth zerg` 是上面那份安靜的對照面。一個什麼都不啟動、也什麼都不說的 client,會讓「toolchain 沒建」、「`zerg`
被舊的安裝蓋掉」、「幾個月前設下的 `vim.g.zerg_lsp` 還是 false」、「server 起來了又掛掉」看起來一模一樣;health check
把它們分開,而且是去問 toolchain,不是自己抄一份關於它的說法。

### 一則不必按鍵就讀得到的 finding

nvim 預設的 `vim.diagnostic.config` 裡 **`virtual_text = false`**(0.11 起改的),所以一則發佈出來的 finding 預設畫出
來的,只有底線與 gutter 上的一個 sign——一個「這行有問題」的記號,而說出**是什麼**問題的那一半,被留在沒有人會看的
地方。而 server 的全部產出就是那句話,所以 client 為自己的 namespace 打開 virtual text,並在前面補上規則的代碼:

```text
    ratio: float = 2      ■ L502 the literal `2` is a float here — write `2.0` and the page shows it
```

**是它自己的 namespace,不是全域設定。** 對 `vim.lsp.diagnostic.get_namespace()` 交回來的 namespace 呼叫
`vim.diagnostic.config(opts, ns)`,改的是一則 **Zerg** finding 怎麼畫,對別人的一句話也沒說——而那是一個語言 plugin
有資格碰 `vim.diagnostic` 的唯一前提。有自己意見的使用者設 `vim.g.zerg_diagnostic = false`,然後留著他自己的。

不管畫成什麼樣,nvim 自己的按鍵都能拿到同一段文字,而且值得知道,因為它們說得比那一行**更多**:

| 按鍵 / 呼叫                                       | 顯示                                             |
| ------------------------------------------------- | ------------------------------------------------ |
| `<C-w>d`——`vim.diagnostic.open_float()`           | 游標所在行的每一則 finding,完整地開在一個視窗裡  |
| `]d` / `[d`——`vim.diagnostic.jump()`              | 下一則 / 上一則                                  |
| `<C-w>d` 按兩次                                   | 進到那個浮動視窗裡,文字可以 yank                 |
| `vim.diagnostic.setloclist()`                     | 全部列進 location list,一則一行                  |
| `vim.diagnostic.config({ virtual_lines = true })` | 那句話自己佔一行、畫在程式碼下面,不會被截斷      |
| `:lua =vim.diagnostic.get(0)`                     | 原始 finding——`code`、`severity`、`source`、範圍 |

畫成 virtual text 的 finding 會被視窗**截斷**,而一則 severity 3、帶著修法的句子,正好就是會寫得很長的那種。行尾出現
`…` 的時候,要按的就是 `<C-w>d`。

**`zerg build` 與 `zerg lint` 是另一條讀它的路**,而且它們才是權威——server 由 `make lsp` held 住去對齊它們:

```sh
zerg lint examples/01_bindings.zg
# examples/01_bindings.zg:10:17: L502 the literal `2` is a float here — write `2.0` and the page shows it
```

**severity 3** 的 finding 是關於一個合法程式的「資訊」,不是錯誤。`examples/01` 與 `examples/03` 各帶著一則,因為兩者
存在的目的就是示範一個 literal 採用它所在位置的型別——`ratio: float = 2` 就是那一課,而 `L502` 是 linter 把它叫出名
字。它們編得過,也跑得動。

## quick fix 是編譯器的答案,不是 server 的

**code action** 是編輯器在一則診斷上提供的東西:一個具名的編輯,使用者按一個鍵就能套用。`L502` 有一個——finding 本來
就說了該寫什麼,那不如讓編輯器直接寫:

```zerg
x: float = 1 / 2      # 兩則 finding:這個 `1` 在這裡是 float,那個 `2` 也是
                      # 各自的 quick fix:Write `1.0`、Write `2.0`
```

有兩件事必須先成立,而兩件都還不成立:

- **finding 必須指在那個 literal 上。** `Diag` 帶的是**敘述**的位置,那是編譯器 marker 的粒度——而上面兩個 literal 在
  同一個敘述裡,所以一個被要求去修「那個 `1`」的編輯器,拿到的會是 `x` 的位置。整數 literal 現在帶著 token 自己的行與
  列,而那也正是讓同一行的兩則 finding 不會被去重成一則的東西。
- **替換文字必須來自編譯器。** 它跟訊息一起放在 `Diag.fix` 裡,因為兩者是同一個決定。一個從「write `1.0`」這句話裡把
  `1.0` 讀回來的 server,就是本頁要禁止的那份第二拷貝——而措辭改動的那一天,拷貝會把原始碼改寫成它剛好剖析得出來的
  東西。

沒有機械答案的 finding 不帶 `fix`,也就不提供 action。一個提供了 quick fix 然後什麼也不做的編輯器,比一個什麼都不提供
的更糟,因為使用者學到的是「這個選單會騙人」。

這個改寫**不是** `zerg fmt` 的工作。formatter 讀的是 token,而且必須能在編譯器編不過的原始碼上運作(見
[Formatter 與 Linter](fmt.zh-TW.md));要知道 `1` 變成了 `float` 需要型別,所以一個做這件事的 formatter,會剛好在人們
最需要它的那種 buffer 裡失效。它同時也是一個意見——`1.5 + 1` 是合法程式——而 formatter 沒有意見。

## 大綱是 parser 的清單,不是 server 的

`textDocument/documentSymbol` 是填滿編輯器大綱、麵包屑與 `gO` 的東西。它是唯一一個**不需要名稱解析**的互動答案——一個
宣告知道自己叫什麼、寫在哪裡——這也就是為什麼它做好了,而 `hover`、`definition`、`references` 沒有。

這一頁講的那條規則決定了它的形狀。編譯器回答 `file_symbols`,它走過一個被剖析的檔案,交出名字、**以「詞」表示的
kind**、以及位置;server 把那個詞對映到 LSP 的 `SymbolKind` 數字,除此之外什麼都不做。兩邊都不會漂進對方的工作:編譯器
如果把函式拼成 `12`,協定改號的那天就得改編譯器;而一個自己決定「什麼算是一個宣告」的 server,就是這一個沒有的那種
分析。

**一個專用的 `Symbol`,而不是把 AST 公開。** `FnDecl` 與它的兄弟們維持 private,跨過 `pub` 邊界的是一個小型別。為了
一串名字就把邊界擴大到每個宣告的每個欄位,等於把 parser 的形狀交到 server 手上,也給了它一個長大的理由。

**只有這個 buffer,不是整個程式。** 這裡其他每一個答案都是對「這個檔案 import 的模組」一起算的,因為借來的名字沒有
它們就是 undefined。大綱問的是相反的問題——這個檔案*裡面*有什麼——把 import 拉進來只會塞滿讀者在畫面上看不到的宣告。

一個剖析不過的 buffer 得到的答案是**什麼都沒有**,而不是一個錯誤。大綱是一個檢視,不是一個判決;而「這個 buffer 壞了」
這則診斷,每次按鍵都在跑的那個檢查早就發佈過了。

`make lsp` 把它held 到 `--emit ast`:大綱必須剛好叫出 parser 讀到的那些宣告。這個比對只在「不 import 任何東西」的檔案
上跑,因為 driver 在產生程式碼前會把整個程式併成一個 `File`——所以只有在沒有 import 時,那份 dump 才等於這個 buffer
自己的宣告;兩個問題不同的地方,dump 就不是 oracle,也就不問它。這個 gate 會數自己比對了幾個,理由跟這裡每一個下限
一樣。

**兩件它不做的事。** 一個 struct 的欄位與一個 enum 的 variant 不是子節點——協定有樹狀結構,而這裡是一份平的清單,反正
編輯器顯示的也是這個。以及 `range` 與 `selectionRange` 是同一個範圍(那個識別字的),因為編譯器對兩者都沒有結束位置
——跟診斷是同一個缺口。跳過去會落在名字上;client 沒辦法把游標所在的整個宣告標起來。

## 讓編輯器保持誠實

這棵樹裡其他每一樣東西都是靠**呼叫**編譯器來held 住的——`zerg fmt` 就是 formatter,而 server 是去問
`emit_files_diag`,不是自己檢查任何東西,所以沒有第二份會漂移的副本。編輯器檔案是唯一的例外,而且沒辦法不是:vim 是
從一份寫在 vimscript 裡的關鍵字清單上色的,而 nvim 必須在任何 Zerg 工具跑起來之前就知道怎麼縮排。

所以那些事實有自己的 gate——`make editor-align`:

- `lookup_keyword` 回傳的每個保留字都是 `zerg.vim` 有上色的,而它當作關鍵字上色的每個字也都是 lexer 保留的(內建的
  **型別**名改為 held 到 parser 的清單,因為 `int` 是個普通的 identifier,lexer 從沒聽過它);
- ftplugin 與 `.editorconfig` 設定的縮排**字元**,就是 `zerg fmt` 實際**寫出**的那個;
- 它們設定的縮排**寬度**,就是 `F403` 把一個 tab 算成的那個數。這不是裝飾:F403 判斷一行有沒有超過第 80 欄,是把 tab
  算成 `fmt_wrap_tab()`,所以把它顯示成別的寬度的編輯器,套用的是與 formatter 不同的 80 欄規則。一個數字、三個地方、
  一道 gate;
- ftplugin 畫的那條**尺**,是 `fmt_wrap_max()` 再往後一欄——也就是一個 flat group 必須在它之前結束的那一欄。一條畫錯
  位置的尺,看起來跟一條尺一模一樣,所以這一條是從 formatter 讀出來的,而不是再寫一次。

`.editorconfig` 是給這個 repository 沒有出 plugin 的那些編輯器用的——VSCode、JetBrains、Emacs、Zed 都會讀它——而且它
是held 到與 ftplugin 同一次探測,而不是held 到 ftplugin,這樣兩者才不會互相同意卻同時是錯的。

兩者都不是假想。`zerg.vim` 自己的註解記著 `close` 曾經「完全不在這份清單裡——那個結束一條 stream 的 statement 從來沒
有被上過色」,而那是用讀的發現的。而 ftplugin 設了 `expandtab` 與四格 shift,`F101` 卻是用**tab** 縮排、`make
fmt-self` 把樹裡每個 source 都held 在上面——所以一個在 nvim 裡打字的人產生出空白,下一次存檔又被轉成 tab:每寫一次就
一個整檔 diff,原因是編輯器與 formatter 對同一條規則的看法不同。兩者都在這裡修好了,而且現在都被量著。

這兩道 gate 表達的規則是:**server 不得知道任何編譯器能告訴它的語言事實;而當一個編輯器檔案不得不重複一項時,用一道
diff 把兩邊綁在一起。**

## 還沒做的,以及各自在等什麼

| 缺的                                          | 在等                                                |
| --------------------------------------------- | --------------------------------------------------- |
| `hover`、`definition`、`references`、`rename` | 沒有任何東西能把位置對映到宣告                      |
| `completion`                                  | 同一套 query surface                                |
| `semanticTokens`                              | `Kind` 的 variant 無法在 `zerg` module 之外被 match |
| `zerg lint` 發現的 **code** 作為資料          | 那些規則回答 `list[str]`,把代碼渲染進字串裡         |
| 診斷的**結束**位置                            | 編譯器追蹤一個東西從哪開始,不追蹤到哪結束           |
| `lint_files` 的 findings                      | 它們回答 `list[str]`,沒有位置可以放                 |
| 增量同步、debounce、取消                      | 一次量測;Phase 1 每次按鍵都重檢整個程式             |

第一列是真正的缺口,所有互動功能都卡在它後面。資訊是存在的——`check.zg` 全都算了出來——只是在 build 之後被丟掉。需要
的不是把那些型別一個一個公開,而是一個 **query surface**:給一個 path 與一個位置,那裡宣告了什麼、它在哪裡被宣告、它
的型別是什麼。

`semanticTokens` 是另一種缺,值得這樣點名:它會需要一張把 token kind 對映到 LSP token type 的表,而那正是上一節存在
就是為了防止的那種**重複的語言事實清單**。vim 語法檔已經在為 Zerg 上色,而且它有 gate。

最後一列是成本,不是缺口。scheduler 是協作式且非搶佔的,所以一次長檢查會佔住它的 worker 直到做完;`emit.zg` 的 9264
行是這個 repository 裡最壞的情況,也是在這裡設計任何東西之前該拿來量的數字。
