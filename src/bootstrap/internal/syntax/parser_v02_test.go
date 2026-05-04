package syntax

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// v0.2 parser tests — composite data forms.
//
// Unit 1b adds AST + parser for struct/enum decls, match, list/tuple/struct
// literals, indexing, slicing, field access, and match patterns. Type-checking
// for these shapes lands in Unit 2; the tests below exercise the parser
// surface only.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// struct declaration.
// ---------------------------------------------------------------------------

func TestParseStructDeclSingleField(t *testing.T) {
	prog := parseProgramSrc(t, "struct Point { x: int }\n")
	s := expectOne[*StructDecl](t, prog)
	if s.Name != "Point" {
		t.Errorf("name = %q, want %q", s.Name, "Point")
	}
	if len(s.Fields) != 1 {
		t.Fatalf("got %d fields, want 1", len(s.Fields))
	}
	if s.Fields[0].Name != "x" {
		t.Errorf("field 0 name = %q, want %q", s.Fields[0].Name, "x")
	}
	if s.Fields[0].Type.Name != "int" {
		t.Errorf("field 0 type = %q, want %q", s.Fields[0].Type.Name, "int")
	}
}

func TestParseStructDeclMultipleFields(t *testing.T) {
	prog := parseProgramSrc(t, "struct Point { x: int, y: int }\n")
	s := expectOne[*StructDecl](t, prog)
	if len(s.Fields) != 2 {
		t.Fatalf("got %d fields, want 2", len(s.Fields))
	}
	for i, want := range []string{"x", "y"} {
		if s.Fields[i].Name != want {
			t.Errorf("field %d name = %q, want %q", i, s.Fields[i].Name, want)
		}
	}
}

func TestParseStructDeclTrailingComma(t *testing.T) {
	prog := parseProgramSrc(t, "struct Point { x: int, y: int, }\n")
	s := expectOne[*StructDecl](t, prog)
	if len(s.Fields) != 2 {
		t.Errorf("trailing comma broke field count = %d, want 2", len(s.Fields))
	}
}

func TestParseStructDeclEmpty(t *testing.T) {
	// Parser admits empty struct decls; typeck is the layer that rejects.
	prog := parseProgramSrc(t, "struct Empty {}\n")
	s := expectOne[*StructDecl](t, prog)
	if len(s.Fields) != 0 {
		t.Errorf("got %d fields, want 0", len(s.Fields))
	}
}

func TestParseStructDeclMultiline(t *testing.T) {
	src := `struct Point {
x: int,
y: int,
}
`
	prog := parseProgramSrc(t, src)
	s := expectOne[*StructDecl](t, prog)
	if len(s.Fields) != 2 {
		t.Errorf("got %d fields, want 2", len(s.Fields))
	}
}

// ---------------------------------------------------------------------------
// enum declaration.
// ---------------------------------------------------------------------------

func TestParseEnumDeclSingleVariant(t *testing.T) {
	prog := parseProgramSrc(t, "enum Maybe { Just }\n")
	s := expectOne[*EnumDecl](t, prog)
	if s.Name != "Maybe" {
		t.Errorf("name = %q, want %q", s.Name, "Maybe")
	}
	if len(s.Variants) != 1 || s.Variants[0].Name != "Just" {
		t.Errorf("variants = %+v, want [Just]", s.Variants)
	}
}

func TestParseEnumDeclMultipleVariants(t *testing.T) {
	prog := parseProgramSrc(t, "enum Color { Red, Green, Blue }\n")
	s := expectOne[*EnumDecl](t, prog)
	if len(s.Variants) != 3 {
		t.Fatalf("got %d variants, want 3", len(s.Variants))
	}
	for i, want := range []string{"Red", "Green", "Blue"} {
		if s.Variants[i].Name != want {
			t.Errorf("variant %d = %q, want %q", i, s.Variants[i].Name, want)
		}
	}
}

func TestParseEnumDeclTrailingComma(t *testing.T) {
	prog := parseProgramSrc(t, "enum Color { Red, Green, Blue, }\n")
	s := expectOne[*EnumDecl](t, prog)
	if len(s.Variants) != 3 {
		t.Errorf("trailing comma broke variant count = %d, want 3", len(s.Variants))
	}
}

// Forward references at parse time: struct A's field references B declared
// later in the file. The parser does not resolve names; it just records.
func TestParseStructEnumMixedOrderForwardRef(t *testing.T) {
	src := `struct A { b: B }
enum B { X, Y }
fn f() -> int { return 0 }
`
	prog := parseProgramSrc(t, src)
	if len(prog.Statements) != 3 {
		t.Fatalf("got %d statements, want 3", len(prog.Statements))
	}
	if _, ok := prog.Statements[0].(*StructDecl); !ok {
		t.Errorf("stmt 0 is %T, want *StructDecl", prog.Statements[0])
	}
	if _, ok := prog.Statements[1].(*EnumDecl); !ok {
		t.Errorf("stmt 1 is %T, want *EnumDecl", prog.Statements[1])
	}
	if _, ok := prog.Statements[2].(*FnDecl); !ok {
		t.Errorf("stmt 2 is %T, want *FnDecl", prog.Statements[2])
	}
}

