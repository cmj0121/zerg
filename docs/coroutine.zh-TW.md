# Zerg Coroutines 與 Channels

Zerg 的並行**只有 coroutine + channel**——沒有共享可變狀態、沒有 lock、沒有 future、沒有 join/handle。它建立在
[語言參考](language.zh-TW.md) 的記憶體與錯誤模型之上。亦有 [English](coroutine.md) 版本。

## `spawn`

`spawn f(args)` 在 **M:N scheduler** 上啟動一個 coroutine（Go 的 `go`）。它**不回傳任何東西**——沒有 handle、沒有
join/await；結果與完成**只能靠 channel** 觀察。

- **Fire-and-forget**——runtime 從不追蹤或 join 該 coroutine；要得知結果，它必須把結果送進一條觀察者持有的 channel。
- **捕獲受限**於 **immutable 值與 `Ref` 值**（channel、`Ref[T]`）——`mut` ref 無法跨越 `spawn`，所以 coroutine 不會
  共享可變的 Zerg 狀態（不會有 data race）。什麼能跨越邊界、以及如何跨越，見下一節 **共享與 memory model**。

### 唯一不 scope-owned 的東西

Zerg 其它每一樣東西都是 **scope-owned**——一個值、一個 `defer`、一個 `Ref[T]` 資源——在 scope 離開時決定性清理。
**coroutine 是唯一、刻意的例外。** `spawn` 把 child 放生：它的 lifetime **不**綁在啟動它的 scope 上——可以活得比那個
scope 久、也可以早早結束，而且沒有 parent 等它。

這正是 fire-and-forget 的全部重點，是**選擇、不是遺漏**。把 coroutine 的 lifetime 綁在啟動它的 scope 上，就恰恰是
**結構化並行**（一個會 join child 的 nursery）；Zerg 拒絕它，以保住 `spawn` 無 handle、保住模型的小。代價是被接受且
明講的：**沒有 join、沒有 parent 等待、沒有自動的失敗傳播**——協調是 caller 的事、永遠透過 channel。child 的失敗只會以
channel close 上的 `Right(err)` 傳到別人那裡（見未處理的 abort）；程式結束時，還在跑的 coroutine 就地被 abandon（見
終止與 deadlock）。

一個 scope-owned 的*值*仍可**通知**一條 coroutine——例如一個資源，它的 `drop` 關掉 coroutine 正在 watch 的一條 cancel
channel——但那是協作式通知、不是 ownership：coroutine 觀察到 close 並*選擇*停止，也仍然可以無視它。coroutine 本身永遠
不會被某個 scope 回收。

## 共享與 memory model

因為 coroutine 邊界對「除 `Ref` 值以外」的一切都複製，Zerg **不需要龐大的 memory model**——根本沒有共享可變狀態
可 race。可觀察的 ordering 保證只有一條，其餘全都由它導出：

> **一個 channel 的 `send` happens-before 對應的 `receive` 完成。**

接收端因此看到的是**完整建構好**的 payload（在 send 當下快照）；除此之外沒有任何跨 coroutine 的 ordering 存在、也
不需要。這就是 memory model 的全部。

**什麼能跨越邊界**——作為 `spawn`／closure capture，或 channel payload：

- 一個 **immutable 值**——複製進去（源死時 move 最佳化）；
- 一個 **`Ref` 值**（channel，或 `Ref[T]`）——以 reference 共享、refcount-bump；
- 一個 **mutable、非-`Ref` 值**——絕不共享；送出時複製。

所以不變式精確為：**沒有共享可變的 _Zerg_ 狀態。** 跨 coroutine 分享的 `Ref[T]` 只給**唯讀視圖**——讀取與非-`mut`
方法，永不給 `mut` binding——所以並發讀不需要鎖。任何**必須被變動**的東西就不分享：由一個 coroutine 獨佔（見下方
actor pattern）。

`Ref[T]` 若包著**外部 handle**（見 [FFI](ffi.zh-TW.md)）是 Zerg 唯一管不到的情況——它看不到資源的 C 端狀態，所以
該資源的 thread-safety 屬於那個外部 library、在本 model 之外。安全預設一樣：把 `Ref[handle]` 交給**一個** owner
coroutine。

