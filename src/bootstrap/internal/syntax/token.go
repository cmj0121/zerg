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

	// --- v0.4 polymorphism keywords. `spec` declares an interface, `impl`
	// attaches methods (inherent or for-spec), and `this` names the receiver
	// inside method bodies. All three are reserved — using them as bindings
	// is a parse-time error.
	KindSpec // spec
	KindImpl // impl
	KindThis // this

	// --- v0.5 module keywords. `pub` is a top-level visibility modifier; it
	// applies to `fn`, `struct`, `enum`, `spec`, and impl methods. Default
	// visibility is private; `pub` opts in. The lexer recognises the keyword
	// but the visibility bit is inert at v0.5 Unit 1a — typeck consumption
	// arrives at Unit 3.
	KindPub // pub
	// `import` introduces a top-level module import; `as` binds the imported
	// module to a local alias inside an import statement. Both are reserved
	// keywords starting v0.5 Unit 1b. The parser consumes them; module loading
	// (Unit 2) and cross-module name resolution (Unit 3) handle the semantics.
	KindImport // import
	KindAs     // as

	// --- v0.6 null-safety tokens. `?` is the postfix nullable / propagation
	// marker (disambiguated by position: type-position ⇒ nullable, expression-
	// position ⇒ propagation). `??` is the right-associative nil-coalescing
	// infix operator at the lowest precedence rung. `?.` is the safe-navigation
	// member-access operator. `nil` is the absence-of-value literal keyword.
	// All four are added at v0.6 Unit 1; longest-match in the lexer keeps `??`
	// from gobbling `?.` and vice versa.
	KindQuestion // ?
	KindCoalesce // ??
	KindSafeDot  // ?.
	KindNil      // nil

	// --- v0.7 concurrency keywords. `spawn` starts a fire-and-forget task;
	// `defer` registers code to run at fn-body exit in LIFO order. Lexed at
	// v0.7 Unit 1a; consumed by typeck at Unit 3 and the interpreter / codegen
	// at Units 6 / 7.
	KindSpawn // spawn
	KindDefer // defer

	// KindLArrow is the v0.7 channel send / receive operator `<-`. Disambiguated
	// in the lexer against `<`, `<=`, `<<`, `<<=` via longest-match. Used in two
	// shapes by the parser: `expr <- expr` for a send statement, and prefix
	// `<- expr` for a receive expression.
	KindLArrow // <-

	// KindSelect is the v0.7 `select` statement keyword for multiplexed channel
	// wait. Reserved at v0.7 Unit 1c; consumed by typeck at Unit 4 and the
	// interpreter / codegen at Units 6 / 7.
	KindSelect // select

	// KindBuiltin is the v0.8 `__builtin` fn-decl marker. Lexed only when the
	// surrounding file declares `# requires: v0.8` or higher; older files lex
	// the literal as KindIdent so v0.0–v0.7 backwards compatibility holds.
	// The parser consumes it in fn-decl tail position (`__builtin <ident>`),
	// and the typeck / loader (Unit 2) restrict the keyword to embedded
	// `std/` modules — user code referencing it surfaces a focused diagnostic.
	KindBuiltin // __builtin

	// KindAsm is the v0.13 inline-assembly keyword. Lexed only when the source
	// declares `# requires: v0.13` or higher; v0.0–v0.12 files keep lexing the
	// bare word as KindIdent so older corpora that named locals `asm` keep
	// parsing. The lexer follows the keyword with a body-scan that walks the
	// `{ … }` payload verbatim — string-literal-aware brace counting — and
	// emits KindAsmBody carrying the raw bytes. The parser splits the body
	// into text / `${name}` chunks; cgen (U4) lowers each chunk into a GCC
	// __asm__ volatile operand list.
	KindAsm // asm

	// KindAsmBody is the raw payload between the `{` and the matching `}` of
	// an `asm { … }` block. The lexer emits it as a single composite token
	// after KindAsm so the parser does not need to count braces or track
	// string-literal state itself — that work lives next to the rest of
	// lexical scanning. Token.Value carries the verbatim body (no surrounding
	// braces, no edits); Token.Pos is the source position of the opening `{`
	// so diagnostics point at the asm block, not the file head.
	KindAsmBody // asm-block body payload

	// --- v0.16 string-interpolation tokens. Interpolated strings ("foo {x}
	// bar") expand to a structured token sequence so the parser does not need
	// to re-scan the lexeme. The lexer emits exactly one Start, then alternating
	// Lit / Var pieces (always starting and ending with a Lit — empty Lits
	// sandwich any leading or trailing Var so the alternation is uniform),
	// then exactly one End. Non-interpolated strings still emit a single
	// KindString for back-compat. The parser drops empty Lit pieces when
	// building the AST; the wire-level alternation is purely a parser
	// convenience.
	KindInterpStart // opening `"` of an interpolated string
	KindInterpLit   // literal chunk between interpolations; Token.Value carries the unescaped text (may be "")
	KindInterpVar   // `{ident}` inside an interpolated string; Token.Value carries the ident name
	KindInterpEnd   // closing `"` of an interpolated string
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
	case KindSpec:
		return "'spec'"
	case KindImpl:
		return "'impl'"
	case KindThis:
		return "'this'"
	case KindPub:
		return "'pub'"
	case KindImport:
		return "'import'"
	case KindAs:
		return "'as'"
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
	case KindQuestion:
		return "'?'"
	case KindCoalesce:
		return "'??'"
	case KindSafeDot:
		return "'?.'"
	case KindNil:
		return "'nil'"
	case KindSpawn:
		return "'spawn'"
	case KindDefer:
		return "'defer'"
	case KindLArrow:
		return "'<-'"
	case KindSelect:
		return "'select'"
	case KindBuiltin:
		return "'__builtin'"
	case KindAsm:
		return "'asm'"
	case KindAsmBody:
		return "asm body"
	case KindInterpStart:
		return "interpolated string start"
	case KindInterpLit:
		return "interpolated string literal chunk"
	case KindInterpVar:
		return "interpolation variable"
	case KindInterpEnd:
		return "interpolated string end"
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

// CommentToken is one `# … \n` line comment produced by the lexer's
// comment-side-channel. v0.10 Unit 1: `zerg fmt` consumes these to emit user
// comments verbatim; every other consumer (typeck, run, build) discards the
// slice. Pos points at the `#` itself; Text is the comment body with the
// leading `#` stripped (leading whitespace inside the comment is preserved).
// Leading is true when the comment was alone on its line, false when it
// followed other tokens on the same line (a "trailing" inline comment).
//
// CommentTokens never enter the regular Token stream — the parser ignores
// them when threading positions, and the lexer's emission of NEWLINE / EOF
// is unchanged.
type CommentToken struct {
	Pos     Position
	Text    string
	Leading bool
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
