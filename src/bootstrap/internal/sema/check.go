package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// synth synthesizes the type of an expression bottom-up (DESIGN-1b §4.1) — the
// ':=' direction. It records the result in Info.Types[e] (kept under the historic
// name ExprTypes) so the emitter reads it unchanged.
func (c *checker) synth(e ast.Expr) Type {
	t := c.synthExpr(e)
	c.info.ExprTypes[e] = t
	return t
}

// check checks an expression against an expected type, pushing the context down
// into the context-sensitive forms (numeric literals, composite literals, and
// closures) and otherwise falling back to synth (DESIGN-1b §4.1). It records and
// returns the resulting type but does not itself report an assignability error;
// the caller compares with assignable so it can phrase a context-appropriate
// message.
func (c *checker) check(e ast.Expr, want Type) Type {
	t := c.checkExpr(e, want)
	c.info.ExprTypes[e] = t
	return t
}

func (c *checker) checkExpr(e ast.Expr, want Type) Type {
	switch n := e.(type) {
	case *ast.IntLit:
		// literal defaulting: an integer literal adopts a numeric context type
		// (int by default, but 'x: u8 = 5' / 'x: float = 1' retype it) — §4.3.
		if isNumeric(want) {
			return want
		}
	case *ast.ListLit:
		return c.checkListLit(n, want)
	case *ast.ListFill:
		return c.checkListFill(n, want)
	case *ast.MapLit:
		return c.checkMapLit(n, want)
	case *ast.TupleLit:
		return c.checkTupleLit(n, want)
	case *ast.FnExpr:
		if fn, ok := underlyingFn(want); ok {
			return c.checkClosure(n, fn)
		}
	}
	return c.synthExpr(e)
}

func (c *checker) synthExpr(e ast.Expr) Type {
	switch n := e.(type) {
	case *ast.IntLit:
		return Int
	case *ast.FloatLit:
		return Float
	case *ast.BoolLit:
		return Bool
	case *ast.StrLit, *ast.RawStrLit, *ast.CmdLit:
		return Str
	case *ast.RuneLit:
		return types.Rune
	case *ast.ByteLit:
		return types.Byte
	case *ast.NilLit:
		return Nil
	case *ast.Ident:
		return c.inferIdent(n)
	case *ast.Unary:
		return c.inferUnary(n)
	case *ast.Binary:
		return c.inferBinary(n)
	case *ast.Call:
		return c.inferCall(n)
	case *ast.Field:
		return c.inferField(n)
	case *ast.TupleIndex:
		return c.inferTupleIndex(n)
	case *ast.Bracket:
		return c.inferBracket(n)
	case *ast.ListLit:
		return c.synthListLit(n)
	case *ast.ListFill:
		return c.synthListFill(n)
	case *ast.TupleLit:
		return c.synthTupleLit(n)
	case *ast.MapLit:
		return c.synthMapLit(n)
	case *ast.FnExpr:
		return c.synthFn(n)
	case *ast.IsExpr:
		c.synth(n.X)
		return Bool
	case *ast.Range:
		c.synth(n.Lo)
		if n.Hi != nil {
			c.synth(n.Hi)
		}
		return types.Unknown
	case *ast.MatchExpr:
		return c.inferMatch(n)
	case *ast.Coalesce:
		return c.inferCoalesce(n)
	case *ast.Try:
		return c.inferTry(n)
	case *ast.Force:
		return c.inferForce(n)
	case *ast.OptChain:
		return c.inferOptChain(n)
	case *ast.Diverge:
		return c.synthDiverge(n)
	case *ast.GuardExpr:
		return c.inferGuard(n)
	case *ast.Recv:
		// a channel receive yields Result[T]; with no stdlib channel model yet its
		// payload stays Unknown (FORK-4).
		c.synth(n.X)
		return types.Unknown
	}
	// blocks, if-expressions, f-strings, and the remaining group-8 operators are
	// modelled in later iterations; treat them as Unknown so they do not cascade.
	return types.Unknown
}

// synthBlock types a block as an expression and returns its value type: the type
// of its last statement when that is an expression, otherwise nil. It opens a
// nested scope so the block's bindings do not leak. It backs the guard expression
// (DESIGN-1b §6); ordinary statement blocks go through checkBlock.
func (c *checker) synthBlock(b *ast.Block) Type {
	c.pushScope()
	defer c.popScope()
	value := Nil
	for i, s := range b.Stmts {
		if es, ok := s.(*ast.ExprStmt); ok && i == len(b.Stmts)-1 {
			value = c.synth(es.X)
			continue
		}
		c.checkStmt(s)
		value = Nil
	}
	return value
}

