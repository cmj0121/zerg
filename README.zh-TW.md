# Zerg

[English](README.md) | 繁體中文

> 想到什麼就寫什麼——做一件事，只有一種、也是唯一一種方法。

Zerg 是一門**編譯式、通用型程式語言**。編譯器會把你的 Zerg 原始碼轉譯成 **C**（預設 **C17**，不行就 fallback 到
**C99**），再交給 C 編譯器做出原生執行檔。程式寫得快、讀得懂、直白到不能再直白。

## 設計原則（Design Principles）

| 原則             | 說明                                                    |
| ---------------- | ------------------------------------------------------- |
| small and crisp  | 最精簡的語法                                            |
| safe by default  | 除非明確標記 `mut`/`pub`，否則預設 immutable 且 private |
| null-safe        | 沒有那個造成十億美元損失的錯誤（null）                  |
| concurrent       | 內建的並行支援                                          |
| procedural-first | 直白、由上而下的控制流程                                |
| scope-owned      | 無 GC——記憶體在離開 scope 時釋放                        |
| strongly typed   | 在編譯期就抓出錯誤                                      |
| explicit casts   | 預設無隱式轉換；型別可 opt-in 一個 auto-cast            |
| copy-by-value    | 值預設以複製傳遞；編譯器可自行最佳化                    |

完整語意——primitive 與使用者型別、型別轉換、記憶體模型、並行、null-safety——見
**[語言參考（Language Reference）](docs/language.zh-TW.md)**，另有配套參考：**[Module、Package 與
Program](docs/package.zh-TW.md)**、**[Coroutines 與 Channels](docs/coroutine.zh-TW.md)**、
**[Collection](docs/collections.zh-TW.md)**、**[Derive 與預設行為](docs/derive.zh-TW.md)**、
**[Process 與 I/O](docs/io.zh-TW.md)**、與 **[FFI](docs/ffi.zh-TW.md)**。

## 編譯流程（Compile Flow）

```text
┌──────────────────┐
│  Zerg source     │
│  (.zg)           │
└────────┬─────────┘
         │
         ▼
┌────────────────────────── Zerg compiler ───────────────────────────┐
│                                                                    │
│  ┌─────────┐    ┌─────────┐    ┌────────────┐    ┌─────────────┐   │
│  │  lexer  │──> │ parser  │──> │ type check │──> │  C codegen  │   │
│  └─────────┘    └─────────┘    └────────────┘    └─────────────┘   │
│  └───────────────── frontend ──────────────┘     └── backend ──┘   │
└─────────────────────────────────┬──────────────────────────────────┘
                                  │
                                  ▼
                     ┌───────────────────────────┐
                     │  C source code            │
                     │  (default C17 → C99)      │
                     └─────────────┬─────────────┘
                                  │
                                  ▼
                     ┌───────────────────────────┐
                     │  C compiler (cc)          │
                     └─────────────┬─────────────┘
                                  │
                                  ▼
                     ┌───────────────────────────┐
                     │  native executable        │
                     └───────────────────────────┘
```

Bootstrap 編譯器：以 **Go** 撰寫，刻意保持最小化。

## DDD（Dream-Driven Development）

功能由作者的夢想與需求驅動——僅此而已。