// ---------------------------------------------------------------------------
// list literal.
// ---------------------------------------------------------------------------

// firstExprStmt extracts the LetStmt's value expression for the common
// "let x := <expr>" smoke pattern.
func firstExprStmt(t *testing.T, prog *Program) Expr {
	t.Helper()
	if len(prog.Statements) == 0 {
		t.Fatal("empty program")
	}
	let, ok := prog.Statements[0].(*LetStmt)
	if !ok {
		t.Fatalf("stmt 0 is %T, want *LetStmt", prog.Statements[0])
	}
	return let.Value
}

func TestParseListLitEmpty(t *testing.T) {
	prog := parseProgramSrc(t, "let xs := []\n")
	lit, ok := firstExprStmt(t, prog).(*ListLit)
	if !ok {
		t.Fatalf("value is %T, want *ListLit", firstExprStmt(t, prog))
	}
	if len(lit.Elements) != 0 {
		t.Errorf("got %d elements, want 0", len(lit.Elements))
	}
}

func TestParseListLitSingle(t *testing.T) {
	prog := parseProgramSrc(t, "let xs := [42]\n")
	lit := firstExprStmt(t, prog).(*ListLit)
	if len(lit.Elements) != 1 {
		t.Errorf("got %d elements, want 1", len(lit.Elements))
	}
}

func TestParseListLitMultiple(t *testing.T) {
	prog := parseProgramSrc(t, "let xs := [1, 2, 3]\n")
	lit := firstExprStmt(t, prog).(*ListLit)
	if len(lit.Elements) != 3 {
		t.Errorf("got %d elements, want 3", len(lit.Elements))
	}
}

func TestParseListLitTrailingComma(t *testing.T) {
	prog := parseProgramSrc(t, "let xs := [1, 2, 3,]\n")
	lit := firstExprStmt(t, prog).(*ListLit)
	if len(lit.Elements) != 3 {
		t.Errorf("trailing comma broke element count = %d, want 3", len(lit.Elements))
	}
}

func TestParseListLitNested(t *testing.T) {
	prog := parseProgramSrc(t, "let xs := [[1, 2], [3, 4]]\n")
	outer := firstExprStmt(t, prog).(*ListLit)
	if len(outer.Elements) != 2 {
		t.Fatalf("outer got %d, want 2", len(outer.Elements))
	}
	inner, ok := outer.Elements[0].(*ListLit)
	if !ok {
		t.Fatalf("inner 0 is %T, want *ListLit", outer.Elements[0])
	}
	if len(inner.Elements) != 2 {
		t.Errorf("inner got %d, want 2", len(inner.Elements))
	}
}

// ---------------------------------------------------------------------------
// tuple literal vs paren expression.
// ---------------------------------------------------------------------------

func TestParseTupleLit2(t *testing.T) {
	prog := parseProgramSrc(t, "let p := (1, 2)\n")
	tup, ok := firstExprStmt(t, prog).(*TupleLit)
	if !ok {
		t.Fatalf("value is %T, want *TupleLit", firstExprStmt(t, prog))
	}
	if len(tup.Elements) != 2 {
		t.Errorf("got %d elements, want 2", len(tup.Elements))
	}
}

func TestParseTupleLit3(t *testing.T) {
	prog := parseProgramSrc(t, "let p := (1, 2, 3)\n")
	tup := firstExprStmt(t, prog).(*TupleLit)
	if len(tup.Elements) != 3 {
		t.Errorf("got %d elements, want 3", len(tup.Elements))
	}
}

// `(a)` is grouping, not a 1-tuple. PLAN: 1-tuples deferred.
func TestParseSingleParenStaysParenExpr(t *testing.T) {
	prog := parseProgramSrc(t, "let p := (42)\n")
	if _, ok := firstExprStmt(t, prog).(*ParenExpr); !ok {
		t.Fatalf("value is %T, want *ParenExpr", firstExprStmt(t, prog))
	}
}

// `(a,)` — 1-tuple form is a parse error at v0.2.
func TestParseOneTupleRejected(t *testing.T) {
	expectParseErr(t, "let p := (42,)\n", "tuple literal requires at least 2 elements")
}

// ---------------------------------------------------------------------------
// struct literal.
// ---------------------------------------------------------------------------

func TestParseStructLitTwoFields(t *testing.T) {
	prog := parseProgramSrc(t, "let p := Point { x: 1, y: 2 }\n")
	lit, ok := firstExprStmt(t, prog).(*StructLit)
	if !ok {
		t.Fatalf("value is %T, want *StructLit", firstExprStmt(t, prog))
	}
	if lit.TypeName != "Point" {
		t.Errorf("type name = %q, want %q", lit.TypeName, "Point")
	}
	if len(lit.Fields) != 2 {
		t.Errorf("got %d field inits, want 2", len(lit.Fields))
	}
}

func TestParseStructLitTrailingComma(t *testing.T) {
	prog := parseProgramSrc(t, "let p := Point { x: 1, y: 2, }\n")
	lit := firstExprStmt(t, prog).(*StructLit)
	if len(lit.Fields) != 2 {
		t.Errorf("trailing comma broke field count = %d, want 2", len(lit.Fields))
	}
}

