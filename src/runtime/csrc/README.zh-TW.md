# Zerg Runtime — C Sources

[English](README.md) | 繁體中文

[Zerg runtime](../README.md) 的 C 實作：`cc` 會連進每支需要 runtime 的 Zerg 程式的那一層。它是可攜 C，站在平台
C 函式庫（libc / libSystem）之上，另有一小塊 per-architecture 組合語言核心負責 coroutine 的 context switch。
每個符號都帶 `zrt_` 前綴，因此絕不與編譯器 emit 的 `zg_` 名稱相撞。

## 依關注點分類的檔案

### Memory

- **`alloc.c`**——包住 `malloc` / `free` 的 allocator wrapper（freestanding 目標唯一要換的接縫）。
- **`ref.c`**——`Ref[T]`，reference-counted 的 heap box；retain / release，最後一個持有者釋放一次。

### Collections

- **`list.c`**——內建 `list[T]`，by-value 的可增長序列。
- **`map.c`**——內建 `map[K, V]`，by-value 且保留插入順序的 hash table。

### Text & conversion

- **`str.c`**——`str`（NUL 結尾的 UTF-8 `const char*`），以及 `str` ⇄ `list[byte]` / `list[rune]` 的橋接。
- **`fmt.c`**——text rendering 與 `f"…"` 格式化表面（寬度、精度、對齊、整數進位）。
- **`conv.c`**——原生型別轉換 `T(x)`：帶範圍檢查的**重新建構**，絕非 reinterpretation。

### Concurrency

- **`sched.c`**——N:1 cooperative coroutine scheduler 與 `spawn`。
- **`chan.c`**——channels：typed、buffered、支援 `select`。
- **`ctx_arm64.S`**——AArch64 的 coroutine context switch。
- **`ctx_x86_64.S`**——x86-64（System V）的 coroutine context switch。
- **`ctx_ucontext.c`**——沒有對應 arch 專屬 `.S` 時的可攜 `ucontext` fallback。

### Control & system

- **`entry.c`**——program-entry shim（`zrt_run` 佈置並執行 `main`）。
- **`unwind.c`**——abort / unwind 機制與 `defer` 的 cleanup stack。
- **`sys.c`**——最小系統表面：自舉編譯器賴以建置的行程底層（`zrt_exec`、`zrt_proc_spawn` /
  `zrt_proc_wait`、`zrt_mkdir`、`zrt_listdir`）、標準函式庫 `io` 下沉所到的 `write` / `read` / `open` /
  `close` syscall floor、abort 回報、`Atomic[int]` 運算，以及命令列 `args`。

### Header & tests

- **`zergrt.h`**——**唯一**的公開 header；編譯器 emit 的 C 只 include 這一個，且僅在程式需要 runtime 時。
- **`zrt_test.c` / `zrt_test.h`**——`#[test]` runner harness。目前無人引用：`zerg test` 產生的 driver 用 Zerg
  自己回報，因此沒有任何建置會連結它們。

## 慣例

- **單一 header。** emit 的 C 只 include `zergrt.h`，別無其他。value-only 程式（無 `Ref`、無 collections、無
  `defer` / `spawn`）完全不 include runtime，其 C 是自足的。
- 每個符號都用 **`zrt_` 前綴**，與編譯器的 `zg_` 名稱互不相交。
- MVP 階段 **hosted 於 libc**。有兩個接縫已標好，供日後 freestanding / atomic 重新設定目標而不動 emit 的碼：
  allocator wrapper（`alloc.c`）與單執行緒 reference count（`ref.c`）。
- **Layout 是一份 build contract。** 編譯器依賴的 `Ref` / `list` / `map` header layout 固定在此；它們是內部的，
  絕不被 FFI 凍結。
- runtime 是**不可再壓縮的底層**——所有更高層邏輯都在 [`src/stdlib/`](../stdlib) 以純 Zerg 實作
  （見 [`../README.md`](../README.md)）。
