package syntax

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cmj/zerg/src/bootstrap/internal/version"
)

// LexError is returned when the lexer cannot make sense of the input.
type LexError struct {
	Pos     Position
	Message string
}

// Error implements the error interface.
func (e *LexError) Error() string {
	return fmt.Sprintf("lex error at %s: %s", e.Pos, e.Message)
}

// Lex turns a source buffer into a flat slice of tokens. The returned slice
// always ends with a single KindEOF token.
//
// The lexer skips spaces / tabs / carriage returns and `# … \n` line comments,
// always emits NEWLINE for `\n` (line-joining inside brackets is a parser
// concern at v0.1), recognises every v0.1 keyword and operator, and lexes
// integer / float / string literals.
//
// Version-gated keywords like `__builtin` (v0.8) are recognised only when the
// source's leading `# requires:` marker declares the relevant minor or higher.
// Lex pre-scans src for that marker so callers do not need to thread a version
// argument; the byte-level scan reuses internal/version.ScanRequires.
//
// Comments are scanned identically but discarded — the token stream is
// byte-identical to a comment-aware lex. Callers that need comments (the
// v0.10 `zerg fmt`) call LexWithComments instead.
func Lex(src []byte) ([]Token, error) {
	tokens, _, err := LexWithComments(src)
	return tokens, err
}

// LexWithComments is Lex plus a side-channel of CommentToken entries, in
// source order. The token slice it returns is byte-identical to what Lex
// returns; comment scanning is unchanged. v0.10 Unit 1: the formatter needs
// comment text + position to emit user comments verbatim, but every other
// consumer (typeck, run, build) discards the side-channel — the AST and
// runtime semantics are unaffected.
func LexWithComments(src []byte) ([]Token, []CommentToken, error) {
	maj, min, ok := version.ScanRequires(src)
	if !ok {
		// No requires-marker: lex against the toolchain version. The driver's
		// CLI gate guarantees we never see a marker that exceeds the toolchain,
		// so the pre-Unit-1 default of "everything available" is preserved.
		maj, min = version.Major, version.Minor
	}
	return lexWithVersion(src, maj, min)
}

// lexWithVersion is the shared body of Lex / LexWithComments; tests use it to
// drive the version-gating without a `# requires:` line in the source string.
func lexWithVersion(src []byte, reqMajor, reqMinor int) ([]Token, []CommentToken, error) {
	l := &lexer{src: src, line: 1, col: 1, reqMajor: reqMajor, reqMinor: reqMinor}
	var tokens []Token
	for {
		tok, err := l.next()
		if err != nil {
			return nil, nil, err
		}
		tokens = append(tokens, tok)
		if tok.Kind == KindEOF {
			return tokens, l.comments, nil
		}
	}
}

type lexer struct {
	src  []byte
	pos  int // byte offset into src
	line int
	col  int
	// reqMajor / reqMinor record the file's `# requires:` version (or the
	// toolchain default when absent). Version-gated keywords like `__builtin`
	// promote only when (reqMajor, reqMinor) >= the keyword's introduction.
	reqMajor int
	reqMinor int
	// comments collects CommentToken entries in source order. Used by
	// LexWithComments (v0.10 Unit 1); plain Lex discards the slice. Comments
	// do NOT enter the token stream — token-stream consumers see byte-
	// identical output to the pre-Unit-1 lexer.
	comments []CommentToken
	// tokenSeenOnLine is set when at least one non-comment token has been
	// emitted on the current source line. A `#` scan that fires while this
	// is true is a trailing inline comment; when false, the comment is
	// alone on its line (Leading == true). Reset at every NEWLINE.
	tokenSeenOnLine bool
}

func (l *lexer) atEnd() bool { return l.pos >= len(l.src) }

func (l *lexer) peek() byte {
	if l.atEnd() {
		return 0
	}
	return l.src[l.pos]
}

// peekAt returns the byte n ahead of the cursor (0 == peek), or 0 past EOF.
func (l *lexer) peekAt(n int) byte {
	if l.pos+n >= len(l.src) {
		return 0
	}
	return l.src[l.pos+n]
}

func (l *lexer) advance() byte {
	if l.atEnd() {
		return 0
	}
	b := l.src[l.pos]
	l.pos++
	if b == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return b
}

func (l *lexer) position() Position {
	return Position{Line: l.line, Column: l.col}
}

