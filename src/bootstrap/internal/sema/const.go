package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// foldConst evaluates a compile-time-constant expression to a value (DESIGN-1b
// §4.8). Phase 1b needs only the minimal set an array length '[T; N]', an enum
// discriminant, or a value-generic argument can hold: integer and boolean
// literals and the basic arithmetic/negation over them. The second result is
// false when the expression is not foldable, leaving the caller to report the
// error at the use site.
func (c *checker) foldConst(e ast.Expr) (types.ConstVal, bool) {
	switch n := e.(type) {
	case *ast.IntLit:
		return intConst(n.Value), true
	case *ast.BoolLit:
		return types.ConstVal{Kind: types.KBool, B: n.Value, Known: true}, true
	case *ast.Unary:
		v, ok := c.foldConst(n.X)
		if !ok || v.Kind != types.KInt {
			return types.ConstVal{}, false
		}
		return foldUnary(n.Op, v)
	case *ast.Binary:
		l, okl := c.foldConst(n.L)
		r, okr := c.foldConst(n.R)
		if !okl || !okr || l.Kind != types.KInt || r.Kind != types.KInt {
			return types.ConstVal{}, false
		}
		return foldBinary(n.Op, l, r)
	}
	return types.ConstVal{}, false
}

func intConst(i int64) types.ConstVal {
	return types.ConstVal{Kind: types.KInt, I: i, Known: true}
}

func foldUnary(op token.Kind, v types.ConstVal) (types.ConstVal, bool) {
	switch op {
	case token.Minus, token.MinusMod:
		return intConst(-v.I), true
	case token.Tilde:
		return intConst(^v.I), true
	}
	return types.ConstVal{}, false
}

func foldBinary(op token.Kind, l, r types.ConstVal) (types.ConstVal, bool) {
	switch op {
	case token.Plus, token.PlusMod:
		return intConst(l.I + r.I), true
	case token.Minus, token.MinusMod:
		return intConst(l.I - r.I), true
	case token.Star, token.StarMod:
		return intConst(l.I * r.I), true
	case token.Slash:
		if r.I == 0 {
			return types.ConstVal{}, false
		}
		return intConst(l.I / r.I), true
	case token.Percent:
		if r.I == 0 {
			return types.ConstVal{}, false
		}
		return intConst(l.I % r.I), true
	}
	return types.ConstVal{}, false
}
