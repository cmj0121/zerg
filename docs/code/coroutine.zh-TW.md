# Zerg Coroutines 與 Channels

Zerg 的並行**只有 coroutine + channel**——沒有共享可變狀態、沒有 lock、沒有 future、沒有 join/handle。它建立在
[語言參考](../language.zh-TW.md) 的記憶體與錯誤模型之上。亦有 [English](coroutine.md) 版本。

## `spawn`

`spawn f(args)` 在 runtime scheduler 上啟動一個 coroutine（Go 的 `go`；**M:N** 模型、以及「它不是搶佔式」這條
**[deviation]** 見「排程與公平性」）。它**不回傳任何東西**——沒有 handle、沒有
join/await；結果與完成**只能靠 channel** 觀察。被呼叫者可以是任何呼叫——一個普通函式、一個**方法**(`spawn
obj.run()`)、或一個**帶命名空間**的函式(`spawn mod.work()`),與 `defer` 一致,後者接受相同的被呼叫者形式
(`defer f.close()`)。

> 一個 **closure literal** 不在那三種 callee 形式之列,會被指名拒絕:這個階段的 lambda 什麼都捕獲不了,
> 所以就算接受了那個形狀,也沒有環境可以交給一條 coroutine。

- **引數是一份快照**——在 `spawn` **被寫下**的那一行取得，不是在呼叫執行的那一刻。之後才寫入的 `mut` 綁定
  不會被 coroutine 看到（它可能根本還沒開始跑）；`list`、`map`、`struct` 在那一刻成為 coroutine **自己的值**。
  （`list` 是以 copy-on-write 實現的，所以捕獲的成本是一次遞增，buffer 由先寫入的那一方複製出來 —— 這是
  實作細節，不是比較弱的保證，見 [值與記憶體](../core/memory.zh-TW.md)。）**channel 是對照，
  也是重點**：它是一個 **handle**，coroutine 拿到的是同一條 channel 的另一個 handle，之後送出的東西**看得到**。
  `defer` 以同樣方式捕獲，在 `defer` 那一行。**值被快照，handle 被共享** —— `zerg lint` 以 `L301` 說出這件事。
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
channel close 上的 `Right(err)` 傳到別人那裡（見未處理的 abort）；程式結束時，還在跑的 coroutine 只是不再被排程（見
終止與 deadlock）。

一個 scope-owned 的*值*仍可**通知**一條 coroutine——例如一個資源，它的 `drop` 往 coroutine 正在 watch 的一條 cancel
channel **送一個值**——但那是協作式通知、不是 ownership：coroutine 觀察到那個值並*選擇*停止，也仍然可以無視它。
coroutine 本身永遠不會被某個 scope 回收。（必須是 send、不是 close：乾淨關閉的 receive arm 會被除名而非觸發——見
`select`。）

## 共享與 memory model

因為 coroutine 邊界對「除 `Ref` 值以外」的一切都複製，Zerg **不需要龐大的 memory model**——根本沒有共享可變狀態
可 race。可觀察的 ordering 保證只有一條，其餘全都由它導出：

> **一個 channel 的 `send` happens-before 對應的 `receive` 完成。**

接收端因此看到的是**完整建構好**的 payload（在 send
當下快照）；
除此之外沒有任何跨 coroutine 的 ordering 存在、也不需要。這就是 memory model 的全部。任何**超出**這條邊的
ordering——ready coroutine 被恢復的 run-queue 順序、未同步 coroutine 之間的交錯——都是
**[implementation-defined]**：今日是數條 worker thread 一起抽同一條共享的 FIFO run queue，所以一條 coroutine
每次可能在不同 worker 上恢復，交錯也不可重現。任何程式都不得依賴它。

**什麼能跨越邊界**——作為 `spawn`／closure capture，或 channel payload：

- 一個 **immutable 值**——複製進去（源死時 move 最佳化）；
- 一個 **`Ref` 值**（channel，或 `Ref[T]`）——以 reference 共享、refcount-bump；
- 一個 **mutable、非-`Ref` 值**——絕不共享；送出時複製。

所以不變式精確為：**沒有共享可變的 _Zerg_ 狀態。** 跨 coroutine 分享的 `Ref[T]` 只給**唯讀視圖**——讀取與非-`mut`
方法，永不給 `mut` binding——所以並發讀不需要鎖。任何**必須被變動**的東西就不分享：由一個 coroutine 獨佔（見下方
actor pattern）。