// next produces the next significant token. It also handles whitespace and
// comments inline because they never cross statement boundaries.
//
// Comments are captured into l.comments as a side-channel — the returned
// token stream is identical to a comment-stripping lex. Each CommentToken
// records the line, the comment text without the leading `#`, and a Leading
// bit (true when the comment is alone on its line; false when it follows
// other tokens on the same line — a "trailing" inline comment).
func (l *lexer) next() (Token, error) {
	for !l.atEnd() {
		c := l.peek()
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			l.advance()
		case c == '#':
			// Comment: consume until (but not including) the newline so the
			// newline is emitted as a normal NEWLINE token. The shebang
			// `#! /usr/bin/env zerg` is a comment per the grammar — `!` is
			// just a CHAR — so no special state is needed here.
			pos := l.position()
			leading := !l.tokenSeenOnLine
			l.advance() // consume `#`
			start := l.pos
			for !l.atEnd() && l.peek() != '\n' {
				l.advance()
			}
			l.comments = append(l.comments, CommentToken{
				Pos:     pos,
				Text:    string(l.src[start:l.pos]),
				Leading: leading,
			})
		case c == '\n':
			pos := l.position()
			l.advance()
			l.tokenSeenOnLine = false
			return Token{Kind: KindNewline, Pos: pos}, nil
		case c == '"':
			tok, err := l.lexString()
			if err == nil {
				l.tokenSeenOnLine = true
			}
			return tok, err
		case c == '\'':
			tok, err := l.lexRune()
			if err == nil {
				l.tokenSeenOnLine = true
			}
			return tok, err
		case isDigit(c):
			tok, err := l.lexNumber()
			if err == nil {
				l.tokenSeenOnLine = true
			}
			return tok, err
		case isIdentStart(c):
			l.tokenSeenOnLine = true
			return l.lexIdent(), nil
		default:
			tok, err := l.lexOperator()
			if err == nil {
				l.tokenSeenOnLine = true
			}
			return tok, err
		}
	}
	return Token{Kind: KindEOF, Pos: l.position()}, nil
}

func (l *lexer) lexIdent() Token {
	start := l.pos
	pos := l.position()
	for !l.atEnd() && isIdentPart(l.peek()) {
		l.advance()
	}
	word := string(l.src[start:l.pos])
	if k, ok := keywords[word]; ok {
		return Token{Kind: k, Value: word, Pos: pos}
	}
	// Version-gated keywords. `__builtin` lexes as a keyword only when the
	// file's `# requires:` line declares >= v0.8; older files keep lexing
	// the bare word as an identifier so v0.0–v0.7 corpora that legally
	// named things `__builtin` (none ship in-tree but the gate exists for
	// strict backwards compat) keep parsing.
	if word == "__builtin" && !version.Less(l.reqMajor, l.reqMinor, 0, 8) {
		return Token{Kind: KindBuiltin, Value: word, Pos: pos}
	}
	return Token{Kind: KindIdent, Value: word, Pos: pos}
}

