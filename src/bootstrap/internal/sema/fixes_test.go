package sema

import "testing"

// These tests lock the Iteration-3 review fixes (M1–M4, MINOR, C1, C2).

// TestVariantPatternValidation covers M1: a variant pattern is validated against
// the subject enum — the variant must belong to it and its payload arity must
// match.
func TestVariantPatternValidation(t *testing.T) {
	const enums = "enum Color {\n  Red\n  Green\n  Blue\n}\nenum Shape {\n  Circle\n  Square\n}\n"
	const maybe = "enum Maybe {\n  None\n  Some(int)\n}\n"

	t.Run("a foreign enum's variant is rejected", func(t *testing.T) {
		wantErr(t, enums+"fn f(c: Color) -> int {\n  return match c {\n    Shape.Circle => 0\n    _ => 1\n  }\n}",
			"has no variant \"Circle\"")
	})
	t.Run("a variant pattern on a non-enum subject is rejected", func(t *testing.T) {
		wantErr(t, enums+"fn f(n: int) -> int {\n  return match n {\n    Color.Red => 0\n    _ => 1\n  }\n}",
			"cannot match a subject of type int")
	})
	t.Run("a payload variant in nullary form is rejected", func(t *testing.T) {
		wantErr(t, maybe+"fn f(m: Maybe) -> int {\n  return match m {\n    Maybe.None => 0\n    Maybe.Some => 1\n  }\n}",
			"requires a payload")
	})
	t.Run("a payload arity mismatch is rejected", func(t *testing.T) {
		wantErr(t, maybe+"fn f(m: Maybe) -> int {\n  return match m {\n    Maybe.None => 0\n    Some(a, b) => a\n  }\n}",
			"expects 1 payload value")
	})
	t.Run("a well-formed payload variant is accepted", func(t *testing.T) {
		wantOK(t, maybe+"fn f(m: Maybe) -> int {\n  return match m {\n    Maybe.None => 0\n    Some(a) => a\n  }\n}")
	})
}

// TestImmutableIndexAssignment covers M2: an index into an immutable base is not a
// mutable place.
func TestImmutableIndexAssignment(t *testing.T) {
	wantErr(t, "fn f(xs: list[int]) {\n  xs[0] = 5\n}", "cannot assign to immutable")

	t.Run("a mutable container element is assignable", func(t *testing.T) {
		wantOK(t, "fn f() {\n  mut xs := [1, 2, 3]\n  xs[0] = 5\n}")
	})
}

// TestGenericCallArgChecks covers M3: a generic call reports extra positional and
// unknown named arguments, matching the non-generic path.
func TestGenericCallArgChecks(t *testing.T) {
	const id = "fn id[T](x: T) -> T {\n  return x\n}\n"

	t.Run("too many arguments", func(t *testing.T) {
		wantErr(t, id+"fn f() {\n  print id(1, 2)\n}", "too many arguments")
	})
	t.Run("an unknown argument name", func(t *testing.T) {
		wantErr(t, id+"fn f() {\n  print id(bogus: 2)\n}", "unknown argument")
	})
}

// TestNegatedLiteralContext covers M4: a negated numeric literal adopts a numeric
// context type through the sign.
func TestNegatedLiteralContext(t *testing.T) {
	wantOK(t, "fn f() {\n  a: i8 = -1\n  b: i32 = -5\n  c: f32 = -1.5\n  d: float = -2\n  print a\n}")
}

// TestFixedWidthOverflow covers MINOR: an integer literal outside a fixed-width
// type's range is a compile-time overflow.
func TestFixedWidthOverflow(t *testing.T) {
	wantErr(t, "fn f() {\n  x: u8 = 999\n}", "overflows")

	t.Run("a negative literal below the minimum overflows", func(t *testing.T) {
		wantErr(t, "fn f() {\n  x: i8 = -200\n}", "overflows")
	})
	t.Run("in-range literals are accepted", func(t *testing.T) {
		wantOK(t, "fn f() {\n  x: u8 = 200\n  y: i8 = -128\n  print x\n}")
	})
}

// TestEnumConstruction covers C1: an enum variant is usable as a value — a nullary
// variant names a value of its enum, a payload variant constructs by call.
func TestEnumConstruction(t *testing.T) {
	const color = "enum Color {\n  Red\n  Green\n  Blue\n}\n"
	const maybe = "enum Maybe {\n  None\n  Some(int)\n}\n"

	t.Run("a nullary variant is a value of its enum", func(t *testing.T) {
		if ty := bindType(t, color+"fn f() {\n  c := Red\n}"); ty.String() != "Color" {
			t.Fatalf("c := Red has type %s, want Color", ty)
		}
	})
	t.Run("a nullary variant matches its declared enum return", func(t *testing.T) {
		wantOK(t, color+"fn f() -> Color {\n  return Red\n}")
	})
	t.Run("a payload variant constructs by call", func(t *testing.T) {
		wantOK(t, maybe+"fn f() -> Maybe {\n  return Some(3)\n}")
	})
	t.Run("a payload variant checks its argument type", func(t *testing.T) {
		wantErr(t, maybe+"fn f() -> Maybe {\n  return Some(true)\n}", "cannot use bool as int")
	})
	t.Run("a nullary variant is not callable", func(t *testing.T) {
		wantErr(t, color+"fn f() -> Color {\n  return Red()\n}", "nullary")
	})
	t.Run("a payload variant used bare needs its payload", func(t *testing.T) {
		wantErr(t, maybe+"fn f() {\n  c := Some\n}", "requires a payload")
	})
}

// TestResultTypeAndTry covers C2: Either and Result are nameable types, so a
// function can declare them and the null-safety operators type against them.
func TestResultTypeAndTry(t *testing.T) {
	t.Run("a function may return Result and use '?'", func(t *testing.T) {
		wantOK(t, "fn f(x: int?) -> Result[int] {\n  return guard {\n    x?\n  }\n}")
	})
	t.Run("'?' unwraps a Result value", func(t *testing.T) {
		wantOK(t, "fn g(r: Result[int]) -> Result[int] {\n  n := r?\n  return r\n}")
	})
	t.Run("'!' unwraps a Result value", func(t *testing.T) {
		wantOK(t, "fn g(r: Result[int]) -> int {\n  return r!\n}")
	})
	t.Run("Either is nameable", func(t *testing.T) {
		wantOK(t, "fn f(e: Either[int, str]) -> int {\n  return e!\n}")
	})
}
