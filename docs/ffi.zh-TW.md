# Zerg 外部函式介面（FFI）

Zerg package 如何與 **C ABI** 交界——這是唯一一處 Zerg 值變成 C 值、再變回來的邊界。因為 C 是 Zerg 的
**codegen target**（不只是逃生口），FFI 是原生概念：它直接建立在 [語言參考](language.zh-TW.md) 的型別、記憶體、
錯誤與可見性模型，以及 [Module、Package 與 Program](package.zh-TW.md) 的 public surface 規則之上。
亦有 [English](ffi.md) 版本。

## 兩條邊、一份契約

FFI 有兩個方向，它們刻意重用你已經有的機制，而不是各自長出一套新語法：

| 邊         | 方向     | 如何表達                                                            |
| ---------- | -------- | ------------------------------------------------------------------- |
| **export** | Zerg → C | **無新語法**——package 的 public surface _就是_ 它的 C ABI，按需輸出 |
| **import** | C → Zerg | **無新語法**——由 **stdlib** 把外部 C 符號綁成一個 `unsafe fn`       |

兩條邊共用**同一份**「哪些值可跨界」的定義（FFI-safe 型別）、**同一條**邊界記憶體所有權規則，以及**同一套**
錯誤與並行處理。**兩條邊都不是語法**：export 依附 `pub` surface，而 import 是一個 **stdlib 設施**——沒有
`extern` 關鍵字——其外部呼叫是 **`unsafe`** 的，位於 `unsafe` 情境內（見下）。

## FFI-safe 型別

只有具備**固定、且在邊界上已知的 C 可表示 layout** 的值能跨界。形式上，一個宣告是 **FFI-safe**，當且僅當它提到的
每個型別都是 FFI-safe；FFI-safe 型別就是那些 primitive、對 FFI-safe `T` 的 `list[T]`、opaque 外部 handle、
一個不捕獲的頂層 `fn`，以及任何**非遞迴**、由它們層層組成的 `struct`／`enum`。

| Zerg                        | C 表示法                           | 說明                                          |
| --------------------------- | ---------------------------------- | --------------------------------------------- |
| `bool`                      | `bool`（`<stdbool.h>`）            |                                               |
| `byte`                      | `uint8_t`                          | Zerg 的 char／一個原始位元組                  |
| `rune`                      | `int32_t`                          | 一個 Unicode **code point**（純量，非 UTF-8） |
| `int`                       | `int64_t`                          | 溢位在 Zerg 內仍 abort（見「錯誤」）          |
| `uint`                      | `uint64_t`                         | unsigned；溢位在 Zerg 內仍 abort              |
| `float`                     | `double`                           | 純 IEEE-754，不變                             |
| `str`                       | `const char*`                      | NUL 結尾的 UTF-8；入境 copy、出境 borrow      |
| `list[T]`（FFI-safe `T`）   | `T*` **+** `size_t` 長度           | 一個 fat 值（指標 + 長度）；入境 copy         |
| 不捕獲的頂層 `fn`           | C function pointer                 | `fn` 型別中的 `mut &` 參數降為指標參數        |
| `struct`（欄位皆 FFI-safe） | C `struct`，by value               | 逐欄位對應；`list`／`str` 欄位為 fat          |
| `enum`（payload FFI-safe）  | tagged union `{ tag; union {…}; }` | discriminant + payload（layout 待決，見下）   |
| `T?`                        | tagged union——**除**下述例外       | pointer-shaped `T` → 一個 nullable pointer    |
| opaque handle               | opaque `typedef`（指標形狀）       | 一個外部資源——Zerg 永不解參考                 |

一個 **`T?`、其 `T` 為 pointer-shaped**（opaque handle，或 `fn`）**不會**長出 tag：`nil` 就是**null pointer**、
值就是那個裸指標。只有非指標 `T` 的 `T?`（例如 `int?`）才需要 tagged 形式。這就是為什麼 handle 的 out-parameter 能
對上 C 的 `T**` 慣例（見範例）。

**非 FFI-safe**——在外部綁定的簽章中被拒、也不會進到匯出的 header，且**一律附診斷、絕不靜默**：

- **generic 與 `spec` bound**——generic 在 monomorphize 前不是單一型別，所以沒有單一 C 簽章。改讓一個**具體**實例跨界
  （在固定型別上包一層 `pub` wrapper）。
- **`spec` 用作型別**（existential）——heap-boxed 且動態分派，沒有穩定 layout。這就是為什麼**`Result[T]` 永遠不是
  FFI-safe**：它右側恆為 `Err`，也就是 `Error` spec 用作型別，沒有任何例外。匯出時改用 `T?`（右側是具體的
  `nil`）或 `Either[T, C]`（`C` 為具體、FFI-safe 的錯誤型別）——不是新規則，只是把型別映射套上去。