// lexNumber recognises integer and float literals.
//
// Integer forms: decimal, `0x` hex, `0b` binary, `0o` octal. `_` is a digit
// separator and is allowed between digits but not adjacent to the prefix or
// at the start/end of the digit run, and never doubled. The Token.Value field
// stores the literal text with `_` characters stripped and the prefix
// preserved as written; downstream code feeds it to strconv.ParseInt with
// base 0.
//
// Float forms: `<digits>.<digits>` only at v0.1. The dot in a float must
// have at least one digit on each side. `1.` (digit-dot-non-digit) is a
// lex error. `.5` is not a float at all — the lexer never enters this
// path for a leading `.` because dispatch routes it to lexOperator, so
// `.5` lexes as DOT followed by INT 5. Both choices defer the parser
// problem cleanly: `..` and `..=` are separate tokens, and admitting
// `.5` as a float would conflict with method-call syntax we expect
// post-v0.1. Token.Value stores the digits with `_` stripped (e.g.
// `"3.14"` for `3.14`).
func (l *lexer) lexNumber() (Token, error) {
	pos := l.position()
	// Detect non-decimal integer prefix.
	if l.peek() == '0' && l.pos+1 < len(l.src) {
		next := l.src[l.pos+1]
		if next == 'x' || next == 'X' || next == 'b' || next == 'B' || next == 'o' || next == 'O' {
			prefix := string(l.src[l.pos : l.pos+2])
			l.advance() // 0
			l.advance() // x/b/o
			isValidDigit := isHexDigit
			what := "hex"
			switch next {
			case 'b', 'B':
				isValidDigit = isBinDigit
				what = "binary"
			case 'o', 'O':
				isValidDigit = isOctDigit
				what = "octal"
			}
			digits, err := l.readDigitRun(isValidDigit, what)
			if err != nil {
				return Token{}, err
			}
			return Token{Kind: KindInt, Value: prefix + digits, Pos: pos}, nil
		}
	}

	// Decimal integer or float.
	intDigits, err := l.readDecimalRun()
	if err != nil {
		return Token{}, err
	}
	// Float requires a digit on each side of the dot. `1.` is a lex error
	// (digit-dot-non-digit) — we reject it explicitly rather than letting
	// the dot fall through to operator scanning, because the user clearly
	// meant a float. Range tokens (`..`, `..=`) are handled by the dot not
	// being followed by a digit, in which case we leave the dot alone.
	if l.peek() == '.' {
		next := l.peekAt(1)
		if isDigit(next) {
			l.advance() // consume .
			fracDigits, err := l.readDecimalRun()
			if err != nil {
				return Token{}, err
			}
			return Token{Kind: KindFloat, Value: intDigits + "." + fracDigits, Pos: pos}, nil
		}
		if next != '.' {
			// `1.` followed by non-digit, non-dot is a malformed float.
			return Token{}, &LexError{
				Pos:     l.position(),
				Message: "float literal requires a digit after '.'",
			}
		}
		// `..` or `..=` follows — leave the dots for the operator scanner.
	}
	return Token{Kind: KindInt, Value: intDigits, Pos: pos}, nil
}

// readDecimalRun reads a run of decimal digits with optional `_` separators.
// The leading character is assumed to already be a digit (lexNumber's
// dispatch guarantees that for the first call); for the fractional part the
// caller has already verified the first byte is a digit too.
func (l *lexer) readDecimalRun() (string, error) {
	return l.readDigitRun(isDigit, "decimal")
}

// readDigitRun reads digits matching `isDigit`, allowing single `_` between
// digits but rejecting leading, trailing, and doubled separators. It returns
// the digit text with `_` stripped.
func (l *lexer) readDigitRun(isValidDigit func(byte) bool, what string) (string, error) {
	var b strings.Builder
	// Must start with at least one valid digit.
	if l.atEnd() || !isValidDigit(l.peek()) {
		// Underscore-first or empty digit sequence (e.g. `0x_` or `0x`).
		errPos := l.position()
		msg := fmt.Sprintf("expected %s digit", what)
		if !l.atEnd() && l.peek() == '_' {
			msg = fmt.Sprintf("'_' may not lead a %s digit run", what)
		}
		return "", &LexError{Pos: errPos, Message: msg}
	}
	for !l.atEnd() {
		c := l.peek()
		switch {
		case isValidDigit(c):
			b.WriteByte(c)
			l.advance()
		case c == '_':
			usPos := l.position()
			l.advance()
			// Doubled underscore or trailing underscore is an error.
			if l.atEnd() || !isValidDigit(l.peek()) {
				return "", &LexError{
					Pos:     usPos,
					Message: fmt.Sprintf("'_' must be followed by %s digit", what),
				}
			}
		default:
			return b.String(), nil
		}
	}
	return b.String(), nil
}

