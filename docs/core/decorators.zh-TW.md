# Zerg Decorator

**decorator** 是掛在宣告前的 `#[…]` 前綴——一道給 compiler 的指令。這個集合是**固定、compiler 擁有**的:使用者
不能自訂（Zerg **沒有 macro**），所以本頁以外的任何東西都無法改寫你的程式。因為集合封閉,**未知或拼錯的 decorator
是編譯錯誤**——絕不會被默默忽略。每個 decorator 綁定其後的宣告。屬於
[語言參考](../language.zh-TW.md) 的一部分。亦有 [English](decorators.md) 版本。

> **[not yet]** `#[sealed]` 有自己的碼——_E496 NotImplemented: the decorator `#[sealed]` — it is a reserved
> decorator … and this compiler does not build it, so the constructor stays public rather than being sealed
> in silence_。這個字是對的,缺的是行為,而那正是寫下它的讀者需要被告知的事。

## 集合

`#[derive]`、`#[obj]` 與 `#[test]` 是這個編譯器會讀的 decorator。其他每一個——`#[sealed]`、
layout 指示詞——都是 **[not yet]**,並且會被指名拒絕。

- **`#[derive(Spec, …)]`** — 掛在 `struct` / `enum`。依型別的**結構**生成每個所列 blessed spec 的 canonical impl。
  受祝福集合是 **`Eq`**——已建置,會在一個 `struct` 與一個無欄位 `enum` 上生成正確的 `==` / `!=`（掛在**帶 payload**
  的 `enum` 上則是 **[not yet]**）——以及 **`Ord`**、**`Hash`**、**`Encode`**、**`Decode`**,各自已規範、但
  **[not yet]**:指名其中一個是一次乾淨的拒絕,_NotImplemented: `#[derive(Ord)]` — this compiler derives `Eq`;
  `Ord`, `Hash`, `Encode` and `Decode` are specified and unbuilt_。**沒有自動 derive 的 `Object`**。使用者 spec 不可
  被 derive（`#[derive(MySpec)]` 為編譯錯誤）。見 **[Derive & Default Behavior](derive.zh-TW.md)**。
- **`#[test]`** — 掛在 `fn`。把該函式標記為測試案例,**只在測試建置**中編譯與執行,一般建置則排除。函式不帶參數;
  其中的斷言失敗或 abort 即令測試失敗（測試放在何處見 [模組、套件與程式](../runtime/package.zh-TW.md)）。

## 已識別但尚未支援

另有四個 decorator 名稱被 compiler **識別**,但這個階段會**大聲拒絕**——用了就是「尚未支援」的**編譯錯誤**,絕不
默默當作 no-op:

- **`#[sealed]`** — 掛在 `struct`。*原意*是把預設的 field-wise `T(…)` constructor 降為**模組私有**,外部必須改走
  公開的自訂 constructor（具名關聯 `fn`）,而模組自身仍以 `T(…)` 建——搭配私有、帶 default 的 field 以強制不變量。
  **[not yet]**
- **`#[repr]`** / **`#[packed]`** / **`#[align]`** — 記憶體 **layout** decorator。保留以對接外部 ABI 時控制記憶體寬度、
  padding 與對齊（見〈保持稀少〉與 [值與記憶體](memory.zh-TW.md)）。**[not yet]**

> **[not yet]** `#[repr]` 仍是一個沒有自己規則的保留名字:它落進未知 decorator 的分支,拿到
> _E217 … this compiler reads `#[derive(…)]`, `#[obj]` and `#[test]`, and no other_——與拼錯的
> `#[frobnicate]` 同一句話。它被拒絕,所以沒有任何東西被默默丟掉;失去的是「等待實作」與「打錯字」之間的
> 區分——`#[sealed]` 原本也有同樣的問題,現在有了 `E496`。
>
> `#[test]` 現在**兩個編譯器都會讀**,而 `zerg test` 會把它標記的東西跑起來(那個指令走到哪裡,見
> [模組、套件與程式](../runtime/package.zh-TW.md))。裡面還留著一個 **[deviation]**:種子在它的檢查器跑之前
> 就把 `#[test]` 函式剝掉,所以那裡從來不曾對函式本體做型別檢查,而 `zerg` 與其他函式一視同仁。一個編不過的
> 測試在 `zerg` 底下是編譯錯誤,在 `zerg0` 底下是沉默——記錄在 `src/bootstrap/README.md`。

## 不是 macro

decorator 只**選取**一個 **compiler 提供**的行為——它不在編譯期執行使用者程式碼,也不會展開成任意原始碼。這正是
集合封閉的原因:「沒有任何指令能默默改寫你的程式」這個保證,恰恰因為你無法自己新增一個而成立。

## 保持稀少

decorator 的定位是**極少動用**。有兩個日常需求**不是**它的職責:一個值的**序列化 / wire 格式**靠手寫它的
`Encode` / `Decode` **spec impl** 客製（`#[repr]` 只管記憶體寬度,絕不管 wire 上的 bytes）;而**記憶體佈局**遵循
一條可預測的預設（宣告順序、自然對齊）——只有要**偏離**去對接外部 ABI 時才加 layout decorator。走在預設路徑上、
日常之中,你一個 decorator 都不必寫。

## 保留

這個集合只在 compiler 新增指令時成長。layout decorator（`#[repr]`、`#[packed]`、`#[align]`）與 `#[sealed]`
已是**保留名稱**——會被識別並大聲拒絕（見上）直到實作為止——而 **logging** / 觀測與 **FFI** 是可能的下一批。任何
**未**列在本頁的名稱根本不是保留的 decorator:它是**編譯錯誤**,所以拼錯絕不會被當成某個 compiler 默默丟棄的指令。
