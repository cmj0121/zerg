package syntax

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers — every test in this file goes through Lex+Parse on a one-program
// source string and then asserts on the AST shape. The corpus here is the
// parser-only checklist; broader v0.1 corpus parity tests live in test/.
// ---------------------------------------------------------------------------

// parseProgramSrc is the standard "lex this string, parse it, fail the test
// on either error". The trailing newline keeps callers from having to worry
// about whether the lexer wants one (it doesn't, but the realistic cases all
// have one).
func parseProgramSrc(t *testing.T, src string) *Program {
	t.Helper()
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex(%q): %v", src, err)
	}
	prog, err := Parse(tokens)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return prog
}

// expectParseErr asserts that Parse fails and that the error message contains
// the expected substring. It returns the error message for further checks.
func expectParseErr(t *testing.T, src, wantSubstr string) string {
	t.Helper()
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex(%q): %v", src, err)
	}
	_, err = Parse(tokens)
	if err == nil {
		t.Fatalf("Parse(%q) succeeded, expected error containing %q", src, wantSubstr)
	}
	if _, ok := err.(*ParseError); !ok {
		t.Errorf("error is %T, want *ParseError: %v", err, err)
	}
	if wantSubstr != "" && !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("error %q does not contain %q", err.Error(), wantSubstr)
	}
	return err.Error()
}

// expectOne asserts the program has exactly one statement and returns it
// already typed. Tests that exercise multi-statement programs use
// `prog.Statements` directly.
func expectOne[T Stmt](t *testing.T, prog *Program) T {
	t.Helper()
	if len(prog.Statements) != 1 {
		t.Fatalf("got %d statements, want 1: %#v", len(prog.Statements), prog.Statements)
	}
	stmt, ok := prog.Statements[0].(T)
	if !ok {
		var zero T
		t.Fatalf("statement 0 is %T, want %T", prog.Statements[0], zero)
	}
	return stmt
}

// ---------------------------------------------------------------------------
// Statement forms.
// ---------------------------------------------------------------------------

func TestParseLetWalrus(t *testing.T) {
	prog := parseProgramSrc(t, "let x := 42\n")
	s := expectOne[*LetStmt](t, prog)
	if s.Name != "x" {
		t.Errorf("name = %q, want %q", s.Name, "x")
	}
	if s.Type != nil {
		t.Errorf("type ref = %v, want nil (inferred)", s.Type)
	}
	if lit, ok := s.Value.(*IntLit); !ok || lit.Text != "42" {
		t.Errorf("value = %#v, want IntLit{Text:42}", s.Value)
	}
}

func TestParseLetAnnotated(t *testing.T) {
	prog := parseProgramSrc(t, "let x: int = 0xff\n")
	s := expectOne[*LetStmt](t, prog)
	if s.Type == nil || s.Type.Name != "int" {
		t.Errorf("type ref = %#v, want int", s.Type)
	}
	if lit, ok := s.Value.(*IntLit); !ok || lit.Text != "0xff" {
		t.Errorf("value = %#v, want IntLit{Text:0xff}", s.Value)
	}
}

func TestParseMutAndConst(t *testing.T) {
	prog := parseProgramSrc(t, "mut a := 1\nconst b: int = 2\n")
	if len(prog.Statements) != 2 {
		t.Fatalf("got %d statements, want 2", len(prog.Statements))
	}
	if _, ok := prog.Statements[0].(*MutStmt); !ok {
		t.Errorf("statement 0 is %T, want *MutStmt", prog.Statements[0])
	}
	if _, ok := prog.Statements[1].(*ConstStmt); !ok {
		t.Errorf("statement 1 is %T, want *ConstStmt", prog.Statements[1])
	}
}

func TestParseAssignAllOps(t *testing.T) {
	cases := []struct {
		src  string
		want AssignOp
	}{
		{"x = 1", AssignSet},
		{"x += 1", AssignAdd},
		{"x -= 1", AssignSub},
		{"x *= 1", AssignMul},
		{"x /= 1", AssignDiv},
		{"x %= 1", AssignMod},
		{"x &= 1", AssignAnd},
		{"x |= 1", AssignOr},
		{"x ^= 1", AssignXor},
		{"x <<= 1", AssignShl},
		{"x >>= 1", AssignShr},
	}
	for _, c := range cases {
		c := c
		t.Run(c.src, func(t *testing.T) {
			prog := parseProgramSrc(t, c.src+"\n")
			s := expectOne[*AssignStmt](t, prog)
			if s.Op != c.want {
				t.Errorf("op = %v, want %v", s.Op, c.want)
			}
			ident, ok := s.Target.(*IdentExpr)
			if !ok {
				t.Fatalf("Target is %T, want *IdentExpr", s.Target)
			}
			if ident.Name != "x" {
				t.Errorf("target = %q, want x", ident.Name)
			}
		})
	}
}