// lexOperator handles every punctuation / operator token. Longest-match wins:
// we look at up to two extra bytes to disambiguate (e.g. `<<=` vs `<<` vs `<`).
func (l *lexer) lexOperator() (Token, error) {
	pos := l.position()
	c := l.peek()
	c1 := l.peekAt(1)
	c2 := l.peekAt(2)

	// Helper to emit a single-byte token.
	emit := func(k Kind) (Token, error) {
		v := string([]byte{l.advance()})
		return Token{Kind: k, Value: v, Pos: pos}, nil
	}
	// Helper to emit a two-byte token.
	emit2 := func(k Kind) (Token, error) {
		l.advance()
		l.advance()
		return Token{Kind: k, Value: string(l.src[l.pos-2 : l.pos]), Pos: pos}, nil
	}
	// Helper to emit a three-byte token.
	emit3 := func(k Kind) (Token, error) {
		l.advance()
		l.advance()
		l.advance()
		return Token{Kind: k, Value: string(l.src[l.pos-3 : l.pos]), Pos: pos}, nil
	}

	switch c {
	case '+':
		if c1 == '=' {
			return emit2(KindPlusEq)
		}
		return emit(KindPlus)
	case '-':
		if c1 == '=' {
			return emit2(KindMinusEq)
		}
		if c1 == '>' {
			return emit2(KindArrow)
		}
		return emit(KindMinus)
	case '*':
		if c1 == '=' {
			return emit2(KindStarEq)
		}
		return emit(KindStar)
	case '/':
		if c1 == '=' {
			return emit2(KindSlashEq)
		}
		if c1 == '/' {
			return emit2(KindFloorDiv)
		}
		return emit(KindSlash)
	case '%':
		if c1 == '=' {
			return emit2(KindPctEq)
		}
		return emit(KindPercent)
	case '&':
		if c1 == '=' {
			return emit2(KindAmpEq)
		}
		return emit(KindAmp)
	case '|':
		if c1 == '=' {
			return emit2(KindPipeEq)
		}
		return emit(KindPipe)
	case '^':
		if c1 == '=' {
			return emit2(KindCaretEq)
		}
		return emit(KindCaret)
	case '~':
		return emit(KindTilde)
	case '<':
		// Longest-match disambiguation across every `<`-led operator:
		//   `<<=` > `<<` > `<-` > `<=` > `<`.
		// `<-` is the v0.7 channel send / receive operator; the rest are v0.1.
		if c1 == '<' && c2 == '=' {
			return emit3(KindShlEq)
		}
		if c1 == '<' {
			return emit2(KindShl)
		}
		if c1 == '-' {
			return emit2(KindLArrow)
		}
		if c1 == '=' {
			return emit2(KindLE)
		}
		return emit(KindLT)
	case '>':
		if c1 == '>' && c2 == '=' {
			return emit3(KindShrEq)
		}
		if c1 == '>' {
			return emit2(KindShr)
		}
		if c1 == '=' {
			return emit2(KindGE)
		}
		return emit(KindGT)
	case '=':
		if c1 == '=' {
			return emit2(KindEq)
		}
		if c1 == '>' {
			// `=>` is the v0.2 match-arm separator. The longest-match rule
			// keeps `==` from gobbling the `=` first because of the c1 check.
			return emit2(KindFatArrow)
		}
		return emit(KindAssign)
	case '!':
		if c1 == '=' {
			return emit2(KindNE)
		}
		// Standalone `!` is a parser problem (v0.1 spells negation as `not`),
		// but the lexer still emits the token so the parser can produce a
		// pinpoint error.
		return emit(KindBang)
	case ':':
		if c1 == '=' {
			return emit2(KindWalrus)
		}
		return emit(KindColon)
	case '.':
		if c1 == '.' && c2 == '=' {
			return emit3(KindRangeEq)
		}
		if c1 == '.' {
			return emit2(KindRange)
		}
		return emit(KindDot)
	case '(':
		return emit(KindLParen)
	case ')':
		return emit(KindRParen)
	case '{':
		return emit(KindLBrace)
	case '}':
		return emit(KindRBrace)
	case '[':
		return emit(KindLBracket)
	case ']':
		return emit(KindRBracket)
	case ',':
		return emit(KindComma)
	case '?':
		// v0.6 null-safety. Longest-match wins: `??` and `?.` are emitted as
		// single tokens; bare `?` lexes for the parser to disambiguate by
		// position (type-position = nullable, expr-position = propagation).
		if c1 == '?' {
			return emit2(KindCoalesce)
		}
		if c1 == '.' {
			return emit2(KindSafeDot)
		}
		return emit(KindQuestion)
	}

	errPos := l.position()
	l.advance()
	return Token{}, &LexError{
		Pos:     errPos,
		Message: fmt.Sprintf("unexpected character %q", c),
	}
}

