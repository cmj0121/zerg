package sema

import "testing"

// TestRefBuiltins covers the Phase 1d Ref[T] builtins the checker models: the box
// constructor 'Ref(v)' / 'Ref[T](v)' and its reader 'deref(r)'.
func TestRefBuiltins(t *testing.T) {
	t.Run("Ref(v) infers the element type", func(t *testing.T) {
		if ty := bindType(t, "fn f() {\n  r := Ref(7)\n}"); ty.String() != "Ref[int]" {
			t.Fatalf("Ref(7) has type %s, want Ref[int]", ty)
		}
	})
	t.Run("Ref[T](v) takes the element type explicitly", func(t *testing.T) {
		if ty := bindType(t, "fn f() {\n  r := Ref[str](\"hi\")\n}"); ty.String() != "Ref[str]" {
			t.Fatalf("Ref[str](\"hi\") has type %s, want Ref[str]", ty)
		}
	})
	t.Run("deref reads the boxed element", func(t *testing.T) {
		if ty := bindType(t, "fn f() {\n  x := deref(Ref(7))\n}"); ty != Int {
			t.Fatalf("deref(Ref(7)) has type %s, want int", ty)
		}
	})
	t.Run("Ref[T] names a type in a signature", func(t *testing.T) {
		if ty := bindType(t, "fn box() -> Ref[int] {\n  return Ref(1)\n}\nfn f() {\n  r := box()\n}"); ty.String() != "Ref[int]" {
			t.Fatalf("box() has type %s, want Ref[int]", ty)
		}
	})
	t.Run("a mismatched explicit element is an error", func(t *testing.T) {
		wantErr(t, "fn f() {\n  r := Ref[str](7)\n}", "cannot store")
	})
	t.Run("deref of a non-Ref is an error", func(t *testing.T) {
		wantErr(t, "fn f() {\n  x := deref(7)\n}", "deref expects a Ref")
	})
	t.Run("del of an undefined name is an error", func(t *testing.T) {
		wantErr(t, "fn f() {\n  del gone\n}", "undefined name")
	})
	t.Run("del of a Ref binding checks clean", func(t *testing.T) {
		bindType(t, "fn f() {\n  r := Ref(7)\n  del r\n}")
	})
}
