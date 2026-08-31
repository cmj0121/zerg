# Zerg 格式化與文字（Formatting & Text）

每個值如何渲染——內建的 `display` / `debug` 渲染、`f"…"` 內插,以及 `print` 關鍵字。屬於
[語言參考](../language.zh-TW.md) 的一部分。亦有 [English](format.md) 版本。

每個值都有兩種渲染。它們是**內建的值渲染**、不是任何 `Object` spec 的 method——Zerg 沒有 auto-implement 的
`Object` spec（[Spec 與 Generics](../core/specs.zh-TW.md)）——所以 `display` 與 `debug` 在每個值上都可用、不需 opt-in 任何
東西：

- **`debug`——開發者視圖。** **結構化**渲染（sum 則先 tag 再 payload）、可 override。logging、`stderr`、abort
  backtrace 用的就是它——機械、不憑空猜文案。
- **`display`——給人看的視圖。** 它的**預設就是 `debug`**，所以永遠存在。override 它來決定終端使用者該怎麼讀這個值
  （金額、日期）；compiler 永不衍生語意化渲染，所以有意義的 `display` 只能 override。

**override** 是寫在該型別 `impl` 裡、形狀固定的 method：`fn display() -> str`（或 `fn debug() -> str`）——
它只接收這個值本身、回答該值顯示成的 `str`，而且絕不改動它（是普通 `fn`、不是 `mut fn`）；同名而形狀不同的
method 會在宣告處被拒絕。`print`、格式洞與 `str(…)` 都會採用 override，而只寫了 `debug` 的型別在每個渲染點
都透過它渲染，因為 `display` 預設就是它。

> **狀態。** 除了一類之外,每個值都渲染得出來。**純量**、**`str`**、**`Err`** 與**複合值**——`struct`、`enum`、
> `list`、`map`、tuple、陣列、載體——透過純 `{x}` 洞、`print`、`str(x)` 與那兩個 method 全都渲染得出來,而任何
> 宣告了 override 的具名型別（`type X = Y`、`struct`、`enum`）會先採用該 override。一個 `Err` 渲染為它的**訊息**;
> 它的 kind 是拿來比較的（`e is IOError`）,不是拿來讀出的。**渲染不出來**的是沒有任何部分、也不在等任何東西的
> 那一類:**channel**、**函式值**——_E4076 a … is an identity rather than a value, and the language gives it no
> rendering_——以及 **nil**,它根本不是一個值（_E3086 this rendering needs a value, and this one is nil_）。
>
> **結構化的拼法就是這個語言自己的 literal。** 一個複合值渲染成讀者本來就會寫出來的那個 literal,只有一個差別,
> 而那正是 developer 視角的重點:**複合值裡面的 `str` 會加引號並跳脫**,因為 `["a, b"]` 與 `["a", "b"]` 是兩個
> 不同的 list。**單獨**的一個 `str` 就是它自己的文字——引號是**位置**的性質而不是視角的性質,這正是讓
> `print s` 印出 `hi` 而 `print [s]` 印出 `["hi"]` 的那條規則。
>
> | 形狀                | 渲染成                                                           |
> | ------------------- | ---------------------------------------------------------------- |
> | `list[T]`、`[T; N]` | `[1, 2]`                                                         |
> | `map[K, V]`         | `{"k": 1}`——**插入**序,也就是 `for` 走訪它的那個順序             |
> | tuple               | `(1, "x")`                                                       |
> | `struct P`          | `P(x: 1, y: "a")`——那個 constructor,外加讀者否則得自己數的欄位名 |
> | `enum E`            | `E.A`,帶 payload 的變體則是 `E.B(3)`                             |
> | `T?`                | 那個值,或 `nil`                                                  |
> | `Either[X, Y]`      | `Left(1)` / `Right(…)`——先 tag、再 payload                       |
>
> 複合值沒有結構化的**相等**,那是同一個問題換一個動詞問:兩個 list 的 `xs == ys` 是 `E9057`
> （[Spec 與泛型](../core/specs.zh-TW.md)）。渲染被推導出來而比較沒有——這是兩個各自的決定,理由是渲染是一種
> 視角,而相等是一個關於值的主張。
>
> **四種拼法都抵達同一個產生器。** `str(x)`、洞、`print`,以及寫出來的 `x.display()` / `x.debug()`,都先諮詢
> override、再落到結構化渲染,所以 `map` 不會為了一個渲染而拿到 map 那句關於 `len` 與 `has` 的話,而這四者之間
> 也不可能對同一個值講出不一樣的答案。
>
> **哪裡都渲染不出來的那一類,用一句話說清楚。** **channel** 與**函式值**是身分而不是值——就是 `==` 被拒絕的那
> 同一類,_E4034_——所以沒有部分可以拿來渲染,也沒有東西在路上:四種拼法都得到 _E4076 a … is an identity rather
> than a value, and the language gives it no rendering_。**nil** 是第三種答案,因為 nil 根本不是一個值:沒有
> `-> type` 的 `fn` 回答的就是它（[`GRAMMAR#fn-decl`](../../GRAMMAR)）,而 `str(f())` 會被指名告知——
> _E3086 this rendering needs a value, and this one is nil_——讀者需要的是一個會回答東西的 `fn`,不是一個渲染。
> **內插——`f"…"`。** 裸 `"…"` 是字面量（大括號是普通字元）。**`f`-string** 內嵌 `{ expr }`，透過 `display` 渲染
> 再串接——`f"sum={x + y}"`——在**編譯期 desugar** 成 `str` 串接（Collections），不需 variadic、無 runtime 格式
> 引擎。洞是 **Python 形狀**——`{ expr =? !conv? :spec? }`：

