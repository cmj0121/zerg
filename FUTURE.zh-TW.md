# Zerg — 留著的門

[English](FUTURE.md) | 繁體中文

這是 Zerg **決定不要的**東西,以及每一項要重新打開需要什麼。

它不是 roadmap。roadmap 說的是「接下來要做什麼」;這裡說的是「什麼被權衡過然後放棄」,好讓重新打開一個案子
變成「回答當初關掉它的論證」,而不是把討論重跑一次。每一條都寫出**門檻**——什麼必須成立——因為沒有門檻的門
不是門,是願望。

[規格](docs/conformance.md)才是語言所在的地方。這裡沒有一項屬於它。

## `dyn` — 執行期 existential dispatch

**狀態:以「多餘」關閉。**

existential 的兩種編碼,核心語言裡都已經有了。**開放**的那種是 closure——一個由函式值組成、包住被捕捉的
實作者的 struct,那正是 `#[obj]` 產生的東西([Specs & Generics](docs/core/specs.md))。**封閉**的那種是
`enum`,它的 `match` 把具體型別還給你。`dyn` 會是第一種的第三種拼法,只是把 vtable 放在 closure 已經在的
位置。

**如果要重新打開**,它會是一個**型別位置的關鍵字**加上**邊界上的 fat pointer**——Go 的模型——而絕不會是
decorator:一個值的型別「是什麼」,不可能由宣告旁邊的註記來拼。有兩件事無論如何都排除在外:

- **per-instance header 永久出局。** 每個值都要為一個 P1 量到是零的使用點付費;它並沒有解決異質大小(所以
  box 還是回來了,而 metadata 屬於 box);所有 value-semantics 的先例都走另一邊(Go 與 Swift 放在邊界、
  C++ 做成 opt-in、Java 與 Objective-C 是 reference 語言,這個問題根本不存在);而放在 emitter 的靜態
  copy/drop 旁邊的 descriptor,是同一個決定的第二份拷貝。
- **object safety,如果哪天真的需要**:`Self` 在參數位置禁止,在回傳位置必須 box。那正是 `#[obj]` 今天用來
  拒絕的同一張表,而那就是「編碼是同一個」的證據。

**門檻:一個量測。** 一個「型別集合在編譯器離場之後、在獵場裡才決定」的使用情境——不是 plugin(那是 process
邊界,`zerg lsp` 就是證明)、不是執行期 introspection、也不是 binary-stable SDK(那是 C ABI)。

## `#[derive(Reflect)]` — 把型別的結構當成值

**狀態:開著、未建、而且可 desugar。**

一個 opt-in 的 per-type 常數,描述型別的欄位與它們的型別,要求時才產生。opt-in 就是整個設計:這些知識編譯器
本來就有([Derive](docs/core/derive.md)),而標記一個型別才是讓它付費的動作。

**門檻:一個呼叫者。** serialization 是最明顯的那個,而 `Encode`/`Decode` 很可能不需要通用描述就答得出來;
這一條留著,是因為「數個 derive 共用一份描述」比「數個 derive 各自讀結構」便宜。

## `#[derive(From)]` — 一個會包住自己 variant 的 error enum

**狀態:開著,而且是唯一一個有量到需求的候選。**

這個語言選擇保留的缺口是**開放的 error downcast**——一個錯誤型別由編譯器看不到的那層決定的值。取而代之的
答案是**每層一個 error enum**,而那個答案的代價,是每個邊界上手寫的包裝。`#[derive(From)]` 就是那個包裝:

```zerg
#[derive(From)]
enum AppError {
    Io(IoError)
    Parse(ParseError)
}
```

產生每個 variant 隱含的轉換,讓邊界上的 `?` 把某層的錯誤抬進呼叫者的錯誤,而不必為它寫一個 `match`。

**門檻:沒有——這是一個「待建的候選」**,不是一個「待重開的案子」。它列在這些關上的門旁邊,是因為它是同一
種決定、只是反過來讀:那個包裝是語言選擇承擔的代價,而這是會替它付帳的糖。

## `f.[T]` — 在使用點明寫型別引數

**狀態:關閉。後綴的中括號就是索引。**

`id[int](7)` 讀起來是對 `id` 取索引,而要分辨那個下標與型別引數列,就得知道 `id` 是什麼——為了一個形式,在
parser 裡放一張符號表。generic 從它的引數取得型別,而在推導不夠時,由**有型別的 binding** 來引導
(`xs: list[int] = empty()`)。

