# Zerg by example

English | [繁體中文](README.zh-TW.md)

Thirty-three programs, in a reading order. Every one of them is **built and run by
`make examples`**, so nothing here is a snippet that used to work — the two that are meant to be
_refused_ are held to the sentence they must be refused with.

```sh
make                            # ./bin/zerg
./bin/zerg build examples/00_hello.zg -o /tmp/hello && /tmp/hello
```

If you have not written any Zerg yet, read [Getting started](../docs/getting-started.md) first: it
takes `hello.zg` to a program in more than one file, and hands you back here.

## Start here — the language, in order

The number in the name **is** the order. Each one is a whole program that prints something, and
each adds one idea to the one before it.

| Example                                | What it shows                                                 |
| -------------------------------------- | ------------------------------------------------------------- |
| [`00_hello.zg`](00_hello.zg)           | `fn main`, and `print` as a keyword rather than a function    |
| [`01_bindings.zg`](01_bindings.zg)     | `:=` binds; a binding is immutable until you write `mut`      |
| [`02_arithmetic.zg`](02_arithmetic.zg) | the integer operators, and what binds tighter than what       |
| [`03_floats.zg`](03_floats.zg)         | `float`, and why `1 / 2` is not `0.5`                         |
| [`04_booleans.zg`](04_booleans.zg)     | comparison, `and` / `or` / `not` as words                     |
| [`05_bitwise.zg`](05_bitwise.zg)       | the bit operators, which integers have and floats do not      |
| [`06_functions.zg`](06_functions.zg)   | parameters, a return type, and calling                        |
| [`07_match.zg`](07_match.zg)           | `match` over values and ranges, and why it must be exhaustive |
| [`08_loops.zg`](08_loops.zg)           | `for` in its three shapes — condition, range, and over a list |
| [`09_recursion.zg`](09_recursion.zg)   | a function calling itself, and where the stack ends           |
| [`10_fizzbuzz.zg`](10_fizzbuzz.zg)     | a capstone: loops, conditions and `print` together            |

## The parts that make it a language rather than a calculator

| Example                                  | What it shows                                                        |
| ---------------------------------------- | -------------------------------------------------------------------- |
| [`11_coroutines.zg`](11_coroutines.zg)   | `spawn`, and a channel as the way to watch what you spawned          |
| [`12_actor.zg`](12_actor.zg)             | mutable state owned by one coroutine, reached only by messages       |
| [`13_cancel.zg`](13_cancel.zg)           | a timeout and a cancellation are both channels, and `select` waits   |
| [`14_optional.zg`](14_optional.zg)       | `T?`, and the four ways to ask whether the value is there            |
| [`15_conversions.zg`](15_conversions.zg) | `T(x)` — there is no implicit conversion, so every one is written    |
| [`16_text.zg`](16_text.zg)               | a `str` is UTF-8, and what that makes a "character"                  |
| [`17_arithmetic.zg`](17_arithmetic.zg)   | integer arithmetic is **checked**: overflow raises, it does not wrap |
| [`18_scoped.zg`](18_scoped.zg)           | what frees a value, and who decides when                             |
| [`19_environment.zg`](19_environment.zg) | the environment: read anywhere, written only at startup              |

## The module layer

A second program hits this first, and it is the part with the fewest one-liners in the
specification. Each of these is a **directory**: an entry file and the modules it imports.

| Example                             | What it shows                                                        |
| ----------------------------------- | -------------------------------------------------------------------- |
| [`modules/`](modules)               | a two-module program — an entry file and a sibling directory module  |
| [`1g/visible/`](1g/visible)         | what a `pub` surface **does** reach across a module boundary         |
| [`1g/pubconst/`](1g/pubconst)       | `pub COUNT := 3` is a real member; a module binding needs no `const` |
| [`1g/modconst/`](1g/modconst)       | a module constant is ONE object, read the same wherever it is read   |
| [`1g/shapedconst/`](1g/shapedconst) | a module constant of a spelled type — a tuple, an optional           |
| [`1g/modtype/`](1g/modtype)         | a type reached through an import is also its constructor             |
| [`1g/init/`](1g/init)               | an imported module's `init()` runs once, before `main`'s first line  |
| [`1g/reexport/`](1g/reexport)       | `import pub` — a module putting another module's name on its surface |
| [`1g/spec/`](1g/spec)               | a `spec` declared in one module and implemented in another           |
| [`1g/strings/`](1g/strings)         | the standard library's `strings`, exercised end to end               |
| [`1g/testfile/`](1g/testfile)       | what a normal build compiles, and what it leaves on the floor        |

### The two that must be refused

An example is a claim about the language, and a **negative** one — _this program is not legal_ — is
a claim a build-and-run loop cannot check: it can only report that the build failed, which is what
a typo does too. So these two are held to the sentence they must be refused with, the way
`make reject` holds its own cases:

| Example                         | Refused with                             |
| ------------------------------- | ---------------------------------------- |
| [`1g/private/`](1g/private)     | `… is not a public member of module …`   |
| [`1g/privconst/`](1g/privconst) | the same rule, for a module **constant** |

If you compile one of these and get an error, that **is** the example.

## Where to go next

- **[Getting started](../docs/getting-started.md)** — `hello.zg` to a program in more than one file
- **[The Language Reference](../docs/language.md)** — every chapter, and what each one decides
- **[Conformance](../docs/conformance.md)** — how to read the specification's status markers. Read
  it once you want to know why something is not built yet, not before.
