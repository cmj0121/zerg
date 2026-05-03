package syntax

import (
	"fmt"
	"strings"
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
// The lexer is intentionally tiny: it skips whitespace and comments, emits
// NEWLINE as a significant token, and recognises the two keywords v0.0 needs
// (`nop`, `print`). Anything else that looks like an identifier is returned
// as KindIdent so the parser can produce a focused error.
func Lex(src []byte) ([]Token, error) {
	l := &lexer{src: src, line: 1, col: 1}
	var tokens []Token
	for {
		tok, err := l.next()
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, tok)
		if tok.Kind == KindEOF {
			return tokens, nil
		}
	}
}

type lexer struct {
	src  []byte
	pos  int // byte offset into src
	line int
	col  int
}

func (l *lexer) atEnd() bool { return l.pos >= len(l.src) }

func (l *lexer) peek() byte {
	if l.atEnd() {
		return 0
	}
	return l.src[l.pos]
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
			for !l.atEnd() && l.peek() != '\n' {
				l.advance()
			}
		case c == '\n':
			pos := l.position()
			l.advance()
			return Token{Kind: KindNewline, Pos: pos}, nil
		case c == '"':
			return l.lexString()
		case isIdentStart(c):
			return l.lexIdent(), nil
		default:
			pos := l.position()
			l.advance()
			return Token{}, &LexError{
				Pos:     pos,
				Message: fmt.Sprintf("unexpected character %q", c),
			}
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
	switch word {
	case "nop":
		return Token{Kind: KindNop, Value: word, Pos: pos}
	case "print":
		return Token{Kind: KindPrint, Value: word, Pos: pos}
	default:
		return Token{Kind: KindIdent, Value: word, Pos: pos}
	}
}

// lexString reads a double-quoted string. v0.0 understands the standard
// C-style escapes (`\n`, `\t`, `\r`, `\\`, `\"`, `\0`) and rejects `{`
// outright — interpolation is reserved for v0.1.
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
				Message: "interpolation not supported in v0.0",
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

// v0.0 identifiers are ASCII-only. The lexer scans byte-wise, so any
// pretense of unicode.IsLetter on a single byte would be wrong for UTF-8
// lead bytes anyway. Switch to rune-level scanning when v0.1 admits
// non-ASCII source.

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
