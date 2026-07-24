package emit

import (
	"fmt"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
)

// This file lowers the binary operations over `str`. A Zerg `str` is a NUL-terminated
// `const char*`, so neither operator the language gives it maps to a C operator on
// that pointer:
//
//   - `a + b` concatenates into a NEW str (docs/collections.md: `str` implements Add),
//     which is the runtime's zrt_str_concat — it allocates a fresh Zerg-owned string.
//   - a comparison sorts lexicographically by code point (`str` implements Ord), which
//     is strcmp. A native `==` would compare POINTERS instead, so two equal strings
//     built by different routes would silently test unequal — the reason
//     registerPrimitiveBlessed used to withhold Eq/Ord from `str` altogether.
//
// Both are gated on the operand type being `str`, so every other operand keeps its
// native C operator and its emitted C stays byte-identical.

// strBinary lowers a binary operation over two `str` operands. It reports false for
// any other operand type, leaving the caller on the native C operator path.
func (e *emitter) strBinary(n *ast.Binary) (string, bool) {
	if !e.isStrExpr(n.L) || !e.isStrExpr(n.R) {
		return "", false
	}
	switch n.Op {
	case token.Plus:
		return fmt.Sprintf("zrt_str_concat(%s, %s)", e.expr(n.L), e.expr(n.R)), true
	case token.EqEq, token.Ne, token.Lt, token.Le, token.Gt, token.Ge:
		return fmt.Sprintf("(strcmp(%s, %s) %s 0)", e.expr(n.L), e.expr(n.R), binaryOp(n.Op)), true
	}
	return "", false
}

// isStrExpr reports whether an expression's type is `str` in the instance being
// emitted, reading through the instance's substitution overlay so a generic body
// monomorphized at T = str is recognized too.
func (e *emitter) isStrExpr(x ast.Expr) bool {
	if e.cur == nil {
		return e.info.ExprTypes[x] == sema.Str
	}
	return e.cur.ExprType(e.info, x) == sema.Str
}

// programUsesStrConcat reports whether the program concatenates two strings — the one
// str operation that needs the runtime, since zrt_str_concat rides in the
// always-linked fmt.c. A str COMPARISON needs only <string.h>, which every emitted
// program already includes. A program that concatenates nothing leaves the flag false
// and stays byte-identical.
func (e *emitter) programUsesStrConcat() bool {
	for node, t := range e.info.ExprTypes {
		if b, ok := node.(*ast.Binary); ok && b.Op == token.Plus && t == sema.Str {
			return true
		}
	}
	return false
}
