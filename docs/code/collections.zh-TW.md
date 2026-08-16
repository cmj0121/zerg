# Zerg Collection

Zerg 的內建容器——**`list`**、**`map`**、**`set`**，外加定長的 **`[T; N]`** 陣列——每個角色只一個 canonical
型別，不弄變體動物園。它們就是普通的 **scope-owned 值**，建立在 [語言參考](../language.zh-TW.md) 之上。也有
[English](collections.md) 版本。

| 型別        | 角色                 | 元素／key 需求       | iteration 順序 | 狀態          |
| ----------- | -------------------- | -------------------- | -------------- | ------------- |
| `list[T]`   | 一個**有序序列**     | 任意 `T`（無 bound） | 索引序         |               |
| `map[K, V]` | 一張**關聯**表       | `K: Eq + Hash`       | **插入**序     |               |
| `set[T]`    | 一個**唯一成員**集合 | `T: Eq + Hash`       | **插入**序     | **[not yet]** |
| `[T; N]`    | 一個**定長陣列**     | 任意 `T`（無 bound） | 索引序         | **[not yet]** |

上表的 `map` key 需求是預期的那一種；這個階段 key 僅限 **`int`** 或 **`str`**（見下方 [key](#keyeq-免費hash-顯式)）。
兩個 **[not yet]** 的列各自指名自己:`set[T]` 在型別或值任一位置都是 _E466 NotImplemented: the built-in `set`_,
而 `[T; N]` 是 _E233 NotImplemented: an array type `[T; N]` — this compiler has `list[T]`, whose length is not
part of its type_。

更豐富的形狀都是組合出來的，不是新的內建型別。`list[byte]` 是原始位元組序列（可索引、可含 NUL）；`str` 還是獨立的
immutable primitive（見下）。

## 是值，不是 reference

collection 是 **scope-owned 值**：**copy-by-value**（compiler 安全時 elide 或 move）、scope 一結束就釋放、**無
aliasing**——複製會讓持有者拿到**自己的**元素、並對它持有的任何 **reference-counted** 元素做 **retain**
（refcount-bump）：一個
`chan`、一個 `Ref[T]`、一個 `str`、或一個遞迴型別的裝箱 tail。這正是那條記憶體規則——值型別的部分被複製、
reference-counted 的部分被共享（見 [值與記憶體](../core/memory.zh-TW.md#複製語意-vs-參照語意)）。不會有「兩個名字共用一個
容器」這種事：要共享來讀就用不可變傳參，要共享來改就用 `mut &` param；經 channel 傳送的 collection 跟其他 payload
一樣，都是用複製的。

## 可變性——一個 per-binding 的 knob

可變性是普通的 **per-instance** 軸：**單一 knob** 同時解鎖「改內容」與「重指名字」——Rust `let mut` / Swift `var`
的模型，不是把「變數」和「元素」拆開。

- **`mut xs`**——可**改元素**（`xs[i] = v`）、**增長／縮短**（append、insert、remove），以及**重指**
  （`xs = other`）。改元素與增長都是 `mut this` method，就像 struct 的 mutator。
- **plain `xs`**——**完全凍結**：固定的是*內容*，但它的長度仍是 heap 上的 runtime 值。（你還是可以用 `:=`
  re-declare 這個名字——**新** binding、舊的被 `del`——絕對不是變更。）要一個在**編譯期**就固定、且 inline 排布的
  固定*大小*，改用 `[T; N]` 陣列（見下）。

所以同一種 `list` 型別，既是凍結序列（plain）也是可增長 vector（`mut`）；**只有 `mut` collection 能改它的元素**。

> **[not yet]** 上面點名的增長 method 裡只有 `append` 建置了：`insert` 與 `remove` 在 `list` 與 `map` 上都會被
> 指名拒絕（_E444 NotImplemented: the list method `insert` — this compiler has `len` and `append`_），所以一個
> collection 只能從尾端增長、完全不能縮短。

```text
xs := [1, 2, 3]            # 凍結：xs.append(4) 與 xs[0] = 9 都是錯誤
mut ys := [1, 2, 3]
ys.append(4)               # 增長 · ys[0] = 9  # 改 · ys = [2, 4]  # 重指
```

## key——`Eq` 免費、`Hash` 顯式

`list[T]` 接受**任意** `T`（只需每個值都有的結構操作）。`map` 的 key／`set` 的元素同時需要**相等性**與 **`Hash`**
——key 以 `==` 比較並雜湊。兩者都不是自動的：相等性經 **`derive(Eq)`**（或手寫 `impl Eq`）opt-in，而 **`Hash` 同樣
顯式**——型別經 `derive(Hash)` 或手寫取得——讓「什麼能當 key」是 opt-in、`safe by default` 的決定。作者要負起
compiler 無法檢查的契約：**equal ⇒ same hash**。因為 key 是用凍結快照 **copy-in** 進去的，所以就算是 `mut`
collection 也能拿來當 key。

> **狀態。** 預期的規則——**任何 `Eq + Hash` 型別**皆可當 key——是 **[not yet]**。這個階段 `map` 的 key 僅限
> **`int`** 或 **`str`**:其他一律是 _E431 NotImplemented: a map key of type … — a key needs `Hash`, and this
> compiler has one for `int` and for `str`_。`derive(Hash)` 與一般的 keyed 型別尚未建置。

## 存取——`[]` 斷言、`.get` 檢查

索引比照 `!` / `?` 的「強取 vs 檢查」：

- **`xs[i]` / `m[k]`**——元素**值**；碰到壞索引或缺 key 就 **abort**（`IndexError` / `KeyError`）。壞索引就是 **bug**，
  跟 overflow 一樣。
- **`xs.get(i)` / `m.get(k)`**——檢查路徑 → **`T?`** / **`V?`**，給那種你本來就預期可能不存在的情況用。
- **`x in xs` / `x in s` / `k in m`** → `bool`；在 `mut` collection 上 **`xs[i] = v`** 就地設定。list 是**掃描**、
  map 是**雜湊**——同一個問題、不同的代價——而被找的那個值會像進入任何其他
  [有型別的位置](../core/types.zh-TW.md#typed-positions)一樣去符合元素型別，所以 `72 in bytearray(…)` 是一個 byte。

```text
first := xs[0]                 # 空的話 abort
name  := m.get(id) ?? "anon"   # 檢查後給預設
```

> **[not yet]** 檢查路徑並不存在：`xs.get(i)` 與 `m.get(k)` 都是 `E444`，所以上面那行 `m.get(id) ?? "anon"`
> 編不過，而會 abort 的索引是進入容器的唯一途徑。於是「預期內的不存在」不是程式問得出口的問題，而是它必須在索引
> 之前先用 `k in m` 迴避掉的事。

## 切片——唯讀子區間

一個**子區間**——`xs.slice(a, b)`，即 `[a, b)` 的元素——是一個普通的**唯讀 `list[T]` 值**、不是 borrow：它絕不寫回
母體，所以**沒有 aliasing**、也不需要 borrow checker，並遵守與任何 collection 相同的 copy-by-value 模型。編譯器可
把那份 copy 實現成 **copy-on-write**——與母體共享底層 storage，直到任一方被變動才複製——所以帳面上是 value
semantics，而唯讀情況維持**零拷貝**；COW 是與 copy-elision、move 同列的不可觀察最佳化（見 Values & Memory），不新增
任何看得見的共享，只是讓 `copy` 更便宜。

於是 lexer 用索引掃描（`xs[i]` 為 O(1)）、用 `slice` 取唯讀窗格而零複製，只在保留一個 token 時才實體化成 `str`。

> **[not yet]** 尚未建置的是 **method** 那個拼法：`xs.slice(a, b)` 是 `E444`。**`x[a..b]`** slice-index 語法糖
> 已建置且正確——`xs[1..3]` 產出一個全新的兩元素 `list`、`xs[0..=2]` 產出三元素的，各自都是獨立的值——所以在
> method 落地前，子區間就用中括號那個形式寫。上面那個唯讀、copy-on-write 的設計是兩者共同的預期語意。

## 順序與相等性

`list` 依索引序走訪；`map`／`set` 依**插入序**——有決定性、不會有 hash 亂序的驚嚇。走訪時**以值讀取每個元素**（可
elide 成唯讀 by-ref）；要就地改就綁 `mut x`（一個 by-ref，要求 collection 是 `mut`）。預期的結構相等性是：`list`
**依序**比，`map`／`set` **與順序無關**（插入序決定 iteration，永遠不會決定相等）。

> **[not yet]** 容器相等性尚未建置：今天用 `==` / `!=` 比較兩個 `list` 或兩個 `map` 是
> _E445 NotImplemented: `==` on a list[int] — structural equality over a container is unbuilt, and a container
> has no declaration to derive it on_。只有 **`str ==`** 能比。`for mut x` 走訪一個
> collection 對**每一種**元素型別都是 **[not yet]**、包含 POD：不論 `ys` 裝什麼，`for mut x in ys` 都是 `E242`，
> 所以下面範例的第二行不是一個程式。

```text
for x in xs { total = total + x }         # 讀取
for mut x in ys { x = x * 2 }             # 就地改——[not yet]，對每一種元素型別都被拒絕
```

## 迭代與變動

在 `for … in xs` 內，`xs` 對**結構性變更凍結**——在迴圈裡 append、insert、remove、grow/shrink、或 rebind 它都是
**compile error**——所以 iterator **永遠不會失效**（無 dangling cursor、無 runtime fail-fast 檢查）。這是一條
**local** 規則——迴圈知道自己走訪的是哪個 collection——所以**不需要 borrow checker，runtime 也零成本**。就地改
某個 **元素**（`for mut x`）還是可以：它不會移動 cursor——不過那個綁定本身是 **[not yet]**（見
[順序與相等性](#順序與相等性)）。

> **[deviation]** 這道凍結只看得見**裸名**。`for x in xs { xs.append(x) }` 與 `for x in xs { xs = [9] }` 是
> compile error（`E393`），但同一個結構性改動只要透過**路徑**抵達就不是：`for x in p.xs { p.xs.append(v) }`、
> `for x in p.xs { p.xs = [9] }`、`for x in xs[0] { xs[0].append(v) }`，以及一個收 `mut &xs` 並在迴圈裡 append
> 的 function，今天全都編得過，而且真的把正在被走訪的 collection 長大或重指掉。沒有任何 iterator 失效——迴圈走的
> 是它在開頭取的那份 copy-on-write 複本，所以程式仍然是記憶體安全的——但**本節承諾的那個 compile error 沒有出
> 現**：迴圈只是安靜地走訪那個當初的 collection，而不是把話說出來。只有裸名那個拼法被強制。

想就地轉換的話，用一個內部走訪受控的 `mut` method（`xs.retain(pred)`），或是重建（`xs = xs.filter(pred)`——迴圈後
rebind）。想邊讀 `xs` 邊累積，就 append 到**另一個** collection。

> **[not yet]** 這兩個替代方案都不存在：`xs.retain(pred)` 與 `xs.filter(pred)` 都是 `E444`，所以一次轉換今天
> 得寫成一個 append 進第二個 `list` 的 `for`，再在迴圈之後 rebind。

## 定長陣列——`[T; N]`

**`[T; N]`** 是一個**定長陣列**——N 個 `T` 值 **inline** 排布（在 stack 上、或嵌在它所屬的值裡），**無 heap、無
`Ref`**。它的長度 **N 是型別的一部分**、在**編譯期**固定，所以 `[int; 3]` 與 `[int; 4]` 是**不同型別**、兩者間無
隱式轉換。這正是 `list` 做不到的一件事：`list[T]` 是 heap-backed、長度是 runtime 值，而陣列的大小靜態已知、儲存
inline——這也是為什麼對得上 C 的 `T[N]` 欄位（見 [FFI](../runtime/ffi.zh-TW.md)）、以及「layout 要緊時該拿」的是陣列而非
`list`。

N 是一個**編譯期常數**——整數 literal、top-level 或**型別 `const`**（見 [型別常數](../core/specs.zh-TW.md)），
或由它們經算術／位元運算子組合、被 compiler 摺疊（`[int; ROWS * COLS]`）。它絕不是 runtime 值、也絕不是**函式
呼叫**：Zerg 不做一般的編譯期求值，所以 `[int; f(x)]`
是錯誤。

```text
xs: [int; 4] = [1, 2, 3, 4]     # 一個 list literal，由目標型別定型為陣列——長度須為 4
buf := [0; 256]                 # 裸 := ——這是 256 個的 list[int]，不是陣列 [int; 256]
row := [b'\0'; WIDTH]           # WIDTH 是 top-level const——在裸 := 下也是 list
```

陣列是個普通的**值**：copy-by-value 複製全部 N 個元素（bump 所含的任何 `Ref`）、scope 結束釋放、絕不 alias——就是
容器的值模型。其餘一切都從 `list` 已述的規則掉出來：

- **建構**——list literal `[a, b, …]` 是 **context-typed**：預設 `list[T]`，當目標是 `[T; N]` 時才是陣列（長度在
  編譯期查）。**fill 形式 `[v; N]`** 意圖做出 **N 份 `v`**——用來建大集合而不必逐一列出；沒有隱式 zero-fill。在裸
  `:=` 下 fill 形式建的是一個 **`list[T]`**，不是陣列型別 `[T; N]`；陣列定型的 fill 形式（在明確的 `[T; N]` 位置）是
  **[not yet]**。

  這個 **count 就是陣列長度那個編譯期常數**——一個 literal、一個初始式摺得出來的名字（module-level 或 local），
  或它們之間的算術：`[0; 256]`、`[0; ROWS * COLS]` 與 `[b'\0'; WIDTH]` 是同一個形式。摺不出來的 count（runtime
  才讀到的值、函式呼叫）是**在 fill 這一行**報錯，而不是在它所指名的 binding 上，因為要編譯期值的是 fill 這一
  行；負數也一樣，因為 count 是「要複製幾份」。

  `v`只求值**一次**，再複製 N 份——這正是「N 份 v 的複本」的意思：元素運算式帶有副作用或可觀察成本的 fill 只會
  跑一次，不是 N 次。第一份之後的每一份都是真正的複本，所以 `str` 或 `list` 的 fill 得到 N 個各自獨立的元素，
  而不是 N 個共用同一份的槽。

- **存取**——`a[i]` by value、bounds-check → `IndexError`，而落在 `[0, N)` 之外的**常數** index 在**編譯期**就被
  抓出；`a.get(i) -> T?` 是 checked 路徑。`mut a` 可原地改元素（`a[i] = v`），但**永遠不能 grow/shrink**——大小在
  型別裡；plain `a` 則凍結。
- **長度**——`a.len()` 就是 N，本身是編譯期常數。
- **寫進簽章**——函式透過**值泛型**對長度泛化,`fn sum[N: int](xs: [int; N])`,`N` 由引數推出、呼叫端從不寫它。

  > **[not yet]** 值參數會被拒絕——_NotImplemented: a value generic parameter `N: int`_——所以今天的函式只吃
  > 一個具體長度（`[int; 4]`）,要處理任意長度就改收 `list[T]`。

- **迭代／derive／slice**——它實作 `Iterator`／`Iterable`（`for x in a`；**`for mut x in a` 是 [not yet]**，對每一
  種元素型別皆然、包含 POD），並**逐元素** derive：當元素型別 `T` 具備時，陣列才逐元素 derive `Eq`／`Ord`（以及建置後的
  `Hash`／`Encode`）——兩個同型別陣列於是逐元素比較（與雜湊）。**沒有**任何一律 auto-derive 的 `Object`；相等性只來自
  元素上的 `derive(Eq)`。`a.slice(p, q)` 意圖產出**唯讀 `list[T]`** view——從陣列橋回 list 家族的 COW 通道——但
  `slice` 這個 **method** 是 **[not yet]**（見 [切片](#切片唯讀子區間)）。

## 字串與位元組

`str` 是**獨立的 immutable primitive**、不是 collection——它以 `rune` 走訪、且**不可索引**。透過
**`bytearray(s)`**（原始位元組、可含 NUL）或 **`runearray(s)`**（code point）橋接。兩者指的是 list 而不是新型別:
**`bytearray` 就是 `list[byte]`**、**`runearray` 就是 `list[rune]`**——處處可與展開寫法互換,**不是** strong
typedef（`type X = Y`,見[型別](../core/types.zh-TW.md)）。這個集合**封閉**於這兩個。反方向:建字串的方式是先
收集進 `list`，再用
**`str(...)`** 轉，
它會**驗證** `str` 的 invariant（從 bytes 來的要是 valid UTF-8、無 embedded NUL）、違反就 **raise**——不信任的輸入就用
`guard { str(bytes) }` 降級成 `Result[str]`（沿用錯誤模型的 checked 路徑，不另設 constructor）。編輯文字永遠會產生
一個**新的** `str`。

`str` 實作 **`Ord`**、**`Hash`**、**`Add`**——收錄在 [Spec 與 Generics](../core/specs.zh-TW.md)（內建 spec）：依 code point
字典序排序、（因為不可變）是天然的 `map`/`set` key、且 `a + b` **串接**成新 `str`。在迴圈裡建字串就用前述
list-collect，別用重複的 `+`（那樣每一步都會複製整個累積字串）。`float` 既不實作 `Ord` 也不實作 `Hash`，所以永遠
不會是排序集合的元素，也永遠不會是 key。（把非文字值渲染成文字——`int` 變 `"42"`、`f"…"` 內插——是
**[Formatting & Text](../runtime/format.zh-TW.md)**，建立在 `display` 上。）

## 待決

上文每一個標了 **[not yet]** 的東西，都與它指名的那個特性一起待決。只有一種形狀是別處都沒有載明的:一個**有序
變體**——以 `Ord`（而非 `Hash`）為 key 的排序 `map`／`set`——如果需求確實成立的話。
