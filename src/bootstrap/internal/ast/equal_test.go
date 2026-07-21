package ast

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// lit builds an IntLit with a span, the smallest node to compose fixtures from.
func lit(v int64, offset int) *IntLit {
	n := &IntLit{Value: v}
	n.SetSpan(token.Span{Start: token.Pos{Offset: offset}, End: token.Pos{Offset: offset + 1}})
	return n
}

func TestEqualIgnoresSpans(t *testing.T) {
	if !Equal(lit(1, 0), lit(1, 99)) {
		t.Fatalf("Equal must ignore spans")
	}
	if Equal(lit(1, 0), lit(2, 0)) {
		t.Fatalf("Equal must see differing values")
	}
}

func TestEqualSeesStructure(t *testing.T) {
	a := &Binary{Op: token.Plus, L: lit(1, 0), R: lit(2, 2)}
	b := &Binary{Op: token.Plus, L: lit(1, 5), R: lit(2, 7)}
	c := &Binary{Op: token.Minus, L: lit(1, 0), R: lit(2, 2)}
	if !Equal(a, b) {
		t.Fatalf("same structure must be Equal")
	}
	if Equal(a, c) {
		t.Fatalf("different operator must differ")
	}
	if Equal(a, lit(1, 0)) {
		t.Fatalf("different node kinds must differ")
	}
}

func TestEqualComparesTriviaText(t *testing.T) {
	a, b := lit(1, 0), lit(1, 0)
	a.SetLead([]token.Trivia{{Kind: token.LineComment, Text: "# x"}})
	if Equal(a, b) {
		t.Fatalf("a leading comment must make nodes differ")
	}
	b.SetLead([]token.Trivia{{Kind: token.LineComment, Text: "# x"}})
	if !Equal(a, b) {
		t.Fatalf("same comment text must be Equal")
	}
	b.SetLead([]token.Trivia{{Kind: token.LineComment, Text: "# y"}})
	if Equal(a, b) {
		t.Fatalf("different comment text must differ")
	}
}

func TestEqualCollapsesBlankRuns(t *testing.T) {
	a, b := lit(1, 0), lit(1, 0)
	a.SetLead([]token.Trivia{{Kind: token.BlankLine}})
	b.SetLead([]token.Trivia{{Kind: token.BlankLine}, {Kind: token.BlankLine}})
	if !Equal(a, b) {
		t.Fatalf("consecutive blank lines must collapse before comparison")
	}
}

func TestEqualHandlesNilSlots(t *testing.T) {
	bare := &ReturnStmt{}
	valued := &ReturnStmt{Value: lit(1, 0)}
	if Equal(bare, valued) {
		t.Fatalf("return with and without a value must differ")
	}
	if !Equal(bare, &ReturnStmt{}) {
		t.Fatalf("two bare returns must be Equal")
	}
}

func TestBaseAccessors(t *testing.T) {
	n := lit(7, 3)
	if n.Span().Start.Offset != 3 {
		t.Fatalf("Span() must return the stamped span")
	}
	tr := []token.Trivia{{Kind: token.LineComment, Text: "# t"}}
	n.SetTrail(tr)
	if len(n.Trail()) != 1 || n.Trail()[0].Text != "# t" {
		t.Fatalf("Trail() must return the attached trivia")
	}
	if len(n.Lead()) != 0 {
		t.Fatalf("Lead() starts empty")
	}
}