// lexString reads a double-quoted string. v0.0 understands the standard
// C-style escapes (`\n`, `\t`, `\r`, `\\`, `\"`, `\0`) and rejects `{`
// outright — interpolation is reserved for a later version. v0.1 keeps the
// same rule: `{` inside a string is a lex error.
func (l *lexer) lexString() (Token, error) {
	pos := l.position()
	l.advance() // consume opening "
	var b strings.Builder
	for {
		if l.atEnd() {
			return Token{}, &LexError{
				Pos:     pos,
				Message: "unterminated string literal",
			}
		}
		c := l.peek()
		switch c {
		case '\n':
			return Token{}, &LexError{
				Pos:     l.position(),
				Message: "unterminated string literal (newline before closing quote)",
			}
		case '"':
			l.advance()
			return Token{Kind: KindString, Value: b.String(), Pos: pos}, nil
		case '{':
			return Token{}, &LexError{
				Pos:     l.position(),
				Message: "string interpolation is deferred to v0.2",
			}
		case '\\':
			escPos := l.position()
			l.advance()
			if l.atEnd() {
				return Token{}, &LexError{
					Pos:     escPos,
					Message: "unterminated escape sequence",
				}
			}
			esc := l.advance()
			switch esc {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case '\'':
				b.WriteByte('\'')
			case '0':
				b.WriteByte(0)
			case '{':
				b.WriteByte('{')
			default:
				return Token{}, &LexError{
					Pos:     escPos,
					Message: fmt.Sprintf("unknown escape sequence \\%c", esc),
				}
			}
		default:
			b.WriteByte(l.advance())
		}
	}
}

// lexRune reads a single-quoted character literal. Exactly one rune (possibly
// expressed as an escape sequence) must appear between the quotes. Token.Value
// is the decimal string form of the resulting code-point so typeck can replay
// it through strconv.ParseInt; the byte-vs-rune classification is typeck's
// call, since the lexer can't tell whether the surrounding context wants the
// narrower of the two types.
func (l *lexer) lexRune() (Token, error) {
	pos := l.position()
	l.advance() // consume opening '

	if l.atEnd() {
		return Token{}, &LexError{Pos: pos, Message: "unterminated rune literal"}
	}
	c := l.peek()
	if c == '\'' {
		// `''` — empty rune.
		errPos := l.position()
		l.advance()
		return Token{}, &LexError{Pos: errPos, Message: "empty rune literal"}
	}
	if c == '\n' {
		return Token{}, &LexError{
			Pos:     l.position(),
			Message: "unterminated rune literal (newline before closing quote)",
		}
	}

	var r rune
	if c == '\\' {
		escPos := l.position()
		l.advance() // consume backslash
		if l.atEnd() {
			return Token{}, &LexError{Pos: escPos, Message: "unterminated escape sequence"}
		}
		esc := l.advance()
		switch esc {
		case 'n':
			r = '\n'
		case 't':
			r = '\t'
		case 'r':
			r = '\r'
		case '\\':
			r = '\\'
		case '\'':
			r = '\''
		case '"':
			r = '"'
		case '0':
			r = 0
		default:
			return Token{}, &LexError{
				Pos:     escPos,
				Message: fmt.Sprintf("unknown escape sequence \\%c", esc),
			}
		}
	} else {
		// Decode one UTF-8 rune. For ASCII bytes utf8.DecodeRuneInString
		// returns size 1; for multi-byte sequences it returns the actual
		// width. We step l.pos by `size` and bump l.col by 1 (one column per
		// rune, not per byte) so column tracking stays user-meaningful.
		decoded, size := utf8.DecodeRuneInString(string(l.src[l.pos:]))
		if decoded == utf8.RuneError && size <= 1 {
			errPos := l.position()
			return Token{}, &LexError{Pos: errPos, Message: "invalid UTF-8 in rune literal"}
		}
		r = decoded
		l.pos += size
		l.col++
	}

	if l.atEnd() {
		return Token{}, &LexError{Pos: pos, Message: "unterminated rune literal"}
	}
	if l.peek() != '\'' {
		// Either more characters before the closing quote (multi-rune
		// literal) or some other parse problem. Either way it's a lex error.
		errPos := l.position()
		return Token{}, &LexError{
			Pos:     errPos,
			Message: "rune literal must contain exactly one character",
		}
	}
	l.advance() // consume closing '
	return Token{Kind: KindRune, Value: strconv.FormatInt(int64(r), 10), Pos: pos}, nil
}

// v0.0 identifiers are ASCII-only. The lexer scans byte-wise, so any
// pretense of unicode.IsLetter on a single byte would be wrong for UTF-8
// lead bytes anyway. Switch to rune-level scanning when the language admits
// non-ASCII source.

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHexDigit(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isBinDigit(c byte) bool { return c == '0' || c == '1' }

func isOctDigit(c byte) bool { return c >= '0' && c <= '7' }
