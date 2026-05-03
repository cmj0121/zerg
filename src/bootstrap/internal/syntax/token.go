// Package syntax bundles the v0.0 token, lexer, AST, and parser into a single
// package. The collapsed layout is deliberate: at v0.0 the language is small
// enough that splitting these into separate packages would calcify boundaries
// before we know where the joints actually want to be.
package syntax

import "fmt"

// Kind identifies a Token's lexical category.
type Kind int

const (
	// KindEOF marks the end of input. The lexer emits exactly one of these
	// at the tail of the stream.
	KindEOF Kind = iota
	// KindNewline is a significant token that terminates a statement.
	KindNewline
	// KindNop is the literal keyword "nop".
	KindNop
	// KindPrint is the literal keyword "print".
	KindPrint
	// KindIdent is any identifier that is not a reserved keyword. v0.0 has
	// no use for IDENT inside expressions, but we still emit it so the
	// parser can produce a precise "unexpected identifier" error.
	KindIdent
	// KindString is a double-quoted string literal whose value has already
	// had escape sequences interpreted.
	KindString
)

// String returns a human-readable name for a Kind, suitable for error
// messages.
func (k Kind) String() string {
	switch k {
	case KindEOF:
		return "EOF"
	case KindNewline:
		return "NEWLINE"
	case KindNop:
		return "'nop'"
	case KindPrint:
		return "'print'"
	case KindIdent:
		return "identifier"
	case KindString:
		return "string"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// Position records where a token starts in the source. Lines and columns are
// both one-based to match every editor on the planet.
type Position struct {
	Line   int
	Column int
}

// String formats a Position as "line:col".
func (p Position) String() string {
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// Token is a single lexeme produced by the lexer.
type Token struct {
	Kind  Kind
	Value string // for KindString this is the unescaped text; for KindIdent the identifier
	Pos   Position
}
