# Zerg Language Server

`zerg lsp`——編譯器回答編輯器的問題,而不是回答 shell 的。屬於[語言參考](../language.zh-TW.md)的一部分。亦有
[English](lsp.md) 版本。

```sh
zerg lsp        # 在 stdin/stdout 上講 JSON-RPC 2.0;由編輯器啟動與關閉
```

## 主張

language server **不是一個新程式**。它就是已經存在的那個編譯器,被問了另一個問題:不是*「把這個 lower 成 C」*,而是
_「這個 buffer 現在有什麼問題」_。後者編譯器一直都在回答——`check_files_diag` 正是 `zerg build --emit check`——所以這裡
交付的是把答案送到人正在看的地方的那段管線。

這同時也是不變式,而且它是被**強制**的,不是被宣稱的:

> **如果 server 和 `zerg build` 對一個程式的看法不同,錯的是 server。** 它沒有自己的分析。

`make lsp` 就是這句話變成的 gate。它為每個 example 與 40 個 corpus 程式在 stdio 上跑一次真的 session,把 server 發布
的東西held 到 `zerg build` 與 `zerg lint` 對同一個檔案說的話——error 對上會為此拒絕的那個命令,lint findings 對上會
回報它的那個命令。這是 `make oracle` 的論證套用在第二個前端上。

