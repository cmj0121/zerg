package syntax

import "strings"

// v0.6 Unit 4 — typeck for the null-safety operators (`?`, `??`, `?.`).
//
// PropagateExpr (`expr?`) requires its inner to be Option[T] or Result[T, E]
// and is only legal inside a fn whose return type is shape-compatible. The
// resulting type is the inner T.
//
// CoalesceExpr (`lhs ?? rhs`) requires LHS to be Option[T] or Result[T, E];
// the RHS must be assignable to T. The resulting type is T.
//
// FieldAccessExpr.Safe (`obj?.field`) requires the receiver to be Option[T]
// for some struct T owning the named field; the result is Option[U] where U
// is the field's declared type.

// isResultInstance reports whether t is a monomorphized Result[...] enum.
// Mirrors isOptionInstance for the Result branch of the null-safety operators.
func isResultInstance(t *Type) bool {
	if t == nil || t.Kind != TypeEnum {
		return false
	}
	return strings.HasPrefix(t.Name, "Result[")
}

// optionOrResultInner extracts the inner T from an Option[T] or Result[T, E]
// canonical *Type. Returns (inner, ok). The Option / Result discrimination is
// recoverable from t via isOptionInstance / isResultInstance; the inner T is
// always the first variant's first payload slot for both shapes.
func optionOrResultInner(t *Type) (*Type, bool) {
	if t == nil || t.Kind != TypeEnum {
		return nil, false
	}
	if !isOptionInstance(t) && !isResultInstance(t) {
		return nil, false
	}
	if len(t.VariantPayloads) < 1 || len(t.VariantPayloads[0]) != 1 {
		return nil, false
	}
	return t.VariantPayloads[0][0], true
}

// resultErrType extracts the E from a Result[T, E] canonical *Type. Returns
// (err, ok). The Err variant is index 1 with a single payload slot.
func resultErrType(t *Type) (*Type, bool) {
	if !isResultInstance(t) {
		return nil, false
	}
	if len(t.VariantPayloads) < 2 || len(t.VariantPayloads[1]) != 1 {
		return nil, false
	}
	return t.VariantPayloads[1][0], true
}

// checkPropagate type-checks `inner?`. The inner must be Option[T] or
// Result[T, E]. The enclosing fn's return type must be shape-compatible:
//
//   - Option[T] inner ⇒ enclosing fn must return Option[U] (any U)
//   - Result[T, E] inner ⇒ enclosing fn must return Result[U, E] (E exact)
//
// Out-of-context (no enclosing fn, or enclosing fn returns a non-Option /
// non-Result type) rejects with the canonical diagnostic.
func (c *checker) checkPropagate(e *PropagateExpr) (*Type, error) {
	innerT, err := c.checkExpr(e.Inner)
	if err != nil {
		return nil, err
	}
	if innerT == nil || innerT.Kind != TypeEnum ||
		(!isOptionInstance(innerT) && !isResultInstance(innerT)) {
		return nil, typeErr(e.Pos,
			"? requires Option[...] or Result[..., ...] receiver, got %s", innerT)
	}
	if c.currentFn == nil || c.currentFn.ret == nil {
		return nil, typeErr(e.Pos,
			"? propagation only legal inside fn returning Option[...] or Result[..., E]")
	}
	ret := c.currentFn.ret
	if isOptionInstance(innerT) {
		if !isOptionInstance(ret) {
			return nil, typeErr(e.Pos,
				"? propagation only legal inside fn returning Option[...] or Result[..., E]")
		}
	} else {
		if !isResultInstance(ret) {
			return nil, typeErr(e.Pos,
				"? propagation only legal inside fn returning Option[...] or Result[..., E]")
		}
		innerErr, _ := resultErrType(innerT)
		retErr, _ := resultErrType(ret)
		if !typeEq(innerErr, retErr) {
			return nil, typeErr(e.Pos,
				"? error type mismatch: %s vs %s", retErr, innerErr)
		}
	}
	out, ok := optionOrResultInner(innerT)
	if !ok {
		return nil, typeErr(e.Pos, "internal: cannot extract inner T from %s", innerT)
	}
	e.setType(out)
	return out, nil
}

// checkCoalesce type-checks `lhs ?? rhs`. LHS must be Option[T] or
// Result[T, E]; RHS must be assignable to T. Result type is T.
//
// Bidirectional hint: when the outer hint is some U (a non-Option type), we
// pass Option[U] (or Result[U, ?]) as the LHS hint? — at v0.6 the cleanest
// choice is to type-check LHS first with no hint and then drive the RHS hint
// from the LHS's inner T. This keeps the caller's hint flowing into RHS only.
func (c *checker) checkCoalesce(e *CoalesceExpr, hint *Type) (*Type, error) {
	leftT, err := c.checkExpr(e.Left)
	if err != nil {
		return nil, err
	}
	if leftT == nil || leftT.Kind != TypeEnum ||
		(!isOptionInstance(leftT) && !isResultInstance(leftT)) {
		return nil, typeErr(e.Pos,
			"?? requires Option[...] or Result[..., ...] on the left, got %s", leftT)
	}
	inner, ok := optionOrResultInner(leftT)
	if !ok {
		return nil, typeErr(e.Pos, "internal: cannot extract inner T from %s", leftT)
	}
	newRight, rightT, err := c.checkExprLift(e.Right, inner)
	if err != nil {
		return nil, err
	}
	if newRight != e.Right {
		e.Right = newRight
	}
	if !c.assignableTo(rightT, inner) {
		return nil, typeErr(e.Right.ExprPos(),
			"?? right-hand side has type %s, expected %s", rightT, inner)
	}
	_ = hint
	e.setType(inner)
	return inner, nil
}

// checkSafeFieldAccess type-checks `obj?.field`. The receiver must be
// Option[T] for some struct T owning the named field; the result is
// Option[U] where U is the field's declared type. Chains compose because
// each `?.` returns Option[...] which the next `?.` consumes.
func (c *checker) checkSafeFieldAccess(e *FieldAccessExpr) (*Type, error) {
	rt, err := c.checkExpr(e.Receiver)
	if err != nil {
		return nil, err
	}
	if rt == nil || !isOptionInstance(rt) {
		return nil, typeErr(e.Pos,
			"?. requires nullable receiver, got %s", rt)
	}
	inner, ok := optionOrResultInner(rt)
	if !ok {
		return nil, typeErr(e.Pos, "internal: cannot extract inner T from %s", rt)
	}
	if inner == nil || inner.Kind != TypeStruct {
		return nil, typeErr(e.Pos,
			"?. requires struct inside Option, got Option[%s]", inner)
	}
	for _, f := range inner.Fields {
		if f.Name == e.FieldName {
			out, err := c.wrapOption(f.Type, e.Pos)
			if err != nil {
				return nil, err
			}
			e.setType(out)
			return out, nil
		}
	}
	return nil, typeErr(e.NamePos,
		"field %q not found on %s", e.FieldName, inner.Name)
}
