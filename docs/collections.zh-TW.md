# Zerg Collection

Zerg 的內建容器——**`list`**、**`map`**、**`set`**——每個角色只一個 canonical 型別，不弄變體動物園。它們是普通的
**scope-owned 值**，建立在 [語言參考](language.zh-TW.md) 之上。亦有 [English](collections.md) 版本。

| 型別        | 角色                 | 元素／key 需求       | iteration 順序 |
| ----------- | -------------------- | -------------------- | -------------- |
| `list[T]`   | 一個**有序序列**     | 任意 `T`（無 bound） | 索引序         |
| `map[K, V]` | 一張**關聯**表       | `K: Hash`            | **插入**序     |
| `set[T]`    | 一個**唯一成員**集合 | `T: Hash`            | **插入**序     |

更豐富的形狀是組合出來的，不是新內建型別。`list[byte]` 是原始位元組序列（可索引、可含 NUL）；`str` 仍是獨立的
immutable primitive（見下）。

## 是值，不是 reference

collection 是 **scope-owned 值**：**copy-by-value**（compiler 安全時 elide 或 move）、scope 結束即釋放、**無
aliasing**——複製會深拷貝元素、並對含有的 `Ref` 值（channel 或 `Ref[T]`）refcount-bump，就是既有記憶體規則。
不存在「兩個名字共用一個容器」：讀取共享用不可變傳參、修改共享用 `mut` param；經 channel 傳送的 collection 一如
任何 payload 是複製。

## 可變性——一個 per-binding 的 knob

可變性是普通的 **per-instance** 軸：**單一 knob** 同時解鎖「改內容」與「重指名字」——Rust `let mut` / Swift `var`
的模型，不是把「變數」和「元素」拆開。

- **`mut xs`**——可**改元素**（`xs[i] = v`）、**增長／縮短**（append、insert、remove），以及**重指**
  （`xs = other`）。改元素與增長都是 `mut this` method，一如 struct 的 mutator。
- **plain `xs`**——**完全凍結**，即 Zerg 的固定陣列。（你仍可用 `:=` re-declare 這個名字——**新** binding、舊的被
  `del`——絕非變更。）

因此一種 `list` 型別兼任固定陣列（plain）與可增長 vector（`mut`）；**只有 `mut` collection 能改它的元素**。

```text
xs := [1, 2, 3]            # 凍結：xs.append(4) 與 xs[0] = 9 都是錯誤
mut ys := [1, 2, 3]
ys.append(4)               # 增長 · ys[0] = 9  # 改 · ys = [2, 4]  # 重指
```

## key——`equal` 免費、`Hash` 顯式

`list[T]` 接受**任意** `T`（只需每個值都有的結構操作）。`map` 的 key／`set` 的元素需 **`Hash`**（key 以 `equal` 比較）。
兩半刻意不對稱：`Object` 會 **auto-derive `equal`**，但 **`Hash` 不 derive——型別須顯式實作**才能當 key，讓「什麼
能當 key」是 opt-in、`safe by default` 的決定。作者要負起 compiler 無法檢查的契約：**equal ⇒ same hash**。因為 key
是以凍結快照 **copy-in**，即使 `mut` collection 也能當 key。

## 存取——`[]` 斷言、`.get` 檢查

索引比照 `!` / `?` 的「強取 vs 檢查」：

- **`xs[i]` / `m[k]`**——元素**值**；遇壞索引或缺 key 即 **abort**（`IndexError` / `KeyError`）。壞索引是 **bug**，
  一如 overflow。
- **`xs.get(i)` / `m.get(k)`**——檢查路徑 → **`T?`** / **`V?`**，供預期的缺席。
- **`x in s` / `k in m`** → `bool`；在 `mut` collection 上 **`xs[i] = v`** 就地設定。

```text
first := xs[0]                 # 空的話 abort
name  := m.get(id) ?? "anon"   # 檢查後給預設
```

## 順序與相等性

`list` 依索引序走訪；`map`／`set` 依**插入序**——決定性、無 hash 亂序驚嚇。走訪時**以值讀取每個元素**（可 elide 成
唯讀 by-ref）；要就地改就綁 `mut x`（一個 by-ref，要求 collection 是 `mut`）。相等性是結構性的：`list` **依序**比，
`map`／`set` **與順序無關**（插入序決定 iteration，永不決定相等）。

```text
loop x in xs { total = total + x }        # 讀取
loop mut x in ys { x = x * 2 }            # 就地改——ys 必須是 mut
```

## 迭代與變動

在 `loop … in xs` 內，`xs` 對**結構性變更凍結**——在迴圈裡 append、insert、remove、grow/shrink、或 rebind 它都是
**compile error**——所以 iterator **永遠不會失效**（無 dangling cursor、無 runtime fail-fast 檢查）。這是一條
**local** 規則——迴圈知道自己走訪的是哪個 collection——因此**不需要 borrow checker、runtime 零成本**。就地改一個
**元素**（`loop mut x`）仍可以：它不移動 cursor。

要就地轉換，用一個內部走訪受控的 `mut` method（`xs.retain(pred)`），或重建（`xs = xs.filter(pred)`——迴圈後
rebind）。要邊讀 `xs` 邊累積，就 append 到**另一個** collection。

## 字串與位元組

`str` 是**獨立的 immutable primitive**、不是 collection——它以 `rune` 走訪、且**不可索引**。透過 **`list[byte]`**
（原始位元組、可含 NUL）或 **`list[rune]`**（code point）橋接：建字串＝收集進 `list` 再用 **`str(...)`** 轉，它會
**驗證** `str` 的 invariant（從 bytes 需 valid UTF-8、無 embedded NUL）、違反就 **raise**——不信任的輸入用
`guard { str(bytes) }` 降級成 `Result[str]`（沿用錯誤模型的 checked 路徑，不另設 constructor）。編輯文字永遠產生
一個**新的** `str`。

`str` 實作 **`Ord`**、**`Hash`**、**`Add`**——收錄在 [語言參考](language.zh-TW.md)（內建 spec）：依 code point
字典序排序、（不可變故）是天然的 `map`/`set` key、且 `a + b` **串接**成新 `str`。在迴圈裡建字串就用前述 list-collect，
別用重複的 `+`（那會每步複製整個累積字串）。`float` 既不實作 `Ord` 也不實作 `Hash`，故永不是排序
集合的元素、也永不是 key。（把非文字值格式化成文字——`int` 變 `"42"`、string interpolation——是另一件事，延後。）

## 待決

- **有序變體**——以 `Ord`（而非 `Hash`）為 key 的排序 `map`／`set`，若有需要。
