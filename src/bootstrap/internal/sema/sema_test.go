package sema

import (
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
)

// check parses and type-checks src, returning the diagnostics.
func check(t *testing.T, src string) []string {
	t.Helper()
	file, diags := parser.Parse(src)
	if len(diags) != 0 {
		t.Fatalf("parse errors for %q: %v", src, diags)
	}
	_, sdiags := Check(file)
	msgs := make([]string, len(sdiags))
	for i, d := range sdiags {
		msgs[i] = d.Msg
	}
	return msgs
}

func wantOK(t *testing.T, src string) {
	t.Helper()
	if msgs := check(t, src); len(msgs) != 0 {
		t.Fatalf("unexpected sema errors for %q: %v", src, msgs)
	}
}

func wantErr(t *testing.T, src, substr string) {
	t.Helper()
	msgs := check(t, src)
	for _, m := range msgs {
		if strings.Contains(m, substr) {
			return
		}
	}
	t.Fatalf("expected an error containing %q for %q, got %v", substr, src, msgs)
}

func TestValidProgram(t *testing.T) {
	wantOK(t, `
fn add(a: int, b: int) -> int { return a + b }
fn main() {
  x := add(1, 2)
  mut y := x * 3
  y = y - 1
  print y
  if y > 0 { print y } else { print 0 }
  for y > 0 { y = y - 1 }
}`)
}

func TestFloatLiteralCoercion(t *testing.T) {
	wantOK(t, "fn f() {\n  x: float = 1\n  y := x + 2\n  print y\n}")
}

func TestShadowParam(t *testing.T) {
	// 'mut n := n' shadows the by-value parameter in the body scope.
	wantOK(t, "fn countdown(n: int) {\n  mut n := n\n  for n > 0 { n = n - 1 }\n}")
}

func TestUndefinedName(t *testing.T) {
	wantErr(t, "fn f() { print x }", `undefined name "x"`)
}

func TestAssignImmutable(t *testing.T) {
	wantErr(t, "fn f() {\n  x := 1\n  x = 2\n}", "immutable")
}

func TestConditionMustBeBool(t *testing.T) {
	wantErr(t, "fn f() {\n  if 1 { nop }\n}", "condition must be bool")
}

func TestTypeMismatchArith(t *testing.T) {
	wantErr(t, "fn f() {\n  x := 1\n  y := 2.0\n  print x + y\n}", "matching numeric operands")
}

func TestCallArity(t *testing.T) {
	wantErr(t, "fn g(a: int) -> int { return a }\nfn f() { print g(1, 2) }", "expects 1 argument")
}

func TestCallArgType(t *testing.T) {
	wantErr(t, `fn g(a: int) -> int { return a }`+"\nfn f() { print g(true) }", "cannot use bool as int")
}

func TestReturnIfGuard(t *testing.T) {
	wantOK(t, "fn max(a: int, b: int) -> int {\n  return a if a > b\n  return b\n}")
	// the guard condition must be bool
	wantErr(t, "fn f(n: int) -> int {\n  return 0 if n\n  return 1\n}", "condition must be bool")
}

func TestMatchOK(t *testing.T) {
	wantOK(t, "fn sign(n: int) -> int {\n  return match n {\n"+
		"    0 => 0\n    x if x < 0 => -1\n    _ => 1\n  }\n}")
}

func TestMatchExhaustive(t *testing.T) {
	wantErr(t, "fn f(n: int) -> int {\n  return match n {\n    0 => 1\n    1 => 2\n  }\n}",
		"non-exhaustive")
}

func TestMatchArmTypesMustAgree(t *testing.T) {
	wantErr(t, "fn f(n: int) -> str {\n  return match n {\n    0 => 1\n    _ => \"x\"\n  }\n}",
		"same type")
}

func TestMatchGuardMustBeBool(t *testing.T) {
	wantErr(t, "fn f(n: int) -> int {\n  return match n {\n    x if x => 1\n    _ => 0\n  }\n}",
		"condition must be bool")
}

// TestMatchSubjectAnyExpr: the semantic core lifts the Phase-0 "name or literal"
// subject restriction — any expression may be a match subject (DESIGN-1b §5).
func TestMatchSubjectAnyExpr(t *testing.T) {
	wantOK(t, "fn g() -> int { return 5 }\nfn f() -> int {\n  return match g() {\n    _ => 1\n  }\n}")
}

func TestReturnMismatch(t *testing.T) {
	wantErr(t, "fn f() -> int { return true }", "cannot return bool")
	wantErr(t, "fn f() { return 1 }", "unexpected return value")
}

func TestPrintNil(t *testing.T) {
	wantErr(t, "fn f() { print nil }", "cannot print")
}

func TestBreakOutsideLoop(t *testing.T) {
	wantErr(t, "fn f() { break }", "break outside of a loop")
}

func TestDuplicateFunction(t *testing.T) {
	wantErr(t, "fn f() { nop }\nfn f() { nop }", "already declared")
}

