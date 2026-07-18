# Zerg Derive 與預設行為

型別想拿到實作、又不必逐一手寫每個 method 的**兩種**途徑，以及它們之間那條不可跨越的界線。本文延續
**[Spec 與 Generics](specs.zh-TW.md)**。另有 [English](derive.md) 版本。

## 兩種「免費」行為的來源

一個型別要拿到自己沒手寫的實作，其實有**兩種**截然不同的方式，而 Zerg 把它們嚴格分開：

| 來源                        | 由誰撰寫       | 能讀 fields？ | 依據的輸入         | 使用者可自定？ |
| --------------------------- | -------------- | ------------- | ------------------ | -------------- |
| **behavioral default body** | **spec** 作者  | **不能**      | `this` 及其 method | **可以**       |
| **concrete impl**           | **型別**擁有者 | 能            | 型別自己的 fields  | 可（手寫）     |
| **structural derive**       | **compiler**   | 能（特權）    | 型別的結構         | **不可**       |

concrete impl 就是手寫的基準線——一般的 module-local 程式，本來就能讀自己的 fields。另外兩層才是「免費」層，
本文接下來要談的，就是這兩者之間的界線。

## 不變式：spec 對 fields 是盲的

一個 `spec`——無論是 **required** 簽章或 **provided** 的 default body——只能透過 **`this`** 及其介面
（與其他 spec）暴露的 method 看見一個值，**永遠不讀 field**。

讀取一個值的**結構**——它的 fields、它的 variants——是 **compiler 的特權**，不是語言層的能力。任何以
`spec` 形式寫成的東西都做不到。這同時守住三件事：

- **抽象與表示脫鉤**——`T: X` 這個 bound 只暴露行為，從不暴露 layout；
- **spec 程式沒有結構性 `match`**——不必為了提供某個 method 而拆解 struct 或 switch variant，spec 因此
  保持精簡而通用；
- **結構只有一處被讀**——就是 compiler——所以「誰能看見 fields？」有唯一、可稽核的答案。

## Behavioral default——spec 自帶，且使用者可自定

provided method 是**寫在 `this` 上其他 method 之上的 default body**，從不寫在 fields 之上（也就是上述那條
不變式）。它讓一個 spec 只靠很小的 required 核心，就導出許多 method；實作者可以**繼承**、也可以**覆寫**
其中一個。每個使用者 spec 都能帶這種 default——這就是**可擴充**的那一層。

```text
spec Summable {
    fn zero() -> This                       # required
    fn add(other: This) -> This             # required

    fn sum(items: list[This]) -> This {     # provided——只讀 method，不讀 field，也沒有 match
        mut acc := This.zero()
        for x in items { acc = acc.add(x) }
        return acc
    }
}
```

實作 `zero` 與 `add`，`sum` 就免費得到。這是**從行為導出行為**、完全不碰結構，所以自然遵守 field-blind，也
完全可由使用者自定——這就是「我要怎麼定義自己可重用的 default？」的答案。

## Structural derive——compiler 的特權，且封閉

`derive X for T` 就是請 **compiler** 靠**讀取 T 的結構**，產生 `(T, X)` 的 canonical 實作——product
**逐欄位**、sum **逐 variant**，並遞迴進入每個欄位自己的 `X`。它**不是**「空 impl 繼承 default body」的
語法糖：spec 沒有結構化 default（它 field-blind），所以 `derive` 是一個**以受祝福的 spec 為鍵的
compiler code generator**，與上面兩層都不同。

**為何使用者沒辦法自定新的 structural derive。** 從結構產生 impl，需要一段**會讀結構**的程式。這段程式
只可能是：

- 一個 **spec / default body**——被 field-blind 禁止，或
- 一個 **macro**——Zerg 刻意沒有，或
- **compiler**——那就不是使用者撰寫的。

所以使用者自定 structural derive 是**結構上不可能**，不是漏做。可 derive 的集合是**固定且 compiler
擁有**的；使用者 spec 永遠不在其中（`derive UserSpec for T` 是編譯錯誤）。可擴充的一層是上面的
behavioral default；結構這一層是封閉的。

## 可 derive 的 spec 清單

這組受祝福的 spec——每個都有一份 compiler 擁有的 canonical 結構解讀。`Object` 一律 derive；其餘經由
`derive` **opt-in**：

