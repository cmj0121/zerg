package syntax

import "testing"

// ---------------------------------------------------------------------------
// v0.7 Unit 1b — parser tests for chan[T] constructors, send/recv operators,
// and the bare `<- ch` discard form.
//
// Unit 1b is parser-only: typeck / interpreter / codegen do not yet consume
// the new ChanConstructorExpr / SendStmt / RecvExpr nodes (Units 2 / 6 / 7
// do). These tests pin the lexer / AST plumbing for `<-` and the rejection
// diagnostics for the malformed shapes the parser owns. Existing v0.0–v0.6
// corpora continue to parse with no new node types appearing in their trees.
// ---------------------------------------------------------------------------

// --- chan[T](...) constructor --------------------------------------------

func TestParseChanConstructorUnbuffered(t *testing.T) {
	prog := parseProgramSrc(t, "let ch := chan[int]()\n")
	s := expectOne[*LetStmt](t, prog)
	cc, ok := s.Value.(*ChanConstructorExpr)
	if !ok {
		t.Fatalf("value = %T, want *ChanConstructorExpr", s.Value)
	}
	if cc.Element == nil || cc.Element.Name != "int" {
		t.Errorf("element = %v, want TypeRef{int}", cc.Element)
	}
	if cc.Capacity != nil {
		t.Errorf("capacity = %v, want nil (unbuffered)", cc.Capacity)
	}
}

func TestParseChanConstructorBuffered(t *testing.T) {
	prog := parseProgramSrc(t, "let ch := chan[str](10)\n")
	s := expectOne[*LetStmt](t, prog)
	cc, ok := s.Value.(*ChanConstructorExpr)
	if !ok {
		t.Fatalf("value = %T, want *ChanConstructorExpr", s.Value)
	}
	if cc.Element == nil || cc.Element.Name != "str" {
		t.Errorf("element = %v, want TypeRef{str}", cc.Element)
	}
	cap, ok := cc.Capacity.(*IntLit)
	if !ok || cap.Text != "10" {
		t.Errorf("capacity = %v, want IntLit{10}", cc.Capacity)
	}
}

func TestParseChanConstructorBufferedExprCapacity(t *testing.T) {
	// Capacity may be any expression; typeck (Unit 2) enforces int. The
	// parser stays liberal so `chan[int](n + 1)` is admitted here.
	prog := parseProgramSrc(t, "fn make(n: int) { let ch := chan[int](n + 1) }\n")
	fn := expectOne[*FnDecl](t, prog)
	if len(fn.Body.Statements) != 1 {
		t.Fatalf("body has %d stmts, want 1", len(fn.Body.Statements))
	}
	let := fn.Body.Statements[0].(*LetStmt)
	cc := let.Value.(*ChanConstructorExpr)
	if _, ok := cc.Capacity.(*BinaryExpr); !ok {
		t.Errorf("capacity = %T, want *BinaryExpr", cc.Capacity)
	}
}

func TestParseChanTypeInLetAnnotation(t *testing.T) {
	// `let ch: chan[int] := ...` — chan as a type position. Routes through
	// parseTypeRef as a TypeRefNamed with TypeArgs (the same path that
	// admits Box[int], Result[int, str]).
	prog := parseProgramSrc(t, "let ch: chan[int] = chan[int]()\n")
	s := expectOne[*LetStmt](t, prog)
	if s.Type == nil || s.Type.Name != "chan" {
		t.Fatalf("type = %v, want TypeRef{chan}", s.Type)
	}
	if len(s.Type.TypeArgs) != 1 || s.Type.TypeArgs[0].Name != "int" {
		t.Errorf("type args = %v, want [int]", s.Type.TypeArgs)
	}
}

func TestParseChanRejectMultipleTypeArgs(t *testing.T) {
	expectParseErr(t,
		"let ch := chan[int, str]()\n",
		"chan[T] takes exactly one type argument",
	)
}

func TestParseChanRejectMultipleCapArgs(t *testing.T) {
	expectParseErr(t,
		"let ch := chan[int](10, 20)\n",
		"chan constructor takes at most one capacity argument",
	)
}

func TestParseChanRejectMissingParens(t *testing.T) {
	// `chan[int]` standalone in expression position is a type, not a value.
	// The parser commits to a constructor on `chan [` and then expects `(`.
	expectParseErr(t,
		"let ch := chan[int]\n",
		"expected '('",
	)
}

