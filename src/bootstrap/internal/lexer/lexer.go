// Package lexer turns Zerg source into a token stream, following GRAMMAR group 2
// (Lexical). It handles horizontal whitespace, line and doc comments, the literal
// forms of group 3, and — the one subtle rule — automatic ';' insertion (ASI) at
// a line break when the last token can end an item, suppressed inside an unclosed
// '(' or '[' (but not '{').
//
// The lexer recognizes the full lexical surface it can (so the token vocabulary is
// forward-compatible), and records a diagnostic for constructs the Phase 0 pipeline
// does not yet accept (f-strings, command literals, decorators) rather than
// silently mis-tokenizing them.
package lexer

import (
	"strings"

	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// Lexer scans one source file into tokens.
type Lexer struct {
	src  string
	offs int // byte offset of the next unread character
	line int // 1-based current line
	col  int // 1-based current column

	depth int        // nesting of '(' and '[' — suppresses ASI while > 0
	prev  token.Kind // kind of the previous emitted token, for ASI
	diags diag.List
}

// New builds a lexer over the given source text.
func New(src string) *Lexer {
	return &Lexer{src: src, offs: 0, line: 1, col: 1, prev: token.Semi}
}

// Tokenize scans src to completion, returning the token stream (terminated by an
// EOF token) and any diagnostics gathered along the way.
func Tokenize(src string) ([]token.Token, []diag.Diagnostic) {
	l := New(src)
	var toks []token.Token
	for {
		t := l.Next()
		toks = append(toks, t)
		if t.Kind == token.EOF {
			break
		}
	}
	return toks, l.diags.Items()
}

// Diagnostics returns the diagnostics gathered so far.
func (l *Lexer) Diagnostics() []diag.Diagnostic { return l.diags.Items() }

// --- character helpers ---------------------------------------------------------

func (l *Lexer) at(i int) byte {
	j := l.offs + i
	if j < len(l.src) {
		return l.src[j]
	}
	return 0
}

func (l *Lexer) eof() bool { return l.offs >= len(l.src) }

func (l *Lexer) pos() token.Pos {
	return token.Pos{Offset: l.offs, Line: l.line, Col: l.col}
}

// advance consumes one byte and tracks line/column.
func (l *Lexer) advance() byte {
	c := l.src[l.offs]
	l.offs++
	if c == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return c
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isHex(c byte) bool {
	return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
func isIdent(c byte) bool { return isLetter(c) || isDigit(c) || c == '_' }

// --- token production ----------------------------------------------------------

// Next returns the next token, inserting a ';' at line breaks per the ASI rule.
func (l *Lexer) Next() token.Token {
	// Skip horizontal whitespace and comments; a significant newline yields a
	// Semi token, otherwise the newline is consumed and scanning continues.
	for !l.eof() {
		switch l.at(0) {
		case ' ', '\t', '\r':
			l.advance()
		case '\n':
			start := l.pos()
			l.advance()
			if l.depth == 0 && l.prev.EndsItem() {
				return l.emit(token.Semi, start, ";")
			}
			// otherwise insignificant; keep scanning
		case '#':
			if l.at(1) == '[' {
				// '#[' opens a decorator, not a comment; scanToken reports it.
				return l.scanToken()
			}
			l.skipComment()
		default:
			return l.scanToken()
		}
	}
	return l.emit(token.EOF, l.pos(), "")
}

// emit records prev and returns a token with the given span.
func (l *Lexer) emit(k token.Kind, start token.Pos, lexeme string) token.Token {
	l.prev = k
	return token.Token{Kind: k, Lexeme: lexeme, Span: token.Span{Start: start, End: l.pos()}}
}

func (l *Lexer) emitStr(k token.Kind, start token.Pos, lexeme, value string) token.Token {
	t := l.emit(k, start, lexeme)
	t.Str = value
	return t
}

// skipComment consumes a line comment or doc comment to end of line. The '#['
// decorator form is separated out by Next before this is called.
func (l *Lexer) skipComment() {
	for !l.eof() && l.at(0) != '\n' {
		l.advance()
	}
}

func (l *Lexer) scanToken() token.Token {
	start := l.pos()
	c := l.at(0)

	// literal prefixes: r"…" raw string, b'…' byte, f"…"/f`…` (unsupported here).
	switch {
	case c == 'r' && l.at(1) == '"':
		return l.scanRawString(start)
	case c == 'b' && l.at(1) == '\'':
		return l.scanByte(start)
	case c == 'f' && (l.at(1) == '"' || l.at(1) == '`'):
		return l.unsupported(start, 2, "f-strings are a Phase 1 feature")
	}

	switch {
	case isLetter(c) || c == '_':
		return l.scanIdent(start)
	case isDigit(c):
		return l.scanNumber(start)
	case c == '"':
		return l.scanString(start)
	case c == '\'':
		return l.scanRune(start)
	case c == '`':
		return l.unsupported(start, 1, "command literals are a Phase 1 feature")
	case c == '#': // '#[' decorator — the only '#' that reaches scanToken
		return l.unsupported(start, 2, "decorators are a Phase 1 feature")
	}
	return l.scanOperator(start)
}

func (l *Lexer) scanIdent(start token.Pos) token.Token {
	for !l.eof() && isIdent(l.at(0)) {
		l.advance()
	}
	lexeme := l.src[start.Offset:l.offs]
	return l.emit(token.Lookup(lexeme), start, lexeme)
}

// scanNumber reads an integer or float literal (GRAMMAR group 3). Underscores are
// accepted only between two digits.
func (l *Lexer) scanNumber(start token.Pos) token.Token {
	kind := token.Int
	if l.at(0) == '0' && (l.at(1) == 'x' || l.at(1) == 'o' || l.at(1) == 'b') {
		base := l.at(1)
		l.advance() // 0
		l.advance() // x/o/b
		var valid func(byte) bool
		switch base {
		case 'x':
			valid = isHex
		case 'o':
			valid = func(b byte) bool { return b >= '0' && b <= '7' }
		default:
			valid = func(b byte) bool { return b == '0' || b == '1' }
		}
		if !valid(l.at(0)) {
			l.consumeDigits(valid)
			return l.illegal(start, "malformed based integer literal")
		}
		l.consumeDigits(valid)
		lexeme := l.src[start.Offset:l.offs]
		return l.emit(kind, start, lexeme)
	}

	l.consumeDigits(isDigit)
	// fractional part: '.' immediately followed by a digit (so '..' and '.field'
	// are not consumed as part of the number).
	if l.at(0) == '.' && isDigit(l.at(1)) {
		kind = token.Float
		l.advance() // .
		l.consumeDigits(isDigit)
	}
	// exponent: e/E [sign] digits.
	if l.at(0) == 'e' || l.at(0) == 'E' {
		if next := l.at(1); isDigit(next) || ((next == '+' || next == '-') && isDigit(l.at(2))) {
			kind = token.Float
			l.advance() // e/E
			if l.at(0) == '+' || l.at(0) == '-' {
				l.advance()
			}
			l.consumeDigits(isDigit)
		}
	}
	lexeme := l.src[start.Offset:l.offs]
	return l.emit(kind, start, lexeme)
}

// consumeDigits reads a run of digits satisfying valid, allowing a single '_'
// grouping separator only when flanked by such digits.
func (l *Lexer) consumeDigits(valid func(byte) bool) {
	for !l.eof() {
		c := l.at(0)
		switch {
		case valid(c):
			l.advance()
		case c == '_' && valid(l.at(1)):
			l.advance()
		default:
			return
		}
	}
}

func (l *Lexer) scanString(start token.Pos) token.Token {
	if l.at(1) == '"' && l.at(2) == '"' {
		return l.scanTripleString(start)
	}
	l.advance() // opening "
	var sb strings.Builder
	for {
		if l.eof() || l.at(0) == '\n' {
			return l.illegal(start, "unterminated string literal")
		}
		c := l.at(0)
		if c == '"' {
			l.advance()
			break
		}
		if c == '\\' {
			if !l.scanEscape(&sb, false) {
				return l.illegal(start, "invalid escape in string literal")
			}
			continue
		}
		sb.WriteByte(l.advance())
	}
	return l.emitStr(token.Str, start, l.src[start.Offset:l.offs], sb.String())
}

func (l *Lexer) scanTripleString(start token.Pos) token.Token {
	l.advance()
	l.advance()
	l.advance() // opening """
	var sb strings.Builder
	for {
		if l.eof() {
			return l.illegal(start, "unterminated multi-line string literal")
		}
		if l.at(0) == '"' && l.at(1) == '"' && l.at(2) == '"' {
			l.advance()
			l.advance()
			l.advance()
			break
		}
		if l.at(0) == '\\' {
			if !l.scanEscape(&sb, false) {
				return l.illegal(start, "invalid escape in string literal")
			}
			continue
		}
		sb.WriteByte(l.advance())
	}
	return l.emitStr(token.Str, start, l.src[start.Offset:l.offs], sb.String())
}

func (l *Lexer) scanRawString(start token.Pos) token.Token {
	l.advance() // r
	l.advance() // opening "
	contentStart := l.offs
	for {
		if l.eof() || l.at(0) == '\n' {
			return l.illegal(start, "unterminated raw string literal")
		}
		if l.at(0) == '"' {
			break
		}
		l.advance()
	}
	value := l.src[contentStart:l.offs]
	l.advance() // closing "
	return l.emitStr(token.RawStr, start, l.src[start.Offset:l.offs], value)
}

func (l *Lexer) scanRune(start token.Pos) token.Token {
	l.advance() // opening '
	var sb strings.Builder
	switch {
	case l.eof() || l.at(0) == '\n':
		return l.illegal(start, "empty or unterminated rune literal")
	case l.at(0) == '\'':
		l.advance() // consume the stray quote so scanning makes progress
		return l.illegal(start, "empty rune literal")
	case l.at(0) == '\\':
		if !l.scanEscape(&sb, false) {
			return l.illegal(start, "invalid escape in rune literal")
		}
	default:
		sb.WriteByte(l.advance())
	}
	if l.at(0) != '\'' {
		return l.illegal(start, "unterminated rune literal")
	}
	l.advance() // closing '
	return l.emitStr(token.Rune, start, l.src[start.Offset:l.offs], sb.String())
}

func (l *Lexer) scanByte(start token.Pos) token.Token {
	l.advance() // b
	l.advance() // opening '
	var sb strings.Builder
	switch {
	case l.eof() || l.at(0) == '\n':
		return l.illegal(start, "empty or unterminated byte literal")
	case l.at(0) == '\'':
		l.advance() // consume the stray quote so scanning makes progress
		return l.illegal(start, "empty byte literal")
	case l.at(0) == '\\':
		if !l.scanEscape(&sb, true) {
			return l.illegal(start, "invalid escape in byte literal")
		}
	default:
		sb.WriteByte(l.advance())
	}
	if l.at(0) != '\'' {
		return l.illegal(start, "unterminated byte literal")
	}
	l.advance() // closing '
	return l.emitStr(token.Byte, start, l.src[start.Offset:l.offs], sb.String())
}

// scanEscape decodes a backslash escape into sb, returning false on a malformed
// escape. When byteMode is set the '\xHH' form is accepted (byte literals);
// otherwise the '\u{…}' form is (string and rune literals).
func (l *Lexer) scanEscape(sb *strings.Builder, byteMode bool) bool {
	l.advance() // backslash
	if l.eof() {
		return false
	}
	c := l.advance()
	switch c {
	case 'n':
		sb.WriteByte('\n')
	case 't':
		sb.WriteByte('\t')
	case 'r':
		sb.WriteByte('\r')
	case '0':
		sb.WriteByte(0)
	case '\\':
		sb.WriteByte('\\')
	case '\'':
		sb.WriteByte('\'')
	case '"':
		sb.WriteByte('"')
	case 'x':
		if byteMode {
			return l.scanHexByte(sb)
		}
		return false
	case 'u':
		if !byteMode {
			return l.scanUnicodeEscape(sb)
		}
		return false
	default:
		return false
	}
	return true
}

func (l *Lexer) scanHexByte(sb *strings.Builder) bool {
	if !isHex(l.at(0)) || !isHex(l.at(1)) {
		return false
	}
	hi, lo := hexVal(l.advance()), hexVal(l.advance())
	sb.WriteByte(byte(hi<<4 | lo))
	return true
}

func (l *Lexer) scanUnicodeEscape(sb *strings.Builder) bool {
	if l.at(0) != '{' {
		return false
	}
	l.advance() // {
	var r rune
	n := 0
	for isHex(l.at(0)) {
		r = r<<4 | rune(hexVal(l.advance()))
		n++
	}
	if n == 0 || l.at(0) != '}' {
		return false
	}
	l.advance() // }
	sb.WriteRune(r)
	return true
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}

// scanOperator reads the longest matching operator or punctuation token.
func (l *Lexer) scanOperator(start token.Pos) token.Token {
	c := l.advance()
	two := l.at(0) // the character now under the cursor (second of a 2-char op)
	emit := func(k token.Kind, extra int) token.Token {
		for i := 0; i < extra; i++ {
			l.advance()
		}
		return l.emit(k, start, l.src[start.Offset:l.offs])
	}
	switch c {
	case '+':
		if two == '%' {
			return emit(token.PlusMod, 1)
		}
		return emit(token.Plus, 0)
	case '-':
		if two == '%' {
			return emit(token.MinusMod, 1)
		}
		if two == '>' {
			return emit(token.Arrow, 1)
		}
		return emit(token.Minus, 0)
	case '*':
		if two == '%' {
			return emit(token.StarMod, 1)
		}
		return emit(token.Star, 0)
	case '/':
		return emit(token.Slash, 0)
	case '%':
		return emit(token.Percent, 0)
	case '&':
		return emit(token.Amp, 0)
	case '|':
		return emit(token.Pipe, 0)
	case '^':
		return emit(token.Caret, 0)
	case '~':
		return emit(token.Tilde, 0)
	case '<':
		switch two {
		case '<':
			return emit(token.Shl, 1)
		case '=':
			return emit(token.Le, 1)
		case '-':
			return emit(token.LArrow, 1)
		}
		return emit(token.Lt, 0)
	case '>':
		switch two {
		case '>':
			return emit(token.Shr, 1)
		case '=':
			return emit(token.Ge, 1)
		}
		return emit(token.Gt, 0)
	case '=':
		if two == '=' {
			return emit(token.EqEq, 1)
		}
		return emit(token.Assign, 0)
	case '!':
		if two == '=' {
			return emit(token.Ne, 1)
		}
		return emit(token.Bang, 0)
	case ':':
		if two == '=' {
			return emit(token.Walrus, 1)
		}
		return emit(token.Colon, 0)
	case '?':
		if two == '?' {
			return emit(token.Coalesce, 1)
		}
		if two == '.' {
			return emit(token.OptDot, 1)
		}
		return emit(token.Question, 0)
	case '.':
		if two == '.' {
			if l.at(1) == '=' {
				return emit(token.DotDotEq, 2)
			}
			return emit(token.DotDot, 1)
		}
		return emit(token.Dot, 0)
	case ',':
		return emit(token.Comma, 0)
	case '(':
		l.depth++
		return emit(token.LParen, 0)
	case ')':
		if l.depth > 0 {
			l.depth--
		}
		return emit(token.RParen, 0)
	case '[':
		l.depth++
		return emit(token.LBrack, 0)
	case ']':
		if l.depth > 0 {
			l.depth--
		}
		return emit(token.RBrack, 0)
	case '{':
		return emit(token.LBrace, 0)
	case '}':
		return emit(token.RBrace, 0)
	case ';':
		return emit(token.Semi, 0)
	}
	return l.illegal(start, "unexpected character %q", string(c))
}

// unsupported consumes width characters and reports a not-yet-supported construct.
func (l *Lexer) unsupported(start token.Pos, width int, msg string) token.Token {
	for i := 0; i < width && !l.eof(); i++ {
		l.advance()
	}
	return l.illegal(start, "%s", msg)
}

// illegal records a diagnostic spanning [start, cursor) and returns an Illegal
// token so the parser can resynchronize.
func (l *Lexer) illegal(start token.Pos, format string, args ...any) token.Token {
	tok := l.emit(token.Illegal, start, l.src[start.Offset:l.offs])
	l.diags.Add(tok.Span, format, args...)
	return tok
}
