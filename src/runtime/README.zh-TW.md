# Zerg Runtime

[English](README.md) | 繁體中文

`src/runtime/` 是 **Zerg runtime**——每支編譯後的 Zerg 程式都會連結的那一小層自足底層，外加把它包進 `zerg`
工具鏈的 Go 黏合碼。

Zerg 是**零外部依賴，like Go**：編譯出的程式不拉入任何第三方函式庫。runtime 是唯一不可再壓縮的底層——它只透過平台
C 函式庫（libc / libSystem，與 Go 相同的地基）碰 OS，別無其他。它之上的一切——[`src/stdlib/`](../stdlib) 的標準
函式庫——都是**純 Zerg**。

## 兩層

- **`csrc/`**——runtime **本體**，以 C 加上一小塊 per-architecture 組合語言核心構成。這是 `cc` 連進程式的部分：
  allocator、reference counting、collections、strings、formatting、scheduler、channels、syscall floor、以及
  unwind 機制。逐檔對照見 [`csrc/README.md`](csrc/README.md)。
- **`embed.go`**——Go 黏合碼（不會進到程式裡）。它用 `go:embed` 把 `csrc/` 整棵嵌進 `zerg0` **種子**，好讓
  `zerg build` 能把原始碼 materialize 到 emit 出的 C 旁邊交給 `cc`。
- **`runtime_test.go`**——直接編譯並測試 C runtime 的 Go 測試（透過 `csrc/zrt_test.*`）。
- **`go.mod`**——runtime 的 Go module，接進根目錄的 `go.work`。

## 它如何進到程式

1. 工具鏈建置時，C 原始碼被嵌入 `zerg0` 種子（`embed.go`、`go:embed`）。自舉的 `zerg` 不內嵌它們：
   它從磁碟讀取，位置是 `$ZERG_RUNTIME` 或 `$ZERG_ROOT/src/runtime/csrc`。
2. `zerg build foo.zg` 為 `foo` emit C，把 runtime 原始碼 **materialize** 到旁邊，再呼叫 `cc`。
3. `cc` 編譯並連結成單一 binary。不需要 runtime 的 value-only 程式一點都不連——它 emit 出的 C 是自足的。

## Cross-compilation

因為後端是 **emit C → `cc`**，跨平台編譯一支 Zerg 程式，就只是跨平台編譯那份 C：`cc --target=…` 會把 emit 出的
程式碼與 runtime 一起為目標平台建置。runtime 是可攜 POSIX C，所以任何有 libc 的 hosted 平台都行。唯一的
per-architecture 例外是 coroutine 的 context switch（`csrc/ctx_*.S`），依目標 arch 選用——正是 Go 也保留的那一小塊
平台特定核心。

## runtime / stdlib 的邊界

runtime 是**最薄的底層**：raw syscall、記憶體、reference counting、scheduler、container 原語、text rendering。
所有更高層的邏輯都在它之上以純 Zerg 實作（[`src/stdlib/`](../stdlib)）——例如 `io.read_file` 的 read loop 與
`io.write_int` 的十進位轉換，都是純 Zerg 站在 runtime 的 syscall leaf 上；`math` 的 `sqrt` / `pow` 則是純 Zerg
數值演算法，而非綁 libm。把這條線守清楚，正是語言得以自足的原因。

timer 也落在這條線上，而且正好標出它的位置。runtime 只擁有一件 coroutine 無法自己完成的事：停到某個 deadline
（`zrt_sleep_ns`）。它非得由 scheduler 擁有不可，因為閒置的 worker 必須睡到那個 deadline，而 deadlock 偵測也必須
知道「正在睡的 coroutine 遲早會自己往前走」。程式真正會用到的東西全都是它之上的 Zerg——`time.after(d)` 是一個先睡
再送值的 coroutine、`ticker(d)` 是同一件事放進迴圈、timeout 則是對兩者回傳的 channel 開一個 `select` arm。runtime
裡沒有 duration 型別、沒有格式化、也沒有任何 policy，因為這些都不需要放在裡面。

## 測試排程器,以及 ThreadSanitizer 停在哪裡

`make -C src/runtime test` 用宿主的 `cc` 編譯 `csrc/` 並執行。那套測試是 runtime 唯一被當作 C 來操練的地方——
而不是當成某支 Zerg 程式的副產物——而在它被接進根目錄的 `make test` 之前,它**從來沒有跑過**:一個 `map` 的
bug 和三個排程器 race 就這樣安然住在裡面。

`TestConcurrencyStress` 是盯著 race 的那一部分。它不能檢查事情**何時**發生,因為 M:N 之下沒有任何順序是被承諾
的,所以它檢查唯一一件交錯不得改變的事:算術。每個 producer 送出一組已知的值,consumer 把收到的加起來,而總和
是固定的。一次遺失的喚醒會讓它掛住;一個被重複排隊的 coroutine、或一次被送達兩次的交接,會讓總和跑掉。它跑很多
次,因為一個撐過一輪的 race 是家常便飯。