func (c *checker) inferIdent(n *ast.Ident) Type {
	if sym := c.lookup(n.Name); sym != nil {
		return sym.typ
	}
	if _, ok := c.info.Funcs[n.Name]; ok {
		c.errorf(n.Span(), "functions are not first-class values in Phase 0: %q", n.Name)
		return Invalid
	}
	c.errorf(n.Span(), "undefined name %q", n.Name)
	return Invalid
}

// --- bindings -----------------------------------------------------------------

func (c *checker) checkBind(b *ast.BindStmt) {
	var typ Type
	if b.Type != nil {
		declared := c.resolveType(b.Type)
		vt := c.check(b.Value, declared)
		typ = declared
		if !bad(declared) && !bad(vt) && !c.assignable(declared, b.Value, vt) {
			c.errorf(b.Span(), "cannot bind %s to a %s binding", vt, declared)
		}
	} else if vt := c.synth(b.Value); vt == Nil {
		c.errorf(b.Span(), "cannot infer a type from nil; use a type annotation")
		typ = Invalid
	} else {
		typ = vt
	}
	c.info.BindTypes[b] = typ
	c.declare(b.Span(), b.Name, typ, b.Mut)
}

// --- reassignment -------------------------------------------------------------

func (c *checker) checkAssign(n *ast.Reassign) {
	lv, ok := n.Target.(*ast.LValueTarget)
	if !ok {
		// tuple/struct-shape destructuring targets are settled in a later iteration;
		// still synth the value so nested errors surface.
		c.synth(n.Value)
		return
	}
	lty, mutable, name, resolved := c.lvalue(lv.X)
	if !resolved {
		c.synth(n.Value)
		return
	}
	vt := c.check(n.Value, lty)
	if !mutable {
		c.errorf(n.Span(), "cannot assign to immutable binding %q", name)
	}
	if !bad(lty) && !bad(vt) && !c.assignable(lty, n.Value, vt) {
		c.errorf(n.Span(), "cannot assign %s to %q of type %s", vt, name, lty)
	}
}

// lvalue resolves an assignable expression (an Ident, or a Field/Bracket/
// TupleIndex chain rooted at one) to its type, its mutability, and the bound
// name for diagnostics. The final bool is false when the target is undefined.
func (c *checker) lvalue(e ast.Expr) (t Type, mutable bool, name string, ok bool) {
	switch x := e.(type) {
	case *ast.Ident:
		if sym := c.lookup(x.Name); sym != nil {
			return sym.typ, sym.mutable, x.Name, true
		}
		c.errorf(x.Span(), "undefined name %q", x.Name)
		return Invalid, false, x.Name, false
	case *ast.Field:
		bt, m, _, base := c.lvalue(x.X)
		if !base {
			return Invalid, false, x.Name, false
		}
		if st, ok := bt.(*types.Struct); ok && st.Def.Struct != nil {
			if f := findField(st.Def, x.Name); f != nil {
				return f.Type, m, x.Name, true
			}
			c.errorf(x.Span(), "type %s has no field %q", st.Def.Name, x.Name)
			return Invalid, m, x.Name, true
		}
		return types.Unknown, m, x.Name, true
	case *ast.Bracket, *ast.TupleIndex:
		// an element/field of a mutable container is itself a mutable place.
		return c.synth(e), true, "", true
	}
	return c.synth(e), true, "", true
}

// --- operators ----------------------------------------------------------------

func (c *checker) inferUnary(n *ast.Unary) Type {
	t := c.synth(n.X)
	if bad(t) {
		return t
	}
	switch n.Op {
	case token.Minus, token.MinusMod:
		if !isNumeric(t) {
			c.errorf(n.Span(), "operator %q requires a numeric operand, found %s", n.Op, t)
			return Invalid
		}
		return t
	case token.Not:
		if t != Bool {
			c.errorf(n.Span(), "operator 'not' requires a bool operand, found %s", t)
			return Invalid
		}
		return Bool
	case token.Tilde:
		if !isIntegral(t) {
			c.errorf(n.Span(), "operator '~' requires an int operand, found %s", t)
			return Invalid
		}
		return t
	}
	return Invalid
}

