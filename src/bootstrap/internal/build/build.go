// Package build ties the front-end and back-end into one call: it runs the Phase
// 0 pipeline lex -> parse -> sema -> emit and returns C source. Compiling that C to
// a binary with a C compiler is the driver's job (it is host I/O, not language
// processing), so this package stays pure and easy to test.
package build

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/diag"
	"github.com/cmj0121/zerg/src/bootstrap/internal/emit"
	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
	"github.com/cmj0121/zerg/src/bootstrap/internal/parser"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
)

// Compile lowers Zerg source to C. It stops at the first stage that reports
// diagnostics, returning them with an empty string; on success it runs the
// pipeline parse -> sema -> mono -> emit and returns the C translation unit and
// no diagnostics.
func Compile(src string) (string, []diag.Diagnostic) {
	file, diags := parser.Parse(src)
	if len(diags) > 0 {
		return "", diags
	}
	info, diags := sema.Check(file)
	if len(diags) > 0 {
		return "", diags
	}
	return emit.Emit(mono.Build(file, info))
}
