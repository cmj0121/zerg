package sema

import "testing"

const colorEnum = "enum Color {\n  Red\n  Green\n  Blue\n}\n"

// TestEnumExhaustive covers coverage over an enum subject (DESIGN-1b §5.1):
// matching every variant is exhaustive without a catch-all, a catch-all covers
// the rest, and a missing variant is reported by name.
func TestEnumExhaustive(t *testing.T) {
	t.Run("every variant is exhaustive", func(t *testing.T) {
		wantOK(t, colorEnum+"fn f(c: Color) -> int {\n  return match c {\n"+
			"    Red => 0\n    Green => 1\n    Blue => 2\n  }\n}")
	})
	t.Run("a catch-all covers the rest", func(t *testing.T) {
		wantOK(t, colorEnum+"fn f(c: Color) -> int {\n  return match c {\n"+
			"    Red => 0\n    _ => 1\n  }\n}")
	})
	t.Run("a missing variant is reported", func(t *testing.T) {
		wantErr(t, colorEnum+"fn f(c: Color) -> int {\n  return match c {\n"+
			"    Red => 0\n    Green => 1\n  }\n}", "missing variant Color.Blue")
	})
}

// TestGuardDoesNotCount confirms a guarded arm never counts toward coverage: a
// guard on the arm that would complete an enum leaves the match non-exhaustive
// (DESIGN-1b §5.2).
func TestGuardDoesNotCount(t *testing.T) {
	wantErr(t, colorEnum+"fn f(c: Color) -> int {\n  return match c {\n"+
		"    Red => 0\n    Green => 1\n    Blue if true => 2\n  }\n}", "missing variant Color.Blue")
}

// TestBoolExhaustive covers a bool subject: both truth values (or a catch-all)
// are required (DESIGN-1b §5.1).
func TestBoolExhaustive(t *testing.T) {
	t.Run("true and false are exhaustive", func(t *testing.T) {
		wantOK(t, "fn f(b: bool) -> int {\n  return match b {\n    true => 1\n    false => 0\n  }\n}")
	})
	t.Run("a missing truth value is reported", func(t *testing.T) {
		wantErr(t, "fn f(b: bool) -> int {\n  return match b {\n    true => 1\n  }\n}", "'false' case")
	})
}

// TestIntegerNeedsCatchAll confirms the conservative FORK-5 rule: an integer
// subject always needs a catch-all — range arms never prove exhaustiveness
// (DESIGN-1b §5.3).
func TestIntegerNeedsCatchAll(t *testing.T) {
	t.Run("a catch-all is exhaustive", func(t *testing.T) {
		wantOK(t, "fn f(n: int) -> int {\n  return match n {\n    0 => 0\n    _ => 1\n  }\n}")
	})
	t.Run("range arms do not prove exhaustiveness", func(t *testing.T) {
		wantErr(t, "fn f(n: int) -> int {\n  return match n {\n    0..10 => 0\n    10.. => 1\n  }\n}",
			"catch-all")
	})
}

// TestUnreachableAfterCatchAll reports an arm placed after an unguarded catch-all
// (DESIGN-1b §5.4).
func TestUnreachableAfterCatchAll(t *testing.T) {
	wantErr(t, "fn f(n: int) -> int {\n  return match n {\n    _ => 0\n    1 => 1\n  }\n}", "unreachable")
}