func TestParseStructLitEmpty(t *testing.T) {
	// Parser admits `Empty {}`; typeck rejects empty struct decls.
	prog := parseProgramSrc(t, "let p := Empty {}\n")
	lit, ok := firstExprStmt(t, prog).(*StructLit)
	if !ok {
		t.Fatalf("value is %T, want *StructLit", firstExprStmt(t, prog))
	}
	if len(lit.Fields) != 0 {
		t.Errorf("got %d field inits, want 0", len(lit.Fields))
	}
}

func TestParseStructLitInArgPosition(t *testing.T) {
	prog := parseProgramSrc(t, "draw(Point { x: 1, y: 2 })\n")
	stmt := expectOne[*ExprStmt](t, prog)
	call, ok := stmt.Expr.(*CallExpr)
	if !ok {
		t.Fatalf("expr is %T, want *CallExpr", stmt.Expr)
	}
	if len(call.Args) != 1 {
		t.Fatalf("got %d args, want 1", len(call.Args))
	}
	if _, ok := call.Args[0].(*StructLit); !ok {
		t.Errorf("arg 0 is %T, want *StructLit", call.Args[0])
	}
}

// PLAN: `if Point { x: 1, y: 2 } != other { ... }` parses correctly. The
// struct literal disambiguator must not consume the if-body brace.
func TestParseStructLitInIfCondition(t *testing.T) {
	src := `if Point { x: 1, y: 2 } == other {
nop
}
`
	prog := parseProgramSrc(t, src)
	ifs := expectOne[*IfStmt](t, prog)
	bin, ok := ifs.Cond.(*BinaryExpr)
	if !ok {
		t.Fatalf("if cond is %T, want *BinaryExpr", ifs.Cond)
	}
	if _, ok := bin.Left.(*StructLit); !ok {
		t.Errorf("if cond LHS is %T, want *StructLit", bin.Left)
	}
}

// PLAN: `if cond { ... }` with cond a bare ident must not be misread as a
// struct literal followed by garbage. The brace belongs to the if body.
func TestParseIfBodyNotConfusedWithStructLit(t *testing.T) {
	src := `if x {
nop
}
`
	prog := parseProgramSrc(t, src)
	ifs := expectOne[*IfStmt](t, prog)
	if _, ok := ifs.Cond.(*IdentExpr); !ok {
		t.Fatalf("if cond is %T, want *IdentExpr", ifs.Cond)
	}
	if len(ifs.Then.Statements) != 1 {
		t.Errorf("if body length = %d, want 1", len(ifs.Then.Statements))
	}
}

// ---------------------------------------------------------------------------
// enum variant access.
// ---------------------------------------------------------------------------

func TestParseEnumVariantAccess(t *testing.T) {
	prog := parseProgramSrc(t, "let c := Color.Red\n")
	fa, ok := firstExprStmt(t, prog).(*FieldAccessExpr)
	if !ok {
		t.Fatalf("value is %T, want *FieldAccessExpr", firstExprStmt(t, prog))
	}
	id, ok := fa.Receiver.(*IdentExpr)
	if !ok || id.Name != "Color" {
		t.Errorf("receiver = %+v, want IdentExpr{Color}", fa.Receiver)
	}
	if fa.FieldName != "Red" {
		t.Errorf("field name = %q, want %q", fa.FieldName, "Red")
	}
}

// ---------------------------------------------------------------------------
// indexing and slicing.
// ---------------------------------------------------------------------------

func TestParseIndexZero(t *testing.T) {
	prog := parseProgramSrc(t, "let v := xs[0]\n")
	idx, ok := firstExprStmt(t, prog).(*IndexExpr)
	if !ok {
		t.Fatalf("value is %T, want *IndexExpr", firstExprStmt(t, prog))
	}
	if _, ok := idx.Receiver.(*IdentExpr); !ok {
		t.Errorf("receiver is %T, want *IdentExpr", idx.Receiver)
	}
}

func TestParseIndexExpr(t *testing.T) {
	prog := parseProgramSrc(t, "let v := xs[i + 1]\n")
	idx := firstExprStmt(t, prog).(*IndexExpr)
	if _, ok := idx.Index.(*BinaryExpr); !ok {
		t.Errorf("index is %T, want *BinaryExpr", idx.Index)
	}
}

func TestParseIndexCallResult(t *testing.T) {
	prog := parseProgramSrc(t, "let v := xs[fn_call(j)]\n")
	idx := firstExprStmt(t, prog).(*IndexExpr)
	if _, ok := idx.Index.(*CallExpr); !ok {
		t.Errorf("index is %T, want *CallExpr", idx.Index)
	}
}

func TestParseSliceFull(t *testing.T) {
	prog := parseProgramSrc(t, "let v := xs[1..3]\n")
	sl, ok := firstExprStmt(t, prog).(*SliceExpr)
	if !ok {
		t.Fatalf("value is %T, want *SliceExpr", firstExprStmt(t, prog))
	}
	if sl.Low == nil || sl.High == nil {
		t.Errorf("Low/High = %v/%v, want both non-nil", sl.Low, sl.High)
	}
	if sl.Inclusive {
		t.Errorf("Inclusive = true, want false for `..`")
	}
}