## Channels

channel 是一條型別化的 by-ref 管道，payload **複製**流過它。它是一個 **reference-counted 的值**——`Ref` 的內建
實作者（與 `Ref[T]` 並列；見 [值與記憶體](memory.zh-TW.md)），scope-owning 的例外：在最後一個持有者的 scope 結束時
free，複製一個值會 bump 它所含 `Ref` 值的 refcount、其餘深拷貝。channel 是 **FIFO** 且為**一等值**（可被送進另一條
channel）。

```text
ch := chan[int]()      # unbuffered——每次 send 與一次 receive rendezvous
ch := chan[int](64)    # buffered，容量 64
```

容量是唯一可調之處；**send 在滿時 block、receive 在空時 block**。unbuffered（容量 0）是 rendezvous——send 只有在
receiver 取走值時才完成，也是 Zerg 唯一的同步原語。

### 送出——`ch <- v`

send 與 receive **不對稱**：關閉是 producer 的決定，所以它一定知道。send 不回傳值——它只會完成、block，或 abort：

| channel 狀態                              | `ch <- v`                                                      |
| ----------------------------------------- | -------------------------------------------------------------- |
| 開、送得出（有空位、或有等待的 receiver） | 完成；值在 **send 當下快照**                                   |
| 開、暫時送不出（滿了、或沒有 receiver）   | **block**——對還沒被收的 channel 送是合法的，不是 bug           |
| 已 close                                  | **abort**（`SendOnClosedError`）——見 [Aborts](errors.zh-TW.md) |

### 接收——`<-ch`

receive 回傳 **`Result[T]`**：`Left(v)` 是值；`Right(err)` 代表 channel **已 close 且排空**，`err` 是**原因**——
所以崩潰的原因永遠不會遺失。它**不**代表「空、等一下」（那是 block）。

| channel 狀態                     | `<-ch : Result[T]`                         |
| -------------------------------- | ------------------------------------------ |
| 開、有值                         | `Left(v)`                                  |
| 開、空                           | **block**                                  |
| 已 close、仍有 buffered 值       | `Left(v)`——**先排空**，不丟資料            |
| 乾淨關閉（最後 sender 正常離場） | `Right(StopIteration)`——end-of-stream 哨兵 |
| 崩潰關閉（最後 sender abort）    | `Right(err)`——傳播過來的崩潰 `Err`         |

多數程式把**任一 `Right` 當「停」**，只有需要原因時才檢視 `err`。每種需求都由既有運算子掉出來——由 **receiver**
決定：

```text
v := <-ch?                 # 把關閉原因往上傳（崩潰會級聯）
v := <-ch!                 # force：崩潰 Err 在此 re-raise 成 abort
v := <-ch ?? fallback      # 任一關閉都給預設值
if v := <-ch { … }         # 只有有值（Left）時才跑區塊
for { v := <-ch ?? break }               # 逐一收，任一關閉就跳出
match <-ch { Left(v) -> use(v)  Right(e) -> report(e) }
```

因為關閉落在 `Right`，`chan[U?]` 不再含糊：**送了一個 `nil`** 是 `Left(nil)`，**關閉**是 `Right`。

## 關閉——自動、發生在最後一個 sender

Zerg **沒有 explicit `close`**。channel 在其**最後一個 send 能力持有者的 scope 結束時**關閉——refcount 依方向拆開：

- **send-count → 0 ⇒ close**（receiver 仍可排空 buffered 的值），
- **完全無持有者 ⇒ free**。

正常結束與崩潰用**同一條路徑**關閉：aborting producer 的 unwind 放掉它的 send 端（遞減 channel refcount），若它是
最後一個 sender，channel 就關閉——**帶原因**：乾淨離場給 `StopIteration`、abort 給崩潰的 `Err`。receiver 從上面的 `Right`
讀到它，所以崩潰以一個普通錯誤抵達 consumer，而非讓它對著孤兒 channel 永遠 block。

