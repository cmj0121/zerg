package sema

import (
	"math"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// foldConst evaluates a compile-time-constant expression to a value (DESIGN-1b
// §4.8). It is the CONST-EXPR of GRAMMAR group 7: integer and boolean literals,
// the arithmetic over them, and a NAME whose own binding folds — "any binding, ':='
// or 'const', module-level or local, whose initializer is a CONST-EXPR". There are
// no calls, so 'sizeof'/'len' are out. The second result is false when the
// expression is not foldable, leaving the caller to report the error at the use
// site, which is where the reader can see what the value was wanted for.
func (c *checker) foldConst(e ast.Expr) (types.ConstVal, bool) {
	switch n := e.(type) {
	case *ast.IntLit:
		return intConst(n.Value), true
	case *ast.BoolLit:
		return types.ConstVal{Kind: types.KBool, B: n.Value, Known: true}, true
	case *ast.Ident:
		return c.constOfName(n.Name)
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

// constOfName is the NAME leaf of the fold: what a binding of this name is worth at
// compile time, or the news that it is worth nothing.
//
// THE INNERMOST BINDING ANSWERS, and it answers even when the answer is "not a
// constant": a local 'n := f()' shadowing a module 'const n := 4' must fold to
// nothing rather than to 4, so the scope walk stops at the first binding of the name
// instead of falling through to the module. The local scopes are walked directly
// rather than through c.lookup, because a fold is not a use and must not record a
// closure capture.
//
// A MODULE CONSTANT IS FOLDED FROM ITS DECLARATION, on demand. Nothing pre-computes
// them: the constants are checked in dependency order elsewhere, and a compile-time
// position may be reached before that order has been established (an enum
// discriminant is resolved with the type declarations), so the value is worked out
// from the initializer the symbol points back at.
func (c *checker) constOfName(name string) (types.ConstVal, bool) {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if s, ok := c.scopes[i][name]; ok {
			if s.cval.Known {
				return s.cval, true
			}
			return types.ConstVal{}, false
		}
	}
	sym := c.module.lookup(name)
	if sym == nil || (sym.Kind != SymConst && sym.Kind != SymVar) {
		return types.ConstVal{}, false
	}
	b, ok := sym.Decl.(*ast.BindStmt)
	if !ok || b.Mut {
		return types.ConstVal{}, false
	}
	// 'const A := A' and any longer cycle would recurse forever here. The declaration
	// is marked while its own initializer is being folded, so a name that reaches
	// itself is simply not a constant — the cycle itself is reported by the
	// dependency sort, which is the pass that can name the whole ring.
	if c.folding[b] {
		return types.ConstVal{}, false
	}
	c.folding[b] = true
	defer delete(c.folding, b)
	return c.foldConst(b.Value)
}

// bindConstVal records what a binding's initializer folds to, on the symbol declare
// has just created — the LOCAL half of GRAMMAR group 7, which is what lets 'n := 4'
// be a fill count. A 'mut' binding folds to nothing however constant its initializer
// looks: a compile-time position asks for a value fixed for the whole program, and a
// name that may be assigned tomorrow is not one.
func (c *checker) bindConstVal(name string, value ast.Expr, mut bool) {
	if mut || value == nil {
		return
	}
	sym, ok := c.scopes[len(c.scopes)-1][name]
	if !ok {
		return
	}
	if v, ok := c.foldConst(value); ok {
		sym.cval = v
	}
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
	case token.Slash, token.SlashDiv:
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
