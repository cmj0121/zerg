package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// runSrc lexes, parses, and runs src. Returns stdout and any error.
// Run.Run calls syntax.Check internally, so callers do not pass a typed AST.
func runSrc(t *testing.T, src string) (string, error) {
	t.Helper()
	tokens, err := syntax.Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	prog, err := syntax.Parse(tokens)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var buf bytes.Buffer
	rerr := Run(prog, &buf)
	return buf.String(), rerr
}

// expectOK runs src and asserts the program completed without error and
// produced exactly want on stdout.
func expectOK(t *testing.T, src, want string) {
	t.Helper()
	got, err := runSrc(t, src)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got != want {
		t.Fatalf("stdout mismatch\n got: %q\nwant: %q", got, want)
	}
}

// expectErr runs src and asserts Run returns an error. Optionally checks
// the error message contains want when want != "".
func expectErr(t *testing.T, src, want string) {
	t.Helper()
	_, err := runSrc(t, src)
	if err == nil {
		t.Fatalf("Run unexpectedly succeeded; expected error containing %q", want)
	}
	if want != "" && !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not contain %q", err.Error(), want)
	}
}

// ---------------------------------------------------------------------------
// nop / hello — must keep working for v0.0 parity.
// ---------------------------------------------------------------------------

func TestNop(t *testing.T) {
	expectOK(t, "nop\n", "")
}

func TestPrintString(t *testing.T) {
	expectOK(t, `print "Hello, Zerg!"`+"\n", "Hello, Zerg!\n")
}

// ---------------------------------------------------------------------------
// print formats: int / float / bool / str.
// ---------------------------------------------------------------------------

func TestPrintInt(t *testing.T) {
	expectOK(t, "print 42\n", "42\n")
}

func TestPrintNegativeInt(t *testing.T) {
	expectOK(t, "print -7\n", "-7\n")
}

func TestPrintIntHex(t *testing.T) {
	expectOK(t, "print 0xff\n", "255\n")
}

func TestPrintIntUnderscore(t *testing.T) {
	expectOK(t, "print 1_000_000\n", "1000000\n")
}

func TestPrintFloat(t *testing.T) {
	// 17 sig digits matches what %.17g emits on the C side; PLAN.md pins the
	// precision so run/build agree byte-for-byte.
	expectOK(t, "print 3.14\n", "3.1400000000000001\n")
}

func TestPrintFloatTrailingZeroStripped(t *testing.T) {
	// %g (and FormatFloat 'g') drop redundant trailing zeros even at high precision.
	expectOK(t, "print 1.50\n", "1.5\n")
}

func TestPrintBoolTrue(t *testing.T) {
	expectOK(t, "print true\n", "true\n")
}

func TestPrintBoolFalse(t *testing.T) {
	expectOK(t, "print false\n", "false\n")
}

// ---------------------------------------------------------------------------
// let / mut / const bindings.
// ---------------------------------------------------------------------------

func TestLetInferred(t *testing.T) {
	expectOK(t, "let x := 10\nprint x\n", "10\n")
}

func TestLetAnnotated(t *testing.T) {
	expectOK(t, "let x: int = 7\nprint x\n", "7\n")
}

func TestMutAssignment(t *testing.T) {
	src := "mut x := 1\nx = 5\nprint x\n"
	expectOK(t, src, "5\n")
}

func TestMutCompoundAdd(t *testing.T) {
	src := "mut x := 10\nx += 5\nprint x\n"
	expectOK(t, src, "15\n")
}

func TestMutCompoundMul(t *testing.T) {
	src := "mut x := 3\nx *= 4\nprint x\n"
	expectOK(t, src, "12\n")
}

func TestConst(t *testing.T) {
	src := "const k := 100\nprint k\n"
	expectOK(t, src, "100\n")
}

// ---------------------------------------------------------------------------
// Arithmetic and bitwise operators.
// ---------------------------------------------------------------------------

func TestArithPrecedence(t *testing.T) {
	expectOK(t, "print 1 + 2 * 3\n", "7\n")
}

func TestArithParens(t *testing.T) {
	expectOK(t, "print (1 + 2) * 3\n", "9\n")
}

