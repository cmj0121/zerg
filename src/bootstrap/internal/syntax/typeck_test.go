package syntax

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers.
//
// checkSrc lexes, parses, and type-checks src. The default expectation is
// success; tests that expect failure use checkErr instead.
// ---------------------------------------------------------------------------

func checkSrc(t *testing.T, src string) *Program {
	t.Helper()
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex(%q): %v", src, err)
	}
	prog, err := Parse(tokens)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	if err := Check(prog); err != nil {
		t.Fatalf("Check(%q): %v", src, err)
	}
	return prog
}

// checkErr asserts that Check fails and the error message contains want.
func checkErr(t *testing.T, src, want string) string {
	t.Helper()
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex(%q): %v", src, err)
	}
	prog, err := Parse(tokens)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	err = Check(prog)
	if err == nil {
		t.Fatalf("Check(%q) succeeded, expected error containing %q", src, want)
	}
	if _, ok := err.(*TypeError); !ok {
		t.Errorf("error is %T, want *TypeError: %v", err, err)
	}
	if want != "" && !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not contain %q", err.Error(), want)
	}
	return err.Error()
}

// firstStmt returns the first top-level statement of prog.
func firstStmt(t *testing.T, prog *Program) Stmt {
	t.Helper()
	if len(prog.Statements) == 0 {
		t.Fatalf("program has no statements")
	}
	return prog.Statements[0]
}

// ---------------------------------------------------------------------------
// Literal inference.
// ---------------------------------------------------------------------------