`Ref[T]` 若包著**外部 handle**（見 [FFI](../runtime/ffi.zh-TW.md)）是 Zerg 唯一管不到的情況——它看不到資源的 C 端狀態，所以
該資源的 thread-safety 屬於那個外部 library、在本 model 之外。安全預設一樣：把 `Ref[handle]` 交給**一個** owner
coroutine。

## Channels

channel 是一條型別化的 by-ref 管道，payload **複製**流過它。它是一個 **reference-counted 的值**——`Ref` 的內建
實作者（與 `Ref[T]` 並列；見 [值與記憶體](../core/memory.zh-TW.md)），scope-owning 的例外：在最後一個持有者的 scope 結束時
free，複製一個值會 bump 它所含 `Ref` 值的 refcount、其餘深拷貝。channel 是 **FIFO** 且為**一等值**：
可以放進 struct 欄位、當成 enum payload 攜帶——actor 的 ask 就是這樣攜帶它的回覆 channel——也可以
送進另一條 channel。

```text
ch := chan[int]()      # unbuffered——每次 send 與一次 receive rendezvous
ch := chan[int](64)    # buffered，容量 64
```

容量是唯一可調之處；**send 在滿時 block、receive 在空時 block**。unbuffered（容量 0）是 rendezvous——send 只有在
receiver 取走值時才完成，也是 Zerg 唯一的同步原語。

本章的整個 channel 核心——buffered 與 unbuffered 的 block、close 通知 receiver、對已關閉 channel 的 send
abort、最後 sender 自動 close，以及 payload 在**送出當下深拷貝**、使 receiver 絕不共享 sender 的儲存
。兩個 channel 錯誤種類都已**具現化且叫得出名字**：對已關閉 channel
的 send 以 `SendOnClosedError` raise，`DeadlockError` 則是「收尾與 deadlock」所述的乾淨、可攔截 abort，兩者都能用
一般的 `err is …` 測試（見 [錯誤處理](errors.zh-TW.md)）。

**`StopIteration` 叫得出名字，卻刻意無法建構。** receiver 可以寫 `err is StopIteration` 來分辨乾淨結束與崩潰；但
**沒有**任何程式能 `raise StopIteration(…)`——這個名字不是建構子，在**兩個編譯器**裡寫了都是編譯錯誤。這個不
對稱正是「以**種類**測試、而非比對訊息字串」的全部理由：一個能 raise 這個哨兵的 sender，會讓自己的 channel 戴著
end-of-stream 標記關閉，而它的 consumer 會把崩潰讀成乾淨結束。

### 送出——`ch <- v`

send 與 receive **不對稱**：關閉是 producer 的決定，所以它一定知道。send 不回傳值——它只會完成、block，或 abort：

| channel 狀態                              | `ch <- v`                                                      |
| ----------------------------------------- | -------------------------------------------------------------- |
| 開、送得出（有空位、或有等待的 receiver） | 完成；值在 **send 當下快照**                                   |
| 開、暫時送不出（滿了、或沒有 receiver）   | **block**——對還沒被收的 channel 送是合法的，不是 bug           |
| 已 close                                  | **abort**（`SendOnClosedError`）——見 [Aborts](errors.zh-TW.md) |

### 接收——`<-ch`

receive 回傳 **`T?`**。一條串流會結束的兩種方式，在這個語言裡本來就是兩件不同的事，而且各自早就有自己的機制：
串流**結束**是**缺席**，所以是 `nil`；生產者**死掉**是**失敗**，所以會 **raise**，帶著那個生產者自己的 `Err`。
沒有人需要問「我手上這個結束是哪一種」，因為兩者根本不從同一條路來。

| channel 狀態                     | `<-ch : T?`                                 |
| -------------------------------- | ------------------------------------------- |
| 開、有值                         | 那個值                                      |
| 開、空                           | **block**                                   |
| 已 close、仍有 buffered 值       | 那個值——**先排空**，不丟資料                |
| 乾淨關閉（最後 sender 正常離場） | `nil`，而且之後每次都是 `nil`——結束是黏著的 |
| 崩潰關閉（最後 sender abort）    | **raise** 生產者的 `Err`，訊息與 kind 都在  |

這個分家正是「原因不會遺失」的全部理由。`Result` 逼收方先問「我拿到的 `Right` 是哪一種」才分得出結束與死亡；
忘了問的人就把死亡弄丟了。現在沒有人忘得掉。

`chan[T?]` 因此被**拒絕**，理由跟它變得不必要是同一個：`nil` 會同時代表「送出來的那個值」和「串流結束了」，
沒有任何運算子分得開。包進一個 struct，或約定一個哨兵值。