除此之外它還帶了 **protocol case**,而且每一個都曾經是壞的:exit status、shutdown 之後的回覆、空的變更、增量變更、
完整變更、`$/` notification 對比 `$/` request、格式錯誤的 frame、字串 id、一行 CJK 之後的 UTF-16 欄位、大於 runtime
bounded leaf 一次讀取量的 body、發佈在 `zerg build` 所指位置的 abort,以及
[quick fix](#quick-fix-是編譯器的答案不是-server-的)。那是另一種、也更安靜的失敗——buffer 被弄壞的編輯器,
或一個在乾等的 client,什麼都不會說。

## 它住在哪裡

`src/compiler/lsp/`——自成一個 module,像任何其他消費者一樣跨 `pub` 邊界 import `src/compiler/zerg/`,由 `zergc.zg`
裡多一行 `.sub(cmd.lsp_cmd())` 接上。啟動它的那個命令是 `src/compiler/cmd/lsp_cmd.zg`,跟另外五個放在一起。

**一個 binary,不是兩個。** 子命令讓編譯器與 server 之間的版本歪斜**物理上不可能**——它們是同一個檔案——而且編輯器不
需要 PATH 上多任何東西。

**獨立 module,而不是在 `src/compiler/zerg/` 裡多幾個檔案。** 目錄就是隱私單位,所以放進那個 module 裡,server 就能碰
到每一個 private 宣告,並且會像這棵樹裡每個能糾纏的東西一樣糾纏起來。強迫它走 `pub`,才換得到日後把它拆出去的選項。

**它不解析 `import`。** driver 已經知道 module 住在哪——環境變數、安裝根目錄、然後是 checkout——所以 `serve` 收一個
**函式**:給它一個 path 與一個 buffer 的文字,拿回那個 buffer 所屬的整個程式,其中該 buffer 的文字取代磁碟上的內容。
module 擁有協定;driver 擁有檔案系統。

## 已經做好的

| 請求                                                          | 由誰回答                                     |
| ------------------------------------------------------------- | -------------------------------------------- |
| `initialize` / `shutdown` / `exit`                            | session 本身                                 |
| `textDocument/didOpen` · `didChange` · `didSave` · `didClose` | 全文同步                                     |
| `textDocument/publishDiagnostics`                             | `lex_diags`、`check_lint_index`              |
| `textDocument/formatting`                                     | `fmt_src_off`——`zerg fmt` 呼叫的同一個函式   |
| `textDocument/codeAction`                                     | 一則 finding 帶著的 `fix`,包成一個 quick fix |
| `textDocument/documentSymbol`                                 | `file_symbols`——被剖析的檔案裡的宣告         |
| `textDocument/definition` · `references`                      | [名稱索引](#一個名字從同一份索引回答)        |
| `textDocument/hover`                                          | `doc_decl_at`——`zerg doc` 印的那份文件       |

`initialize` 為上表文件同步之外的**每一個請求宣告 capability**——`documentFormattingProvider`、
`codeActionProvider`、`documentSymbolProvider`、`definitionProvider`、`referencesProvider`、`hoverProvider`——外加
`textDocumentSync: 1`。其他每一個請求都會收到 **method-not-found 錯誤**,而不是沉默。一個在等永遠不會來的回覆的 client 會停止送下一個請求,然後編輯器就靜掉了,什麼也沒說。

**session 是一台狀態機,而 exit status 是它的一部分。** `shutdown` 之後 server 只接受 `exit`;之後才到的 request 會收
到 `InvalidRequest`,因為在等回覆的 client 會停止送下一個。`shutdown` 後的 `exit` 以 **0** 結束,沒有 `shutdown` 的
`exit`——或者標準輸入就這樣結束了——以 **1** 結束。永遠回 0 的 server,是在告訴 client 每一次崩潰都是乾淨關閉。

**同步是全文的,而不是全文的變更會被拒絕。** `textDocumentSync: 1` 表示 client 送整份文件,所以帶 `range` 的變更是
這個 server 從來沒要求過的增量編輯,把它的 `text` 套上去會把整份文件換成它自己的一個片段。**空的**
`contentChanges` 會讓 buffer 保持原狀,而不是換成空字串——那個旗標與空字串並不重複:client 把檔案清空時送的是 `""`。

**診斷是對整個程式檢查的**,不是只對 buffer。一個 import 了別的 module 的檔案必須連同那個 module 一起檢查,否則它借來
的每個名字都會讀成 undefined——會在正確的程式碼底下畫線的 server,是人會關掉的那種。

**而那是哪一個程式,是找出來的,不是假設的。** 編輯器只說了哪個檔案被打開,而被打開的檔案通常不是一個 entry:它是某個
目錄 module 的一個成員,它用到的型別、呼叫它的地方、以及它的第二個 source root,全都在這個檔案之外。把它當成 entry
讀,它會為隔壁檔案宣告的 struct 報 `E4056`、為 sibling 呼叫的 private function 報 `L102`、為一個坐在自己目錄旁邊而不
是裡面的 module 報 `E5002`——三句在說正確程式碼的話。所以 driver 會**去找一個能走到這個 buffer 的 entry**:先看 buffer
所在目錄的上一層裡直接放著的 `.zg` 檔,再往上一層,在第一個放了任何原始碼的層級停下,取第一個程式裡含有這個 buffer 的
檔案。「走得到」是由 loader 自己判定的——同一個 `module_files`、同一個 `module_at`——所以 server 從來不會對「什麼是一
個 module」長出第二個答案。當沒有任何東西走得到這個 buffer 時,這個 buffer **就是**它自己的 entry,而單檔程式、stdlib
module 與測試檔正好都是這一類。

這也是為什麼規則不是「buffer 所在的目錄就是一個 module」,即使那很誘人。目錄本身沒有任何東西能說明它是不是:
`src/stdlib/` 是一個目錄,裡面每個 `.zg` 檔各自是一個 module;`examples/` 是一個目錄,裡面是二十個各自獨立的程式。一
個目錄是在有東西 import 它的時候才成為 module,而只有從 entry 走一遍才知道這件事。

**四種嚴重度,來自兩個地方。** **error**——LSP 嚴重度 1——是檢查走訪回報、`zerg build` 會為此拒絕的東西,
也是編譯器自己的診斷唯一會用的嚴重度。線上其餘的一切都來自 linter 的規則,而那些每一個都是能 build 的**合法**程式,
所以沒有一個會是 error:把一個能動的程式塗成紅色的 server,是在教它的使用者忽略紅色。linter 自己的三個層級是有序的
——**finding** 會讓 `zerg lint` 失敗,**warning** 印出來但 exit 0,**info** 永遠不 gate 任何東西
([linter 的嚴重度](lint.zh-TW.md))——所以它們就照這個順序落在 LSP 剩下的三個上:

| `Finding.sev` | `zerg lint` 印出  | LSP 嚴重度      |
| ------------- | ----------------- | --------------- |
| `""`          | `L103 …`          | 2 — warning     |
| `"warning"`   | `warning: L601 …` | 3 — information |
| `"info"`      | `info: L106 …`    | 4 — hint        |

這兩欄的字面意思對不上,而且本來就不該對上:左欄決定的是一個**命令的 exit status**,右欄是**編輯器**要畫多大聲。這個
對映只寫在一個地方,也就是 `ls_severity`,而 `make lsp` held 住它——gate 會把每一則發布出去的診斷重組成 `zerg lint`
本來會印的那一行,連形容詞一起,所以一個把三個層級壓成同一個嚴重度的 server 會失敗,而不是在每個數字上都同意。

**代碼是以代碼的身分傳遞的。** `Diag` 用一個自己的欄位帶著規則的身分——`E3006`、`L502`——所以 server 送出 LSP 的
`Diagnostic.code`,編輯器可以據此過濾、分組與連結。那正是這一頁講的規則套用在它自己身上:只拼在句子裡的代碼,是每個
讀者都得再解析一次出來的東西,而 server 去解析它就會是一份語言事實的第二份拷貝。沒有代碼的 finding 會省略這個欄位,
而不是送一個空的。

**abort 也帶著一個,而它是上一段唯一的例外。** 一個 `raise` 沒有結構可以放代碼,所以建立它的那個管道把代碼寫進句子
的最前面——於是一則 abort 到了編輯器就成了一則**沒有**規則的 finding,旁邊卻擺著有規則的 checked finding。server 把
把它拆回來的是 `rule_msg_code`,它就住在當初把它打包起來的 `rule_msg` 旁邊:一個 `E`、數字、一個空格,不讀更寬鬆的
形式——那正是 `rule_code` 送出來的東西——因為開頭是別的東西的訊息就是沒有代碼,而一個比它的寫者更寬的讀者,會一直接受
一種沒有人產生的拼法。

**一則 finding 畫的線是編譯器指名的那個構造。** checked 診斷回報在 parser 放在每個 statement 前面的那個位置標記上,
而那個標記除了開始之外也帶著 statement **結束**的地方——所以畫的線就是那個 statement。一條自己知道地點的規則知道的
就只有一個地點,它的 `Diag` 就以「完全不帶結束」來說這件事,而不是把開始重複填進去;server 這時畫的是那一欄上的那個
字,那是一個猜測,而且被標成猜測。見[位置](#位置)。

**abort 落在它的句子所說的地方。** parse error 與 `NotImplemented` refusal 都是被 `raise` 的句子,不是 `Diag`,
地點就在句子裡:`zerg build` 印在它底下的最後一行 `--> file:line:col`。當那一行指名的是這個 buffer 的檔案,finding
就發佈在那個位置,而那一行會從訊息裡拿掉,因為 range 已經說了。只讀這個形式,不讀更寬鬆的——沒有路徑的
`--> line:col` 不知道是哪個檔案。

有兩種 finding 改以檔案頂端一個零寬度的 range 落地,理由各不相同。**沒有指名地點**的,落在那裡是因為編譯器沒說在哪。
地點在程式裡**另一個**檔案的,落在那裡是因為那個地點不在這個 buffer 裡,而且它保留自己的 `-->` 那一行——這時只有那
一行說得出該往哪看。兩種都不是 1:1 上那個字:畫在 `fn` 底下的線是在說 `fn` 錯了,而那正是兩者都沒有說的事。

## 位置

編譯器回答的是 1-based 的行與 1-based 的**位元組**欄,標記一個東西開始的地方。LSP 要的是 0-based 的行、以 **UTF-16
code unit** 計的 0-based character,以及一個 **range**。這道轉換的兩半都是 server 的,而且兩者都不是可選的:

- **單位**,因為位元組欄與 UTF-16 欄只有在一行全是 ASCII 時才一致,而這棵樹自己的 source 到處是 em-dash;
- **range**,因為一個地點不是一個範圍。

編譯器**有結束位置**的地方,就用它,什麼都不導出。每一個宣告與每一個 statement 都帶著一個:parser 建立它們的時候正
站在那個收尾的 token 上,所以結束就是那個 token 最後一個位元組再過去一欄,是讀來的而不是猜來的。另一個被提出來的做
法——下一個宣告的開始——只要兩個宣告之間有空行、註解或一個收尾的大括號就是錯的,而且錯在會吞掉不屬於任何人的文字那
個方向。

**沒有**結束位置的地方——一條自己知道地點的規則、一則關於單一 literal 的 lint note、一則地點是從自己句子裡讀回來的
abort——range 就是那個位置上的字:那裡有 identifier 就取它,否則一個字元。那是一個猜測,它做在 server 裡,也就是這
一頁說得出「這是猜測」的地方,而且它永遠不會被寫進 `Diag`、假裝是編譯器說的。一個讀 `Diag` 的人因此分得出兩者,而
把開始複製進兩端就分不出來了。

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

| 設定                       | 是什麼                                         | held 到什麼      |
| -------------------------- | ---------------------------------------------- | ---------------- |
| `noexpandtab`、`tabstop=4` | 一層一個 tab,顯示四欄                          | `F101`、`F403`   |
| `colorcolumn=121`          | `F403` 換行預算之後的第一欄                    | `fmt_wrap_max()` |
| `foldexpr` / `indentexpr`  | 一行所觸及的最低分隔符深度                     | 同一個掃描器     |
| `makeprg` / `errorformat`  | `:make` 跑 `--emit check`,並讀得懂兩種診斷形狀 | 編譯器自己的輸出 |

**摺疊與縮排是同一條規則,問了兩次。** 一行的層級是它觸及的最低分隔符深度——這讓一個區塊被摺起來時,包住它的兩行都
留在畫面上(上面的 `fn f() {` 與下面的 `}`),也讓 `}` 在打出來的當下就自己 dedent。差別在分隔符:摺疊只數大括號,
因為一個被拆行的參數列不是一個 fold;縮排數 `(`、`[`、`{` 三者,因為 `F403` 與 `F404` 在三者裡面都會縮排。

兩者原本都不存在。`indentexpr` 是空的,`autoindent`、`smartindent`、`cindent` 也都是,所以 `fn f() {` 之後按 `<CR>`,
游標停在第 1 欄,每一層都是人自己按出來的 tab——然後 formatter 在下次寫入時把它整理好,也就是說這個檔案只有在工具跑過
之後才是對的。現在它是對的這件事,是用 `gg=G` 掃過整個 repo 檢查的:對 formatter 寫出來的每一份原始碼重新縮排,必須
什麼都不改——而找出它真的改了的那兩種情況(一條被拆行的 `+` 鏈,與一行以 `# >>>` 結尾的 doctest 註解),就是這條規則
被塑造出來的過程。

**`:make` 是 quickfix list 裡的編譯器**,它值得跟 language server 並存,因為兩者的失敗方式不同:程式在 buffer 以外
的檔案 abort 時,server 只能把那則 finding 發佈在 buffer 頂端,而 `:make` 會跳到編譯器所指名那個檔案裡的位置。

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

## Claude Code

coding agent 是又一個 client。checkout 根目錄的 `.claude-plugin/marketplace.json` 列出一個 plugin `zerg-lsp`,它的
全部內容就是為 `.zg` 檔啟動 `zerg lsp` 的那一行——與官方 `gopls-lsp` plugin 同樣的 inline `lspServers` 條目。

```text
/plugin marketplace add ./
/plugin install zerg-lsp@zerg
```

裝完後重開 session;server 是在 session 開始時載入的,不是在啟用時。

**agent 拿到的,就是人拿到的。** 每次編輯一個 `.zg` 檔之後,agent 會收到 `zerg build` 與 `zerg lint` 本來會印出的
diagnostics,而不必執行其中任何一個;它的 go-to-definition、find-references、大綱與 hover,就是上面的 `definition`、
`references`、`documentSymbol` 與 [hover](#hover-是宣告的文件),所以不必打開宣告所在的檔案,就讀得到它的文件。
workspace symbol 沒有做,server 會用 method-not-found 錯誤這麼說,而不是給一個空答案。

**它執行 `PATH` 上的 `zerg`,理由與 nvim 相同**——server 就是編譯器,所以裝好的 toolchain 就是裝好的 server。代價正是
這個 checkout 最容易碰上的那一個:一個在編輯 `src/compiler/` 的 agent,是被最後一次安裝的編譯器檢查的,不是它分支上的
那一個。分支上改掉的規則,在 `make install` 之前 server 都不知道,所以對編譯器自己的原始碼,`make build` 仍然是答案,
diagnostic 只是提示。request 本身也一樣:比 definition 與 references 更舊的安裝,在 `initialize` 裡兩者都不宣告,而
agent 沒有辦法要求一個從未被提供的東西。

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
[格式化器規則](fmt.zh-TW.md));要知道 `1` 變成了 `float` 需要型別,所以一個做這件事的 formatter,會剛好在人們
最需要它的那種 buffer 裡失效。它同時也是一個意見——`1.5 + 1` 是合法程式——而 formatter 沒有意見。

## 大綱是 parser 的清單,不是 server 的

`textDocument/documentSymbol` 是填滿編輯器大綱、麵包屑與 `gO` 的東西。它是唯一一個**不需要名稱解析**的互動答案——一個
宣告知道自己叫什麼、寫在哪裡——這也就是為什麼它最先做好。

這一頁講的那條規則決定了它的形狀。編譯器回答 `file_symbols`,它走過一個被剖析的檔案,交出名字、**以「詞」表示的
kind**、以及位置;server 把那個詞對映到 LSP 的 `SymbolKind` 數字,除此之外什麼都不做。兩邊都不會漂進對方的工作:編譯器
如果把函式拼成 `12`,協定改號的那天就得改編譯器;而一個自己決定「什麼算是一個宣告」的 server,就是這一個沒有的那種
分析。

**一個專用的 `Symbol`,而不是把 AST 公開。** `FnDecl` 與它的兄弟們維持 private,跨過 `pub` 邊界的是一個小型別。為了
一串名字就把邊界擴大到每個宣告的每個欄位,等於把 parser 的形狀交到 server 手上,也給了它一個長大的理由。

**只有這個 buffer,不是整個程式。** 這裡其他每一個答案都是對「這個檔案 import 的模組」一起算的,因為借來的名字沒有
它們就是 undefined。大綱問的是相反的問題——這個檔案*裡面*有什麼——把 import 拉進來只會塞滿讀者在畫面上看不到的宣告。

**一個 struct 的欄位與一個 enum 的 variant 是子節點。** LSP 的 `DocumentSymbol` 是一棵樹,大綱是一個程式的檢視,而
一份平的清單就只是一串名字。欄位帶著自己的型別當 detail,variant 帶著自己的 payload,那正是讀者掃過清單時據以知道哪
一個 arm 綁了東西的資訊。兩份清單都不排序:一個 variant 的順序就是它的 tag,在這裡重新決定它,等於讓大綱跟程式對
「哪一個 variant 在前面」講不同的話。

**`range` 是構造,`selectionRange` 是名字。** 協定要的是兩個,而它們是兩個不同的範圍:外面那個讓 client 說得出游標
在哪一個宣告裡面,裡面那個是跳過去會落下的地方。各自在只有地點的時候退回那個位置上的字,而不是發明一個結束——語言
自己宣告的 `init` 就沒有名字 token——而一個落在宣告自己第一欄上的字,無論如何都在它的範圍裡面。

**`pub` 是宣告的一部分,所以它在範圍裡面。** 協定說 `range` 涵蓋的是這個宣告「包含例如註解與程式碼」,而 GRAMMAR
把這個標記放在每一條接受它的產生式裡面——`'pub'? 'struct' …`。一個模組常數與一個 struct 欄位本來就從自己的標記開始;
一個 `struct`、一個 `enum`、一個 `spec`、一個 `type`、一個 `fn` 與一個 `import` 的 re-export 晚一個 token 才開始,
於是同一個標記對兩種形式在範圍裡、對六種在範圍外。現在對每一種接受它的形式都在裡面,而一個宣告的地點——關於它簽名
的診斷回報的那一個——就是那第一個 token。`selectionRange` 仍然指著名字。

**一個 decorator 不在裡面,而那是一個限制,不是一條規則。** GRAMMAR#decorated-decl 也把 `#[…]` 放在產生式裡面,所以
照上一段的說法它屬於這個範圍;但一個有 decorator 的宣告,它的範圍是從它的 `pub` 或它的 `fn` 開始的。一個 decorator
是當成自己的最上層項目讀的,在它底下那個宣告存在之前就讀完了,所以那個宣告是在從沒見過它的情況下建起來的——要把它折
進來,改的是這兩者怎麼配對,不是範圍從哪裡開始。寫在這裡,是為了讓這個缺口被說出來,而不是被發現。

**一個 `impl` 的範圍包住它的方法們的,而且兩者都在最上層。** parser 把一個 `impl` 的主體攤平成帶著 receiver 的普通
函式,所以一個方法是它的 `impl` 旁邊的符號,而不是它的子節點——這個大綱的子節點是一個 struct 的欄位與一個 enum 的
variant,目前就只有這些。後果是看得見的:游標在一個方法裡面,就同時在兩個最上層符號裡面,而一個把位置解析成「包住
它的那個符號」的 client 會有兩個答案可挑。那就是編譯器手上那個東西誠實的形狀;讓方法變成子節點才會把這個重疊拿掉,
而那還沒做。

**一個剖析不過的 buffer 不會得到一份空的大綱。** 它以前會,而 `[]` 是一個什麼都沒宣告的檔案的大綱——於是最後一次按
鍵弄壞剖析的編輯器把大綱面板清空,而理由哪裡都沒說,跟一個在這個請求上崩掉的 server 分不出來。現在它改成讓這個請求
**失敗**,以 `RequestFailed`(-32803),帶著編譯器自己的句子:那是協定用來說「一個格式正確的請求現在回答不了」的方
式,也是唯一一個讓 client 可以繼續顯示手上那份大綱的答案。同一次按鍵跑的那個檢查也會把同一個句子當診斷發佈出去;這
不是第二則 finding,這是大綱選擇不回答,而不是說謊。

協定還允許第三個答案 `null`,而它被否決的理由跟 `[]` 一樣:client 分不出「我讀不了這個 buffer」與「這個 buffer 什麼
都沒宣告」,所以那只是同一種沉默換個拼法。改成失敗的代價是它**看得見**——一個預設 handler 會把請求錯誤顯示出來的
client 就會顯示一則,而 nvim 的就會——而那正是重點,不是副作用:大綱要變舊了,就該說出來。

`make lsp` 把它held 到 `--emit ast`:大綱必須剛好叫出 parser 讀到的那些宣告。這個比對只在「不 import 任何東西」的檔案
上跑,因為 driver 在產生程式碼前會把整個程式併成一個 `File`——所以只有在沒有 import 時,那份 dump 才等於這個 buffer
自己的宣告;兩個問題不同的地方,dump 就不是 oracle,也就不問它。這個 gate 會數自己比對了幾個,理由跟這裡每一個下限
一樣。

這個 gate 的後半問的是每一筆對一個宣告**說了什麼**,而它問的方式是拿回傳的 range 去**切那個 buffer**,再把切出來
的文字跟寫下去的比對。拿手寫的行號與 character 去比,在一個把結束的 UTF-16 轉換搞反的 server 上也會過——那些數字就
是這個 gate 被告知要期待的數字——所以 fixture 裡到處是中日韓文字,而斷言講的是文字。

## 一個名字從同一份索引回答

`textDocument/definition` 與 `textDocument/references` 是**同一張表**的兩個視角:位置到宣告的索引 `NameIndex`。游標下
的使用點指向一個宣告,而一個宣告的 references 是每一個指向它的使用點。hover(#192)與 completion、signature help、
workspace symbol、rename(#194)讀的是同一張表;它們沒有一個自己解析名字,而索引答不了的問題,是索引該長大的理由,不是
某個 handler 去走一遍程式的理由。

**它記錄在編譯器解析每個名字的地方。** 檢查走訪本來就會為它 lower 的每個名字決定它指的是哪個宣告——仍在範圍內最內層的
綁定、dispatch 選中的 method、target 型別所指 struct 的 field。設了 `want_index`,它就在同一行把那個決定寫下來,所以索引
為一個使用點給出的宣告,就是編譯器把它 lower 成的那一個——在做出那個決定的分支裡寫下。這一頁的規則,套用在名字上。

**有兩種答案是推導出來而不是記錄下來的,因為編譯器從不解析它們。** 型別名是走訪之後在檢查器讀的那份型別表裡查的,因為沒有
任何 lowering 經過型別;型別參數是對到範圍涵蓋它的最內層宣告,因為替換在任何東西能解析它之前就把它拿掉了。兩者都由
`make lsp` 的位置與 rename 性質 held 到編譯器。

**它是同一趟走訪。** `check_lint_index` 就是也交回索引的 `check_and_lint`,成本是記錄,不是走訪,而 `make lsp` 的一趟走訪
案例量的正是建出索引的那次檢查。build 不要索引,每個記錄點只付一次判斷——走訪旁的那些表連一欄都不會多長。

**鍵是宣告的名字 token**——它的檔案、行號與 byte 欄位。泛型的特化是保留樣板位置的副本,所以一個以兩種型別呼叫的樣板是
**一個**宣告,每次呼叫都在它的 references 裡。宣告被記成自己的使用點,所以一個名字的 references 包含它被宣告的地方,游標
停在宣告上也有答案。

| 游標下的名字             | 回答                                                  |
| ------------------------ | ----------------------------------------------------- |
| 綁定、參數               | 仍在範圍內最內層的綁定——遮蔽已被解析                  |
| closure 捕獲的名字       | closure 複製的那個綁定                                |
| 呼叫、函式值             | 那個函式;泛型則是它的樣板                             |
| 呼叫裡的 `name: value`   | 它指名的參數;在建構裡則是 field                       |
| method                   | dispatch 選中的實作                                   |
| 泛型程式碼裡的method     | 型別參數的 bound 宣告了它時,是 spec 的 requirement    |
| 一個本體兩種解析的method | 兩個實作共同遵守的 spec requirement                   |
| field、`?.`、variant     | field、variant、associated function 或值              |
| 一個本體兩種解析的field  | 它解析到的每一個 field——兩種型別的泛型 `x.n` 是共用的 |
| namespace、`ns.f`        | 在這個檔案裡綁定它的 `import`,以及它指的成員          |
| 型別名、型別參數         | 宣告;範圍涵蓋該使用點的那個 `[T]`                     |
| 內建、intrinsic          | 沒有——`null`,對一個沒人寫過的名字這才是誠實的答案     |

**中止的檢查會丟掉它。** 每次檢查都取代 session 唯一的那一格,只有走到終點的走訪才會存一份新的。一個不再能 lex、載入或
lower 的 buffer,兩個請求都回 `null`,直到下一次完成的檢查——而不是回答一個編譯器已不再認為是這個程式的位置。

**位置只轉換一次。** 編譯器記錄 1-based 行號與 byte 欄位;索引在存下時就轉成 0-based 行號與 UTF-16 欄位,用的是診斷用的
同一條規則(`ls_utf16_from`),而原始碼不保留。留下的是整數欄與宣告的名字——在編譯器自身程式上是幾 MB,相對於峰值數百 MB 的
一次檢查。

**共用的使用點以它可能是的每一個宣告回答。** 泛型本體每個實例化走一次,而它經由型別參數讀的 field——以 `P` 與 `Q` 呼叫的
`x.n`——在一次走訪解析成 `P.n`,另一次解析成 `Q.n`。索引把兩者都留作候選,所以 `definition` 回答一個位置的**清單**(LSP
允許),而這個使用點出現在每一個候選的 references 裡。兩者都不是「先被走訪的那個」:答案不能取決於實例化的順序。以兩種方式
解析的 method,則在有共同 requirement 時以它回答。所以只有一個宣告的名字,`definition` 回答**一個** `Location` 物件;
共用的使用點回答一個 `Location` **清單**,依各自宣告的位置排序——檔案、行、欄。

`make lsp` 用三種方式把它 held 住,每一種抓不同的錯誤索引。手寫的**位置**:被遮蔽的綁定、旁邊有同名 local 的參數、以兩種
型別呼叫的泛型 method、經由 spec bound 的呼叫、一個本體兩種解析的 method、field 與具名引數(各自以反過來的實例化順序再問
一次,因為答案不能取決於哪個先被走訪)、經由被綁定遮蔽的型別取的 variant 與 associated 名字、closure 的捕獲、`impl` 的
型別參數、具名引數、建構的 field、`?.` field、解構出的綁定、另一個檔案的成員、namespace、標準函式庫、intrinsic,以及一行
CJK 之後的名字。**對稱性**:fixture 裡每一個有 definition 的名字,都在那個
definition 的 references 裡,而且沒有宣告在同拼寫的名字回不出答案時還一個 reference 都沒有——那是被丟掉的使用點留下的形狀。
共用的使用點是從索引的回答推導出來的,必須恰好是 fixture 寫的那些,兩個方向都要。
以及 **rename**,held 到 `zerg build --emit check` 與程式印出的東西:把一個宣告與它每一個 reference 改成新名字,兩者都必須一
樣;改掉除了宣告以外的每一個 reference,則不能 build——索引漏掉的 reference 是留在舊名字底下的使用點,歸錯綁定的則會讀到
別的值。共用的使用點與它可能是的每一個宣告一起改名,當作一組。rename 略過的東西——標準函式庫、import 的路徑、契約——每一種
理由都有下限,所以一個越長越大的過濾器沒辦法靠什麼都不改名讓這項性質變綠。

**它不做的事。**

- **讀一則註解。** 索引找到宣告就停在那裡;那個宣告帶著的文件是 [hover](#hover-是宣告的文件),在被問到時才抽取。
- **completion、signature help、workspace symbol、rename。** 每一個都是這份索引的一個視角,每一個都是 #194。
- **沒有人實例化的樣板。** 泛型本體每個特化走一次,所以沒有呼叫抵達的樣板從未被走訪,它的名字沒有條目。
- **契約。** spec 的 requirement 與每個遵守它的 method 是各自的宣告。單獨改名其中一個,依設計就會弄壞程式,而 rename 要怎麼
  處理契約是 #194 的決定。
- **or-pattern 綁的名字。** `A(x) | B(x)` 在兩側各綁一次 `x`,本體讀的是匹配到的那一側——一個名字兩個宣告,所以回 `null`,
  而不是其中一個。
- **共用使用點的單一宣告。** 泛型本體以兩種方式解析的 field、具名引數、variant pattern,或沒有共同 requirement 的
  method,回答它解析到的每一個宣告,而不是先被走訪的那個實例化。rename 該帶走哪一個,是 #194 的決定。

## hover 是宣告的文件

`textDocument/hover` 是游標停在一個名字上時編輯器會顯示的東西。[索引](#一個名字從同一份索引回答)說那個名字是哪一個
宣告;顯示出來的則是 **`zerg doc` 為它印的那份文件**——寫在它上面的註解,以及編譯器拼出來的簽名。

> **這棵樹裡只有一個讀註解的人。** 一個自己去掃 `#` 的 hover,會是一份終端機沒有的文件,也是一份編輯器沒有共享的文件。

所以文字來自 `doc_decl_at`,那就是 `zerg doc` 自己的抽取,只是問的是一個位置而不是一整個 module:同一套附著規則——直接
寫在宣告上方的整行註解、decorator 不算斷開、banner 什麼都不認領([哪一則註解記錄哪一個宣告](doc.zh-TW.md#哪一則註解記錄哪一個宣告))
——同一個編譯器型別印表器印出的簽名,以及沒寫任何東西時同一個 `(undocumented)`。一次抽取,兩個讀者。

**它為每一個宣告作答,而 `zerg doc` 只為暴露出去的那些作答。** 那是一趟走訪的兩個問題,不是兩個答案:文件是一個 module
_暴露_ 了什麼,而游標停在作者正在看的任何東西上——編譯器自己的原始碼幾乎全是私有的,一個在裡面就靜掉的 hover,會是一個
只能用來讀別人程式碼的工具。

**索引帶的是位置,不是文字。** 每個宣告留一份註解,就是每一列留一個字串、整個 session 都握著,而且不管有沒有人 hover
每次檢查都要付——那正是 #23 在量的那種累積。存下來的是 `definition` 本來就需要的東西,文件則在問題被問到時才抽取:
**把宣告所在的那一個檔案剖析一次**,事後不被任何東西留著。從不 hover 的 session 不為這件功能付出任何東西。

**峰值是檢查的,而 hover 不會往上加。** 以這個編譯器自己的進入點為根的程式檢查一次,峰值是 **0.156 GB**;同一次檢查
後面接上一次、十次與一百次 hover,峰值是 0.155、0.162 與 0.156——這個散佈不比同一項量測跑兩次之間的散佈更大(0.156
與 0.153)。五次檢查沒有 hover 是 **0.188 GB**,五次檢查加五次 hover 是 0.182:再多一次檢查的代價,比一百次 hover 還大。
一個**請求**留下來的殘留低於峰值能看見的尺度——三百個 `definition` 請求把峰值留在一次檢查放的地方(0.154 對 0.156)
——而更細的量測在那裡找到的東西,一個 `definition` 請求留下的和一次 hover 一樣多:那是請求路徑的殘留,不是這個答案的。

**原始碼是 loader 的。** 宣告常常在別的檔案裡——標準函式庫的一個函式、module 的一個兄弟檔——所以整個程式會照檢查載入
它的方式載入,並以尚未存檔的 buffer 頂替磁碟上的內容。因此 hover 回答的是正在被打出來的那份文字,而一個 raise 掉的
載入就只回答索引知道的事,不多說。它只為 client **開著**的 buffer 作答,因為那份 buffer 就是文件被讀出來的地方。

**而且只有在它在別處時才會去取。** 游標底下那個名字的宣告,多半就寫在游標底下那個檔案裡,而那份文字 client 已經送來了
——也正是 loader 會拿來頂替的同一份——所以只有當宣告在**另一個**檔案裡時才會載入整個程式。對文件根本不可能收錄的
kind,則完全不載入:`doc_covers` 是抽取自己對這半個問題的答案,在付出取得原始碼的代價**之前**問,而不是在從裡面什麼都
沒讀到之後才問;所以對一個區域綁定或一個參數 hover——那是函式本體裡大多數的識別字——什麼都不載入。剩下那個情況的代價
值得寫下來:對以這個編譯器自己的進入點為根的程式,一次必須載入的 hover 大約要半秒,幾乎全部花在把 buffer 再變成那個
程式上;那一個檔案的剖析是比較小的一半,而單檔程式是即時的。

**它是 markdown。** LSP 的 `MarkupContent` 兩種都收,而這棵樹裡的一則 doc 註解本來就是帶著 ` ```zerg ` fence 的散文,
所以它原樣送出,編輯器就會把例子渲染成例子。簽名放在散文上方自己的一個 fence 裡,那是讀者在找的那一行。純文字會把每個
fence 顯示成三個反引號。

| 游標停在                                 | hover 顯示                                       |
| ---------------------------------------- | ------------------------------------------------ |
| 一個有文件的宣告                         | 它的簽名,然後它的註解——`zerg doc` 印的那個條目   |
| 一個沒有註解的                           | 它的簽名,然後 `(undocumented)`——文件印的那個標記 |
| 一個 field、variant、spec 的 requirement | 文件為它印的那一行,然後它自己的註解              |
| 一個 method                              | 文件拼寫它的那個簽名,receiver 去掉               |
| 一個私有宣告                             | 一樣,即使沒有任何文件列出它                      |
| 一個綁定、一個參數                       | 它是什麼,以及它叫什麼                            |
| 一個型別參數、一個 namespace             | 一樣                                             |
| 一個內建、intrinsic                      | 什麼都沒有                                       |

一個綁定**不會**被標成 `(undocumented)`。沒有人能為它寫註解,所以那個標記會是對作者的抱怨,而不是關於程式碼的事實;
它拿到的是說明它是什麼的那個詞——`binding`、`parameter`、`type parameter`、`import`——以及它的名字。一個**私有**宣告
會被標記,而那是同一條規則,不是它的例外:那個標記回答的是一個宣告有沒有帶著文件文字,而一個私有函式本來就可能帶著。
它講的是作者寫了什麼,不是頁面列了什麼。

**一個共用的使用點會顯示它可能是的每一個宣告**,順序與 `definition` 列出來的一樣,一個接一個。挑其中一個的 hover,
會是編輯器說了跳轉不會說的話。

**還有三種答案是沒有答案:** 沒有人宣告過的名字、這個 session 從未打開過的檔案裡的位置,以及檢查**中止**了的 buffer
——每次檢查都會換掉那唯一一份索引,而且只有走到底的走訪才會存下新的一份,所以一次拒絕之後的 hover 會是一份編譯器已經
不認為是這個程式的文件。

`make lsp` 把兩份文字 held 在一起,當作一項**性質**而不是一份逐字稿:它 hover 的每一個宣告,都會向 `zerg doc` 問同一個,
然後比對簽名,以及它底下的註解。那個條目是用**案例問的那個被宣告的名字**找出來的,絕不是用 hover 答出來的文字,而且
同一個名字只能有一個條目——兩個印出同一個簽名的宣告(`log` 有一個 `Logger.trace` 和一個自由函式 `trace`)否則會讓一個
以答案為鍵的查找挑到錯的條目,然後自己跟自己說得一樣。

散文是**一段一段**比的,空白折平,因為 `zerg doc` 會把散文折到一頁寬而 hover 不會;而 **fence 是一行一行**比的,因為
註解裡的一個 ` ```zerg ` 區塊是程式碼,它的每一行不是拿來重新折行的散文。兩者的**順序**也一起比:區塊照它們出現的次序
對齊。兩邊都折平的話,一個把註解併成一行的 hover 會在每一個詞上都說得一樣,卻毀掉裡面每一個段落分隔與每一個例子。

它也 held 住那些不是文件的答案的形狀——那個標記、它在一個綁定上的缺席,以及它在一個沒有註解的成員上的缺席,兩者 fixture
都寫了也都 hover 了——並把 hover held 到索引本身:對**開著的 buffer** 裡的每一個識別字,hover 作答的名字必須恰好就是
`definition` 作答的那些。只限開著的 buffer,而那正是兩者依設計唯一不同的地方:hover 是從 client 送來的那份文字讀出來的,
所以這個 session 從未打開過的檔案裡的位置沒有 hover,而 `definition` 對程式裡的任何檔案都從索引作答。用第二種方式讀註解
的 hover 會在某個詞上對不上;回答型別而不是文件的,則每一個詞都對不上。

## 一份被寫了兩次的文法

`editors/tree-sitter-zerg` 是 Zerg 的 **tree-sitter** 文法——一個真的 parser,給那些要的是一棵樹而不是一組樣式的編輯器。

```sh
make -C editors treesitter    # 產生、建置、安裝 parser 與它的 queries
:lua vim.treesitter.start()   # 在一個 .zg buffer 裡
```

**它打破了這一頁的規則,而且沒辦法不打破。** 這裡其他每一樣東西都是靠呼叫編譯器來held 住的;而編輯器檔案不得不重複一
條語言事實的地方,有一份 diff 把兩邊綁在一起。一份 tree-sitter 文法是 `GRAMMAR` 的**第二份實作**——大約一百條產生
式——而沒有任何東西能拿一條 tree-sitter 規則去 diff 一條 BNF 產生式,或去 diff `parser.zg`。

所以held 住它的是一個 **corpus**:`make treesitter` 會剖析這棵樹裡的每一個 `.zg` 檔——編譯器自己的原始碼、標準函式
庫、examples,以及 private corpus(有 checkout 的話)——只要出現一個 `ERROR` 或 `MISSING` 節點就失敗。這比看板上其他
gate 都弱,而且弱的方式跟 `fmt-corpus` 一模一樣:它只看得見某個檔案裡真的有的形式。對一份被寫了兩次的文法來說,這是
拿得到的最強檢查,也是為什麼那份檔案清單是「全部」而不是抽樣。有一部分**是**可以 diff 的,而且真的 diff 了——
`editor-align` 把這份文法的關鍵字清單held 到 `lookup_keyword`,跟它早就對 vim 檔做的是同一件事。

**它換來什麼。** `syntax/zerg.vim` 是用正規表達式上色的,而且在自己的註解裡承認了那個承重的猜測:`\<\u\w*\>` 讓每
一個大寫開頭的字都是型別,「這是一個上色的啟發式,不是文法規則」,因為一個不會剖析的上色器分不出型別、variant 與建構
子呼叫。一個 parser 不用猜——小寫的型別名第一次被正確上色,f-string 的洞被當成它們本來就是的運算式上色,而摺疊跟著結
構走,不是跟著大括號走。

**產生出來的 parser 沒有進版控。** `grammar.js` 產出將近七 MB 的 C,比這個 repo 其餘部分加起來還大,而且完全是從一個
已經在 review 裡的檔案導出的。`make -C editors treesitter` 會寫出它;`.gitignore` 把它擋在外面。這也是為什麼它是自己
的一個 target,而不是 `make -C editors install` 的一部分:它需要 node,而這套 toolchain 不需要——一個因為缺了編輯器
工具而失敗的 install,是更差的交換。

**有兩件事它需要一個 scanner。** 換行是敘述分隔符(`GRAMMAR#stmt-sep`),而在一個群組裡面它又沒有意義,這就是標準的
自動分號問題:scanner 只會被問「這裡 parser 收得下哪些 token」,所以換行剛好在一個敘述可以結束的地方變成分隔符。以及
字面值的內容不是程式碼——`comment` 是一個 `extra`,所以它在每一個位置都是候選,而 `f"{recv}#{name}"` 裡的 `#` 比任何
字串規則都匹配得更長,把結尾的引號一起吞掉了。token 優先權沒有解決它,`immediate` token 也沒有;「先被問到」有。

## 讓編輯器保持誠實

這棵樹裡其他每一樣東西都是靠**呼叫**編譯器來held 住的——`zerg fmt` 就是 formatter,而 server 是去問
`check_lint_index`,不是自己檢查任何東西,所以沒有第二份會漂移的副本。編輯器檔案是唯一的例外,而且沒辦法不是:vim 是
從一份寫在 vimscript 裡的關鍵字清單上色的,而 nvim 必須在任何 Zerg 工具跑起來之前就知道怎麼縮排。

所以那些事實有自己的 gate——`make editor-align`:

- `lookup_keyword` 回傳的每個保留字都是 `zerg.vim` 有上色的,而它當作關鍵字上色的每個字也都是 lexer 保留的(內建的
  **型別**名改為 held 到 parser 的清單,因為 `int` 是個普通的 identifier,lexer 從沒聽過它);
- ftplugin 與 `.editorconfig` 設定的縮排**字元**,就是 `zerg fmt` 實際**寫出**的那個;
- 它們設定的縮排**寬度**,就是 `F403` 把一個 tab 算成的那個數。這不是裝飾:F403 判斷一行有沒有超過第 120 欄,是把 tab
  算成 `fmt_wrap_tab()`,所以把它顯示成別的寬度的編輯器,套用的是與 formatter 不同的 120 欄規則。一個數字、三個地方、
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

追蹤在 issue [#15](https://github.com/cmj0121/zerg/issues/15),它是兩半共同掛靠的傘:每次檢查的成本,以及一個能回答
「一個名字在哪裡被宣告」的索引。

| 缺的                                              | 在等                                                |
| ------------------------------------------------- | --------------------------------------------------- |
| `completion`、`signatureHelp`、`workspace/symbol` | #194——同一份索引的視角                              |
| `rename`                                          | #194——以及 rename 要怎麼處理 spec 契約              |
| `semanticTokens`                                  | `Kind` 的 variant 無法在 `zerg` module 之外被 match |
| 增量同步、debounce、取消                          | 一次量測;Phase 1 每次按鍵都重檢整個程式             |

上面那幾列與 hover 原本是同一個缺口,而[名稱索引](#一個名字從同一份索引回答)補上了它的前半:給一個 path 與一個位置,
那裡宣告了什麼、在哪裡。[hover](#hover-是宣告的文件)是後半——它找到的那個宣告的文件——剩下的讀同一份索引,而不是再建
第二份:某個位置在範圍內的宣告,以及一個宣告的每個使用點。

`semanticTokens` 是另一種缺,值得這樣點名:它會需要一張把 token kind 對映到 LSP token type 的表,而那正是上一節存在
就是為了防止的那種**重複的語言事實清單**。vim 語法檔已經在為 Zerg 上色,而且它有 gate。

最後一列是成本,不是缺口。scheduler 是協作式且非搶佔的,所以一次長檢查會佔住它的 worker 直到做完;`emit.zg` 是這個
repository 裡最壞的情況,也是在這裡設計任何東西之前該拿來量的數字。

**這個成本的記憶體那一半已經關掉了。** 以 `src/compiler/zergc.zg` 為根的那個程式檢查一次——現在只要打開
`src/compiler/` 底下**任何一個**檔案就會要求這件事,因為一個 module 成員是對著它的 module 檢查的;量測當時它是 24
個檔案,今天是 27 個——以前要 6.7 秒,峰
值 **6.7 GB**,而一個長時間存活的 session 在三到四次之後會被作業系統殺掉。這個上限是 **emitter 的**,不是協定的:編
譯器的檢查住在 lowering 的走訪裡,所以唯一碰得到它們的路是 `emit_files_diag`,而它會先把整個程式降到 C。

`check_files_diag` 就是同一趟走訪,只是把 C 丟掉而不是累積起來,同一次檢查現在是 **4.9 秒、峰值 0.32 GB**。同一個
session 裡:改之前在第三次檢查就被 SIGKILL,改之後六十次都發出診斷,峰值 2.9 GB,而且每次的時間沒有變慢。省下來的不
是 C 的大小——它只有 3.6 MB——而是把它組起來的形狀:`defs = defs + c_fn(…)` 在大約 1500 個步驟裡,每一步都把先前產出
的全部再抄一份。`make check-equal` 負責讓這兩條路誠實,而且它是逐位元組比對兩者的診斷的,因為一個比 build 找得少的檢
查,就是一個對著一份根本編不過的檔案顯示乾淨 buffer 的編輯器。

**每次檢查留下來的東西也關掉了。** 在那之後,長時間的 session 還是會爬——這裡寫過的數字是一次 0.32 GB、六十次 2.9
GB——而在下面第二趟走訪拿掉之後重新量,它變小了,但仍然在爬:峰值 footprint 一次 0.12 GB、二十次 0.18、六十次 0.21。
**server** 自己持有的東西沒有在長;兩次檢查之間它的 heap 只有幾 MB。漏的是編譯器產生的程式碼,而檢查本身就是用它寫
的:每次檢查大約兩 MB 的小 cell,走訪讀過的每個識別字一個。從沒人持有的值裡讀出的欄位或元素(`cur(p).lexeme`)被再
複製了一次;對自己產生的值做 `match` 從不把那個值還回去;一個擁有的 `str` 比較完之後、以及交給 runtime intrinsic 的一
個擁有的引數,都從不釋放。每個 cell 都很小;代價在於它們的位置,散在 allocator 的各個 region 裡、一頁一個,所以它們
釘住的頁,就是下一次檢查無法重用的頁。修好之後,一次檢查只留下幾 KB,footprint 在前二十次檢查內就穩定下來並停在那
裡:一次 0.12 GB、二十次 0.15、六十次 0.16。修法是一條規則——一個不指名任何儲存位置的運算式,交出的是它自己的值
——而要讓編譯器守住它,就得在只是假設它成立的地方讓它真的成立:分支指名儲存位置的
`if`、作用在指名儲存位置的 carrier 上的 `?`,以及 optional chain,現在都各自交出自己的值;一個被陳述式丟棄的值,則
在丟棄的地方還回去。channel 端點是計數而不是複製,所以它有自己的一個問題,而會計數的使用者都問它:binding、
`return`、自由函式的引數、spawn 或 `for … in` 接下它時,一個端點都只被計數一次。還不成立的地方,和 `main` 上
一樣:一個擁有的端點——呼叫的結果,`mk()`——交給方法引數、struct 欄位、list 元素或 `defer` 引數時,它的計數會留
下來,而對它做 `defer` 可能死結而不只是漏;一個丟棄 channel 端點的陳述式,單獨寫的 `buffered()`,也會留下計數,因
為丟棄還回去的是會被複製的值,而 channel 是計數的。`make lsp` 要求 N 次檢查的 session 不超過一次檢查的 session
的 K 倍,這看的是整體的漏;`make mem-check` 要求每一種漏的形狀各自漏零,這才是逐一形狀的保證;corpus 則在
sanitizer 底下跑過每一個這樣的使用者。

**第二趟走訪也沒了。** 一次檢查的時間大約有一半,是因為 `publishDiagnostics` 把程式走了**兩趟**——一趟拿錯誤,
再在 `lint_program` 裡用自己的 merge、自己的走訪拿 `L5xx` conversion lint。`check_and_lint` 用一趟走訪同時回答兩者:
notes 在走過時順手留下,這不改變任何錯誤,所以錯誤仍然是 `check-equal` 拿去對 build 的那些。`make lsp` 從行程
外面量 `src/compiler/` 底下一個檔案的一次檢查成本,對照同一個程式的 `zerg build --emit check`——定義上就是一趟——
單位是 instructions retired:之前 2.07 趟,之後 1.09 趟,到一趟半就失敗。用 instructions 而不是秒數,因為秒數取決
於機器和行程落在哪一顆核心上,instructions 才是工作量。debounce 只會把剩下的藏起來。
