package ast

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// comment builds a LineComment trivia with the given text.
func comment(text string) token.Trivia {
	return token.Trivia{Kind: token.LineComment, Text: text}
}

// TestTriviaWalksWholeTree checks the collector reaches lead/trail on nested
// nodes, the dangling File.End / Block.End slots, and match arms.
func TestTriviaWalksWholeTree(t *testing.T) {
	inner := lit(1, 0)
	inner.SetLead([]token.Trivia{comment("# on the literal")})

	body := &Block{Stmts: []Stmt{stmt(inner)}}
	body.End = []token.Trivia{comment("# block end")}

	fn := &FuncDecl{Name: "f", Body: body}
	fn.SetTrail([]token.Trivia{comment("# on the fn")})

	file := &File{Decls: []Decl{fn}}
	file.End = []token.Trivia{comment("# file end")}

	got := texts(Trivia(file))
	want := []string{"# on the literal", "# block end", "# on the fn", "# file end"}
	if len(got) != len(want) {
		t.Fatalf("got %d trivia %v, want %d %v", len(got), got, len(want), want)
	}
	for _, w := range want {
		if !contains(got, w) {
			t.Errorf("missing %q in %v", w, got)
		}
	}
}

// TestTriviaTolueratesAbsentElse ensures a nil 'else' block does not panic.
func TestTriviaToleratesAbsentElse(t *testing.T) {
	iff := &IfStmt{
		Branches: []IfBranch{{Cond: boolLit(true), Body: &Block{}}},
		Else:     nil,
	}
	fn := &FuncDecl{Name: "f", Body: &Block{Stmts: []Stmt{iff}}}
	_ = Trivia(&File{Decls: []Decl{fn}}) // must not panic
}

// TestTriviaReachesMatchArms collects per-arm trivia.
func TestTriviaReachesMatchArms(t *testing.T) {
	arm := MatchArm{Pat: &WildPattern{}, Body: lit(1, 0)}
	arm.SetLead([]token.Trivia{comment("# arm")})
	m := &MatchExpr{Subject: lit(2, 0), Arms: []MatchArm{arm}}
	if !contains(texts(Trivia(m)), "# arm") {
		t.Fatalf("match arm trivia not collected")
	}
}

func stmt(e Expr) *ExprStmt { return &ExprStmt{X: e} }

func boolLit(v bool) *BoolLit { return &BoolLit{Value: v} }

func texts(list []token.Trivia) []string {
	var out []string
	for _, tr := range list {
		out = append(out, tr.Text)
	}
	return out
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