每種需求都由 `T?` 本來就有的四個運算子掉出來——由 **receiver** 決定：

```text
v := <-ch ?? fallback      # 串流結束了 → 用預設值
if v := <-ch { … }         # 區塊內 v 是 T；else 就是結束
v := <-ch!                 # 堅持：已經結束就中止
v := <-ch?                 # 把缺席交回給呼叫者（這個函式也答 T?）
for { v := <-ch ?? break }               # 逐一收，結束就跳出
for v in ch { use(v) }                   # 同一種 drain，串流結束處就是迴圈結束處
```

**崩潰**關閉不在上面任何一行裡，而那正是重點：它 raise。想讀原因又要繼續跑的接收端，用 `guard { <-ch }` 把它
降階，跟處理其他任何失敗一樣。

`guard { <-ch }` 交回帶著生產者自己 `Err` 的 `Right(err)`，接收端繼續跑。那就是崩潰關閉
對一支程式的全部要求——決定一次，這條串流以壞的方式結束是不是你的事——而 `guard` 就是那個決定被寫下來的地方。
至於施於 receive 的 `?`，它現在只需要任何 `T?` 都需要的東西：缺席就是一個普通的 optional，所以原本記在這裡的
「`Result[T]` 活不進簽章」問題，已經不在 channel 這條路上了。

上面那行 `match` 另外帶著一條限制，而且是最容易踩到的一條：arm 裡可以放什麼。

> **[not yet]** 規格讓 `match` arm 的 body 是一個運算式，而區塊**本身就是**運算式，所以 `Left(v) => { … }` 合
> 文法——但 `c_match` 降階成三元運算鏈，容不下一個區塊，於是那條 arm 被拒絕
> （_a block used as an expression_）——因此像 `print` 這種敘述不能站在那裡。變通做法是把 arm 寫成一次**呼叫**、
> 讓它的值就是 arm 的值，如下方 actor 範例所示。`select` 的 arm 不受影響，理由值得記清楚：`match` 的 arm 必須
> **產出**該次 match 的值，而 `select` 不產出值、它的 arm 是**執行**。所以 select arm 的 body 是一個**敘述**
> （GRAMMAR group 9）——`break` 在那裡很平常，裸的 `print` 也站得住，區塊只是眾多敘述中的一種。

## 關閉——自動發生在最後一個 sender，必須提早時才用 `close(ch)`

channel 在其**最後一個 send 能力持有者離場時自己**關閉——refcount 依方向拆開：

- **send-count → 0 ⇒ close**（receiver 仍可排空 buffered 的值），
- **完全無持有者 ⇒ free**。

**一個持有者在它的 binding scope 離開時離場**，且是**每一條**離開路徑：區塊結尾、`return`、`break` 或 `continue`、
以及 abort unwind。

這是日常形式，也是崩潰中的 producer **唯一**能走的那條：abort 到不了任何敘述，所以正是 unwind 放掉它的 send 端在
攜帶原因。因此正常結束與崩潰走**同一條路徑**關閉——**帶原因**：乾淨離場給 `StopIteration`、abort 給崩潰的 `Err`。
receiver 從上面的 `Right` 讀到它，所以崩潰以一個普通錯誤抵達 consumer，而非讓它對著孤兒 channel 永遠 block。

### `close(ch)`——提早結束一條 stream

`close(ch)` 是**條件式**形式：在我的 scope 結束*之前*就結束這條 stream。它是一個**敘述、不是呼叫**——`close` 是關
鍵字，不指涉任何函式、也不產生值，所以不能被傳遞、綁定或 spawn。`defer` 把它當成唯一一個非運算式的形式接受。

```text
close(ch)              # 這條 stream 結束了
defer close(ch)        # ……在區塊離開時，每一條路徑都算，含 abort unwind
```

它標記的是 **channel**、不是某個持有者，其餘一切由此導出：

- **由構造保證冪等**——關閉一條已關閉的 channel 什麼都不改。不報錯、不 abort、也沒有帳要記。
- **不移動任何計數**，所以 `ch` 之後仍是一個好端端的 handle：仍可讀、仍可複製。
- **已 buffered 的值照樣送達**——receive 會先交出 channel 手上的東西，才回答 `Right`。
- **之後的 send 會 abort**（`SendOnClosedError`），而不是被默默丟掉。
- **receive-only 端不得 close**——consumer 不可以代替 producer 結束一條 stream。那是編譯錯誤
  （_cannot close a receive-only channel_）。

