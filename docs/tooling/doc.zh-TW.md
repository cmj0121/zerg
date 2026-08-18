# Zerg Documentation Tool

`zerg doc`——一個 module 對外露出什麼，以及記錄它們的那些註解，在終端機上讀。屬於[語言參考](../language.zh-TW.md)的
一部分。亦有 [English](doc.md) 版本。

```sh
zerg doc                      # 每一個讀得到的 module
zerg doc strings              # 那個 module 的整份文件
zerg doc --brief strings      # 它露出的表面，一個宣告一行
zerg doc strings.split        # 單一宣告；方法寫成 log.Logger.level
zerg doc src/stdlib/log.zg    # 一個檔案、或一個目錄，就地成為文件
```

## 主張

**原始碼是這份文件的唯一副本。** 沒有第二份要同步的文件，沒有被抄進頁面裡的註解，也沒有任何東西是寫作者要離開程式碼
另外維護的。`zerg doc` 印出來的東西，每次被問到時都是從原始碼讀出來的。

這是底下每一個決定的理由，也是這個工具被要求做到的事：

> **這個工具漏掉的宣告，會讓函式庫看起來比實際更完整。** 讀者沒有辦法分辨「這個 module 就只有這些」與「這個 module
> 的抽取已經對不上了」。

`make doc-check` 就是這句話變成的 gate。它用 `sed` 把 `pub` 宣告從原始碼裡讀出來——第二意見，而且刻意粗糙——再按名字
跟文件裡的名字**雙向**比對，標準函式庫的每一個 module 都比。

## 四個問題

它們是照這個順序被讀的，而這個順序就是消歧的全部：

| 你指名的               | 回答的是                                 |
| ---------------------- | ---------------------------------------- |
| 什麼都不指             | 每一個讀得到的 module，各帶它的第一句話  |
| 一條真的存在的**路徑** | 那個檔案、或那個目錄的檔案，就地成為文件 |
| 一個 **module**        | 那個 module 的整份文件                   |
| `module.name`          | 單一宣告——方法寫成 `log.Logger.level`    |
| 其他任何東西           | 一句拒收，並列出它看得見的東西，離開碼 1 |

路徑**只有真的在那裡才算**：帶 `/` 或以 `.zg` 結尾、卻在磁碟上什麼都不指的名字是個錯誤而不是路徑，它會掉進其他所有
不明名字得到的同一句拒收。接著才試整個 module，因為 `strings` 是最常見的問題，而且它不是任何東西的宣告。最後才把名
字切開，而且是**切在第一個 `.` 而不是最後一個**——切最後一個會留下 `log.Logger`，它什麼都解析不到，於是一個本來有答
案的問題變成拒收。

**這裡什麼都不建置。** 讀一個 module 不是 import 它，所以 `import` 要通過的那些檢查在這裡不會被問：一個有出貨卻編不
起來的 module，仍然是一個讀得到的 module。

`--brief` 縮短的是**列表**——每個宣告變成它的簽名加上註解的第一句，欄位、variant 與方法都不列。它跟 module 索引走的
是同一趟、同一段程式碼，所以它漏掉的宣告會從讀者看到的第一頁上消失。單一宣告不讀這個旗標：只有一項的列表就是那項東
西本身，所以 `zerg doc --brief strings.split` 印的是整條。

## 露出什麼，就記錄什麼

每一種 `pub` 形式都在文件裡，其他的都不在：

| 形式                  | 連帶顯示                                 |
| --------------------- | ---------------------------------------- |
| `pub fn`              | 它的簽名，含從 `impl` 區塊攤平出來的方法 |
| `pub struct`          | 它的**公開**欄位                         |
| `pub enum`            | 它的 variant                             |
| `pub const`           | 它的值，當那個值是字面值時               |
| `pub type`            | 它代表什麼                               |
| `pub spec`            | 它要求的方法                             |
| module 層的 `pub mut` | 照它寫的樣子，歸在常數那一段             |

