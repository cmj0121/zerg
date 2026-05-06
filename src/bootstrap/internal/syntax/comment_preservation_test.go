package syntax

import (
	"strings"
	"testing"
)

// v0.10 Unit 1 lands the comment-preservation foundation that `zerg fmt -w`
// needs to be non-destructive. The lexer keeps a side-channel of `#` line
// comments; the parser threads them onto LeadingComments of the next
// statement (or onto Program.HeadComments for file-head blocks). Existing
// Lex / Parse callers see byte-identical token streams and structurally-
// identical ASTs (the new fields default to nil), so the v0.0–v0.9 corpora
// pass unchanged.

func TestLexCommentsCaptured(t *testing.T) {
	src := []byte(`# header line one
# header line two
print "hi"  # trailing tail
# bottom comment
`)
	tokens, comments, err := LexWithComments(src)
	if err != nil {
		t.Fatalf("LexWithComments: %v", err)
	}
	// Token stream must NOT include any comment bodies — comments sit in
	// the side-channel only. Spot-check that the print/string survive.
	var sawPrint, sawString bool
	for _, tk := range tokens {
		if tk.Kind == KindPrint {
			sawPrint = true
		}
		if tk.Kind == KindString && tk.Value == "hi" {
			sawString = true
		}
	}
	if !sawPrint || !sawString {
		t.Fatalf("token stream missing print/string: %+v", tokens)
	}

	// Four comments expected: 3 leading, 1 trailing.
	if len(comments) != 4 {
		t.Fatalf("comment count = %d, want 4: %+v", len(comments), comments)
	}
	wantLeading := []bool{true, true, false, true}
	wantLines := []int{1, 2, 3, 4}
	wantSnippets := []string{"header line one", "header line two", "trailing tail", "bottom comment"}
	for i, c := range comments {
		if c.Leading != wantLeading[i] {
			t.Errorf("comment %d Leading = %v, want %v (text=%q)", i, c.Leading, wantLeading[i], c.Text)
		}
		if c.Pos.Line != wantLines[i] {
			t.Errorf("comment %d line = %d, want %d", i, c.Pos.Line, wantLines[i])
		}
		if !strings.Contains(c.Text, wantSnippets[i]) {
			t.Errorf("comment %d text = %q, want substring %q", i, c.Text, wantSnippets[i])
		}
	}
}

func TestLexCommentsTokenStreamUnchanged(t *testing.T) {
	// LexWithComments must return the SAME token stream as Lex: comments
	// are a pure side-channel. This locks the contract that pre-Unit-1
	// callers see byte-identical output.
	src := []byte(`# top
x := 1
# inner
print x  # tail
`)
	tokensA, err := Lex(src)
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	tokensB, _, err := LexWithComments(src)
	if err != nil {
		t.Fatalf("LexWithComments: %v", err)
	}
	if len(tokensA) != len(tokensB) {
		t.Fatalf("len mismatch: Lex=%d LexWithComments=%d", len(tokensA), len(tokensB))
	}
	for i := range tokensA {
		if tokensA[i].Kind != tokensB[i].Kind ||
			tokensA[i].Value != tokensB[i].Value ||
			tokensA[i].Pos != tokensB[i].Pos {
			t.Errorf("token %d differs: A=%+v B=%+v", i, tokensA[i], tokensB[i])
		}
	}
}

