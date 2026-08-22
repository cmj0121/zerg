# Getting started

English | [繁體中文](getting-started.zh-TW.md).

From `hello, world` to a program in more than one file. Everything below is a command you can
type; nothing is described that has not been run.

This is not the specification. It is the shortest honest path through what the toolchain already
does, and it hands you to the [Language Reference](language.md) at the end.

## Getting the toolchain

Three ways in, and the one you want depends on whether you intend to change the compiler.

```sh
brew tap cmj0121/zerg https://github.com/cmj0121/zerg
brew install zerg
```

Homebrew **builds from source**, which is why there is one formula rather than a bottle per
platform — and why it is the answer on an Intel Mac, which the release's three native tarballs
do not reach.

Or take a tarball from the [release page](https://github.com/cmj0121/zerg/releases) —
`linux-x86_64`, `linux-arm64`, `darwin-arm64` — and unpack it anywhere. Nothing needs to be
exported: `zerg` finds its runtime and standard library beside itself.

```sh
tar -xzf zerg-0.1.0-darwin-arm64.tar.gz
./zerg-0.1.0-darwin-arm64/bin/zerg --version
```

Or build it, which is what the rest of this page assumes.

## The toolchain

A build needs **Go ≥ 1.26** and a **C compiler**. `zerg` translates your source to C17 and hands
that to `cc`, so the C compiler is needed to build a program and not only to build `zerg`.

```sh
make                # ./bin/zerg0, the Go seed → ./bin/zerg, the compiler you use
```

`make` builds two compilers and the second is the one you use. `zerg0` is a Go-hosted seed cut
down to a single job — building the compiler. `zerg` is that compiler, written in Zerg, compiled
by itself.

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

`print` is a **keyword**, not a function — so it takes no parentheses. `main` is an ordinary
function that the runtime calls; nothing is reserved about the name except that a program is a
build rooted at an entry file which defines one.

`-o` names the file written. Without it, `zerg build hello.zg` writes `hello` beside the source.

### Stopping earlier

`--emit` stops at a stage instead of running `cc`:

```sh
./bin/zerg build --emit check hello.zg     # the diagnostics alone, no artifact
./bin/zerg build --emit c hello.zg         # the C, to stdout
```

`--emit check` is the fast one — it runs every rule and throws the C away. It is what an editor
asks on a keystroke.

## One canonical style

There is one, and it is not a preference:

```sh
./bin/zerg fmt hello.zg
```

`zerg fmt` rewrites the file in place. It has no options for width, quotes or indentation, because
a formatter with options is a formatter two people can disagree with. If the input parses, the
output parses — that is the invariant, and a gate holds it.

`zerg lint` is the other half: it reports an import nothing uses and a private declaration nothing
calls.

## A second file

A module is a **directory**. Put one beside your entry file:

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

Two things are doing work in that `import`.

**`pub` is what crosses the boundary.** Every declaration starts module-private. Without the `pub`
on `hello`, `greet.hello` is refused by name — and that is [`1g/private/`](../examples/1g/private),
one of the two examples that exist to be turned away.

**The prefix says which root**, and there are four:

| Written                         | Found under                                                                |
| ------------------------------- | -------------------------------------------------------------------------- |
| `import "io"`                   | the **standard library**, always — even if you have an `io.zg` of your own |
| `import "/http"`                | **this package's root**, wherever the entry file sits                      |
| `import "./http"`               | **beside the file that wrote it**                                          |
| `import "github.com/you/thing"` | a **remote package** — reserved, and not built                             |

The last two differ as soon as a file is not at the root: a module two directories down names one
up there as `/shared`, and its own neighbour as `./count`. That is what lets a folder be moved
whole — the imports inside it still point at each other.

Reading an import tells you which of the four it is. Adding or renaming a file can change whether
an import resolves, never what kind of thing it names.

## A test beside it

A test file sits **beside the module it tests**, named `*_test.zg`, and reaches that module's
private surface:

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

**It does not import its own module.** The test file's package IS that module, so `hello` is
already in scope; writing `import "./greet"` compiles the module twice and every declaration in it
collides with itself. A `*_test.zg` is compiled by `zerg test` and by nothing else — a normal
build leaves it on the floor, so nothing it declares reaches a shipped program.

`assert` is a reserved word rather than a function, because what it reports — the file, the line,
the source text of the claim, and what the operands held — is what a function cannot carry.

## Reading what a module offers

```sh
./bin/zerg doc                # the modules there are
./bin/zerg doc strings        # one module's whole document
./bin/zerg doc strings.split  # one declaration
```

The document is extracted from the source, which stays the only copy. **What is exposed is what is
documented**, and an exposed declaration nobody wrote about is shown and marked — a tool that
quietly omitted one would make the library look more complete than it is.

## Where to go next

- **[Zerg by example](../examples/README.md)** — thirty-three programs in a reading order, each
  one built and run by a gate.
- **[The Language Reference](language.md)** — the index: every chapter, and what each decides.
- **[Conformance](conformance.md)** — how to read the specification's status markers. Worth
  reading **once you hit something that is not built yet**, and not before: the specified language
  is deliberately larger than what the compiler builds today, and that chapter is how the two are
  told apart.
