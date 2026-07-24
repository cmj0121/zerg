package emit

// This file carries the range VALUE + membership backend (GRAMMAR group 4). A range
// `lo..hi` / `lo..=hi` is normally pure sugar (for-in lowers it inline, and a match
// range arm is a containment test), so nothing here fires for those. It becomes real
// only when a program tests membership `v in r` or binds a range to a name:
//
//   - `v in lo..hi`  → an inline bounds test `((v) >= lo && (v) < hi)` (`<= hi` for
//     `..=`, and just `(v) >= lo` for the open `lo..`). No carrier is needed.
//   - `r := lo..hi`  → the shared `zg_range` carrier `{ int64_t lo, hi; int inclusive; }`
//     and a membership on that value reads its fields.
//
// The surface is additive and gated on range-value use, so a program that never
// materializes a range value emits no typedef and stays byte-identical.

import (
	"fmt"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// prepareRange sets needsRange when the program materializes a range as a value — a
// binding, parameter, or return type that is a range. A pure membership test never
// stores a range value, so it does not set the flag (and emits no typedef).
func (e *emitter) prepareRange() {
	consider := func(t sema.Type) {
		if _, ok := t.(*types.Range); ok {
			e.needsRange = true
		}
	}
	for _, sig := range e.info.Funcs {
		consider(sig.Ret)
		for _, p := range sig.Params {
			consider(p)
		}
	}
	for _, t := range e.info.BindTypes {
		consider(t)
	}
}

// emitRangeTypedef writes the shared range carrier typedef, when the program
// materializes a range value. Emits nothing otherwise.
func (e *emitter) emitRangeTypedef() {
	if !e.needsRange {
		return
	}
	e.line("typedef struct { int64_t lo; int64_t hi; int inclusive; } zg_range;")
	e.blank()
}

// rangeValue lowers a range used AS A VALUE to the shared carrier literal. Only a
// bounded range becomes a value; an open range `lo..` has no upper bound to store,
// so it is gated cleanly (its only valid use is a for-in, which never reaches here).
func (e *emitter) rangeValue(n *ast.Range) string {
	if n.Hi == nil {
		e.diags.Add(n.Span(), "an open range (`lo..`) cannot be used as a value")
		return "0"
	}
	inclusive := 0
	if n.Inclusive {
		inclusive = 1
	}
	return fmt.Sprintf("(zg_range){ .lo = (int64_t)(%s), .hi = (int64_t)(%s), .inclusive = %d }",
		e.expr(n.Lo), e.expr(n.Hi), inclusive)
}

// rangeMembership lowers `v in r`. A range LITERAL on the right (`v in lo..hi`) is an
// inline bounds test with statically known inclusivity; a range VALUE reads its
// carrier fields (the inclusivity is then a runtime flag). Returns false when the
// right operand is not a range, so the caller falls through to the other `in` paths.
func (e *emitter) rangeMembership(n *ast.Binary) (string, bool) {
	if _, ok := e.cur.ExprType(e.info, n.R).(*types.Range); !ok {
		return "", false
	}
	v := e.expr(n.L)
	if rng, ok := n.R.(*ast.Range); ok {
		lo := e.expr(rng.Lo)
		if rng.Hi == nil {
			return fmt.Sprintf("((%s) >= (%s))", v, lo), true
		}
		op := "<"
		if rng.Inclusive {
			op = "<="
		}
		return fmt.Sprintf("((%s) >= (%s) && (%s) %s (%s))", v, lo, v, op, e.expr(rng.Hi)), true
	}
	// a range value: read its carrier bounds, honoring the runtime inclusive flag.
	r := e.expr(n.R)
	tmp := e.freshName("rng")
	return fmt.Sprintf("({ zg_range %s = %s; (int64_t)(%s) >= %s.lo && "+
		"(%s.inclusive ? (int64_t)(%s) <= %s.hi : (int64_t)(%s) < %s.hi); })",
		tmp, r, v, tmp, tmp, v, tmp, v, tmp), true
}
