package emit

// This file carries the range VALUE + membership backend (GRAMMAR group 4). A range
// `lo..hi` / `lo..=hi` is normally pure sugar (for-in lowers it inline, and a match
// range arm is a containment test), so nothing here fires for those. It becomes real
// only when a program tests membership `v in r` or binds a range to a name:
//
//   - `v in lo..hi`  → an inline bounds test `((v) >= lo && (v) < hi)` (`<= hi` for
//     `..=`, and just `(v) >= lo` for the open `lo..`). No carrier is needed — but the
//     subject is bound to a temp first, because the test names it at every bound and an
//     expression spliced in twice is evaluated twice.
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
//
// THE SUBJECT IS EVALUATED ONCE. Both shapes name `v` at more than one bound, and `v` is
// an expression rather than a name — so splicing its lowering in at each bound is not a
// longer way of asking the same question, it is running the program's code again:
// `f() in 1..10` called `f()` twice, and the second call happened AFTER the lower bound,
// so the order was wrong as well as the count. orderRepeated binds the subject and the
// bounds to temps in source order, which answers both at once (emit_order.go).
func (e *emitter) rangeMembership(n *ast.Binary) (string, bool) {
	if _, ok := e.cur.ExprType(e.info, n.R).(*types.Range); !ok {
		return "", false
	}
	if rng, ok := n.R.(*ast.Range); ok {
		// the run is the subject and the bounds it is compared against, in the order they
		// are written: `v` first, then `lo`, then `hi`. An absent `hi` is an open range and
		// is not part of the run at all.
		pre, undo := e.orderRepeated([]ast.Expr{n.L, rng.Lo, rng.Hi})
		defer undo()
		v, lo := e.expr(n.L), e.expr(rng.Lo)
		if rng.Hi == nil {
			return orderedForm(pre, fmt.Sprintf("((%s) >= (%s))", v, lo)), true
		}
		op := "<"
		if rng.Inclusive {
			op = "<="
		}
		hi := e.expr(rng.Hi)
		return orderedForm(pre, fmt.Sprintf("((%s) >= (%s) && (%s) %s (%s))", v, lo, v, op, hi)), true
	}
	// a range value: read its carrier bounds, honoring the runtime inclusive flag. The
	// carrier temp holds the RANGE, and is a copy of an already-evaluated value; it is the
	// subject beside it that the run has to bind, since the ternary names it on both arms.
	pre, undo := e.orderRepeated([]ast.Expr{n.L, n.R})
	defer undo()
	v, r := e.expr(n.L), e.expr(n.R)
	tmp := e.freshName("rng")
	return orderedForm(pre, fmt.Sprintf("({ zg_range %s = %s; (int64_t)(%s) >= %s.lo && "+
		"(%s.inclusive ? (int64_t)(%s) <= %s.hi : (int64_t)(%s) < %s.hi); })",
		tmp, r, v, tmp, tmp, v, tmp, v, tmp)), true
}