**如果要重新打開**,拼法必須是任何索引都不可能是的那一種,而 `f.[T]` 就是一直為它留著的佔位——中括號前面一個
`.`,那是任何索引都沒有的。它是佔位,不是計畫。

**門檻:一個有型別的位置修不好的推導失敗。** 目前一個都沒找到;唯一看起來像的那個——型別參數只出現在回傳
——由 binding 的型別回答了。

## 一次深度檢查的 stack 溢位

**狀態:以 fault 的形式關閉。** 由 0.2.0 開成一扇門 —— 那次發布把規格移到它上面。

stack 溢位是一次 **fault、不是一次 abort**:runtime 會給它名字、行程以 `1` 結束,但待決的 `defer` 被跳過、沒有
`guard` 攔得住,而且它結束的是整個行程而不是那一條 coroutine(見 [Errors](docs/code/errors.zh-TW.md)、
[Conformance](docs/conformance.zh-TW.md))。理由不是疏漏,是算術:那條會去跑那些 `defer` 的 stack,正是已經耗盡的
那一條,而站在它上面的 signal handler 展不開自己腳下的東西。

在這次發布之前,規格要的是另一件事:一個**擁有每一條 stack**、並**自己檢查呼叫深度**的 runtime,好讓溢位在 fault
發生的前一個 frame 就被接住、乾淨地展開。那是 Go 的模型,也是一個真正的設計;它不是的東西,是「一個 runtime 可以
事後加到原生 stack 上的性質」。它需要 runtime 自己配置並成長它所執行的那些 stack,以及在每一個可能超出的呼叫上
加一次檢查 —— 那是幾乎沒有人會走到的情況,卻由每一個呼叫來付。

**如果要重新打開**,檢查該放在 frame 大小已知的地方 —— 編譯器 emit 的 prologue —— 而不是 runtime,這樣一個可證明
放得下的 leaf 就能省掉它。兩半必須一起落地:沒有自有 stack 的深度檢查無從比較,而沒有檢查的自有 stack 只是把 fault
搬個位置。

**門檻:runtime 擁有並自行成長它的 stack。** 這件事不在這裡任何一張清單上。在那之前,無界遞迴是一次具名的死亡而
不是一次可攔截的死亡,而 `for` 才是迴圈。

## 搶佔式排程

**狀態:以合作式的形式關閉。** 由 0.2.0 開成一扇門 —— 那次發布把規格移到它上面。

scheduler 是**合作式**的:一條 coroutine 在 channel 操作、`select` 或 sleep 讓出,在它自己讓出之前沒有東西能把它從
worker 上拿下來(見 [Coroutine 與 Channel](docs/code/coroutine.zh-TW.md))。因此一條從不 park 的 CPU-bound
coroutine 會佔住一條 worker 直到跑完,而 `M` 條這樣的 coroutine 就沒有東西能跑其他任何事 —— 包含 `main`。

在這次發布之前,規格要的是那個性質而不是機制:_沒有任何 coroutine 能無限期餓死其他人,即使是一個從不碰 channel 的
CPU-bound 迴圈也不行_。那是一個真正的保證,Go 在 1.14 用非同步搶佔買下了它。它的代價不在訊號處理 —— 而在於**每一條**
coroutine 的 stack 都必須能在一個 runtime 認得出來的 safepoint 上被中斷,這件事會伸進 emitter、stack map,以及每一個
「有值跨過一次呼叫而活著」的地方。

**如果要重新打開**,便宜的那一半先做,而且它自己就值得:**compiler 插入的 safepoint**,放在 back-edge 上,把一個沒有
channel 操作的 `for` 迴圈變成會讓出的迴圈,每次迭代的代價是一次載入與一次分支。那涵蓋了迴圈形狀的空轉者,而幾乎每一
個真實的空轉者都是那個形狀。真正的非同步搶佔 —— 一個訊號打進任意程式碼 —— 是另一半,而它需要 stack map。

**門檻:一個 back-edge safepoint 接不住的空轉者**,而且出現在某個人真的寫出來的程式裡。在那之前,紀律就是每一個合作
式 runtime 都要求的那一條:在迴圈裡放一個 channel 操作。