`close` **不**取代 auto-close，兩種形狀說明了原因。**崩潰中**的 producer 到不了任何敘述。而在 **fan-in** 裡，數個
producer 中最後完成的那個自然結束了 stream、完全不需要協調——由其中一個手足呼叫的 channel 層級 close，會替其他人
把 stream 結掉，而那正是本設計要避開的陷阱。

### 提早關 = 縮 scope

第三條路完全不需要敘述：把 send 端放進更窄的 scope，讓離開動作去關它。既然 scope 離開現在真的會歸還 channel
binding 所持有的東西，最自然的寫法就是一個 **factory**——建立 channel、`spawn` producer，然後交回一個
**receive-only** 端：

```text
fn source(n: int) -> <-chan[int] {
    ch := chan[int](4)
    spawn producer(ch, n)
    return ch              # `ch` 自己的持有隨 `source` 結束——producer 成為最後一個 sender
}

for v in source(4) { use(v) }   # 呼叫端從來不是 sender，所以它撐不開這條 stream
```

本章其餘部分都是照這個形狀寫的，而它成立的理由正是「refcount 依方向計數」：呼叫端的端只計入 receive-count，所以
stream 恰好在 producer 結束時結束。

`del ch` **不是**停止送出的方法。`del` 對每一種型別都只有一個意思——**撤銷這個名字**——所以它放掉這個 binding 的
持有，*而且*讓之後任何對 `ch` 的使用變成編譯錯誤（_`ch` is used after del_）；見
[值與記憶體](../core/memory.zh-TW.md)。用它來交出一個你已經用完的持有，絕不要拿它當通知 consumer 的訊號。

### send 覆蓋不變量

auto-close 是 **level-triggered**：send-count 一碰 0 就開火，沒有「等一下還有 sender 要來」這種概念。所以就一條規則：

> 從建立起、到你認定*此後再無任何 send* 為止，**任一瞬間都必須至少存在一個 send 能力的持有者。**

一個由 coroutine 持有的 send 端，會在該 coroutine 的整個生命（睡著與否）維持 send 側存活；唯一的失敗是出現一個
**完全沒有 send 端的空窗**。

```text
ch := chan[int]()
spawn consumer(ch)          # ch 在 consumer 的 `<-chan[int]` 參數處窄化;建立者仍握著雙向的 `ch`
... 延遲 ...                # 安全：建立者的端讓 send-count ≥ 1 撐過延遲
spawn producer(ch)          # ch 在 producer 的 `chan[int]<-` 參數處窄化
```

安全：建立者握著 send 能力端時，consumer 只是 **block**。只有當你**先放掉自己的 send 端**、*再*延遲、而 producer
尚未存在時才會壞——那個空窗提早關閉 channel，稍後的 send 則 abort。準則：**自己的 send 端最後才放**（如同 Rust 的
`mpsc`）。

## Directional channels

裸的 `chan[T]` 是**雙向**的。它可 **narrow** 成單向端——**send-only**（`ch <- v`，給 producer）或 **receive-only**
（`<-ch`，給 consumer）。

**narrowing 是單向的**，絕不能回到雙向——這是安全保證：send-only 端**不可能**偷收值，receive-only 端也不可能插入
值。方向型別的目標（參數、`return`、typed binding）把這個端**包**成窄化的視角——position 只會包值、絕不轉換值
（[型別系統](../core/type-system.zh-TW.md)）——且**不會**放掉你自己的持有：目標拿到窄化的視角，你仍保有自己的
雙向端。窄化後的 binding 會取得**自己的**一份 reference，所以結束它的 scope 就會把那份 reference 還回去：要放
掉你自己的貢獻，可以結束該 binding 的 scope（上面的 factory、或更窄的區塊）、用 `close(ch)` 在保留 handle 的
同時結束 stream、或用 `del ch` 把持有與名字一起交出。

方向也正是讓 auto-close **精準**的關鍵，因為 refcount 依方向計數：send-only 計入 send-count、receive-only 計入
receive-count、雙向**兩者都計入**。所以要看到「producer 做完」的 consumer 必須持 **receive-only** 端——雙向
consumer 會被算成 sender，害 channel 永遠開著。雙向端適合**對稱壽命**用法（自用緩衝、共享的 worker-pool channel），
最後一個成員離場時 close 與 free 同時發生。雙向對話用**兩條** directional channel——一條共享的雙向 channel 會把
任一值路由給任一 receiver，那是 race，不是對話。