func TestCheckLiteralsInfer(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want *Type
	}{
		{"int", "let x := 42\n", TInt()},
		{"float", "let x := 3.14\n", TFloat()},
		{"bool_true", "let x := true\n", TBool()},
		{"bool_false", "let x := false\n", TBool()},
		{"string", "let x := \"hi\"\n", TStr()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog := checkSrc(t, tt.src)
			let := firstStmt(t, prog).(*LetStmt)
			if got := let.Value.Type(); got != tt.want {
				t.Fatalf("Type = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestCheckIntLiteralStoresParsedValue(t *testing.T) {
	prog := checkSrc(t, "let x := 0xff\n")
	let := firstStmt(t, prog).(*LetStmt)
	lit := let.Value.(*IntLit)
	if lit.Int != 255 {
		t.Fatalf("Int = %d, want 255", lit.Int)
	}
}

func TestCheckFloatLiteralStoresParsedValue(t *testing.T) {
	prog := checkSrc(t, "let x := 3.5\n")
	let := firstStmt(t, prog).(*LetStmt)
	lit := let.Value.(*FloatLit)
	if lit.Float != 3.5 {
		t.Fatalf("Float = %v, want 3.5", lit.Float)
	}
}

func TestCheckIntLiteralOverflow(t *testing.T) {
	// 2^63 == 9223372036854775808 — one past int64 max.
	checkErr(t, "let x := 9223372036854775808\n", "overflows int")
}

func TestCheckFloatLiteralOverflow(t *testing.T) {
	// 1e400 is too large for float64; strconv yields ±Inf.
	// We don't have exponents at v0.1, so we use a long literal that
	// still parses through ParseFloat to ±Inf.
	src := "let x := 1" + strings.Repeat("0", 400) + ".0\n"
	checkErr(t, src, "overflows float")
}

// ---------------------------------------------------------------------------
// Identifier resolution and scope.
// ---------------------------------------------------------------------------

func TestCheckUseDeclaredName(t *testing.T) {
	prog := checkSrc(t, "let x := 1\nprint x\n")
	pst := prog.Statements[1].(*PrintStmt)
	if pst.Expr.Type() != TInt() {
		t.Fatalf("Type = %s, want int", pst.Expr.Type())
	}
}

func TestCheckUndefinedName(t *testing.T) {
	checkErr(t, "print x\n", `undefined name "x"`)
}

func TestCheckSameScopeRedeclaration(t *testing.T) {
	checkErr(t, "let x := 1\nlet x := 2\n", `already declared`)
}

func TestCheckInnerScopeShadowingAllowed(t *testing.T) {
	src := "let x := 1\nif true {\nlet x := \"s\"\nprint x\n}\n"
	checkSrc(t, src)
}

func TestCheckOuterNameVisibleFromInner(t *testing.T) {
	src := "let x := 1\nif true {\nprint x\n}\n"
	checkSrc(t, src)
}

// ---------------------------------------------------------------------------
// Type annotations.
// ---------------------------------------------------------------------------

func TestCheckTypeAnnotationMatch(t *testing.T) {
	prog := checkSrc(t, "let x: int = 1\n")
	let := firstStmt(t, prog).(*LetStmt)
	if let.Type.Resolved != TInt() {
		t.Fatalf("Resolved = %s, want int", let.Type.Resolved)
	}
}

func TestCheckTypeAnnotationMismatch(t *testing.T) {
	checkErr(t, "let x: int = \"hi\"\n", "cannot assign str to int")
}

func TestCheckUnknownTypeName(t *testing.T) {
	checkErr(t, "let x: bogus = 1\n", `unknown type "bogus"`)
}

// ---------------------------------------------------------------------------
// Mut / let / const assignment legality.
// ---------------------------------------------------------------------------

func TestCheckMutAssignAllowed(t *testing.T) {
	checkSrc(t, "mut x := 1\nx = 2\n")
}

func TestCheckLetAssignRejected(t *testing.T) {
	checkErr(t, "let x := 1\nx = 2\n", "declared with let")
}

func TestCheckConstAssignRejected(t *testing.T) {
	checkErr(t, "const x := 1\nx = 2\n", "declared with const")
}

func TestCheckCompoundAssignNumeric(t *testing.T) {
	checkSrc(t, "mut x := 1\nx += 2\nx -= 1\nx *= 3\nx /= 1\nx %= 1\n")
}

func TestCheckCompoundAssignStrConcat(t *testing.T) {
	checkSrc(t, "mut s := \"a\"\ns += \"b\"\n")
}

func TestCheckCompoundAssignBitwiseNeedsInt(t *testing.T) {
	checkErr(t, "mut x := 1.0\nx &= 1\n", "requires int operands")
}

func TestCheckCompoundAssignTypeMismatch(t *testing.T) {
	checkErr(t, "mut x := 1\nx += 1.0\n", "requires numeric operands of the same type")
}

func TestCheckPlainAssignTypeMismatch(t *testing.T) {
	checkErr(t, "mut x := 1\nx = 1.0\n", "cannot assign float")
}

// ---------------------------------------------------------------------------
// Operator typing.
// ---------------------------------------------------------------------------

func TestCheckArithIntOK(t *testing.T) {
	checkSrc(t, "let x := 1 + 2 * 3 - 4\nlet y := 7 // 2\nlet z := 5 % 3\n")
}

func TestCheckArithFloatOK(t *testing.T) {
	checkSrc(t, "let x := 1.0 + 2.0\nlet y := 3.5 / 1.5\n")
}

func TestCheckArithIntFloatRejected(t *testing.T) {
	checkErr(t, "let x := 1 + 2.0\n", "operator + requires numeric or str operands of the same type")
}

func TestCheckStringConcatOK(t *testing.T) {
	prog := checkSrc(t, "let s := \"a\" + \"b\"\n")
	let := firstStmt(t, prog).(*LetStmt)
	if let.Value.Type() != TStr() {
		t.Fatalf("Type = %s, want str", let.Value.Type())
	}
}

func TestCheckStringSubRejected(t *testing.T) {
	checkErr(t, "let s := \"a\" - \"b\"\n", "requires numeric operands")
}

func TestCheckStringIntAddRejected(t *testing.T) {
	checkErr(t, "let s := \"a\" + 1\n", "operator + requires numeric or str operands")
}

func TestCheckBitwiseIntOK(t *testing.T) {
	checkSrc(t, "let x := 1 & 2\nlet y := 1 | 2\nlet z := 1 ^ 2\nlet a := 1 << 2\nlet b := 8 >> 1\n")
}

func TestCheckBitwiseFloatRejected(t *testing.T) {
	checkErr(t, "let x := 1.0 & 1.0\n", "requires int operands")
}

func TestCheckCompareSameTypeOK(t *testing.T) {
	checkSrc(t, "let a := 1 == 2\nlet b := \"a\" == \"b\"\nlet c := true == false\nlet d := 1.0 == 2.0\n")
}

func TestCheckCompareDifferentTypeRejected(t *testing.T) {
	checkErr(t, "let x := 1 == 1.0\n", "operator == requires operands of the same type")
}

func TestCheckRelationalNumericAndStrOK(t *testing.T) {
	checkSrc(t, "let a := 1 < 2\nlet b := 1.0 <= 2.0\nlet c := \"a\" < \"b\"\n")
}

func TestCheckRelationalBoolRejected(t *testing.T) {
	checkErr(t, "let x := true < false\n", "requires same-typed numeric or str operands")
}

func TestCheckLogicalBoolOK(t *testing.T) {
	checkSrc(t, "let x := true and false\nlet y := true or false\nlet z := true xor false\n")
}

func TestCheckLogicalNonBoolRejected(t *testing.T) {
	checkErr(t, "let x := 1 and 2\n", "requires bool operands")
}

func TestCheckUnaryNegInt(t *testing.T) {
	prog := checkSrc(t, "let x := -1\n")
	let := firstStmt(t, prog).(*LetStmt)
	if let.Value.Type() != TInt() {
		t.Fatalf("Type = %s, want int", let.Value.Type())
	}
}

func TestCheckUnaryNegBoolRejected(t *testing.T) {
	checkErr(t, "let x := -true\n", "unary - requires a numeric operand")
}

func TestCheckUnaryBitNotIntOK(t *testing.T) {
	checkSrc(t, "let x := ~1\n")
}

func TestCheckUnaryBitNotFloatRejected(t *testing.T) {
	checkErr(t, "let x := ~1.0\n", "unary ~ requires an int operand")
}

func TestCheckUnaryNotBoolOK(t *testing.T) {
	checkSrc(t, "let x := not true\n")
}

func TestCheckUnaryNotIntRejected(t *testing.T) {
	checkErr(t, "let x := not 1\n", "unary not requires a bool operand")
}

// ---------------------------------------------------------------------------
// Control flow.
// ---------------------------------------------------------------------------

func TestCheckIfCondMustBeBool(t *testing.T) {
	checkErr(t, "if 1 {\nnop\n}\n", "'if' condition must be bool")
}

func TestCheckElifCondMustBeBool(t *testing.T) {
	checkErr(t, "if true {\nnop\n} elif 1 {\nnop\n}\n", "'elif' condition must be bool")
}

func TestCheckForCondMustBeBool(t *testing.T) {
	checkErr(t, "for 1 {\nnop\n}\n", "'for' condition must be bool")
}

func TestCheckForRangeBoundsMustBeInt(t *testing.T) {
	checkErr(t, "for x in 0..1.0 {\nnop\n}\n", "range end must be int")
}

func TestCheckForRangeStartMustBeInt(t *testing.T) {
	checkErr(t, "for x in 1.0..5 {\nnop\n}\n", "range start must be int")
}

func TestCheckForRangeVarBoundToInt(t *testing.T) {
	prog := checkSrc(t, "for x in 0..3 {\nprint x\n}\n")
	for_ := firstStmt(t, prog).(*ForStmt)
	pst := for_.Body.Statements[0].(*PrintStmt)
	if pst.Expr.Type() != TInt() {
		t.Fatalf("loop var type = %s, want int", pst.Expr.Type())
	}
}

func TestCheckBreakOutsideLoopRejected(t *testing.T) {
	checkErr(t, "break\n", "'break' outside of a loop")
}

func TestCheckContinueOutsideLoopRejected(t *testing.T) {
	checkErr(t, "continue\n", "'continue' outside of a loop")
}

func TestCheckReturnOutsideFnRejected(t *testing.T) {
	checkErr(t, "return\n", "'return' outside of a function")
}

func TestCheckBreakGuardMustBeBool(t *testing.T) {
	checkErr(t, "for {\nbreak if 1\n}\n", "guard must be bool")
}

// ---------------------------------------------------------------------------
// Functions.
// ---------------------------------------------------------------------------

func TestCheckFnSimple(t *testing.T) {
	checkSrc(t, "fn add(a: int, b: int) -> int {\nreturn a + b\n}\n")
}

func TestCheckFnArityError(t *testing.T) {
	src := "fn add(a: int, b: int) -> int {\nreturn a + b\n}\nlet z := add(1)\n"
	checkErr(t, src, "expects 2 argument(s), got 1")
}

func TestCheckFnArgTypeError(t *testing.T) {
	src := "fn add(a: int, b: int) -> int {\nreturn a + b\n}\nlet z := add(1, \"x\")\n"
	checkErr(t, src, "argument 2")
}

func TestCheckFnReturnTypeMismatch(t *testing.T) {
	checkErr(t, "fn f() -> int {\nreturn \"x\"\n}\n", "return type mismatch")
}

func TestCheckFnBareReturnInTypedFnRejected(t *testing.T) {
	checkErr(t, "fn f() -> int {\nreturn\n}\n", "bare 'return'")
}

func TestCheckFnReturnValueInVoidFnRejected(t *testing.T) {
	checkErr(t, "fn f() {\nreturn 1\n}\n", "function returns no value")
}

func TestCheckFnVoidBareReturnOK(t *testing.T) {
	checkSrc(t, "fn f() {\nreturn\n}\n")
}

func TestCheckCallUndefinedFunction(t *testing.T) {
	checkErr(t, "let x := bogus(1)\n", `undefined function "bogus"`)
}

func TestCheckCallVariableAsFunction(t *testing.T) {
	checkErr(t, "let x := 1\nlet y := x(1)\n", `is not a function`)
}

func TestCheckNestedFnRejected(t *testing.T) {
	src := "fn outer() {\nfn inner() {\nnop\n}\n}\n"
	checkErr(t, src, "nested functions are not supported")
}

func TestCheckDuplicateFnRejected(t *testing.T) {
	src := "fn f() {\nnop\n}\nfn f() {\nnop\n}\n"
	checkErr(t, src, `function "f" already declared`)
}

func TestCheckFnForwardCall(t *testing.T) {
	// f calls g defined later — top-level signatures are collected up front.
	src := "fn f() -> int {\nreturn g()\n}\nfn g() -> int {\nreturn 1\n}\n"
	checkSrc(t, src)
}

// ---------------------------------------------------------------------------
// Const initialisers.
// ---------------------------------------------------------------------------

func TestCheckConstLiteralOK(t *testing.T) {
	checkSrc(t, "const x := 1\nconst y := 1 + 2 * 3\nconst z := -1\n")
}

func TestCheckConstFromIdentRejected(t *testing.T) {
	// At v0.1 const-init may not reference any name (even another const).
	checkErr(t, "let a := 1\nconst x := a\n", "must be a constant expression")
}

func TestCheckConstFromCallRejected(t *testing.T) {
	src := "fn f() -> int {\nreturn 1\n}\nconst x := f()\n"
	checkErr(t, src, "must be a constant expression")
}

// ---------------------------------------------------------------------------
// Print.
// ---------------------------------------------------------------------------

func TestCheckPrintAllPrimitives(t *testing.T) {
	checkSrc(t, "print 1\nprint 1.0\nprint true\nprint \"hi\"\n")
}

func TestCheckPrintRejectsVoidCall(t *testing.T) {
	src := "fn f() {\nnop\n}\nprint f()\n"
	checkErr(t, src, "cannot print value of type ()")
}

// ---------------------------------------------------------------------------
// Range outside for-in (parser already rejects this; defence in depth).
// ---------------------------------------------------------------------------

// This one is parser-level — included for completeness.
func TestCheckRangeOutsideForRejectedAtParse(t *testing.T) {
	src := "let x := 0..3\n"
	tokens, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if _, perr := Parse(tokens); perr == nil {
		t.Fatalf("expected parse error for range outside for-in")
	}
}

// ---------------------------------------------------------------------------
// TypeRef Resolved is set.
// ---------------------------------------------------------------------------

func TestCheckTypeRefResolvedFilledIn(t *testing.T) {
	prog := checkSrc(t, "fn f(a: int, b: float) -> bool {\nreturn true\n}\n")
	fn := firstStmt(t, prog).(*FnDecl)
	if fn.Params[0].Type.Resolved != TInt() {
		t.Fatalf("param 0 Resolved = %s, want int", fn.Params[0].Type.Resolved)
	}
	if fn.Params[1].Type.Resolved != TFloat() {
		t.Fatalf("param 1 Resolved = %s, want float", fn.Params[1].Type.Resolved)
	}
	if fn.Return.Resolved != TBool() {
		t.Fatalf("return Resolved = %s, want bool", fn.Return.Resolved)
	}
}

// ---------------------------------------------------------------------------
// Sanity: a fully-typed program has no nil Type() on any reachable Expr.
// ---------------------------------------------------------------------------

func TestCheckEveryExprAnnotated(t *testing.T) {
	src := `let a := 1 + 2
let b := -a
mut c := "x"
c = c + "y"
if a > 0 {
print c
}
fn f(x: int) -> int {
return x * 2
}
let r := f(5)
print r
`
	prog := checkSrc(t, src)
	walkProgram(t, prog)
}

// walkProgram asserts every Expr in prog has a non-nil Type. RangeExprs are
// exempted because v0.1 does not have a Range type.
func walkProgram(t *testing.T, prog *Program) {
	t.Helper()
	for _, s := range prog.Statements {
		walkStmt(t, s)
	}
}

func walkStmt(t *testing.T, s Stmt) {
	t.Helper()
	switch x := s.(type) {
	case *LetStmt:
		walkExpr(t, x.Value)
	case *MutStmt:
		walkExpr(t, x.Value)
	case *ConstStmt:
		walkExpr(t, x.Value)
	case *AssignStmt:
		walkExpr(t, x.Target)
		walkExpr(t, x.Value)
	case *PrintStmt:
		walkExpr(t, x.Expr)
	case *ExprStmt:
		walkExpr(t, x.Expr)
	case *ReturnStmt:
		if x.Value != nil {
			walkExpr(t, x.Value)
		}
		if x.Guard != nil {
			walkExpr(t, x.Guard)
		}
	case *BreakStmt:
		if x.Guard != nil {
			walkExpr(t, x.Guard)
		}
	case *ContinueStmt:
		if x.Guard != nil {
			walkExpr(t, x.Guard)
		}
	case *IfStmt:
		walkExpr(t, x.Cond)
		walkBlock(t, x.Then)
		for i := range x.Elifs {
			walkExpr(t, x.Elifs[i].Cond)
			walkBlock(t, x.Elifs[i].Body)
		}
		if x.Else != nil {
			walkBlock(t, x.Else)
		}
	case *ForStmt:
		if x.Cond != nil {
			walkExpr(t, x.Cond)
		}
		walkBlock(t, x.Body)
	case *FnDecl:
		walkBlock(t, x.Body)
	case *NopStmt:
	default:
		t.Fatalf("unhandled stmt %T", s)
	}
}

func walkBlock(t *testing.T, b *Block) {
	t.Helper()
	if b == nil {
		return
	}
	for _, s := range b.Statements {
		walkStmt(t, s)
	}
}

func walkExpr(t *testing.T, e Expr) {
	t.Helper()
	if _, isRange := e.(*RangeExpr); !isRange {
		if e.Type() == nil {
			t.Fatalf("Expr %T at %s has nil Type", e, e.ExprPos())
		}
	}
	switch x := e.(type) {
	case *BinaryExpr:
		walkExpr(t, x.Left)
		walkExpr(t, x.Right)
	case *UnaryExpr:
		walkExpr(t, x.Operand)
	case *CallExpr:
		walkExpr(t, x.Callee)
		for _, a := range x.Args {
			walkExpr(t, a)
		}
	case *ParenExpr:
		walkExpr(t, x.Inner)
	case *RangeExpr:
		walkExpr(t, x.Start)
		walkExpr(t, x.End)
	}
}