func TestParseAttachesLeadingCommentsToStmt(t *testing.T) {
	src := []byte(`# leading for first let
x := 1

# leading block for the second let
# spans two lines
y := 2
`)
	tokens, comments, err := LexWithComments(src)
	if err != nil {
		t.Fatalf("LexWithComments: %v", err)
	}
	prog, err := ParseWithComments(tokens, comments)
	if err != nil {
		t.Fatalf("ParseWithComments: %v", err)
	}
	if len(prog.Statements) != 2 {
		t.Fatalf("statement count = %d, want 2", len(prog.Statements))
	}
	// File-head: the first leading-line comment block is collected on
	// HeadComments, NOT on the first stmt's LeadingComments.
	if len(prog.HeadComments) != 1 {
		t.Errorf("HeadComments = %v, want 1 entry", prog.HeadComments)
	}
	if !strings.Contains(strings.Join(prog.HeadComments, "\n"), "leading for first let") {
		t.Errorf("HeadComments missing first-header: %v", prog.HeadComments)
	}
	first, ok := prog.Statements[0].(*LetStmt)
	if !ok {
		t.Fatalf("stmt 0 is %T, want *LetStmt", prog.Statements[0])
	}
	if len(first.LeadingComments) != 0 {
		t.Errorf("first LeadingComments = %v, want empty (file-head goes to HeadComments)", first.LeadingComments)
	}
	second, ok := prog.Statements[1].(*LetStmt)
	if !ok {
		t.Fatalf("stmt 1 is %T, want *LetStmt", prog.Statements[1])
	}
	if len(second.LeadingComments) != 2 {
		t.Errorf("second LeadingComments len = %d, want 2: %v", len(second.LeadingComments), second.LeadingComments)
	}
	joined := strings.Join(second.LeadingComments, "|")
	if !strings.Contains(joined, "leading block") || !strings.Contains(joined, "spans two lines") {
		t.Errorf("second LeadingComments missing expected text: %v", second.LeadingComments)
	}
}

func TestParseBlankLineDoesNotBreakAttachment(t *testing.T) {
	// PLAN: blank lines between a comment block and the next stmt do NOT
	// break attribution. The two-line comment block here sits between two
	// statements; the blank line under the comments must not detach them
	// from the next stmt.
	src := []byte(`first := 1
# above
# also above

second := 2
`)
	tokens, comments, err := LexWithComments(src)
	if err != nil {
		t.Fatalf("LexWithComments: %v", err)
	}
	prog, err := ParseWithComments(tokens, comments)
	if err != nil {
		t.Fatalf("ParseWithComments: %v", err)
	}
	if len(prog.Statements) != 2 {
		t.Fatalf("statements = %d, want 2", len(prog.Statements))
	}
	second, ok := prog.Statements[1].(*LetStmt)
	if !ok {
		t.Fatalf("stmt 1 is %T, want *LetStmt", prog.Statements[1])
	}
	if len(second.LeadingComments) != 2 {
		t.Errorf("second.LeadingComments = %v, want 2 entries", second.LeadingComments)
	}
}

func TestParseFileHeadIncludesRequiresLine(t *testing.T) {
	// File-head test: a `# requires:` line plus license / attribution lines
	// all get preserved on the Module/Bundle (Program.HeadComments).
	src := []byte(`# requires: v0.7
# Copyright (c) 2026 someone
# Licensed under MIT
print "hi"
`)
	tokens, comments, err := LexWithComments(src)
	if err != nil {
		t.Fatalf("LexWithComments: %v", err)
	}
	prog, err := ParseWithComments(tokens, comments)
	if err != nil {
		t.Fatalf("ParseWithComments: %v", err)
	}
	if len(prog.HeadComments) != 3 {
		t.Fatalf("HeadComments = %v, want 3 entries", prog.HeadComments)
	}
	joined := strings.Join(prog.HeadComments, "\n")
	for _, want := range []string{"requires: v0.7", "Copyright", "Licensed under MIT"} {
		if !strings.Contains(joined, want) {
			t.Errorf("HeadComments missing %q: %v", want, prog.HeadComments)
		}
	}
}

