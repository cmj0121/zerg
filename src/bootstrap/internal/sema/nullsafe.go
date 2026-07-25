package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// This file types the group-8 null-safety operators (DESIGN-1b §6). Each rests on
// the Either/Result/optional shapes leftType models: '?' and '!' unwrap the Left
// value, '??' supplies a default (or diverges) for a missing one, '?.' reads a
// field through an optional, 'raise' aborts, and 'guard' demotes an abort back to
// a Result. An operand of an un-modelled type (Unknown) or a prior error (Invalid)
// yields Unknown/Invalid so the operators never cascade.

// inferTry types the propagate postfix 'x?' (DESIGN-1b §6): x must be an Either,
// Result, or optional; the expression yields its Left type and early-returns the
// Right, so the enclosing function must itself return a compatible Either/Result/
// optional.
func (c *checker) inferTry(n *ast.Try) Type {
	xt := c.synth(n.X)
	if bad(xt) {
		return types.Unknown
	}
	lt, ok := leftType(xt)
	if !ok {
		c.errorf(n.Span(), "'?' requires an Either, Result, or optional value, found %s", xt)
		return Invalid
	}
	ret := Nil
	if c.curFn != nil {
		ret = c.curFn.Ret
	}
	if !bad(ret) {
		if _, ok := leftType(ret); !ok {
			c.errorf(n.Span(), "'?' can only be used in a function returning Either, Result, or an optional")
		}
	}
	return lt
}

// inferCoalesce types the default operator 'a ?? b' (DESIGN-1b §6): a must be an
// optional or Either; the expression yields its Left type T; b is checked against
// T unless it diverges (break/continue/return/raise produce no value).
func (c *checker) inferCoalesce(n *ast.Coalesce) Type {
	at := c.synth(n.X)
	var t Type = types.Unknown
	if !bad(at) {
		lt, ok := leftType(at)
		if !ok {
			c.errorf(n.X.Span(), "'??' requires an optional or Either left operand, found %s", at)
			t = Invalid
		} else {
			t = lt
		}
	}
	if _, diverges := n.Y.(*ast.Diverge); diverges {
		c.synth(n.Y) // resolve the escape for nested errors; it produces no value
		return t
	}
	bt := c.check(n.Y, t)
	if !bad(t) && !bad(bt) && !c.assignable(t, n.Y, bt) {
		c.errorf(n.Y.Span(), "'??' default: cannot use %s as %s", bt, t)
	}
	return t
}

// inferOptChain types the optional-chain postfix 'a?.b' (DESIGN-1b §6): a must be
// an optional 'S?'; when S is a struct, b names one of its fields and the result is
// that field's type made optional; a missing link is nil at run time.
func (c *checker) inferOptChain(n *ast.OptChain) Type {
	at := c.synth(n.X)
	if bad(at) {
		return types.Unknown
	}
	opt, ok := at.(*types.Opt)
	if !ok {
		c.errorf(n.Span(), "'?.' requires an optional value, found %s", at)
		return Invalid
	}
	u := types.Type(types.Unknown)
	if st, ok := opt.Elem.(*types.Struct); ok && st.Def.Struct != nil {
		f := findField(st.Def, n.Name)
		if f == nil {
			c.errorf(n.Span(), "type %s has no field %q", st.Def.Name, n.Name)
			return Invalid
		}
		u = f.Type
	}
	return &types.Opt{Elem: u}
}

// inferForce types the force-unwrap postfix 'x!' (DESIGN-1b §6): x must be an
// Either, Result, or optional; the expression yields its Left type and aborts at
// run time when the value is absent.
func (c *checker) inferForce(n *ast.Force) Type {
	xt := c.synth(n.X)
	if bad(xt) {
		return types.Unknown
	}
	lt, ok := leftType(xt)
	if !ok {
		c.errorf(n.Span(), "'!' requires an Either, Result, or optional value, found %s", xt)
		return Invalid
	}
	return lt
}

// inferGuard types the guard expression 'guard { block }' (DESIGN-1b §6): it
// demotes any abort inside the block back to a value, so it yields 'Result[T]'
// where T is the block's value type.
func (c *checker) inferGuard(n *ast.GuardExpr) Type {
	return resultType(c.synthBlock(n.Body))
}

// synthDiverge types a control-flow escape ('break', 'continue', 'return e?', or
// 'raise e (from c)?') used as the right side of '??'. The escape produces no
// value; its operands are still synthesized so nested errors surface.
func (c *checker) synthDiverge(n *ast.Diverge) Type {
	if n.Value != nil {
		c.synth(n.Value)
	}
	if n.From != nil {
		c.synth(n.From)
	}
	return types.Unknown
}