每一個並行測試都跑兩次:一次用機器的 worker 數,一次用 `ZRT_WORKERS=1`。重點是把兩種失敗分開——排程器自身邏輯
的 bug 在單 worker 下依然成立,而一個 race 需要好幾個——而**單 worker 模式是比較嚴苛的那個**,因為沒有別的東西
在跑、可以替一個永不讓出的 coroutine 打圓場。它上任第一天就證明了自己:`select` 有一個 livelock,只有單 worker
看得見,因為在多 worker 下,那些本來會把它解開的 coroutine 只是被別人跑掉了。

除了壓力測試,這套測試還釘住排程器**承諾**的行為,而不只是它的一致性:main 回傳就結束整個程式;deadlock 是一次
乾淨、可捕捉的中止,而且不會在某個 channel 的佇列裡留下等待者;當 `select` 所看的東西全部關閉時,它 raise 而不是
觸發某個 arm;以及一個 timer 會準時醒來,而且等待期間不燒 CPU。這幾件事失敗的方式都是掛住,所以每一個都在自己的
deadline 底下跑——一次退化會自己報上名來,而不是把整套測試停在那裡。

**ThreadSanitizer 建得起來也跑得動,但它不是一道 gate。** 一個 binary 可以像其他任何 binary 一樣用
`-fsanitize=thread` 插樁;`zergrt.h` 裡的 fiber 註記是讓那件事**根本可能**的原因——沒有它們,TSan 會跟著一次
`zrt_ctx_swap` 走到一個它沒有 shadow 的堆疊上,然後死在一個信號上,什麼也報不出來。

而它報出來的東西,到目前為止全都是同一個假象。park 協定讓一個 coroutine 取得某個 channel 的鎖,再由**排程器**
放開它——在那個 coroutine 已經離開 CPU 之後。那次交接正是整件事的重點,因為對手方不可以找到一個屬於仍在執行中的
coroutine 的等待者。它發生在同一條 OS thread 上,所以 pthreads 是滿意的;但 TSan 把 fiber 算成 thread:它看見一
個鎖被非其擁有者放開,於是不再從它推導 happens-before,此後那個鎖底下的每一次存取都讀起來像 race。那些報告是關於
一個不合身的模型的真實輸出,不是發現——每一則都落在一行可以證明持有該鎖的程式碼上。

所以**壓力測試才是那道 gate,而 TSan 是一個要刻意伸手去拿的工具**。要讓它成為 gate,需要把上面那次交接**描述給**
TSan,而不是對它隱藏,而那還沒有做。

## AddressSanitizer 需要同樣的告知,而那不是可選的

`make sanitize-conc` 是一道 gate,而它需要的那個註記,不像 TSan 的報告品質那樣只是錦上添花。什麼都不告訴它,
TSan 報出雜訊;什麼都不告訴它,**ASan 會弄壞它正在檢查的那支程式**,而且是一次幾百輪地、安靜地弄壞。

機制是 `detect_stack_use_after_return`。開啟它——某些編譯器預設開、某些不開,所以這道 gate 現在指名要求它——
一個被插樁的 frame 根本不在堆疊上:ASan 從位址空間別處一個 per-thread 的 arena 把它發給你。那件事搬動了這個
runtime 的 channel 所倚賴的唯一一樣東西。`chan.c` 停放一個活在**被停放 coroutine 自己堆疊上**的 `zrt_waiter`,
而且不在 heap 上配置任何佇列節點,因為一個被暫停的堆疊不會移動。在這道 gate 底下,那個等待者其實是在**跑過那個
frame 的那條 worker thread** 的假堆疊裡——而一條 worker 的 arena 會在它結束時被 unmap,那件事在 main 的
coroutine 結束、而其他 coroutine 還在跑的當下就會發生。等待者變成位址空間裡的一個洞,而下一次走訪那個佇列會吃到
一個 SEGV,ASan 對它一句話也說不出來,因為那個位址既不屬於它追蹤的 heap,也不屬於它認得的任何堆疊。

`__sanitizer_start_switch_fiber` / `__sanitizer_finish_switch_fiber` 把 `sched.c` 裡每一次 `zrt_ctx_swap` 夾在
中間,正是為了這件事:假堆疊於是屬於**那個 coroutine**,並隨著 coroutine 一起死去。要回去的那條 worker 的邊界是
從「結束」那一半學到的,並存在 `zrt_coro` 裡,**永遠不放在 thread-local**——一個 coroutine 會在接手它的任何一條
worker 上恢復,而一個跨越切換被快取起來的 TLS base,正是它最不可以信任的東西。

那條絆線是 `WARNING: ASan is ignoring requested __asan_handle_no_return`。它的意思是 ASan 拿執行中的堆疊去對照
一組**不屬於這個 coroutine** 的邊界;那一行裡的負數大小,就是整個 bug 濃縮成一個數字。在那個 SEGV 還沒被解釋出來
之前,它一直被當成 fiber 的假象容忍下來。它現在會讓這道 gate 失敗。
