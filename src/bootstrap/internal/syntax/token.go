// Package syntax bundles the v0.0 token, lexer, AST, and parser into a single
// package. The collapsed layout is deliberate: the language is small enough
// that splitting these into separate packages would calcify boundaries before
// we know where the joints actually want to be.
package syntax

import "fmt"

// Kind identifies a Token's lexical category.
type Kind int

// Token kinds. v0.0 names (KindNop, KindPrint, KindIdent, KindString, KindEOF,
// KindNewline) are preserved because the v0.0 parser still consumes them; v0.1
// adds the rest. New entries are appended so existing iota-based code keeps
// working, but callers should rely on the exported names, not numeric values.
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
	// KindIdent is any identifier that is not a reserved keyword.
	KindIdent
	// KindString is a double-quoted string literal whose value has already
	// had escape sequences interpreted.
	KindString

	// --- v0.1 literal kinds. Token.Value stores the literal as-typed (with
	// any `_` digit separators removed and the prefix preserved as written
	// for hex/binary/octal). The parser is responsible for turning that
	// string into a typed numeric value via strconv.ParseInt or similar. We
	// keep the prefix so type inference can tell base apart from value when
	// it matters (it usually doesn't, but it's free here).
	KindInt
	KindFloat

	// --- v0.1 keywords (in alphabetical order to keep the table readable).
	KindAnd
	KindBreak
	KindConst
	KindContinue
	KindElif
	KindElse
	KindFalse
	KindFn
	KindFor
	KindIf
	KindIn
	KindLet
	KindLoop
	KindMut
	KindNot
	KindOr
	KindReturn
	KindTrue
	KindWhile
	KindXor

	// --- v0.1 punctuation.
	KindLParen   // (
	KindRParen   // )
	KindLBrace   // {
	KindRBrace   // }
	KindLBracket // [
	KindRBracket // ]
	KindColon    // :
	KindComma    // ,
	KindDot      // .
	KindArrow    // ->
	KindRange    // ..
	KindRangeEq  // ..=

	// --- v0.1 single-char arithmetic / bitwise operators.
	KindPlus    // +
	KindMinus   // -
	KindStar    // *
	KindSlash   // /
	KindPercent // %
	KindAmp     // &
	KindPipe    // |
	KindCaret   // ^
	KindTilde   // ~
	KindBang    // ! — v0.1 only uses it for `!=`; a standalone `!` lexes so the parser can issue a precise error
	KindLT      // <
	KindGT      // >
	KindAssign  // =

	// --- v0.1 multi-char operators.
	KindFloorDiv // //
	KindEq       // ==
	KindNE       // !=
	KindLE       // <=
	KindGE       // >=
	KindShl      // <<
	KindShr      // >>
	KindWalrus   // :=
	KindPlusEq   // +=
	KindMinusEq  // -=
	KindStarEq   // *=
	KindSlashEq  // /=
	KindPctEq    // %=
	KindAmpEq    // &=
	KindPipeEq   // |=
	KindCaretEq  // ^=
	KindShlEq    // <<=
	KindShrEq    // >>=

	// KindRune is a single-quoted character literal. Token.Value carries the
	// Unicode code-point of the contained rune as a decimal string (e.g.
	// `'A'` -> "65"). Storing the codepoint as a string keeps Token.Value
	// uniformly typed; typeck parses it back via strconv.ParseInt and decides
	// whether the literal is a `byte` (ASCII, code-point < 128) or a `rune`
	// (anything else).
	KindRune

	// --- v0.2 composite-data keywords and punctuation.
	KindStruct   // struct
	KindEnum     // enum
	KindMatch    // match
	KindFatArrow // =>
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
	case KindInt:
		return "integer literal"
	case KindFloat:
		return "float literal"
	case KindRune:
		return "rune literal"
	case KindStruct:
		return "'struct'"
	case KindEnum:
		return "'enum'"
	case KindMatch:
		return "'match'"
	case KindFatArrow:
		return "'=>'"
	case KindAnd:
		return "'and'"
	case KindBreak:
		return "'break'"
	case KindConst:
		return "'const'"
	case KindContinue:
		return "'continue'"
	case KindElif:
		return "'elif'"
	case KindElse:
		return "'else'"
	case KindFalse:
		return "'false'"
	case KindFn:
		return "'fn'"
	case KindFor:
		return "'for'"
	case KindIf:
		return "'if'"
	case KindIn:
		return "'in'"
	case KindLet:
		return "'let'"
	case KindLoop:
		return "'loop'"
	case KindMut:
		return "'mut'"
	case KindNot:
		return "'not'"
	case KindOr:
		return "'or'"
	case KindReturn:
		return "'return'"
	case KindTrue:
		return "'true'"
	case KindWhile:
		return "'while'"
	case KindXor:
		return "'xor'"
	case KindLParen:
		return "'('"
	case KindRParen:
		return "')'"
	case KindLBrace:
		return "'{'"
	case KindRBrace:
		return "'}'"
	case KindLBracket:
		return "'['"
	case KindRBracket:
		return "']'"
	case KindColon:
		return "':'"
	case KindComma:
		return "','"
	case KindDot:
		return "'.'"
	case KindArrow:
		return "'->'"
	case KindRange:
		return "'..'"
	case KindRangeEq:
		return "'..='"
	case KindPlus:
		return "'+'"
	case KindMinus:
		return "'-'"
	case KindStar:
		return "'*'"
	case KindSlash:
		return "'/'"
	case KindPercent:
		return "'%'"
	case KindAmp:
		return "'&'"
	case KindPipe:
		return "'|'"
	case KindCaret:
		return "'^'"
	case KindTilde:
		return "'~'"
	case KindBang:
		return "'!'"
	case KindLT:
		return "'<'"
	case KindGT:
		return "'>'"
	case KindAssign:
		return "'='"
	case KindFloorDiv:
		return "'//'"
	case KindEq:
		return "'=='"
	case KindNE:
		return "'!='"
	case KindLE:
		return "'<='"
	case KindGE:
		return "'>='"
	case KindShl:
		return "'<<'"
	case KindShr:
		return "'>>'"
	case KindWalrus:
		return "':='"
	case KindPlusEq:
		return "'+='"
	case KindMinusEq:
		return "'-='"
	case KindStarEq:
		return "'*='"
	case KindSlashEq:
		return "'/='"
	case KindPctEq:
		return "'%='"
	case KindAmpEq:
		return "'&='"
	case KindPipeEq:
		return "'|='"
	case KindCaretEq:
		return "'^='"
	case KindShlEq:
		return "'<<='"
	case KindShrEq:
		return "'>>='"
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
//
// For literal kinds Value carries the textual form ready for strconv:
//   - KindString: unescaped string contents.
//   - KindIdent: the identifier text.
//   - KindInt: digits with prefix preserved (e.g. "0xff", "255", "0b1010"),
//     digit separators stripped. The parser calls strconv.ParseInt with
//     base 0 to handle every prefix uniformly.
//   - KindFloat: digits with `_` stripped, e.g. "3.14".
//   - KindTrue / KindFalse: "true" / "false".
//
// For all other kinds Value is either empty or the literal source text of the
// punctuation/operator (e.g. "+", "<<="). Tools that just switch on Kind can
// ignore Value entirely.
type Token struct {
	Kind  Kind
	Value string
	Pos   Position
}