- **`chan` 與 coroutine handle**——由 runtime 管理、reference-counted、綁定 scheduler；對 C 毫無意義。
- **會捕獲的 closure**——它是一個以 capture 為欄位的 scope-owned struct（見語言參考）。只有**不捕獲的頂層
  `fn`** 能跨界，成為一個純 C function pointer（見「並行」）。
- **遞迴或自我參照的型別**——compiler 會把它 **auto-box**（插入一層 heap indirection，見語言參考），因此它沒有
  **flat**（連續、非 boxed）的 C layout，跨界時會把 Zerg 擁有的 heap 一起拖過去。先把它壓平成一個 FFI-safe 形狀
  （一個 id 或索引）。

**C 的整數寬度。** Zerg 的 `int`／`uint`／`byte`／`rune` 是固定的（i64／u64／u8／i32）——`uint` 恰好對映
`uint64_t`——但 C 的**平台寬度** `int`、`unsigned`、`long`、
`size_t`……仍**沒有固定的 Zerg 對應**（`size_t` 不保證是 64-bit）。`list` 的 `size_t` 長度由 compiler 產生、不是你
命名的值——但一個必須命名 C `int` 或 `size_t` 的外部綁定，需要一組僅存在於邊界的 C 寬度別名（`c_int`、
`c_uint`、`c_size`……）。那組別名**尚待決定**（見「待決問題」）；在它落地前，只有 Zerg 的固定寬度能跨界。

## Opaque handle——沒有指標的外部資源

Zerg **沒有 pointer surface** 且 **safe by default**，所以 FFI 不能把一個可解參考的裸指標偷渡進語言。它也確實
沒有。外部資源以 **opaque handle** 跨界——即 stdlib 那個指標形狀的 **`handle`** token，Zerg 能持有卻永遠無法
窺看。它**沒有無主體的型別宣告**（裸的 `type sqlite3` 無法 parse——`type` 是強 typedef、需要右手邊）；raw token
就是 stdlib 的 `handle`，而一個具名資源是包在你自己擁有的 newtype 裡的 `Ref[handle]`（與 [Process 與 I/O](io.zh-TW.md)
的 `File = Ref[handle]` 同一套**foreign-handle pattern**）。

stdlib 把每個外部符號綁成一個**`unsafe fn`**、其簽章由你提供——linker 名**原封不動**、不 mangle：

```text
import "ffi"

sqlite3_open  := ffi.symbol[unsafe fn(path: str, mut &db: handle?) -> int]("sqlite3_open")
sqlite3_close := ffi.symbol[unsafe fn(db: handle) -> int]("sqlite3_close")
```

（確切的 stdlib API 是 stdlib 細節；**語言**所固定的是：結果是一個 `unsafe fn`、只能在 `unsafe` 內呼叫。）
一個 handle **可以**存進 binding 或欄位、被複製、傳給其他外部呼叫、以及被 `del`。它**不能**被解參考、索引、
做算術，或用 constructor 建構（它沒有欄位）——它只會以外部呼叫的回傳或 out-parameter 形式出現。

一個 handle 是一個**像任何 primitive 一樣 by-value 複製的 opaque token**——複製它就是複製那些 bit、在 Zerg
的意義上不共享任何東西、也不釋放任何東西。所以裸 handle **不是**記憶體模型的新例外：它就是些 bit，而一個含它的
struct 也照樣像其他值一樣深拷貝（reference-counted 的值仍是 `chan` 與 `Ref[T]`，裸 handle 兩者皆非）。唯一的
微妙之處是這個 token **命名了一個 Zerg 不擁有的資源**：那個外部配置完全活在記憶體模型之外，所以 scope 結束只
釋放 token（不過是些 bit，微不足道）、卻永不釋放資源；對一個 handle binding 下 `del` 也只終止名字，不釋放任何
外部東西。資源**只**由一次明確、配對的**外部 free** 釋放。

把 raw `handle` **包進一個 `Ref[handle]`**——那個 reference-counted 資源盒（見語言參考），其 `drop` 就是配對的
外部 free——再放進一個你自己擁有的 newtype（一個單欄位 `struct`，[package.zh-TW.md](package.zh-TW.md) 的模式，**不**採
那個會把盒子再度外洩的 auto-cast）。它的**私有欄位使它在 module 外不透明**，所以只對外提供安全方法，而 `Ref[T]` 讓
close **精確**：被複製、回傳、或跨 `spawn` 送出，每個 `Db` 都命名同一條連線、在最後一個持有者的 scope 退出時**關閉一次**：

