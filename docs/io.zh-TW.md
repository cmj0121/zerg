# Zerg Process 與 I/O

Zerg 程式如何讀寫外界——檔案、標準串流、子行程。建立在[語言參考](language.zh-TW.md)的記憶體、錯誤、spec、迭代
模型，[Coroutine 與 Channel](coroutine.zh-TW.md) 的 `Ref[T]` 盒與 coroutine，以及 [FFI](ffi.zh-TW.md) 的 C 邊界
之上。亦有 [English](io.md) 版本。

I/O 是 stdlib 的 **`io`** package（`import io`，不是 ambient）；唯一例外是 **`print`** 關鍵字（見 Formatting &
text）——把一個值寫到 stdout 的免 import 捷徑。三個想法承載它，每個都複用既有模型：

- **串流**是 `Reader` 或 `Writer`——byte 來源／去處，用 `for` 抽乾；
- **handle** 是 `Ref[T]`——檔案或 socket，scope-owned、恰好關一次；
- **失敗是值**——會失敗的呼叫回 `Result[T]`、以 `?` 傳播；EOF 不算。

## 串流——`Reader` 與 `Writer`

各只有**一個 required method**；其餘是 provided default（Spec 與 Generics），所以新串流只要供給 primitive 就繼承
所有便利方法。

**`Reader`**——`read_bytes(n: uint) -> Result[list[byte]]`，至多 `n` bytes（空 = 輸入結束，絕非阻塞式的等）。其上：
**`read() -> Iterator[str]`**，預設逐行讀取——`for line in f.read()` 乾淨抽乾，在 EOF（`StopIteration`）結束、對
中途的解碼或裝置錯誤 re-raise（迭代）；可能非 valid UTF-8 就用 `read_bytes` 取、在 `guard` 下解碼。另有
`bytes() -> Iterator[byte]` 與 `chunks(n) -> Iterator[list[byte]]`。

**`Writer`**——`write(bytes: list[byte]) -> Result[uint]`（寫入數量）；provided `write_str(s: str)` 與 `flush()`。
寫入失敗——磁碟滿、broken pipe——是值、以 `?` 傳播；絕不靜默丟棄（那個便利只屬於 `print`）。

```text
fn copy_lines(src: Reader, mut dst: Writer) -> Result[nil] {
    for line in src.read() { dst.write_str(line)?; dst.write_str("\n")? }
    return dst.flush()
}
```

## 檔案與 handle

一個 **`File`** 是包在 opaque newtype 裡的 `Ref[handle]`（foreign-handle 模式，[FFI](ffi.zh-TW.md)），所以它
**恰好關一次**，在最後持有者 scope 退出或顯式 `del` 時；侷限單一 scope 的檔案則用 `defer f.close()`。

```text
open(path: str)   -> Result[File]      # 讀；File 實作 Reader
create(path: str) -> Result[File]      # 寫/truncate；File 實作 Writer
```

檔案不存在是**預期**的 value 失敗、絕非 abort。開檔模式、seek、metadata 都是 `io` method——stdlib 細節、不是新
概念。**socket** 是同一形狀：一個既是 `Reader` 又是 `Writer` 的 `Ref[handle]`，網路 API 留給 `io`。

## 標準串流

`import io` 綁定 **`io.stdin`**（`Reader`）、**`io.stdout`** 與 **`io.stderr`**（`Writer`）——透過 stdlib 取得的
唯讀 OS 事實，跟 `env` 與 clock 同級（[Module、Package 與 Program](package.zh-TW.md)），絕非 `main` 參數。
**`print`** 關鍵字是免 import 捷徑——`print x` 把 `x.display()` 加換行寫到 stdout、盡力而為；要 `Result`、
`io.stderr`、或原始 bytes 才用 `io.stdout`。

```text
import io
for line in io.stdin.read() { io.stdout.write_str(transform(line))? }
```

## 阻塞——在 coroutine、不在 thread

原生 `io` 同步地讀、卻絕不阻塞 runtime：一個必須等的 `read_bytes`／`write` 會**停泊它的 coroutine**、scheduler
轉去跑別的——與任何 channel 等待相同的 fairness 保證（[Coroutine 與 Channel](coroutine.zh-TW.md)），沒有
`async`／`await`、沒有 function coloring。唯一例外是 FFI 邊界：一個阻塞的 **`extern` C 呼叫**會停泊整條 OS
thread，因為 Zerg 不擁有那個 frame（[FFI](ffi.zh-TW.md)）。

## Process 與命令執行

子行程用**反引號命令字面量**啟動，並透過同一套串流觀察——它的 pipe 是 `Reader` 與 `Writer`、它的 handle 是一個
`Ref[proc]`，其 `drop` 會 wait（或 kill）它、恰好回收一次。**`f` 標出危險：**

- **`` `git status` ``**——**靜態**字面量，以空白切成 argv（引號會被尊重）、**直接執行、不經 shell**：沒有內插，
  所以沒有 injection、glob、pipe——安全的預設（Go / Rust / Elixir）。
- **`` f`git checkout {branch}` ``**——**內插**、**經 shell** 執行（pipe 與 redirect 可用）；每個 `{x}` 預設
  **被 shell-quote 成單一參數**（擊敗 command-injection、但擋不了惡意 `-flag`），**raw** 拼接是顯式的 `{x:raw}`。
  `f` 讀來與 `f`-string 一致。

```text
p := `ls -l`
for line in p.stdout.read() { print line }    # p.stdin: Writer；p.stdout/stderr: Reader
code := p.wait()!                             # 停泊這條 coroutine 等退出碼
```

要**同時**等好幾個——stdout、stderr、timeout——就把每個 `Reader` 橋接成 channel 再 `select`（fan-in，
[Coroutine 與 Channel](coroutine.zh-TW.md)）；模型不新增任何等待 primitive。子行程是 foreign resource，其
thread-safety 與 lifetime 遵循 FFI 規則——除非刻意共享，由單一 coroutine 擁有（[FFI](ffi.zh-TW.md)）。

## 延後

- 具體的 **`io` 清單**——開檔模式、seek、buffered wrapper、socket／網路——都是 stdlib。
- **write-back 緩衝**（`read`／`recv` 填滿呼叫者的 `list[byte]`）是 FFI 的 out-buffer open question
  （[FFI](ffi.zh-TW.md)）；在它落地前，`read_bytes` 回傳全新的 `list[byte]`。
- **格式指示子**（`f"{x:>.2f}"`）導向 per-type 的 format 協定（Formatting & text）。
