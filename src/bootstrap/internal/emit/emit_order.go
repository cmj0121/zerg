package emit

// Left-to-right evaluation order (docs/core/memory.md: "Function arguments, operator
// operands, and the elements of a `list`/`map` literal or a `set(...)` constructor
// evaluate in source order, deterministically.").
//
// C does not promise it. `zrt_add_i64(zg_a(), zg_b())` is a call, and the order in which
// a call's arguments are evaluated is unspecified — so `print a() + b()` printed "a b"
// under clang/arm64 and "b a" under gcc/x86-64, from one source, with no diagnostic
// anywhere. The list/map element paths already sequenced their elements; every other
// combining form (a binary operator, a call's argument list, a struct construction) took
// whatever the C compiler chose.
//
// The fix is to stop handing several effectful operands to one C combining form. When a
// form needs ordering, each of its operands is materialized into its own temp inside a
// GNU statement-expression — `({ __auto_type t1 = …; __auto_type t2 = …; f(t1, t2); })`
// — which C evaluates as a statement sequence, in the order written. The temps are typed
// `__auto_type` rather than spelled out because the operand's C type is not always the
// operand expression's Zerg type: an argument is copied and coerced into its PARAMETER
// type afterwards, and a by-ref operand denotes storage rather than a value. Deducing the
// type from the initializer is exact where restating it would be a second source of truth.
//
// The trigger is deliberately narrow — see needsOrdering. Sequencing costs a temp and a
// statement-expression in the emitted C, and a form whose operands cannot observe each
// other gains nothing from it.
//
// This is the SEED's half of a change the self-hosting compiler makes identically; the two
// must agree about which operands get sequenced, or `make oracle` sees one program print
// two things.

import (
	"fmt"
	"strings"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
)

// orderTrivial reports whether an operand can be spliced straight into a C combining form
// with no temp: a literal or a plain identifier read (a local, a parameter, a module
// constant). Everything else — a call, an index, a field access, a receive, a nested
// binary, a match/if/block expression — may run code, and is therefore NOT trivial.
//
// Misjudging a PURE form as non-trivial only costs a redundant temp; it can never change
// what the program does, because sequencing a pure operand is unobservable. That is why
// this list is short rather than clever: everything it leaves out is either effectful (and
// must be sequenced) or pure (and loses nothing by being sequenced anyway).
func orderTrivial(x ast.Expr) bool {
	switch x.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.StrLit, *ast.RawStrLit:
		return true
	case *ast.Ident:
		return true
	}
	return false
}

// needsOrdering reports whether a combining form's operand list must be sequenced: any
// operand AFTER the first can run code.
//
// The first operand is exempt because nothing in the list precedes it — whatever C picks,
// it cannot observe an effect that has not happened yet. That is what keeps a
// single-argument call (`f(g())`) and a trailing-literal form (`f(g(), 1)`, `f(y) + 1`)
// byte-identical to what this emitter wrote before.
//
// What it does NOT keep identical is `x + f(y)`: the second operand is a call, so the pair
// is sequenced even though the first operand is only a name. That is deliberate rather
// than an over-approximation — a callee taking `mut &x` may WRITE the very variable the
// left operand reads, so `x + f(x)` has an observable order and no cheap test tells it
// apart from `x + f(y)`. The cost of being wrong in this direction is one redundant temp;
// the cost of being wrong in the other is a silently different answer.
//
// skip[i] marks an operand this form does not evaluate as a value (a `mut &` argument,
// whose "evaluation" is taking an address and so is pure); it is neither counted here nor
// materialized below.
func needsOrdering(xs []ast.Expr, skip []bool) bool {
	for i := 1; i < len(xs); i++ {
		if i < len(skip) && skip[i] {
			continue
		}
		if !orderTrivial(xs[i]) {
			return true
		}
	}
	return false
}

