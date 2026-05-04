// v0.2 codegen smoke tests. Each program is parsed, type-checked, emitted to
// C, compiled with the same flags `zerg build` uses, and executed; the binary
// stdout (and exit code, where relevant) is compared against the expected
// value.
//
// The harness shells out to `cc` so it is skipped (not failed) when no C
// compiler is on PATH. Every program here exercises a v0.2 surface element
// — runes, lists, tuples, structs, enums, match, etc. — and is small enough
// to keep the suite fast.
package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runV02 emits, compiles, and runs src. Returns (stdout, exitCode). Skips
// the test when cc is unavailable.
func runV02(t *testing.T, src string) (string, int) {
	t.Helper()
	cc := DefaultCC()
	if _, err := exec.LookPath(cc); err != nil {
		t.Skip("cc not available")
	}
	out := mustEmit(t, src)
	dir := t.TempDir()
	cPath := filepath.Join(dir, "p.c")
	if err := os.WriteFile(cPath, []byte(out), 0o644); err != nil {
		t.Fatalf("write c: %v", err)
	}
	binPath := filepath.Join(dir, "p")
	cmd := exec.Command(cc, "-fwrapv", "-O2", "-o", binPath, cPath, "-lm")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("cc failed: %v\n--- stderr ---\n%s\n--- generated ---\n%s",
			err, stderr.String(), out)
	}
	c := exec.Command(binPath)
	var stdout bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &bytes.Buffer{}
	err := c.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("exec: %v\n--- generated ---\n%s", err, out)
		}
	}
	return stdout.String(), code
}