**提早關 = 縮 scope**——要在 producer 的 scope 結束前就通知「沒有更多值」，把它的 send 端放進更窄的區塊：

```text
{
    out := ch.send
    produce(out)
}              # out 的 scope 結束 → 自動 close（若是最後 sender）
cleanup()
```

`del ch` 也直接做到同一件事——當下放掉你對 `ch` 的持有，若你是最後 sender 就關閉 channel，無需更窄的區塊
（見 [值與記憶體](memory.zh-TW.md)）。

### send 覆蓋不變量

auto-close 是 **level-triggered**：send-count 一碰 0 就開火，沒有「等一下還有 sender 要來」這種概念。所以就一條規則：

> 從建立起、到你認定*此後再無任何 send* 為止，**任一瞬間都必須至少存在一個 send 能力的持有者。**

一個由 coroutine 持有的 send 端，會在該 coroutine 的整個生命（睡著與否）維持 send 側存活；唯一的失敗是出現一個
**完全沒有 send 端的空窗**。

```text
ch := chan[int]()
spawn consumer(ch.recv)     # 建立者仍握著雙向的 `ch`
... 延遲 ...                # 安全：建立者的端讓 send-count ≥ 1 撐過延遲
spawn producer(ch.send)
```

安全：建立者握著 send 能力端時，consumer 只是 **block**。只有當你**先放掉自己的 send 端**、*再*延遲、而 producer
尚未存在時才會壞——那個空窗提早關閉 channel，稍後的 send 則 abort。準則：**自己的 send 端最後才放**（如同 Rust 的
`mpsc`）。

## Directional channels

裸的 `chan[T]` 是**雙向**的。它可 **narrow** 成單向端——**send-only**（`ch <- v`，給 producer）或 **receive-only**
（`<-ch`，給 consumer）。

**narrowing 是單向的**，絕不能回到雙向——這是安全保證：send-only 端**不可能**偷收值，receive-only 端也不可能插入
值。它是一個安全的內建 upcast，在明確方向型別的目標處觸發（參數、`return`、typed binding）；顯式 narrow（`ch.send`
/ `ch.recv`）則丟掉你自己的雙向貢獻。

方向也正是讓 auto-close **精準**的關鍵，因為 refcount 依方向計數：send-only 計入 send-count、receive-only 計入
receive-count、雙向**兩者都計入**。所以要看到「producer 做完」的 consumer 必須持 **receive-only** 端——雙向
consumer 會被算成 sender，害 channel 永遠開著。雙向端適合**對稱壽命**用法（自用緩衝、共享的 worker-pool channel），
最後一個成員離場時 close 與 free 同時發生。雙向對話用**兩條** directional channel——一條共享的雙向 channel 會把
任一值路由給任一 receiver，那是 race，不是對話。

## `select`——同時等多條 channel

`select` 是**唯一**的多路等待：它盯住多個 send/receive 操作，block 到其中一個 **ready**，執行那條 arm；平手時
**公平**任選（不依位置，故無 arm 餓死）。

```text
select {
    v := <-a -> use(v)      # receive arm：開著且有值才 ready
    b <- x   -> sent()      # send arm：送得出去才 ready
    done     -> break       # 所盯的 receive channel 全部關閉 → 觸發一次
    _        -> tick()      # 此刻沒人 ready → 非阻塞
}
```

receive arm 綁定的型別與一般 receive 相同——`Result[T]`：有值時是 `Left(v)`、崩潰關閉時是 `Right(err)`：

- **乾淨關閉**的 receive arm 被**除名**（不觸發、不空轉）並計入 `done`；**崩潰關閉**的 arm 則**浮現** `Right(err)`
  ——崩潰絕不被默默丟棄。
- 落在已 close channel 上的 **send arm** 被選到時 **abort**（send-on-closed 是 bug）。
- **`done`** 在所盯的 receive channel **全部關閉**（select _耗盡_）時**觸發一次**——沒有 join、沒有手動倒數的乾淨
  fan-in 結束。
- **`_`** 在此刻沒有任何 arm ready 時觸發，使 `select` **非阻塞**。
- 全關且**沒有 `done`、也沒有 `_`** → **abort**（`DeadlockError`），給忘記安排結束的安全網。`done` 優先於 `_`。