module 層的 `pub mut` 綁定照它寫的樣子顯示——`mut COUNTER := 0`——而它必須待在裡面的那個 `unsafe { }` 群組**不會**
印在它外面，儘管 `unsafe fn` 會留住它的關鍵字。`unsafe` 群組是 module 的性質，不是那一行綁定的性質。

私有宣告不是文件——`main` 不在任何文件裡，沒有 `pub` 的其他東西也一樣。一個六個欄位只顯示兩個的 struct，描述的是一個
編不過的字面值，所以私有欄位被略去的 struct 會說 `(private fields not shown)`。

**沒有註解的公開宣告照樣顯示，並標上 `(undocumented)`。** 它永遠不會被省略。這個標記的判準是**完全沒有註解**，此外
無他：一則只由一個可執行範例構成的註解就是註解，把那個叫做沒有文件，是跟沉默同一種謊，只是方向相反。

**簽名由編譯器自己的型別印表機寫出來**，不是第二個。文件裡的簽名跟診斷裡的簽名不可能對這個語言有不同說法，因為寫出
它們的是同一個函式。

## 哪一則註解記錄哪一個宣告

註解是從 **lexer 的 token 串流**讀來的，不是掃描原始碼找 `#`。這就是為什麼字串字面值裡的 `#` 在這裡不是註解，也是為
什麼 `#[derive(Eq)]` 是 decorator 而不是備註。

一則註解附著在什麼上，是由行的幾何決定的，而下面這些是寫作者必須知道的規則：

| 原始碼裡                                       | 文件裡                           |
| ---------------------------------------------- | -------------------------------- |
| 緊貼在宣告上方、一整片的整行註解               | 記錄那個宣告                     |
| 那片註解與宣告之間有**空行**                   | **什麼都不記錄**                 |
| 那片註解與宣告之間有 `#[…]` decorator          | 仍然記錄它——decorator 不構成中斷 |
| 以 `# --- 橫幅 ---` 開頭的一片註解             | **什麼都不記錄**，而且整片都不算 |
| 寫在一行程式碼尾巴的註解                       | **什麼都不記錄**                 |
| 整則內容就是一個可執行範例的註解               | 記錄它                           |
| 檔案裡第一片、上方沒有程式碼、沒被任何宣告認領 | 就是 **module 檔頭**             |

空行不需要自己的規則——它不發出任何 token，於是那片註解就只是比上一行更早結束。**橫幅是對檔案下的記號**：它把原始碼
分成給讀原始碼的人看的章節，而一個章節叫什麼，並不是它底下第一個宣告叫什麼。整片都不算，不是只有那排破折號，因為橫
幅底下的散文是寫給那一組的，把它交給單一宣告等於把一段從來不是在講它的文字掛到它身上。

**module 檔頭**是沒有人認領的那一片：檔案裡第一片、上方沒有程式碼，而且在每一個宣告——包含私有的——都拿走自己的之後
仍然沒被認領。`import` 什麼都不認領，這就是把緊貼著 `import "ascii"` 的檔頭，跟記錄第一個宣告的註解分開來的東西。

**以 module 命名的那個檔案，就是在索引裡替這個 module 說話的檔案**。沒有這種檔案的目錄 module 就沒有 module 層的描
述，索引會什麼都不說，而不是說點什麼。

以下這個檔案一次示範上面每一條規則：

```zerg
# tally — counting things, and the comments that document them.
#
# This first run belongs to the FILE: no code stands above it and no
# declaration below it claims it.

# LIMIT is the largest tally this module will count to.
pub const LIMIT: int = 64

# --- the counter ----------------------------------------------------------
# A run that opens with a banner documents nothing, and the whole run goes.

pub struct Counter {
    pub n: int
}

# this comment is cut off from the declaration by a blank line

pub fn reset(c: Counter) -> Counter {
    return Counter(0)
}

# Bumped is a counter already raised, and the decorator does not detach this.
#[derive(Eq)]
pub struct Bumped {
    pub n: int
}

# hashy is documented by this comment, and the `#` in the string below is not one.
pub fn hashy() -> str {
    return "# not a comment"
}