func TestParseSliceInclusive(t *testing.T) {
	prog := parseProgramSrc(t, "let v := xs[1..=3]\n")
	sl := firstExprStmt(t, prog).(*SliceExpr)
	if !sl.Inclusive {
		t.Errorf("Inclusive = false, want true for `..=`")
	}
}

func TestParseSliceLowOmitted(t *testing.T) {
	prog := parseProgramSrc(t, "let v := xs[..3]\n")
	sl := firstExprStmt(t, prog).(*SliceExpr)
	if sl.Low != nil {
		t.Errorf("Low = %v, want nil", sl.Low)
	}
	if sl.High == nil {
		t.Errorf("High = nil, want non-nil")
	}
}

func TestParseSliceHighOmitted(t *testing.T) {
	prog := parseProgramSrc(t, "let v := xs[1..]\n")
	sl := firstExprStmt(t, prog).(*SliceExpr)
	if sl.Low == nil {
		t.Errorf("Low = nil, want non-nil")
	}
	if sl.High != nil {
		t.Errorf("High = %v, want nil", sl.High)
	}
}

func TestParseSliceFullCopy(t *testing.T) {
	prog := parseProgramSrc(t, "let v := xs[..]\n")
	sl := firstExprStmt(t, prog).(*SliceExpr)
	if sl.Low != nil || sl.High != nil {
		t.Errorf("Low/High = %v/%v, want both nil", sl.Low, sl.High)
	}
}

// ---------------------------------------------------------------------------
// chained postfix.
// ---------------------------------------------------------------------------

func TestParseChainedIndexFieldAccess(t *testing.T) {
	prog := parseProgramSrc(t, "let v := xs[0].field\n")
	fa, ok := firstExprStmt(t, prog).(*FieldAccessExpr)
	if !ok {
		t.Fatalf("value is %T, want *FieldAccessExpr", firstExprStmt(t, prog))
	}
	if _, ok := fa.Receiver.(*IndexExpr); !ok {
		t.Errorf("receiver is %T, want *IndexExpr", fa.Receiver)
	}
}

func TestParseChainedCallFieldAccess(t *testing.T) {
	prog := parseProgramSrc(t, "let v := f(x).field\n")
	fa := firstExprStmt(t, prog).(*FieldAccessExpr)
	if _, ok := fa.Receiver.(*CallExpr); !ok {
		t.Errorf("receiver is %T, want *CallExpr", fa.Receiver)
	}
}

func TestParseDoubleFieldAccess(t *testing.T) {
	prog := parseProgramSrc(t, "let v := p.x.y\n")
	outer, ok := firstExprStmt(t, prog).(*FieldAccessExpr)
	if !ok {
		t.Fatalf("value is %T, want *FieldAccessExpr", firstExprStmt(t, prog))
	}
	inner, ok := outer.Receiver.(*FieldAccessExpr)
	if !ok {
		t.Fatalf("inner receiver is %T, want *FieldAccessExpr", outer.Receiver)
	}
	if inner.FieldName != "x" || outer.FieldName != "y" {
		t.Errorf("got %q.%q, want x.y", inner.FieldName, outer.FieldName)
	}
}

func TestParseFieldThenCall(t *testing.T) {
	// v0.4 routes `expr DOT IDENT '('` to a MethodCallExpr so the impl
	// dispatch path can pick it up. The receiver is the leading expression
	// and the method name is the identifier after the dot. Prior to v0.4
	// this parsed as CallExpr{Callee: FieldAccessExpr{...}}; the postfix
	// rule changed but the source surface (`p.method()`) is identical.
	prog := parseProgramSrc(t, "let v := p.method()\n")
	mc, ok := firstExprStmt(t, prog).(*MethodCallExpr)
	if !ok {
		t.Fatalf("value is %T, want *MethodCallExpr (v0.4 method-call shape)", firstExprStmt(t, prog))
	}
	if id, ok := mc.Receiver.(*IdentExpr); !ok || id.Name != "p" {
		t.Errorf("receiver = %T %v, want IdentExpr p", mc.Receiver, mc.Receiver)
	}
	if mc.Method != "method" {
		t.Errorf("method = %q, want method", mc.Method)
	}
}

// ---------------------------------------------------------------------------
// match statement.
// ---------------------------------------------------------------------------

func TestParseMatchLiteralArms(t *testing.T) {
	src := `match x {
1 => nop
2 => nop
_ => nop
}
`
	prog := parseProgramSrc(t, src)
	m := expectOne[*MatchStmt](t, prog)
	if len(m.Arms) != 3 {
		t.Fatalf("got %d arms, want 3", len(m.Arms))
	}
	if _, ok := m.Arms[0].Pattern.(*LitPat); !ok {
		t.Errorf("arm 0 pattern is %T, want *LitPat", m.Arms[0].Pattern)
	}
	if _, ok := m.Arms[2].Pattern.(*WildcardPat); !ok {
		t.Errorf("arm 2 pattern is %T, want *WildcardPat", m.Arms[2].Pattern)
	}
}

