# Zerg Decorator

**decorator** 是掛在 statement 前的 `#[…]` 前綴——一道給 compiler 的指令。這個集合是**固定、compiler 擁有**的:使用者
不能自訂（Zerg **沒有 macro**），所以本頁以外的任何東西都無法改寫你的程式。因為集合封閉,**未知或拼錯的 decorator
是編譯錯誤**——絕不會被默默忽略。每個 decorator 綁定其後的 statement。屬於
[語言參考](../language.zh-TW.md) 的一部分。亦有 [English](decorators.md) 版本。

## 形狀

無論指名什麼,每個 decorator 都受這三條規則約束。

- **它領一個 statement**,而宣告也是 statement——所以 `struct` 上的 `#[derive(Eq)]` 與一個綁定上的
  `#[allow(L103)]` 是同一種形式（`statement`、`decorated-decl`,[`GRAMMAR`](../../GRAMMAR) group 1）。**哪一個**
  decorator 能用在哪裡是**語意**規則,而且很短:`#[derive]`、`#[obj]` 與 `#[test]` 講的是**宣告**,放在一般 statement
  上會被指名拒絕——_E612 `#[derive(Eq)]` applies to the `struct`, `enum` or `spec` that follows it, and a
  statement is not one_。`#[allow(…)]` 才是屬於 statement 的那一個。
- **一個項目一個 decorator。** 要掛好幾個就寫**逗號列表**——`#[allow(L601), test]`——把一個疊在另一個上是編譯錯誤:
  _E613 a second decorator on one item — an item takes ONE decorator, so merge them into its comma list_。
  一件事兩種寫法正是 `zerg fmt` 存在的理由,而兩種都合法之後它就無從移除。
- **它自成一行。** decorator 和其他項目一樣是 statement list 的一個項目,所以有分隔符把它和它所領的項目分開;
  `#[derive(Eq)] struct P` 寫在同一行不是一種形式。

## 集合

`#[derive]`、`#[obj]`、`#[test]`、`#[fixture]` 與 `#[allow]` 是這個編譯器會讀的 decorator。其他每一個——
`#[sealed]`、layout 指示詞——都是 **[not yet]**,並且會被指名拒絕（見下方〈保留字，以及一個保留名實際上得到什麼〉）。

- **`#[derive(Spec, …)]`** — 掛在 `struct` / `enum`。依型別的**結構**生成每個所列 blessed spec 的 canonical impl。
  受祝福集合是 **`Eq`**——已建置,會在一個 `struct` 與一個無欄位 `enum` 上生成正確的 `==` / `!=`——以及 **`Ord`**、
  **`Hash`**、**`Encode`**、**`Decode`**,各自已規範、但 **[not yet]**:指名其中一個是一次乾淨的拒絕,
  _E436 NotImplemented: `#[derive(Ord)]` — this compiler derives `Eq`; `Ord`, `Hash`, `Encode` and `Decode`
  are specified and unbuilt_。掛在**帶 payload 的** `enum` 上的 `Eq` 是 **[not yet]**,而且有自己的代碼,
  _E438 … it carries a payload (`A`), and this compiler derives equality for a fieldless enum_。
  **沒有自動 derive 的 `Object`**。使用者 spec 不可在 **struct** 上被 derive——`E437`——而在 **`enum`** 上則任何
  spec 都可以,因為生成的 impl 是委派給 payload、而不是讀取結構。見 **[Derive & Default Behavior](derive.zh-TW.md)**。
- **`#[obj]`** — 掛在 `spec`,不帶參數。生成一個由 function value 組成的**伴生 struct** 與一個**泛型 wrap**,這是
  在「spec 是 bound、從來不是型別」的語言裡寫出異質集合的方式。`mut fn`、收 `This` 的方法,以及任何不是 spec 的東西,
  都會被指名拒絕。見 **[Specs & Generics](specs.zh-TW.md)**。
- **`#[test]`** — 掛在 `fn`。把該函式標記為測試案例,由 `zerg test` 執行,別的東西都不會。它**不回傳東西**,
  參數可以是一個 **`testing.Context`**（以型別辨識）、它需要的 **fixture**（以名字比對）,或是完全不帶參數;其中的
  斷言失敗或 abort 即令測試失敗。宣告了回傳型別會被 `zerg test` **拒絕**,並指出位置:driver 是把測試當成一個
  statement 呼叫的,那個值會被丟掉,而把它當成判定結果的讀者必須被告知它不是。它可以寫在**任何地方**,`zerg test`
  會在它被寫下的地方找到它;寫在 `*_test.zg` **之外**是合法的,而且會**被打包出去**,所以 `zerg lint` 會對它發出
  警告（**L601**,見[檢查器](../tooling/lint.zh-TW.md)）——linter 說測試該住在哪裡,runner 執行寫下來的東西。
  測試住在哪裡見 [模組、套件與程式](../runtime/package.zh-TW.md)。
