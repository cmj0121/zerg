package run

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// errPropagate carries an early-return value triggered by `?` propagation. The
// enclosing fn's call frame catches it and converts to an errReturn whose
// value's type matches the enclosing fn's declared return type.
type errPropagate struct {
	value Value
}

func (e *errPropagate) Error() string { return "propagate" }

func isOptionType(t *syntax.Type) bool {
	if t == nil || t.Kind != syntax.TypeEnum {
		return false
	}
	return strings.HasPrefix(t.Name, "Option[")
}

func isResultType(t *syntax.Type) bool {
	if t == nil || t.Kind != syntax.TypeEnum {
		return false
	}
	return strings.HasPrefix(t.Name, "Result[")
}

// displayEnumName strips the `[type-args]` suffix from a monomorphized enum
// name for the print path. PLAN.md §Print parity pins that
// `print Option[int].Some(7)` emits `Option.Some(7)` — the bracketed instance
// name is suppressed. The same rule applies uniformly to user-defined generic
// enums and structs so `Box[int] { value: 7 }` prints as `Box { value: 7 }`.
//
// Diagnostics elsewhere keep the full bracketed name; only formatValue and
// any other stdout-bound formatter routes through this helper.
func displayEnumName(name string) string {
	if i := strings.IndexByte(name, '['); i >= 0 {
		return name[:i]
	}
	return name
}

