package build

import (
	"strings"
	"testing"
)

// TestCompletenessIter1Runs is the compile+run acceptance for Iteration 1 of the
// language-completeness pass: fixed-width local bindings (U4), concrete-type
// inherent (and generic) method dispatch (U3), and the expression-as-value trio —
// if-expression, block-expression, and return-if (U1), including a generic call
// nested inside each so monomorphization descends into the new value nodes.
func TestCompletenessIter1Runs(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "u4_fixed_width_locals",
			src: "fn main() {\n" +
				"  u: uint = 5\n" +
				"  b: byte = 65\n" +
				"  n: i32 = -7\n" +
				"  print u\n  print b\n  print n\n}\n",
			want: "5\n65\n-7\n",
		},
		{
			name: "u3_concrete_method",
			src: "struct Vec {\n  x: int\n  y: int\n}\n" +
				"impl Vec {\n  fn sum() -> int {\n    return this.x + this.y\n  }\n}\n" +
				"fn main() {\n  v := Vec(1, 2)\n  print v.sum()\n}\n",
			want: "3\n",
		},
		{
			name: "u1_if_expr_value",
			src:  "fn main() {\n  c := true\n  x := if c { 10 } else { 20 }\n  print x\n}\n",
			want: "10\n",
		},
		{
			name: "u1_block_expr_value",
			src:  "fn main() {\n  x := { a := 3\n    a + 4 }\n  print x\n}\n",
			want: "7\n",
		},
		{
			name: "u1_return_if",
			src: "fn pick(c: bool) -> int {\n  return if c { 1 } else { 2 }\n}\n" +
				"fn main() {\n  print pick(true)\n  print pick(false)\n}\n",
			want: "1\n2\n",
		},
		{
			name: "u1_generic_call_in_if_expr",
			src: "fn id[T](x: T) -> T {\n  return x\n}\n" +
				"fn main() {\n  c := true\n  x := if c { id(42) } else { 0 }\n  print x\n}\n",
			want: "42\n",
		},
		{
			name: "u1_generic_call_in_block_expr",
			src: "fn id[T](x: T) -> T {\n  return x\n}\n" +
				"fn main() {\n  x := { id(9) }\n  print x\n}\n",
			want: "9\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runProgram(t, tc.src); got != tc.want {
				t.Fatalf("run: got %q, want %q\n--- src ---\n%s", got, tc.want, tc.src)
			}
		})
	}
}

// TestIfExprBranchTypeMismatch covers the sema guard on the if-expression: every
// branch must yield the same type (GRAMMAR: "every branch must yield the same
// type"), so a mismatched branch is a clean diagnostic rather than bad C.
func TestIfExprBranchTypeMismatch(t *testing.T) {
	src := "fn main() {\n  c := true\n  x := if c { 1 } else { \"two\" }\n  print x\n}\n"
	_, _, diags := Compile(src)
	if len(diags) == 0 {
		t.Fatal("an if-expression with differently-typed branches should be rejected")
	}
	joined := ""
	for _, d := range diags {
		joined += d.Error()
	}
	if !strings.Contains(joined, "same type") {
		t.Fatalf("expected a branch-type diagnostic, got: %v", diags)
	}
}

// TestCompletenessIter2Runs is the compile+run acceptance for Iteration 2 of the
// language-completeness pass: tuple values (U2) — a tuple binding, a tuple returned
// from a function, and a static `.N` element read — together with the iteration-1
// value constructs (if/block-expression-as-value, concrete-type method dispatch, and
// fixed-width locals) exercised through a tuple, so the whole value surface composes.
// Every case here is value-only (no runtime), so it links as a single cc unit.
func TestCompletenessIter2Runs(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "u2_tuple_bind_and_index",
			src:  "fn main() {\n  t := (11, 22)\n  print t.0 + t.1\n}\n",
			want: "33\n",
		},
		{
			name: "u2_tuple_returned_from_fn",
			src: "fn pair() -> (int, int) {\n  return (3, 4)\n}\n" +
				"fn main() {\n  t := pair()\n  print t.0\n  print t.1\n}\n",
			want: "3\n4\n",
		},
		{
			name: "u2_three_tuple",
			src:  "fn main() {\n  p := (1, 2, 3)\n  print p.0 + p.1 + p.2\n}\n",
			want: "6\n",
		},
		{
			name: "u2_tuple_in_if_expr",
			src: "fn main() {\n  c := true\n  t := if c { (1, 2) } else { (3, 4) }\n" +
				"  print t.0 + t.1\n}\n",
			want: "3\n",
		},
		{
			// tuple value + method dispatch (U3) + fixed-width local (U4) + if-expr (U1).
			name: "u2_composed_with_iter1",
			src: "struct Vec {\n  x: int\n  y: int\n}\n" +
				"impl Vec {\n  fn sum() -> int {\n    return this.x + this.y\n  }\n}\n" +
				"fn main() {\n  w: u16 = 7\n  v := Vec(1, 2)\n" +
				"  hi := if w > 5 { 100 } else { 0 }\n" +
				"  t := (v.sum(), hi)\n  print t.0\n  print t.1\n}\n",
			want: "3\n100\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runProgram(t, tc.src); got != tc.want {
				t.Fatalf("run: got %q, want %q\n--- src ---\n%s", got, tc.want, tc.src)
			}
		})
	}
}

