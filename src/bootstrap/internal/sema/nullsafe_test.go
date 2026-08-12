package sema

import "testing"

// TestTryOperator types the propagate postfix 'x?' (DESIGN-1b §6): it unwraps an
// optional's value and requires the enclosing function to return an optional.
func TestTryOperator(t *testing.T) {
	t.Run("unwraps in an optional-returning function", func(t *testing.T) {
		wantOK(t, "fn f(x: int?) -> int? {\n  n := x?\n  return x\n}")
	})
	t.Run("requires an optional operand", func(t *testing.T) {
		wantErr(t, "fn f(x: int) -> int? {\n  n := x?\n  return x\n}", "'?' requires an Either")
	})
	t.Run("requires an optional-returning function", func(t *testing.T) {
		wantErr(t, "fn f(x: int?) -> int {\n  return x?\n}", "can only be used in a function returning")
	})
}

// TestCoalesceOperator types the default operator 'a ?? b' (DESIGN-1b §6): it
// yields the left operand's value type and checks the default against it — unless
// the default diverges.
func TestCoalesceOperator(t *testing.T) {
	t.Run("yields the element type", func(t *testing.T) {
		wantOK(t, "fn f(x: int?) -> int {\n  return x ?? 0\n}")
	})
	t.Run("a diverging default is allowed", func(t *testing.T) {
		wantOK(t, "fn f(x: int?) -> int {\n  y := x ?? return 0\n  return y\n}")
	})
	t.Run("a mismatched default is an error", func(t *testing.T) {
		wantErr(t, "fn f(x: int?) -> int {\n  return x ?? true\n}", "cannot use bool as int")
	})
	t.Run("requires an optional left operand", func(t *testing.T) {
		wantErr(t, "fn f(x: int) -> int {\n  return x ?? 0\n}", "'??' requires an optional")
	})
}

// TestOptChainOperator types the optional-chain postfix 'a?.b' (DESIGN-1b §6): a
// must be optional and the result is the field type made optional.
func TestOptChainOperator(t *testing.T) {
	const point = "struct Point {\n  pub x: int\n}\n"
	t.Run("reads a field through an optional", func(t *testing.T) {
		wantOK(t, point+"fn f(p: Point?) -> int? {\n  return p?.x\n}")
	})
	t.Run("requires an optional receiver", func(t *testing.T) {
		wantErr(t, point+"fn f(p: Point) -> int {\n  return p?.x\n}", "requires an optional value")
	})
}

// TestForceOperator types the force-unwrap postfix 'x!' (DESIGN-1b §6): it unwraps
// an optional's value with no constraint on the enclosing function.
func TestForceOperator(t *testing.T) {
	t.Run("unwraps an optional", func(t *testing.T) {
		wantOK(t, "fn f(x: int?) -> int {\n  return x!\n}")
	})
	t.Run("requires an optional operand", func(t *testing.T) {
		wantErr(t, "fn f(x: int) -> int {\n  return x!\n}", "'!' requires an Either")
	})
}

// TestGuardExpr types the guard expression 'guard { block }' as Result[T] over the
// block's value type (DESIGN-1b §6).
func TestGuardExpr(t *testing.T) {
	ty := bindType(t, "fn f() {\n  g := guard {\n    1\n  }\n}")
	if got := ty.String(); got != "Either[int, Err]" {
		t.Fatalf("guard { 1 } has type %s, want Either[int, Err]", got)
	}
}

// TestRaiseDiverges accepts a 'raise' statement: it aborts, so it needs no value
// context (DESIGN-1b §6).
//
// The operand used to be an `int`, chosen when `raise` accepted anything at all — which is
// the fact TestRaiseOperand below now pins separately. Divergence is what THIS test is about,
// and it is about it with an operand `raise` actually carries.
func TestRaiseDiverges(t *testing.T) {
	wantOK(t, "fn f(x: str) {\n  raise x\n}")
}

// TestRaiseOperand checks what `raise` carries: an Err, or a message to build one from
// (docs/code/errors.md). Anything else was handed to the runtime's `zrt_err_new`, whose
// parameter is a `const char *`, so `raise 5` reached cc as an incompatible-type argument.
func TestRaiseOperand(t *testing.T) {
	wantOK(t, "fn f() {\n  raise \"boom\"\n}")
	wantOK(t, "fn f() {\n  raise ValueError(\"bad\")\n}")
	wantErr(t, "fn f() {\n  raise 5\n}", "raise carries an `Err`")
	wantErr(t, "struct P {\n  pub a: int\n}\nfn f() {\n  raise P(1)\n}", "raise carries an `Err`")
}

// TestResultOkWidening checks the context-typed Ok/Left widening (Phase 1f U0): a T
// value is accepted where a Result[T] / T? is expected, and `nil` fills a Result[nil]
// or a T? — the fix for the earlier "cannot return nil from Result[nil]" rejection.
func TestResultOkWidening(t *testing.T) {
	wantOK(t, "fn f() -> Result[int] {\n  return 5\n}")
	wantOK(t, "fn f() -> Result[nil] {\n  return nil\n}")
	wantOK(t, "fn f() -> int? {\n  return 5\n}")
	wantOK(t, "fn f() -> int? {\n  return nil\n}")
	wantOK(t, "fn f() -> Result[int] {\n  x: Result[int] = 7\n  return x\n}")
	// a genuinely wrong Left type is still rejected.
	wantErr(t, "fn f() -> Result[int] {\n  return \"s\"\n}", "cannot return")
}
