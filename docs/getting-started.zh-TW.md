# Getting started

[English](getting-started.md) | 繁體中文。

從 `hello, world` 到一支多檔案的程式。底下每一行都是你可以照打的指令；沒有任何一件事是沒跑過就寫
出來的。

這不是規格。它是穿過「這套工具鏈已經會做的事」最短而誠實的一條路，並在結尾把你交給
[語言參考](language.zh-TW.md)。

## 取得工具鏈

三條路,選哪一條取決於你打不打算改編譯器。

```sh
brew tap cmj0121/zerg https://github.com/cmj0121/zerg
brew install zerg
```

Homebrew **從原始碼建**,這也是為什麼只有一份 formula 而不是每個平台一個 bottle —— 而且它正是 Intel Mac 的答
案:發布的三個原生 tarball 沒有涵蓋那個平台。

或者從[發布頁](https://github.com/cmj0121/zerg/releases)拿一個 tarball —— `linux-x86_64`、`linux-arm64`、
`darwin-arm64` —— 解壓到任何地方。不需要 export 任何東西:`zerg` 會在自己旁邊找到 runtime 與標準函式庫。

```sh
tar -xzf zerg-0.1.0-darwin-arm64.tar.gz
./zerg-0.1.0-darwin-arm64/bin/zerg --version
```

或者自己建,而這一頁接下來假設的就是這條路。

## 工具鏈

建置需要 **Go ≥ 1.26** 與一個 **C 編譯器**。`zerg` 把你的原始碼翻成 C17 再交給 `cc`，所以那個 C
編譯器是**建置你的程式**時需要的，不只是建置 `zerg` 時需要。

```sh
make                # ./bin/zerg0，Go 種子 → ./bin/zerg，你會用的那個編譯器
```

`make` 會建出兩個編譯器，而你用的是第二個。`zerg0` 是一個 Go 寫的種子，被削減到只剩一件工作——建
出那個編譯器。`zerg` 就是那個編譯器，用 Zerg 寫成，由它自己編譯。

## Hello

```zerg
fn main() {
    print "hello, world"
}
```

```sh
./bin/zerg build hello.zg -o hello
./hello                              # hello, world
```

`print` 是**關鍵字**、不是函式——所以它不帶括號。`main` 是一個由 runtime 呼叫的普通函式；這個名字
沒有任何保留可言，只是「一支程式是一次以定義了它的 entry 檔為根的建置」。

`-o` 指定寫出的檔名。不給它，`zerg build hello.zg` 會把 `hello` 寫在原始碼旁邊。

### 更早停下來

`--emit` 讓它停在某個階段，而不是一路跑到 `cc`：

```sh
./bin/zerg build --emit check hello.zg     # 只要診斷，不產出任何 artifact
./bin/zerg build --emit c hello.zg         # 產出 C，寫到 stdout
```

`--emit check` 是快的那個——它跑每一條規則然後把 C 丟掉。編輯器每按一批鍵所問的就是它。

## 只有一種正典風格

只有一種，而且那不是偏好問題：

```sh
./bin/zerg fmt hello.zg
```

`zerg fmt` 就地改寫檔案。它沒有寬度、引號或縮排的選項，因為一個有選項的格式化工具，是一個兩個人可
以吵起來的格式化工具。**輸入若能解析，輸出就能解析**——那是不變量，而且有一道 gate 守著它。

`zerg lint` 是另外一半：它回報沒人用的 import，以及沒人呼叫的私有宣告。

## 第二個檔案

一個模組是一個**目錄**。把它放在你的 entry 檔旁邊：

```text
app/
    main.zg
    greet/
        greet.zg
```

```zerg
# greet/greet.zg
pub fn hello(name: str) -> str {
    return "hello, " + name
}
```

```zerg
# main.zg
import "./greet"

fn main() {
    print greet.hello("zerg")
}
```

```sh
./bin/zerg build main.zg -o app && ./app     # hello, zerg
```

那個 `import` 裡有兩件事在做工。

**跨過邊界的是 `pub`。** 每個宣告都從 module-private 起步。`hello` 上沒有那個 `pub`，`greet.hello`
就會被具名拒收——那就是 [`1g/private/`](../examples/1g/private)，兩支存在的目的就是被拒收的範例之一。

**`./` 說的是哪一個根。** 裸名字是**標準函式庫**、而且只會是它，所以 `import "io"` 永遠是標準函式庫
的 `io`，就算你自己有一個 `io.zg` 也一樣。以 `./` 開頭的路徑是**本專案**，在 entry 檔的目錄之下解析。
第一段含有一個點的路徑——`github.com/you/thing`——是遠端套件，這個編譯器保留了那個形狀但還沒建。

讀一個 import 就知道它是三者中的哪一個。新增或改名一個檔案，能改變的是一個 import **解不解析得到**，
而不是它指的是哪一類東西。

## 一個並排的測試

測試檔坐在**它所測試的模組旁邊**，命名為 `*_test.zg`，而且搆得到那個模組的私有表面：

```zerg
# greet/greet_test.zg
#[test]
fn test_hello_names_who() {
    assert hello("a") == "hello, a"
}
```

```sh
./bin/zerg test greet
# greet/greet_test.zg
#   ok    test_hello_names_who
#
#   1 passed, 0 failed, 0 skipped, 0 timed out
```

**它不 import 自己的模組。** 測試檔的 package 就是那個模組，所以 `hello` 本來就在 scope 裡；寫
`import "./greet"` 會讓那個模組被編兩次，而它裡面每一個宣告都會跟自己撞名。一個 `*_test.zg` 只由
`zerg test` 編譯、其他什麼都不編——一次正常建置會把它留在地上，所以它宣告的任何東西都到不了出貨的
程式。

`assert` 是保留字而不是函式，因為它回報的東西——檔案、行號、那個主張的原始文字，以及運算元當時的
值——是一個函式帶不了的。

## 讀一個模組提供什麼

```sh
./bin/zerg doc                # 有哪些模組
./bin/zerg doc strings        # 一個模組的完整文件
./bin/zerg doc strings.split  # 一個宣告
```

文件是從原始碼裡萃取出來的，而原始碼是唯一的那一份。**被公開的就是被記錄的**，而一個被公開卻沒人寫
過說明的宣告會被列出來並標記——一個安靜略過它的工具，會讓那個函式庫看起來比實際更完整。

## 接下來去哪

- **[Zerg by example](../examples/README.zh-TW.md)** —— 三十三支附閱讀順序的程式，每一支都由一道
  gate 建起來並執行。
- **[語言參考](language.zh-TW.md)** —— 索引：每一章，以及每一章決定了什麼。
- **[Conformance](conformance.zh-TW.md)** —— 怎麼讀規格的狀態標記。值得在**你撞到某個還沒建的東西
  之後**讀一次，而不是在那之前：被規範的語言刻意大於今天這個編譯器建起來的部分，而那一章正是把兩者
  分開的方法。