func TestParseMatchBindGuard(t *testing.T) {
	src := `match x {
n if n > 0 => nop
_ => nop
}
`
	prog := parseProgramSrc(t, src)
	m := expectOne[*MatchStmt](t, prog)
	bind, ok := m.Arms[0].Pattern.(*BindPat)
	if !ok {
		t.Fatalf("arm 0 pattern is %T, want *BindPat", m.Arms[0].Pattern)
	}
	if bind.Name != "n" {
		t.Errorf("bind name = %q, want %q", bind.Name, "n")
	}
	if m.Arms[0].Guard == nil {
		t.Errorf("arm 0 guard = nil, want non-nil")
	}
}

func TestParseMatchTuplePattern(t *testing.T) {
	src := `match p {
(a, b) => nop
_ => nop
}
`
	prog := parseProgramSrc(t, src)
	m := expectOne[*MatchStmt](t, prog)
	tp, ok := m.Arms[0].Pattern.(*TuplePat)
	if !ok {
		t.Fatalf("arm 0 pattern is %T, want *TuplePat", m.Arms[0].Pattern)
	}
	if len(tp.Elements) != 2 {
		t.Errorf("tuple elements = %d, want 2", len(tp.Elements))
	}
}

func TestParseMatchStructPattern(t *testing.T) {
	src := `match p {
Point { x: 0, y } => nop
_ => nop
}
`
	prog := parseProgramSrc(t, src)
	m := expectOne[*MatchStmt](t, prog)
	sp, ok := m.Arms[0].Pattern.(*StructPat)
	if !ok {
		t.Fatalf("arm 0 pattern is %T, want *StructPat", m.Arms[0].Pattern)
	}
	if sp.TypeName != "Point" {
		t.Errorf("type name = %q, want %q", sp.TypeName, "Point")
	}
	if len(sp.Fields) != 2 {
		t.Fatalf("fields = %d, want 2", len(sp.Fields))
	}
	// Field 0 is `x: 0` — sub-pattern is a LitPat.
	if _, ok := sp.Fields[0].Pattern.(*LitPat); !ok {
		t.Errorf("field 0 sub-pattern is %T, want *LitPat", sp.Fields[0].Pattern)
	}
	// Field 1 is `y` shorthand, desugared to BindPat{y}.
	bp, ok := sp.Fields[1].Pattern.(*BindPat)
	if !ok || bp.Name != "y" {
		t.Errorf("field 1 sub-pattern = %+v, want BindPat{y}", sp.Fields[1].Pattern)
	}
}

func TestParseMatchStructPatternWithRest(t *testing.T) {
	src := `match p {
Point { x, .. } => nop
}
`
	prog := parseProgramSrc(t, src)
	m := expectOne[*MatchStmt](t, prog)
	sp := m.Arms[0].Pattern.(*StructPat)
	if !sp.Rest {
		t.Errorf("Rest = false, want true")
	}
	if len(sp.Fields) != 1 {
		t.Errorf("fields = %d, want 1", len(sp.Fields))
	}
}

func TestParseMatchEnumPattern(t *testing.T) {
	src := `match c {
Color.Red => nop
Color.Green => nop
_ => nop
}
`
	prog := parseProgramSrc(t, src)
	m := expectOne[*MatchStmt](t, prog)
	if len(m.Arms) != 3 {
		t.Fatalf("got %d arms, want 3", len(m.Arms))
	}
	ep, ok := m.Arms[0].Pattern.(*EnumPat)
	if !ok {
		t.Fatalf("arm 0 pattern is %T, want *EnumPat", m.Arms[0].Pattern)
	}
	if ep.TypeName != "Color" || ep.VariantName != "Red" {
		t.Errorf("got %s.%s, want Color.Red", ep.TypeName, ep.VariantName)
	}
}

func TestParseMatchArmBraceBlock(t *testing.T) {
	src := `match x {
1 => {
print x
nop
}
_ => nop
}
`
	prog := parseProgramSrc(t, src)
	m := expectOne[*MatchStmt](t, prog)
	if len(m.Arms[0].Body.Statements) != 2 {
		t.Errorf("brace-block arm body length = %d, want 2", len(m.Arms[0].Body.Statements))
	}
	if len(m.Arms[1].Body.Statements) != 1 {
		t.Errorf("single-statement arm body length = %d, want 1", len(m.Arms[1].Body.Statements))
	}
}

func TestParseNestedMatch(t *testing.T) {
	src := `match x {
1 => match y {
2 => nop
_ => nop
}
_ => nop
}
`
	prog := parseProgramSrc(t, src)
	m := expectOne[*MatchStmt](t, prog)
	inner, ok := m.Arms[0].Body.Statements[0].(*MatchStmt)
	if !ok {
		t.Fatalf("nested arm body 0 is %T, want *MatchStmt", m.Arms[0].Body.Statements[0])
	}
	if len(inner.Arms) != 2 {
		t.Errorf("nested match arms = %d, want 2", len(inner.Arms))
	}
}

func TestParseMatchSingleArmMinimal(t *testing.T) {
	prog := parseProgramSrc(t, "match x { _ => nop }\n")
	m := expectOne[*MatchStmt](t, prog)
	if len(m.Arms) != 1 {
		t.Errorf("got %d arms, want 1", len(m.Arms))
	}
}

// ---------------------------------------------------------------------------
// rune literals in expression position.
// ---------------------------------------------------------------------------

