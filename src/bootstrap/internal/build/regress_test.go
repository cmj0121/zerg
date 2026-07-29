package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExamplesNoSyntheticNames guards the byte-identical contract: a non-generic
// program must emit only 'zg_<name>' names, never a generics-era synthesized prefix
// (zgg_/zgt_/zgm_/zge_/zgd_/zgw_/zgs_). Every numbered example is non-generic, so
// any such prefix would mean the generic paths perturbed the historic backend.
func TestExamplesNoSyntheticNames(t *testing.T) {
	root, ok := repoRoot()
	if !ok {
		t.Skip("repo root (go.work) not found")
	}
	sources, err := filepath.Glob(filepath.Join(root, "examples", "[0-9][0-9]_*.zg"))
	if err != nil || len(sources) == 0 {
		t.Skip("no numbered examples found")
	}
	prefixes := []string{"zgg_", "zgt_", "zgm_", "zge_", "zgd_", "zgw_", "zgs_"}
	for _, zg := range sources {
		name := strings.TrimSuffix(filepath.Base(zg), ".zg")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(zg)
			if err != nil {
				t.Fatalf("read %s: %v", zg, err)
			}
			// An example past the seed's line emits no C at all, so it has no name to
			// check; that its refusal is the clean, named one is asserted there.
			if beyondSeed(string(src)) {
				checkSeedRefuses(t, name, string(src))
				return
			}
			code, _, diags := Compile(string(src))
			if len(diags) != 0 {
				t.Fatalf("%s should compile: %v", name, diags)
			}
			for _, p := range prefixes {
				if strings.Contains(code, p) {
					t.Fatalf("%s emitted a synthesized prefix %q; non-generic C must stay byte-identical", name, p)
				}
			}
		})
	}
}

// runProgram compiles src to C, links it, runs it, and returns stdout, failing on
// any diagnostic. It skips when no C compiler is present.
func runProgram(t *testing.T, src string) string {
	t.Helper()
	cc := findCC()
	if cc == "" {
		t.Skip("no C compiler found")
	}
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	return compileAndRun(t, cc, code)
}

// TestManglingNoFunctionCollision (B3) is the review's first repro: a user function
// whose name contains '__' must not collide with a generic instance. 'id__i' and
// 'id[int]' would both mangle to 'zg_id__i' under the old scheme and silently merge;
// with the prefixed, injective scheme they stay distinct and each runs.
func TestManglingNoFunctionCollision(t *testing.T) {
	got := runProgram(t, "fn id[T](x: T) -> T {\n  return x\n}\n"+
		"fn id__i(x: int) -> int {\n  return x * 100\n}\n"+
		"fn main() {\n  print id(5)\n  print id__i(5)\n}")
	if got != "5\n500\n" {
		t.Fatalf("collision mis-dispatch: got %q, want %q", got, "5\n500\n")
	}
}

// TestManglingNoTypeCollision (B4) is the review's second repro: a user type named
// 'Box__i' must not collide with the specialized 'Box[int]'. Both would key to
// 'zg_Box__i' before the fix and merge; after it each is its own C type.
func TestManglingNoTypeCollision(t *testing.T) {
	got := runProgram(t, "struct Box[T] {\n  v: T\n}\n"+
		"struct Box__i {\n  v: int\n}\n"+
		"fn main() {\n  a := Box(5)\n  b := Box__i(9)\n  print a.v\n  print b.v\n}")
	if got != "5\n9\n" {
		t.Fatalf("type collision mis-dispatch: got %q, want %q", got, "5\n9\n")
	}
}

// TestBoundMethodCallRuns (B1) is a generic body calling a bound method — the case
// that used to nil-deref in the emitter and was never enqueued. It must compile and
// dispatch to the impl method.
func TestBoundMethodCallRuns(t *testing.T) {
	got := runProgram(t, "spec Tag {\n  fn tag() -> int\n}\n"+
		"struct A {\n  v: int\n}\n"+
		"impl Tag for A {\n  fn tag() -> int {\n    return this.v\n  }\n}\n"+
		"fn label[T: Tag](x: T) -> int {\n  return x.tag()\n}\n"+
		"fn main() {\n  print label(A(7))\n}")
	if got != "7\n" {
		t.Fatalf("bound-method dispatch: got %q, want %q", got, "7\n")
	}
}

// TestExplicitTypeArgCallRuns (B1) is an explicit type-argument call 'id[int](x)',
// a non-Ident callee that used to nil-deref and was never resolved.
func TestExplicitTypeArgCallRuns(t *testing.T) {
	got := runProgram(t, "fn id[T](x: T) -> T {\n  return x\n}\n"+
		"fn main() {\n  print id[int](9)\n}")
	if got != "9\n" {
		t.Fatalf("explicit type-arg call: got %q, want %q", got, "9\n")
	}
}

// TestStructComparisonRuns (B2) compares two structs with a derived Eq/Ord — the
// operator must lower to the impl method, not raw C '=='.
func TestStructComparisonRuns(t *testing.T) {
	got := runProgram(t, "#[derive(Eq, Ord)]\nstruct P {\n  x: int\n  y: int\n}\n"+
		"fn main() {\n  print P(1, 2) == P(1, 2)\n  print P(1, 2) == P(1, 3)\n  print P(1, 2) < P(2, 0)\n}")
	if got != "true\nfalse\ntrue\n" {
		t.Fatalf("struct comparison: got %q, want %q", got, "true\nfalse\ntrue\n")
	}
}

// TestGenericEnumTwoTypeArgs (M1) instantiates one generic enum at two different
// type arguments — the struct case worked, the enum case falsely errored.
func TestGenericEnumTwoTypeArgs(t *testing.T) {
	got := runProgram(t, "enum Box[T] {\n  Full(T)\n  Empty\n}\n"+
		"fn unwrap[T](b: Box[T], d: T) -> T {\n  return match b {\n    Full(v) => v\n    Empty => d\n  }\n}\n"+
		"fn main() {\n  print unwrap(Full(42), 0)\n  print unwrap(Full(true), false)\n}")
	if got != "42\ntrue\n" {
		t.Fatalf("generic enum at two type args: got %q, want %q", got, "42\ntrue\n")
	}
}

// TestDerivedEnumEqPayload (M2) checks that a derived enum Eq distinguishes
// payloads, not just variants: same variant with different payload is not equal.
func TestDerivedEnumEqPayload(t *testing.T) {
	got := runProgram(t, "#[derive(Eq)]\nenum Shape {\n  Dot\n  Line(int)\n  Rect(int, int)\n}\n"+
		"fn same[T: Eq](a: T, b: T) -> bool {\n  return a == b\n}\n"+
		"fn main() {\n  print same(Line(3), Line(3))\n  print same(Line(3), Line(4))\n"+
		"  print same(Line(3), Dot)\n  print same(Rect(1, 2), Rect(1, 9))\n}")
	if got != "true\nfalse\nfalse\nfalse\n" {
		t.Fatalf("derived enum Eq payload: got %q, want %q", got, "true\nfalse\nfalse\nfalse\n")
	}
}
