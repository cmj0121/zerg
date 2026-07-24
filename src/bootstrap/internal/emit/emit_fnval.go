package emit

import (
	"fmt"
	"sort"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// First-class function values (docs/functions.md). A `fn(P...) -> R` value lowers to a
// C function pointer. A named top-level function used as a value is that function's
// designator (`zg_<name>`, which decays to a pointer); a call through a function value
// is an ordinary C call through the pointer. To give the type a spellable C name in
// every position (a parameter, a field, a local, a return), each distinct function
// type gets a `typedef R (*zg_fn_<n>)(P...);` — mirroring the tuple/Result carriers.
//
// This file is iteration 1: NAMED function values and calls through them. A closure
// literal used as a value is a later iteration (emit still gates it).

// fnCarrier is one generated function-pointer typedef: its C name and the function
// type it spells.
type fnCarrier struct {
	name string
	fn   *types.Fn
}

// prepareFnTypes numbers every distinct function TYPE the program uses as a value, so
// each gets a stable `zg_fn_<n>` typedef. It scans the same recorded type positions as
// prepareTuples. A program that names no function value registers nothing and stays
// byte-identical.
func (e *emitter) prepareFnTypes() {
	e.fntypes = map[string]*fnCarrier{}
	seen := map[string]*types.Fn{}
	var consider func(t sema.Type)
	consider = func(t sema.Type) {
		fn, ok := t.(*types.Fn)
		if !ok || fn == nil {
			return
		}
		// A function type still mentioning a type parameter is a generic template, never
		// a ground value type (generic function values are gated in sema); skip it so no
		// `void`-parameter typedef is emitted.
		if mentionsParam(fn) {
			return
		}
		if _, dup := seen[fn.String()]; dup {
			return
		}
		seen[fn.String()] = fn
		for _, p := range fn.Params {
			consider(p.Type) // a parameter may itself be a function type
		}
		consider(fn.Ret)
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
	reserved := e.reservedTopLevel()
	i := 0
	for _, k := range keys {
		name := e.freshCarrierName("zg_fn_%d", &i, reserved)
		e.fntypes[k] = &fnCarrier{name: name, fn: seen[k]}
	}
}

// emitFnTypedefs writes each function-pointer typedef, before the prototypes that may
// name one as a parameter/field/return type. A function type whose parameter or result
// is itself a function value is emitted after that inner typedef, so C sees the
// complete type first. Emits nothing when the program registered none.
func (e *emitter) emitFnTypedefs() {
	done := map[string]bool{}
	var emit func(c *fnCarrier)
	emit = func(c *fnCarrier) {
		if done[c.name] {
			return
		}
		done[c.name] = true
		for _, p := range c.fn.Params {
			if inner, ok := e.fnTypeFor(p.Type); ok {
				emit(inner)
			}
		}
		if inner, ok := e.fnTypeFor(c.fn.Ret); ok {
			emit(inner)
		}
		e.line(fmt.Sprintf("typedef %s (*%s)(%s);", e.fnRetType(c.fn.Ret), c.name, e.fnParamList(c.fn)))
	}
	for _, c := range e.orderedFnTypes() {
		emit(c)
	}
}

// fnParamList renders a function type's parameters as a C parameter-type list. A
// `mut &` parameter is a pointer, exactly as in a signature (emit_byref.go). An empty
// list spells `void`, C's no-parameter marker.
func (e *emitter) fnParamList(fn *types.Fn) string {
	if len(fn.Params) == 0 {
		return "void"
	}
	parts := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		parts[i] = e.paramType(p.Type)
		if p.ByRef {
			parts[i] += "*"
		}
	}
	out := parts[0]
	for _, s := range parts[1:] {
		out += ", " + s
	}
	return out
}

// fnRetType renders a function type's C return type; a nil (no `-> type`) result is
// `void`.
func (e *emitter) fnRetType(ret sema.Type) string {
	if ret == nil || ret == sema.Nil {
		return "void"
	}
	return e.ctype(ret)
}

// fnTypeFor returns the typedef registered for a function type, if any.
func (e *emitter) fnTypeFor(t sema.Type) (*fnCarrier, bool) {
	if t == nil || len(e.fntypes) == 0 {
		return nil, false
	}
	c, ok := e.fntypes[t.String()]
	return c, ok
}

// indirectCallEmit lowers a call through a function VALUE — a fn-typed local/param, a
// fn-typed struct field, or any expression of function type — to an ordinary C call
// through the pointer. It reports false for a direct call of a named top-level
// function (handled by the direct path) and for a call whose callee is not a function
// value, leaving those byte-identical.
func (e *emitter) indirectCallEmit(n *ast.Call) (string, bool) {
	// A bare name that IS a top-level function is a direct call, not a value call.
	if id, ok := n.Callee.(*ast.Ident); ok {
		if _, isFn := e.info.Funcs[id.Name]; isFn {
			return "", false
		}
	}
	if _, ok := e.cur.ExprType(e.info, n.Callee).(*types.Fn); !ok {
		return "", false
	}
	args := make([]string, len(n.Args))
	for i, a := range n.Args {
		args[i] = e.copyValue(e.cur.ExprType(e.info, a.Value), a.Value)
	}
	return fmt.Sprintf("%s(%s)", e.expr(n.Callee), joinArgs(args)), true
}

// joinArgs joins a comma-separated argument list.
func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += ", "
		}
		out += a
	}
	return out
}

// orderedFnTypes returns the function-type carriers in a deterministic order (by
// generated name), so the emitted typedefs are stable run to run.
func (e *emitter) orderedFnTypes() []*fnCarrier {
	out := make([]*fnCarrier, 0, len(e.fntypes))
	for _, c := range e.fntypes {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}
