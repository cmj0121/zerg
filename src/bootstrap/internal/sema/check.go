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
			c.checkIntRange(n, want, false)
			return want
		}
	case *ast.FloatLit:
		// a fractional literal adopts a float context type (float / f32 / f64).
		if isFloatWant(want) {
			return want
		}
	case *ast.Unary:
		// a negated numeric literal '-lit' pushes the numeric context through the
		// sign to the inner literal so defaulting applies (§4.3).
		if t, ok := c.checkSignedLit(n, want); ok {
			return t
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
	case *ast.ChanNew:
		return c.inferChanNew(n)
	case *ast.Recv:
		return c.inferRecv(n)
	case *ast.FStr:
		return c.inferFStr(n)
	case *ast.UnsafeExpr:
		return c.inferUnsafe(n)
	}
	// blocks, if-expressions, f-cmds, and the remaining group-8 operators are
	// modelled in later iterations; treat them as Unknown so they do not cascade.
	return types.Unknown
}

// inferFStr types an f-string f"…{expr}…" (GRAMMAR group 5), the Format cluster's
// entry point: it synthesizes every hole expression (so the backend can pick the
// hole's display()/format(:spec) rendering by the hole's static type) and yields
// str. The `:spec` text is opaque to the language — its meaning is the hole type's
// own Format impl — so it is not type-checked here. A hole with no display/format
// rendering for its type is caught at lowering, not here.
func (c *checker) inferFStr(n *ast.FStr) Type {
	for i := range n.Parts {
		if n.Parts[i].Expr != nil {
			c.synth(n.Parts[i].Expr)
		}
	}
	return Str
}

