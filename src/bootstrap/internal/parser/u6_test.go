package parser

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// bodyStmt parses a single-function source and returns the i-th body statement.
func bodyStmt(t *testing.T, src string, i int) ast.Stmt {
	t.Helper()
	fn := onlyFunc(t, src)
	if i >= len(fn.Body.Stmts) {
		t.Fatalf("want at least %d statements, got %d", i+1, len(fn.Body.Stmts))
	}
	return fn.Body.Stmts[i]
}

// TestIfBindingHead pins the binding form 'if x := e' — the bound name is kept
// and the present-tested expression sits in the branch condition.
func TestIfBindingHead(t *testing.T) {
	stmt := bodyStmt(t, "fn f() {\nif y := g() {\nnop\n}\n}", 0)
	ifs, ok := stmt.(*ast.IfStmt)
	if !ok || len(ifs.Branches) != 1 {
		t.Fatalf("want a 1-branch if, got %T", stmt)
	}
	br := ifs.Branches[0]
	if br.Bind != "y" {
		t.Fatalf("branch bind = %q, want %q", br.Bind, "y")
	}
	if _, ok := br.Cond.(*ast.Call); !ok {
		t.Fatalf("branch cond = %T, want a *ast.Call", br.Cond)
	}
}

// TestIfExprRequiresElse pins the if expression: it is a distinct node from the
// statement form and its trailing 'else' is mandatory.
func TestIfExprRequiresElse(t *testing.T) {
	stmt := bodyStmt(t, "fn f() -> int {\nx := if true {\n1\n} else {\n2\n}\nreturn x\n}", 0)
	bind := stmt.(*ast.BindStmt)
	if _, ok := bind.Value.(*ast.IfExpr); !ok {
		t.Fatalf("binding value = %T, want *ast.IfExpr", bind.Value)
	}
	if _, diags := Parse("fn f() -> int {\nx := if true {\n1\n}\nreturn x\n}"); len(diags) == 0 {
		t.Fatal("an if-expression with no 'else' should be rejected")
	}
}

// TestForFormsIterate pins the three loop forms and the parenthesized
// membership while.
func TestForFormsIterate(t *testing.T) {
	infinite := bodyStmt(t, "fn f() {\nfor {\nbreak\n}\n}", 0).(*ast.ForStmt)
	if infinite.Cond != nil || infinite.Var != "" || infinite.Iter != nil {
		t.Fatalf("infinite for should carry no head: %+v", infinite)
	}

	iter := bodyStmt(t, "fn f() {\nfor mut x in items {\nnop\n}\n}", 0).(*ast.ForStmt)
	if !iter.Mut || iter.Var != "x" || iter.Iter == nil {
		t.Fatalf("iterate for = %+v, want mut x in <iter>", iter)
	}

	while := bodyStmt(t, "fn f() {\nfor (v in r) {\nnop\n}\n}", 0).(*ast.ForStmt)
	bin, ok := while.Cond.(*ast.Binary)
	if !ok || bin.Op != token.In {
		t.Fatalf("parenthesized membership while cond = %+v, want a binary 'in'", while.Cond)
	}
	if while.Var != "" {
		t.Fatalf("membership while must not be the iterate form: %+v", while)
	}
}

// TestNamePatternProvisional pins DESIGN §3b: a bare name in pattern position is
// the provisional NamePattern, while a name followed by '(' is a VariantPattern.
func TestNamePatternProvisional(t *testing.T) {
	fn := onlyFunc(t, "fn f(n: int) -> int {\nreturn match n {\nSome(v) => 1\nx => 2\n}\n}")
	m := fn.Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.MatchExpr)
	variant, ok := m.Arms[0].Pat.(*ast.VariantPattern)
	if !ok || variant.Name != "Some" || len(variant.Elems) != 1 {
		t.Fatalf("arm 0 = %T, want a Some(_) variant pattern", m.Arms[0].Pat)
	}
	name, ok := m.Arms[1].Pat.(*ast.NamePattern)
	if !ok || name.Name != "x" {
		t.Fatalf("arm 1 = %T, want the provisional NamePattern", m.Arms[1].Pat)
	}
}

