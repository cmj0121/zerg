package syntax

import "fmt"

// ParseError is returned when the parser cannot fit the token stream into the
// v0.0 grammar.
type ParseError struct {
	Pos     Position
	Message string
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	return fmt.Sprintf("parse error at %s: %s", e.Pos, e.Message)
}

// Parse consumes a token slice (typically produced by Lex) and returns the
// program AST. Leading, trailing, and repeated NEWLINE tokens are tolerated;
// the v0.0 examples both contain a shebang+comment block followed by blank
// lines, so this is required for the parity test to even reach the
// statements.
func Parse(tokens []Token) (*Program, error) {
	p := &parser{tokens: tokens}
	return p.parseProgram()
}

// ParseStatement parses exactly one statement from a single line of input —
// the shape the REPL feeds in. Trailing whitespace/EOF is fine, but a second
// statement on the same line is rejected.
func ParseStatement(tokens []Token) (Stmt, error) {
	p := &parser{tokens: tokens}
	// Skip any leading NEWLINEs (the REPL shouldn't produce them, but we
	// stay tolerant).
	for p.peek().Kind == KindNewline {
		p.advance()
	}
	if p.peek().Kind == KindEOF {
		return nil, nil
	}
	stmt, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	// Allow optional trailing NEWLINEs before EOF.
	for p.peek().Kind == KindNewline {
		p.advance()
	}
	if p.peek().Kind != KindEOF {
		t := p.peek()
		return nil, &ParseError{
			Pos:     t.Pos,
			Message: fmt.Sprintf("unexpected %s after statement", t.Kind),
		}
	}
	return stmt, nil
}

type parser struct {
	tokens []Token
	pos    int
}

func (p *parser) peek() Token {
	if p.pos >= len(p.tokens) {
		// Defensive: Lex always terminates with EOF, but make sure we never
		// index past the slice anyway.
		return Token{Kind: KindEOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) advance() Token {
	t := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return t
}

func (p *parser) parseProgram() (*Program, error) {
	prog := &Program{}
	for {
		// Skip blank lines between statements.
		for p.peek().Kind == KindNewline {
			p.advance()
		}
		if p.peek().Kind == KindEOF {
			return prog, nil
		}
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		prog.Statements = append(prog.Statements, stmt)
		// A statement must be terminated by NEWLINE or EOF.
		switch p.peek().Kind {
		case KindNewline:
			p.advance()
		case KindEOF:
			return prog, nil
		default:
			t := p.peek()
			return nil, &ParseError{
				Pos:     t.Pos,
				Message: fmt.Sprintf("expected newline or end of file, got %s", t.Kind),
			}
		}
	}
}

func (p *parser) parseStatement() (Stmt, error) {
	t := p.peek()
	switch t.Kind {
	case KindNop:
		p.advance()
		return &NopStmt{Pos: t.Pos}, nil
	case KindPrint:
		return p.parsePrint()
	default:
		return nil, &ParseError{
			Pos:     t.Pos,
			Message: fmt.Sprintf("expected statement, got %s", t.Kind),
		}
	}
}

func (p *parser) parsePrint() (Stmt, error) {
	pt := p.advance() // consume "print"
	t := p.peek()
	if t.Kind != KindString {
		return nil, &ParseError{
			Pos:     t.Pos,
			Message: "v0.0 only supports print of a string literal",
		}
	}
	p.advance()
	return &PrintStmt{Pos: pt.Pos, Value: t.Value}, nil
}
