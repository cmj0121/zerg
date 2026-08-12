# Zerg Decorator

**decorator** 是掛在宣告前的 `#[…]` 前綴——一道給 compiler 的指令。這個集合是**固定、compiler 擁有**的:使用者
不能自訂（Zerg **沒有 macro**），所以本頁以外的任何東西都無法改寫你的程式。因為集合封閉,**未知或拼錯的 decorator
是編譯錯誤**——絕不會被默默忽略。每個 decorator 綁定其後的宣告。屬於
[語言參考](../language.zh-TW.md) 的一部分。亦有 [English](decorators.md) 版本。

> **[deviation]** 一個**不帶引數**的 `#[derive]` 或 `#[derive()]` 會被接受、然後默默丟掉,而那正是本頁說絕不會發生
> 的那件事。引數清單是以「對括號內的 spec 名字做迴圈」讀進來的,所以空清單就是零次迭代,既不生成也不拒絕任何東西;
> 而且因為「decorator 有沒有掛在對的宣告種類上」這道檢查是**逐個具名 spec** 做的,裸寫的形式連那道檢查也一併跳過
> ——`#[derive]` 掛在一個 `fn` 上會編過,而同一個 `fn` 上的 `#[derive(Eq)]` 則正確地被拒絕、報
> _`#[derive(Eq)]` has no declaration under it_。沒有任何東西被編錯,但一道指令被讀進來又被丟掉,而那正是封閉集合
> 本來要排除的事。

## 集合

`#[derive]` 與 `#[obj]` 是這個編譯器會讀的 decorator。其他每一個——`#[test]`、`#[sealed]`、
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

> **[deviation]** 編譯器並不區分一個**已識別**的 decorator 與一個**未知**的。除了 `#[derive]` 與 `#[obj]` 以外的
> 每一個 `#[…]` 都落進同一個分支,所以 `#[sealed]`、`#[repr]`、`#[test]`,以及拼錯的 `#[frobnicate]`,拿到的是
> 同一句話——_E217 NotImplemented: the decorator `#[X]` — this compiler reads `#[derive(…)]` and `#[obj]`, and
> no other_。它們每一個都被拒絕,
> 所以沒有任何東西被默默丟掉、也沒有任何東西被編錯;失去的是本節與下面〈保留〉賴以成立的那個區分。一個打錯的字會
> 被報成一個「保留、等待實作」的名字,而「未知的 decorator 是一個讀者分辨得出來的錯誤」這個承諾並沒有兌現。

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
