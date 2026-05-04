// v0.6 Unit 7 — codegen for null-safety operators (?, ??, ?., nil) and
// per-instance helpers for monomorphised generics.
//
// The lowerings track PLAN.md §Null-safety semantics. Each operator emits a
// GCC statement-expression so the result is usable in any expression slot.
// `?` propagation uses an inline `return` inside the stmt-expr to bubble
// the Err / None up to the enclosing fn — GCC's stmt-expr extension permits
// `return`, and the surrounding fn's return type drives the construction
// of the early-return value.

package build

import (
	"fmt"
	"strings"

	"github.com/cmj/zerg/src/bootstrap/internal/syntax"
)

// nilLitStr lowers a NilLit expression. typeck has already resolved the
// expression type to the contextually-inferred Option[T]; the C value is
// `((<OptionType>){.tag = <None_idx>})`. The None variant is at index 1
// in the canonical Option enum (Some at 0, None at 1).
func (g *cgen) nilLitStr(e *syntax.NilLit) (string, error) {
	t := e.Type()
	if t == nil || t.Kind != syntax.TypeEnum || !strings.HasPrefix(t.Name, "Option[") {
		return "", fmt.Errorf("codegen: nil literal at %s has non-Option type %s", e.Pos, t)
	}
	idx := variantIndex(t, "None")
	if idx < 0 {
		return "", fmt.Errorf("codegen: Option type %s has no None variant", t.Name)
	}
	return fmt.Sprintf("((%s){.tag = %d})", g.mangleType(t), idx), nil
}

// propagateStr lowers `inner?`. The inner produces an Option[T] or
// Result[T, E]; on Some/Ok the value is unwrapped; on None/Err the
// enclosing fn early-returns with the propagated tag.
//
// The enclosing fn's return type is g.currentFnRet; typeck has validated
// that it is shape-compatible (Option[U] for an Option inner; Result[U, E]
// with the same E for a Result inner). For Option propagation the early-
// return is the None variant of the enclosing Option type; for Result it
// is the Err variant of the enclosing Result type, carrying the same E
// payload as the inner.
func (g *cgen) propagateStr(e *syntax.PropagateExpr) (string, error) {
	innerS, err := g.exprStr(e.Inner)
	if err != nil {
		return "", err
	}
	innerT := e.Inner.Type()
	if innerT == nil || innerT.Kind != syntax.TypeEnum {
		return "", fmt.Errorf("codegen: ? receiver at %s has non-enum type %s", e.Pos, innerT)
	}
	retT := g.currentFnRet
	if retT == nil || retT.Kind != syntax.TypeEnum {
		return "", fmt.Errorf("codegen: ? at %s requires an Option/Result-returning enclosing fn", e.Pos)
	}
	innerMname := g.mangleType(innerT)
	retMname := g.mangleType(retT)
	tmp := g.freshTmp("prop")
	// v0.7 Unit 5.5: when the enclosing fn carries HasDefers, drain the
	// defer stack before the early-return so deferred actions observe the
	// `?` propagation path identically to a normal return.
	drain := ""
	if g.currentHasDefers {
		drain = "zerg_defer_drain(__zerg_defer_marker); "
	}
	if strings.HasPrefix(innerT.Name, "Option[") {
		// Option propagation: tag 0 = Some, tag 1 = None.
		noneIdx := variantIndex(retT, "None")
		var sb strings.Builder
		fmt.Fprintf(&sb, "({ %s %s = %s; ", innerMname, tmp, innerS)
		fmt.Fprintf(&sb, "if (%s.tag != 0) { %sreturn ((%s){.tag = %d}); } ",
			tmp, drain, retMname, noneIdx)
		fmt.Fprintf(&sb, "%s.payload.p0.a0; })", tmp)
		return sb.String(), nil
	}
	// Result propagation: tag 0 = Ok, tag 1 = Err.
	errIdx := variantIndex(retT, "Err")
	var sb strings.Builder
	fmt.Fprintf(&sb, "({ %s %s = %s; ", innerMname, tmp, innerS)
	fmt.Fprintf(&sb,
		"if (%s.tag != 0) { %sreturn ((%s){.tag = %d, .payload.p%d = {.a0 = %s.payload.p1.a0}}); } ",
		tmp, drain, retMname, errIdx, errIdx, tmp)
	fmt.Fprintf(&sb, "%s.payload.p0.a0; })", tmp)
	return sb.String(), nil
}

// coalesceStr lowers `lhs ?? rhs`. The LHS is Option[T] or Result[T, E];
// when the tag is Some/Ok the value is the LHS payload, otherwise the RHS
// value. RHS is evaluated only when LHS misses.
func (g *cgen) coalesceStr(e *syntax.CoalesceExpr) (string, error) {
	lhsS, err := g.exprStr(e.Left)
	if err != nil {
		return "", err
	}
	rhsS, err := g.exprStr(e.Right)
	if err != nil {
		return "", err
	}
	lt := e.Left.Type()
	if lt == nil || lt.Kind != syntax.TypeEnum {
		return "", fmt.Errorf("codegen: ?? lhs at %s has non-enum type %s", e.Pos, lt)
	}
	tmp := g.freshTmp("coal")
	mname := g.mangleType(lt)
	// Both Option and Result have the "good" variant at index 0 (Some / Ok)
	// with one payload slot at .p0.a0; both have the "bad" variant at 1.
	return fmt.Sprintf("({ %s %s = %s; %s.tag == 0 ? %s.payload.p0.a0 : (%s); })",
		mname, tmp, lhsS, tmp, tmp, rhsS), nil
}

// safeFieldAccessStr lowers `obj?.field`. The receiver is an Option[T] for
// some struct T owning the field; when Some, wrap the field's value in a
// new Option of the field-type-Option canonical type; when None, propagate
// None of the same canonical type.
func (g *cgen) safeFieldAccessStr(e *syntax.FieldAccessExpr) (string, error) {
	rs, err := g.exprStr(e.Receiver)
	if err != nil {
		return "", err
	}
	rt := e.Receiver.Type()
	if rt == nil || rt.Kind != syntax.TypeEnum {
		return "", fmt.Errorf("codegen: ?. receiver at %s has non-Option type %s", e.Pos, rt)
	}
	// The receiver inner type is Option[T] — recover T from the Some payload.
	if len(rt.VariantPayloads) < 1 || len(rt.VariantPayloads[0]) != 1 {
		return "", fmt.Errorf("codegen: ?. receiver Option payload shape unexpected at %s", e.Pos)
	}
	innerT := rt.VariantPayloads[0][0]
	// e.Type() is Option[fieldT]; produce its mangled name.
	resT := e.Type()
	if resT == nil {
		return "", fmt.Errorf("codegen: ?. result type missing at %s", e.Pos)
	}
	resMname := g.mangleType(resT)
	rcvMname := g.mangleType(rt)
	tmp := g.freshTmp("safe")
	field := mangleField(e.FieldName)
	// Some: wrap the inner struct's field in Some of the result Option.
	// None: result Option's None tag = 1.
	someConstr := fmt.Sprintf("((%s){.tag = 0, .payload.p0 = {.a0 = %s.payload.p0.a0.%s}})",
		resMname, tmp, field)
	noneConstr := fmt.Sprintf("((%s){.tag = 1})", resMname)
	_ = innerT
	return fmt.Sprintf("({ %s %s = %s; %s.tag == 0 ? %s : %s; })",
		rcvMname, tmp, rs, tmp, someConstr, noneConstr), nil
}