fn main() {
    print LIMIT
}
```

而 `zerg doc tally.zg` 為它印出來的是：

```text
tally — counting things, and the comments that document them.

This first run belongs to the FILE: no code stands above it and no declaration
below it claims it.

CONSTANTS

  const LIMIT: int = 64
      LIMIT is the largest tally this module will count to.

TYPES

  struct Counter
      (undocumented)

      n: int

  struct Bumped
      Bumped is a counter already raised, and the decorator does not detach
      this.

      n: int

FUNCTIONS

  fn reset(c: Counter) -> Counter
      (undocumented)

  fn hashy() -> str
      hashy is documented by this comment, and the `#` in the string below is
      not one.
```

`main` 不在裡面，`Counter` 失去了它的橫幅，`reset` 失去了那則被空行隔開的註解，`Bumped` 跨過 decorator 留住了它
的註解，而字串裡的 `#` 從來沒被當成註解讀。

最後一條規則有真實的案例，不必靠 fixture。`json.null` 的整則註解就是一個可執行範例與它的輸出，所以
`zerg doc json.null` 會印出那個範例，而且**不會**標記它。在 `--brief` 之下它印出簽名就停住：一則裡面沒有句子的
註解沒有摘要可寫，而一個意思是「有文件，但不是一句話」的空行，是沒有讀者看得出來的區別。

## 一份文件的形狀

四個縮排，而且每一層巢狀都是同一對往內四欄：

| 欄位 | 放什麼                                          |
| ---- | ----------------------------------------------- |
| 0    | 檔案自己的檔頭，以及區段標題                    |
| 2    | 一個宣告的簽名                                  |
| 6    | 它的註解；以及它底下的欄位、variant、要求或方法 |
| 10   | 那個成員自己的註解                              |

一個檔案的段落是它的檔頭，然後 **CONSTANTS**、**TYPES**、**FUNCTIONS**——任何一段裡沒有東西時就整段不出現。一個型別
的方法印在**那個型別**底下，是在型別被宣告的那一段，而不是方法被寫在哪一段，因為 `impl T { … }` 早在這個工具看到它
之前就被攤平成帶接收者的函式了。

散文**填到 80 欄**，這個數字是寫死的，不是去問裝置的。圍欄區塊與縮排行**原樣帶過、永不重新折行**：那裡面的文字是要
被複製出去執行的。比預算還長的一個詞會自己佔一行、永遠不會被切斷，因為長的東西是網址、路徑或一段行內程式碼——從中間
斷開之後看起來沒問題、實際上不能用的那種文字。

填充是以 **rune** 計量的，所以一個 em-dash 算一欄而不是三欄。已知的近似是另一個方向：全形 CJK 字佔兩個終端機欄位，
卻被算成一欄，所以用中文寫的註解會超出邊界。這裡沒有為它帶一份 `wcwidth` 表。

**目錄 module 是一個檔案一個檔案地成為文件。** 一個檔案一段，段首是讀者可以照打回去的路徑，各自帶著那個檔案自己的檔
頭與自己的宣告。反過來把每個檔頭串起來，會讓 `zerg doc src/compiler/zerg` 在第一個宣告之前先開出四百行堆疊的散文
——那些檔頭每一份都是一個檔案在替自己辯護，從頭讀到尾它們是各說各話。只有一個檔案的 module，也就是標準函式庫裡的每
一個，剛好就是一段，上面沒有段首。

一個既沒有自己的檔頭、也沒有任何公開宣告的檔案會說 `(nothing exposed)`。一個底下空無一物的段首，讀起來像是排版壞
掉，而不是像一個沒有表面的檔案——這跟 `(undocumented)` 替宣告擋掉的是同一種失敗，只是高了一層。

## 顏色跟著終端機走，形狀永遠不跟

`NO_COLOR` 的**存在**先決定——任何值都算，包含空值——之後才是 stdout 是不是終端機。這是 [`log`](../runtime/stdlib.zh-TW.md)
已經畫好的那條線，這個命令照著走。