// TestFullPatterns pins the or / as / struct / tuple / list / range surface.
func TestFullPatterns(t *testing.T) {
	fn := onlyFunc(t, "fn f(n: int) -> int {\nreturn match n {\nA | B => 1\n"+
		"Some(inner as v) => 2\nMove{x, ..} => 3\n(a, b) => 4\n[h, ..t] => 5\n"+
		"200..=300 => 6\n_ => 7\n}\n}")
	m := fn.Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.MatchExpr)

	if or, ok := m.Arms[0].Pat.(*ast.OrPattern); !ok || len(or.Alts) != 2 {
		t.Fatalf("arm 0 = %T, want a 2-alt or-pattern", m.Arms[0].Pat)
	}
	variant := m.Arms[1].Pat.(*ast.VariantPattern)
	if _, ok := variant.Elems[0].(*ast.AsPattern); !ok {
		t.Fatalf("Some(inner as v) inner = %T, want an as-pattern", variant.Elems[0])
	}
	if sp, ok := m.Arms[2].Pat.(*ast.StructPattern); !ok || !sp.Rest || len(sp.Fields) != 1 {
		t.Fatalf("arm 2 = %+v, want Move{x, ..}", m.Arms[2].Pat)
	}
	if tp, ok := m.Arms[3].Pat.(*ast.TuplePattern); !ok || len(tp.Elems) != 2 {
		t.Fatalf("arm 3 = %T, want a 2-element tuple pattern", m.Arms[3].Pat)
	}
	lp, ok := m.Arms[4].Pat.(*ast.ListPattern)
	if !ok || len(lp.Elems) != 2 || !lp.Elems[1].Rest || lp.Elems[1].Name != "t" {
		t.Fatalf("arm 4 = %+v, want [h, ..t]", m.Arms[4].Pat)
	}
	ra, ok := m.Arms[5].Pat.(*ast.RangeArm)
	if !ok || !ra.Inclusive || ra.Hi == nil {
		t.Fatalf("arm 5 = %+v, want an inclusive range arm", m.Arms[5].Pat)
	}
}

// TestGuardAndRaise pins the guard block expression and the 'raise e from c'
// statement.
func TestGuardAndRaise(t *testing.T) {
	g := bodyStmt(t, "fn f() {\nr := guard {\nrisky()\n}\n}", 0).(*ast.BindStmt)
	if _, ok := g.Value.(*ast.GuardExpr); !ok {
		t.Fatalf("guard binding value = %T, want *ast.GuardExpr", g.Value)
	}

	r := bodyStmt(t, "fn f() {\nraise Wrap(e) from e\n}", 0).(*ast.RaiseStmt)
	if r.Value == nil || r.From == nil {
		t.Fatalf("raise = %+v, want a value and a 'from' cause", r)
	}
}

// TestWithBinding pins 'with e as id' and the binding-less form.
func TestWithBinding(t *testing.T) {
	w := bodyStmt(t, "fn f() {\nwith acquire() as y {\nnop\n}\n}", 0).(*ast.WithStmt)
	if w.Var != "y" || w.Resource == nil {
		t.Fatalf("with = %+v, want 'as y'", w)
	}
	w2 := bodyStmt(t, "fn f() {\nwith lock() {\nnop\n}\n}", 0).(*ast.WithStmt)
	if w2.Var != "" {
		t.Fatalf("with lock() should carry no binding: %+v", w2)
	}
}

// TestDocBetweenDecorator pins follow-up F1: a doc-comment between a decorator
// and its declaration is attached (parses with no diagnostics) rather than lost.
func TestDocBetweenDecorator(t *testing.T) {
	file := parseOK(t, "#[derive(Encode)]\n## a documented type\nstruct S {\npub x: int\n}")
	if len(file.Items) != 1 {
		t.Fatalf("want 1 decl, got %d", len(file.Items))
	}
	lead := file.Items[0].Lead()
	found := false
	for _, tr := range lead {
		if tr.Text == "## a documented type" {
			found = true
		}
	}
	if !found {
		t.Fatalf("doc-comment not attached to the declaration; lead = %+v", lead)
	}
}
