package emit

// This file carries the completeness-iteration-2 U2 additions to the C backend:
// making a tuple VALUE `(a, b, …)` real end to end. It mirrors the Result carrier
// (emit_result.go): every distinct tuple shape a program uses gets one monomorphized
// C struct `zg_tuple_<n>` with positional fields `.f0, .f1, …`, lowers construction
// (a struct literal, each element context-typed and copied), and lowers the static
// element read `t.N` to `.fN`.
//
// The surface is additive and gated on tuple use: a program that names no tuple
// value registers no carrier, so nothing here fires and its emitted C is
// byte-identical.
//
// Like the Result carrier (Decision A) the layout is INTERNAL and never FFI-frozen,
// so a later phase may retune the field naming and shape-keying freely.

import (
	"fmt"
	"sort"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// tupleCarrier is one monomorphized tuple C carrier: its generated struct type name
// and the concrete element types it wraps, in order.
type tupleCarrier struct {
	name  string
	elems []sema.Type
}

// prepareTuples numbers every distinct tuple shape the program uses, so each gets a
// stable `zg_tuple_<n>` typedef. It scans every recorded type position (function
// signatures, bindings, expression types), descending into composite types so a
// tuple nested inside another tuple (or an element of one) is registered too. It
// leaves the tuple map empty when no tuple value is used, keeping such a program
// byte-identical.
func (e *emitter) prepareTuples() {
	e.tuples = map[string]*tupleCarrier{}
	seen := map[string]*types.Tuple{}
	var consider func(t sema.Type)
	consider = func(t sema.Type) {
		tup, ok := t.(*types.Tuple)
		if !ok {
			return
		}
		if _, dup := seen[tup.String()]; dup {
			return
		}
		seen[tup.String()] = tup
		for _, el := range tup.Elems {
			consider(el) // a tuple element may itself be a tuple
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
	for _, t := range e.info.ExprTypes {
		consider(t)
	}

	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		e.tuples[k] = &tupleCarrier{name: fmt.Sprintf("zg_tuple_%d", i), elems: seen[k].Elems}
	}
}

// tupleFor returns the carrier registered for a tuple type, if any.
func (e *emitter) tupleFor(t sema.Type) (*tupleCarrier, bool) {
	if t == nil || e.tuples == nil {
		return nil, false
	}
	c, ok := e.tuples[t.String()]
	return c, ok
}

// emitTupleTypedefs writes every tuple carrier's struct typedef, before the function
// prototypes and Result carriers that may name one. A tuple whose element is itself a
// tuple is emitted after that inner tuple's typedef (C needs the complete type
// first), so the emit order follows element dependencies. Emits nothing when the
// program registered no tuple.
func (e *emitter) emitTupleTypedefs() {
	done := map[string]bool{}
	var emit func(c *tupleCarrier)
	emit = func(c *tupleCarrier) {
		if done[c.name] {
			return
		}
		done[c.name] = true
		for _, el := range c.elems {
			if inner, ok := e.tupleFor(el); ok {
				emit(inner) // an element tuple's typedef must precede this one
			}
		}
		var b string
		for i, el := range c.elems {
			b += fmt.Sprintf("%s f%d; ", e.ctype(el), i)
		}
		e.line(fmt.Sprintf("typedef struct { %s} %s;", b, c.name))
	}
	for _, c := range e.orderedTuples() {
		emit(c)
	}
	if len(e.tuples) > 0 {
		e.blank()
	}
}

// orderedTuples returns the tuple carriers sorted by generated name, so the emitted
// typedefs are deterministic run to run.
func (e *emitter) orderedTuples() []*tupleCarrier {
	out := make([]*tupleCarrier, 0, len(e.tuples))
	for _, c := range e.tuples {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// tupleLit lowers a tuple literal `(a, b, …)` to its carrier's struct literal, each
// element context-typed to (and copied into) its field, so an element that is an
// optional/Result adopts its carrier shape and a non-POD element retains correctly.
func (e *emitter) tupleLit(n *ast.TupleLit) string {
	tt := e.cur.ExprType(e.info, n)
	c, ok := e.tupleFor(tt)
	if !ok {
		return "0" // an un-modelled tuple shape: leave a scalar rather than bad C
	}
	var b string
	for i, el := range n.Elems {
		if i > 0 {
			b += ", "
		}
		elemT := c.elems[i]
		b += fmt.Sprintf(".f%d = %s", i, e.wrapValue(elemT, e.cur.ExprType(e.info, el), e.copyValue(elemT, el)))
	}
	return fmt.Sprintf("(%s){ %s }", c.name, b)
}

// tupleIndex lowers a static tuple element read `t.N` to the carrier field `.fN`.
func (e *emitter) tupleIndex(n *ast.TupleIndex) string {
	return fmt.Sprintf("%s.f%d", e.expr(n.X), n.Index)
}
