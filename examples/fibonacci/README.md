# Fibonacci — algorithmic complexity examples (1/N)

The first of an algorithmic-complexity series under `examples/`. Computes the
N-th Fibonacci number iteratively in O(N) time and O(1) space, with N read
from the command line. The running state is `math.BigInt` so large indices
(e.g. `1000`) stay exact instead of overflowing `int64`.

## Run

```sh
./src/bootstrap/bin/zerg run examples/fibonacci/main.zg 10
# 55
```

Or compile to a native binary and execute it (output is byte-identical to
the interpreter, per the run/build parity rule):

```sh
cd /tmp
/path/to/zerg build /path/to/examples/fibonacci/main.zg
./main 10
# 55
```

Large indices stay exact:

```sh
./src/bootstrap/bin/zerg run examples/fibonacci/main.zg 100
# 354224848179261915075
./src/bootstrap/bin/zerg run examples/fibonacci/main.zg 200
# 280571172992510140037611932413038677189525
```

## Argument contract

| Input              | Output                        | Exit |
| ------------------ | ----------------------------- | ---- |
| `main.zg 10`       | `55`                          | 0    |
| `main.zg` (no arg) | `Usage: zerg run main.zg <N>` | 1    |
| `main.zg foo`      | `Invalid N argument: foo`     | 1    |

## Language surface demonstrated

| Feature                              | Since | Where in `main.zg`                               |
| ------------------------------------ | ----- | ------------------------------------------------ |
| `os.argv()` for CLI arguments        | v0.9  | `args := os.argv()`                              |
| `strings.parse_int` + `Result` match | v0.8  | `match strings.parse_int(n) { … }`               |
| Bare-identifier string interpolation | v0.16 | `print "Invalid N argument: {n}"`                |
| `math.BigInt` arbitrary precision    | v0.17 | `math.from_int(0)`                               |
| Bundled `Arithmetic` operator spec   | v0.17 | `a + b` (lowers to `a.add(b)` on BigInt)         |
| `pub import` flat re-export          | v0.18 | `import "math"` → `math.from_int`, `math.BigInt` |
| Self-rehydrating multi-assign        | v0.19 | `a, b = b, a + b` over `BigInt` (struct)         |
| `print` auto-dispatch via `to_str`   | v0.20 | `print a` calls `a.to_str()` on `BigInt`         |

## Note on the loop shape

The fib step is written as a tuple parallel reassignment:

```zerg
a, b = b, a + b
```

Before v0.19 this only worked when `a` and `b` were primitive `int`s
(copy on read). Composite types — a struct like `BigInt` — tripped the
borrow checker's loop-body move guard: the bare-identifier `b` on the
RHS looked like a move into the tuple temp, and the second iteration
would observe a moved binding.

v0.19 special-cases bare idents that name one of the multi-assign's own
targets: they are read, not moved. The LHS write immediately rebinds the
slot, so the binding's post-statement state matches the pre-statement
state even inside a loop. The canonical Fibonacci step now reads the
same whether the running state is `int` or `BigInt`.

## Adding a new example to the series

Drop a sibling directory under `examples/<topic>/` with the same shape:

```text
examples/<topic>/
├── main.zg     # runnable Zerg source
└── README.md   # what / how to run / which language surface it exercises
```

Use `main.zg` as the entry filename — `zerg build` writes the produced
binary alongside the source under that name, which keeps the run / build
invocation symmetric across the series.