粒度是刻意的：單一 receive 逐值呈現關閉；`select` 把乾淨關閉聚合成一個 `done`、同時仍讓崩潰浮現——於是乾淨關閉
永不混進「有資料」的競賽，也就不空轉。

## Timer 與 cancellation

**timeout** 與 **cancellation** 都從 channel 與 `select` 掉出來——沒有新 primitive。

- **timer 就是一條 channel。** stdlib 的 `after(d)` 回傳一條 receive-only channel，在 duration `d` 之後**一次**變成
  可接收（`ticker(d)` 則重複觸發）；`select` 對它的一個 receive arm 就是 **timeout**。`d` 是 stdlib duration、clock
  是 ambient-OS 的 stdlib 設施（如 `env`），都不是 primitive。
- **cancellation 也是一條 channel。** 給 coroutine 一條 **cancel channel**、讓它在自己的 `select` 裡監看；取消方
  close 它，coroutine 看到那個 arm 觸發就收手。因為 `spawn` 是 fire-and-forget、**無 handle，所以沒有 preemptive
  kill**——cancellation 是**合作式**的：一個 coroutine 只會因 return、或因察覺 cancel/timeout arm 而選擇停下才結束。

```text
select {
    v := <-work           -> handle(v)   # 真正的工作
    _ := <-after(timeout) -> stop()      # timeout——timer channel 變成可接收
    _ := <-cancel         -> stop()      # cancellation——有人 close 了 `cancel`
}
```

## 共享狀態——actor pattern

Zerg 沒有鎖、也沒有共享可變狀態，但真實程式需要協調的可變 state——counter、cache、registry。答案是一個
**pattern**、不是新原語：一個 **actor** 就是一個**獨佔**某份 `mut` state 的 coroutine，只能經由 channel 上的訊息
觸及。單一 coroutine 一次處理一則 mailbox 訊息，所以寫入**無鎖地序列化**；又因為沒有別人握著那份 state，不會有
data race。

```text
enum Cmd {
    Add(int)                  # 寫
    Get(chan[int]<-)          # 讀——夾帶回覆用的 channel
}

fn counter(inbox: <-chan[Cmd]) {
    mut n := 0                       # state：一個普通 mut int，只有這裡獨佔
    for cmd in inbox {               # drain 到最後一個 sender 離場
        match cmd {
            Add(d)   -> n = n + d    # 寫入發生在 owner 內
            Get(rep) -> rep <- n     # 回覆到呼叫端的 channel
        }
    }
}
```

- **tell**（fire-and-forget）就是一次普通 send——`inbox <- Add(5)`。
- **ask**（request-reply）送一條全新 reply channel 並 block 等它——
  `rep := chan[int]();  inbox <- Get(rep.send);  v := <-rep!`。
- **收尾自動**——最後一個 client 放掉 send 端時，`inbox` 關閉、`for` 結束、owner 的 `mut` state 釋放；就是既有的
  channel-close 與 scope-owned 規則，沒有新增。

inbox 是個 `Ref` 值，所以**分享 actor 就是分享 inbox**（refcount-bump）——每個持有它的 copy 與 coroutine 都對著
同一個 owner 講話。這才是共享可變 state 的方式、而非 `Ref[T]`：`Ref[T]` **唯讀**地分享一個值，actor 則在一個 owner
背後**序列化寫入**。必須被序列化的資源（非 thread-safe 的 `Ref[handle]`）同樣由一個 actor 獨佔。

## producer——generator pattern

**generator 不是語言特性**——它就是一個**送值到 channel 的 coroutine**，由消費者用 `for v in ch` drain。那條
channel _就是_ `Iterator`：它一直 yield 值，直到 producer 的 scope 離開、channel 關閉，收尾的 `StopIteration`
結束迴圈。沒有 `yield` 關鍵字、沒有 generator 型別；`send` 就是 yield。