// --- send statement -------------------------------------------------------

func TestParseSendStmt(t *testing.T) {
	prog := parseProgramSrc(t, "fn run(ch: chan[int]) { ch <- 5 }\n")
	fn := expectOne[*FnDecl](t, prog)
	if len(fn.Body.Statements) != 1 {
		t.Fatalf("body has %d stmts, want 1", len(fn.Body.Statements))
	}
	send, ok := fn.Body.Statements[0].(*SendStmt)
	if !ok {
		t.Fatalf("stmt = %T, want *SendStmt", fn.Body.Statements[0])
	}
	id, ok := send.Chan.(*IdentExpr)
	if !ok || id.Name != "ch" {
		t.Errorf("chan = %v, want IdentExpr{ch}", send.Chan)
	}
	lit, ok := send.Value.(*IntLit)
	if !ok || lit.Text != "5" {
		t.Errorf("value = %v, want IntLit{5}", send.Value)
	}
}

func TestParseSendStmtComplexValue(t *testing.T) {
	// The RHS is a full expression — `ch <- f(x) + 1` parses as
	// `ch <- (f(x) + 1)` because parseExpr binds tighter than the SendStmt
	// detector.
	prog := parseProgramSrc(t, "fn run(ch: chan[int], x: int) { ch <- f(x) + 1 }\n")
	fn := expectOne[*FnDecl](t, prog)
	send := fn.Body.Statements[0].(*SendStmt)
	if _, ok := send.Value.(*BinaryExpr); !ok {
		t.Errorf("value = %T, want *BinaryExpr", send.Value)
	}
}

func TestParseSendRejectChained(t *testing.T) {
	// PLAN.md: chained `<-` rejects at parse time so users split with parens.
	expectParseErr(t,
		"fn run(a: chan[int], b: chan[int]) { a <- 2 <- 3 }\n",
		"chained '<-' is not allowed",
	)
}

// --- receive expression ---------------------------------------------------

func TestParseRecvExprInLet(t *testing.T) {
	prog := parseProgramSrc(t, "fn run(ch: chan[int]) { let v := <- ch }\n")
	fn := expectOne[*FnDecl](t, prog)
	let, ok := fn.Body.Statements[0].(*LetStmt)
	if !ok {
		t.Fatalf("stmt = %T, want *LetStmt", fn.Body.Statements[0])
	}
	rv, ok := let.Value.(*RecvExpr)
	if !ok {
		t.Fatalf("value = %T, want *RecvExpr", let.Value)
	}
	id, ok := rv.Chan.(*IdentExpr)
	if !ok || id.Name != "ch" {
		t.Errorf("chan = %v, want IdentExpr{ch}", rv.Chan)
	}
}

func TestParseRecvExprAsDiscardStmt(t *testing.T) {
	// `<- ch` at statement position is a receive-discard: the value is dropped
	// but the side effect (advancing the channel) is meaningful.
	prog := parseProgramSrc(t, "fn run(ch: chan[int]) { <- ch }\n")
	fn := expectOne[*FnDecl](t, prog)
	es, ok := fn.Body.Statements[0].(*ExprStmt)
	if !ok {
		t.Fatalf("stmt = %T, want *ExprStmt", fn.Body.Statements[0])
	}
	if _, ok := es.Expr.(*RecvExpr); !ok {
		t.Errorf("expr = %T, want *RecvExpr", es.Expr)
	}
}

func TestParseRecvExprPrecedenceWithPlus(t *testing.T) {
	// PLAN.md pin: `<- 1 + 2` parses as `(<- 1) + 2` because `<-` is a
	// prefix unary at the same precedence rung as `-` / `~`. Typeck rejects
	// receiving on an int; the parser only records the shape.
	prog := parseProgramSrc(t, "fn run() { let v := <- a + 2 }\n")
	fn := expectOne[*FnDecl](t, prog)
	let := fn.Body.Statements[0].(*LetStmt)
	bin, ok := let.Value.(*BinaryExpr)
	if !ok {
		t.Fatalf("value = %T, want *BinaryExpr", let.Value)
	}
	if _, ok := bin.Left.(*RecvExpr); !ok {
		t.Errorf("left = %T, want *RecvExpr", bin.Left)
	}
}

