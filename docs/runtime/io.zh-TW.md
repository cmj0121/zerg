# Zerg Process 與 I/O

Zerg 程式如何讀寫外界——檔案、標準串流、子行程。建立在[語言參考](../language.zh-TW.md)的記憶體、錯誤、spec、迭代
模型，[Coroutine 與 Channel](../code/coroutine.zh-TW.md) 的 `Ref[T]` 盒與 coroutine，以及 [FFI](ffi.zh-TW.md) 的 C 邊界
之上。亦有 [English](io.md) 版本。

I/O 是 stdlib 的 **`io`** package（`import "io"`，不是 ambient）；唯一例外是 **`print`** 關鍵字（見 Formatting &
text）——把一個值寫到 stdout 的免 import 捷徑。三個想法承載它，每個都複用既有模型：

- **串流**是 `Reader` 或 `Writer`——byte 來源／去處，用 `for` 抽乾；
- **handle** 是 `Ref[T]`——檔案或 socket，scope-owned、恰好關一次；
  **[not yet]**——`Ref[T]` 與它的 `drop` 動作都建好了(見[值與記憶體](../core/memory.zh-TW.md));handle 等的是
  那個型別本身,而 `handle` 是一個沒有任何宣告帶著的名字——_E4056 no type named `handle`_,那是
  [FFI](ffi.zh-TW.md) 自己的標記——所以讀寫都走下方的整檔 leaf;
- **失敗是值**——會失敗的呼叫回 `Result[T]`、以 `?` 傳播；EOF 不算。