func TestParseAttachesCommentsAcrossDeclShapes(t *testing.T) {
	// Each top-level decl shape (FnDecl / StructDecl / EnumDecl) must
	// carry its leading comments. Spot-check the three most common ones.
	src := []byte(`# header

# struct comment
struct Point { x: int, y: int }

# enum comment
enum Color { Red, Green }

# fn comment
fn double(x: int) -> int {
    # inside-fn comment
    return x * 2
}
`)
	tokens, comments, err := LexWithComments(src)
	if err != nil {
		t.Fatalf("LexWithComments: %v", err)
	}
	prog, err := ParseWithComments(tokens, comments)
	if err != nil {
		t.Fatalf("ParseWithComments: %v", err)
	}
	if len(prog.Statements) != 3 {
		t.Fatalf("got %d stmts, want 3", len(prog.Statements))
	}
	if got := joinedLeading(prog.Statements[0]); !strings.Contains(got, "struct comment") {
		t.Errorf("StructDecl leading = %q, want struct comment", got)
	}
	if got := joinedLeading(prog.Statements[1]); !strings.Contains(got, "enum comment") {
		t.Errorf("EnumDecl leading = %q, want enum comment", got)
	}
	fn, ok := prog.Statements[2].(*FnDecl)
	if !ok {
		t.Fatalf("stmt 2 is %T, want *FnDecl", prog.Statements[2])
	}
	if got := strings.Join(fn.LeadingComments, "|"); !strings.Contains(got, "fn comment") {
		t.Errorf("FnDecl leading = %q, want fn comment", got)
	}
	// Inside-block comment must attach to the fn body's return statement.
	if fn.Body == nil || len(fn.Body.Statements) != 1 {
		t.Fatalf("fn body not as expected: %+v", fn.Body)
	}
	ret, ok := fn.Body.Statements[0].(*ReturnStmt)
	if !ok {
		t.Fatalf("fn body[0] is %T, want *ReturnStmt", fn.Body.Statements[0])
	}
	if got := strings.Join(ret.LeadingComments, "|"); !strings.Contains(got, "inside-fn comment") {
		t.Errorf("ReturnStmt leading = %q, want inside-fn comment", got)
	}
}

func TestParseRoundTripCommentsPresentSomewhere(t *testing.T) {
	// Round-trip preservation: every leading-line comment in the source
	// must appear somewhere in the AST's LeadingComments / HeadComments
	// slots so the formatter can emit them. Trailing inline comments are
	// stripped at v0.10 (documented limitation) and live on
	// Program.Comments only.
	src := []byte(`# top one
# top two
x := 1

# mid
y := 2

# fn-leading
fn f() {
    # body-leading
    return
}
`)
	tokens, comments, err := LexWithComments(src)
	if err != nil {
		t.Fatalf("LexWithComments: %v", err)
	}
	prog, err := ParseWithComments(tokens, comments)
	if err != nil {
		t.Fatalf("ParseWithComments: %v", err)
	}
	all := strings.Join(prog.HeadComments, "\n")
	walkAST(prog, func(s Stmt) {
		all += "\n" + joinedLeading(s)
	})
	for _, want := range []string{"top one", "top two", "mid", "fn-leading", "body-leading"} {
		if !strings.Contains(all, want) {
			t.Errorf("comment %q lost in AST traversal; got=%q", want, all)
		}
	}
}

