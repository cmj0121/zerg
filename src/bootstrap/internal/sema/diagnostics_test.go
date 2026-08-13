package sema

import (
	"strings"
	"testing"

	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
)

// TestSemanticDiagnostics is the Phase 1b semantic-error corpus (DESIGN-1b §8-U7):
// each ill-typed program must raise exactly one span-anchored diagnostic — no
// cascade — whose message names the fault and whose span points at the offending
// construct. It exercises the exhaustiveness checker (U5), the null-safety
// operators (U6), and a plain type error carried over from Iteration 2.
func TestSemanticDiagnostics(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		substr string
		line   int
		col    int
	}{
		{
			name: "non-exhaustive enum match",
			src: "enum Color {\n  Red\n  Green\n  Blue\n}\n" +
				"fn f(c: Color) -> int {\n  return match c {\n    Color.Red => 0\n    Color.Green => 1\n  }\n}",
			substr: "non-exhaustive match: missing variant Color.Blue",
			line:   7, col: 10,
		},
		{
			name: "a guarded arm does not count toward coverage",
			src: "enum Color {\n  Red\n  Green\n  Blue\n}\n" +
				"fn f(c: Color) -> int {\n  return match c {\n    Color.Red => 0\n    Color.Green => 1\n    Color.Blue if true => 2\n  }\n}",
			substr: "non-exhaustive match: missing variant Color.Blue",
			line:   7, col: 10,
		},
		{
			name:   "'?' outside an Either-returning function",
			src:    "fn f(x: int?) -> int {\n  return x?\n}",
			substr: "'?' can only be used in a function returning",
			line:   2, col: 10,
		},
		{
			name:   "'??' default type mismatch",
			src:    "fn f(x: int?) -> int {\n  return x ?? true\n}",
			substr: "'??' default: cannot use bool as int",
			line:   2, col: 15,
		},
		{
			name:   "'?.' on a non-optional value",
			src:    "struct Point {\n  pub x: int\n}\nfn f(p: Point) -> int {\n  return p?.x\n}",
			substr: "'?.' requires an optional value",
			line:   5, col: 10,
		},
		{
			name:   "a type error carried over from Iteration 2",
			src:    "fn g(a: int) -> int {\n  return a\n}\nfn f() {\n  print g(true)\n}",
			substr: "cannot use bool as int",
			line:   5, col: 11,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, pdiags := parser.Parse(tc.src)
			if len(pdiags) != 0 {
				t.Fatalf("unexpected parse errors: %v", pdiags)
			}
			_, diags := Check(file)
			if len(diags) != 1 {
				t.Fatalf("want exactly 1 diagnostic, got %d: %v", len(diags), diags)
			}
			d := diags[0]
			if !strings.Contains(d.Msg, tc.substr) {
				t.Fatalf("message %q does not contain %q", d.Msg, tc.substr)
			}
			if d.Span.Start.Line != tc.line || d.Span.Start.Col != tc.col {
				t.Fatalf("diagnostic at %d:%d, want %d:%d (%s)",
					d.Span.Start.Line, d.Span.Start.Col, tc.line, tc.col, d.Msg)
			}
		})
	}
}