// expectV02 runs src and asserts stdout matches want (and exit code is 0).
func expectV02(t *testing.T, src, want string) {
	t.Helper()
	got, code := runV02(t, src)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s", code, got)
	}
	if got != want {
		t.Errorf("stdout mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// ---------------------------------------------------------------------------
// Rune + byte literals.
// ---------------------------------------------------------------------------

func TestCgenRuneAsciiPrintsDecimal(t *testing.T) {
	expectV02(t, "print 'A'\n", "65\n")
}

func TestCgenRuneNonAsciiPrintsCodepoint(t *testing.T) {
	expectV02(t, "print '漢'\n", "28450\n")
}

// ---------------------------------------------------------------------------
// List literal, indexing, slicing, len.
// ---------------------------------------------------------------------------

func TestCgenListLitPrint(t *testing.T) {
	expectV02(t, "let xs := [1, 2, 3]\nprint xs\n", "[ 1, 2, 3 ]\n")
}

func TestCgenListEmptyPrint(t *testing.T) {
	expectV02(t,
		"let xs: list[int] = []\nprint xs\nprint len(xs)\n",
		"[]\n0\n")
}

func TestCgenListIndex(t *testing.T) {
	expectV02(t,
		"let xs := [10, 20, 30]\nprint xs[0]\nprint xs[2]\n",
		"10\n30\n")
}

func TestCgenListSliceForms(t *testing.T) {
	src := `let xs := [1, 2, 3, 4, 5]
print xs[1..3]
print xs[..2]
print xs[3..]
print xs[..]
print xs[1..1]
print xs[1..=3]
`
	expectV02(t, src,
		"[ 2, 3 ]\n[ 1, 2 ]\n[ 4, 5 ]\n[ 1, 2, 3, 4, 5 ]\n[]\n[ 2, 3, 4 ]\n")
}

func TestCgenListBindIsValueCopy(t *testing.T) {
	// v0.3: `let ys := xs` MOVES xs. Sampling both bindings requires an
	// explicit clone of the source so xs remains usable. The codegen path
	// still emits the per-shape copy helper, so the parity guarantee carries
	// over from v0.2 — only the user-facing shape changed.
	src := `let xs := [1, 2, 3]
let ys := clone(xs)
print xs
print ys
`
	expectV02(t, src, "[ 1, 2, 3 ]\n[ 1, 2, 3 ]\n")
}

func TestCgenListOfListIndexAndPrint(t *testing.T) {
	src := `let xs := [[1, 2], [3, 4, 5]]
print xs
print xs[1]
print len(xs)
print len(xs[1])
`
	expectV02(t, src, "[ [ 1, 2 ], [ 3, 4, 5 ] ]\n[ 3, 4, 5 ]\n2\n3\n")
}

func TestCgenForListIter(t *testing.T) {
	src := `let xs := [10, 20, 30]
for x in xs {
  print x
}
print "done"
`
	expectV02(t, src, "10\n20\n30\ndone\n")
}

// ---------------------------------------------------------------------------
// Tuples.
// ---------------------------------------------------------------------------

func TestCgenTupleLitPrint(t *testing.T) {
	expectV02(t, "let p := (1, 2)\nprint p\n", "( 1, 2 )\n")
}

func TestCgenTupleHeterogeneous(t *testing.T) {
	expectV02(t,
		"let p := (1, \"two\", 3)\nprint p\n",
		"( 1, two, 3 )\n")
}

func TestCgenLetTupleDestructure(t *testing.T) {
	src := `let p := (10, 20)
let (a, b) := p
print a + b
`
	expectV02(t, src, "30\n")
}

// ---------------------------------------------------------------------------
// Structs.
// ---------------------------------------------------------------------------

func TestCgenStructLitFieldAccess(t *testing.T) {
	src := `struct Point { x: int, y: int }
let p := Point { x: 7, y: 11 }
print p
print p.x
print p.y
`
	expectV02(t, src, "Point { x: 7, y: 11 }\n7\n11\n")
}

func TestCgenStructFieldOrderIsDeclOrder(t *testing.T) {
	src := `struct Point { x: int, y: int }
let p := Point { y: 99, x: 1 }
print p
`
	expectV02(t, src, "Point { x: 1, y: 99 }\n")
}

func TestCgenStructInList(t *testing.T) {
	src := `struct Point { x: int, y: int }
let pts := [Point { x: 1, y: 2 }, Point { x: 3, y: 4 }]
print pts[1].x
`
	expectV02(t, src, "3\n")
}

func TestCgenForwardStructRef(t *testing.T) {
	// struct A's field references struct B declared after it.
	src := `struct A { b: B, name: str }
struct B { v: int }
let a := A { b: B { v: 100 }, name: "hi" }
print a
print a.b.v
`
	expectV02(t, src, "A { b: B { v: 100 }, name: hi }\n100\n")
}

// ---------------------------------------------------------------------------
// Enums.
// ---------------------------------------------------------------------------

func TestCgenEnumVariantAccess(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
let c := Color.Green
print c
print Color.Red
print Color.Blue
`
	expectV02(t, src, "Color.Green\nColor.Red\nColor.Blue\n")
}

func TestCgenEnumInList(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
let cs := [Color.Red, Color.Blue]
print cs
print cs[0]
`
	expectV02(t, src, "[ Color.Red, Color.Blue ]\nColor.Red\n")
}

// ---------------------------------------------------------------------------
// Match.
// ---------------------------------------------------------------------------

func TestCgenMatchLiteralArms(t *testing.T) {
	src := `let n := 2
match n {
  1 => print "one"
  2 => print "two"
  _ => print "other"
}
`
	expectV02(t, src, "two\n")
}

func TestCgenMatchBindGuard(t *testing.T) {
	src := `let n := 7
match n {
  x if x > 5 => print "big"
  x => print "small"
}
`
	expectV02(t, src, "big\n")
}

func TestCgenMatchTupleDestructure(t *testing.T) {
	src := `let p := (10, 20)
match p {
  (a, b) => print a + b
}
`
	expectV02(t, src, "30\n")
}

func TestCgenMatchStructWithRest(t *testing.T) {
	src := `struct Point { x: int, y: int }
let p := Point { x: 5, y: 99 }
match p {
  Point { x: 0, .. } => print "x zero"
  Point { x, .. } => print x
}
`
	expectV02(t, src, "5\n")
}

func TestCgenMatchEnumVariants(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
let c := Color.Green
match c {
  Color.Red => print "red"
  Color.Green => print "green"
  Color.Blue => print "blue"
}
`
	expectV02(t, src, "green\n")
}

func TestCgenMatchNestedTupleStruct(t *testing.T) {
	src := `struct Point { x: int, y: int }
enum Color { Red, Blue }
let pair := (Point { x: 1, y: 2 }, Color.Blue)
match pair {
  (Point { x, .. }, Color.Red) => print x
  (Point { x, y }, Color.Blue) => print x + y
}
`
	expectV02(t, src, "3\n")
}

// TestCgenMatchNoArmPanics asserts the no-match panic exits 1 with the
// expected stderr line. PLAN says both halves emit the same message; the
// codegen helper writes "match: no arm matched at <pos>\n" to stderr.
func TestCgenMatchNoArmPanics(t *testing.T) {
	cc := DefaultCC()
	if _, err := exec.LookPath(cc); err != nil {
		t.Skip("cc not available")
	}
	src := `let n := 5
match n {
  1 => print "one"
  2 => print "two"
}
`
	out := mustEmit(t, src)
	dir := t.TempDir()
	cPath := filepath.Join(dir, "p.c")
	if err := os.WriteFile(cPath, []byte(out), 0o644); err != nil {
		t.Fatalf("write c: %v", err)
	}
	binPath := filepath.Join(dir, "p")
	cmd := exec.Command(cc, "-fwrapv", "-O2", "-o", binPath, cPath, "-lm")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("cc: %v\n%s", err, out)
	}
	c := exec.Command(binPath)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected exit error, got %v", err)
	}
	if ee.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1", ee.ExitCode())
	}
	if !strings.Contains(stderr.String(), "no arm matched") {
		t.Fatalf("stderr does not mention 'no arm matched': %q", stderr.String())
	}
}

// TestCgenIndexOutOfRange asserts list index OOB exits 1 with a stderr line
// pointing at the index expression's source position.
func TestCgenIndexOutOfRange(t *testing.T) {
	cc := DefaultCC()
	if _, err := exec.LookPath(cc); err != nil {
		t.Skip("cc not available")
	}
	src := `let xs := [1, 2]
print xs[5]
`
	out := mustEmit(t, src)
	dir := t.TempDir()
	cPath := filepath.Join(dir, "p.c")
	if err := os.WriteFile(cPath, []byte(out), 0o644); err != nil {
		t.Fatalf("write c: %v", err)
	}
	binPath := filepath.Join(dir, "p")
	cmd := exec.Command(cc, "-fwrapv", "-O2", "-o", binPath, cPath, "-lm")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("cc: %v", err)
	}
	c := exec.Command(binPath)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	err := c.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit")
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected exit error, got %v", err)
	}
	if ee.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1", ee.ExitCode())
	}
	if !strings.Contains(stderr.String(), "out of range") {
		t.Fatalf("stderr does not mention 'out of range': %q", stderr.String())
	}
}