// evalNilLit constructs an Option[T].None value of the contextually-inferred
// Option type. Typeck has stamped NilLit.Type() with the Option[T] resolved at
// the surrounding hint (binding / return / fn-arg / list-elem / struct-field).
func (in *interp) evalNilLit(e *syntax.NilLit) (Value, error) {
	t := e.Type()
	if !isOptionType(t) {
		return Value{}, fmt.Errorf("internal: nil at %s lacks Option[T] type stamp (got %s)", e.Pos, t)
	}
	idx := -1
	for i, v := range t.Variants {
		if v == "None" {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Value{}, fmt.Errorf("internal: %s has no None variant at %s", t, e.Pos)
	}
	return enumVal(t, idx, "None", nil), nil
}

// evalPropagate implements `inner?` per PLAN.md §Null-safety semantics.
// Evaluate inner; if Some/Ok, the expression's value is the wrapped payload.
// If None/Err, raise errPropagate carrying the value to early-return from the
// enclosing fn — callFn / callMethodFn convert it to an errReturn whose Type
// is the outer fn's declared return.
func (in *interp) evalPropagate(e *syntax.PropagateExpr) (Value, error) {
	inner, err := in.evalExpr(e.Inner)
	if err != nil {
		return Value{}, err
	}
	if inner.Type == nil || inner.Type.Kind != syntax.TypeEnum {
		return Value{}, fmt.Errorf("internal: ? receiver is not an enum at %s", e.Pos)
	}
	switch inner.VariantName {
	case "Some", "Ok":
		if len(inner.Payload) != 1 {
			return Value{}, fmt.Errorf("internal: %s.%s payload arity %d at %s",
				inner.Type.Name, inner.VariantName, len(inner.Payload), e.Pos)
		}
		return inner.Payload[0], nil
	case "None", "Err":
		return Value{}, &errPropagate{value: inner}
	}
	return Value{}, fmt.Errorf("internal: ? receiver variant %q at %s", inner.VariantName, e.Pos)
}

// evalCoalesce implements `lhs ?? rhs` per PLAN.md. Evaluate LHS once; on
// Some/Ok yield the inner; on None/Err evaluate RHS. RHS is NOT evaluated
// when LHS is Some/Ok — the operator is the user's explicit short-circuit.
func (in *interp) evalCoalesce(e *syntax.CoalesceExpr) (Value, error) {
	lv, err := in.evalExpr(e.Left)
	if err != nil {
		return Value{}, err
	}
	if lv.Type == nil || lv.Type.Kind != syntax.TypeEnum {
		return Value{}, fmt.Errorf("internal: ?? lhs is not an enum at %s", e.Pos)
	}
	switch lv.VariantName {
	case "Some", "Ok":
		if len(lv.Payload) != 1 {
			return Value{}, fmt.Errorf("internal: %s.%s payload arity %d at %s",
				lv.Type.Name, lv.VariantName, len(lv.Payload), e.Pos)
		}
		return lv.Payload[0], nil
	case "None", "Err":
		return in.evalExpr(e.Right)
	}
	return Value{}, fmt.Errorf("internal: ?? lhs variant %q at %s", lv.VariantName, e.Pos)
}

// evalSafeFieldAccess implements `obj?.field` per PLAN.md §Null-safety
// semantics. The receiver must be Option[T]. On Some(inner), produce
// Option[U].Some(inner.field); on None, produce Option[U].None — both Option
// instances carry the canonical *Type stamped by typeck on the FieldAccessExpr
// so the value is structurally identical to a nil literal at the same site.
//
// Chains (`a?.b?.c`) compose because each ?. yields Option[U] which the next
// ?. consumes as its receiver.
func (in *interp) evalSafeFieldAccess(e *syntax.FieldAccessExpr) (Value, error) {
	rv, err := in.evalExpr(e.Receiver)
	if err != nil {
		return Value{}, err
	}
	if !isOptionType(rv.Type) {
		return Value{}, fmt.Errorf("internal: ?. receiver is not Option at %s (got %s)", e.Pos, rv.Type)
	}
	resultT := e.Type()
	if !isOptionType(resultT) {
		return Value{}, fmt.Errorf("internal: ?. expression lacks Option[T] type stamp at %s", e.Pos)
	}
	if rv.VariantName == "None" {
		idx := variantIndex(resultT, "None")
		if idx < 0 {
			return Value{}, fmt.Errorf("internal: %s has no None variant at %s", resultT, e.Pos)
		}
		return enumVal(resultT, idx, "None", nil), nil
	}
	if rv.VariantName != "Some" || len(rv.Payload) != 1 {
		return Value{}, fmt.Errorf("internal: ?. receiver shape %s.%s at %s",
			rv.Type.Name, rv.VariantName, e.Pos)
	}
	inner := rv.Payload[0]
	if inner.Type == nil || inner.Type.Kind != syntax.TypeStruct {
		return Value{}, fmt.Errorf("internal: ?. inner is not a struct at %s (got %s)", e.Pos, inner.Type)
	}
	field, ok := lookupField(inner, e.FieldName)
	if !ok {
		return Value{}, fmt.Errorf("internal: struct %q has no field %q at %s",
			inner.Type.Name, e.FieldName, e.NamePos)
	}
	idx := variantIndex(resultT, "Some")
	if idx < 0 {
		return Value{}, fmt.Errorf("internal: %s has no Some variant at %s", resultT, e.Pos)
	}
	return enumVal(resultT, idx, "Some", []Value{field}), nil
}

// variantIndex returns the index of variantName in t.Variants, or -1.
func variantIndex(t *syntax.Type, variantName string) int {
	for i, v := range t.Variants {
		if v == variantName {
			return i
		}
	}
	return -1
}

// lookupField returns the value of the named field on a struct Value.
func lookupField(v Value, name string) (Value, bool) {
	if v.Type == nil || v.Type.Kind != syntax.TypeStruct {
		return Value{}, false
	}
	for i, f := range v.Type.Fields {
		if f.Name == name {
			return v.Fields[i], true
		}
	}
	return Value{}, false
}

// adaptPropagated converts a propagated inner value (the None / Err that
// triggered `?` propagation) to a value of the outer fn's declared return
// type. The two share the same variant tag and payload values; only the *Type
// pointer needs swapping so the runtime walks the right (possibly different
// inner-T) Option / Result instance.
//
// Typeck has guaranteed shape compatibility: Option-in-Option (any U), or
// Result-in-Result with exact-match E. Mismatch reaching here would be an
// internal bug.
func adaptPropagated(propagated Value, outerRet *syntax.Type) (Value, error) {
	if outerRet == nil || outerRet.Kind != syntax.TypeEnum {
		return Value{}, fmt.Errorf("internal: propagate target is not an enum (%s)", outerRet)
	}
	idx := variantIndex(outerRet, propagated.VariantName)
	if idx < 0 {
		return Value{}, fmt.Errorf("internal: %s has no variant %q", outerRet, propagated.VariantName)
	}
	return enumVal(outerRet, idx, propagated.VariantName, propagated.Payload), nil
}

// catchPropagate converts an errPropagate at fn-call boundary to an errReturn
// whose Value has its Type re-pointed at the outer fn's return Type. Returns
// nil when err is not an errPropagate so callers chain naturally.
func catchPropagate(err error, outerRet *syntax.Type) (*errReturn, error) {
	var p *errPropagate
	if !errors.As(err, &p) {
		return nil, nil
	}
	v, aerr := adaptPropagated(p.value, outerRet)
	if aerr != nil {
		return nil, aerr
	}
	return &errReturn{value: v}, nil
}