// inferUnsafe types the function-body `unsafe { block }` block-expression (GRAMMAR
// group 12): raw operations — foreign calls, `asm`, raw-pointer ops — are legal
// inside, and it yields the block's value. It marks the body an unsafe context so a
// foreign (`#[extern]`-bound) call within it is accepted; outside such a context
// that same call is rejected (docs/ffi.md: a foreign call is unsafe).
func (c *checker) inferUnsafe(n *ast.UnsafeExpr) Type {
	saved := c.inUnsafe
	c.inUnsafe = true
	defer func() { c.inUnsafe = saved }()
	return c.synthBlock(n.Body)
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

// inferChanNew types the channel constructor 'chan[T](cap?)' (GRAMMAR group 9): it
// yields a bidirectional 'chan[T]' — narrowing to a send-/receive-only handle is a
// property of the binding it flows into. An explicit capacity must be an integer
// (0 = unbuffered rendezvous); an omitted capacity is unbuffered.
func (c *checker) inferChanNew(n *ast.ChanNew) Type {
	elem := c.resolveType(n.Elem)
	if n.Cap != nil {
		if ct := c.synth(n.Cap); !bad(ct) && !isIntegral(ct) {
			c.errorf(n.Cap.Span(), "channel capacity must be an integer, found %s", ct)
		}
	}
	return &types.Chan{Elem: elem, Dir: types.ChanBidi}
}

// inferRecv types a channel receive '<-ch' (GRAMMAR group 9): it yields Result[T]
// (Left = a received value, Right = the channel closed, carrying StopIteration or a
// crash Err). Receiving from a send-only channel is rejected (directional narrowing).
func (c *checker) inferRecv(n *ast.Recv) Type {
	xt := c.synth(n.X)
	ch, ok := xt.(*types.Chan)
	if !ok {
		if !bad(xt) {
			c.errorf(n.Span(), "receive '<-' requires a channel, found %s", xt)
		}
		return types.Unknown
	}
	if ch.Dir == types.ChanSend {
		c.errorf(n.Span(), "cannot receive from a send-only channel %s", ch)
	}
	return resultType(ch.Elem)
}

func (c *checker) inferIdent(n *ast.Ident) Type {
	if sym := c.lookup(n.Name); sym != nil {
		c.checkLive(sym, n.Span(), n.Name)
		return sym.typ
	}
	// an enum variant is a value: a nullary variant names a value of its enum type,
	// while a payload variant is only usable as a constructor 'V(...)' (§3.6 C1).
	if sym := c.module.lookup(n.Name); sym != nil && sym.Kind == SymVariant {
		if sym.Variant != nil && len(sym.Variant.Payload) != 0 {
			c.errorf(n.Span(), "variant %q requires a payload; use %s(...)", n.Name, n.Name)
			return Invalid
		}
		return c.enumType(sym)
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
	// Record the resolved target type (mirroring how the RHS is recorded), so the
	// emitter's reassignment lowering sees a Ref/Ref-holding target and emits its
	// release-old + retain-new — not the POD bare '='. Without this a Ref target
	// double-frees / leaks. A POD target records its POD type, so the emitted C is
	// unchanged.
	c.info.ExprTypes[lv.X] = lty
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
			// a reassignment (or a field/index write rooted here) counts as a use, so
			// writing through a name revoked by `del` is rejected (DESIGN-1d §5.1).
			c.checkLive(sym, x.Span(), x.Name)
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
	case *ast.Bracket:
		_, m, nm, base := c.lvalue(x.Base)
		if !base {
			return Invalid, false, nm, false
		}
		// an element of a container is a mutable place only when its base is.
		return c.synth(e), m, nm, true
	case *ast.TupleIndex:
		_, m, nm, base := c.lvalue(x.X)
		if !base {
			return Invalid, false, nm, false
		}
		return c.synth(e), m, nm, true
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
	// A generic operand routes through its bound spec: 'a < b' on a type parameter
	// resolves to Ord, '==' to Eq (U3, DESIGN-1c §3.3). Concrete operands keep their
	// primitive semantics below.
	if isTypeParam(lt) || isTypeParam(rt) {
		return c.inferGenericOp(n, lt, rt)
	}
	// A comparison on a nominal (struct/enum) operand is not a native C operator: it
	// requires the corresponding Eq/Ord impl (derived or hand-written), and the
	// backend lowers it to that method (B2, DESIGN-1c §3.3).
	if isCompareOp(n.Op) && (isNominal(lt) || isNominal(rt)) {
		return c.inferNominalCompare(n, lt, rt)
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

// isNominal reports whether a type is a user struct or enum — the operands whose
// comparison must go through an Eq/Ord impl rather than a native C operator.
func isNominal(t Type) bool {
	switch t.(type) {
	case *types.Struct, *types.Enum:
		return true
	}
	return false
}

// isCompareOp reports whether an operator is an equality or ordering comparison.
func isCompareOp(k token.Kind) bool { return isEqOp(k) || isOrderOp(k) }

// inferNominalCompare types a comparison whose operand is a nominal type: the
// operands must be the same type, and it must have the Eq (for '=='/'!=') or Ord
// (for '<'/'<='/'>'/'>=') impl the comparison lowers to (B2). It yields bool, or
// Invalid with a diagnostic when the impl is missing.
func (c *checker) inferNominalCompare(n *ast.Binary, lt, rt Type) Type {
	if !types.Identical(lt, rt) {
		c.errorf(n.Span(), "cannot compare %s and %s", lt, rt)
		return Invalid
	}
	spec := "Eq"
	if isOrderOp(n.Op) {
		spec = "Ord"
	}
	reg := c.info.Specs
	if reg != nil {
		if sp := reg.Specs[spec]; sp == nil || reg.resolveImpl(sp, lt) == nil {
			c.errorf(n.Span(), "operator %q on %s requires an impl of %s; add #[derive(%s)] or write one",
				n.Op, lt, spec, spec)
			return Invalid
		}
	}
	return Bool
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
	// Context-typed Ok/Left construction: a value of type T widens implicitly to a
	// Result[T] / Either[T, _] (its Left/Ok) or a T? optional (its present case),
	// and `nil` widens to the empty case of a T? or a Result[nil]. This is what lets
	// `fn f() -> Result[int] { return 5 }` and `return nil` in a Result[nil] read as
	// the wrapped value; the emitter builds the carrier from the recorded value type.
	if lt, wrap := leftType(want); wrap {
		if vt == Nil && (isOptional(want) || bad(lt) || lt == Nil) {
			return true
		}
		if !bad(vt) && c.assignable(lt, e, vt) {
			return true
		}
	}
	// A bidirectional channel narrows implicitly to a receive-only or send-only handle
	// of the same element type (GRAMMAR group 9: 'chan[T]' narrows to '<-chan[T]' /
	// 'chan[T]<-'). Directional narrowing is what lets a program hand a send-capable copy
	// to a producer while keeping a receive-only handle — so the channel can auto-close
	// when the last sender leaves and a 'select' can observe 'done'.
	if want, ok := want.(*types.Chan); ok {
		if vt, ok := vt.(*types.Chan); ok {
			return vt.Dir == types.ChanBidi && types.Identical(want.Elem, vt.Elem)
		}
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
		c.errorf(n.Span(), "tuple values are not yet supported")
		return w
	}
	return c.synthTupleLit(n)
}

func (c *checker) synthTupleLit(n *ast.TupleLit) Type {
	elems := make([]Type, len(n.Elems))
	for i, el := range n.Elems {
		elems[i] = c.synth(el)
	}
	// A tuple VALUE has no backend lowering yet (its per-shape C struct is not
	// generated), so the emitter would produce `void zg_t = 0;`. Reject it cleanly
	// here rather than emit bad C; the value carrier is tracked for a later slice.
	c.errorf(n.Span(), "tuple values are not yet supported")
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

// --- numeric literal context typing -------------------------------------------

// checkSignedLit context-types a negated numeric literal '-lit' by pushing a
// numeric want through the sign to the inner literal, so literal defaulting and
// the fixed-width overflow check apply (DESIGN-1b §4.3). It reports whether it
// handled the expression.
func (c *checker) checkSignedLit(n *ast.Unary, want Type) (Type, bool) {
	if (n.Op != token.Minus && n.Op != token.MinusMod) || !isNumeric(want) {
		return nil, false
	}
	switch lit := n.X.(type) {
	case *ast.IntLit:
		c.info.ExprTypes[lit] = want
		c.checkIntRange(lit, want, true)
		return want, true
	case *ast.FloatLit:
		if isFloatWant(want) {
			c.info.ExprTypes[lit] = want
			return want, true
		}
	}
	return nil, false
}

// checkIntRange reports an integer literal that overflows a fixed-width integer
// context type (DESIGN-1b §4.3 MINOR); other numeric contexts impose no
// compile-time literal bound here. When neg is set the literal carries a leading
// '-'.
func (c *checker) checkIntRange(lit *ast.IntLit, want Type, neg bool) {
	f, ok := want.(*types.Fixed)
	if !ok || f.Float {
		return
	}
	v := lit.Value
	if neg {
		v = -v
	}
	if !fitsFixed(v, f) {
		c.errorf(lit.Span(), "literal %d overflows %s", v, want)
	}
}

// fitsFixed reports whether the value v is representable in the fixed-width
// integer f.
func fitsFixed(v int64, f *types.Fixed) bool {
	if f.Signed {
		if f.Bits >= 64 {
			return true
		}
		return v >= -(int64(1)<<(f.Bits-1)) && v <= int64(1)<<(f.Bits-1)-1
	}
	if v < 0 {
		return false
	}
	if f.Bits >= 64 {
		return true
	}
	return v <= int64(1)<<f.Bits-1
}

// isFloatWant reports whether a context type is a floating-point type (float, f32,
// or f64) — the contexts a fractional literal may adopt.
func isFloatWant(want Type) bool {
	if want.Kind() == types.KFloat {
		return true
	}
	f, ok := want.(*types.Fixed)
	return ok && f.Float
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

// isTypeParam reports whether t is an abstract type parameter, the operand that
// routes a binary operator through its bound spec (U3).
func isTypeParam(t Type) bool { return t.Kind() == types.KParam }

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
