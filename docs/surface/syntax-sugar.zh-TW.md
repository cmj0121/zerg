# Zerg 語法糖（Syntax Sugar）

Zerg 保持一個**精簡核心**,在其上疊了幾個方便的表面寫法——每個都是 desugar 回核心的**語法糖**,語意上沒有新東西要
學。本頁把它們收在一處;各主題的完整說明見 [語言參考](../language.zh-TW.md)。亦有 [English](syntax-sugar.md) 版本。

## 已落地的語法糖

| 語法糖                             | Desugar 成                                                       |
| ---------------------------------- | ---------------------------------------------------------------- |
| `break if c` / `continue if c`     | `if c { break }` / `if c { continue }`                           |
| `raise e if c`                     | `if c { raise e }`——同一個後綴 guard，用在第四種 diverge         |
| `if x := e { … }`                  | 對 `e` 的 one-arm `match`——`x` 存在時才執行區塊                  |
| `with e as y { … }`                | `{ y := e; … }`(block 自己的離開路徑已涵蓋)                      |
| `f"…{x}…"`                         | 編譯期把各段 `str` 串接,每個洞 `x.display()`                     |
| `f"{x!r}"` / `f"{x=}"`             | `f"{x.debug()}"` / 原文 `x=` 再接值                              |
| `f"{x:spec}"`                      | 經 `Format` protocol 呼叫 `x.format(spec)`                       |
| `a + b`、`a == b`、`a[i]`、`-a`、… | 該運算子的 spec 方法——`a.add(b)`、`a.eq(b)`、…                   |
| `for x in it { … }`                | 對 `it` 的迭代協定(以 `StopIteration` 收尾)                      |
| `x..y` / `x..=y` / `x..`           | `range(x, y)` / `range(x, y + 1)` / 開放 range（皆 builtin）     |
| `v in r`                           | `r.contains(v)`——membership（Range 靠 `Ord`,否則靠迭代）         |
| `lo..hi =>`（match arm）           | 一個 `_ if _ in lo..hi` arm——以 containment 比對,非 `equal`      |
| `xs[k]`                            | `Indexable[k 的型別].index(k)`——元素、slice（`Range`）或 map key |
| `p: T = e`（default）              | 呼叫端省略掉的那個引數;default `e` 每次呼叫時求值                |
| `print x`                          | best-effort 把 `x.display()` 加換行寫到 stdout                   |
| `e?`                               | 取出 `Left`,否則從函式提前 return 那個 `Right`                   |
| `a ?? b` / `a?.m` / `e!`           | default;optional chain 成 `nil`;force-unwrap 否則 raise          |
| `del ch`                           | 撤銷名字**並**放掉這個持有者（要結束 stream 請用 `close(ch)`）   |
| `assert c`                         | 運算元暫時變數,再 `raise AssertionError(<訊息>) if not (c)`      |

**狀態。** 上表每一列皆可用，唯 f-string 的洞、`del ch`,以及使用者自訂型別上的運算子那幾列除外。洞裡只有純
`{x}` 形式可用:**轉換**（`!r` / `!s` / `!a`）是 `E9013`、**format spec**（`{x:.2f}`）是 `E9012`、自述的
`f"{x=}"` 是 `E9014`。**複合值**的洞同樣被拒,所以結構化渲染也是 **[not yet]**——`struct` 是指名的
（_E9059 NotImplemented: rendering a P as text_）,而 `list` 或 `map` 走的是一條普通的受檢規則、且怪罪一個程式從未
寫過的 bridge（_E4011 `str(…)` over a list bridges bytes or code points_）——見
[格式化與文字](../runtime/format.zh-TW.md)。

**`del ch`** 是 **[not yet]**:_E9066 NotImplemented: `del ch` on a CHANNEL_,它指向 `close(ch)`,以及 binding
的 scope 本來就會做的那次釋放。而**運算子**那一列只有在運算子屬編譯器所有時才 desugar:沒有任何運算子 `spec` 被
宣告,所以 `impl Add for P` 是 _E3013 no spec named `Add`_、`P(1) + P(2)` 是 _E3043_——見
[Spec 與 Generics](../core/specs.zh-TW.md)。`==` 是例外,經 `#[derive(Eq)]` 或手寫的 `impl Eq`。

兩種命令字面量都是 **[not yet]**,而且分得開:純 `` `…` `` 是 _E9020 NotImplemented: a command literal_,內插的
`` f`…` ``（屬文法、未列於此）是 _E9019_。上表其餘各 desugar 一如所寫。

**文法有、而上表沒有的 sugar。** 有兩個文法推導得出的改寫是 **[not yet]**,因此它們不在上表、而不是被列成已落地:
**解構綁定**——`(a, b) := e`（`E9021`）與它的 struct 形式 `P{x, y} := e`（`E9008`）,本編譯器要你改寫成一個名字加
一次 field access——以及**具名引數** `f(x: 1)`（`E9010`）,引數只以位置綁定。那一列真正落地的另一半是 default
參數,它就在上表。處於此狀態的完整形式清單,連同守著它的 gate,見
[已規範但未實作](grammar.zh-TW.md#已規範但未實作)。

## 把它還原回去

`zerg desugar` 會把上表裡的 sugar 改寫回它 desugar 成的 core 形式,而 `make desugar` 會把兩種形式都建起來、都跑一
遍,檢查它們是同一個程式——因為編譯器對每個 surface form 是直接 lowering 的,所以這裡某一列**點名**的那個 core 形式
走的是 emitter 裡的另一條路徑,而沒有東西比較過兩者。

今天有四列能被還原:postfix guard、while-`for`、range-`for`,以及 `assert`。其餘 decline,而每一條 decline 都是量出來的理由、不是
缺口——`for x in xs` 需要 `xs` 的型別,而 range arm 的 core 形式目前建不起來。見
[Desugar 規則](../tooling/desugar.zh-TW.md)。

## 刻意**不是**語法糖的

為了讓核心誠實,有些看起來像糖的其實是獨立的東西,不是改寫:

- **`type X = Y`** 是**強 typedef**——全新、獨立的型別,非透明別名。
- **`+%` / `-%` / `*%`** 是**獨立的回繞運算子**,不是 `+` / `-` / `*` 的修飾。
- **`#[derive(X)]`** 是 compiler 的**程式碼產生器**,以 blessed spec 為鍵、讀型別結構產出 impl——**不是**空 `impl`
  的語法糖（見 [Derive & Default Behavior](../core/derive.zh-TW.md)）。
- **`#[…]` decorator** 是**固定、compiler 擁有**的指令集,不是使用者可自訂的 macro——Zerg **沒有 macro**,所以沒有任何
  使用者語法糖能改寫你的程式。
