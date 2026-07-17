# Zerg Process 與 I/O

Zerg 程式如何讀寫外界——檔案、標準串流、以及子行程。它建立在[語言參考](language.zh-TW.md)的記憶體、錯誤、spec、
迭代模型，[Coroutine 與 Channel](coroutine.zh-TW.md) 的 `Ref[T]` 資源盒與 coroutine 模型，以及 [FFI](ffi.zh-TW.md)
的 C 邊界之上。亦有 [English](io.md) 版本。

I/O 住在 stdlib 的 **`io`** package——像任何 package 一樣要 import（`import io`），不是 ambient。唯一的例外是
**`print`** 關鍵字（見[語言參考](language.zh-TW.md)的 Formatting & text）——「把一個值寫到 stdout」的免 import
捷徑；更豐富的一切都在 `io`。

三個想法承載全部，每個都複用你已有的模型：

- **串流就是 `Reader` 或 `Writer`**——一個 byte 來源或去處，用 `for` 抽乾（迭代）；
- **handle 就是 `Ref[T]`**——一個檔案或 socket 是 scope-owned、恰好關一次的資源；
- **失敗是值**——每個會失敗的操作都回 `Result[T]`、以 `?` 傳播；EOF 不算失敗。

## 串流——`Reader` 與 `Writer`

byte 來源實作 **`Reader`**、去處實作 **`Writer`**。各只有**一個 required method**；其餘都是 provided default
（Spec 與 Generics），所以新的串流型別只要供給那個 primitive，就繼承所有便利方法。

**`Reader`**——required `read_bytes(n: uint) -> Result[list[byte]]`：至多 `n` bytes，**輸入結束時回空 list**
（絕不是阻塞式的「等」）。其餘都是建立在它之上的 provided default：

- **`read() -> Iterator[str]`**——**預設的逐行讀取**：解碼 UTF-8、每行產出一個 `str`。`for line in f.read() { … }`
  乾淨地抽乾來源，在 EOF（`StopIteration`）結束、對中途的解碼或裝置錯誤則**re-raise**——就是一般的迭代協定
  （迭代）。可能不是 valid UTF-8 的 bytes，就用 `read_bytes` 取、再在 `guard` 下解碼。
- **`bytes() -> Iterator[byte]`** 與 **`chunks(n) -> Iterator[list[byte]]`**——byte 與定長區塊視圖，供二進位處理。

**`Writer`**——required `write(bytes: list[byte]) -> Result[uint]`（寫入的數量）；provided `write_str(s: str)` 與
`flush() -> Result[nil]`。`Writer` 的失敗是值——磁碟滿了、broken pipe——所以它像任何 `Result` 一樣以 `?` 傳播；
它絕不靜默丟棄（那個便利只屬於 `print`）。

```text
fn copy_lines(src: Reader, mut dst: Writer) -> Result[nil] {
    for line in src.read() {           # EOF 結束迴圈；讀取錯誤 re-raise
        dst.write_str(line)?           # 寫入錯誤 early-return
        dst.write_str("\n")?
    }
    return dst.flush()
}
```

## 檔案與 handle

一個 **`File`** 是包在你無法窺看的 newtype 裡的 **`Ref[handle]`**——就是 [FFI](ffi.zh-TW.md) 的 opaque
foreign-handle 模式，所以檔案**恰好關一次**，在最後持有者 scope 退出時（或顯式 `del`）。它透過那個 `Ref[T]`
逃出 scope；侷限在單一 scope 的檔案則要 `defer f.close()`——就是[語言參考](language.zh-TW.md)那條「逃不逃出 scope」
的分界。

```text
open(path: str)   -> Result[File]      # 讀；File 實作 Reader
create(path: str) -> Result[File]      # 寫/truncate；File 實作 Writer
```

`open` 回 `Result`——檔案不存在是**預期**的失敗、是值，絕非 abort。開檔模式（append、read-write）、seek、metadata
都是 `File` 上的 `io` method；它們的清單是 stdlib 細節、不是新概念。**socket** 是同一個形狀——一個既是 `Reader`
又是 `Writer` 的 `Ref[handle]`——具體的網路 API 留給 `io`。

## 標準串流

