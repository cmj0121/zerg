package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
)

// Inline assembly (GRAMMAR group 12, Phase 1h U3). An `asm(...)` expression is a
// statement-like unsafe operation: it emits a target instruction sequence and
// yields nil. Like every raw operation it is legal only inside an `unsafe { }`
// context, and — mirroring the raw-pointer intrinsics — the runtime gains no new
// C: emit lowers it straight to a GCC extended-asm statement.
//
// The template and every constraint string are OPAQUE — their meaning belongs to
// the target backend (the constraint spelling "=r"/"+r"/"r"/"m"/"x0" is verbatim,
// passed to `cc` untouched), so sema does not parse or validate them. It only
// checks the operand data flow: an `in(c) expr` carries a value, while an
// `out(c) lvalue` / `inout(c) lvalue` names a writable place.

// inferAsm types an inline-asm expression: it gates the operation to an unsafe
// context, resolves each operand (a value for `in`, an assignable lvalue for
// `out`/`inout`, nothing for `clobber`), and yields nil since asm is used for its
// effect, not its value.
func (c *checker) inferAsm(n *ast.AsmExpr) Type {
	c.unsafeOp(n.Span(), "inline asm")
	for _, op := range n.Operands {
		switch op.Kind {
		case ast.AsmIn:
			c.synth(op.Value)
		case ast.AsmOut, ast.AsmInout:
			c.asmLvalue(op)
		case ast.AsmClobber:
			// clobber(reg, …) names registers the asm overwrites; the strings are
			// opaque to the backend, so there is nothing to resolve.
		}
	}
	return Nil
}

// asmLvalue resolves the place an `out`/`inout` asm operand writes through: it must
// name addressable storage (a variable, field, or element), since the asm writes
// into it directly. Like `addr(x)`, it does not require the binding to be `mut` — the
// write is a raw effect the author vouches for, not an ordinary reassignment. The
// resolved type is recorded so the emitter renders the operand as a C lvalue.
func (c *checker) asmLvalue(op *ast.AsmOperand) {
	c.synth(op.Value)
	if !isAddressable(op.Value) {
		c.errorf(op.Value.Span(), "an asm out/inout operand must be an assignable lvalue (a variable, field, or element)")
	}
}
