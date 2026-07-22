package emit

import (
	"strings"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
)

// Inline-assembly lowering (GRAMMAR group 12, Phase 1h U3). An `asm(...)` expression
// lowers to a GCC extended-asm statement wrapped in a GNU statement-expression that
// yields 0 (asm types as nil):
//
//	({ __asm__ __volatile__(<template> : <outputs> : <inputs> : <clobbers>); 0; })
//
// The template and every constraint string are passed through verbatim (as C string
// literals) — their meaning is the target backend's. `__volatile__` is always added:
// inline asm is treated as having side effects, so `cc` may not move or drop it.
// Only a program that spells `asm(...)` reaches asmExpr, so the existing examples
// stay byte-identical.
//
// Operand mapping (extended-asm numbers %0, %1, … in output-then-input order):
//   - out(c) / inout(c) lvalue -> output slot `"<c>"(<lvalue>)`
//   - in(c) value              -> input slot  `"<c>"(<value>)`
//   - clobber(c, …)            -> clobber list `"<c>", …`
//
// The author writes the GCC constraint form directly (`"=r"` for an output, `"+r"`
// for an inout, a bare `"r"`/`"m"` or a register name for an input); the compiler
// never synthesizes the `=`/`+` prefix.
func (e *emitter) asmExpr(n *ast.AsmExpr) string {
	var outputs, inputs, clobbers []string
	for _, op := range n.Operands {
		switch op.Kind {
		case ast.AsmOut, ast.AsmInout:
			outputs = append(outputs, cString(op.Constraint)+"("+e.expr(op.Value)+")")
		case ast.AsmIn:
			inputs = append(inputs, cString(op.Constraint)+"("+e.expr(op.Value)+")")
		case ast.AsmClobber:
			for _, reg := range op.Clobbers {
				clobbers = append(clobbers, cString(reg))
			}
		}
	}
	asm := "__asm__ __volatile__(" + cString(n.Template) +
		" : " + strings.Join(outputs, ", ") +
		" : " + strings.Join(inputs, ", ") +
		" : " + strings.Join(clobbers, ", ") + ")"
	return "({ " + asm + "; 0; })"
}
