package sema

import (
	"math"

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
	case token.Minus:
		// the minimum has no positive counterpart, so negating it does not fold
		if v.I == math.MinInt64 {
			return types.ConstVal{}, false
		}
		return intConst(-v.I), true
	case token.MinusMod:
		return intConst(-v.I), true
	case token.Tilde:
		return intConst(^v.I), true
	}
	return types.ConstVal{}, false
}

// foldBinary evaluates one operator over two known integers. It is the SAME
// arithmetic the emitted program performs (docs/core/types.md): `/` and `%` are
// Euclidean, and an operation that would raise at runtime does not fold — a
// compile-time `-7 % 3` answering -1 while the identical expression answers 2 at
// runtime is one language rule with two answers inside one compiler.
//
// Not folding is the right refusal rather than a diagnostic here: the caller
// reports at the use site, which is where the reader can see what the constant was
// wanted for.
func foldBinary(op token.Kind, l, r types.ConstVal) (types.ConstVal, bool) {
	switch op {
	case token.Plus:
		return checkedConst(func() (int64, bool) { s, o := addOverflow(l.I, r.I); return s, !o })
	case token.Minus:
		return checkedConst(func() (int64, bool) { d, o := subOverflow(l.I, r.I); return d, !o })
	case token.Star:
		return checkedConst(func() (int64, bool) { p, o := mulOverflow(l.I, r.I); return p, !o })
	case token.PlusMod:
		return intConst(l.I + r.I), true
	case token.MinusMod:
		return intConst(l.I - r.I), true
	case token.StarMod:
		return intConst(l.I * r.I), true
	case token.Slash:
		if r.I == 0 || (l.I == math.MinInt64 && r.I == -1) {
			return types.ConstVal{}, false
		}
		q := l.I / r.I
		if l.I%r.I < 0 {
			if r.I > 0 {
				q--
			} else {
				q++
			}
		}
		return intConst(q), true
	case token.Percent:
		if r.I == 0 {
			return types.ConstVal{}, false
		}
		if l.I == math.MinInt64 && r.I == -1 {
			return intConst(0), true
		}
		m := l.I % r.I
		if m < 0 {
			if r.I > 0 {
				m += r.I
			} else {
				m -= r.I
			}
		}
		return intConst(m), true
	}
	return types.ConstVal{}, false
}

// checkedConst turns "the answer, or it overflowed" into the fold's two results.
func checkedConst(f func() (int64, bool)) (types.ConstVal, bool) {
	v, ok := f()
	if !ok {
		return types.ConstVal{}, false
	}
	return intConst(v), true
}

// The three overflow tests, in the same order the runtime states them
// (src/runtime/csrc/zergrt.h). Go has no __builtin_add_overflow, so they are
// written out; each returns the wrapped result and whether it wrapped.

func addOverflow(a, b int64) (int64, bool) {
	s := a + b
	return s, (a > 0 && b > 0 && s < 0) || (a < 0 && b < 0 && s >= 0)
}

func subOverflow(a, b int64) (int64, bool) {
	d := a - b
	return d, (a >= 0 && b < 0 && d < 0) || (a < 0 && b > 0 && d >= 0)
}

func mulOverflow(a, b int64) (int64, bool) {
	p := a * b
	if a == 0 {
		return p, false
	}
	return p, p/a != b || (a == -1 && b == math.MinInt64)
}
