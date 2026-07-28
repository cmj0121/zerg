package sema

import "testing"

// TestEnumIntConversion covers `int(v)` reading a C-style enum's discriminant
// (docs/core/types.md, GRAMMAR group 7): a payload-free enum has one, a payload enum does
// not, and `int(v)` targets int, never another scalar.
func TestEnumIntConversion(t *testing.T) {
	const cstyle = "enum Color {\n  Red = 1\n  Green\n  Blue = 10\n}\n"
	t.Run("reads a C-style discriminant", func(t *testing.T) {
		wantOK(t, cstyle+"fn f(c: Color) -> int {\n  return int(c)\n}")
	})
	t.Run("reads a qualified variant value", func(t *testing.T) {
		wantOK(t, cstyle+"fn f() -> int {\n  return int(Color.Green)\n}")
	})
	t.Run("a payload enum has no readable discriminant", func(t *testing.T) {
		wantErr(t, "enum Shape {\n  Circle(int)\n  Square(int)\n}\n"+
			"fn f(s: Shape) -> int {\n  return int(s)\n}", "only a payload-free")
	})
	t.Run("an enum discriminant reads only as int", func(t *testing.T) {
		wantErr(t, cstyle+"fn f(c: Color) -> byte {\n  return byte(c)\n}", "reads as int")
	})
}

// TestEnumOfReverse covers `E.of(n) -> E?` — the discriminant reverse (docs/core/types.md):
// the enum name is a value namespace exposing `of`, which yields an optional of the
// enum, and it needs a payload-free enum with discriminants.
func TestEnumOfReverse(t *testing.T) {
	const cstyle = "enum Color {\n  Red = 1\n  Green\n  Blue = 10\n}\n"
	t.Run("yields an optional of the enum", func(t *testing.T) {
		wantOK(t, cstyle+"fn f() -> Color? {\n  return Color.of(2)\n}")
	})
	t.Run("takes an int", func(t *testing.T) {
		wantErr(t, cstyle+"fn f() -> Color? {\n  return Color.of(\"x\")\n}", "cannot use")
	})
	t.Run("needs a C-style enum", func(t *testing.T) {
		wantErr(t, "enum Shape {\n  Circle(int)\n  Square(int)\n}\n"+
			"fn f() -> Shape? {\n  return Shape.of(1)\n}", "payload-free")
	})
	t.Run("an unknown member is reported", func(t *testing.T) {
		wantErr(t, cstyle+"fn f() -> int {\n  return Color.nope(1)\n}", "no variant or member")
	})
}

// TestEnumQualifiedVariant covers the enum name as a value namespace for a variant:
// `Color.Green` is the nullary variant value and `E.Variant(...)` a qualified
// constructor (the mirror of the bare forms).
func TestEnumQualifiedVariant(t *testing.T) {
	const cstyle = "enum Color {\n  Red = 1\n  Green\n  Blue = 10\n}\n"
	t.Run("a nullary variant is a value", func(t *testing.T) {
		wantOK(t, cstyle+"fn f() -> Color {\n  return Color.Blue\n}")
	})
	t.Run("a qualified payload variant constructs", func(t *testing.T) {
		wantOK(t, "enum Shape {\n  Circle(int)\n  Square(int)\n}\n"+
			"fn f() -> Shape {\n  return Shape.Circle(3)\n}")
	})
	t.Run("an unknown qualified variant is reported", func(t *testing.T) {
		wantErr(t, cstyle+"fn f() -> Color {\n  return Color.Purple\n}", "no variant")
	})
}
