package fmt

import (
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// roundTrip parses src, formats, and returns the formatted bytes.
func roundTrip(t *testing.T, src string) string {
	t.Helper()
	tokens, comments, err := syntax.LexWithComments([]byte(src))
	if err != nil {
		t.Fatalf("LexWithComments: %v", err)
	}
	prog, err := syntax.ParseWithComments(tokens, comments)
	if err != nil {
		t.Fatalf("ParseWithComments: %v", err)
	}
	return string(Format(prog))
}

// idempotent verifies fmt(parse(fmt(parse(s)))) == fmt(parse(s)).
func idempotent(t *testing.T, src string) string {
	t.Helper()
	once := roundTrip(t, src)
	twice := roundTrip(t, once)
	if once != twice {
		t.Errorf("fmt is not idempotent.\nfirst:\n%s\nsecond:\n%s", once, twice)
	}
	return once
}

// canonical asserts that input is already in canonical form (fmt is a no-op
// on it).
func canonical(t *testing.T, src string) {
	t.Helper()
	got := roundTrip(t, src)
	if got != src {
		t.Errorf("not canonical.\nwant:\n%s\ngot:\n%s", src, got)
	}
	idempotent(t, src)
}

func TestFmtSimpleLet(t *testing.T) {
	canonical(t, `a := 5
b: int = 7
pi: float = 3.14
greeting := "hello"
flag: bool = true
print a
print b
print pi
print greeting
print flag
`)
}

func TestFmtIfElse(t *testing.T) {
	canonical(t, `x := 5
if x > 0 {
    print "pos"
} elif x == 0 {
    print "zero"
} else {
    print "neg"
}
`)
}

func TestFmtForLoops(t *testing.T) {
	canonical(t, `for i in 0..10 {
    print i
}
xs := [1, 2, 3]
for x in xs {
    print x
}
`)
}

func TestFmtFnDecl(t *testing.T) {
	canonical(t, `fn add(a: int, b: int) -> int {
    return a + b
}

print add(1, 2)
`)
}

func TestFmtStructAndImpl(t *testing.T) {
	canonical(t, `struct Counter { count: int }

impl Counter {
    fn double() -> int {
        return this.count * 2
    }
}

c := Counter { count: 7 }
print c.double()
`)
}

func TestFmtSpecImpl(t *testing.T) {
	canonical(t, `spec Printable {
    fn to_string() -> str
}

struct Counter { count: int }

impl Counter for Printable {
    fn to_string() -> str {
        return "counter"
    }
}

c := Counter { count: 7 }
p: Printable = c
print p.to_string()
`)
}

func TestFmtEnumAndMatch(t *testing.T) {
	canonical(t, `enum Color { Red, Green, Blue }

fn name(c: Color) -> str {
    match c {
        Color.Red => return "red"
        Color.Green => return "green"
        Color.Blue => return "blue"
    }
    return "unreachable"
}

print name(Color.Red)
`)
}

func TestFmtMatchTuple(t *testing.T) {
	canonical(t, `pair := (3, 4)
match pair {
    (0, 0) => print "origin"
    (a, b) => print a + b
}
`)
}

func TestFmtMatchStructWithRest(t *testing.T) {
	canonical(t, `struct Point { x: int, y: int }

fn classify(p: Point) -> str {
    match p {
        Point { x: 0, y: 0 } => return "origin"
        Point { x: 0, .. } => return "on y-axis"
        Point { x, y } => return "elsewhere"
    }
    return "unreachable"
}

print classify(Point { x: 0, y: 0 })
`)
}

func TestFmtGenericFn(t *testing.T) {
	canonical(t, `fn id[T](x: T) -> T {
    return x
}

print id(42)
print id("hello")
`)
}

func TestFmtNullableAndOption(t *testing.T) {
	canonical(t, `x: int? = 42
y: int? = nil
print x
print y
`)
}

func TestFmtCoalesceAndPropagate(t *testing.T) {
	canonical(t, `a: int? = 7
b: int? = nil
print a ?? 0
print b ?? 99
`)
}

func TestFmtSafeNav(t *testing.T) {
	canonical(t, `struct Inner { tag: str }
struct Outer { inner: Inner }

o: Outer? = Outer { inner: Inner { tag: "deep" } }
print o?.inner
print o?.inner?.tag
`)
}

func TestFmtChannelsAndSpawn(t *testing.T) {
	canonical(t, `fn main() {
    ch := chan[int]()
    spawn fn() {
        ch <- 1
    }()
    v := <- ch
    match v {
        Option.Some(x) => print x
        Option.None => print -1
    }
}

main()
`)
}

func TestFmtSelectStmt(t *testing.T) {
	canonical(t, `fn main() {
    ch := chan[int](1)
    ch <- 77
    select {
        bound := <- ch -> { print bound }
        _ -> { print 0 }
    }
}

main()
`)
}

func TestFmtDeferStmt(t *testing.T) {
	canonical(t, `fn three_defers() {
    defer print 1
    defer print 2
    defer print 3
    print 0
}

three_defers()
`)
}

func TestFmtImports(t *testing.T) {
	canonical(t, `import "util"

print util.add(1, 2)
`)
}

func TestFmtAliasedImport(t *testing.T) {
	canonical(t, `import "long/path/util" as u

print u.add(1, 2)
`)
}

func TestFmtTuplesAndLists(t *testing.T) {
	canonical(t, `pair := (1, 2)
triple := (10, 20, 30)
xs := [1, 2, 3]
print pair
print triple
print xs
`)
}

func TestFmtHeadCommentsNoBlank(t *testing.T) {
	// Source had NO blank between the head block and the first stmt;
	// canonical output preserves that — head→stmt without an inserted
	// blank.
	src := `# requires: v0.6
# Copyright (c) 2026 someone
# Licensed under MIT
x := 1
print x
`
	got := roundTrip(t, src)
	if got != src {
		t.Errorf("head comment format failed.\nwant:\n%s\ngot:\n%s", src, got)
	}
	idempotent(t, src)
}

func TestFmtHeadCommentsWithBlank(t *testing.T) {
	// Source had a blank between the head block and the first stmt;
	// canonical output preserves it.
	src := `# requires: v0.6

x := 1
print x
`
	got := roundTrip(t, src)
	if got != src {
		t.Errorf("head comment format failed.\nwant:\n%s\ngot:\n%s", src, got)
	}
	idempotent(t, src)
}

func TestFmtLeadingCommentsBeforeStmt(t *testing.T) {
	src := `first := 1

# above one
# above two
second := 2
`
	got := roundTrip(t, src)
	if !strings.Contains(got, "# above one") || !strings.Contains(got, "# above two") {
		t.Errorf("leading comments lost: %s", got)
	}
	idempotent(t, got)
}

func TestFmtNonCanonicalCollapsesBlankLines(t *testing.T) {
	src := `a := 1



b := 2
`
	want := `a := 1

b := 2
`
	got := roundTrip(t, src)
	if got != want {
		t.Errorf("multi-blank not normalised.\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestFmtBuiltinFn(t *testing.T) {
	// `__builtin name` form (as used in stdlib). Stdlib-only construct, so
	// must use ParseWithOptionsAndComments with InStdlibFile = true.
	src := `pub fn read_file(path: str) -> Result[str, IoError] __builtin io_read_file
`
	tokens, comments, err := syntax.LexWithComments([]byte(src))
	if err != nil {
		t.Fatalf("LexWithComments: %v", err)
	}
	prog, err := syntax.ParseWithOptionsAndComments(tokens, comments, syntax.ParseOptions{InStdlibFile: true})
	if err != nil {
		t.Fatalf("ParseWithOptionsAndComments: %v", err)
	}
	got := string(Format(prog))
	if got != src {
		t.Errorf("builtin fn round-trip failed.\nwant:\n%s\ngot:\n%s", src, got)
	}
}

func TestFmtAnonFnMultiStmt(t *testing.T) {
	canonical(t, `fn main() {
    xs := [1, 2, 3]
    done := chan[int]()
    spawn fn() {
        print xs[0]
        print xs[1]
        done <- 0
    }()
    _ := <- done
}

main()
`)
}

func TestFmtParenInExpr(t *testing.T) {
	// Parens kept when user wrote them — favour explicit over clever.
	canonical(t, `x := (1 + 2) * 3
print x
`)
}

func TestFmtEmptyListAnnotated(t *testing.T) {
	canonical(t, `empty: list[int] = []
print empty
`)
}

func TestFmtPropagateInBody(t *testing.T) {
	canonical(t, `fn parse(input: str) -> Result[int, str] {
    return Result.Ok(42)
}

fn process(input: str) -> Result[int, str] {
    v := parse(input)?
    return Result.Ok(v + 1)
}

print process("good")
`)
}

func TestFmtBreakContinue(t *testing.T) {
	canonical(t, `for i in 0..10 {
    if i == 3 {
        continue
    }
    if i == 7 {
        break
    }
    print i
}
`)
}

func TestFmtNestedMatch(t *testing.T) {
	canonical(t, `enum E { A, B(int) }

fn describe(e: E) -> str {
    match e {
        E.A => return "a"
        E.B(n) => match n {
            0 => return "b zero"
            x => return "b other"
        }
    }
    return "?"
}

print describe(E.A)
`)
}

// TestFmtIdempotenceSampler runs idempotence on a grab-bag of programs, to
// catch shape-specific drift in a single test.
func TestFmtIdempotenceSampler(t *testing.T) {
	cases := []string{
		`x := 1
print x
`,
		`fn main() {
    print 0
}

main()
`,
		`enum X { A, B, C }
print X.A
`,
		`struct S { x: int }
s := S { x: 1 }
print s.x
`,
	}
	for i, c := range cases {
		got := roundTrip(t, c)
		again := roundTrip(t, got)
		if got != again {
			t.Errorf("case %d not idempotent.\nfirst:\n%s\nsecond:\n%s", i, got, again)
		}
	}
}
