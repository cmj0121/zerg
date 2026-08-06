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

其他每一個請求都會收到 **method-not-found 錯誤**,而不是沉默。一個在等永遠不會來的回覆的 client 會停止送下一個請求,
然後編輯器就靜掉了,什麼也沒說。

**診斷是對整個程式檢查的**,不是只對 buffer。一個 import 了別的 module 的檔案必須連同那個 module 一起檢查,否則它借來
的每個名字都會讀成 undefined——會在正確的程式碼底下畫線的 server,是人會關掉的那種。

**兩種嚴重度,來自兩個地方。** **error** 是 `emit_files_diag` 回報、`zerg build` 會為此拒絕的東西。`L5xx` conversion
findings 是關於**合法**程式的——一個在原始碼沒說的地方改變了型別的值——所以它們以 **information** 抵達。把一個能動的
程式塗成紅色的 server,是在教它的使用者忽略紅色。

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

`make -C editors install` 會 symlink 語法檔,再加兩個:

- `ftplugin/zerg.lua`——為 `.zg` buffer 啟動 server;
- `lua/zerg/lsp.lua`——`vim.lsp.start`(nvim 0.8+),不需要 plugin manager,也不需要 `nvim-lspconfig`。

```lua
vim.g.zerg_lsp = false            -- 不要啟動 server
vim.g.zerg_lsp_cmd = { 'zerg', 'lsp' }
vim.g.zerg_format_on_save = true  -- 每次寫入都跑 zerg fmt
```

當 `zerg` 不在 `PATH` 上時它**安靜地**回答。在一個沒有建好 toolchain 的 checkout 裡,每開一個 `.zg` 就報錯的 server,
是人會停用而且再也不會啟用的那種。

## 讓編輯器保持誠實

這棵樹裡其他每一樣東西都是靠**呼叫**編譯器來held 住的——`zerg fmt` 就是 formatter,而 server 是去問
`emit_files_diag`,不是自己檢查任何東西,所以沒有第二份會漂移的副本。編輯器檔案是唯一的例外,而且沒辦法不是:vim 是
從一份寫在 vimscript 裡的關鍵字清單上色的,而 nvim 必須在任何 Zerg 工具跑起來之前就知道怎麼縮排。

所以那些事實有自己的 gate——`make editor-align`:

- `lookup_keyword` 回傳的每個保留字都是 `zerg.vim` 有上色的,而它當作關鍵字上色的每個字也都是 lexer 保留的(內建的
  **型別**名改為 held 到 parser 的清單,因為 `int` 是個普通的 identifier,lexer 從沒聽過它);
- ftplugin 設定的縮排字元,就是 `zerg fmt` 實際**寫出**的那個。

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
| `documentSymbol`                              | `File` 是 `pub`,`FnDecl` 與它的兄弟們不是           |
| `semanticTokens`                              | `Kind` 的 variant 無法在 `zerg` module 之外被 match |
| 診斷的 **code** 作為資料                      | `F401` 與 `L502` 是被渲染進訊息文字裡的             |
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
