package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// First-class function values (docs/code/functions.md): a function is a value with a type
// `fn(P...) -> R`, and can be passed as an argument, returned, stored in a field, and
// bound to a variable. This file types a bare top-level function NAME used as a value.
// A closure literal (`fn(...) { }`) is a separate surface (checkClosure/synthFn); an
// anonymous closure captured by value is a later iteration.
//
// The type is the function's input/output contract and ONLY that: its parameters,
// each with its `mut &` calling-convention marker, and its result. Defaults, parameter
// names, and visibility do not ride in the type.

// funcValueType builds the `types.Fn` value type of a named top-level function, or
// reports a diagnostic and returns Invalid when the function cannot yet be a value.
func (c *checker) funcValueType(sig *FuncSig, span token.Span) Type {
	// A generic function is not one type until it is monomorphized, so it has no single
	// value type (which instantiation?). Reject it loudly rather than pick one.
	if sig.Generic != nil {
		c.errorf(span, "a generic function %q is not yet a first-class value; call it, or wrap it in a non-generic function", sig.Name)
		return Invalid
	}
	fn := &types.Fn{Ret: sig.Ret}
	for i, pt := range sig.Params {
		fn.Params = append(fn.Params, types.Param0{Type: pt, ByRef: paramByRef(sig.Decl, i)})
	}
	return fn
}

// paramByRef reports whether a function's i-th declared parameter is a `mut &`
// mutable reference — part of the function's type (docs/code/functions.md).
func paramByRef(fn *ast.FuncDecl, i int) bool {
	if fn == nil || i >= len(fn.Params) {
		return false
	}
	return fn.Params[i].Ref
}