func TestLexCommentsStripCRLF(t *testing.T) {
	// CRLF input: the comment scanner ran up to `\n`, capturing the `\r`
	// as part of the comment body. Iter 2 strips one trailing `\r` so a
	// comment authored on a CRLF host lex+parse-emits identically to one
	// authored on an LF host.
	src := []byte("# header line\r\nx := 1\r\n# trailing block\r\n")
	tokens, comments, err := LexWithComments(src)
	if err != nil {
		t.Fatalf("LexWithComments: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("comment count = %d, want 2", len(comments))
	}
	for i, c := range comments {
		if strings.Contains(c.Text, "\r") {
			t.Errorf("comment %d retained CR: %q", i, c.Text)
		}
	}
	prog, err := ParseWithComments(tokens, comments)
	if err != nil {
		t.Fatalf("ParseWithComments: %v", err)
	}
	for i, c := range prog.HeadComments {
		if strings.Contains(c, "\r") {
			t.Errorf("HeadComments[%d] retained CR: %q", i, c)
		}
	}
}

func TestParseWithoutCommentsLeavesFieldsEmpty(t *testing.T) {
	// Pre-Unit-1 callers (Parse with no comment threading) get an AST
	// whose new fields are all nil. This is the structural-additivity
	// guarantee that keeps v0.0–v0.9 corpora passing.
	src := []byte(`# top
x := 1
`)
	tokens, err := Lex(src)
	if err != nil {
		t.Fatalf("Lex: %v", err)
	}
	prog, err := Parse(tokens)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if prog.HeadComments != nil {
		t.Errorf("Parse-only program HeadComments = %v, want nil", prog.HeadComments)
	}
	if len(prog.Statements) != 1 {
		t.Fatalf("statement count = %d, want 1", len(prog.Statements))
	}
	let, ok := prog.Statements[0].(*LetStmt)
	if !ok {
		t.Fatalf("stmt 0 is %T, want *LetStmt", prog.Statements[0])
	}
	if let.LeadingComments != nil {
		t.Errorf("Parse-only LetStmt.LeadingComments = %v, want nil", let.LeadingComments)
	}
}

// joinedLeading returns the LeadingComments slice of a stmt, joined by `|`,
// or "" for stmt types that don't carry the field. Used by tests.
func joinedLeading(s Stmt) string {
	switch x := s.(type) {
	case *LetStmt:
		return strings.Join(x.LeadingComments, "|")
	case *MutStmt:
		return strings.Join(x.LeadingComments, "|")
	case *ConstStmt:
		return strings.Join(x.LeadingComments, "|")
	case *AssignStmt:
		return strings.Join(x.LeadingComments, "|")
	case *ExprStmt:
		return strings.Join(x.LeadingComments, "|")
	case *PrintStmt:
		return strings.Join(x.LeadingComments, "|")
	case *ReturnStmt:
		return strings.Join(x.LeadingComments, "|")
	case *BreakStmt:
		return strings.Join(x.LeadingComments, "|")
	case *ContinueStmt:
		return strings.Join(x.LeadingComments, "|")
	case *FnDecl:
		return strings.Join(x.LeadingComments, "|")
	case *IfStmt:
		return strings.Join(x.LeadingComments, "|")
	case *ForStmt:
		return strings.Join(x.LeadingComments, "|")
	case *NopStmt:
		return strings.Join(x.LeadingComments, "|")
	case *ImportDecl:
		return strings.Join(x.LeadingComments, "|")
	case *StructDecl:
		return strings.Join(x.LeadingComments, "|")
	case *EnumDecl:
		return strings.Join(x.LeadingComments, "|")
	case *MatchStmt:
		return strings.Join(x.LeadingComments, "|")
	case *SpecDecl:
		return strings.Join(x.LeadingComments, "|")
	case *ImplDecl:
		return strings.Join(x.LeadingComments, "|")
	case *SpawnStmt:
		return strings.Join(x.LeadingComments, "|")
	case *DeferStmt:
		return strings.Join(x.LeadingComments, "|")
	case *SendStmt:
		return strings.Join(x.LeadingComments, "|")
	case *SelectStmt:
		return strings.Join(x.LeadingComments, "|")
	}
	return ""
}

// walkAST visits every statement in the program, recursing into block
// bodies (fn / if / for / match arms). Used by the round-trip test to
// ensure no leading comment is dropped on the floor.
func walkAST(prog *Program, visit func(Stmt)) {
	for _, s := range prog.Statements {
		visitStmtRec(s, visit)
	}
}

func visitStmtRec(s Stmt, visit func(Stmt)) {
	visit(s)
	switch x := s.(type) {
	case *FnDecl:
		if x.Body != nil {
			for _, sub := range x.Body.Statements {
				visitStmtRec(sub, visit)
			}
		}
	case *IfStmt:
		visitBlockRec(x.Then, visit)
		for _, eli := range x.Elifs {
			visitBlockRec(eli.Body, visit)
		}
		visitBlockRec(x.Else, visit)
	case *ForStmt:
		visitBlockRec(x.Body, visit)
	case *MatchStmt:
		for _, arm := range x.Arms {
			visitBlockRec(arm.Body, visit)
		}
	case *ImplDecl:
		for _, m := range x.Methods {
			if m == nil || m.Body == nil {
				continue
			}
			for _, sub := range m.Body.Statements {
				visitStmtRec(sub, visit)
			}
		}
	case *SpecDecl:
		for _, m := range x.Methods {
			if m == nil || m.Body == nil {
				continue
			}
			for _, sub := range m.Body.Statements {
				visitStmtRec(sub, visit)
			}
		}
	case *DeferStmt:
		visitBlockRec(x.Body, visit)
	}
}

func visitBlockRec(b *Block, visit func(Stmt)) {
	if b == nil {
		return
	}
	for _, s := range b.Statements {
		visitStmtRec(s, visit)
	}
}
