// Package build emits a C source file from a parsed Zerg program and shells
// out to the system C compiler to produce a native binary.
package build

import (
	"fmt"
	"io"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// Emit writes the C source for prog to w. The output is a complete,
// self-contained .c file with `int main(void)` as the entry point.
func Emit(prog *syntax.Program, w io.Writer) error {
	var b strings.Builder
	b.WriteString("#include <stdio.h>\n")
	b.WriteString("\n")
	b.WriteString("int main(void) {\n")
	for _, stmt := range prog.Statements {
		switch s := stmt.(type) {
		case *syntax.NopStmt:
			// `(void)0;` is clearer than an empty statement and survives
			// `-Wpedantic` without complaint.
			b.WriteString("    (void)0;\n")
		case *syntax.PrintStmt:
			// fwrite + putchar instead of puts: parity-safe across NUL bytes,
			// which puts() would truncate at while fmt.Fprintln in the
			// interpreter would not.
			fmt.Fprintf(&b, "    fwrite(%s, %d, 1, stdout);\n", cQuote(s.Value), len(s.Value))
			b.WriteString("    putchar('\\n');\n")
		default:
			return fmt.Errorf("internal error: unknown statement type %T at %s", s, stmt.StmtPos())
		}
	}
	b.WriteString("    return 0;\n")
	b.WriteString("}\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// cQuote returns a C string literal, complete with surrounding double quotes,
// whose runtime value equals s. Non-printable bytes are emitted as octal
// escapes so the output is portable across C compilers.
func cQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			// Octal must be zero-padded to three digits so a literal digit
			// in the source can't be folded into the escape. High-bit bytes
			// pass through verbatim so a UTF-8 source string round-trips
			// byte-identically through the C compiler.
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, `\%03o`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