- **`#[fixture]`** — 掛在 `fn`,而它該住在 `*_test.zg` 裡。把該函式標記為 `zerg test` 會**為指名它的測試建置**的
  東西。它把自己的測試當作 **continuation** 收下:一個型別為 `fn (T)` 的參數,以型別辨識,它同時是那些測試執行的
  所在,也是這個 fixture **產出什麼**的宣告。其餘每個參數都**指名另一個 fixture**。它和 `#[test]` 一樣,
  **寫在哪裡就在哪裡被讀到**,同一條 **L601** 也適用。見 [模組、套件與程式](../runtime/package.zh-TW.md)。
- **`#[allow(Lxxx, …)]`** — 掛在任何 **statement**,宣告也在內。壓下該 statement 上所列的 **lint** finding;若該
  statement 帶一個區塊,區塊也在涵蓋範圍內:範圍就是它所領的 statement 的大小,這是**一條**規則,而不是在「一行」與
  「一個 scope」之間二選一。它不會延伸到下一個 statement,也到不了另一個檔案——刻意**沒有檔案層級的範圍**。

  它只收 **`L` 代碼**。`E` 代碼是**編譯器診斷**,`#[allow]` 絕不壓下任何一個:一個程式若能把編譯器的檢查關掉,
  等於把繞過檢查變成官方功能。lint finding 是建議性質的,所以壓下它是正當的。

  它是編譯器**會讀、但從不使用**的那一個 decorator。parser 接受這個名字並且不賦予它任何意義——代碼目錄屬於 linter,
  在編譯器裡放一份副本就是同一個語言事實的第二個落點。因此關於「壓制」本身,由 linter 說兩件事:**L106**（**info**）
  代表它沒有東西可壓,**L107**（**warning**）代表它指名了沒有規則對應的代碼。完全沒點名代碼的 `#[allow]` 則直接被
  拒絕——_E614_。

## 保留字，以及一個保留名實際上得到什麼

有四個名稱**已規範但未建置**，而其中只有一個是編譯器認得的名字：

- **`#[sealed]`** — 掛在 `struct`。*原意*是把預設的 field-wise `T(…)` constructor 降為**模組私有**，外部必須改走
  公開的自訂 constructor（具名關聯 `fn`），而模組自身仍以 `T(…)` 建——搭配私有、帶 default 的 field 以強制不變量。
  **[not yet]**，而且有自己的代碼：`E496`。
- **`#[repr]`** / **`#[packed]`** / **`#[align]`** — 記憶體 **layout** decorator，用於對接外部 ABI 時控制記憶體
  寬度、padding 與對齊（見〈保持稀少〉與 [值與記憶體](memory.zh-TW.md)）。**[not yet]**

> **[not yet]** layout 那三個是**保留在本頁上、而不保留在編譯器裡**。`#[repr]` 沒有自己的規則：它落進未知
> decorator 的分支，拿到 _E217 … this compiler reads `#[derive(…)]`, `#[obj]`, `#[test]`, `#[fixture]` and
> `#[allow(…)]`, and no other_——與拼錯的 `#[frobnicate]` 同一句話。沒有任何東西被默默丟掉；失去的是「等待實作」
> 與「打錯字」之間的區分，而那正是 `#[sealed]` 的 `E496` 買回來的東西。
>
> **[deviation]** `#[test]` 兩個編譯器都會讀，但**種子在它的檢查器跑之前就把 `#[test]` 函式剝掉**，所以那裡從來
> 不曾對函式本體做型別檢查，而 `zerg` 與其他函式一視同仁。一個編不過的測試在 `zerg` 底下是編譯錯誤、在 `zerg0`
> 底下是沉默——記錄在 `src/bootstrap/README.md`。

這個集合只在編譯器新增指令時成長；**logging** / 觀測與 **FFI** 是可能的下一批。任何**未**列在本頁的名稱根本不是
保留的 decorator——它是編譯錯誤，所以拼錯絕不會被當成某個編譯器默默丟棄的指令。

## 不是 macro

decorator 只**選取**一個 **compiler 提供**的行為——它不在編譯期執行使用者程式碼,也不會展開成任意原始碼。這正是
集合封閉的原因:「沒有任何指令能默默改寫你的程式」這個保證,恰恰因為你無法自己新增一個而成立。

## 保持稀少

decorator 的定位是**極少動用**。有兩個日常需求**不是**它的職責:一個值的**序列化 / wire 格式**靠手寫它的
`Encode` / `Decode` **spec impl** 客製（`#[repr]` 只管記憶體寬度,絕不管 wire 上的 bytes）;而**記憶體佈局**遵循
一條可預測的預設（宣告順序、自然對齊）——只有要**偏離**去對接外部 ABI 時才加 layout decorator。走在預設路徑上、
日常之中,你一個 decorator 都不必寫。