- **`{x}`** 用 `display`；**轉換**可先換視圖——**`!r`** 用開發者 `debug`、**`!s`** 用 `display`、**`!a`** 用
  ASCII-escaped 的 debug。`f"{x!r}"` 把 `x` 以 `debug` 渲染。三者皆為 **[not yet]**——_E9013 NotImplemented: an
  f-string '!r' / '!s' / '!a' conversion_。
- **`{x=}`** 自述：印出運算式原文、`=`，再接值——`f"{n=}"` → `n=42`（可與其餘組合：`f"{n=:04d}"`）。**[not yet]**
  ——被辨識之後由 **parser 拒絕**（`E9014`）。
- **`{x:spec}`** 把 spec 字串交給型別的 **`Format`** 協定——`f"{pi:.2f}"`、`f"{n:04d}"`、`f"{p:>10}"`。這是
  **per-type 協定**、非 `display` 參數：語言只固定 `:spec` 的**語法**（到 `}` 為止的不透明文字）；一個 spec 的**意義**
  由型別自定——stdlib 數字與 `str` 讀常見的 `[[fill]align][sign][#][0][width][.precision][type]`，比照 Python。對
  format spec 為 **[not yet]**——_E9012 NotImplemented: an f-string ':spec' format spec_。

  > **spec 是程式自己寫的文字,而它的每一個欄位都有界。** `type` 字母對每一種渲染都是**封閉集合**——float 取
  > `e E f F g G`，int 取 `b o x X c d`——其中 `c` 把 int 當作它指名的**碼位**渲染,並拒絕任何 `str` 裝不下的
  > 碼位——`str` 取 `s`，而 `width` 與 `precision` 有實作上限
  > ([一致性](../conformance.zh-TW.md))。落在這兩者之外的 spec 會被指名拒絕為 `ValueError`。今天做這件事的是
  > **runtime**:spec 這個形式本身在這個實作裡是 `[not yet]`,所以會去檢查它的那個編譯器,正是不建置它的那
  > 一個。這不是修飾。那個字母原本會被拼進 C 格式器自己的 pattern，所以
  > `{x:.6s}` 會把一個 float 用 `%s` 渲染——把數字當指標讀——而 `{x:.6n}` 會走到 `%n`，那是**寫**穿它的引數。
  > **[implementation-defined]** 浮點渲染——預設的 `%g` 式（6 位有效數字）以及 `NaN`、`Inf`／`-Inf`、`-0.0` 的
  > 拼法——規格不釘定；conforming 實作各自載明。切勿依賴確切的浮點拼法。

**`print`** 把一個值的 `display` 渲染加換行寫到 stdout——一個**保留字**、永遠在 scope 內、免 import，所以最小的
程式就是 `print f"hello {name}"`。它**盡力而為**（寫入錯誤被丟掉、不 raise），所以不需 `?`；有檢查的完整 I/O 面是
要 import 的 `io` package（見 [Process & I/O](io.zh-TW.md)）。
