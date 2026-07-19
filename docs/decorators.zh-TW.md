# Zerg Decorator

**decorator** 是掛在宣告前的 `#[…]` 前綴——一道給 compiler 的指令。這個集合是**固定、compiler 擁有**的:使用者
不能自訂（Zerg **沒有 macro**），所以本頁以外的任何東西都無法改寫你的程式。每個 decorator 綁定其後的宣告。屬於
[語言參考](language.zh-TW.md) 的一部分。亦有 [English](decorators.md) 版本。

## 集合

- **`#[derive(Spec, …)]`** — 掛在 `struct` / `enum`。依型別的**結構**生成每個所列 blessed spec 的 canonical impl:
  `Object`（一律生成）加上可選的 `Ord`、`Hash`、`Encode`、`Decode`。使用者 spec 不可被 derive（`#[derive(MySpec)]`
  為編譯錯誤）。見 **[Derive & Default Behavior](derive.zh-TW.md)**。
- **`#[dyn]`** — 掛在泛型 `fn`。把泛型編成**一份共享的 witness-table body**,而非依型別引數各自 monomorphize——以
  zero-cost 換較小的碼,並讓 compiler 封頂實例化膨脹。見 **[Grammar](grammar.zh-TW.md)**（group 7）。
- **`#[sealed]`** — 掛在 `struct`。把預設的 field-wise `T(…)` constructor 降為**模組私有**,外部必須改走公開的
  自訂 constructor（具名關聯 `fn`）;模組自身仍以 `T(…)` 建。搭配私有、帶 default 的 field 以強制不變量。

## 不是 macro

decorator 只**選取**一個 **compiler 提供**的行為——它不在編譯期執行使用者程式碼,也不會展開成任意原始碼。這正是
集合封閉的原因:「沒有任何指令能默默改寫你的程式」這個保證,恰恰因為你無法自己新增一個而成立。

## 保持稀少

decorator 的定位是**極少動用**。有兩個日常需求**不是**它的職責:一個值的**序列化 / wire 格式**靠手寫它的
`Encode` / `Decode` **spec impl** 客製（`#[repr]` 只管記憶體寬度,絕不管 wire 上的 bytes）;而**記憶體佈局**遵循
一條可預測的預設（宣告順序、自然對齊）——只有要**偏離**去對接外部 ABI 時才加 layout decorator。走在預設路徑上、
日常之中,你一個 decorator 都不必寫。

## 保留

這個集合只在 compiler 新增指令時成長——記憶體 **layout** 控制（`#[repr]`、`#[packed]`、`#[align]`）、
**logging** / 觀測、**FFI** 是可能的下一批。在某個 decorator 列入本頁之前,它都不是合法語法。