func TestParseRuneLitInLet(t *testing.T) {
	prog := parseProgramSrc(t, "let c := 'A'\n")
	rl, ok := firstExprStmt(t, prog).(*RuneLit)
	if !ok {
		t.Fatalf("value is %T, want *RuneLit", firstExprStmt(t, prog))
	}
	if rl.Value != 65 {
		t.Errorf("value = %d, want 65", rl.Value)
	}
}

// ---------------------------------------------------------------------------
// range still rejected outside slice + for-in.
// ---------------------------------------------------------------------------

func TestParseRangeOutsideSliceStillRejected(t *testing.T) {
	expectParseErr(t, "let r := 0..10\n", "range expressions")
}

// ---------------------------------------------------------------------------
// pattern parser failures.
// ---------------------------------------------------------------------------

func TestParseMatchPatternArithRejected(t *testing.T) {
	// `1 + 1 => …` — a binary expression in pattern position must fail.
	// The pattern parser recognises `1` as a literal pattern; the trailing
	// `+` then fails the `=>` expectation.
	expectParseErr(t, "match x {\n1 + 1 => nop\n}\n", "expected '=>'")
}

func TestParseMatchGuardMissingExpr(t *testing.T) {
	// `_ if =>` — guard expression missing.
	expectParseErr(t, "match x {\n_ if => nop\n}\n", "expected expression")
}

// ---------------------------------------------------------------------------
// integration sanity: a v0.2-shaped program parses end-to-end.
// ---------------------------------------------------------------------------

