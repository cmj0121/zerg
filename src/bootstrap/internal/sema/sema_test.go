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