func TestParseAssignNonIdentLHSIsError(t *testing.T) {
	expectParseErr(t, "1 = x\n", "must be an identifier or list[i]")
}

func TestParsePrintOfExpression(t *testing.T) {
	prog := parseProgramSrc(t, "print x + 1\n")
	s := expectOne[*PrintStmt](t, prog)
	bin, ok := s.Expr.(*BinaryExpr)
	if !ok {
		t.Fatalf("PrintStmt.Expr is %T, want *BinaryExpr", s.Expr)
	}
	if bin.Op != BinAdd {
		t.Errorf("op = %v, want BinAdd", bin.Op)
	}
}

func TestParsePrintStringStillWorks(t *testing.T) {
	// v0.0 carry-over: the original `print "..."` shape must continue to
	// parse correctly with the v0.1 generalised PrintStmt.
	prog := parseProgramSrc(t, `print "Hello, Zerg!"`+"\n")
	s := expectOne[*PrintStmt](t, prog)
	lit, ok := s.Expr.(*StringLit)
	if !ok {
		t.Fatalf("PrintStmt.Expr is %T, want *StringLit", s.Expr)
	}
	if lit.Value != "Hello, Zerg!" {
		t.Errorf("value = %q, want %q", lit.Value, "Hello, Zerg!")
	}
}

func TestParseExprStmtMustBeCall(t *testing.T) {
	// A bare expression that's not a call is rejected — v0.1 forbids
	// "expression statements" beyond function calls.
	expectParseErr(t, "1 + 2\n", "must be function calls")
}

func TestParseCallAsStatement(t *testing.T) {
	prog := parseProgramSrc(t, "do_thing(1, 2)\n")
	s := expectOne[*ExprStmt](t, prog)
	call, ok := s.Expr.(*CallExpr)
	if !ok {
		t.Fatalf("ExprStmt.Expr is %T, want *CallExpr", s.Expr)
	}
	if id, ok := call.Callee.(*IdentExpr); !ok || id.Name != "do_thing" {
		t.Errorf("callee = %#v, want IdentExpr{do_thing}", call.Callee)
	}
	if len(call.Args) != 2 {
		t.Errorf("got %d args, want 2", len(call.Args))
	}
}

func TestParseReturnVariants(t *testing.T) {
	cases := []struct {
		src       string
		wantValue bool
		wantGuard bool
	}{
		{"fn f() { return }\n", false, false},
		{"fn f() -> int { return 1 }\n", true, false},
		{"fn f() { return if cond }\n", false, true},
		{"fn f() -> int { return 1 if cond }\n", true, true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.src, func(t *testing.T) {
			prog := parseProgramSrc(t, c.src)
			fn := expectOne[*FnDecl](t, prog)
			if len(fn.Body.Statements) != 1 {
				t.Fatalf("got %d body stmts, want 1", len(fn.Body.Statements))
			}
			rs, ok := fn.Body.Statements[0].(*ReturnStmt)
			if !ok {
				t.Fatalf("body stmt is %T, want *ReturnStmt", fn.Body.Statements[0])
			}
			if (rs.Value != nil) != c.wantValue {
				t.Errorf("value present = %v, want %v", rs.Value != nil, c.wantValue)
			}
			if (rs.Guard != nil) != c.wantGuard {
				t.Errorf("guard present = %v, want %v", rs.Guard != nil, c.wantGuard)
			}
		})
	}
}

func TestParseBreakContinueGuard(t *testing.T) {
	prog := parseProgramSrc(t, "for {\nbreak if x\ncontinue if y\n}\n")
	fs := expectOne[*ForStmt](t, prog)
	if len(fs.Body.Statements) != 2 {
		t.Fatalf("got %d body stmts, want 2", len(fs.Body.Statements))
	}
	bs, ok := fs.Body.Statements[0].(*BreakStmt)
	if !ok || bs.Guard == nil {
		t.Errorf("body[0] = %#v, want BreakStmt with guard", fs.Body.Statements[0])
	}
	cs, ok := fs.Body.Statements[1].(*ContinueStmt)
	if !ok || cs.Guard == nil {
		t.Errorf("body[1] = %#v, want ContinueStmt with guard", fs.Body.Statements[1])
	}
}