func TestIntDivTruncToZero(t *testing.T) {
	// PLAN.md pins truncation-toward-zero: -7 / 2 == -3, NOT -4.
	expectOK(t, "print -7 / 2\n", "-3\n")
}

func TestIntFloorDivSameAsDivAtV01(t *testing.T) {
	// Decision: at v0.1 `//` on int is identical to `/` on int (truncate
	// toward zero). Documented in run.go.
	expectOK(t, "print -7 // 2\n", "-3\n")
}

func TestIntModDividendSign(t *testing.T) {
	// PLAN.md pins: a == (a/b)*b + (a%b); sign of result follows dividend.
	expectOK(t, "print -7 % 3\n", "-1\n")
}

func TestFloatArith(t *testing.T) {
	expectOK(t, "print 1.5 + 2.5\n", "4\n")
}

func TestUnaryBitNot(t *testing.T) {
	expectOK(t, "print ~0\n", "-1\n")
}

func TestBitwiseAnd(t *testing.T) {
	expectOK(t, "print 12 & 10\n", "8\n")
}

func TestBitwiseOr(t *testing.T) {
	expectOK(t, "print 12 | 10\n", "14\n")
}

func TestBitwiseXor(t *testing.T) {
	expectOK(t, "print 12 ^ 10\n", "6\n")
}

func TestShiftLeft(t *testing.T) {
	expectOK(t, "print 1 << 4\n", "16\n")
}

func TestShiftRight(t *testing.T) {
	expectOK(t, "print 32 >> 2\n", "8\n")
}

// ---------------------------------------------------------------------------
// Comparison and logical.
// ---------------------------------------------------------------------------

func TestCompareIntEq(t *testing.T) {
	expectOK(t, "print 1 == 1\n", "true\n")
}

func TestCompareIntLT(t *testing.T) {
	expectOK(t, "print 2 < 1\n", "false\n")
}

func TestCompareStrLT(t *testing.T) {
	expectOK(t, `print "abc" < "abd"`+"\n", "true\n")
}

func TestLogicalAnd(t *testing.T) {
	expectOK(t, "print true and false\n", "false\n")
}

func TestLogicalOr(t *testing.T) {
	expectOK(t, "print true or false\n", "true\n")
}

func TestLogicalNot(t *testing.T) {
	expectOK(t, "print not true\n", "false\n")
}

func TestLogicalXor(t *testing.T) {
	expectOK(t, "print true xor false\n", "true\n")
}

// TestShortCircuitAnd verifies that the rhs of `and` is NOT evaluated when
// lhs is false. We observe this via a side effect (counter increment in a
// fn). Since v0.1 has no side effects in expressions other than fn calls,
// we use a function with a print that we'd see in stdout if it were called.
func TestShortCircuitAnd(t *testing.T) {
	src := `fn side() -> bool {
  print "side"
  return true
}
print false and side()
`
	// "side" must NOT appear because and short-circuits.
	expectOK(t, src, "false\n")
}

// TestShortCircuitOr is the dual: rhs not evaluated when lhs is true.
func TestShortCircuitOr(t *testing.T) {
	src := `fn side() -> bool {
  print "side"
  return false
}
print true or side()
`
	expectOK(t, src, "true\n")
}

// TestXorNotShortCircuit confirms xor evaluates both sides.
func TestXorNotShortCircuit(t *testing.T) {
	src := `fn side() -> bool {
  print "side"
  return false
}
print true xor side()
`
	// "side" must appear before "true".
	expectOK(t, src, "side\ntrue\n")
}

// ---------------------------------------------------------------------------
// String concat.
// ---------------------------------------------------------------------------

func TestStrConcat(t *testing.T) {
	src := `let s := "hello, " + "world"
print s
`
	expectOK(t, src, "hello, world\n")
}

// ---------------------------------------------------------------------------
// if / elif / else.
// ---------------------------------------------------------------------------

func TestIfTrue(t *testing.T) {
	src := `if true { print "yes" }
`
	expectOK(t, src, "yes\n")
}

