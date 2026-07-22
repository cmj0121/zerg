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
