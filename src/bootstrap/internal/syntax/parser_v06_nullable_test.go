package syntax

import (
	"testing"
)

// ---------------------------------------------------------------------------
// v0.6 Unit 1b — parser tests for nullable types, the `nil` literal, and the
// `?` / `??` / `?.` operators.
//
// Unit 1b is parser-only. Typeck (Unit 4) consumes the new fields / nodes;
// the parser only records the shape so downstream layers can dispatch.
// ---------------------------------------------------------------------------

// --- nullable type-position tests ----------------------------------------

func TestParseNullableSimpleType(t *testing.T) {
	prog := parseProgramSrc(t, "fn maybe() -> int? { return 0 }\n")
	fn := expectOne[*FnDecl](t, prog)
	if fn.Return == nil || !fn.Return.Nullable {
		t.Fatalf("return = %#v, want Nullable=true", fn.Return)
	}
	if fn.Return.Name != "int" {
		t.Errorf("name = %q, want int", fn.Return.Name)
	}
}

func TestParseNullableTypeOnLetAnnotation(t *testing.T) {
	prog := parseProgramSrc(t, "let x: int? = nil\n")
	st := expectOne[*LetStmt](t, prog)
	if st.Type == nil || !st.Type.Nullable || st.Type.Name != "int" {
		t.Errorf("type = %v, want int?", st.Type)
	}
	if _, ok := st.Value.(*NilLit); !ok {
		t.Errorf("value = %T, want *NilLit", st.Value)
	}
}

func TestParseNullableTypeOnFnParam(t *testing.T) {
	prog := parseProgramSrc(t, "fn use(x: str?) {}\n")
	fn := expectOne[*FnDecl](t, prog)
	if len(fn.Params) != 1 {
		t.Fatalf("got %d params, want 1", len(fn.Params))
	}
	pt := fn.Params[0].Type
	if pt == nil || !pt.Nullable || pt.Name != "str" {
		t.Errorf("param type = %v, want str?", pt)
	}
}

func TestParseNullableTypeOnStructField(t *testing.T) {
	prog := parseProgramSrc(t, "struct Account { name: str, age: int? }\n")
	st := expectOne[*StructDecl](t, prog)
	if len(st.Fields) != 2 {
		t.Fatalf("got %d fields, want 2", len(st.Fields))
	}
	if st.Fields[1].Type == nil || !st.Fields[1].Type.Nullable {
		t.Errorf("age field type = %v, want int?", st.Fields[1].Type)
	}
}

func TestParseNullableTypeOnGenericTypeArg(t *testing.T) {
	// `Box[int?]` — nullable inside a type-arg list.
	prog := parseProgramSrc(t, "fn make() -> Box[int?] { return 0 }\n")
	fn := expectOne[*FnDecl](t, prog)
	ret := fn.Return
	if len(ret.TypeArgs) != 1 {
		t.Fatalf("got %d type args, want 1", len(ret.TypeArgs))
	}
	if !ret.TypeArgs[0].Nullable || ret.TypeArgs[0].Name != "int" {
		t.Errorf("inner type = %v, want int?", ret.TypeArgs[0])
	}
}

func TestParseNullableTypeOnList(t *testing.T) {
	prog := parseProgramSrc(t, "fn make() -> list[int]? { return [] }\n")
	fn := expectOne[*FnDecl](t, prog)
	if fn.Return == nil || !fn.Return.Nullable || fn.Return.Kind != TypeRefList {
		t.Errorf("return = %#v, want list[int]?", fn.Return)
	}
}

func TestParseRejectDoubleNullable(t *testing.T) {
	// `int??` lexed as `?` then `?` — the parser sees two KindQuestions and
	// rejects on the second.
	expectParseErr(t, "let x: int? ? = nil\n", "double-nullable types")
}

func TestParseRejectDoubleNullableFusedToken(t *testing.T) {
	// `int??` (no space) — lexer fuses to one KindCoalesce token; the
	// type-position rejection still fires with the same diagnostic.
	expectParseErr(t, "let x: int?? = nil\n", "double-nullable types")
}

// --- nil literal tests ---------------------------------------------------

func TestParseNilLiteralInLet(t *testing.T) {
	prog := parseProgramSrc(t, "let x: int? = nil\n")
	st := expectOne[*LetStmt](t, prog)
	nl, ok := st.Value.(*NilLit)
	if !ok {
		t.Fatalf("value = %T, want *NilLit", st.Value)
	}
	if nl.Pos.Line != 1 {
		t.Errorf("nil pos line = %d, want 1", nl.Pos.Line)
	}
}