// --- close(ch) -- regular call --------------------------------------------

func TestParseCloseIsRegularCall(t *testing.T) {
	// `close(ch)` is a built-in fn at typeck (Unit 2); the parser sees a
	// vanilla CallExpr with callee IdentExpr{close} — no special parser path.
	prog := parseProgramSrc(t, "fn run(ch: chan[int]) { close(ch) }\n")
	fn := expectOne[*FnDecl](t, prog)
	es, ok := fn.Body.Statements[0].(*ExprStmt)
	if !ok {
		t.Fatalf("stmt = %T, want *ExprStmt", fn.Body.Statements[0])
	}
	call, ok := es.Expr.(*CallExpr)
	if !ok {
		t.Fatalf("expr = %T, want *CallExpr", es.Expr)
	}
	id, ok := call.Callee.(*IdentExpr)
	if !ok || id.Name != "close" {
		t.Errorf("callee = %v, want IdentExpr{close}", call.Callee)
	}
}

// --- for v in ch ----------------------------------------------------------

func TestParseForInChan(t *testing.T) {
	// `for v in ch` reuses the existing ForIter parse path — the parser does
	// not look at the type of the iterable. typeck (Unit 2) detects chan[T]
	// and applies the chan-specific desugaring.
	prog := parseProgramSrc(t, "fn run(ch: chan[int]) { for v in ch { print v } }\n")
	fn := expectOne[*FnDecl](t, prog)
	for_, ok := fn.Body.Statements[0].(*ForStmt)
	if !ok {
		t.Fatalf("stmt = %T, want *ForStmt", fn.Body.Statements[0])
	}
	if for_.Kind != ForIter {
		t.Errorf("kind = %v, want ForIter", for_.Kind)
	}
	if for_.Var != "v" {
		t.Errorf("var = %q, want v", for_.Var)
	}
	id, ok := for_.Iter.(*IdentExpr)
	if !ok || id.Name != "ch" {
		t.Errorf("iter = %v, want IdentExpr{ch}", for_.Iter)
	}
}

// --- lexer ----------------------------------------------------------------

func TestLexLArrow(t *testing.T) {
	got := kindsOf(t, "<-")
	if len(got) != 1 || got[0] != KindLArrow {
		t.Errorf("got %v, want [KindLArrow]", got)
	}
}

func TestLexLArrowDisambiguation(t *testing.T) {
	// `<` `<-` `<=` `<<` `<<=` must each lex as their own single token; the
	// longest-match rule across all of them is the v0.7 invariant.
	cases := []struct {
		src  string
		want []Kind
	}{
		{"<", []Kind{KindLT}},
		{"<-", []Kind{KindLArrow}},
		{"<=", []Kind{KindLE}},
		{"<<", []Kind{KindShl}},
		{"<<=", []Kind{KindShlEq}},
		// `< -` (with whitespace) is two tokens, NOT KindLArrow — same rule
		// that splits `: =` from `:=`.
		{"< -", []Kind{KindLT, KindMinus}},
		// `<--` is `<-` followed by `-` (longest-match consumes the `<-` first).
		{"<--", []Kind{KindLArrow, KindMinus}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.src, func(t *testing.T) {
			got := kindsOf(t, c.src)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("token %d = %v, want %v", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestLexLArrowKindString(t *testing.T) {
	if got := KindLArrow.String(); got != "'<-'" {
		t.Errorf("KindLArrow.String() = %q, want %q", got, "'<-'")
	}
}

// --- chan-as-identifier still admitted -----------------------------------

func TestParseChanAsBareIdentInExprPosition(t *testing.T) {
	// PLAN.md: `chan` outside a type position with no `[` after it is a
	// regular IDENT — the parser admits it. typeck rejects the use because
	// `chan` is reserved for the built-in, but the parser stays syntactic.
	prog := parseProgramSrc(t, "fn run() { print chan }\n")
	fn := expectOne[*FnDecl](t, prog)
	ps, ok := fn.Body.Statements[0].(*PrintStmt)
	if !ok {
		t.Fatalf("stmt = %T, want *PrintStmt", fn.Body.Statements[0])
	}
	id, ok := ps.Expr.(*IdentExpr)
	if !ok || id.Name != "chan" {
		t.Errorf("expr = %v, want IdentExpr{chan}", ps.Expr)
	}
}
