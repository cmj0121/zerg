// Trivia conservation is the oracle's backbone (DESIGN-1a §2, QA FIX 3): the
// parser attaches trivia only at statement/declaration/match-arm boundaries, so a
// comment sitting inside an expression lands on no node and would vanish on
// reprint. Rather than let that loss stay invisible (the AST-equality oracle
// cannot see a comment that never entered the tree), fmt compares the comments in
// the source token stream against the comments attached to the AST: any comment
// present in the source but absent from the tree is a lost comment. RoundTrip
// turns that into a failure and Format turns it into a diagnostic, so 'zerg fmt'
// never silently corrupts source — it either preserves a comment or loudly
// refuses.
package fmt

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/lexer"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// isComment reports whether a trivia is a comment (blank lines are normalized and
// excluded from conservation).
func isComment(tr token.Trivia) bool {
	return tr.Kind == token.LineComment || tr.Kind == token.DocComment
}

// lostComments returns the comment trivia present in src's token stream but not
// attached anywhere in the parsed tree — the comments a canonical reprint would
// drop. Comments are matched by span, which is stable across the lex the parser
// already performed.
func lostComments(src string, file *ast.File) []token.Trivia {
	attached := map[token.Span]bool{}
	for _, tr := range ast.Trivia(file) {
		if isComment(tr) {
			attached[tr.Span] = true
		}
	}

	toks, _ := lexer.Tokenize(src)
	var lost []token.Trivia
	for _, t := range toks {
		for _, tr := range append(append([]token.Trivia{}, t.Leading...), t.Trailing...) {
			if isComment(tr) && !attached[tr.Span] {
				lost = append(lost, tr)
			}
		}
	}
	return lost
}

// conservationDiags reports one diagnostic per comment a reprint would lose.
func conservationDiags(src string, file *ast.File) []diag.Diagnostic {
	lost := lostComments(src, file)
	if len(lost) == 0 {
		return nil
	}
	var list diag.List
	for _, tr := range lost {
		list.Add(tr.Span, "cannot format: comment %q would be lost "+
			"(a comment inside an expression is not yet preserved in iteration 1)", tr.Text)
	}
	return list.Items()
}
