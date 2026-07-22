// The round-trip harness is the fmt oracle (DESIGN-1a §7): canonical printing
// must preserve the tree — parse(fmt(parse(src))) ≡ parse(src) under ast.Equal
// (spans ignored, trivia normalized) — and must be a fixpoint, fmt(fmt(src)) ==
// fmt(src) byte for byte. Tests feed every corpus source through RoundTrip.
package fmt

import (
	"fmt"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
)

// RoundTrip formats src and verifies the round-trip contract, returning the
// canonical text. It fails when src does not parse cleanly, when the canonical
// text parses to a different tree, or when a second format is not byte-identical
// to the first.
func RoundTrip(src string) (string, error) {
	before, diags := parser.Parse(src)
	if len(diags) > 0 {
		return "", fmt.Errorf("source does not parse cleanly: %v", diags)
	}
	// Conservation: every source comment must have entered the tree, or the
	// reprint below would silently drop it (the AST-equality check cannot see a
	// comment that was never attached).
	if lost := lostComments(src, before); len(lost) > 0 {
		return "", fmt.Errorf("trivia not conserved: %d comment(s) attached to no node, "+
			"first is %q at %s", len(lost), lost[0].Text, lost[0].Span.Start)
	}
	formatted := Print(before)

	after, diags := parser.Parse(formatted)
	if len(diags) > 0 {
		return "", fmt.Errorf("formatted text does not parse cleanly: %v\n--- formatted ---\n%s", diags, formatted)
	}
	if !ast.Equal(before, after) {
		return "", fmt.Errorf("parse(fmt(src)) differs from parse(src)\n--- formatted ---\n%s", formatted)
	}
	if again := Print(after); again != formatted {
		return "", fmt.Errorf("fmt is not idempotent\n--- first ---\n%s\n--- second ---\n%s", formatted, again)
	}
	return formatted, nil
}
