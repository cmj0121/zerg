// Package lexer turns Zerg source into a token stream, following GRAMMAR group 2
// (Lexical). It handles horizontal whitespace, line and doc comments, the literal
// forms of group 3, and — the one subtle rule — automatic ';' insertion (ASI) at
// a line break when the last token can end an item, suppressed inside an unclosed
// '(' or '[' (but not '{').
//
// Comments and blank lines are not dropped: they are collected as trivia
// (DESIGN-1a §2). Full-line comments, folded '##' doc-comment blocks, and
// collapsed blank-line runs become the Leading trivia of the next real token; a
// same-line comment after a token becomes that token's Trailing trivia (attached
// by Tokenize, which sees the already-emitted token).
//
// The lexer recognizes the full lexical surface it can (so the token vocabulary is
// forward-compatible), and records a diagnostic for constructs the Phase 0 pipeline
// does not yet accept (f-strings, command literals, decorators) rather than
// silently mis-tokenizing them.
package lexer

import (
	"strings"
	"unicode/utf8"

	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// Lexer scans one source file into tokens.
type Lexer struct {
	src  string
	offs int // byte offset of the next unread character
	line int // 1-based current line
	col  int // 1-based current column

	depth      int        // nesting of '(' and '[' — suppresses ASI while > 0
	depthStack []int      // saved depth per open '{' — a block re-enables ASI inside parens
	prev       token.Kind // kind of the previous emitted token, for ASI
	diags      diag.List

	pending   []token.Trivia // leading trivia awaiting the next real token
	trail     []token.Trivia // trailing trivia detected for the previously emitted token
	nlRun     int            // consecutive newlines since the last token or comment
	onTokLine bool           // a real (non-Semi) token has been emitted on the current line

	modes []fmode // f-string / f-cmd interpolation mode stack (DESIGN-1a §4)
}

// fmode is one frame of the interpolation mode stack: either the text of an
// f-string / f-cmd (fmStr) or the expression inside a '{ … }' hole (fmHole).
type fmode struct {
	hole  bool // false: string/cmd text mode; true: inside a hole's expression
	cmd   bool // for text mode: a backtick f-cmd rather than a double-quoted f-string
	depth int  // for hole mode: nesting of '(' '[' '{' within the hole
}

func (l *Lexer) pushMode(m fmode) { l.modes = append(l.modes, m) }
func (l *Lexer) popMode()         { l.modes = l.modes[:len(l.modes)-1] }
func (l *Lexer) topMode() *fmode {
	if len(l.modes) == 0 {
		return nil
	}
	return &l.modes[len(l.modes)-1]
}

// New builds a lexer over the given source text.
func New(src string) *Lexer {
	// nlRun starts at 1 as if a newline preceded the file, so a blank first line
	// registers as a BlankLine trivia.
	return &Lexer{src: src, offs: 0, line: 1, col: 1, prev: token.Semi, nlRun: 1}
}

// Tokenize scans src to completion, returning the token stream (terminated by an
// EOF token) and any diagnostics gathered along the way. Leading trivia is
// attached by Next itself; trailing trivia — a same-line comment after a token —
// is attached here, onto the most recent real token.
func Tokenize(src string) ([]token.Token, []diag.Diagnostic) {
	l := New(src)
	var toks []token.Token
	for {
		t := l.Next()
		if tr := l.takeTrailing(); len(tr) > 0 {
			attachTrailing(toks, tr)
		}
		toks = append(toks, t)
		if t.Kind == token.EOF {
			break
		}
	}
	return toks, l.diags.Items()
}

// attachTrailing hangs a same-line trailing comment on the most recent real
// token, skipping any Semi the ASI rule inserted after it.
func attachTrailing(toks []token.Token, tr []token.Trivia) {
	for i := len(toks) - 1; i >= 0; i-- {
		if toks[i].Kind != token.Semi {
			toks[i].Trailing = append(toks[i].Trailing, tr...)
			return
		}
	}
}

// takeTrailing returns and clears the trailing trivia staged for the previously
// emitted token.
func (l *Lexer) takeTrailing() []token.Trivia {
	tr := l.trail
	l.trail = nil
	return tr
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
// Comments and blank lines encountered along the way are collected as trivia
// rather than dropped.
func (l *Lexer) Next() token.Token {
	// Inside an f-string / f-cmd text chunk the next token is a text piece, a hole
	// opener, or the closing quote — driven by the interpolation mode stack.
	if top := l.topMode(); top != nil && !top.hole {
		return l.nextInText(top)
	}
	// Skip horizontal whitespace; a significant newline yields a Semi token, a
	// comment becomes trivia, otherwise scanning continues.
	for !l.eof() {
		switch l.at(0) {
		case ' ', '\t':
			l.advance()
		case '\r':
			// The CR half of a CRLF, and nothing else: GRAMMAR#NEWLINE is `'\r'? '\n'`,
			// so a CR belongs to the line break that follows it and the '\n' arm below
			// does the ASI work. A CR was in the whitespace class, where GRAMMAR#WS never
			// put it, so a lone CR was swallowed and the statements it separated lexed as
			// one line. Out of the class, it reaches the bad-character report.
			if l.at(1) != '\n' {
				return l.scanToken()
			}
			l.advance()
		case '\n':
			start := l.pos()
			l.advance()
			l.onTokLine = false
			l.nlRun++
			if l.nlRun == 2 {
				// the line just ended was blank; a longer run stays one trivia
				l.pending = append(l.pending, token.Trivia{
					Kind: token.BlankLine,
					Span: token.Span{Start: start, End: start},
				})
			}
			if l.depth == 0 && l.prev.EndsItem() {
				return l.emit(token.Semi, start, ";")
			}
			// otherwise insignificant; keep scanning
		case '#':
			if l.at(1) == '[' {
				// '#[' opens a decorator, not a comment; scanToken reports it.
				return l.scanToken()
			}
			l.scanComment()
		default:
			return l.scanToken()
		}
	}
	return l.emit(token.EOF, l.pos(), "")
}

// emit records prev and returns a token with the given span. Pending leading
// trivia is attached to every real token (and to EOF, so end-of-file trivia is
// not lost) but flows past inserted Semi tokens onto the next item.
func (l *Lexer) emit(k token.Kind, start token.Pos, lexeme string) token.Token {
	l.prev = k
	t := token.Token{Kind: k, Lexeme: lexeme, Span: token.Span{Start: start, End: l.pos()}}
	if k != token.Semi {
		t.Leading = l.pending
		l.pending = nil
		l.onTokLine = true
		l.nlRun = 0
	}
	return t
}

func (l *Lexer) emitStr(k token.Kind, start token.Pos, lexeme, value string) token.Token {
	t := l.emit(k, start, lexeme)
	t.Str = value
	return t
}

// scanComment consumes a '#' or '##' comment to end of line and records it as
// trivia: trailing when a token already sits on this line, otherwise leading.
// Consecutive full-line '##' lines fold into a single DocComment block.
func (l *Lexer) scanComment() {
	start := l.pos()
	for !l.eof() && l.at(0) != '\n' {
		l.advance()
	}
	text := strings.TrimRight(l.src[start.Offset:l.offs], " \t\r")
	kind := token.LineComment
	if strings.HasPrefix(text, "##") {
		kind = token.DocComment
	}
	tr := token.Trivia{Kind: kind, Text: text, Span: token.Span{Start: start, End: l.pos()}}
	l.nlRun = 0
	if l.onTokLine {
		l.trail = append(l.trail, tr)
		return
	}
	// fold a '##' line into a directly preceding DocComment block (a blank line
	// or ordinary comment in between breaks the run)
	if kind == token.DocComment && len(l.pending) > 0 {
		if last := &l.pending[len(l.pending)-1]; last.Kind == token.DocComment {
			last.Text += "\n" + text
			last.Span.End = tr.Span.End
			return
		}
	}
	l.pending = append(l.pending, tr)
}

func (l *Lexer) scanToken() token.Token {
	start := l.pos()
	c := l.at(0)

	// Inside a hole, the '}' that closes it, a ':' format spec, and a '!r'/'!s'/'!a'
	// conversion are lexed specially (they are not ordinary expression tokens).
	if top := l.topMode(); top != nil && top.hole && top.depth == 0 {
		switch {
		case c == '}':
			return l.closeHole(start)
		case c == ':':
			return l.scanFmtSpec(start)
		case c == '!' && isConv(l.at(1)) && (l.at(2) == '}' || l.at(2) == ':'):
			return l.scanConv(start)
		}
	}

	// literal prefixes: r"…" raw string, b'…' byte, f"…"/f`…` interpolation.
	switch {
	case c == 'r' && l.at(1) == '"':
		return l.scanRawString(start)
	case c == 'b' && l.at(1) == '\'':
		return l.scanByte(start)
	case c == 'f' && l.at(1) == '"':
		return l.beginFString(start)
	case c == 'f' && l.at(1) == '`':
		return l.beginFCmd(start)
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
		return l.scanCmd(start)
	case c == '#': // '#[' decorator — the only '#' that reaches scanToken
		return l.scanDecoratorOpen(start)
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
	// A number in tuple-index position — after a '.' — is a bare decimal integer,
	// never a float: 'a.0.1' is '(a.0).1', not 'a.(0.1)' (GRAMMAR group 4).
	if l.prev == token.Dot {
		l.consumeDigits(isDigit)
		return l.emit(token.Int, start, l.src[start.Offset:l.offs])
	}
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
	if strings.IndexByte(sb.String(), 0) >= 0 {
		return l.illegal(start, "a string literal may not contain a NUL")
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
	if strings.IndexByte(sb.String(), 0) >= 0 {
		return l.illegal(start, "a string literal may not contain a NUL")
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
	if strings.IndexByte(value, 0) >= 0 {
		return l.illegal(start, "a string literal may not contain a NUL")
	}
	return l.emitStr(token.RawStr, start, l.src[start.Offset:l.offs], value)
}

func (l *Lexer) scanRune(start token.Pos) token.Token {
	return l.scanCharLit(start, token.Rune, false, "rune")
}

func (l *Lexer) scanByte(start token.Pos) token.Token {
	l.advance() // b
	return l.scanCharLit(start, token.Byte, true, "byte")
}

// scanCharLit scans a single-quoted rune or byte literal, the cursor sitting on
// the opening '. byteMode selects the byte form's '\xHH' escape; noun names the
// literal in diagnostics.
func (l *Lexer) scanCharLit(start token.Pos, kind token.Kind, byteMode bool, noun string) token.Token {
	l.advance() // opening '
	var sb strings.Builder
	switch {
	case l.eof() || l.at(0) == '\n':
		return l.illegal(start, "empty or unterminated %s literal", noun)
	case l.at(0) == '\'':
		l.advance() // consume the stray quote so scanning makes progress
		return l.illegal(start, "empty %s literal", noun)
	case l.at(0) == '\\':
		if !l.scanEscape(&sb, byteMode) {
			return l.illegal(start, "invalid escape in %s literal", noun)
		}
	default:
		sb.WriteByte(l.advance())
	}
	if l.at(0) != '\'' {
		return l.illegal(start, "unterminated %s literal", noun)
	}
	l.advance() // closing '
	return l.emitStr(kind, start, l.src[start.Offset:l.offs], sb.String())
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
		// `\"` is not a byte escape: GRAMMAR#byte-escape lists `n t r 0 \ '` and `\x`,
		// and no `"` — the quote a byte literal must escape is the single one that ends
		// it. The `x` and `u` arms below already ask which literal they are in; this one
		// did not, so `b'\"'` lexed to 34 in both compilers.
		if byteMode {
			return false
		}
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
		// clamped one past the code space, so a long run of digits cannot overflow the
		// shift above; the clamp value itself still reads as out of range below.
		if r > utf8.MaxRune {
			r = utf8.MaxRune + 1
		}
	}
	if n == 0 || l.at(0) != '}' {
		return false
	}
	l.advance() // }

	// Past U+10FFFF, or a surrogate: NOT a code point, so not an escape. WriteRune used to
	// substitute U+FFFD here and say nothing, so `'\u{110000}'` and `'\u{D800}'` both became
	// 65533 — a program that named one character got another. `false` is "not a valid
	// escape", which the caller reports as E109, and it is what the shipping compiler says.
	if !utf8.ValidRune(r) {
		return false
	}
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
		// `//` is floor division, not a comment: Zerg comments start with '#'.
		if two == '/' {
			return emit(token.SlashDiv, 1)
		}
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
		if two == '>' {
			return emit(token.FatArrow, 1)
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
		l.holeDepth(+1)
		return emit(token.LParen, 0)
	case ')':
		if l.depth > 0 {
			l.depth--
		}
		l.holeDepth(-1)
		return emit(token.RParen, 0)
	case '[':
		l.depth++
		l.holeDepth(+1)
		return emit(token.LBrack, 0)
	case ']':
		if l.depth > 0 {
			l.depth--
		}
		l.holeDepth(-1)
		return emit(token.RBrack, 0)
	case '{':
		// A '{' opens a block or map composite. Inside it, ASI resumes even when the
		// brace itself sits inside an unclosed '(' or '[' — so a multi-line closure or
		// block passed as a call argument gets its statements separated. Save the
		// enclosing paren/bracket depth and reset, restoring it at the matching '}'.
		l.depthStack = append(l.depthStack, l.depth)
		l.depth = 0
		l.holeDepth(+1)
		return emit(token.LBrace, 0)
	case '}':
		// a hole-closing '}' at depth 0 is intercepted in scanToken; this reaches
		// scanOperator only for a nested map/block brace, so it decrements depth.
		if n := len(l.depthStack); n > 0 {
			l.depth = l.depthStack[n-1]
			l.depthStack = l.depthStack[:n-1]
		}
		l.holeDepth(-1)
		return emit(token.RBrace, 0)
	case ';':
		return emit(token.Semi, 0)
	}
	return l.illegal(start, "unexpected character %q", string(c))
}

// illegal records a diagnostic spanning [start, cursor) and returns an Illegal
// token so the parser can resynchronize.
func (l *Lexer) illegal(start token.Pos, format string, args ...any) token.Token {
	tok := l.emit(token.Illegal, start, l.src[start.Offset:l.offs])
	l.diags.Add(tok.Span, format, args...)
	return tok
}