```text
struct Db { h: Ref[handle] }                           # 私有 + refcounted ⇒ 不透明、只關一次

pub fn open(path: str) -> Db? {
    mut raw: handle? = nil                             # 一個 mut handle out-parameter：C 的 sqlite3**
    status := unsafe { sqlite3_open(path, raw) }       # 外部呼叫是 UNSAFE——只能在 `unsafe` 內
    if status != 0 { return nil }                      # 手動映射 C 的狀態碼——沒有魔法
    return Db(h: Ref(raw!, sqlite3_close))             # 建構是一次 CALL；盒子的 drop 就是配對的 free
}
```

這裡的 `unsafe { … }` 是一個**block-expression**：它產出那次呼叫的 `int`，供下一行檢視。`mut &handle?`
out-parameter 之所以成立，是因為 handle 是一個**以值寫回的 scalar token**——就是普通的 `mut &` 參數路徑
（見語言參考），沒有新東西；注意簽章裡的 `mut &` 標記在名字**之前**。一個**就地填入呼叫端 byte buffer** 的
C 函式是另一種機制（它透過指標*寫入*，不是值的寫回）；那個 write-back 協定尚待決定（見「待決問題」）。

**`Ref[handle]` 讓 close 精確。** 複製一個 `Db` 是對盒子 refcount-bump、而非複製裸 token，其 `drop`——配對的
`sqlite3_close`——在最後一個 `Db` 離開 scope（或被 `del`）時**跑一次**。這正是裸的 `struct Db { h: handle }` 給不了
的保證——那裡兩份 copy 會各自試圖關掉同一條連線；而私有欄位讓裸 token 無法逸出，於是這個保證在普通的「安全」
程式碼中也成立。`Ref[T]` 是**逃出 scope** 的資源之家——在單一 scope 內開啟並關閉的 handle 應改用 `defer`
（見語言參考）。

## 邊界上的所有權與生命週期

讓 **scope-owned** 保持完好的規則：compiler 的自動釋放**只作用於 Zerg 配置的儲存**。外部儲存活在記憶體模型之外；
Zerg 永不隱式釋放它，也永不替 C 保留一個 Zerg buffer。字元與元素 buffer 遵循 Zerg「跨界一律 copy」的精神——
**copy 進 Zerg、borrow 出給 C**：

- **`str`／`list[T]` 傳*進* C**（一個引數）——C 拿到一個**借用的唯讀視圖**，只在該次呼叫期間有效。C 不得釋放它、
  不得寫入、也不得在回傳後保留該指標。Zerg 保有所有權，並照常在 scope 結束時釋放。
- **`str`／`list[T]` 從 C _出來_**（一個回傳，或一個回傳 `struct` 的欄位）——那些位元組在邊界**被 copy 進一個
  全新的 Zerg-owned 值**，所以 C 的 buffer 只需在回傳當下有效，而 Zerg**只**釋放自己的副本、永不釋放 C 的。一個
  入境 `str` 只在 C 保證**有效 UTF-8 且無內嵌 NUL**（`str` 的不變式）時才被接受；否則以 `list[byte]` 取得。
- **C 回傳的一般 scalar 值**——一個純 scalar 的 `struct`、一個 `int`、一個 `bool`：Zerg 現在完全擁有的一份副本，
  由普通 scope 規則釋放。
- **C 配置、且 _Zerg 之後必須釋放_ 的 buffer 或資源**——**不**以 `str`／`list` 回來（那會在 Zerg 複製後洩漏 C 的
  原件），而是以一個 **opaque handle** 回來，配一個明確、由 wrapper 以 `Ref[T]` 的 `drop` 呼叫的**外部 free**
  （見 opaque handle）。外部記憶體永遠不會被隱式釋放——一次都不會。

## 匯出一個 package（Zerg → C）

**沒有 export 關鍵字**。一個 package 的 C ABI 恰好就是它的 **package-public surface**——在 **root module** 上
被 re-export 的那些 `pub` 宣告（見 [package.zh-TW.md](package.zh-TW.md)「可見性」）。任何這樣的 `pub` 宣告都是
候選的 C 進入點；邊界不需要多加任何標記來宣告這件事。

一次普通建置由 entry 的 `main` 產生一個 program。一次 **library 建置**——是一個 compile option，而非原始碼改動
——則在同一趟中輸出：

1. 一個 C library，以穩定符號曝露 root public surface 的 **FFI-safe 子集**，以及
2. 一份對應的 **`.h` header**——含 include guard，收納 opaque `typedef`、`struct`／`enum` layout，以及函式原型。

