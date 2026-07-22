// f-string and f-cmd printing (DESIGN-1a §4/§7): the literal text chunks are
// re-encoded canonically (doubling '{'/'}' and, for an f-string, re-escaping like
// a string) while each hole's expression is reprinted through the ordinary
// expression printer, so 'f"{ x+y }"' canonicalizes to 'f"{x + y}"'.
package fmt

import (
	"strconv"
	"strings"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
)

// fstr prints an f-string in canonical form.
func (p *printer) fstr(n *ast.FStr) {
	p.write(`f"`)
	for i := range n.Parts {
		p.fstrPart(&n.Parts[i], false)
	}
	p.write(`"`)
}

// fcmd prints an interpolating command literal in canonical form.
func (p *printer) fcmd(n *ast.FCmd) {
	p.write("f`")
	for i := range n.Parts {
		p.fstrPart(&n.Parts[i], true)
	}
	p.write("`")
}

// fstrPart prints one text or hole part; cmd selects the raw f-cmd text encoding.
func (p *printer) fstrPart(part *ast.FStrPart, cmd bool) {
	if part.Expr == nil {
		if cmd {
			p.write(encodeCmdText(part.Text))
		} else {
			p.write(encodeFStrText(part.Text))
		}
		return
	}
	p.write("{")
	p.expr(part.Expr, precLowest)
	if part.Debug {
		p.write("=")
	}
	if part.Conv != 0 {
		p.write("!")
		p.write(string(part.Conv))
	}
	if part.HasSpec {
		p.write(":")
		p.write(part.Spec)
	}
	p.write("}")
}

// encodeFStrText re-encodes an f-string text chunk: string escapes plus doubled
// braces (a literal '{' or '}' must be written '{{' / '}}').
func encodeFStrText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '{':
			b.WriteString("{{")
		case r == '}':
			b.WriteString("}}")
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == 0:
			b.WriteString(`\0`)
		case r < 0x20:
			b.WriteString(`\u{` + strconv.FormatInt(int64(r), 16) + `}`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// encodeCmdText re-encodes an f-cmd text chunk: raw (no backslash escapes) with
// doubled braces.
func encodeCmdText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '{':
			b.WriteString("{{")
		case '}':
			b.WriteString("}}")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
