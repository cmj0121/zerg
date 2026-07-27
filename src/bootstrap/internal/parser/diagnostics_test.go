package parser

import (
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
)

// TestDiagnostics pins the parser's span-anchored error reporting across every
// grammar group (the Slice-E "Error UX" deliverable). Each malformed input is
// paired with the substring its diagnostic must contain and the line:col the
// diagnostic must point at (the offending token), so a regression that moves a
// span or dilutes a message is caught. wantDiags asserts the recovery contract:
// one real error yields exactly one diagnostic, never a cascade. wantItems, when
// set, asserts the rest of the file still parsed after recovery.
func TestDiagnostics(t *testing.T) {
	cases := []struct {
		name      string
		group     string // the GRAMMAR group the malformed construct belongs to
		src       string
		wantSub   string // substring the (first) diagnostic must contain
		wantLine  int    // 1-based line the diagnostic points at
		wantCol   int    // 1-based column the diagnostic points at
		wantDiags int    // total diagnostics — one per real error, no pile
		wantItems int    // top-level items that survived recovery (-1 = don't check)
	}{
		{
			name: "unterminated_string", group: "g2 lexical",
			src:     "fn f() {\n\tx := \"oops\n}",
			wantSub: "unterminated string literal", wantLine: 2, wantCol: 7,
			wantDiags: 1, wantItems: 1,
		},
		{
			name: "malformed_number", group: "g3 literals",
			src:     "fn f() {\n\ty := 0xZZ\n}",
			wantSub: "malformed based integer literal", wantLine: 2, wantCol: 7,
			wantDiags: 1, wantItems: 1,
		},
		{
			name: "trailing_operator", group: "g4 expression",
			src:     "fn f() {\n\tz := 1 +\n}",
			wantSub: "expected an expression", wantLine: 3, wantCol: 1,
			wantDiags: 1, wantItems: 1,
		},
		{
			name: "illegal_one_tuple", group: "g4 expression",
			src:     "fn f() {\n\t(a,) = e\n}",
			wantSub: "a tuple literal needs 2 or more elements", wantLine: 2, wantCol: 2,
			wantDiags: 1, wantItems: 1,
		},
		{
			name: "bad_binding", group: "g5 declarations",
			src:     "const x 5\n",
			wantSub: "expected ':=' or ': type =' in binding", wantLine: 1, wantCol: 9,
			wantDiags: 1, wantItems: -1,
		},
		{
			name: "expected_declaration", group: "g5 declarations",
			src:     "pub 5\n",
			wantSub: "expected a declaration, found an integer literal", wantLine: 1, wantCol: 5,
			wantDiags: 1, wantItems: 0,
		},
		{
			name: "unclosed_block", group: "g5 declarations",
			src:     "fn f() {\n\tnop\n",
			wantSub: "unclosed block: expected '}'", wantLine: 3, wantCol: 1,
			wantDiags: 1, wantItems: 0,
		},
		{
			name: "if_expr_needs_else", group: "g6 control-flow",
			src:     "fn f() {\n\ty := if c { 1 }\n}",
			wantSub: "an if-expression requires a trailing 'else'", wantLine: 2, wantCol: 7,
			wantDiags: 1, wantItems: 1,
		},
		{
			name: "match_missing_arrow", group: "g6 patterns",
			src:     "fn f() {\n\tmatch x {\n\t\t1 => 2\n\t\t_ 3\n\t}\n}",
			wantSub: "a match arm needs '=>'", wantLine: 4, wantCol: 5,
			wantDiags: 1, wantItems: 1,
		},
		{
			name: "bad_pattern", group: "g6 patterns",
			src:     "fn f() {\n\tmatch x {\n\t\t+ => 1\n\t}\n}",
			wantSub: "expected a pattern, found '+'", wantLine: 3, wantCol: 3,
			wantDiags: 1, wantItems: 1,
		},
		{
			name: "bad_type", group: "g7 types",
			src:     "fn f(x: ) { nop }",
			wantSub: "expected a type, found ')'", wantLine: 1, wantCol: 9,
			wantDiags: 1, wantItems: -1,
		},
		{
			name: "field_missing_type", group: "g7 declarations",
			src:     "struct S {\n\tx:\n}",
			wantSub: "expected a type", wantLine: 3, wantCol: 1,
			wantDiags: 1, wantItems: -1,
		},
		{
			name: "bad_spec_member", group: "g7 declarations",
			src:     "spec S {\n\t5\n}",
			wantSub: "expected a spec member, found an integer literal", wantLine: 2, wantCol: 2,
			wantDiags: 1, wantItems: -1,
		},
		{
			name: "bad_import_path", group: "g10 modules",
			src:     "import foo\n",
			wantSub: "an import path must be a string literal", wantLine: 1, wantCol: 8,
			wantDiags: 1, wantItems: 0,
		},
		{
			name: "del_needs_identifier", group: "g11 cleanup",
			src:     "fn f() {\n\tdel 5\n}",
			wantSub: "expected an identifier, found an integer literal", wantLine: 2, wantCol: 6,
			wantDiags: 1, wantItems: 1,
		},
		{
			name: "empty_match_needs_arm", group: "g8 patterns",
			src:     "fn f() {\n\tmatch x {}\n}",
			wantSub: "a match needs at least one arm", wantLine: 2, wantCol: 2,
			wantDiags: 1, wantItems: 1,
		},
		{
			name: "struct_brace_literal", group: "g5 statements",
			src:      "fn f() {\n\tx := T{a: 1}\n}",
			wantSub:  "a struct value is written as a call like T(a: 1), not with braces",
			wantLine: 2, wantCol: 8,
			wantDiags: 1, wantItems: 1,
		},
		{
			name: "missing_statement_separator", group: "g1 skeleton",
			src:     "fn f() {\n\ta b\n}",
			wantSub: "expected a newline or ';' to separate statements", wantLine: 2, wantCol: 4,
			wantDiags: 1, wantItems: 1,
		},
		{
			name: "else_on_next_line", group: "g6 control-flow",
			src:     "fn f() {\n\tif c {\n\t\tnop\n\t}\n\telse {\n\t\tnop\n\t}\n}",
			wantSub: "'else' must be on the same line as the closing '}'", wantLine: 5, wantCol: 2,
			wantDiags: 1, wantItems: 1,
		},
		{
			name: "recovery_keeps_following_decl", group: "recovery",
			src:     "fn a() { x := }\nfn b() { nop }",
			wantSub: "expected an expression", wantLine: 1, wantCol: 15,
			wantDiags: 1, wantItems: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, diags := Parse(tc.src)
			if len(diags) != tc.wantDiags {
				t.Fatalf("[%s] got %d diagnostics, want %d:\n%s",
					tc.group, len(diags), tc.wantDiags, formatDiags(diags))
			}
			d := diags[0]
			if !strings.Contains(d.Msg, tc.wantSub) {
				t.Errorf("[%s] message %q does not contain %q", tc.group, d.Msg, tc.wantSub)
			}
			if d.Span.Start.Line != tc.wantLine || d.Span.Start.Col != tc.wantCol {
				t.Errorf("[%s] diagnostic at %d:%d, want %d:%d",
					tc.group, d.Span.Start.Line, d.Span.Start.Col, tc.wantLine, tc.wantCol)
			}
			if tc.wantItems >= 0 && len(file.Items) != tc.wantItems {
				t.Errorf("[%s] recovery kept %d top-level items, want %d",
					tc.group, len(file.Items), tc.wantItems)
			}
		})
	}
}

// formatDiags renders a diagnostic list for a test failure message, one per line.
func formatDiags(diags []diag.Diagnostic) string {
	var b strings.Builder
	for _, d := range diags {
		b.WriteString("  ")
		b.WriteString(d.Error())
		b.WriteByte('\n')
	}
	return b.String()
}