## `select`——同時等多條 channel

`select` 是**唯一**的多路等待：它盯住多個 send/receive 操作，block 到其中一個 **ready**，執行那條 arm。在**多條
arm 同時 ready** 時，勝出者是 **[implementation-defined]**——規格只固定選擇**不依位置**，故沒有 arm 因所在位置而
餓死；預期性質是**公平**。bootstrap 以決定性的**round-robin rotor** 實現它（把同一種公平套用在單次等待上），但
conforming 實作可任選一條 ready arm，所以任何程式都不得依賴哪條勝出。

```text
select {                    # 挑「一條」ready 的 arm 跑
    v := <-a => use(v)      # receive arm：開著且有值才 ready；v 是 T
    b <- x   => sent()      # send arm：送得出去才 ready
    _        => tick()      # 此刻沒人 ready → 非阻塞
}

for select {                # 同一種等待，但是 LOOP：一圈跑一條 ready 的 arm……
    v := <-a => use(v)
    v := <-b => use(v)
}                           # ……而且在所盯的 receive channel 全部結束時結束
```

**select 負責挑，不負責結束。** 結束是迴圈的事，`for select` 就是擁有它的那個迴圈——沒有終局 arm、沒有計數器、
沒有旗標，而且出口寫在迴圈頭，也就是讀者會去找的地方。

- receive arm 綁的是一個普通的 **`T`**。會觸發的 arm 一定有值：**乾淨關閉**是缺席，那條 arm 會被**從等待中除名**
  ——不觸發、也不參賽，這正是「結束的生產者不會餓死還活著的那條」的原因。
- **崩潰關閉**會從 select **raise** 出去，帶著生產者的 `Err`。它永遠不會抵達任何 arm body，所以沒有接收端能在
  沒注意到的情況下跨過它。
- 落在已 close channel 上的 **send arm** 被選到時 **abort**（send-on-closed 是 bug）。
- **`_`** 在此刻沒有任何 arm ready 時觸發，使 `select` **非阻塞**。它**不是**耗盡狀態的答案：一旦不可能再有東西
  ready，「現在還沒有」就是謊話，而繞著那個謊話的迴圈會空轉。它在執行前會先讓出排程器，所以輪詢迴圈不會餓死它
  所在的那個 worker。
- **一次性**的 `select`，若所盯的 receive channel 全部結束，就無處可去，於是 **abort**（`DeadlockError`）——等待
  一件不可能發生的事，被指名。`for select` 則是結束；這就是兩種拼法的差別。

粒度是刻意的：單一 receive 逐值把結束回答成 `nil`；`select` 則把結束的那條 arm 除名——於是乾淨關閉永不混進
「有資料」的競賽，也就不空轉。

除名規則有兩個後果值得明講，因為忽略它們的 `select` 是**卡死**、不是行為怪異：

- **關閉一條 channel 不會觸發它自己的 arm。** 乾淨關閉只是把那條 arm 從等待中移除，它永遠不會 ready。一次 close
  想表達的東西，必須以一個**值 send** 出來，或者就是 `for select` 在等的那個結束。
- **迴圈只在*每一條*被監看的 receive channel 都結束時才結束。** 只要有一條還開著——取消方仍握著的 cancel channel、
  還沒有人結束的 mailbox——迴圈就繼續。一次性的 `select` 在同樣處境下再也沒有任何東西可能 ready，
  runtime 會在決定程式結果的地方回報它：`DeadlockError`，raise 在 `main` 上。

## Timer 與 cancellation

**timeout** 與 **cancellation** 都從 channel 與 `select` 掉出來——沒有新 primitive。本節的完整可執行版本是
[`examples/13_cancel.zg`](../../examples/13_cancel.zg)，`make examples` 會用 `zerg` 建置它**並執行**。

- **timer 就是一條 channel。** `time.after(d)` 回傳一條 receive-only channel，在 `d` 之後**一次**變成可接收
  （`time.ticker(d)` 則重複觸發）；`select` 對它的一個 receive arm 就是 **timeout**。`d` 是以**奈秒**為單位的
  stdlib duration、clock 是 ambient-OS 的 stdlib 設施（如 `env`），都不是 primitive。——見
  [標準函式庫](../runtime/stdlib.zh-TW.md)。