帶顏色的只有四段，不多不少，而且全都是眼睛用來跳的、不是用來讀的：區段標題、每個簽名的關鍵字與名字、索引裡的名字，
以及 `(undocumented)` 標記。註解自己的文字永遠不上色，**圍欄也不上色**——範例是要被複製出去的，而一個被貼進 shell 的
跳脫碼是這份文件自己製造的故障。只用最早的那八個 SGR 顏色。

**形狀永遠不隨裝置改變。** 管道、檔案與終端機收到的是同樣順序的同樣字元；終端機多收到的是圍在那四段外面的 SGR 碼，
沒有別的。`zerg doc strings > page` 與 `zerg doc strings | less` 對每個字元的位置看法一致，而這正是讓輸出成為 gate
可以拿去比對的東西——`make doc-check` 比的就是這個：它驅動一個 pty、把顏色再剝掉，然後跟走管道的那次逐位元組 diff。

## 這個編譯器剖析不了的 module

`src/stdlib/atomic.zg` 宣告了一個泛型 struct，而這個編譯器拒收它（`E9004`）——而且那個拒收是 **parser** 丟的，所以它
的宣告根本列不出來。讀者拿到的是誠實的那份文件：

```text
atomic — the safe shared-mutable primitive (Phase 1f, bundle MVP).

  … 這個檔案檔頭的其餘部分，完整地，然後 …

note: `atomic.zg` does not parse under this compiler; its declarations are not
listed (see docs/runtime/stdlib.md)
```

檔頭活下來了，因為它是從 token 串流出來的，不需要樹。那行 `note:` 是讓「缺席的章節」不至於讀起來像「空的章節」——一
個宣告都沒有、也什麼都不說的 module，是在把自己記錄成沒有表面。它的離開碼是 0：沒有東西失敗，而一個沒有人能 import
的 module，它的文件就是它僅有的文件。

**任何**剖析不過的檔案都會印出同一行 note，並指名那個檔案；指向標準函式庫章節的那半句，只有在檔案真的在標準函式庫
裡時才會加上去。

## 還沒做的東西

寫在這裡而不是留給人去發現，因為一個把自己說得比實際多的文件工具，正是它唯一無法補救的失敗：

| 還沒做                                  | issue                                            |
| --------------------------------------- | ------------------------------------------------ |
| `--check`——建置文件範例並 diff 它的輸出 | [#17](https://github.com/cmj0121/zerg/issues/17) |
| `##`——把讀者的文件與維護者的筆記分開    | [#18](https://github.com/cmj0121/zerg/issues/18) |
| 不指名 module 就找到一個宣告            | [#19](https://github.com/cmj0121/zerg/issues/19) |
| 靜態 HTML 頁面                          | [#20](https://github.com/cmj0121/zerg/issues/20) |
| `--serve`                               | [#21](https://github.com/cmj0121/zerg/issues/21) |

`--check` 是這個第一版所出自的那個 issue 的後半，刻意留給它自己的一次 commit。在 `--check` 落地之前，那些範例仍然由
[`scripts/doc-examples-check.sh`](../../scripts/doc-examples-check.sh) 在跑：它會把標準函式庫註解裡的每一個
`zerg` 圍欄編譯起來、執行，再把跑出來的東西跟寫在旁邊的 `output` 圍欄 diff。它掛在 gate 板的 `stdlib-test` 底下，而
`zerg doc` 沒有任何東西跟它重疊。

`##` 是**刻意**在等的。這個 codebase 的註解很長，而且大半是寫給維護者的，把讀者的那一半跟維護者的那一半分開來沒辦法
自動化——讀這個工具真正的輸出，才是學會那條線落在哪裡的方式，所以先設計那個標記等於在猜。

HTML 會是**同一份抽取的第二種呈現**，絕不會是第二個抽取器。這正是為什麼「一個 module 裝了什麼」與「它怎麼排版」現在
已經是兩段各自獨立的程式碼，誰都不擁有誰。`--serve` 卡在更大的東西上：這個語言完全沒有網路。