func TestParseNilLiteralLexedAsKeyword(t *testing.T) {
	tokens, err := Lex([]byte("nil"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if tokens[0].Kind != KindNil {
		t.Errorf("kind = %v, want KindNil", tokens[0].Kind)
	}
}

func TestParseNilAsArgumentValue(t *testing.T) {
	prog := parseProgramSrc(t, "fn f(x: int?) {}\nf(nil)\n")
	if len(prog.Statements) != 2 {
		t.Fatalf("got %d statements, want 2", len(prog.Statements))
	}
	es, ok := prog.Statements[1].(*ExprStmt)
	if !ok {
		t.Fatalf("stmt 1 is %T, want *ExprStmt", prog.Statements[1])
	}
	call, ok := es.Expr.(*CallExpr)
	if !ok {
		t.Fatalf("call expr is %T, want *CallExpr", es.Expr)
	}
	if len(call.Args) != 1 {
		t.Fatalf("got %d args, want 1", len(call.Args))
	}
	if _, ok := call.Args[0].(*NilLit); !ok {
		t.Errorf("arg 0 = %T, want *NilLit", call.Args[0])
	}
}

func TestParseRejectNilAsCallee(t *testing.T) {
	expectParseErr(t, "let x := nil(1, 2)\n", "cannot call 'nil'")
}

func TestParseNilAsBinding_StatementErrors(t *testing.T) {
	// `nil` in expression-statement position (no assignment, no call) is
	// rejected by the existing "expression statements must be function
	// calls" rule. We don't need a dedicated diagnostic — the v0.1 message
	// covers it.
	expectParseErr(t, "nil\n", "expression statements must be function calls")
}

// --- ? propagation tests -------------------------------------------------

func TestParsePropagateOnIdent(t *testing.T) {
	prog := parseProgramSrc(t, "fn f() -> int { let x := result?\n return x }\n")
	if len(prog.Statements) != 1 {
		t.Fatalf("got %d statements, want 1", len(prog.Statements))
	}
	fn := prog.Statements[0].(*FnDecl)
	if len(fn.Body.Statements) != 2 {
		t.Fatalf("got %d body statements, want 2", len(fn.Body.Statements))
	}
	let := fn.Body.Statements[0].(*LetStmt)
	pe, ok := let.Value.(*PropagateExpr)
	if !ok {
		t.Fatalf("value = %T, want *PropagateExpr", let.Value)
	}
	if id, ok := pe.Inner.(*IdentExpr); !ok || id.Name != "result" {
		t.Errorf("inner = %v, want IdentExpr{result}", pe.Inner)
	}
}

func TestParsePropagateOnCall(t *testing.T) {
	prog := parseProgramSrc(t, "fn f() -> int { let x := divide(10, 2)?\n return x }\n")
	fn := prog.Statements[0].(*FnDecl)
	let := fn.Body.Statements[0].(*LetStmt)
	pe, ok := let.Value.(*PropagateExpr)
	if !ok {
		t.Fatalf("value = %T, want *PropagateExpr", let.Value)
	}
	if _, ok := pe.Inner.(*CallExpr); !ok {
		t.Errorf("inner = %T, want *CallExpr", pe.Inner)
	}
}

func TestParseRejectBareQuestion(t *testing.T) {
	expectParseErr(t, "let x := ?\n", "'?' must follow an expression")
}

// --- ?? coalesce tests ---------------------------------------------------

func TestParseCoalesceSimple(t *testing.T) {
	prog := parseProgramSrc(t, "let x := a ?? b\n")
	st := expectOne[*LetStmt](t, prog)
	ce, ok := st.Value.(*CoalesceExpr)
	if !ok {
		t.Fatalf("value = %T, want *CoalesceExpr", st.Value)
	}
	if id, ok := ce.Left.(*IdentExpr); !ok || id.Name != "a" {
		t.Errorf("LHS = %v, want IdentExpr{a}", ce.Left)
	}
	if id, ok := ce.Right.(*IdentExpr); !ok || id.Name != "b" {
		t.Errorf("RHS = %v, want IdentExpr{b}", ce.Right)
	}
}

func TestParseCoalesceRightAssociative(t *testing.T) {
	// `a ?? b ?? c` ⇒ `a ?? (b ?? c)`.
	prog := parseProgramSrc(t, "let x := a ?? b ?? c\n")
	st := expectOne[*LetStmt](t, prog)
	outer, ok := st.Value.(*CoalesceExpr)
	if !ok {
		t.Fatalf("value = %T, want *CoalesceExpr", st.Value)
	}
	if id, ok := outer.Left.(*IdentExpr); !ok || id.Name != "a" {
		t.Errorf("outer LHS = %v, want a", outer.Left)
	}
	inner, ok := outer.Right.(*CoalesceExpr)
	if !ok {
		t.Fatalf("outer RHS = %T, want *CoalesceExpr (right-assoc)", outer.Right)
	}
	if id, ok := inner.Left.(*IdentExpr); !ok || id.Name != "b" {
		t.Errorf("inner LHS = %v, want b", inner.Left)
	}
	if id, ok := inner.Right.(*IdentExpr); !ok || id.Name != "c" {
		t.Errorf("inner RHS = %v, want c", inner.Right)
	}
}

func TestParseCoalesceLowerThanOr(t *testing.T) {
	// `a or b ?? c` — `??` must bind looser than `or`, so the structure is
	// `(a or b) ?? c`.
	prog := parseProgramSrc(t, "let x := a or b ?? c\n")
	st := expectOne[*LetStmt](t, prog)
	ce, ok := st.Value.(*CoalesceExpr)
	if !ok {
		t.Fatalf("value = %T, want *CoalesceExpr (?? lower than or)", st.Value)
	}
	if be, ok := ce.Left.(*BinaryExpr); !ok || be.Op != BinOr {
		t.Errorf("LHS = %v, want (a or b)", ce.Left)
	}
}

func TestParseRejectBareCoalesce(t *testing.T) {
	expectParseErr(t, "let x := ?? a\n", "'??' must appear between two expressions")
}

func TestParseRejectCoalesceFollowedByDot(t *testing.T) {
	// `lhs ??.field` — common typo for `lhs?.field`. Caught by parseCoalesce
	// before the recursive parseAtom would emit a generic diagnostic.
	expectParseErr(t, "let x := a ??.b\n", "did you mean '?.' for safe navigation")
}

// --- ?. safe navigation tests --------------------------------------------

func TestParseSafeNavigationSimple(t *testing.T) {
	prog := parseProgramSrc(t, "let x := obj?.field\n")
	st := expectOne[*LetStmt](t, prog)
	fa, ok := st.Value.(*FieldAccessExpr)
	if !ok {
		t.Fatalf("value = %T, want *FieldAccessExpr", st.Value)
	}
	if !fa.Safe {
		t.Errorf("Safe = false, want true on `?.`")
	}
	if fa.FieldName != "field" {
		t.Errorf("field = %q, want field", fa.FieldName)
	}
}

func TestParseSafeNavigationChained(t *testing.T) {
	// `a?.b?.c` — chained safe navigation. Each link is a FieldAccessExpr
	// with Safe=true; the receiver-chain walks left-to-right.
	prog := parseProgramSrc(t, "let x := a?.b?.c\n")
	st := expectOne[*LetStmt](t, prog)
	outer, ok := st.Value.(*FieldAccessExpr)
	if !ok {
		t.Fatalf("value = %T, want *FieldAccessExpr", st.Value)
	}
	if outer.FieldName != "c" || !outer.Safe {
		t.Errorf("outer = %+v, want c with Safe=true", outer)
	}
	mid, ok := outer.Receiver.(*FieldAccessExpr)
	if !ok || mid.FieldName != "b" || !mid.Safe {
		t.Errorf("mid = %#v, want b/Safe", mid)
	}
}

func TestParseSafeNavigationMixedWithDot(t *testing.T) {
	// `obj.a?.b.c` — `.` chains on either side of `?.` keep their Safe=false
	// status. Only the `?.b` link is Safe.
	prog := parseProgramSrc(t, "let x := obj.a?.b.c\n")
	st := expectOne[*LetStmt](t, prog)
	c := st.Value.(*FieldAccessExpr) // .c
	if c.Safe {
		t.Errorf("`.c` should not be safe")
	}
	b := c.Receiver.(*FieldAccessExpr) // ?.b
	if !b.Safe {
		t.Errorf("`?.b` must be safe")
	}
	a := b.Receiver.(*FieldAccessExpr) // .a
	if a.Safe {
		t.Errorf("`.a` should not be safe")
	}
}

func TestParseRejectSafeNavMethodCall(t *testing.T) {
	// PLAN.md defers method-form `?.` to a future unit. The parser rejects
	// `obj?.method()` so the user is not surprised when typeck doesn't
	// support the lowering.
	expectParseErr(t, "let x := obj?.method()\n", "method-form safe navigation")
}

func TestParseRejectBareSafeDot(t *testing.T) {
	expectParseErr(t, "let x := ?.field\n", "'?.' must follow an expression")
}

// --- combined / interaction tests ---------------------------------------

func TestParsePropagateThenCoalesce(t *testing.T) {
	// `divide()? ?? 0` — propagate (postfix) before coalesce (lowest infix).
	prog := parseProgramSrc(t, "fn f() -> int { return divide(10, 2)? ?? 0 }\n")
	fn := prog.Statements[0].(*FnDecl)
	ret := fn.Body.Statements[0].(*ReturnStmt)
	ce, ok := ret.Value.(*CoalesceExpr)
	if !ok {
		t.Fatalf("ret.Value = %T, want *CoalesceExpr", ret.Value)
	}
	if _, ok := ce.Left.(*PropagateExpr); !ok {
		t.Errorf("LHS = %T, want *PropagateExpr", ce.Left)
	}
}

func TestParseSafeChainedThenPropagate(t *testing.T) {
	// `obj?.field?` — safe-nav then propagate. Reads as
	// PropagateExpr{ Inner: FieldAccessExpr{ Safe } }.
	prog := parseProgramSrc(t, "fn f() -> int { return obj?.field? }\n")
	fn := prog.Statements[0].(*FnDecl)
	ret := fn.Body.Statements[0].(*ReturnStmt)
	pe, ok := ret.Value.(*PropagateExpr)
	if !ok {
		t.Fatalf("ret.Value = %T, want *PropagateExpr", ret.Value)
	}
	fa, ok := pe.Inner.(*FieldAccessExpr)
	if !ok || !fa.Safe {
		t.Errorf("inner = %#v, want safe FieldAccessExpr", pe.Inner)
	}
}

// --- v0.6 example surface parses cleanly --------------------------------

func TestParseV06NullSafetyExampleBody(t *testing.T) {
	// Subset of examples/11_null_safety.zg — exercise every v0.6 surface
	// form admitted by Unit 1 (the example uses the eventual built-in
	// Result/Ok/Err names which are user-visible identifiers; the parser
	// admits them as bare identifiers / call-shapes and typeck/Unit 2
	// later resolves them to the synthetic Option/Result decls).
	// The example file uses bare `x := ...` shorthand inside the fn body
	// which Zerg's grammar requires to be spelled `let x := ...`. The
	// example is documentation prose; the parser test exercises the
	// canonicalised form so the v0.6 surface itself (Result[int, str], `?`
	// propagation, `int?` annotation, `nil`) is what we verify.
	src := `fn divide(a: int, b: int) -> Result[int, str] {
	return Err("Division by zero") if b == 0
	return Ok(a // b)
}

fn calculate() -> Result[int, str] {
	let x := divide(10, 0)?
	return Ok(x * 2)
}

let x: int? = nil
`
	prog := parseProgramSrc(t, src)
	if len(prog.Statements) != 3 {
		t.Fatalf("got %d top-level statements, want 3", len(prog.Statements))
	}
}

func TestParseV06SpecsExampleSurface(t *testing.T) {
	// Subset of examples/10_specs.zg — exercise the generic-spec /
	// generic-fn / multi-bound surface admitted by Unit 1.
	src := `spec Iterator[T] {
	fn next() -> T?
}

fn display[T: Printable](item: T) {
	print item
}

fn show[T: Printable + Hashable](x: T) {
	print x
}
`
	prog := parseProgramSrc(t, src)
	if len(prog.Statements) != 3 {
		t.Fatalf("got %d top-level statements, want 3", len(prog.Statements))
	}
	sp := prog.Statements[0].(*SpecDecl)
	if len(sp.TypeParams) != 1 {
		t.Errorf("Iterator type params: %+v", sp.TypeParams)
	}
	display := prog.Statements[1].(*FnDecl)
	if len(display.TypeParams) != 1 || len(display.TypeParams[0].Bounds) != 1 {
		t.Errorf("display type params: %+v", display.TypeParams)
	}
	show := prog.Statements[2].(*FnDecl)
	if len(show.TypeParams[0].Bounds) != 2 {
		t.Errorf("show should have 2 bounds, got %v", show.TypeParams[0].Bounds)
	}
}