func (c *checker) inferBinary(n *ast.Binary) Type {
	lt := c.synth(n.L)
	rt := c.synth(n.R)
	if bad(lt) || bad(rt) {
		return firstBad(lt, rt)
	}
	switch {
	case isArithOp(n.Op):
		return c.numericResult(n, lt, rt)
	case isBitOp(n.Op):
		return c.bitResult(n, lt, rt)
	case isOrderOp(n.Op):
		if bad(c.numericResult(n, lt, rt)) {
			return Invalid
		}
		return Bool
	case isEqOp(n.Op):
		if !c.comparable(n, lt, rt) {
			c.errorf(n.Span(), "cannot compare %s and %s", lt, rt)
			return Invalid
		}
		return Bool
	case isLogicOp(n.Op):
		if lt != Bool || rt != Bool {
			c.errorf(n.Span(), "operator %q requires bool operands, found %s and %s", n.Op, lt, rt)
			return Invalid
		}
		return Bool
	case n.Op == token.In:
		// membership test 'v in coll' yields bool (GRAMMAR group 4).
		return Bool
	}
	return Invalid
}

// numericResult checks a numeric binary operation, coercing an untyped integer
// literal to the other operand's numeric type, and returns the result type.
func (c *checker) numericResult(n *ast.Binary, lt, rt Type) Type {
	if types.Identical(lt, rt) && isNumeric(lt) {
		return lt
	}
	if isNumeric(lt) && rt == Int && isIntLit(n.R) {
		c.info.ExprTypes[n.R] = lt
		return lt
	}
	if isNumeric(rt) && lt == Int && isIntLit(n.L) {
		c.info.ExprTypes[n.L] = rt
		return rt
	}
	c.errorf(n.Span(), "operator %q requires matching numeric operands, found %s and %s", n.Op, lt, rt)
	return Invalid
}

// bitResult checks a bitwise operation: both operands must be integral.
func (c *checker) bitResult(n *ast.Binary, lt, rt Type) Type {
	if isIntegral(lt) && isIntegral(rt) {
		if types.Identical(lt, rt) {
			return lt
		}
		if rt == Int && isIntLit(n.R) {
			c.info.ExprTypes[n.R] = lt
			return lt
		}
		if lt == Int && isIntLit(n.L) {
			c.info.ExprTypes[n.L] = rt
			return rt
		}
	}
	c.errorf(n.Span(), "operator %q requires int operands, found %s and %s", n.Op, lt, rt)
	return Invalid
}

func (c *checker) comparable(n *ast.Binary, lt, rt Type) bool {
	if types.Identical(lt, rt) {
		return true
	}
	if isNumeric(lt) && rt == Int && isIntLit(n.R) {
		c.info.ExprTypes[n.R] = lt
		return true
	}
	if isNumeric(rt) && lt == Int && isIntLit(n.L) {
		c.info.ExprTypes[n.L] = rt
		return true
	}
	return false
}

// assignable reports whether a value of type vt (from expr e) fits a target of
// type want, allowing an untyped integer literal to adopt a numeric target
// (literal defaulting). Unknown and Invalid are compatible with anything, so a
// prior error or an un-modelled value does not cascade.
func (c *checker) assignable(want Type, e ast.Expr, vt Type) bool {
	if types.Identical(want, vt) {
		return true
	}
	if isNumeric(want) && vt == Int && isIntLit(e) {
		c.info.ExprTypes[e] = want
		return true
	}
	return false
}

// --- composite literals -------------------------------------------------------

func (c *checker) checkListLit(n *ast.ListLit, want Type) Type {
	switch w := want.(type) {
	case *types.List:
		for _, el := range n.Elems {
			c.checkElem(el, w.Elem, "list element")
		}
		return w
	case *types.Set:
		for _, el := range n.Elems {
			c.checkElem(el, w.Elem, "set element")
		}
		return w
	case *types.Array:
		if w.N.Known && int64(len(n.Elems)) != w.N.I {
			c.errorf(n.Span(), "array literal has %d element(s), expected %d", len(n.Elems), w.N.I)
		}
		for _, el := range n.Elems {
			c.checkElem(el, w.Elem, "array element")
		}
		return w
	}
	return c.synthListLit(n)
}

func (c *checker) synthListLit(n *ast.ListLit) Type {
	if len(n.Elems) == 0 {
		c.errorf(n.Span(), "cannot infer the element type of an empty list; use a type annotation")
		return Invalid
	}
	elem := c.synth(n.Elems[0])
	for _, el := range n.Elems[1:] {
		c.checkElem(el, elem, "list element")
	}
	return &types.List{Elem: elem}
}

