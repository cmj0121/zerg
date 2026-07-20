# Zerg 慣用法（Patterns & Idioms）

用 Zerg 既有的小核心,把日常的 pattern——closure、鏈式 pipeline、builder——寫得道地。屬於
[語言參考](language.zh-TW.md) 的一部分。亦有 [English](patterns.md) 版本。

## Closure 與高階函式

Zerg 的匿名函式 `fn(…) -> R { … }` **就是** closure——first-class,只以 copy capture immutable 值與 channel
（見 [函式與閉包](functions.zh-TW.md)）。刻意**沒有更短的 `|x|` lambda**:一點點冗長會把你**推向** Zerg 的
procedural-first 風格,而非深層 functional 鏈。讓 closure 好讀的三招,依偏好排序:

1. **把函式命名**——first-class 函式可具名傳遞,具名的 `fn` 可重用、可測試,呼叫端也乾淨。
2. **寫 `for` 迴圈**——往往最清楚,也最 procedural-first。
3. **inline `fn` + 型別推論**——一次性用途,且 use-site 已知函式型別時,參數與回傳型別可省。

## 鏈式 pipeline

method call 可鏈（`a.f().g()`）,所以 `map` / `filter` / `fold` 可組合。**優先用具名函式**,少用 inline
closure:

```text
fn double(x: int)   -> int  { return x *% 2 }
fn positive(x: int) -> bool { return x > 0 }

result := xs.map(double).filter(positive).fold(0, add)
```

或者,procedural-first,直接迴圈——這是 [控制流](control-flow.zh-TW.md) 推薦的慣用法（append 進另一個
collection）:

```text
mut out := []
for x in xs {
    continue if not positive(x)
    out.append(double(x))
}
```

若 inline 函式真的只用一次,型別推論能讓它短:

```text
ys := xs.map(fn(x) { return x *% 2 })      # 由 xs 推得 x: int、-> int
```

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

## 刻意不加的

兩個別的語言常用、但為了 small 與 procedural-first 而略去的便利:

- **UFCS**（`x.f(y)` ≡ `f(x, y)`）能讓自由函式像 method 一樣鏈式,但多一條 name-resolution 規則。
- **pipe 運算子** `|>` 對 pipeline 好讀,但是新運算子且偏 functional。

先用具名函式與 `for` 迴圈;它們不需新語法就能覆蓋這些情境。
