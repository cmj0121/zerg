package syntax

import "testing"

// ---------------------------------------------------------------------------
// v0.7 Unit 1c — parser tests for the `select` statement.
//
// Unit 1c is parser-only: typeck (Unit 4) and interpreter / codegen (Units 6
// / 7) do not yet consume SelectStmt. These tests pin the AST plumbing and
// the rejection diagnostics for malformed select shapes. Existing v0.0–v0.6
// corpora continue to parse with no SelectStmt nodes appearing in their
// trees — the regression tests in the prior parser suites already lock that
// in.
// ---------------------------------------------------------------------------

// --- single-arm shapes ----------------------------------------------------

func TestParseSelectRecvBindArm(t *testing.T) {
	src := "fn f() {\n" +
		"    select {\n" +
		"        x := <- ch -> { print x }\n" +
		"    }\n" +
		"}\n"
	prog := parseProgramSrc(t, src)
	fn := expectOne[*FnDecl](t, prog)
	sel := fn.Body.Statements[0].(*SelectStmt)
	if len(sel.Arms) != 1 {
		t.Fatalf("got %d arms, want 1", len(sel.Arms))
	}
	arm := sel.Arms[0]
	if arm.Op != SelectRecvBind {
		t.Errorf("op = %v, want SelectRecvBind", arm.Op)
	}
	if arm.BindName != "x" {
		t.Errorf("bind name = %q, want x", arm.BindName)
	}
	if id, ok := arm.Chan.(*IdentExpr); !ok || id.Name != "ch" {
		t.Errorf("chan = %v, want IdentExpr{ch}", arm.Chan)
	}
	if arm.Body == nil || len(arm.Body.Statements) != 1 {
		t.Fatalf("body = %v, want 1 stmt", arm.Body)
	}
}

func TestParseSelectRecvDiscardArm(t *testing.T) {
	src := "fn f() {\n" +
		"    select {\n" +
		"        <- ch -> { nop }\n" +
		"    }\n" +
		"}\n"
	prog := parseProgramSrc(t, src)
	fn := expectOne[*FnDecl](t, prog)
	sel := fn.Body.Statements[0].(*SelectStmt)
	arm := sel.Arms[0]
	if arm.Op != SelectRecvDiscard {
		t.Errorf("op = %v, want SelectRecvDiscard", arm.Op)
	}
	if arm.BindName != "" {
		t.Errorf("bind name = %q, want empty", arm.BindName)
	}
	if id, ok := arm.Chan.(*IdentExpr); !ok || id.Name != "ch" {
		t.Errorf("chan = %v, want IdentExpr{ch}", arm.Chan)
	}
	if arm.Value != nil {
		t.Errorf("value = %v, want nil", arm.Value)
	}
}

func TestParseSelectSendArm(t *testing.T) {
	src := "fn f() {\n" +
		"    select {\n" +
		"        ch <- 5 -> { print \"sent\" }\n" +
		"    }\n" +
		"}\n"
	prog := parseProgramSrc(t, src)
	fn := expectOne[*FnDecl](t, prog)
	sel := fn.Body.Statements[0].(*SelectStmt)
	arm := sel.Arms[0]
	if arm.Op != SelectSend {
		t.Errorf("op = %v, want SelectSend", arm.Op)
	}
	if id, ok := arm.Chan.(*IdentExpr); !ok || id.Name != "ch" {
		t.Errorf("chan = %v, want IdentExpr{ch}", arm.Chan)
	}
	if v, ok := arm.Value.(*IntLit); !ok || v.Text != "5" {
		t.Errorf("value = %v, want IntLit{5}", arm.Value)
	}
}

func TestParseSelectDefaultArm(t *testing.T) {
	src := "fn f() {\n" +
		"    select {\n" +
		"        _ -> { print \"default\" }\n" +
		"    }\n" +
		"}\n"
	prog := parseProgramSrc(t, src)
	fn := expectOne[*FnDecl](t, prog)
	sel := fn.Body.Statements[0].(*SelectStmt)
	arm := sel.Arms[0]
	if arm.Op != SelectDefault {
		t.Errorf("op = %v, want SelectDefault", arm.Op)
	}
	if arm.Chan != nil {
		t.Errorf("chan = %v, want nil", arm.Chan)
	}
	if arm.Value != nil {
		t.Errorf("value = %v, want nil", arm.Value)
	}
	if arm.Body == nil || len(arm.Body.Statements) != 1 {
		t.Fatalf("body = %v, want 1 stmt", arm.Body)
	}
}

// --- multiple arms in one select -----------------------------------------

func TestParseSelectAllFourArmShapes(t *testing.T) {
	src := "fn f() {\n" +
		"    select {\n" +
		"        x := <- ch1 -> { print x }\n" +
		"        <- ch2 -> { nop }\n" +
		"        ch3 <- 1 -> { print 1 }\n" +
		"        _ -> { print 2 }\n" +
		"    }\n" +
		"}\n"
	prog := parseProgramSrc(t, src)
	fn := expectOne[*FnDecl](t, prog)
	sel := fn.Body.Statements[0].(*SelectStmt)
	if len(sel.Arms) != 4 {
		t.Fatalf("got %d arms, want 4", len(sel.Arms))
	}
	wantOps := []SelectOpKind{SelectRecvBind, SelectRecvDiscard, SelectSend, SelectDefault}
	for i, want := range wantOps {
		if sel.Arms[i].Op != want {
			t.Errorf("arm[%d].Op = %v, want %v", i, sel.Arms[i].Op, want)
		}
	}
}

