// Package run is the v0.0 tree-walking interpreter for `zerg run`.
//
// The interpreter is intentionally minimal: it walks the AST top-to-bottom,
// performs the side effect for each statement, and stops. There is no scope,
// no environment, and no value model — v0.0 only has nop and print of a
// string literal.
package run

import (
	"fmt"
	"io"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// Run executes the program, sending program output to w. The returned error
// is for runtime failures only — at v0.0 the only such failure would be a
// short write on w.
func Run(prog *syntax.Program, w io.Writer) error {
	for _, stmt := range prog.Statements {
		switch s := stmt.(type) {
		case *syntax.NopStmt:
			// no-op
		case *syntax.PrintStmt:
			// Fprintln appends '\n', matching the C codegen which uses
			// puts. Parity here is the entire point of the phase-ship rule.
			if _, err := fmt.Fprintln(w, s.Value); err != nil {
				return err
			}
		default:
			return fmt.Errorf("internal error: unknown statement type %T at %s", s, stmt.StmtPos())
		}
	}
	return nil
}