- **cancellation 也是一條 channel。** 給 coroutine 一條 **cancel channel**、讓它在自己的 `select` 裡監看，並用
  **送一個值**來取消。因為 `spawn` 是 fire-and-forget、**無 handle，所以沒有 preemptive kill**——cancellation 是
  **合作式**的：一個 coroutine 只會因 return、或因察覺 cancel/timeout arm 而選擇停下才結束。

**用 send 取消，不要用 close。** close 是「stream 結束」而不是一個事件：乾淨關閉的 receive arm 會被*除名*，所以
close 掉 `cancel` 永遠不會觸發 `cancel` 那條 arm。close 它是餵給**迴圈的結束**——而那要等**每一條**被監看的 receive
channel，所以它只有在工作也做完之後才來。兩者互補而非可互換：**send** 用來提早停下，**close**（或單純讓最後一個
持有者離場）用來宣告「不會有取消了」，那正是讓工作跑完時迴圈得以結束的條件。

```text
fn stage(work: <-chan[int], cancel: <-chan[int], out: chan[int]<-) {
    mut total := 0

    for select {
        v := <-work           => { total = total + v }          # v 是 int：會觸發的 arm 一定有值
        <-cancel              => { out <- total  return }       # 提早停——有人 SEND 了一個值
        <-time.after(1000000) => { out <- total  return }       # timeout——1ms，單位是奈秒
    }
    out <- total                                                # work 與 cancel 都結束了
}

fn main() {
    cancel := chan[int](1)
    out := chan[int](1)

    spawn stage(source(3), cancel, out)

    cancel <- 1        # 現在就停它……
    # close(cancel)    # ……或者：不取消，讓工作跑完、迴圈自己結束

    print (<-out)!
}
```

迴圈不是裝飾。一個 channel 全部結束、又沒有 `_` 的一次性 `select` 會以 `DeadlockError` abort，那就是一個被
忘記的收尾自報家門的方式。

**一個 timer 要花掉一條 coroutine。** `after` 與 `ticker` 是疊在「把 coroutine 停到某個 monotonic deadline」這唯一
一個 runtime leaf 之上的普通 Zerg，所以**每一個活著的 timer 就是一條 coroutine、帶著自己的 256KB stack**。放在迴圈
裡的 `after`——像上面那個 `select`——會**每一輪**配置一個，而每一個都活到它的 deadline 過去、值被取走為止。
**`ticker` 停不下來**：沒有東西能取消一次 sleep，所以它的 coroutine 會活到程式結束。ticker 該放在程式頂端，不是放
在迴圈裡。

排程器那一半在 runtime 兩者都建好了:閒置的 worker 會睡到最近的截止時間而不是空轉,而一次待決的睡眠
永遠不會被當成 deadlock。

## 共享狀態——actor pattern

本節的完整可執行版本是 [`examples/12_actor.zg`](../../examples/12_actor.zg)，由 `make examples` 建置並執行。

Zerg 沒有鎖、也沒有共享可變狀態，但真實程式需要協調的可變 state——counter、cache、registry。答案是一個
**pattern**、不是新原語：一個 **actor** 就是一個**獨佔**某份 `mut` state 的 coroutine，只能經由 channel 上的訊息
觸及。單一 coroutine 一次處理一則 mailbox 訊息，所以寫入**無鎖地序列化**；又因為沒有別人握著那份 state，不會有
data race。

```text
enum Cmd {
    Add(int)                  # 寫
    Get(chan[int]<-)          # 讀——夾帶回覆用的 channel
}

fn answer(rep: chan[int]<-, n: int) -> int {
    rep <- n                         # 回覆到呼叫端的 channel……
    return n                         # ……並讓 state 維持原樣
}

fn counter(inbox: <-chan[Cmd]) {
    mut n := 0                       # state：一個普通 mut int，只有這裡獨佔

    for cmd in inbox {               # drain 到最後一個 sender 離場
        n = match cmd {              # 對 state 的每一次寫入都是這一個 assignment
            Cmd.Add(d)   => n + d    # 寫入發生在 owner 內
            Cmd.Get(rep) => answer(rep, n)
        }
    }
}
```

`answer` 之所以存在，是因為 **match arm 的 body 是一個運算式**：send 是敘述、不能站在 arm 裡，所以回覆改走一次呼
叫、而它的值就是要保留的 state。這也順帶讓 owner 的 state 只在一個地方被寫。（區塊 `{ … }` 在文法上也是運算式、本
來可以用在這裡，但出貨的 `zerg` 不行——見「接收」一節的 `[deviation]`。）

