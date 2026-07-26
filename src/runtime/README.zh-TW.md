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
- **`embed.go`**——Go 黏合碼（不會進到程式裡）。它用 `go:embed` 把 `csrc/` 整棵嵌進 `zerg` binary，好讓
  `zerg build` 能把原始碼 materialize 到 emit 出的 C 旁邊交給 `cc`。
- **`runtime_test.go`**——直接編譯並測試 C runtime 的 Go 測試（透過 `csrc/zrt_test.*`）。
- **`go.mod`**——runtime 的 Go module，接進根目錄的 `go.work`。

## 它如何進到程式

1. 工具鏈建置時，C 原始碼被嵌入 `zerg` binary（`embed.go`、`go:embed`）。
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
