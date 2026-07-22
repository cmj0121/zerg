// Trivia is the source text the grammar ignores but the formatter must not:
// comments, doc comments, and significant blank lines. The lexer collects trivia
// instead of dropping it and attaches each piece to a neighboring token — leading
// trivia onto the next real token, a same-line trailing comment onto the token it
// follows — so the parser can re-home it on the AST node built from that token
// and 'zerg fmt' can reprint it (DESIGN-1a §2).
package token

// TriviaKind enumerates the kinds of trivia the lexer records.
type TriviaKind int

const (
	// LineComment is a '#' comment occupying (the rest of) one line.
	LineComment TriviaKind = iota
	// DocComment is a run of consecutive full-line '##' comments, folded into
	// one block whose Text joins the lines with '\n'.
	DocComment
	// BlankLine is a run of one or more blank lines, collapsed to a single
	// trivia so the canonical form is idempotent.
	BlankLine
)

// String renders a trivia kind for tests and debugging.
func (k TriviaKind) String() string {
	switch k {
	case LineComment:
		return "LineComment"
	case DocComment:
		return "DocComment"
	case BlankLine:
		return "BlankLine"
	default:
		return "TriviaKind(?)"
	}
}

// Trivia is one piece of trivia: a comment (with its text, marker included) or a
// collapsed blank-line run (empty Text).
type Trivia struct {
	Kind TriviaKind
	Text string // the comment text including '#'/'##'; lines joined by '\n' for a DocComment; empty for BlankLine
	Span Span
}