func TestIfElse(t *testing.T) {
	src := `if false { print "yes" } else { print "no" }
`
	expectOK(t, src, "no\n")
}

func TestIfElifElse(t *testing.T) {
	src := `let x := 2
if x == 1 { print "one" } elif x == 2 { print "two" } else { print "other" }
`
	expectOK(t, src, "two\n")
}

// ---------------------------------------------------------------------------
// for loops: cond, range half-open, range closed, break/continue.
// ---------------------------------------------------------------------------

func TestForCond(t *testing.T) {
	src := `mut i := 0
for i < 3 {
  print i
  i += 1
}
`
	expectOK(t, src, "0\n1\n2\n")
}

func TestForRangeHalfOpen(t *testing.T) {
	src := `for x in 0..3 { print x }
`
	expectOK(t, src, "0\n1\n2\n")
}

func TestForRangeClosed(t *testing.T) {
	src := `for x in 1..=3 { print x }
`
	expectOK(t, src, "1\n2\n3\n")
}

func TestForBreak(t *testing.T) {
	src := `for x in 0..10 {
  break if x == 3
  print x
}
`
	expectOK(t, src, "0\n1\n2\n")
}

func TestForContinue(t *testing.T) {
	src := `for x in 0..5 {
  continue if x == 2
  print x
}
`
	expectOK(t, src, "0\n1\n3\n4\n")
}

func TestForInfiniteWithBreak(t *testing.T) {
	src := `mut i := 0
for {
  break if i == 2
  print i
  i += 1
}
`
	expectOK(t, src, "0\n1\n")
}

// ---------------------------------------------------------------------------
// Functions.
// ---------------------------------------------------------------------------

func TestFnNoArgsNoReturn(t *testing.T) {
	src := `fn greet() {
  print "hi"
}
greet()
`
	expectOK(t, src, "hi\n")
}

func TestFnArgsAndReturn(t *testing.T) {
	src := `fn add(a: int, b: int) -> int {
  return a + b
}
print add(2, 3)
`
	expectOK(t, src, "5\n")
}

func TestFnRecursion(t *testing.T) {
	src := `fn fact(n: int) -> int {
  return 1 if n == 0
  return n * fact(n - 1)
}
print fact(5)
`
	expectOK(t, src, "120\n")
}

func TestFnReturnGuardFallsThrough(t *testing.T) {
	// `return e if cond` only returns when cond is true; otherwise falls
	// through. Here the guard is false on the first call, so we hit the
	// final return.
	src := `fn pick(n: int) -> int {
  return 100 if n > 10
  return 0
}
print pick(5)
print pick(99)
`
	expectOK(t, src, "0\n100\n")
}

// ---------------------------------------------------------------------------
// Scope.
// ---------------------------------------------------------------------------

func TestBlockScopeShadow(t *testing.T) {
	src := `let x := 1
if true {
  let x := 2
  print x
}
print x
`
	expectOK(t, src, "2\n1\n")
}

// TestInnerLetDoesNotLeak ensures a binding made inside a block is gone
// after the block ends. typeck would reject use-after; we check the runtime
// path doesn't confuse this with the outer scope.
func TestInnerLetDoesNotLeak(t *testing.T) {
	// Two consecutive blocks introducing the same name: each gets its own
	// frame, so the second one's value prints — proving the first's binding
	// was popped.
	src := `if true {
  let x := 11
  print x
}
if true {
  let x := 22
  print x
}
`
	expectOK(t, src, "11\n22\n")
}

// ---------------------------------------------------------------------------
// Wrap-around arithmetic — matches C's -fwrapv.
// ---------------------------------------------------------------------------

func TestIntWrapAroundOnAdd(t *testing.T) {
	// 9223372036854775807 is INT64_MAX; +1 wraps to INT64_MIN.
	src := "print 9223372036854775807 + 1\n"
	expectOK(t, src, "-9223372036854775808\n")
}

// ---------------------------------------------------------------------------
// Type errors propagate from Check.
// ---------------------------------------------------------------------------

func TestTypeErrorPropagates(t *testing.T) {
	expectErr(t, "let x: int = 3.14\n", "type error")
}