// orderOperands materializes every operand of a combining form into its own temp, in
// source order, and PINS each temp so the form's own rendering (which happens next, down
// its existing path) picks the temp up instead of re-rendering the operand. It answers the
// declarations to place at the head of the statement-expression and the undo to run once
// the form has been rendered.
//
// Pinning is what keeps this to one rule in one place. Each combining form already knows
// how to render its operands — a call copies and coerces each argument into its parameter
// type, an operator overload copies its right operand, a str concat releases an operand it
// owns — and every one of those decisions is made from the operand's AST node, not from
// its C text. Substituting the temp for the TEXT therefore changes when the operand is
// evaluated and nothing else: the copy, the coercion and the release still happen, to the
// temp, exactly as they happened to the operand before.
//
// When the list does not need ordering this renders nothing and pins nothing, so the form
// emits the C it always did.
func (e *emitter) orderOperands(xs []ast.Expr, skip []bool) (string, func()) {
	if !needsOrdering(xs, skip) {
		return "", func() {}
	}
	return e.materializeOperands(xs, skip)
}

// orderRepeated is orderOperands for a form that READS an operand more than once, rather
// than one that merely combines each of them once. `v in lo..hi` is the only such form:
// the bounds test names `v` at both bounds, so `f() in 1..10` called `f()` twice.
//
// The trigger has to be its own, because needsOrdering exempts the FIRST operand — nothing
// precedes it in an ordinary combining form, so whatever it does it does first. That reason
// does not hold here: the first operand is the one written twice, and its SECOND reading
// comes after every bound. So ANY operand that can run code makes the run observable, the
// subject included.
func (e *emitter) orderRepeated(xs []ast.Expr) (string, func()) {
	for _, x := range xs {
		if x != nil && !orderTrivial(x) {
			return e.materializeOperands(xs, nil)
		}
	}
	return "", func() {}
}

// materializeOperands is the work orderOperands and orderRepeated share, with the decision
// left to the caller: the two differ in WHEN a run is sequenced, never in how.
func (e *emitter) materializeOperands(xs []ast.Expr, skip []bool) (string, func()) {
	var b strings.Builder
	pinned := make([]ast.Expr, 0, len(xs))
	for i, x := range xs {
		if x == nil || (i < len(skip) && skip[i]) {
			continue
		}
		// rendered BEFORE the pin is recorded, so the temp's own initializer is the
		// operand's real lowering and not the (not yet declared) temp name.
		code := e.expr(x)
		t := e.freshName("ord")
		fmt.Fprintf(&b, "__auto_type %s = %s; ", t, code)
		e.pinned[x] = t
		pinned = append(pinned, x)
	}
	return b.String(), func() {
		for _, x := range pinned {
			delete(e.pinned, x)
		}
	}
}

// orderedForm wraps a rendered combining form in the statement-expression that holds its
// operands' temps. An empty pre — the form needed no ordering — returns the form unchanged,
// which is what keeps every already-ordered program byte-identical.
func orderedForm(pre, form string) string {
	if pre == "" {
		return form
	}
	return fmt.Sprintf("({ %s%s; })", pre, form)
}

// argExprs is the operand list of an argument list, optionally led by a receiver (a method
// call's `recv.m(a…)` evaluates the receiver first, so it is the list's first operand).
func argExprs(recv ast.Expr, args []ast.Arg) []ast.Expr {
	xs := make([]ast.Expr, 0, len(args)+1)
	if recv != nil {
		xs = append(xs, recv)
	}
	for _, a := range args {
		xs = append(xs, a.Value)
	}
	return xs
}

// argSkip lifts a call's per-ARGUMENT by-ref flags onto the operand list argExprs built,
// shifting by one because that list is led by the receiver.
func argSkip(byref []bool) []bool {
	if byref == nil {
		return nil
	}
	return append([]bool{false}, byref...)
}

// argByRefSkip marks, per SOURCE argument, whether the parameter that argument fills takes
// it by reference — which is what byref carries, per PARAMETER position. The two lists are
// the same list unless the call named its arguments; when it did, this is what carries a
// `mut &` flag back from the slot to the argument that fills it, so sequencing leaves that
// argument (an address, never a value) alone.
func (e *emitter) argByRefSkip(src, slots []ast.Arg, byref []bool) []bool {
	if byref == nil {
		return nil
	}
	skip := make([]bool, len(src))
	for i, a := range src {
		for j, s := range slots {
			if s.Value == a.Value && j < len(byref) {
				skip[i] = byref[j]
			}
		}
	}
	return skip
}
