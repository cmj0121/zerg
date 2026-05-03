// Package run is the v0.0 tree-walking interpreter for `zerg run`.
//
// At v0.0 the interpreter handles only nop and print of a string literal.
// The AST grew for v0.1 (variables, full expressions, control flow,
// functions) but the interpreter has not yet caught up. Anything the v0.0
// corpus didn't exercise returns a clean "not yet implemented" error
// rather than crashing the program; the v0.0 e2e tests feed only nop and
// print-of-string, so they continue to pass while the v0.1 evaluator is
// being built out.
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
		if err := runStmt(stmt, w); err != nil {
			return err
		}
	}
	return nil
}

func runStmt(stmt syntax.Stmt, w io.Writer) error {
	switch s := stmt.(type) {
	case *syntax.NopStmt:
		return nil
	case *syntax.PrintStmt:
		// v0.1 PrintStmt holds an Expression. Until the v0.1 evaluator
		// lands we only know how to print a bare string literal — every
		// other shape surfaces a precise error that points at the source
		// position.
		lit, ok := s.Expr.(*syntax.StringLit)
		if !ok {
			return fmt.Errorf("interpreter does not yet support print of %T at %s; v0.1 work in progress", s.Expr, s.Pos)
		}
		// Fprintln appends '\n', matching the C codegen which emits
		// putchar('\n') after fwrite. Parity here is the entire point of
		// the phase-ship rule.
		if _, err := fmt.Fprintln(w, lit.Value); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("interpreter does not yet support %T at %s; v0.1 work in progress", s, stmt.StmtPos())
	}
}
