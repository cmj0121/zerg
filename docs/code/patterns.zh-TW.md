# Zerg 慣用法（Patterns & Idioms）

用 Zerg 既有的小核心,把日常的 pattern——closure、鏈式 pipeline、builder——寫得道地。屬於
[語言參考](../language.zh-TW.md) 的一部分。亦有 [English](patterns.md) 版本。

## Closure 與高階函式

Zerg 的匿名函式 `fn(…) -> R { … }` **就是** closure——first-class,以 copy 捕獲
（見 [函式與閉包](functions.zh-TW.md)）。捕獲已經實作:**immutable** 值——一個 scalar,或一個 non-POD 的
`str` / `list` / `map`——與 **`mut`** binding 都可以,後者拿到的是**寫下閉包那一刻**它持有的值。
刻意**沒有更短的 `|x|` lambda**:一點點冗長會把你**推向** Zerg 的 procedural-first
風格,而非深層 functional 鏈。讓 closure 好讀的三招,依偏好排序:

1. **把函式命名**——first-class 函式可具名傳遞,具名的 `fn` 可重用、可測試,呼叫端也乾淨。
2. **寫 `for` 迴圈**——往往最清楚,也最 procedural-first。
3. **貼合槽位的 inline `fn`**——一次性用途時,沒寫型別的參數從 closure 被檢查的函式型別取得型別,省略的
   回傳型別也是（[型別系統](../core/type-system.zh-TW.md)）。若根本沒有那樣一個位置——`f := fn (x) { … }`
   ——就無處可取:參數會是一個具名的錯誤,而沒寫 `-> type` 就是 nil,不是推論。

## 鏈式 pipeline

method call 可鏈（`a.f().g()`）,所以 `map` / `filter` / `fold` 可組合。**優先用具名函式**,少用 inline
closure:

```text
fn double(x: int)   -> int  { return x *% 2 }
fn positive(x: int) -> bool { return x > 0 }

result := xs.map(double).filter(positive).fold(0, add)
```

> **[not yet]** 這些 adapter 本身不存在。`xs.map(double)` 報 _NotImplemented: the list method `map` — this
> compiler has `len` and `append`_，`filter` 與 `fold` 的回答一樣，所以上面那條鏈沒有東西可鏈。method call 確實
> 可鏈，而一個 `list` 有的就是那則訊息裡點名的兩個；在 adapter 落地之前，下面的迴圈就不只是 procedural-first
> 的替代方案，而是唯一的寫法。

或者,procedural-first,直接迴圈——這是 [控制流](control-flow.zh-TW.md) 推薦的慣用法（append 進另一個
collection）:

```text
mut out: list[int] = []
for x in xs {
    continue if not positive(x)
    out.append(double(x))
}
```

型別寫在左邊,因為空 list 自己沒有型別——`mut out := []` 是 _E336 the binding `out` gives the empty list `[]`,
which has no type of its own_。這件事在這裡比平常更要緊:adapter 尚未建置時,這個迴圈是唯一的寫法,那它最好是一個
建得起來的寫法。

若 inline 函式真的只用一次,由 position 供給的參數型別能讓它短:

```text
ys := xs.map(fn(x) -> int { return x *% 2 })   # x: int 取自 xs;-> int 寫出來
```

> **[not yet]** 這一行還沒建的是 `map`,標在上面。沒有型別的參數**不是**:`x` 會從 closure 被檢查的函式型別
> 取得型別,所以等 adapter 存在的那天,這一行可以直接寫成 `xs.map(fn(x) { return x *% 2 })`。

## Builder

**named argument 與 default parameter 就是 Zerg 的 builder。** 多數 `Builder().x().y().build()` 的儀式,只是
為了讓輸入可選——而 [函式與閉包](functions.zh-TW.md) 一次呼叫就給你:

```text
fn connect(host: str, port: int = 443, tls: bool = true, timeout: int = 30) -> Conn { … }

c := connect("example.com")                          # 全用預設
c := connect("example.com", port: 8080, tls: false)  # 只具名覆寫想改的
```

純資料就用具名 field 的**呼叫式建構**:

```text
cfg := Config(host: "example.com", port: 8080)
```

> **[not yet]** 上面兩個呼叫都是具名引數的形式，而具名引數沒做（見 [函式與閉包](functions.zh-TW.md)）：
> `connect("example.com", port: 8080, tls: false)` 與 `Config(host: "example.com", port: 8080)` 一樣報
> _NotImplemented: the named argument `port:` — this compiler binds arguments by position only_。今天 struct
> 是 positional 建構的——`Config("example.com", 8080)`——而有預設的參數只能從呼叫的尾端省略，所以本節拿來取代
> 流式儀式的那個「一次呼叫的 builder」，正是它目前沒有東西可跑的部分。

若真需要**分階段 / 流式**的 builder（如 query builder）,**copy-by-value 讓 fluent-immutable 天然成立**——每步
讀 `this`、改一份 copy、回傳,鏈式全程不共享可變狀態:

```text
q := new_query().where("age > 18").order("name").limit(10)

# fn where(clause: str) -> Query {
#     mut q := this                # receiver 的一份 local copy
#     q.filters.append(clause)
#     return q
# }
```

若要就地改 builder,則在 block 或 `with` 內,對 `mut` binding 用 `mut fn` 方法。

## 解構與 pattern 支援

解構可直接在 `:=` 綁定:一個 tuple `(a, b) := e` 與一個 struct `P{x, y} := e` 都一步解開——這是消費
多重回傳或小型 record 的日常方式;兩者皆為 **[not yet]**,`match`(見 [控制流](control-flow.zh-TW.md))中的
**struct**、**tuple** 與 **`as`** pattern 也是——tuple 用靜態索引（`.0` / `.1`）讀回、struct 用欄位讀回。
`match` 真正會解構的是 **variant**、**萬用 `_`**、**range** 與**負數 literal** pattern,連同它們的**巢狀**。
`GRAMMAR` 允許但 **[not yet]** 的另有兩種:一個 **or-pattern**(`A | B =>`,綁不綁都算)與一個
**list pattern**(`[h, ..t]`)。兩者都在 code generation 被拒絕:list pattern 在型別檢查之後,or-pattern 則是因為那裡的
`|` 被讀成位元運算子,而一個靜默比對到錯的值的 arm,比一個編不過的 arm 更糟。`zerg fmt` 對「連續整數」那個情況能做
什麼,見 [控制流](control-flow.zh-TW.md)。

> **[not yet]** 上面那份清單裡不存在的是**巢狀**；四種 pattern 各自單獨用都成立。variant pattern 的 payload
> 位置只收一個 binding 名字或 `_`——那裡從來沒讀過子 pattern——所以 `L(Yes(v))` 與 `L(0)` 都會被具名拒絕，而且
> 帶位置：_E492 NotImplemented: a sub-pattern inside a variant payload, beginning at `…`_。（在此之前它是一則
> 沒有錯誤碼、也沒有位置的裸 parser 訊息，指名的只是站在那個位置上的 token。）該位置上的**保留字**是另一條規則、
> 保有它自己的碼：`L(this)` 是 `E245`。一次比對一層，把 payload 綁起來，再對那個 binding 做一次 `match`。

## 刻意不加的

兩個別的語言常用、但為了 small 與 procedural-first 而略去的便利:

- **UFCS**（`x.f(y)` ≡ `f(x, y)`）能讓自由函式像 method 一樣鏈式,但多一條 name-resolution 規則。
- **pipe 運算子** `|>` 對 pipeline 好讀,但是新運算子且偏 functional。

先用具名函式與 `for` 迴圈;它們不需新語法就能覆蓋這些情境。
