# Zerg Coroutines 與 Channels

Zerg 的並行**只有 coroutine + channel**——沒有共享可變狀態、沒有 lock、沒有 future、沒有 join/handle。它建立在
[Language Reference](language.md) 的記憶體與錯誤模型之上。亦有 [English](coroutine.md) 版本。

## `spawn`

`spawn f(args)` 在 **M:N scheduler** 上啟動一個 coroutine（Go 的 `go`）。它**不回傳任何東西**——沒有 handle、沒有
join/await；結果與完成**只透過 channel** 觀察。

- **Fire-and-forget**——runtime 從不追蹤或 join 該 coroutine；要得知結果，它必須把結果送進一條觀察者持有的 channel。
- **捕獲受限**於 **immutable 值與 channel**——`mut` ref 無法跨越 `spawn`，故 coroutine 不共享可變狀態（無 data
  race）。
- **一切以複製傳入**（源死時 move 最佳化），跨 coroutine 絕不共享——channel 除外。

## Channels

channel 是一條型別化的 by-ref 管道，payload **複製**流過它。它是 Zerg **唯一 reference-counted 的值**（其餘皆
scope-owned）：在最後一個持有者的 scope 結束時 free，複製一個值會 bump 它所含 channel 的 refcount、其餘深拷貝。
channel 是 **FIFO** 且為**一等值**（可被送進另一條 channel）。

```text
ch := chan[int]()      # unbuffered——每次 send 與一次 receive rendezvous
ch := chan[int](64)    # buffered，容量 64
```

容量是唯一可調之處；**send 在滿時 block、receive 在空時 block**。unbuffered（容量 0）是 rendezvous——send 只有在
receiver 取走值時才完成，也是 Zerg 唯一的同步原語。

### 送出——`ch <- v`

send 與 receive **不對稱**：關閉是 producer 的決定，所以它一定知道。send 不回傳值——它只會完成、block，或 abort：

| channel 狀態                              | `ch <- v`                                                  |
| ----------------------------------------- | ---------------------------------------------------------- |
| 開、送得出（有空位、或有等待的 receiver） | 完成；值在 **send 當下快照**                               |
| 開、暫時送不出（滿了、或沒有 receiver）   | **block**——對還沒被收的 channel 送是合法的，不是 bug       |
| 已 close                                  | **abort**（`SendOnClosedError`）——見 [Aborts](language.md) |

### 接收——`<-ch`

receive 回傳 **`Result[T]`**：`Left(v)` 是值；`Right(err)` 代表 channel **已 close 且排空**，`err` 是**原因**——
因此崩潰原因永不遺失。它**不**代表「空、等一下」（那是 block）。

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
loop { v := <-ch ?? break }              # 逐一收，任一關閉就跳出
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
（見 [語言參考](language.zh-TW.md)）。

### send 覆蓋不變量

auto-close 是 **level-triggered**：send-count 一碰 0 就開火，沒有「等一下還有 sender 要來」的概念。因此一條規則：

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

## 未處理的 abort

未被 `guard` 攔下的 abort（見錯誤模型）**只殺死該 coroutine**——它的 stack unwind（釋放 scope、遞減 channel
refcount），其餘一切照常運行。這就是 fire-and-forget，但失敗**不會遺失**：以最後 sender 身分關閉 channel 時會帶著
崩潰的 `Err`，consumer 讀到 `Right(err)`（乾淨結束則帶 `StopIteration`）。

在源頭，這個死亡此外是**無聲的**。一個**可選的 compiler flag** 會**另外**把每一次未處理的 abort——`Err`、哪個
coroutine、backtrace——報到 `stderr`。它**純觀察**：開或不開，行為完全一致，且預設 build 不帶開銷。

要回報*結構化*的結果——部分結果、特定錯誤、或不會關掉受監看 channel 的失敗——coroutine 仍會 `guard` 並送進
channel。讓一個死亡變得*致命*是觀察者的職責（對 `Right(err)` 反應並 abort），絕不是 `spawn` 的事。

## 收尾與 deadlock

- **程式生命週期**——main 主 stack return 時，**程式結束**；仍在跑的 coroutine 就地停止、OS 回收一切。沒有 join，
  所以若某 coroutine 必須在退出前完成，就把它驅動到一個由 channel 觀察到的完成點。
- **對無 receiver 的 channel send 只是 block**——即使 receive 側可證明永久為空，Zerg 也不 abort 它；要等還是要放棄
  是**呼叫端**的決定（例如帶 cancel 或 timeout arm 的 `select`）。
- **全域 deadlock 偵測**——若每個 coroutine 都 block、無可能前進，runtime 會 raise **`DeadlockError`** 而非默默卡死。
  一個孤零零卡住、而其他 coroutine 仍在前進的 sender，不會被單獨偵測。