- **tell**（fire-and-forget）就是一次普通 send——`inbox <- Add(5)`。
- **ask**（request-reply）送一條全新 reply channel 並 block 等它——
  `rep := chan[int](1);  inbox <- Get(rep);  v := (<-rep)!`。`Get` 的欄位型別為 `chan[int]<-`，故 `rep` 進入訊息時
  窄化成 send-only，而 caller 保有它的 receive 端。
- **收尾自動**——最後一個 client 放掉 send 端時，`inbox` 關閉、`for` 結束、owner 的 `mut` state 釋放；就是既有的
  channel-close 與 scope-owned 規則，沒有新增。

inbox 是個 `Ref` 值，所以**分享 actor 就是分享 inbox**（refcount-bump）——每個持有它的 copy 與 coroutine 都對著
同一個 owner 講話。這才是共享可變 state 的方式、而非 `Ref[T]`：`Ref[T]` **唯讀**地分享一個值，actor 則在一個 owner
背後**序列化寫入**。必須被序列化的資源（非 thread-safe 的 `Ref[handle]`）同樣由一個 actor 獨佔。

對單一共享純量，較低階的替代是用不可變 `:=` 持有一個 stdlib **`Atomic`**（綁定不可變，atomic 內部可變——見
[Module 與 Program](../runtime/package.zh-TW.md)）。它提供 lock-free 的 `load` / `store` / `swap` /
`fetch_add` / `compare_swap`。

> **[not yet]** 在**出貨的 `zerg`** 上，而且原因與 atomic 本身無關：`Atomic[int]` **就是**一個 `Ref[int]`，而
> `zerg` 沒有 `Ref[T]`。它會指名拒絕整個模組——_NotImplemented: a generic struct `Atomic[…]` — this compiler
> erases type parameters, and a field names one_——而不是吐出一個沒人宣告的型別，所以 `import "std/atomic"` 在
> 那裡是一則乾淨的診斷；上面的 actor 才是**兩個編譯器**都成立的做法。

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

fn range(lo: int, hi: int) -> <-chan[int] {
    ch := chan[int]()
    spawn range_gen(lo, hi, ch)
    return ch               # 呼叫端拿到 receive-only 端，從來不是 sender
}

for v in range(0, 10) { use(v) }   # drain 到 StopIteration
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

runtime 會把它**報在 `stderr`** 上——就是那個 `Err` 的訊息，如同頂層 abort 也會印出一則——然後該 coroutine 就
消失，程式繼續跑。這則回報是**純觀察**的：它是 unwind 本來就知道的東西，順路印出來而已，程式行為完全不依賴
它。

> **[not yet]** 這則回報只有訊息，沒有別的。指出它來自**哪個 coroutine**、印出 **backtrace**、以及用一個
> **compiler flag** 決定要不要印，三者都尚未建置——所以一個有很多 coroutine 的程式拿得到原因，卻沒有對應的
> `spawn` 位置可以掛上去。

要回報*結構化*的結果——部分結果、特定錯誤、或不會關掉受監看 channel 的失敗——coroutine 仍會 `guard` 並送進
channel。讓一個死亡變得*致命*是觀察者的職責（對 `Right(err)` 反應並 abort），絕不是 `spawn` 的事。

## 排程與公平性

**預期。** `spawn` 讓 coroutine 跑在**搶佔式 M:N scheduler** 上（多條 coroutine 多工於數條 OS thread），而該
scheduler 是**公平的**：每個 **ready** 的 coroutine 終究會被排到，且**沒有任何 coroutine 能無限期餓死其他
人**——即使是一個從不碰 channel 的 CPU-bound 迴圈也不行。你可以放心 `spawn`；一個忙碌的 worker 凍不住無關的
coroutine。這是對**可觀察性質、而非機制**的保證：公平**如何**達成——搶佔、compiler 插入的 safepoint、reduction
計數——是語言不固定的實作細節；只承諾那個性質。

**今日。** scheduler **確實是 M:N**——`M` 條 worker OS thread（預設每顆 CPU 一條）抽同一條共享的 FIFO run
queue，coroutine 在 worker 之間自由遷移，因此可能在一條它從未啟動於其上的 thread 恢復。它**不是**的，是搶佔式。

