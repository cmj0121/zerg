// Package build emits a C source file from a parsed Zerg program and shells
// out to the system C compiler to produce a native binary.
//
// At v0.0 the codegen handles only nop and print of a string literal.
// The AST grew for v0.1 (variables, full expressions, control flow,
// functions) but the codegen has not yet caught up. Anything the v0.0
// corpus didn't exercise returns a clean "not yet implemented" error
// rather than emitting incorrect code; the v0.0 e2e tests feed only nop
// and print-of-string, so they continue to pass while the v0.1 emitter
// is being built out.
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
		if err := emitStmt(stmt, &b); err != nil {
			return err
		}
	}
	b.WriteString("    return 0;\n")
	b.WriteString("}\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func emitStmt(stmt syntax.Stmt, b *strings.Builder) error {
	switch s := stmt.(type) {
	case *syntax.NopStmt:
		// `(void)0;` is clearer than an empty statement and survives
		// `-Wpedantic` without complaint.
		b.WriteString("    (void)0;\n")
		return nil
	case *syntax.PrintStmt:
		// v0.1 PrintStmt holds an Expression. v0.0 codegen only knows how
		// to emit a string literal; every other shape returns an error
		// pointing at the source position until the v0.1 emitter fills
		// in the missing cases.
		lit, ok := s.Expr.(*syntax.StringLit)
		if !ok {
			return fmt.Errorf("codegen does not yet support print of %T at %s; v0.1 work in progress", s.Expr, s.Pos)
		}
		// fwrite + putchar instead of puts: parity-safe across NUL bytes,
		// which puts() would truncate at while fmt.Fprintln in the
		// interpreter would not.
		fmt.Fprintf(b, "    fwrite(%s, %d, 1, stdout);\n", cQuote(lit.Value), len(lit.Value))
		b.WriteString("    putchar('\\n');\n")
		return nil
	default:
		return fmt.Errorf("codegen does not yet support %T at %s; v0.1 work in progress", s, stmt.StmtPos())
	}
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