`import io` 綁定三個 ambient 串流：**`io.stdin`**（一個 `Reader`）、**`io.stdout`** 與 **`io.stderr`**（`Writer`）。
它們是透過 stdlib 取得的唯讀 OS 事實，跟 `env` 與 clock 同級（見 [Module、Package 與 Program](package.zh-TW.md)）
——絕不是 `main` 的參數。

```text
import io

for line in io.stdin.read() {          # 一個逐行 filter
    io.stdout.write_str(transform(line))?
    io.stdout.write_str("\n")?
}
```

**`print`** 關鍵字是常見情況的免 import 捷徑——`print x` 把 `x.display()` 加換行寫到 stdout、盡力而為
（Formatting & text）。要 `Result`、要 `io.stderr`、或要原始 bytes 時，才用 `io.stdout`。

## 阻塞——在 coroutine、不在 thread

原生 `io` **寫起來同步、跑起來不阻塞**：一個必須等待的 `read_bytes` 或 `write` 會**停泊它的 coroutine**、
M:N scheduler 轉去跑別的——與任何 channel 等待相同的 fairness 保證（見 [Coroutine 與 Channel](coroutine.zh-TW.md)）。
**沒有 `async` / `await`、沒有 function coloring**；普通的 top-down 程式碼永不凍住不相干的 coroutine。

唯一的例外是已提過的 FFI 邊界：一個**阻塞的 `extern` C 呼叫**會停泊整條 OS thread，因為 Zerg 不擁有那個 frame
（見 [FFI](ffi.zh-TW.md)）。原生 `io` 與 scheduler 整合；裸的 C 阻塞呼叫則佔住 thread。

## Process 與命令執行

子行程用**反引號命令字面量**啟動，並透過**同一套串流模型**觀察——它的 pipe 是 `Reader` 與 `Writer`、它的 handle
是 `Ref[T]`。

**兩種形式，`f` 標出危險：**

- **`` `git status` ``**——**靜態**字面量：以空白切成 argv list（引號會被尊重）、**直接執行、不經 shell**。沒有
  內插，所以沒有 injection、沒有 glob 或 pipe——安全的預設，與 Go / Rust / Elixir 一致。
- **`` f`git checkout {branch}` ``**——**內插**的命令，**經 shell** 執行（所以 pipe 與 redirect 可用）。每個 `{x}`
  預設**被 shell-quote 成單一參數**，這擊敗 command-injection、但擋不了惡意的 `-flag`（參數終究是參數）。**raw**
  拼接——動態組 pipeline——是顯式的 `{x:raw}`、opt-in 的腳槍。`f` 讀來與 `f`-string 上完全一致：「這會內插」
  （Formatting & text）。

兩者都產出一個 **process handle**——一個 `Ref[proc]` newtype，其 `drop` 會 wait（或 kill）子行程，所以它恰好被
回收一次：

```text
p := `ls -l`
for line in p.stdout.read() { print line }    # stdout 是一個 Reader
code := p.wait()!                             # 停泊這條 coroutine 等退出碼
```

- **`p.stdin`** 是 `Writer`；**`p.stdout`** 與 **`p.stderr`** 是 `Reader`——像任何串流一樣餵它、抽乾它，而一個
  `read` 只阻塞這條 coroutine。
- **`p.wait() -> Result[int]`** 停泊 coroutine 直到子行程退出、產出它的狀態。
- 要**同時**等好幾個——stdout、stderr、timeout——就用一條抽乾用的 coroutine 把每個 `Reader` 橋接成 channel、再
  `select`（fan-in 模式，[Coroutine 與 Channel](coroutine.zh-TW.md)）；process 模型不新增任何等待 primitive。

因為子行程是 foreign resource，它的 thread-safety 與 lifetime 遵循 FFI 規則——除非刻意共享，handle 由單一
coroutine 擁有（見 [FFI](ffi.zh-TW.md)）。

## 延後

- **具體的 `io` 清單**——開檔模式、seek、buffered wrapper、以及 socket/網路 API 都是 stdlib 面、不是語言概念。
- **`read` / `recv` 的 write-back 緩衝**——就地填滿呼叫者的 `list[byte]`——是 FFI 的 out-buffer open question
  （[FFI](ffi.zh-TW.md)）；在它落地前，`read_bytes` 回傳一個全新的 `list[byte]`。
- **格式指示子**（`f"{x:>.2f}"`）導向一個 per-type 的 format 協定——見 Formatting & text。