func TestParseFnNoReturnType(t *testing.T) {
	prog := parseProgramSrc(t, "fn shout() { print \"hi\" }\n")
	fn := expectOne[*FnDecl](t, prog)
	if fn.Return != nil {
		t.Errorf("Return = %#v, want nil", fn.Return)
	}
	if len(fn.Params) != 0 {
		t.Errorf("got %d params, want 0", len(fn.Params))
	}
}

func TestParseFnWithReturnType(t *testing.T) {
	prog := parseProgramSrc(t, "fn add(a: int, b: int) -> int { return a + b }\n")
	fn := expectOne[*FnDecl](t, prog)
	if fn.Name != "add" {
		t.Errorf("name = %q, want add", fn.Name)
	}
	if len(fn.Params) != 2 {
		t.Fatalf("got %d params, want 2", len(fn.Params))
	}
	for i, want := range []string{"a", "b"} {
		if fn.Params[i].Name != want {
			t.Errorf("param %d name = %q, want %q", i, fn.Params[i].Name, want)
		}
		if fn.Params[i].Type == nil || fn.Params[i].Type.Name != "int" {
			t.Errorf("param %d type = %v, want int", i, fn.Params[i].Type)
		}
	}
	if fn.Return == nil || fn.Return.Name != "int" {
		t.Errorf("Return = %v, want int", fn.Return)
	}
}

func TestParseIfElifElse(t *testing.T) {
	src := "if a { print \"a\" } elif b { print \"b\" } elif c { print \"c\" } else { print \"d\" }\n"
	prog := parseProgramSrc(t, src)
	is := expectOne[*IfStmt](t, prog)
	if len(is.Elifs) != 2 {
		t.Errorf("got %d elifs, want 2", len(is.Elifs))
	}
	if is.Else == nil {
		t.Error("Else = nil, want non-nil")
	}
}

func TestParseIfNoElse(t *testing.T) {
	prog := parseProgramSrc(t, "if a { nop }\n")
	is := expectOne[*IfStmt](t, prog)
	if is.Else != nil {
		t.Errorf("Else = %#v, want nil", is.Else)
	}
	if len(is.Elifs) != 0 {
		t.Errorf("got %d elifs, want 0", len(is.Elifs))
	}
}

func TestParseForInfinite(t *testing.T) {
	prog := parseProgramSrc(t, "for { nop }\n")
	fs := expectOne[*ForStmt](t, prog)
	if fs.Kind != ForInfinite {
		t.Errorf("kind = %v, want ForInfinite", fs.Kind)
	}
}

func TestParseForCond(t *testing.T) {
	prog := parseProgramSrc(t, "for x < 10 { nop }\n")
	fs := expectOne[*ForStmt](t, prog)
	if fs.Kind != ForCond {
		t.Errorf("kind = %v, want ForCond", fs.Kind)
	}
	if fs.Cond == nil {
		t.Error("Cond = nil")
	}
}

func TestParseForRangeHalfOpen(t *testing.T) {
	prog := parseProgramSrc(t, "for i in 0..n { nop }\n")
	fs := expectOne[*ForStmt](t, prog)
	if fs.Kind != ForRange {
		t.Fatalf("kind = %v, want ForRange", fs.Kind)
	}
	if fs.Var != "i" {
		t.Errorf("var = %q, want i", fs.Var)
	}
	if fs.Range == nil || fs.Range.Inclusive {
		t.Errorf("range = %#v, want half-open", fs.Range)
	}
}

func TestParseForRangeInclusive(t *testing.T) {
	prog := parseProgramSrc(t, "for i in 0..=n { nop }\n")
	fs := expectOne[*ForStmt](t, prog)
	if fs.Kind != ForRange {
		t.Fatalf("kind = %v, want ForRange", fs.Kind)
	}
	if fs.Range == nil || !fs.Range.Inclusive {
		t.Errorf("range = %#v, want inclusive", fs.Range)
	}
}

func TestParseRangeOutsideForIsError(t *testing.T) {
	expectParseErr(t, "let r := 0..n\n", "range expressions are only allowed in for-in heads")
	expectParseErr(t, "let r := 0..=10\n", "range expressions are only allowed in for-in heads")
}

func TestParseNopStillWorks(t *testing.T) {
	// Carry-over from v0.0: nop is still a valid statement.
	prog := parseProgramSrc(t, "nop\n")
	expectOne[*NopStmt](t, prog)
}