> **[deviation]** 規格要求沒有任何 coroutine 能無限期餓死其他人；而 scheduler 是**合作式**的：coroutine **只在**
> channel 操作、`select` 或 sleep 讓出，在它自己讓出之前沒有東西能把它從 worker 上拿下來。因此一個**從不 park
> 的 CPU-bound coroutine 會佔住一條 worker**，直到它跑完為止。這個失敗的形狀是「數量」而不是「開關」：一個空轉
> 者吃掉一顆核心，`M` 個空轉者就沒有東西能跑其他任何事——包含 `main`——而在單 CPU 主機（`M` = 1）上，第一個空
> 轉者就已經等於整個程式。搶佔與 compiler 插入的 safepoint 是**延後**、不是放棄；在其中之一落地前，讓每條
> coroutine 皆由 channel 驅動，使它會 park 並讓別人跑，並把任何無界的計算迴圈都當成「裡面需要一個 channel
> 操作」。

兩條界限框住這個模型：

- **一次阻塞的 foreign（FFI）呼叫無法被搶佔。** 它把 OS thread 停在一個 Zerg 不擁有的 C frame 裡（見
  [FFI](../runtime/ffi.zh-TW.md)）；公平只涵蓋 Zerg 的 coroutine，不涵蓋卡在 C 裡的 thread。它佔住**一條
  worker**、其餘仍照跑——與 CPU-bound coroutine 同一套帳——但一次長阻塞呼叫就是佔用 thread，所以優先用非阻塞的
  C API，並預期在 `M` 為 1 時它會阻塞整個程式。
- **公平讓 _ready_ 的前進，不解 _卡住_ 的。** 當每個 coroutine 都阻塞、毫無前進可能時，那是 deadlock，另外處理
  （見下）；`select` 的公平 tie-break 就是把同一種公平套用在一次等待上。

## 收尾與 deadlock

- **程式生命週期**——main 主 stack return 時，**程式結束**。沒有 join，所以要是某個 coroutine 必須在退出前完成，
  就把它驅動到一個由 channel 觀察到的完成點。

  程式結束所保證的是一句關於 **run queue** 的陳述：任何 park 中的都不會再被喚醒、任何排隊中的都不會被啟動。它
  **不會**停下一條**已經在跑**的 coroutine，因為沒有東西能搶佔它（見排程與公平性）——一條在別的 worker 上正算到一
  半的 coroutine 會一路跑到它自己 park 或 return 為止，而行程就活得比 `main` 久那麼久。兩半都是可觀察的：只有一條
  worker 時，一個空轉的 coroutine 佔住它，`main` 在那條 coroutine 讓出之前根本無法恢復、更談不上 return；有數條
  worker 時，`main` 在空轉者仍在跑的情況下 return，而行程在空轉者結束時才結束。

  > **[deviation]** 規格寫的「仍在跑的 coroutine 就地停止」是搶佔式的讀法，而 scheduler 並非搶佔式。請把 `main`
  > return 理解成*不再排程*、而不是一次 kill；任何工作必須被中途切斷的 coroutine，請給它一條 cancel channel 去
  > 觀察。

- **對無 receiver 的 channel send 只是 block**——就算 receive 側可以證明永遠是空的，Zerg 也不會 abort 它；要等還是要放棄
  是**呼叫端**的決定（例如帶 cancel 或 timeout arm 的 `select`）。
- **全域 deadlock 偵測**——若每個 coroutine 都 block、無可能前進，runtime 會 raise **`DeadlockError`** 而非默默卡死。
  一個孤零零卡住、而其他 coroutine 仍在前進的 sender，不會被單獨偵測；一次待決的 sleep 也不算 deadlock——等
  timer 的 coroutine 終究會前進，所以只要還有 sleep 未到期，偵測器就退場不判。

  `DeadlockError` 是一次與其他 abort 無異的**乾淨 abort**：它 unwind、跑 pending `defer`，`guard` 也攔得住。
  在攔它之前有兩件事值得知道。

  - **受害者是 `main`。** 這個 abort 在 `main` 的 coroutine 上 raise，絕不落在被卡住的環裡任意一員身上。
    deadlock 是關於**整個程式**的陳述，所以它落在程式結果被決定的地方——`main` 的 `guard` 與 `main` 的 exit
    status。交給別條 coroutine 的 `guard`（它對這個全域狀況一無所知）只會讓它被吞掉。
  - **每次偵測都 raise，沒有 one-shot。** 因此放在重試迴圈裡的 `guard` 會把 deadlock 變成一次
    **livelock**——程式一圈一圈跑下去，每一圈都回報它的原因。這是刻意的取捨：one-shot 偵測器在第二次發生時會
    沉默並卡死，而那正是這個機制存在的目的所要防的。圍著 deadlock 的 `guard` 應該改變些什麼、或停下來，而不是
    原封不動地重試。