> **狀態。** 編譯器只出貨這個面的**子集**，而這個子集就是**整檔與整串流的葉子**——列表見
> [標準函式庫](stdlib.zh-TW.md#io)——外加 `print` 關鍵字。檔案不存在或不可讀會 raise **`IOError`**，可用
> `guard { io.read_file(p) }` 降級為 `Result`；內容為文字時以 `str(…)` 解碼。
> **`Reader` / `Writer` spec 面**——下文的 `read_bytes` / `read()` / `write` 與 `io.stdin` · `io.stdout` ·
> `io.stderr` 串流物件——為 **[not yet]**：預期語意一如規格所述，而寫出其中任一個名字得到的是 **`E3084`**——
> _module `io` has no `stdout`_——任何位置皆然，包含作為方法呼叫的 receiver。

## 串流——`Reader` 與 `Writer`

兩個 spec 都建好了，這個階段唯一的具體串流 **`io.Fd`**（一個裸 file descriptor，也就是子行程那三個端點）也是。
還沒建的是**模組層級的串流物件**——`io.stdin`／`io.stdout`／`io.stderr`——它們仍是 **[not yet]**，
_E3084 module `io` has no `stdout`_；今日輸入用 `io.read_file`、輸出用 `io.write` / `io.println`。

各只有**一個 required method**；其餘是 provided default（Spec 與 Generics），所以新串流只要供給 primitive 就繼承
所有便利方法。

**`Reader`**——`read_bytes(n: uint) -> Result[list[byte]]`，至多 `n` bytes（空 = 輸入結束，絕非阻塞式的等）。其上
是整份輸入的兩個 default：**`read_all() -> Result[list[byte]]`** 與 **`read_text() -> Result[str]`**，後者是前者
解碼後的樣子。

> **[not yet]** ITERATOR 那幾個 default——**`read() -> Iterator[str]`**，也就是 `for line in f.read()` 走訪的
> 逐行讀取，以及 `bytes()` / `chunks(n)`——是建立在一個 `for … in` 還走不動的協定之上（[Spec 與 Generics](../core/specs.zh-TW.md)），
> 所以它們沒有被宣告：一個叫不到的 default 是承諾、不是 method，呼叫它會得到 _E3131 the method `read` on a Fd_。可能非 valid UTF-8 就用 `read_bytes` 取、在
> `guard` 下解碼。

**`Writer`**——`write(bytes: list[byte]) -> Result[uint]`（寫入數量）；provided `write_str(s: str)`。
**`flush()`** 這個 default 是 **[not yet]**：這個階段沒有任何東西有緩衝，所以它會是一個什麼都不做的 method——
_E3131 the method `flush` on a Fd_ 指出那是這個型別沒有宣告的名字。
寫入失敗——磁碟滿、broken pipe——是值、以 `?` 傳播；絕不靜默丟棄（那個便利只屬於 `print`）。

```text
fn copy_lines(src: Reader, mut &dst: Writer) -> Result[nil] {
    for line in src.read() {
        dst.write_str(line)?
        dst.write_str("\n")?
    }
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

> **[not yet]** `File` handle 與 `open` / `create` 此階段尚未建置；接好的是那一對整檔葉子，檔案不存在或不可讀時
> raise **`IOError`**（用 `guard` 降級）——一經 `guard` 便與「檔案不存在是預期的 value 失敗」一致。兩個葉子都不交還
> handle，所以沒有東西需要關閉，`defer f.close()` 也沒有可指名的對象。

## 標準串流

`import "io"` 綁定 **`io.stdin`**（`Reader`）、**`io.stdout`** 與 **`io.stderr`**（`Writer`）——透過 stdlib 取得的
唯讀 OS 事實，跟 `env` 與 clock 同級（[Module、Package 與 Program](package.zh-TW.md)），絕非 `main` 參數。
**`print`** 關鍵字是免 import 捷徑——`print x` 把值的 `display` 渲染加換行寫到 stdout、盡力而為；要 `Result`、
`io.stderr`、或原始 bytes 才用 `io.stdout`。

```text
import "io"
for line in io.stdin.read() { io.stdout.write_str(transform(line))? }
```

> **[not yet]** `io.stdin` / `io.stdout` / `io.stderr` 串流物件尚未建置，寫出其中一個得到的是 **`E3084`**——
> _module `io` has no `stdout`_——包含作為方法呼叫的 receiver，也就是上方範例寫它的位置。此階段接好的是自由
> 函式（見[標準函式庫](stdlib.zh-TW.md#io)）；各寫出器回 `Result[nil]`，但為 best-effort 寫出，尚不會產出 `Err`。

**標準輸出是無緩衝的**,而那正是讓兩種寫法成為同一條 stream 的原因:`print` 降到 libc `printf`、`io.*` 降到
`write(2)`,所以任何一邊有緩衝,兩者就會依「各自緩衝 flush 的順序」而不是「程式寫下的順序」出現。它沒有緩衝,所以一
支交替呼叫 `print` 與 `io.println` 的程式就交替地印出來,在終端機與經由管線都一樣;而一次 abort 寫到標準錯誤的那一
行,也會落在它之前寫出的輸出之後。Go 的 stdout 無緩衝,理由相同。

代價是每一行一次 write syscall,那就是這個保證的價錢。想把它攤平的程式**先組好一行、再一次寫出**——那是程式做得到
的事,而緩衝不是。

## 阻塞——在 coroutine、不在 thread

原生 `io` 同步地讀、卻絕不阻塞 runtime：一個必須等的 `read_bytes`／`write` 會**停泊它的 coroutine**、scheduler
轉去跑別的——與任何 channel 等待相同的 fairness 保證（[Coroutine 與 Channel](../code/coroutine.zh-TW.md)），沒有
`async`／`await`、沒有 function coloring。唯一例外是 FFI 邊界：一個阻塞的 **foreign（FFI）C 呼叫**會停泊整條 OS
thread，因為 Zerg 不擁有那個 frame（[FFI](ffi.zh-TW.md)）。

> **[not yet]** 會停泊 coroutine 的 `io` 屬於上文尚未建置的串流面。阻塞 foreign 呼叫的「每-thread 停泊」已隨
> **M:N** scheduler 到來（[Coroutine 與 Channel](../code/coroutine.zh-TW.md)）：這樣一次呼叫現在只佔住**一條
> worker**，其餘 worker 仍照跑 Zerg coroutine。在單 worker 的主機上，停下來的仍然是整個程式。

## Process 與命令執行

子行程用**反引號命令字面量**啟動，並透過它的三個串流觀察——`stdin` 是 `Writer`，`stdout` 與 `stderr` 是
`Reader`——而 `wait()` 回答的狀態與 shell 報的一樣（exit code，被訊號殺掉時是 128+signal）。字面量**就是**那個
handle，`os.command(argv)` 則是同一件事手寫出來的樣子（[標準函式庫](stdlib.zh-TW.md)）。

**兩種形式都沒有 shell。** 命令字面量建的是**引數向量**，然後直接執行它：

- **`` `git status` ``**——**靜態**字面量，以空白切成 argv（引號會被尊重）。
- **`` f`git checkout {branch}` ``**——**內插**，而每個 `{x}` 就是**一個引數**，不管裡面是什麼。帶空白的路徑仍是
  一條路徑；讀起來像命令的值就只是資料。沒有任何字串留給 shell 再切一次，這就是全部的安全性質——沒有 shell 就
  沒有可以注入的地方。一個洞是**一整個引數**，不能跟旁邊的文字接起來（_E2081_）：用文字拼出引數正是 shell 在做
  的事。

所以 pipe、redirect 與 glob 不屬於這個形式。需要它們的程式自己去跑那個 shell——`` f`sh -c {script}` ``——讓 shell
出現在 argv 裡，而不是被語法隱含。

```zerg
import "os"

fn main() {
 p := `echo hello`
 print p.out()
 print p.wait()

 dir := "my docs"
 q := f`echo {dir}`
 print q.out()

 c := `cat`
 c.stdin.write_str("in\n")!
 c.stdin.release()
 print c.stdout.read_text()!
}
```

命令字面量就是 `os.command(…)`，所以檔案得寫 `import "os"`——沒寫的檔案裡出現字面量會拿到 _E2083_，它指名的是
那個字面量、而不是它降下去的那個模組呼叫。

要**同時**等好幾個——stdout、stderr、timeout——就把每個 `Reader` 橋接成 channel 再 `select`（fan-in，
[Coroutine 與 Channel](../code/coroutine.zh-TW.md)）；模型不新增任何等待 primitive。子行程是 foreign resource，其
thread-safety 與 lifetime 遵循 FFI 規則——除非刻意共享，由單一 coroutine 擁有（[FFI](ffi.zh-TW.md)）。

## 延後

- 具體的 **`io` 清單**——開檔模式、seek、buffered wrapper、socket／網路——都是 stdlib。
- **write-back 緩衝**（`read`／`recv` 填滿呼叫者的 `list[byte]`）是 FFI 的 out-buffer open question
  （[FFI](ffi.zh-TW.md)）；在它落地前，`read_bytes` 回傳全新的 `list[byte]`。
- **格式指示子**（`f"{x:>.2f}"`）導向 per-type 的 format 協定（Formatting & text）。
