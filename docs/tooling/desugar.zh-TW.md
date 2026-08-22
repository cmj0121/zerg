# Zerg Desugar 規則

`zerg desugar` 套用的每一條規則,以及命名它的代碼。屬於[語言參考](../language.zh-TW.md)的一部分。亦有
[English](desugar.md) 版本。

`zerg desugar` 把 source 改寫成**它的 sugar 被定義成的 core 形式**。方向與 [`zerg fmt`](fmt.zh-TW.md) 相反:
formatter 只要語言提供更短的寫法就採用它——`F401` 把 `if c { return x }` 變成 `return x if c`,而 `D101` 把它變回去。

```sh
zerg desugar <path>...              # 就地改寫;印出它改過的檔案
zerg desugar --check <path>...      # 回報哪些還不是 core 形式,不動任何東西
zerg desugar --off D103 <path>...   # 放過某一條規則(可重複)
```

**一個路徑就是它底下那棵樹** —— 檔案是它自己,目錄是它底下的每一個 `.zg`,而以 `.` 開頭的名字與符號連結不會被進
入。這條規則只寫一次,在[格式化工具那一章](fmt.zh-TW.md#一個路徑就是它底下那棵樹),而且對每一個吃原始碼的指令都
一樣。

> **[deviation]** `--check` 回答的問題比它問的更寬。它拿檔案去比對 `zerg desugar` **會寫出來**的東西,而那個東西
> 是 canonical 格式化過的 core——所以一個完全沒有糖的檔案,只要空白不是 `zerg fmt` 會產出的樣子,照樣會失敗,而且
> 失敗時說的是 _still holds sugar (run `zerg desugar`)_。四格縮排的 `fn main() { x := 1; print x }` 就是整份重現。
> exit status 對「這個檔案會被改動」而言是對的,那句話對「為什麼」而言是錯的。

## 為什麼需要它

[`GRAMMAR`](../../GRAMMAR) 把好幾個 surface form **定義成**別的東西。`return x if c` 就是
`if c { return x }`。`for c { … }` 就是 `for { if not (c) { break } … }`。`for i in a..b { … }` 則是再加一個
counter。每一條定義都是「這兩個程式是同一個程式」的主張——而這整棵樹沒有任何東西在檢查它們。

沒被檢查也不是意外。編譯器對每個 surface form 是**直接** lowering 的:`c_return_if` 產出條件式 return,
`c_forrange` 產出計數迴圈。所以 sugar 被*定義成*的那個 core 形式,走的是**emitter 裡的另一條路徑**,兩條路徑不會
交會。一個被 `continue` 跳過的 step、一個被求值兩次的上界、一條路徑註冊了而另一條沒有的 teardown——每一個都會編譯
成功、執行成功,然後印出錯的答案。

`make desugar` 就是那個檢查:把 corpus 裡每個程式複製一份 desugar,兩份都建、兩份都跑,比對它們印什麼、以什麼狀態
結束。輸入就是 corpus,所以這道 gate 是在新增 case 時長大,而不是在有人想起要擴充它時。

**desugar 過的 source 是產物,不是 canonical source。** 對它跑 `zerg fmt` 會把每個 sugar 放回去,這是正確的而不是
衝突:canonical 的意思就是 sugared——這也正是它自成一個命令、而不是 `fmt` 上一個 `--desugar` 模式的原因。

## 規則

| 代碼   | 規則                                  | C 相同 |
| ------ | ------------------------------------- | ------ |
| `D101` | postfix guard 變回它所是的 `if` block | 是     |
| `D102` | while-`for` 變回它所是的無限 `for`    | 否     |
| `D103` | range-`for` 變回它所是的無限 `for`    | 否     |
| `D104` | `assert` 變回它所是的 guarded raise   | 是     |

**C 相同**是個真實的區別,而且 gate 有在量。`D101` 產出的程式,其 emit 出來的 C 與 sugar 版**逐位元組相同**:五種
postfix guard 有四種是在 **parser** 裡就 desugar 掉的,第五種(`c_return_if`)產出的也是同一個 `if` block。`D104`
逐位元組相同的理由再往下一層:`assert` 同樣是在 parser 裡 desugar 的,而這條規則寫出來的正是它建出來的那些敘述。
`D102` 與 `D103` 產出的是 `for (;;)`,而 sugar 產出的是 `while` 或計數 `for`——同一個程式,不是同一段文字。所以這個
工具主張的等價是**行為上的**,而更強的主張只對成立的檔案主張:`make desugar` 會問「這次是不是只有 `D101` 觸發」,
是的話才比對 C。

**編號不是執行順序。** `D104` 跑在**最前面**,因為它產出的是 `raise … if c`——那正是 `D101` 的 sugar。放到最後跑,
這個 pass 就會留下自己下一輪還要再改寫的輸出,而「答案取決於跑了幾次」正是 gate 的 fixpoint 那一半要抓的東西。
之後才依編號順序執行,而 `D101` 跑在 `D103` 前面是關鍵——見 `D103`。

### `D101`——postfix guard 變回它的 block

```text
fn clamp(n: int) -> int {        fn clamp(n: int) -> int {
    return 0 if n < 0                if n < 0 {
    return 9 if n > 9        →           return 0
                                     }
    return n                         if n > 9 {
}                                        return 9
                                     }
                                     return n
                                 }
```

它涵蓋 postfix `if` 能附著的每一種 **diverge**——`return x if c`、裸的 `return if c`、`break if c`、
`continue if c`、`raise e if c`——也就是 `GRAMMAR` 為它定義的那一整組。

這裡沒有歧義要解。`return if …` **一定**是 guard:Zerg 沒有 `A if X else B`,條件式**運算式**是帶強制 `else` 的
block 形式,而 parser 在來這裡找之前就先讀成 guard 了。

**只要 statement 裡任何地方有註解就 decline**,包含尾隨的那一個。guard 是一行、它的 block 是四行,寫在那一行尾端的
註記沒有誠實的去處——留在原地的話,它會變成 block *之後*那個 statement 的標題。

### `D102`——while-`for` 變回無限 `for`

```text
mut i := 0                       mut i := 0
for i < 4 {                      for {
    print i              →           if not (i < 4) {
    i = i + 1                            break
}                                    }
                                     print i
                                     i = i + 1
                                 }
```

條件會被**加上括號**,而不是去信任 `not` 的優先序。`not a == b` 不是 `not (a == b)`,而一條必須對任何人寫的任何條
件都正確的規則,不可能靠背下優先序表而正確——只能靠不依賴它。

`continue` 不需要任何修補:while 迴圈的 `continue` 會重測條件,而這一版也會,因為那個測試就是 body 的第一個
statement。

它辨別三種 head 的方式與 `GRAMMAR` 相同——`for` 後面緊接 `{` 是無限形式,`mut` 或 `identifier in` 是 iterate 形式,
其餘都是條件——所以 `for (v in r) { … }` 是 while,而那個括號正是讓 membership 測試不被讀成 iteration 的東西。

### `D103`——range-`for` 變回無限 `for`

```text
for i in 0..3 {                  mut zgd_i7c2 := 0
    print i              →       zgd_hi7c2 := 3
}                                for {
                                     if zgd_i7c2 >= zgd_hi7c2 {
                                         break
                                     }
                                     i := zgd_i7c2
                                     print i
                                     zgd_i7c2 = zgd_i7c2 + 1
                                 }
```

上界會被**提出來且只求值一次**,而且排在初始值**之後**——兩個界依它們被寫下的順序計算,那既是 `c_forrange` 計算
它們的順序,也是這個語言其他每一份 operand 清單如今的求值順序。每次迭代都重新求值的上界會是另一個意思的迴圈,而
`for i in f()..g()` 就是這兩件事一起現形的地方。

這些 binding 以它們來自的那個 `for` 的**行與欄**命名,所以同一個函式裡的兩個迴圈不可能撞名,而名字本身說明了它從哪
來 —— 而正是這個命名方式,讓「提到外層 scope 而不是包進自己的 block」是安全的。

> 這裡原本給的理由是「_裸的 `{ … }` statement 是這個編譯器拒絕的形式_」,而那不是真的:`zerg build`
> 接受它,`zerg desugar` 也會原樣印回來。這句話是在收口
> [#12](https://github.com/cmj0121/zerg/issues/12) —— 十個對該形式沒有 arm 的 statement walk ——
> 時發現的,而它正是同一個形狀往上一層:一個沒人看過的形式,被需要理由的人順手描述了。

**inclusive 形式不是 `i <= hi`。** `for i in 0..=MAX` 在最後一個值之後沒有值可以踏過去:那個 step 會溢位,而在本語
言的[檢查式算術](../core/types.zh-TW.md)下它會 **raise** 而不是回繞。emitter 的答案是一個在最後一個值時變 false 的
旗標,而不是踏過去,這條規則也一樣:

```text
for i in 1..=4 { … }     →     zgd_hi3c2 := 4
                               mut zgd_i3c2 := 1
                               mut zgd_done3c2 := zgd_i3c2 > zgd_hi3c2
                               for {
                                   if zgd_done3c2 {
                                       break
                                   }
                                   i := zgd_i3c2
                                   …
                                   if zgd_i3c2 == zgd_hi3c2 {
                                       zgd_done3c2 = true
                                   } else {
                                       zgd_i3c2 = zgd_i3c2 + 1
                                   }
                               }
```

**`continue` 是這條規則的全部難處。** 在 sugar 形式裡 step 屬於迴圈 header,`continue` 會跑到它;在 core 形式裡
step 是 body 的最後一個 statement,而跳過它的 `continue` 會讓 induction variable 停在原地——一個永不結束的迴圈,而
且是由一個唯一承諾「我什麼都沒改」的工具產生的。所以這個迴圈擁有的每個 `continue` 前面都會補上 step,而**巢狀**迴
圈裡的 `continue` 屬於那個迴圈,不動。

這就是 `D101` 先跑的原因:`continue if c` 必須先變成 `if c { continue }`,這條規則才能在 `continue` 的位置放兩個
statement。做不到的時候——因為 `D101` 被關掉而還帶著自己 guard 的 `continue`,或寫成 match arm body 的那種、放不下
兩個 statement 的位置——整個迴圈 decline,而不是讓規則自己發明一個地方放。

### `D104`——`assert` 變回它的 guarded raise

```text
assert count(xs) == 3    →    zga_l7c9 := count(xs)
                              if not (zga_l7c9 == 3) {
                                  raise AssertionError(f"a.zg:7  assert count(xs) == 3\n  count(xs) = {zga_l7c9}")
                              }
```

**訊息是定義的一部分。** 一條只改寫測試、卻把訊息丟掉的規則,等於只 desugar 了一半——而那正是行為 gate 看不到的
一半:成立的主張永遠不會產出訊息。`test-data/desugar/assert_claim.zg` 就是用文字把它釘住的案例。

運算元會**先綁**,這是整個形式賴以成立的規則:訊息會點名它們,若從條件再求值一次,`assert next(it) == 3` 就會讓
iterator 前進兩次。字面值運算元留在原地(`3 = 3` 什麼也沒說),`and` 拆成每個 conjunct 一條主張,而在 `or` / `??`
底下只綁第一個 conjunct——運算元絕不跨過短路運算子被提出來。

它寫進訊息的位置是檔案的**basename**,那是一個 source-to-source 轉換所能誠實說出的極限:這段文字在檔案被複製到
別處建置之後仍必須是同一個意思,basename 撐得住,路徑撐不住。

**拆成哪些 conjunct、運算子在哪、哪些是運算元**都不是在這裡決定的。這條規則直接呼叫 parser 對這串 token 做的
分析,因為那些是關於語言的事實,而第二套掃描器正是同一個形式的兩種寫法開始互相矛盾的起點。寫在這裡的只是另一半:
parser 建的是樹,而這裡建的是文字。

和 `D101` 一樣,敘述裡任何位置有註解就 decline——改寫會變成好幾條敘述,而註解會延伸到它落在的那一行結尾。

## 它 decline 什麼,以及為什麼

會猜的 formatter 就是會改變程式行為的工具。下列每一種都原樣保留:

| 形式                        | 為什麼                                                   |
| --------------------------- | -------------------------------------------------------- |
| `for x in xs`               | 需要型別——list、map、str、channel 是四種不同的 lowering  |
| `for x in f(0..n)`          | 同一種情況,只是 range 藏在 call 裡;那個 `..` 不屬於 head |
| `for mut x in …`            | 就地綁定元素,計數形式做不到                              |
| `for select { … }`          | 第四種 head,不是條件                                     |
| `for i in 0..`              | 沒有上界可數;編譯器會拒絕它,所以這裡留給編譯器拒絕       |
| 帶註解的 guard              | 一行變四行,沒地方安置寫在行尾的註記                      |
| `lo..=hi =>`(range arm)     | 還沒有人為它寫規則;它的 core 形式現在建得起來(見下)      |
| `with` / `if x := e` / `?`  | 需要型別,或需要這個編譯器還沒有的 core 形式              |
| `??` / `?.` / `!` / `print` | 同上                                                     |

一般的 `for x in xs` 值得再說一次,因為這件任務就是從它開始的。`c_forin` 是依被迭代者的**型別**分派的——range 是計
數迴圈、channel 是 receive、map 依插入順序走 key、str 變成它的 code point、list 走 runtime 的索引——而 token pass
沒有任何東西能告訴它是哪一種。改寫成索引迴圈對 list 是對的、對 map 是錯的,所以它 decline。

**range arm** 是另一種 decline。`GRAMMAR` 說 `200..300 =>` 是它寫成 `_ if _ in 200..300` 的那個 guard 的 sugar,
而那個 guard 曾經就是理由:`in` 用在 range 上尚未實作,於是 sugar 是唯一能動的寫法,而一道 desugar 只能在它的 core
形式存在時被檢查。**那個 core 形式現在建得起來**——`v if v in 200..300 => …` 編得過也比對得到——所以剩下的是
「還沒有人寫這條規則」,而不是「沒有東西檢查得了」。這個 arm 會原樣通過。

是 `v` 而不是 `_`,而這正是一條規則得處理對的地方:`GRAMMAR` 的 `_ if _ in …` 是「這個 arm 不做繫結」的記法,
第二個 `_` 並不是 guard 讀得到的名字(_E3069 undefined name `_`\_)。要寫出 core 形式，就得發明一個原始碼裡從來
沒有的 binder，而且它不能跟 arm body 已經用到的任何名字相撞。

## Gate

`make desugar` 跑兩道:

- **`desugar-check`**——`examples/`、`test-data/codegen/` 與 `test-data/desugar/` 裡的每個程式,以整個目錄為單位
  desugar(一個程式不一定只有一個檔案,而 `import` 是相對 source 自己的目錄解析的),兩種形式都建、都跑,比對
  **stdout 與 exit status**。它同時會對每個只有 `D101` 動過的檔案斷言 C 完全相同,並斷言每份 desugar 過的 source 都
  是自己的 fixpoint。它帶有下限,因為「這兩者一致」對空集合恆真。
- **`desugar-golden`**——每個 `test-data/desugar/<case>.zg` 必須 desugar 成旁邊那份 `<case>.core.zg`,逐位元組相
  同,好讓規則產出的改變以 diff 而不是以數字的形式出現。core 檔會再被 desugar 一次且不得改變。最後,每條規則都必須
  有一個 case 讓它**觸發**——這件事是用問的而不是用宣告的:把規則關掉,看輸出會不會變。

行為 gate 看不見一條安靜地什麼都不做的規則,golden gate 看不見一條產出讀起來正確、行為卻不同的規則。兩者都需要,而
那些 decline 需要 golden 那一道:錯誤地展開 `for x in xs`,在 list 上剛好會動。