一個 `pub` **method** 也會匯出：它降級成一個 C 函式，其**第一個參數是 receiver**——by-value 的 `this` 變成那個
struct by value、`mut &this` 變成指向它的指標（就地）——所以建議的 handle-wrapper method 以普通函式的形式抵達 C。
一個**非** FFI-safe 的 `pub` root 宣告會被**回報並排除**於 header 之外，而非靜默丟棄：一個 package 大可對 Zerg
依賴者提供比對 C 更豐富的 API，而該診斷讓 C ABI 誠實地反映真正跨界的東西。一個不回傳值的 Zerg 函式對映到 C `void`。

**符號名穩定且不 mangle。** C 的符號空間是扁平的，穩定 ABI 又不容 mangle，所以匯出名是決定性且不衝突的——概念上
是把 package 名前綴到宣告名上（method 再帶上它的型別，例如 `zg_<pkg>_<name>`／`zg_<pkg>_<Type>_<method>`）。
扁平匯出面上的名稱衝突在 library 模式下是編譯錯誤。（確切方案，以及是否提供逐宣告的 link-name 覆寫，是待決問題
——見下。）

## 匯入 C——一個 stdlib 設施

grammar 裡**沒有 `extern` 關鍵字、也沒有匯入區塊**。綁定一個外部 C 符號——把 `sqlite3_open` 命名出來讓 Zerg
可呼叫——是一個 **stdlib 設施**：stdlib 把一個 linker 符號**原封不動**（不 mangle、名字照字面採用）解析成一個
**`unsafe fn`** 型別的可呼叫值，其簽章由你提供，並與任何邊界宣告一樣被檢查為 FFI-safe。

**外部呼叫是 `unsafe`。** 呼叫這樣一個綁定，**只在 `unsafe` 情境內**合法。目前的 unsafe 模型有三種形狀：函式
本體內的一個 **`unsafe { … }` block-expression**（它產出區塊的值，如上面的 `open`）；一個獨立的 **`unsafe
fn`**，其整個本體都是 unsafe、且只能從 unsafe 呼叫；以及一個**module 層級的 `unsafe { … }`**，它把宣告**分組**
進一個 unsafe 情境（裡面的 `fn` 是一個 unsafe fn，一個 `mut` binding 是一個可變 global）。**沒有 `unsafe mut`
前綴**。在其中任何一種裡，compiler 都不對這次外部呼叫作安全保證——你寫的那層薄 wrapper 就是你擔保之處。把
raw 綁定與它們的 wrapper 分在一組：

```text
unsafe {
    fn raw_open(path: str, mut &db: handle?) -> int { sqlite3_open(path, db) }
}
```

這個綁定是**生的**：它精確映照 C 契約，不把 C 的任何錯誤慣例帶進 Zerg。一個以 `errno`、回傳碼或 `NULL` 結果
表示失敗的 C 函式，會把那些原封回給 Zerg；把它們映射進 null-safety 模型的，是**手寫的薄 wrapper**——在 `unsafe`
內執行那次呼叫——`res.ok_or(err)`、一個提早的 `return nil`，或一個建構出的 `Either`。沒有任何自動化，而這正好
讓映射保持明確、可稽核。

## 錯誤跨越邊界

**abort 永不跨界。** 一個 abort（`OverflowError`、`DivideByZeroError`、`UnwrapError`，任何*被 raise* 的錯誤）
是一次會執行 scope cleanup 的 Zerg stack unwind（見語言參考）；一個 C frame 沒有這種 cleanup，Zerg 也不擁有 C
的 unwind 路徑。所以當一個**被匯出**的 `pub` 函式（或一個被 C 呼叫的 Zerg callback）abort 時，unwind 會**在邊界
trap**——它**結束整個 process**，而不是撕穿 C 呼叫者的 frame（當 C 是呼叫者時，沒有 Zerg `main` stack 可停）。
要把一個失敗交給 C 呼叫者讀取，就**用 `guard` 在邊界把 abort 降級成值**，讓匯出函式回傳一個 FFI-safe 結果，而非
unwind：

```text
pub fn parse_port(s: str) -> int? {
    return guard { parse_int(s) }.ok()                 # 內部溢位變成 nil，而非 trap
}
```

反方向，一個 **`Either`／`T?` 值**在其 payload 為 FFI-safe 時，以普通 tagged union 跨界（或者，對 pointer-shaped
的 `T?`，以一個 nullable pointer）——記得 `Result[T]` 不是，所以匯出一個具體錯誤型別或 `T?`。C 呼叫者讀
**discriminant** 來分辨兩側。預期的失敗是兩邊都能檢視的資料；一個 bug 則是永不離開 Zerg 的 abort。（那個
discriminant 的具體 C layout 是待決細節——見「待決問題」。）