func TestOperators(t *testing.T) {
	wantOK(t, "fn f() {\n"+
		"  print ~5\n"+
		"  print 1 & 2 | 3 ^ 4\n"+
		"  print 8 << 1 >> 2\n"+
		"  print true and false or not false\n"+
		"  print 1 < 2\n"+
		"  print \"a\" == \"b\"\n"+
		"  print true != false\n"+
		"  print -3\n"+
		"  print -2.0\n"+
		"}")
}

func TestUnaryErrors(t *testing.T) {
	wantErr(t, "fn f() { print not 1 }", "requires a bool")
	wantErr(t, "fn f() { print -true }", "requires a numeric")
	wantErr(t, "fn f() { print ~true }", "requires an int")
}

func TestBinaryErrors(t *testing.T) {
	wantErr(t, "fn f() { print 1 & true }", "requires int operands")
	wantErr(t, "fn f() { print true and 1 }", "requires bool operands")
	wantErr(t, "fn f() { print 1 == true }", "cannot compare")
	wantErr(t, "fn f() { print true < false }", "matching numeric")
}

func TestUnknownType(t *testing.T) {
	wantErr(t, "fn f(x: Widget) { nop }", "unknown type")
	wantErr(t, "fn f() {\n  x: Widget = 1\n}", "unknown type")
}

func TestUndefinedFunction(t *testing.T) {
	wantErr(t, "fn f() { g() }", "undefined function")
}

func TestFunctionNotFirstClass(t *testing.T) {
	wantErr(t, "fn g() { nop }\nfn f() { print g }", "first-class")
}

func TestRedeclareInScope(t *testing.T) {
	wantErr(t, "fn f() {\n  x := 1\n  x := 2\n}", "already declared")
}

func TestAssignTypeMismatch(t *testing.T) {
	wantErr(t, "fn f() {\n  mut x := 1\n  x = true\n}", "cannot assign")
}

func TestMatchPatternTypeAndCoercion(t *testing.T) {
	// a str literal cannot match an int subject
	wantErr(t, "fn f(n: int) -> int {\n  return match n {\n    \"s\" => 1\n    _ => 2\n  }\n}", "cannot match")
	// an int literal pattern may match a float subject
	wantOK(t, "fn f(x: float) -> int {\n  return match x {\n    0 => 1\n    _ => 2\n  }\n}")
	// an earlier unguarded catch-all makes later arms unreachable
	wantErr(t, "fn f(n: int) -> int {\n  return match n {\n    _ => 1\n    0 => 2\n  }\n}", "unreachable")
}

func TestExprTypesRecorded(t *testing.T) {
	file, _ := parser.Parse("fn f() {\n  x := 1 + 2\n  print x\n}")
	info, diags := Check(file)
	if len(diags) != 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if len(info.BindTypes) != 1 {
		t.Fatalf("expected 1 binding type, got %d", len(info.BindTypes))
	}
	for _, ty := range info.BindTypes {
		if ty != Int {
			t.Fatalf("binding type = %s, want int", ty)
		}
	}
}

// TestModuleConstForwardRefTypes guards Phase 1g S3: a module constant that
// forward-references a later constant infers the real type (int), not void, and its
// ConstOrder places the dependency first.
func TestModuleConstForwardRefTypes(t *testing.T) {
	file, pdiags := parser.Parse("a := b + 1\nb := 10\nfn main() {\n\tprint a\n\tprint b\n}\n")
	if len(pdiags) != 0 {
		t.Fatalf("parse errors: %v", pdiags)
	}
	info, diags := Check(file)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	// Both constants must infer int (not Unknown/void) despite the forward reference.
	if len(info.BindTypes) != 2 {
		t.Fatalf("expected 2 module-constant binding types, got %d", len(info.BindTypes))
	}
	for b, ty := range info.BindTypes {
		if ty != Int {
			t.Fatalf("module constant %q typed as %s, want int", b.Name, ty)
		}
	}
	// The dependency (b) must come before its dependent (a) in ConstOrder.
	if len(info.ConstOrder) != 2 {
		t.Fatalf("ConstOrder = %d entries, want 2", len(info.ConstOrder))
	}
	if info.ConstOrder[0].Name != "b" || info.ConstOrder[1].Name != "a" {
		t.Fatalf("ConstOrder = [%s, %s], want [b, a]", info.ConstOrder[0].Name, info.ConstOrder[1].Name)
	}
}

// TestModuleConstCycleDiagnostic guards that a constant dependency cycle is a clean
// span-anchored sema error rather than a leaked void.
func TestModuleConstCycleDiagnostic(t *testing.T) {
	msgs := check(t, "p := q + 1\nq := p + 1\nfn main() {\n\tprint p\n}\n")
	found := false
	for _, m := range msgs {
		if strings.Contains(m, "module-constant cycle") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a 'module-constant cycle' diagnostic, got: %v", msgs)
	}
}
