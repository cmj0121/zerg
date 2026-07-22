package lexer

import (
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// tokenize lexes src and fails the test on any diagnostic.
func tokenize(t *testing.T, src string) []token.Token {
	t.Helper()
	toks, diags := Tokenize(src)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics for %q: %v", src, diags)
	}
	return toks
}

// findKind returns the first token of the given kind.
func findKind(t *testing.T, toks []token.Token, k token.Kind) token.Token {
	t.Helper()
	for _, tok := range toks {
		if tok.Kind == k {
			return tok
		}
	}
	t.Fatalf("no %v token in %v", k, toks)
	return token.Token{}
}

// TestLeadingComment attaches a full-line comment to the next real token.
func TestLeadingComment(t *testing.T) {
	toks := tokenize(t, "# a note\nx")
	x := findKind(t, toks, token.Ident)
	if len(x.Leading) != 1 {
		t.Fatalf("got %d leading trivia, want 1: %v", len(x.Leading), x.Leading)
	}
	tr := x.Leading[0]
	if tr.Kind != token.LineComment || tr.Text != "# a note" {
		t.Fatalf("got %v %q, want LineComment \"# a note\"", tr.Kind, tr.Text)
	}
}

// TestLeadingFlowsPastSemi keeps pending trivia off inserted Semi tokens: it
// belongs to the next statement's first token.
func TestLeadingFlowsPastSemi(t *testing.T) {
	toks := tokenize(t, "a\n# between\nb")
	if toks[1].Kind != token.Semi || len(toks[1].Leading) != 0 {
		t.Fatalf("Semi must carry no leading trivia: %v", toks[1].Leading)
	}
	b := toks[2]
	if b.Lexeme != "b" || len(b.Leading) != 1 || b.Leading[0].Text != "# between" {
		t.Fatalf("comment should lead 'b': %v", b.Leading)
	}
}

// TestTrailingComment attaches a same-line comment to the token before it, even
// though the ASI Semi is emitted in between.
func TestTrailingComment(t *testing.T) {
	toks := tokenize(t, "x := 1 # inline\ny")
	one := findKind(t, toks, token.Int)
	if len(one.Trailing) != 1 || one.Trailing[0].Text != "# inline" {
		t.Fatalf("trailing comment lost: %v", one.Trailing)
	}
	if one.Trailing[0].Kind != token.LineComment {
		t.Fatalf("got kind %v, want LineComment", one.Trailing[0].Kind)
	}
}

// TestTrailingCommentAtEOF attaches a trailing comment when no newline follows.
func TestTrailingCommentAtEOF(t *testing.T) {
	toks := tokenize(t, "x # last words")
	x := toks[0]
	if len(x.Trailing) != 1 || x.Trailing[0].Text != "# last words" {
		t.Fatalf("EOF trailing comment lost: %v", x.Trailing)
	}
}

// TestDocCommentFolding folds consecutive '##' lines into one DocComment block.
func TestDocCommentFolding(t *testing.T) {
	toks := tokenize(t, "## line one\n## line two\nfn")
	fn := findKind(t, toks, token.Fn)
	if len(fn.Leading) != 1 {
		t.Fatalf("got %d leading trivia, want 1 folded block: %v", len(fn.Leading), fn.Leading)
	}
	tr := fn.Leading[0]
	if tr.Kind != token.DocComment || tr.Text != "## line one\n## line two" {
		t.Fatalf("got %v %q, want folded DocComment", tr.Kind, tr.Text)
	}
}

// TestDocCommentRunBrokenByBlank keeps doc blocks apart across a blank line.
func TestDocCommentRunBrokenByBlank(t *testing.T) {
	toks := tokenize(t, "## first\n\n## second\nfn")
	fn := findKind(t, toks, token.Fn)
	if len(fn.Leading) != 3 {
		t.Fatalf("got %d leading trivia, want doc+blank+doc: %v", len(fn.Leading), fn.Leading)
	}
	kinds := []token.TriviaKind{fn.Leading[0].Kind, fn.Leading[1].Kind, fn.Leading[2].Kind}
	want := []token.TriviaKind{token.DocComment, token.BlankLine, token.DocComment}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("trivia kinds = %v, want %v", kinds, want)
		}
	}
}

// TestBlankLineCollapse records a run of blank lines as one BlankLine trivia.
func TestBlankLineCollapse(t *testing.T) {
	toks := tokenize(t, "a\n\n\n\nb")
	b := toks[2] // Ident, Semi, Ident
	if b.Lexeme != "b" {
		t.Fatalf("unexpected stream: %v", toks)
	}
	if len(b.Leading) != 1 || b.Leading[0].Kind != token.BlankLine {
		t.Fatalf("got %v, want one collapsed BlankLine", b.Leading)
	}
}

// TestBlankCommentBlankOrder preserves the order blank/comment/blank so fmt can
// reprint the layout.
func TestBlankCommentBlankOrder(t *testing.T) {
	toks := tokenize(t, "a\n\n# note\n\nb")
	b := toks[2]
	want := []token.TriviaKind{token.BlankLine, token.LineComment, token.BlankLine}
	if len(b.Leading) != len(want) {
		t.Fatalf("got %d leading trivia, want %d: %v", len(b.Leading), len(want), b.Leading)
	}
	for i := range want {
		if b.Leading[i].Kind != want[i] {
			t.Fatalf("trivia kinds mismatch at %d: %v", i, b.Leading)
		}
	}
}

// TestEOFCollectsDanglingTrivia hangs end-of-file comments on the EOF token.
func TestEOFCollectsDanglingTrivia(t *testing.T) {
	toks := tokenize(t, "a\n# the end")
	eof := toks[len(toks)-1]
	if eof.Kind != token.EOF {
		t.Fatalf("stream must end in EOF")
	}
	if len(eof.Leading) != 1 || eof.Leading[0].Text != "# the end" {
		t.Fatalf("dangling comment lost: %v", eof.Leading)
	}
}

// TestASIUnchangedByTrivia keeps the ASI decisions identical with comments and
// blank lines present.
func TestASIUnchangedByTrivia(t *testing.T) {
	eq(t, "a # c\nb", token.Ident, token.Semi, token.Ident)
	eq(t, "a +\n# c\nb", token.Ident, token.Plus, token.Ident)
	eq(t, "(a\n# c\nb)", token.LParen, token.Ident, token.Ident, token.RParen)
}

// TestTrailingAfterExplicitSemi attaches the comment to the real token, not the
// literal ';' between them.
func TestTrailingAfterExplicitSemi(t *testing.T) {
	toks := tokenize(t, "x; # note\ny")
	x := toks[0]
	if len(x.Trailing) != 1 || x.Trailing[0].Text != "# note" {
		t.Fatalf("comment should trail 'x' past the ';': %v", x.Trailing)
	}
}