func TestParseMatchOverEnumProgramShape(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
let c := Color.Red
match c {
Color.Red => nop
Color.Green => nop
Color.Blue => nop
}
`
	prog := parseProgramSrc(t, src)
	if len(prog.Statements) != 3 {
		t.Fatalf("got %d statements, want 3", len(prog.Statements))
	}
}

// PLAN: empty struct lit `Point {}` accepted at parse time.
func TestParseEmptyStructLitAccepted(t *testing.T) {
	prog := parseProgramSrc(t, "let p := Foo {}\n")
	if _, ok := firstExprStmt(t, prog).(*StructLit); !ok {
		t.Fatalf("value is %T, want *StructLit", firstExprStmt(t, prog))
	}
}

// Sanity: `Point.x` (field access via dot) and `Color.Red` (enum variant
// access) parse identically — typeck disambiguates by the receiver's type.
func TestParseDotAccessAlwaysFieldAccessExpr(t *testing.T) {
	for _, src := range []string{
		"let v := p.x\n",
		"let c := Color.Red\n",
	} {
		prog := parseProgramSrc(t, src)
		if _, ok := firstExprStmt(t, prog).(*FieldAccessExpr); !ok {
			t.Errorf("%q: value is %T, want *FieldAccessExpr", src, firstExprStmt(t, prog))
		}
	}
}

// PLAN: `..=` in a slice without an upper bound is an error.
func TestParseInclusiveSliceNoUpperBoundRejected(t *testing.T) {
	expectParseErr(t, "let v := xs[..=]\n", "'..=' requires an upper bound")
}

// Pattern parser: bare string literal pattern.
func TestParseMatchStringLitPattern(t *testing.T) {
	src := `match s {
"hello" => nop
_ => nop
}
`
	prog := parseProgramSrc(t, src)
	m := expectOne[*MatchStmt](t, prog)
	lp, ok := m.Arms[0].Pattern.(*LitPat)
	if !ok {
		t.Fatalf("arm 0 pattern is %T, want *LitPat", m.Arms[0].Pattern)
	}
	sl, ok := lp.Lit.(*StringLit)
	if !ok || sl.Value != "hello" {
		t.Errorf("lit = %+v, want StringLit(\"hello\")", lp.Lit)
	}
}

// Pattern parser: negative numeric literal pattern.
func TestParseMatchNegativeIntPattern(t *testing.T) {
	src := `match n {
-1 => nop
_ => nop
}
`
	prog := parseProgramSrc(t, src)
	m := expectOne[*MatchStmt](t, prog)
	lp, ok := m.Arms[0].Pattern.(*LitPat)
	if !ok {
		t.Fatalf("arm 0 pattern is %T, want *LitPat", m.Arms[0].Pattern)
	}
	un, ok := lp.Lit.(*UnaryExpr)
	if !ok || un.Op != UnaryNeg {
		t.Errorf("lit = %+v, want UnaryExpr{-, ...}", lp.Lit)
	}
}

// TypeRef compound shapes lex and parse for `let xs: list[int] = []`.
func TestParseListTypeRefAnnotation(t *testing.T) {
	prog := parseProgramSrc(t, "let xs: list[int] = []\n")
	let := prog.Statements[0].(*LetStmt)
	if let.Type == nil || let.Type.Kind != TypeRefList {
		t.Fatalf("type ref = %+v, want TypeRefList", let.Type)
	}
	if let.Type.Element == nil || let.Type.Element.Name != "int" {
		t.Errorf("element type = %+v, want named 'int'", let.Type.Element)
	}
}

func TestParseTupleTypeRefAnnotation(t *testing.T) {
	prog := parseProgramSrc(t, "let p: tuple[int, str] = (1, \"x\")\n")
	let := prog.Statements[0].(*LetStmt)
	if let.Type == nil || let.Type.Kind != TypeRefTuple {
		t.Fatalf("type ref = %+v, want TypeRefTuple", let.Type)
	}
	if len(let.Type.Elements) != 2 {
		t.Errorf("tuple elements = %d, want 2", len(let.Type.Elements))
	}
}

func TestParseListTypeRefStringRoundTrip(t *testing.T) {
	prog := parseProgramSrc(t, "let xs: list[list[int]] = []\n")
	let := prog.Statements[0].(*LetStmt)
	got := let.Type.String()
	want := "list[list[int]]"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Surprises and edge cases worth pinning.
// ---------------------------------------------------------------------------

// `Point {}` followed by `==` other should still parse as a comparison —
// confirms the empty-struct disambiguator does not eat the comparison.
func TestParseEmptyStructLitComparison(t *testing.T) {
	prog := parseProgramSrc(t, "let b := Point {} == other\n")
	bin, ok := firstExprStmt(t, prog).(*BinaryExpr)
	if !ok {
		t.Fatalf("value is %T, want *BinaryExpr", firstExprStmt(t, prog))
	}
	if _, ok := bin.Left.(*StructLit); !ok {
		t.Errorf("LHS is %T, want *StructLit", bin.Left)
	}
}

// Match with newline-spanning tuple pattern: parens silence newlines.
func TestParseMatchTupleSpanningLines(t *testing.T) {
	src := `match p {
(
a,
b
) => nop
_ => nop
}
`
	prog := parseProgramSrc(t, src)
	m := expectOne[*MatchStmt](t, prog)
	if _, ok := m.Arms[0].Pattern.(*TuplePat); !ok {
		t.Errorf("arm 0 pattern is %T, want *TuplePat", m.Arms[0].Pattern)
	}
}

// Verify the `=>` token is lexed as KindFatArrow (not KindAssign + KindGT).
func TestLexFatArrowSingleToken(t *testing.T) {
	tokens, err := Lex([]byte("=>"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("got %d tokens, want 2", len(tokens))
	}
	if tokens[0].Kind != KindFatArrow {
		t.Errorf("kind = %v, want KindFatArrow", tokens[0].Kind)
	}
}

// Verify struct/enum/match keywords lex.
func TestLexCompositeKeywords(t *testing.T) {
	cases := map[string]Kind{
		"struct": KindStruct,
		"enum":   KindEnum,
		"match":  KindMatch,
	}
	for src, want := range cases {
		tokens, err := Lex([]byte(src))
		if err != nil {
			t.Fatalf("Lex(%q): %v", src, err)
		}
		if tokens[0].Kind != want {
			t.Errorf("Lex(%q)[0] kind = %v, want %v", src, tokens[0].Kind, want)
		}
	}
}

// Sanity: ensure parser_v02 reaches the right error stream even when the
// input is partial enough that the REPL would call it incomplete.
func TestParseUnterminatedMatchIsIncomplete(t *testing.T) {
	tokens, err := Lex([]byte("match x {\n_ => nop\n"))
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	_, err = Parse(tokens)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	pe, ok := err.(*ParseError)
	if !ok {
		t.Fatalf("error is %T, want *ParseError", err)
	}
	if !pe.IsIncomplete() {
		t.Errorf("Incomplete = false, want true on unterminated match")
	}
	if !strings.Contains(pe.Error(), "match") {
		t.Errorf("error %q does not mention match", pe.Error())
	}
}

// ---------------------------------------------------------------------------
// v0.2 Unit 3.5 — `for x in xs` (list iteration) parser surface.
//
// The range form `for i in 0..n` continues to parse to ForRange; the new
// list-iter form parses to ForIter with Iter holding the iterable expression.
// Disambiguation is purely on the token following the head expression — the
// parser stays liberal about what shape the iterable takes (typeck enforces
// list).
// ---------------------------------------------------------------------------

func TestParseForListIterSimple(t *testing.T) {
	prog := parseProgramSrc(t, "for x in xs { nop }\n")
	fs := expectOne[*ForStmt](t, prog)
	if fs.Kind != ForIter {
		t.Fatalf("kind = %v, want ForIter", fs.Kind)
	}
	if fs.Var != "x" {
		t.Errorf("var = %q, want %q", fs.Var, "x")
	}
	if fs.Iter == nil {
		t.Fatal("Iter = nil")
	}
	if id, ok := fs.Iter.(*IdentExpr); !ok || id.Name != "xs" {
		t.Errorf("Iter = %#v, want IdentExpr{xs}", fs.Iter)
	}
	if fs.Range != nil {
		t.Errorf("Range should be nil for ForIter, got %#v", fs.Range)
	}
}

func TestParseForListIterOverListLiteral(t *testing.T) {
	// Parser stays liberal: any expression is acceptable as the iterable.
	prog := parseProgramSrc(t, "for x in [1, 2, 3] { print x }\n")
	fs := expectOne[*ForStmt](t, prog)
	if fs.Kind != ForIter {
		t.Fatalf("kind = %v, want ForIter", fs.Kind)
	}
	if _, ok := fs.Iter.(*ListLit); !ok {
		t.Errorf("Iter = %T, want *ListLit", fs.Iter)
	}
}

func TestParseForListIterOverCall(t *testing.T) {
	// Iterable can be any expression; `make_list()` parses fine here.
	prog := parseProgramSrc(t, "for x in make_list() { print x }\n")
	fs := expectOne[*ForStmt](t, prog)
	if fs.Kind != ForIter {
		t.Fatalf("kind = %v, want ForIter", fs.Kind)
	}
	if _, ok := fs.Iter.(*CallExpr); !ok {
		t.Errorf("Iter = %T, want *CallExpr", fs.Iter)
	}
}

func TestParseForRangeStillParsesAsRange(t *testing.T) {
	// Existing v0.1 form must still produce ForRange — disambiguation is
	// driven by what follows the start expression.
	prog := parseProgramSrc(t, "for i in 0..n { nop }\n")
	fs := expectOne[*ForStmt](t, prog)
	if fs.Kind != ForRange {
		t.Fatalf("kind = %v, want ForRange", fs.Kind)
	}
	if fs.Iter != nil {
		t.Errorf("Iter should be nil for ForRange, got %#v", fs.Iter)
	}
}

func TestParseForRangeInclusiveStillRange(t *testing.T) {
	prog := parseProgramSrc(t, "for i in 1..=5 { nop }\n")
	fs := expectOne[*ForStmt](t, prog)
	if fs.Kind != ForRange {
		t.Fatalf("kind = %v, want ForRange", fs.Kind)
	}
	if fs.Range == nil || !fs.Range.Inclusive {
		t.Errorf("range = %#v, want inclusive", fs.Range)
	}
}

// ---------------------------------------------------------------------------
// v0.2 Unit 3.5 — `let (a, b) := pair` (tuple destructure) parser surface.
//
// Parser populates LetStmt.Tuple (and MutStmt.Tuple) when the LHS is
// parenthesised; Name stays empty in that case. Annotated destructure is
// rejected at parse time — typeck never has to handle it.
// ---------------------------------------------------------------------------

func TestParseLetTupleDestructureTwo(t *testing.T) {
	prog := parseProgramSrc(t, "let (a, b) := (1, 2)\n")
	s := expectOne[*LetStmt](t, prog)
	if s.Tuple == nil {
		t.Fatal("Tuple = nil, want non-nil destructure binding")
	}
	if s.Name != "" {
		t.Errorf("Name = %q, want empty for destructure form", s.Name)
	}
	if got, want := s.Tuple.Names, []string{"a", "b"}; !equalStrSlice(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
}

func TestParseLetTupleDestructureThree(t *testing.T) {
	prog := parseProgramSrc(t, "let (a, b, c) := (1, 2, 3)\n")
	s := expectOne[*LetStmt](t, prog)
	if s.Tuple == nil || len(s.Tuple.Names) != 3 {
		t.Fatalf("Tuple = %#v, want 3 names", s.Tuple)
	}
}

func TestParseLetTupleDestructureTrailingComma(t *testing.T) {
	prog := parseProgramSrc(t, "let (a, b,) := (1, 2)\n")
	s := expectOne[*LetStmt](t, prog)
	if s.Tuple == nil || len(s.Tuple.Names) != 2 {
		t.Fatalf("Tuple = %#v, want 2 names", s.Tuple)
	}
}

func TestParseMutTupleDestructure(t *testing.T) {
	prog := parseProgramSrc(t, "mut (x, y) := (1, 2)\n")
	s := expectOne[*MutStmt](t, prog)
	if s.Tuple == nil || len(s.Tuple.Names) != 2 {
		t.Fatalf("Tuple = %#v, want 2 names", s.Tuple)
	}
}

func TestParseConstTupleDestructureParses(t *testing.T) {
	// Parser admits the form; typeck rejects it (composites aren't
	// const-evaluable at v0.2). We assert parse success here so the
	// diagnostic flow lives in typeck where it belongs.
	prog := parseProgramSrc(t, "const (a, b) := (1, 2)\n")
	s := expectOne[*ConstStmt](t, prog)
	if s.Tuple == nil {
		t.Fatal("Tuple = nil, expected destructure binding")
	}
}

func TestParseLetTupleDestructureRejectsRepeatedName(t *testing.T) {
	expectParseErr(t, "let (a, a) := (1, 2)\n", "repeated")
}

func TestParseLetTupleDestructureRejectsSingleName(t *testing.T) {
	// `let (a) := …` would shadow the ParenExpr-grouping rule for
	// expressions; we keep destructure ≥ 2 names so the form is unambiguous.
	expectParseErr(t, "let (a) := 1\n", "at least 2 names")
}

func TestParseLetTupleDestructureRejectsAnnotation(t *testing.T) {
	// v0.2 doesn't support annotated destructure; the diagnostic fires in
	// the parser so typeck doesn't have to learn this case.
	expectParseErr(t, "let (a, b): tuple[int, int] = (1, 2)\n", "annotations on destructure")
}

func TestParseLetTupleDestructureRequiresWalrus(t *testing.T) {
	// `let (a, b) = (1, 2)` (plain `=`) is rejected — destructure uses `:=`.
	expectParseErr(t, "let (a, b) = (1, 2)\n", "expected ':='")
}

// equalStrSlice is a tiny local helper used only by the destructure tests
// above. We keep it here rather than promoting it to parser_test.go because
// no other test in the package needs it today.
func equalStrSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