// ---------------------------------------------------------------------------
// Operator precedence and associativity.
// ---------------------------------------------------------------------------

// exprFromPrint extracts the expression from a single `print expr` program —
// a lightweight way to feed expression-shape assertions through the parser.
func exprFromPrint(t *testing.T, src string) Expr {
	t.Helper()
	prog := parseProgramSrc(t, "print "+src+"\n")
	ps := expectOne[*PrintStmt](t, prog)
	return ps.Expr
}

func TestPrecedenceMulOverAdd(t *testing.T) {
	// 1 + 2 * 3 ⇒ 1 + (2 * 3)
	expr := exprFromPrint(t, "1 + 2 * 3")
	bin, ok := expr.(*BinaryExpr)
	if !ok || bin.Op != BinAdd {
		t.Fatalf("root = %#v, want BinAdd", expr)
	}
	right, ok := bin.Right.(*BinaryExpr)
	if !ok || right.Op != BinMul {
		t.Errorf("right = %#v, want BinMul", bin.Right)
	}
}

func TestPrecedenceAndOverOr(t *testing.T) {
	// a or b and c ⇒ a or (b and c)
	expr := exprFromPrint(t, "a or b and c")
	bin, ok := expr.(*BinaryExpr)
	if !ok || bin.Op != BinOr {
		t.Fatalf("root = %#v, want BinOr", expr)
	}
	right, ok := bin.Right.(*BinaryExpr)
	if !ok || right.Op != BinAnd {
		t.Errorf("right = %#v, want BinAnd", bin.Right)
	}
}

func TestPrecedenceCompareThenAnd(t *testing.T) {
	// a == b and c == d ⇒ (a == b) and (c == d)
	expr := exprFromPrint(t, "a == b and c == d")
	bin, ok := expr.(*BinaryExpr)
	if !ok || bin.Op != BinAnd {
		t.Fatalf("root = %#v, want BinAnd", expr)
	}
	if l, ok := bin.Left.(*BinaryExpr); !ok || l.Op != BinEq {
		t.Errorf("left = %#v, want BinEq", bin.Left)
	}
	if r, ok := bin.Right.(*BinaryExpr); !ok || r.Op != BinEq {
		t.Errorf("right = %#v, want BinEq", bin.Right)
	}
}

func TestPrecedenceNotBindsLooserThanCompare(t *testing.T) {
	// PLAN says not is row 3 and comparison is row 4 (higher row = tighter
	// binding), so `not a == b` parses as `not (a == b)`.
	expr := exprFromPrint(t, "not a == b")
	un, ok := expr.(*UnaryExpr)
	if !ok || un.Op != UnaryNot {
		t.Fatalf("root = %#v, want UnaryNot", expr)
	}
	bin, ok := un.Operand.(*BinaryExpr)
	if !ok || bin.Op != BinEq {
		t.Errorf("operand = %#v, want BinEq", un.Operand)
	}
}

func TestPrecedenceUnaryNegOverMul(t *testing.T) {
	// -a * b ⇒ (-a) * b
	expr := exprFromPrint(t, "-a * b")
	bin, ok := expr.(*BinaryExpr)
	if !ok || bin.Op != BinMul {
		t.Fatalf("root = %#v, want BinMul", expr)
	}
	if un, ok := bin.Left.(*UnaryExpr); !ok || un.Op != UnaryNeg {
		t.Errorf("left = %#v, want UnaryNeg", bin.Left)
	}
}

func TestPrecedenceShiftBetweenAddAndBitAnd(t *testing.T) {
	// a + b << c ⇒ (a + b) << c    (shift looser than +)
	// a & b << c ⇒ a & (b << c)    (shift tighter than &)
	t.Run("add over shift", func(t *testing.T) {
		expr := exprFromPrint(t, "a + b << c")
		bin, ok := expr.(*BinaryExpr)
		if !ok || bin.Op != BinShl {
			t.Fatalf("root = %#v, want BinShl", expr)
		}
		if l, ok := bin.Left.(*BinaryExpr); !ok || l.Op != BinAdd {
			t.Errorf("left = %#v, want BinAdd", bin.Left)
		}
	})
	t.Run("shift over band", func(t *testing.T) {
		expr := exprFromPrint(t, "a & b << c")
		bin, ok := expr.(*BinaryExpr)
		if !ok || bin.Op != BinBitAnd {
			t.Fatalf("root = %#v, want BinBitAnd", expr)
		}
		if r, ok := bin.Right.(*BinaryExpr); !ok || r.Op != BinShl {
			t.Errorf("right = %#v, want BinShl", bin.Right)
		}
	})
}