```text
fn range_gen(lo: int, hi: int, out: chan[int]<-) {
    mut n := lo
    for n < hi {
        out <- n            # 「yield」n——block 到消費者取走為止
        n = n + 1
    }
}                           # out 的 scope 結束 → channel 關閉（若為最後 sender）

for v in producer(range_gen) { use(v) }   # drain 到 StopIteration
```

早退（消費者先停）是唯一的皺褶。若消費者先 `break`，一個 blocking 的 `out <- n` 會永遠等下去——Zerg 不會 abort
一個沒有 receiver 的 send（見終止與 deadlock）。producer 用**與任何 coroutine 相同的協作方式**選擇停止：在
`select` 裡 watch 一條 **cancel channel**（見計時器與取消），消費者關掉它時就 bail。這是既有機制、不是新的。

一個專用的**人因包裝**——把 value/cancel 接線藏在單一 `for v in generate(...)` 之後，自動接好 channel 並在迴圈
離開時拆掉 producer——**擱置**。它會是純粹疊在上述零件之上的 stdlib 糖，只在需求確實成立時才加（DDD），永不是語言
改動。

## 未處理的 abort

未被 `guard` 攔下的 abort（見錯誤模型）**只殺死該 coroutine**——它的 stack unwind（釋放 scope、遞減 channel
refcount），其餘一切照常運行。這就是 fire-and-forget，但失敗**不會遺失**：以最後 sender 身分關閉 channel 時會帶著
崩潰的 `Err`，consumer 讀到 `Right(err)`（乾淨結束則帶 `StopIteration`）。

在源頭，這個死亡此外是**無聲的**。一個**可選的 compiler flag** 會**另外**把每一次未處理的 abort——`Err`、哪個
coroutine、backtrace——報到 `stderr`。它**純觀察**：開或不開，行為完全一致，且預設 build 不帶開銷。

要回報*結構化*的結果——部分結果、特定錯誤、或不會關掉受監看 channel 的失敗——coroutine 仍會 `guard` 並送進
channel。讓一個死亡變得*致命*是觀察者的職責（對 `Right(err)` 反應並 abort），絕不是 `spawn` 的事。

## 排程與公平性

M:N scheduler 是**公平的**：每個 **ready** 的 coroutine 終究會被排到，且**沒有任何 coroutine 能無限期餓死其他
人**——即使是一個從不碰 channel 的 CPU-bound 迴圈也不行。你可以放心 `spawn`；一個忙碌的 worker 凍不住無關的
coroutine。

這是對**可觀察性質、而非機制**的保證。公平**如何**達成——搶佔、compiler 插入的 safepoint、reduction 計數——是語言
不固定的實作細節；只承諾那個性質。

兩條界限框住它：

- **一次阻塞的 foreign（FFI）呼叫無法被搶佔。** 它把 OS thread 停在一個 Zerg 不擁有的 C frame 裡（見
  [FFI](ffi.zh-TW.md)）；公平只涵蓋 Zerg 的 coroutine，不涵蓋卡在 C 裡的 thread。runtime 可能長 thread pool，但
  一次長阻塞呼叫就是佔用 thread——優先用非阻塞的 C API。
- **公平讓 _ready_ 的前進，不解 _卡住_ 的。** 當每個 coroutine 都阻塞、毫無前進可能時，那是 deadlock，另外處理
  （見下）；`select` 的公平 tie-break 就是把同一種公平套用在一次等待上。

## 收尾與 deadlock

- **程式生命週期**——main 主 stack return 時，**程式結束**；仍在跑的 coroutine 就地停止、OS 回收一切。沒有 join，
  所以要是某個 coroutine 必須在退出前完成，就把它驅動到一個由 channel 觀察到的完成點。
- **對無 receiver 的 channel send 只是 block**——就算 receive 側可以證明永遠是空的，Zerg 也不會 abort 它；要等還是要放棄
  是**呼叫端**的決定（例如帶 cancel 或 timeout arm 的 `select`）。
- **全域 deadlock 偵測**——若每個 coroutine 都 block、無可能前進，runtime 會 raise **`DeadlockError`** 而非默默卡死。
  一個孤零零卡住、而其他 coroutine 仍在前進的 sender，不會被單獨偵測。