// --- body shape variants -------------------------------------------------

func TestParseSelectBodyAsBlockMultipleStatements(t *testing.T) {
	src := "fn f() {\n" +
		"    select {\n" +
		"        <- ch -> {\n" +
		"            print 1\n" +
		"            print 2\n" +
		"        }\n" +
		"    }\n" +
		"}\n"
	prog := parseProgramSrc(t, src)
	fn := expectOne[*FnDecl](t, prog)
	sel := fn.Body.Statements[0].(*SelectStmt)
	arm := sel.Arms[0]
	if arm.Body == nil || len(arm.Body.Statements) != 2 {
		t.Fatalf("body stmts = %d, want 2", len(arm.Body.Statements))
	}
}

func TestParseSelectBodyAsSingleStatement(t *testing.T) {
	src := "fn f() {\n" +
		"    select {\n" +
		"        <- ch -> print 1\n" +
		"    }\n" +
		"}\n"
	prog := parseProgramSrc(t, src)
	fn := expectOne[*FnDecl](t, prog)
	sel := fn.Body.Statements[0].(*SelectStmt)
	arm := sel.Arms[0]
	if arm.Body == nil || len(arm.Body.Statements) != 1 {
		t.Fatalf("body stmts = %d, want 1 (wrapped)", len(arm.Body.Statements))
	}
	if _, ok := arm.Body.Statements[0].(*PrintStmt); !ok {
		t.Errorf("body[0] = %T, want *PrintStmt", arm.Body.Statements[0])
	}
}

// --- top-level select -----------------------------------------------------

func TestParseSelectAtTopLevel(t *testing.T) {
	// select is admitted anywhere a statement is admitted, including the
	// REPL / file top level. The arm body still goes through parseBlock so
	// nested statements parse normally.
	src := "select {\n" +
		"    <- ch -> { nop }\n" +
		"}\n"
	prog := parseProgramSrc(t, src)
	sel := expectOne[*SelectStmt](t, prog)
	if len(sel.Arms) != 1 {
		t.Fatalf("got %d arms, want 1", len(sel.Arms))
	}
}

// --- rejection cases -----------------------------------------------------

func TestParseSelectRejectEmpty(t *testing.T) {
	expectParseErr(t,
		"select { }\n",
		"select must have at least one arm",
	)
}

func TestParseSelectRejectEmptyMultiline(t *testing.T) {
	src := "select {\n" +
		"}\n"
	expectParseErr(t, src, "select must have at least one arm")
}

func TestParseSelectRejectMissingArrow(t *testing.T) {
	src := "fn f() {\n" +
		"    select {\n" +
		"        <- ch { print 1 }\n" +
		"    }\n" +
		"}\n"
	expectParseErr(t, src, "expected '->'")
}

func TestParseSelectRejectMissingBrace(t *testing.T) {
	expectParseErr(t,
		"select <- ch -> { nop }\n",
		"expected '{'",
	)
}

// --- recv-bind triple does not leak outside a select ---------------------

func TestParseLetWithRecvRhsStillParses(t *testing.T) {
	// A normal `let x := <- ch` outside a select must continue to parse as
	// a LetStmt whose value is a RecvExpr. The select-arm recv-bind detector
	// (peekRecvBindHead) must not fire here — it lives only inside
	// parseSelectArm and is consulted only at arm-start.
	prog := parseProgramSrc(t, "let x := <- ch\n")
	s := expectOne[*LetStmt](t, prog)
	if s.Name != "x" {
		t.Errorf("name = %q, want x", s.Name)
	}
	if _, ok := s.Value.(*RecvExpr); !ok {
		t.Errorf("value = %T, want *RecvExpr", s.Value)
	}
}

// --- multiple default arms admitted at parse time ------------------------

func TestParseSelectMultipleDefaultArmsAdmitted(t *testing.T) {
	// PLAN.md: parser admits multiple default arms; typeck (Unit 4) rejects.
	src := "fn f() {\n" +
		"    select {\n" +
		"        _ -> { print 1 }\n" +
		"        _ -> { print 2 }\n" +
		"    }\n" +
		"}\n"
	prog := parseProgramSrc(t, src)
	fn := expectOne[*FnDecl](t, prog)
	sel := fn.Body.Statements[0].(*SelectStmt)
	if len(sel.Arms) != 2 {
		t.Fatalf("got %d arms, want 2", len(sel.Arms))
	}
	for i, arm := range sel.Arms {
		if arm.Op != SelectDefault {
			t.Errorf("arm[%d].Op = %v, want SelectDefault", i, arm.Op)
		}
	}
}

// --- complex chan expression in arm head ---------------------------------

func TestParseSelectArmComplexChanExpr(t *testing.T) {
	// The chan expression in a recv arm may be any expression — typeck
	// validates that it resolves to a chan[T]. Here a method-call result.
	src := "fn f() {\n" +
		"    select {\n" +
		"        v := <- pool.next() -> { print v }\n" +
		"    }\n" +
		"}\n"
	prog := parseProgramSrc(t, src)
	fn := expectOne[*FnDecl](t, prog)
	sel := fn.Body.Statements[0].(*SelectStmt)
	arm := sel.Arms[0]
	if arm.Op != SelectRecvBind {
		t.Errorf("op = %v, want SelectRecvBind", arm.Op)
	}
	if _, ok := arm.Chan.(*MethodCallExpr); !ok {
		t.Errorf("chan = %T, want *MethodCallExpr", arm.Chan)
	}
}