| Spec     | 結構規則                                                | 要求（每個欄位） | 排除                           |
| -------- | ------------------------------------------------------- | ---------------- | ------------------------------ |
| `Object` | `equal`/`copy`/`debug`，逐欄位（可覆寫）                | —                | —                              |
| `Ord`    | 依宣告順序 lexicographic（先 variant 順序、再 payload） | `Ord`            | 任何 `float` 欄位              |
| `Hash`   | 合併各欄位 / tag 的 hash，維持 `equal ⇒ 同 hash`        | `Hash`           | 任何 `float` 欄位              |
| `Encode` | product 逐欄位、sum 先 tag 再 payload                   | `Encode`         | `chan` / `Ref` / `fn` / handle |
| `Decode` | 逐欄位 / 由 tag + payload 重建                          | `Decode`         | `chan` / `Ref` / `fn` / handle |

不符要求的欄位會讓 derive 變成**點名該欄位的編譯錯誤**，絕不靜默略過——`derive Ord for T` 若含 `float`
欄位會被拒，正如 [Spec 與 Generics](specs.zh-TW.md) 裡手寫規則所要求（`float` 無 total order；請手寫並以
canonical `±0.0`、把 `NaN` 放在一端來處理）。

以下橫切情形都從既有記憶體模型自然導出，無需新規則：

- **遞迴 / 自我指涉**（auto-boxed）型別可正常 derive——生成的 impl 會像對待任何欄位一樣，遞迴穿過那層
  透明的 box。
- **`Ref` 值**（`chan`、`Ref[T]`）遵循 `Object.copy`（refcount-bump），並在 `equal` 下以 identity 比較；
  它不是 `Encode`/`Decode`，所以持有它的型別無法 derive 那兩者。
- **新增一個 `enum` variant** 會自動重新 derive——作者沒有 `match` 要更新，因為走訪結構的是 compiler，
  不是使用者程式。

## `derive` 語意與 coherence

- `derive X for T` 產生**唯一的** `(T, X)` canonical 實作——與手寫 impl 填的是同一個槽，只是用生成取代
  撰寫。
- 要特化，就**改成手寫 `impl X for T { … }`** 而非 derive；兩者不可並存（重複 impl 是錯誤），符合
  **每個 `(type, spec)` 唯一 canonical 實作**。
- **orphan rule 不變**：`derive` 撰寫於手寫 impl 合法的所在——擁有 `T` 的 package 或擁有 `X` 的 package。
- `derive` 後只能接**受祝福**的 spec；接使用者 spec 是編譯錯誤。

## Serialization——完整範例

serialization 正是 structural derive 存在的目的：一種機械式、逐欄位的對映，沒人該為每個型別手寫，但它
既不需要 reflection、也不需要 macro。

```text
# stdlib spec——behavioral 介面，與每個 spec 一樣 field-blind
spec Encode {
    fn encode(mut out: Sink)
}
spec Decode {
    fn decode(mut src: Source) -> Result[This]     # This = 重建出的值
}

struct User {
    id:    int
    name:  str
    tags:  list[str]
    email: str?
}

derive Encode, Decode for User        # compiler 讀 User 的結構，寫出兩份 canonical impl
```

compiler 生成的內容——概念示意，你永遠不會寫、也看不到——就是顯而易見的逐欄位走訪，每個欄位委派給
**它自己的** `Encode`：

```text
impl Encode for User {                            # 生成，非手寫
    fn encode(mut out: Sink) {
        out.begin(4)
        out.field("id")
        this.id.encode(out)
        out.field("name")
        this.name.encode(out)
        out.field("tags")
        this.tags.encode(out)     # list[str]：長度，再逐個 str
        out.field("email")
        this.email.encode(out)    # str?：是否存在，再值
        out.end()
    }
}
```

sum 型別依 variant derive——**先 tag、再 payload**：

```text
enum Shape {
    Circle(float)
    Rect(float, float)
}

derive Encode for Shape               # 生成：寫出 variant tag，再逐個 payload 欄位
```

當線上格式必須不同，就**手寫 impl 而非 derive**——仍是唯一 canonical 實作，仍然沒有 macro：

```text
impl Encode for User {                            # 取代 derive 出的那份
    fn encode(mut out: Sink) {
        out.field("uid")
        this.id.encode(out)       # 自訂 key
        out.field("name")
        this.name.encode(out)
        # tags 與 email 刻意不放進線上格式
    }
}
```

`Decode` 回傳 `Result[This]`，所以格式錯誤只是一般的 value-tier 失敗——happy path 免 `guard`、出錯以 `?`
傳播——絕非 abort。（`Result[T]` 非 FFI-safe，但這裡無妨：`Encode`/`Decode` 是純 Zerg spec，永遠不跨
C 邊界——見 [FFI](ffi.zh-TW.md) 參考。）