## 並行跨越邊界

Zerg 的並行——M:N scheduler 上的 `spawn`，以及 `chan`——是 **runtime 內部的，不跨界**。`chan` 與 coroutine
handle 並非 FFI-safe，不得出現在外部綁定的簽章或匯出面上；結果與完成仍只在 Zerg **內部**經由 channel 傳遞。

- **一次阻塞的外部呼叫會阻塞它的 OS thread。** scheduler 把眾多 coroutine 多工到少數 OS thread 上；一個
  阻塞的 C 呼叫（syscall、sleep）會停住整條底層 thread，共用它的 coroutine 因此不前進。優先用非阻塞的 C API，
  或把一次長阻塞呼叫視為佔用 thread。runtime 如何調整或擴張該 thread pool 是實作細節（TBD）。
- **一個 `Ref[handle]` 跨越 coroutine** 分享的是同一份外部資源，其 thread-safety Zerg 無法擔保——它看不到
  C 端狀態。用一般方式序列化：把 handle 交給**一個 owner coroutine**、對它送訊息（actor pattern，見
  [Coroutines 與 Channels](coroutine.zh-TW.md)），除非那個 C library 本身就是 thread-safe。
- **callback（C → Zerg）**只允許以一個**不捕獲、FFI-safe 的頂層 `fn`**、當作純 C function pointer 交出去。
  這樣的 callback 跑在 **C 呼叫它的那條 thread 上**——不是 Zerg 排程的 coroutine——所以它不得假設有 scheduler
  情境，而它內部的 abort 會**在邊界 trap**，與匯出函式完全相同。因為它不能 capture、Zerg 又沒有 `void*`，它也
  還無法像 C 的 `void* userdata` callback 那樣接收一個 Zerg context——一個待決問題，見下。

## 與語言其餘部分的一致性

FFI 不對既有模型新增例外——它多半是從中推導出來的：

- **coherence 與 orphan rule** 毫髮無傷：spec 與 existential 並非 FFI-safe，所以 `(type, spec)` 實作根本不會
  出現在 C 邊界。FFI 交易的是具體資料與函式，不是抽象。
- **`main`** 不受影響——library 建置沒有 `main`；它的 `Result[nil]` 退出模型關乎 program，而非匯出的 library。
- **prelude 與 std** _就是_ 匯入機制：`handle` token 與符號綁定設施都是 **stdlib**，依附在語言層級的 `unsafe`
  邊界與 `Ref[T]` 之上——並非新的關鍵字。std 也可提供便利輔助（例如一個 `str` ⇄ C-string 橋接），全都遵循上述
  同一套規則。
- **測試**把外部綁定與匯出程式碼當作普通程式碼、依常規可見性規則處理；一個黑箱測試可以連結產生出的 header，
  完全如同一個 C 依賴者。

## 待決問題

留給後續設計回合——皆不阻擋上述模型：

- **C 寬度整數別名**（`c_int`、`c_uint`、`c_size`、`c_long`……），讓外部綁定能命名 C 的平台寬度整數，而不只是
  Zerg 的固定寬度。
- C 呼叫者讀作 `.tag` 的 **tagged union 具體 C layout**（discriminant 型別、variant 值、成員命名、對齊）。
- 讓 C 函式就地填入的 `list[byte]` 的 **write-back 協定**（一個可變 out-buffer）——常見的 `read`／`recv` 形狀，
  目前尚無法表達。
- **callback context**——一個被外部呼叫的 callback 在沒有 capture、也沒有 `void*` 的情況下如何觸及 Zerg 狀態
  （例如以一個 opaque handle 當 context），以及它能否 `spawn`。
- 確切的**穩定符號方案**，以及是否提供逐宣告的 **link-name 覆寫**。
- library 模式遇到非 FFI-safe 的 public 宣告：**skip-with-diagnostic**（目前傾向）對上硬性錯誤。
- scheduler 對**阻塞外部呼叫**的策略（thread pool 擴張）——一個 runtime 細節。
- 匯入設施未來是否會綁定 **`"C"` 以外的 ABI**；目前只定義 `"C"`。
- 一個編譯期 **`sizeof` / `alignof`**——把型別的大小與對齊當成常數,既然佈局已固定(見
  [值與記憶體](memory.zh-TW.md))——是一個 **stdlib** 設施、延後到有具體需求;它不是核心語言構造。