// TestGuardResultNilReconciles covers the U5(d) fix: a `return guard { … }` whose
// block yields a value gives the guard the type `Result[T]` (a `zg_result_<n>`
// carrier), yet the enclosing function returns `Result[nil]` (the tag-only
// `zrt_result_nil`). The two representations must be reconciled at the return so the C
// compiles — previously the raw carrier value was returned into a mismatched type. The
// fix projects the carrier's ok/err tag into the nil result.
func TestGuardResultNilReconciles(t *testing.T) {
	src := "fn f() -> Result[nil] {\n  return guard {\n    5\n  }\n}\n" +
		"fn main() -> Result[nil] {\n  f()?\n  return nil\n}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("return guard in a Result[nil] fn should compile, got diagnostics: %v", diags)
	}
	// the guard carrier tag is projected into the tag-only nil result on the return.
	if !strings.Contains(code, "(zrt_result_nil){ .tag =") {
		t.Fatalf("expected the guard carrier tag to be projected into zrt_result_nil, got:\n%s", code)
	}
	// the function returns the tag-only nil result type, not the mismatched carrier.
	if !strings.Contains(code, "zrt_result_nil zg_f(void)") {
		t.Fatalf("expected zg_f to return zrt_result_nil, got:\n%s", code)
	}
}

// TestAtomicNameableType covers U5(c): `Atomic[int]` is a nameable type — usable as a
// parameter/binding type — with its atomic operations reached as methods, backed by a
// stdlib nominal wrapper over a `Ref[int]` cell (zero backend change beyond the shared
// generic/method machinery). It needs the runtime, so it is asserted at the C level.
func TestAtomicNameableType(t *testing.T) {
	src := "struct Atomic[T] {\n  cell: Ref[T]\n}\n" +
		"impl Atomic[int] {\n" +
		"  fn load() -> int {\n    return __zrt_atomic_load(this.cell)\n  }\n" +
		"  fn fetch_add(n: int) -> int {\n    return __zrt_atomic_add(this.cell, n)\n  }\n}\n" +
		"fn make() -> Atomic[int] {\n  return Atomic(Ref(0))\n}\n" +
		"fn f(a: Atomic[int]) -> int {\n  a.fetch_add(5)\n  return a.load()\n}\n" +
		"fn main() -> Result[nil] {\n  a := make()\n  print f(a)\n  return nil\n}\n"
	code, _, diags := Compile(src)
	if len(diags) != 0 {
		t.Fatalf("Atomic[int] as a nameable type should compile, got diagnostics: %v", diags)
	}
	for _, want := range []string{"zrt_atomic_add(", "zrt_atomic_load("} {
		if !strings.Contains(code, want) {
			t.Fatalf("expected the atomic intrinsic %q in the emitted C, got:\n%s", want, code)
		}
	}
}

// TestChanRecvSendNarrowingDiagnostic covers U5(b): `.recv` / `.send` value narrowing
// of a channel is not part of the language — a channel is narrowed by a type annotation,
// not by a `.recv`/`.send` field — so it must fail with a clean diagnostic rather than
// silently typing to void and emitting a bad field access. Direction narrowing via a
// `<-chan[T]` / `chan[T]<-` type annotation remains the supported path.
func TestChanRecvSendNarrowingDiagnostic(t *testing.T) {
	for _, name := range []string{"recv", "send"} {
		src := "fn main() {\n  ch := chan[int](1)\n  x := ch." + name + "\n}\n"
		code, _, diags := Compile(src)
		if len(diags) == 0 {
			t.Fatalf("ch.%s value narrowing should be rejected with a clean diagnostic", name)
		}
		if code != "" {
			t.Fatalf("no C should be emitted for a rejected program, got:\n%s", code)
		}
		joined := ""
		for _, d := range diags {
			joined += d.Msg
			if strings.Contains(d.Msg, "internal:") {
				t.Fatalf("channel narrowing must be a clean gate, got internal error: %v", diags)
			}
		}
		if !strings.Contains(joined, "a channel is narrowed by a type annotation") {
			t.Fatalf("expected the channel narrowing diagnostic for .%s, got: %v", name, diags)
		}
	}
}