func TestPrecedenceLeftAssociativity(t *testing.T) {
	// 1 - 2 - 3 ⇒ (1 - 2) - 3
	expr := exprFromPrint(t, "1 - 2 - 3")
	bin, ok := expr.(*BinaryExpr)
	if !ok || bin.Op != BinSub {
		t.Fatalf("root = %#v, want BinSub", expr)
	}
	if l, ok := bin.Left.(*BinaryExpr); !ok || l.Op != BinSub {
		t.Errorf("left = %#v, want BinSub", bin.Left)
	}
}

func TestComparisonNonAssociative(t *testing.T) {
	expectParseErr(t, "print a < b < c\n", "non-associative")
	expectParseErr(t, "print a == b == c\n", "non-associative")
}

func TestParenChangesPrecedence(t *testing.T) {
	// (1 + 2) * 3 ⇒ ParenExpr at the left of a Mul.
	expr := exprFromPrint(t, "(1 + 2) * 3")
	bin, ok := expr.(*BinaryExpr)
	if !ok || bin.Op != BinMul {
		t.Fatalf("root = %#v, want BinMul", expr)
	}
	if _, ok := bin.Left.(*ParenExpr); !ok {
		t.Errorf("left = %#v, want *ParenExpr", bin.Left)
	}
}

func TestCallExprWithMultipleArgs(t *testing.T) {
	expr := exprFromPrint(t, "f(1, 2 + 3, g())")
	call, ok := expr.(*CallExpr)
	if !ok {
		t.Fatalf("expr = %#v, want *CallExpr", expr)
	}
	if len(call.Args) != 3 {
		t.Fatalf("got %d args, want 3", len(call.Args))
	}
	// args[0] = IntLit, args[1] = BinaryExpr, args[2] = CallExpr
	if _, ok := call.Args[0].(*IntLit); !ok {
		t.Errorf("arg 0 = %#v, want IntLit", call.Args[0])
	}
	if _, ok := call.Args[1].(*BinaryExpr); !ok {
		t.Errorf("arg 1 = %#v, want BinaryExpr", call.Args[1])
	}
	if _, ok := call.Args[2].(*CallExpr); !ok {
		t.Errorf("arg 2 = %#v, want CallExpr", call.Args[2])
	}
}

func TestParseLineContinuationInsideParens(t *testing.T) {
	// NEWLINE inside `(` and `[` is transparent — the call straddles two
	// lines but parses as one expression.
	prog := parseProgramSrc(t, "f(\n1,\n2,\n3\n)\n")
	s := expectOne[*ExprStmt](t, prog)
	call, ok := s.Expr.(*CallExpr)
	if !ok {
		t.Fatalf("Expr = %#v, want *CallExpr", s.Expr)
	}
	if len(call.Args) != 3 {
		t.Errorf("got %d args, want 3", len(call.Args))
	}
}

// ---------------------------------------------------------------------------
// Negative cases and diagnostic positions.
// ---------------------------------------------------------------------------

func TestParseBangIsRejectedAsBoolNegation(t *testing.T) {
	// `!x` should produce a clean diagnostic recommending `not x`.
	expectParseErr(t, "let y := !x\n", "use 'not'")
}

func TestParseUnterminatedBlock(t *testing.T) {
	expectParseErr(t, "fn f() {\n", "unterminated block")
}

func TestParseLetWithoutValue(t *testing.T) {
	expectParseErr(t, "let x\n", "expected ':=' or ': T ='")
}

func TestParseFnMissingType(t *testing.T) {
	expectParseErr(t, "fn add(a, b: int) -> int { return a }\n", "")
}

// ---------------------------------------------------------------------------
// Position propagation: every node should carry the position of its first
// token. We spot-check a few representative kinds.
// ---------------------------------------------------------------------------

func TestPositionsArePropagated(t *testing.T) {
	prog := parseProgramSrc(t, "\n\nlet x := 1\n")
	s := expectOne[*LetStmt](t, prog)
	// `let` is on line 3, column 1.
	if s.Pos.Line != 3 || s.Pos.Column != 1 {
		t.Errorf("LetStmt.Pos = %s, want 3:1", s.Pos)
	}
	if il, ok := s.Value.(*IntLit); ok {
		// IntLit `1` at line 3 column 10 (let, space, x, space, :=, space, 1).
		if il.Pos.Line != 3 {
			t.Errorf("IntLit.Pos.Line = %d, want 3", il.Pos.Line)
		}
	}
}
