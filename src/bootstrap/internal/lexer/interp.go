// The interpolation lexer implements GRAMMAR group 5's f-string / f-cmd and
// group 3's plain command literal via a small mode stack (DESIGN-1a §4). An
// f-string f"…{expr}…" is lexed as a STRUCTURED stream — FStrBegin holding the
// leading text, then, for each '{ … }' hole, an LBrace, the ordinary expression
// tokens of the hole, an optional Conv ('!r'/'!s'/'!a') and opaque FmtSpec
// (':spec'), and an RBrace — followed by FStrText chunks and a final FStrEnd. The
// parser reads the hole expressions from this same stream, so 'zerg fmt' can
// canonicalize inside a hole. '{{' and '}}' decode to literal braces. An f-cmd
// f`…` is analogous (FCmdBegin/FCmdText/FCmdEnd); a plain `…` is one raw Cmd
// token with no holes.
package lexer

import (
	"strings"

	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// isConv reports whether c names an f-string conversion ('r' debug, 's' display,
// 'a' ascii).
func isConv(c byte) bool { return c == 'r' || c == 's' || c == 'a' }

// holeDepth adjusts the innermost hole's bracket nesting, so the lexer knows when
// a '}' closes the hole (depth 0) versus a nested map/block (depth > 0) and when a
// ':' begins the format spec rather than a nested map entry.
func (l *Lexer) holeDepth(delta int) {
	if top := l.topMode(); top != nil && top.hole {
		top.depth += delta
	}
}

// beginFString consumes 'f"' and the leading text chunk, pushing a text-mode
// frame so the following tokens come from the interpolation stream.
func (l *Lexer) beginFString(start token.Pos) token.Token {
	l.advance() // f
	l.advance() // "
	l.pushMode(fmode{cmd: false})
	text, ok := l.readTextChunk(false)
	if !ok {
		l.popMode()
		return l.illegal(start, "unterminated f-string literal")
	}
	return l.emitStr(token.FStrBegin, start, l.src[start.Offset:l.offs], text)
}

// beginFCmd consumes 'f`' and the leading text chunk of an interpolating command
// literal.
func (l *Lexer) beginFCmd(start token.Pos) token.Token {
	l.advance() // f
	l.advance() // `
	l.pushMode(fmode{cmd: true})
	text, ok := l.readTextChunk(true)
	if !ok {
		l.popMode()
		return l.illegal(start, "unterminated f-cmd literal")
	}
	return l.emitStr(token.FCmdBegin, start, l.src[start.Offset:l.offs], text)
}

// nextInText produces the next token while in an f-string / f-cmd text frame: the
// closing quote (FStrEnd/FCmdEnd), a hole opener (LBrace), or a text chunk
// (FStrText/FCmdText).
func (l *Lexer) nextInText(top *fmode) token.Token {
	start := l.pos()
	quote := byte('"')
	endKind, textKind := token.FStrEnd, token.FStrText
	if top.cmd {
		quote, endKind, textKind = '`', token.FCmdEnd, token.FCmdText
	}
	if l.eof() || l.at(0) == '\n' {
		l.popMode()
		return l.illegal(start, "unterminated f-string or f-cmd literal")
	}
	switch {
	case l.at(0) == quote:
		l.advance()
		l.popMode()
		return l.emit(endKind, start, string(quote))
	case l.at(0) == '{' && l.at(1) != '{':
		l.advance() // {
		l.pushMode(fmode{hole: true})
		l.depth++ // suppress ASI inside the hole (mirrors '(' and '[')
		return l.emit(token.LBrace, start, "{")
	default:
		text, ok := l.readTextChunk(top.cmd)
		if !ok {
			l.popMode()
			return l.illegal(start, "unterminated f-string or f-cmd literal")
		}
		return l.emitStr(textKind, start, l.src[start.Offset:l.offs], text)
	}
}

// readTextChunk reads a run of literal text up to a hole opener '{' or the closing
// quote, decoding '{{'/'}}' to single braces and (for an f-string) processing
// backslash escapes; an f-cmd is raw. ok is false on an unterminated literal (a
// newline or EOF before the close).
func (l *Lexer) readTextChunk(cmd bool) (string, bool) {
	quote := byte('"')
	if cmd {
		quote = '`'
	}
	var sb strings.Builder
	for {
		if l.eof() || l.at(0) == '\n' {
			return "", false
		}
		c := l.at(0)
		switch {
		case c == quote:
			return sb.String(), true
		case c == '{' && l.at(1) == '{':
			l.advance()
			l.advance()
			sb.WriteByte('{')
		case c == '{':
			return sb.String(), true // a hole opener
		case c == '}' && l.at(1) == '}':
			l.advance()
			l.advance()
			sb.WriteByte('}')
		case c == '\\' && !cmd:
			if !l.scanEscape(&sb, false) {
				return "", false
			}
		default:
			sb.WriteByte(l.advance())
		}
	}
}

// closeHole consumes the '}' that ends a hole, popping the hole frame back to the
// surrounding text frame and undoing the ASI suppression entering it added.
func (l *Lexer) closeHole(start token.Pos) token.Token {
	l.advance() // }
	l.popMode()
	if l.depth > 0 {
		l.depth--
	}
	return l.emit(token.RBrace, start, "}")
}

// scanFmtSpec reads an opaque ':spec' format spec up to the hole's closing '}'
// (GRAMMAR keeps the spec's meaning to the Format protocol, so the lexer stores it
// verbatim without the leading ':').
func (l *Lexer) scanFmtSpec(start token.Pos) token.Token {
	l.advance() // ':'
	specStart := l.offs
	for !l.eof() && l.at(0) != '}' && l.at(0) != '{' && l.at(0) != '\n' {
		l.advance()
	}
	spec := l.src[specStart:l.offs]
	return l.emitStr(token.FmtSpec, start, l.src[start.Offset:l.offs], spec)
}

// scanConv reads a '!r'/'!s'/'!a' conversion inside a hole.
func (l *Lexer) scanConv(start token.Pos) token.Token {
	l.advance() // '!'
	conv := l.advance()
	return l.emitStr(token.Conv, start, l.src[start.Offset:l.offs], string(conv))
}

// scanCmd reads a plain command literal '`…`' as one raw token (no holes, no
// escapes).
func (l *Lexer) scanCmd(start token.Pos) token.Token {
	l.advance() // opening '`'
	var sb strings.Builder
	for {
		if l.eof() || l.at(0) == '\n' {
			return l.illegal(start, "unterminated command literal")
		}
		if l.at(0) == '`' {
			l.advance()
			break
		}
		sb.WriteByte(l.advance())
	}
	return l.emitStr(token.Cmd, start, l.src[start.Offset:l.offs], sb.String())
}

// scanDecoratorOpen emits the '#[' decorator-open token. The decorator body is
// consumed by the parser in a later slice; the ASI suppression (like '[') lets a
// decorator span lines up to its matching ']'.
func (l *Lexer) scanDecoratorOpen(start token.Pos) token.Token {
	l.advance() // '#'
	l.advance() // '['
	l.depth++   // suppressed until the matching ']'
	return l.emit(token.Hash, start, "#[")
}