func (c *checker) checkListFill(n *ast.ListFill, want Type) Type {
	switch w := want.(type) {
	case *types.Array:
		c.checkElem(n.Value, w.Elem, "fill value")
		if kv, ok := c.foldConst(n.Count); ok && w.N.Known && kv.I != w.N.I {
			c.errorf(n.Span(), "fill count %d does not match array length %d", kv.I, w.N.I)
		}
		return w
	case *types.List:
		c.checkElem(n.Value, w.Elem, "fill value")
		c.synth(n.Count)
		return w
	}
	return c.synthListFill(n)
}

func (c *checker) synthListFill(n *ast.ListFill) Type {
	elem := c.synth(n.Value)
	c.synth(n.Count)
	return &types.List{Elem: elem}
}

func (c *checker) checkTupleLit(n *ast.TupleLit, want Type) Type {
	if w, ok := want.(*types.Tuple); ok && len(w.Elems) == len(n.Elems) {
		for i, el := range n.Elems {
			c.checkElem(el, w.Elems[i], "tuple element")
		}
		return w
	}
	return c.synthTupleLit(n)
}

func (c *checker) synthTupleLit(n *ast.TupleLit) Type {
	elems := make([]Type, len(n.Elems))
	for i, el := range n.Elems {
		elems[i] = c.synth(el)
	}
	return &types.Tuple{Elems: elems}
}

func (c *checker) checkMapLit(n *ast.MapLit, want Type) Type {
	if w, ok := want.(*types.Map); ok {
		for _, en := range n.Entries {
			c.checkElem(en.Key, w.Key, "map key")
			c.checkElem(en.Value, w.Val, "map value")
		}
		return w
	}
	return c.synthMapLit(n)
}

func (c *checker) synthMapLit(n *ast.MapLit) Type {
	if len(n.Entries) == 0 {
		c.errorf(n.Span(), "cannot infer the type of an empty map; use a type annotation")
		return Invalid
	}
	k := c.synth(n.Entries[0].Key)
	v := c.synth(n.Entries[0].Value)
	for _, en := range n.Entries[1:] {
		c.checkElem(en.Key, k, "map key")
		c.checkElem(en.Value, v, "map value")
	}
	return &types.Map{Key: k, Val: v}
}

// checkElem checks a composite-literal element against its expected type and
// reports one span-anchored diagnostic on a mismatch.
func (c *checker) checkElem(e ast.Expr, want Type, what string) {
	t := c.check(e, want)
	if !bad(t) && !bad(want) && !c.assignable(want, e, t) {
		c.errorf(e.Span(), "%s: cannot use %s as %s", what, t, want)
	}
}

// --- type predicates ----------------------------------------------------------

// bad reports whether a type is a cascade-suppressing placeholder (a prior error
// or an un-modelled value); such a type is compatible with anything.
func bad(t Type) bool {
	return t == nil || t.Kind() == types.KInvalid || t.Kind() == types.KUnknown
}

func firstBad(a, b Type) Type {
	if bad(a) {
		return a
	}
	return b
}

// isNumeric reports whether t is one of the numeric families (int/uint/float,
// rune/byte, or a fixed-width numeric).
func isNumeric(t Type) bool {
	switch t.Kind() {
	case types.KInt, types.KUint, types.KFloat, types.KRune, types.KByte, types.KFixedInt:
		return true
	}
	return false
}

// isIntegral reports whether t is an integer family (numeric but not a float).
func isIntegral(t Type) bool {
	if f, ok := t.(*types.Fixed); ok {
		return !f.Float
	}
	switch t.Kind() {
	case types.KInt, types.KUint, types.KRune, types.KByte:
		return true
	}
	return false
}

// underlyingFn returns the function type of t (a *types.Fn), if it is one.
func underlyingFn(t Type) (*types.Fn, bool) {
	fn, ok := t.(*types.Fn)
	return fn, ok
}

// isIntLit reports whether e is an integer literal (used for literal defaulting).
func isIntLit(e ast.Expr) bool { _, ok := e.(*ast.IntLit); return ok }

// findField returns a struct's field by name, or nil.
func findField(def *types.TypeDef, name string) *types.FieldDef {
	if def.Struct == nil {
		return nil
	}
	for i := range def.Struct.Fields {
		if def.Struct.Fields[i].Name == name {
			return &def.Struct.Fields[i]
		}
	}
	return nil
}
