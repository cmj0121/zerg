package parser

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

func parseOK(t *testing.T, src string) *ast.File {
	t.Helper()
	file, diags := Parse(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics for %q: %v", src, diags)
	}
	return file
}

func onlyFunc(t *testing.T, src string) *ast.FuncDecl {
	t.Helper()
	file := parseOK(t, src)
	if len(file.Decls) != 1 {
		t.Fatalf("got %d decls, want 1", len(file.Decls))
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("decl is %T, want *ast.FuncDecl", file.Decls[0])
	}
	return fn
}

func TestEmptyFunc(t *testing.T) {
	fn := onlyFunc(t, "fn main() { nop }")
	if fn.Name != "main" || len(fn.Params) != 0 || fn.Ret != nil {
		t.Fatalf("unexpected signature: %+v", fn)
	}
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("got %d stmts, want 1", len(fn.Body.Stmts))
	}
	if _, ok := fn.Body.Stmts[0].(*ast.NopStmt); !ok {
		t.Fatalf("stmt is %T, want *ast.NopStmt", fn.Body.Stmts[0])
	}
}

func TestSignature(t *testing.T) {
	fn := onlyFunc(t, "fn add(a: int, b: int) -> int { return a + b }")
	if len(fn.Params) != 2 || fn.Params[0].Name != "a" || fn.Params[1].Type.Name != "int" {
		t.Fatalf("unexpected params: %+v", fn.Params)
	}
	if fn.Ret == nil || fn.Ret.Name != "int" {
		t.Fatalf("unexpected return: %+v", fn.Ret)
	}
}

func TestBindings(t *testing.T) {
	fn := onlyFunc(t, "fn f() {\n  x := 1\n  mut y := 2\n  z: int = 3\n  const w := 4\n}")
	want := []struct {
		mut, konst bool
		name       string
		typed      bool
	}{
		{false, false, "x", false},
		{true, false, "y", false},
		{false, false, "z", true},
		{false, true, "w", false},
	}
	if len(fn.Body.Stmts) != len(want) {
		t.Fatalf("got %d stmts, want %d", len(fn.Body.Stmts), len(want))
	}
	for i, w := range want {
		b, ok := fn.Body.Stmts[i].(*ast.BindStmt)
		if !ok {
			t.Fatalf("stmt %d is %T, want *ast.BindStmt", i, fn.Body.Stmts[i])
		}
		if b.Mut != w.mut || b.Const != w.konst || b.Name != w.name || (b.Type != nil) != w.typed {
			t.Fatalf("stmt %d = %+v, want %+v", i, b, w)
		}
	}
}

func TestReassignVsBind(t *testing.T) {
	fn := onlyFunc(t, "fn f() {\n  mut x := 0\n  x = 1\n}")
	if _, ok := fn.Body.Stmts[1].(*ast.AssignStmt); !ok {
		t.Fatalf("stmt 1 is %T, want *ast.AssignStmt", fn.Body.Stmts[1])
	}
}

// TestPrecedence checks the expression ladder shape: '1 + 2 * 3' parses as
// '1 + (2 * 3)', and comparison sits below add.
func TestPrecedence(t *testing.T) {
	fn := onlyFunc(t, "fn f() { print 1 + 2 * 3 < 10 }")
	ps := fn.Body.Stmts[0].(*ast.PrintStmt)
	cmp, ok := ps.Value.(*ast.Binary)
	if !ok || cmp.Op != token.Lt {
		t.Fatalf("top is %+v, want a '<' comparison", ps.Value)
	}
	add, ok := cmp.L.(*ast.Binary)
	if !ok || add.Op != token.Plus {
		t.Fatalf("left is %+v, want a '+'", cmp.L)
	}
	mul, ok := add.R.(*ast.Binary)
	if !ok || mul.Op != token.Star {
		t.Fatalf("add.R is %+v, want a '*'", add.R)
	}
}

func TestUnaryAndCall(t *testing.T) {
	fn := onlyFunc(t, "fn f() { print -g(1, 2) }")
	ps := fn.Body.Stmts[0].(*ast.PrintStmt)
	un, ok := ps.Value.(*ast.Unary)
	if !ok || un.Op != token.Minus {
		t.Fatalf("want unary minus, got %+v", ps.Value)
	}
	call, ok := un.X.(*ast.Call)
	if !ok || len(call.Args) != 2 {
		t.Fatalf("want a 2-arg call, got %+v", un.X)
	}
}

func TestIfElseChain(t *testing.T) {
	fn := onlyFunc(t, "fn f(n: int) {\n  if n < 0 { print 1 } else if n == 0 { print 2 } else { print 3 }\n}")
	ifs, ok := fn.Body.Stmts[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.IfStmt", fn.Body.Stmts[0])
	}
	if len(ifs.Branches) != 2 || ifs.Else == nil {
		t.Fatalf("got %d branches, else=%v", len(ifs.Branches), ifs.Else != nil)
	}
}

func TestForForms(t *testing.T) {
	fn := onlyFunc(t, "fn f() {\n  for { break }\n  for true { continue }\n}")
	inf, ok := fn.Body.Stmts[0].(*ast.ForStmt)
	if !ok || inf.Cond != nil {
		t.Fatalf("stmt 0 should be an infinite for, got %+v", fn.Body.Stmts[0])
	}
	whileFor, ok := fn.Body.Stmts[1].(*ast.ForStmt)
	if !ok || whileFor.Cond == nil {
		t.Fatalf("stmt 1 should be a while-for, got %+v", fn.Body.Stmts[1])
	}
}

func TestSemicolonSeparator(t *testing.T) {
	// ';' separates statements just like a newline.
	fn := onlyFunc(t, "fn f() { x := 1; y := 2 }")
	if len(fn.Body.Stmts) != 2 {
		t.Fatalf("got %d stmts, want 2", len(fn.Body.Stmts))
	}
}

func TestReturnIf(t *testing.T) {
	// 'return e if c' carries both a value and a condition.
	fn := onlyFunc(t, "fn f(a: int, b: int) -> int {\n  return a if a > b\n  return b\n}")
	ret, ok := fn.Body.Stmts[0].(*ast.ReturnStmt)
	if !ok || ret.Value == nil || ret.Cond == nil {
		t.Fatalf("stmt 0 = %+v, want a conditional return with a value", fn.Body.Stmts[0])
	}
	// the fall-through return is unconditional.
	if tail := fn.Body.Stmts[1].(*ast.ReturnStmt); tail.Cond != nil {
		t.Fatalf("stmt 1 should be unconditional")
	}
	// 'return if c' is a bare conditional early exit.
	fn2 := onlyFunc(t, "fn g(n: int) {\n  return if n < 0\n  nop\n}")
	ret2 := fn2.Body.Stmts[0].(*ast.ReturnStmt)
	if ret2.Value != nil || ret2.Cond == nil {
		t.Fatalf("stmt 0 = %+v, want a bare conditional return", ret2)
	}
}

func TestErrorRecovery(t *testing.T) {
	// a bad statement is reported but the following function still parses.
	file, diags := Parse("fn a() { x := }\nfn b() { nop }")
	if len(diags) == 0 {
		t.Fatalf("expected a diagnostic for the empty binding RHS")
	}
	if len(file.Decls) != 2 {
		t.Fatalf("got %d decls, want 2 (recovery should keep fn b)", len(file.Decls))
	}
}
