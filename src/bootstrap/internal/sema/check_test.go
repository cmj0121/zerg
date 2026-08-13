package sema

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
)

// bindType parses src, checks it, and returns the single binding's resolved type
// (failing when there is not exactly one binding or any diagnostic).
func bindType(t *testing.T, src string) Type {
	t.Helper()
	file, pdiags := parser.Parse(src)
	if len(pdiags) != 0 {
		t.Fatalf("parse errors for %q: %v", src, pdiags)
	}
	info, sdiags := Check(file)
	if len(sdiags) != 0 {
		msgs := make([]string, len(sdiags))
		for i, d := range sdiags {
			msgs[i] = d.Msg
		}
		t.Fatalf("unexpected sema errors for %q: %v", src, msgs)
	}
	if len(info.BindTypes) != 1 {
		t.Fatalf("expected exactly one binding, got %d", len(info.BindTypes))
	}
	for _, ty := range info.BindTypes {
		return ty
	}
	return nil
}

// TestLiteralDefaultingAndContext covers literal defaulting and context-typing: a
// bare integer literal defaults to int, a fractional one to float, and a context
// type retypes the integer literal (DESIGN-1b §4.2/§4.3).
func TestLiteralDefaultingAndContext(t *testing.T) {
	t.Run("int literal defaults to int", func(t *testing.T) {
		if ty := bindType(t, "fn f() {\n  x := 5\n}"); ty != Int {
			t.Fatalf("x := 5 has type %s, want int", ty)
		}
	})
	t.Run("fractional literal defaults to float", func(t *testing.T) {
		if ty := bindType(t, "fn f() {\n  x := 1.5\n}"); ty != Float {
			t.Fatalf("x := 1.5 has type %s, want float", ty)
		}
	})
	t.Run("a context type retypes an int literal", func(t *testing.T) {
		if ty := bindType(t, "fn f() {\n  x: u8 = 5\n}"); ty.String() != "u8" {
			t.Fatalf("x: u8 = 5 has type %s, want u8", ty)
		}
	})
	t.Run("a fractional literal cannot fill an int", func(t *testing.T) {
		wantErr(t, "fn f() {\n  x: int = 1.5\n}", "cannot bind")
	})
}

// TestBindingInference synthesizes a binding's type from a composite literal
// (DESIGN-1b §4.2/§4.5).
func TestBindingInference(t *testing.T) {
	if ty := bindType(t, "fn f() {\n  xs := [1, 2, 3]\n}"); ty.String() != "list[int]" {
		t.Fatalf("xs := [1, 2, 3] has type %s, want list[int]", ty)
	}
}

// TestFixedWidthOperators types arithmetic over a fixed-width numeric and reports
// a mismatch between distinct numeric families (DESIGN-1b §4.6).
func TestFixedWidthOperators(t *testing.T) {
	wantOK(t, "fn f() {\n  a: u8 = 5\n  b: u8 = 6\n  c := a + b\n  print c\n}")
	wantErr(t, "fn f() {\n  a: u8 = 5\n  b := 2.0\n  c := a + b\n}", "matching numeric")
}

// TestStructConstructionAndField covers construction 'T(a: …)' with named
// arguments (fields as parameters) and field access (DESIGN-1b §3.6/§4.6).
func TestStructConstructionAndField(t *testing.T) {
	src := "struct Point {\n  pub x: int\n  pub y: int\n}\n" +
		"fn f() -> int {\n  p := Point(x: 1, y: 2)\n  return p.x\n}"
	wantOK(t, src)

	t.Run("an unknown field is an error", func(t *testing.T) {
		wantErr(t, "struct Point {\n  pub x: int\n}\nfn f() -> int {\n  p := Point(x: 1)\n  return p.z\n}",
			"no field")
	})
	t.Run("a wrong field type is an error", func(t *testing.T) {
		wantErr(t, "struct Point {\n  pub x: int\n}\nfn f() {\n  p := Point(x: true)\n}",
			"cannot use bool as int")
	})
}

// TestClosureParamInference infers an omitted closure parameter type from the
// expected function type at a call site (DESIGN-1b §4.4).
func TestClosureParamInference(t *testing.T) {
	wantOK(t, "fn apply(f: fn(int) -> int, x: int) -> int {\n  return f(x)\n}\n"+
		"fn g() {\n  print apply(fn(y) { return y + 1 }, 3)\n}")

	t.Run("an omitted parameter with no context cannot be inferred", func(t *testing.T) {
		wantErr(t, "fn g() {\n  h := fn(y) { return y }\n}", "infer")
	})
}

// TestValueGenericInference binds a value-generic parameter from the first
// argument and enforces it on the rest — local structural inference, not global
// unification (DESIGN-1b §4.7).
func TestValueGenericInference(t *testing.T) {
	pair := "fn pair[N: int](a: [int; N], b: [int; N]) -> int {\n  return 0\n}\n"
	wantOK(t, pair+"fn f() {\n  a: [int; 3] = [1, 2, 3]\n  b: [int; 3] = [4, 5, 6]\n  print pair(a, b)\n}")
	wantErr(t, pair+"fn f() {\n  a: [int; 3] = [1, 2, 3]\n  c: [int; 2] = [1, 2]\n  print pair(a, c)\n}",
		"as [int; 3]")
}

// TestReassignTypeMismatch reports a reassignment whose value type does not match
// the target's declared type, over both a bare binding and a struct field
// (DESIGN-1b §4.9).
func TestReassignTypeMismatch(t *testing.T) {
	wantErr(t, "fn f() {\n  mut x := 1\n  x = true\n}", "cannot assign")

	t.Run("a struct field reassignment is typed", func(t *testing.T) {
		wantErr(t, "struct Point {\n  pub x: int\n}\nfn f() {\n  mut p := Point(x: 1)\n  p.x = true\n}",
			"cannot assign")
	})
}

// TestThisInMethod binds 'this' to the receiver type inside an inherent method
// body (DESIGN-1b §4.9).
func TestThisInMethod(t *testing.T) {
	wantOK(t, "struct Point {\n  pub x: int\n}\nimpl Point {\n  fn get() -> int {\n    return this.x\n  }\n}")
}
