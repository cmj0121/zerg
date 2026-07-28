package sema

import (
	"strconv"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// Closure literals (docs/code/functions.md). A closure `fn(...) { }` is a first-class value
// whose captures are its free variables. "A closure is a scope-owned struct whose
// fields are its captures"; capture is by copy, of immutable values and channels only.
//
// A closure is lifted to an ordinary top-level function, reusing the whole function
// machinery. A NON-CAPTURING closure needs no environment. A CAPTURING closure records
// its captures — POD or NON-POD — so the backend builds the environment its lifted
// function reads: a POD capture copies by bits, a non-POD capture
// (Ref/list/map/str/boxed) is retained into a refcounted environment box and released
// when the closure value is dropped. One capture stays illegal: a `mut` variable (the
// grammar captures immutable captures, so snapshot it into an immutable binding first).

// captureFrame records, while a closure body is checked, the scope depth at the
// closure's entry and the enclosing locals its body referenced.
type captureFrame struct {
	boundary int // len(c.scopes) at closure entry: a lookup below this is a capture
	captured map[string]*symbol
}

// enterClosure opens a capture frame at the current scope depth. The caller pushes the
// closure's parameter scope AFTER this, so a parameter reference (at or above the
// boundary) is not mistaken for a capture.
func (c *checker) enterClosure() {
	c.captureStack = append(c.captureStack, &captureFrame{
		boundary: len(c.scopes),
		captured: map[string]*symbol{},
	})
}

// leaveClosure pops the innermost capture frame and returns the enclosing locals the
// closure captured.
func (c *checker) leaveClosure() map[string]*symbol {
	top := c.captureStack[len(c.captureStack)-1]
	c.captureStack = c.captureStack[:len(c.captureStack)-1]
	return top.captured
}

// resolveClosure decides what a checked closure becomes: a lifted top-level function
// when it captures nothing, or a diagnostic when it captures. It is called by
// checkClosure/synthFn once the body has been checked and the closure's function type
// (fn) is known.
func (c *checker) resolveClosure(fe *ast.FnExpr, fn *types.Fn, captured map[string]*symbol) {
	caps := make([]Capture, 0, len(captured))
	for name, sym := range captured {
		// A `mut` capture is never legal: the grammar captures IMMUTABLE captures, and the
		// value cannot change through the capture, so the intent (mutate shared state) is
		// unmet — snapshot it into an immutable binding first (docs/code/functions.md).
		if sym.mutable {
			c.errorf(fe.Span(), "cannot capture the mutable variable %q in a closure; snapshot it into an immutable binding first", name)
			return
		}
		// Capture is by copy — of an IMMUTABLE value, POD or not. A POD value copies by
		// bits; a non-POD value (a Ref/list/map/str/boxed recursive value) is RETAINED into
		// the closure's refcounted environment box (copyValue at env construction, like a
		// struct field) and RELEASED when the closure value is dropped, so its lifetime is
		// correctly extended past the defining scope. This is now expressible because
		// refcount exists (S1/S2); the old non-POD refusal is gone.
		caps = append(caps, Capture{Name: name, Type: sym.typ})
	}
	// A deterministic order (by name) so the generated environment struct is stable.
	sortCaptures(caps)
	c.liftClosure(fe, fn, caps)
}

// liftClosure synthesizes a top-level function from a closure: a FuncDecl whose body is
// the closure's, and a matching FuncSig registered under a fresh name, so mono enqueues
// it and emit lowers it like any function. A capturing closure records its captures so
// the backend can build the environment the lifted function reads.
func (c *checker) liftClosure(fe *ast.FnExpr, fn *types.Fn, caps []Capture) {
	name := c.freshLambdaName()

	params := make([]ast.Param, len(fe.Params))
	names := make([]string, len(fe.Params))
	for i := range fe.Params {
		params[i] = ast.Param{Name: fe.Params[i].Name, Type: fe.Params[i].Type, Ref: fe.Params[i].Ref}
		names[i] = fe.Params[i].Name
	}
	decl := &ast.FuncDecl{Name: name, Params: params, Ret: fe.Ret, Body: fe.Body}

	ptypes := make([]Type, len(fn.Params))
	for i, p := range fn.Params {
		ptypes[i] = p.Type
	}
	c.info.Funcs[name] = &FuncSig{
		Name:       name,
		ParamNames: names,
		Params:     ptypes,
		Ret:        retOrNil(fn.Ret),
		Decl:       decl,
	}
	c.info.Lambdas[fe] = &Lambda{Name: name, Decl: decl, Captures: caps}
}

// sortCaptures orders captures by name, so a closure's environment struct and its field
// order are deterministic run to run.
func sortCaptures(caps []Capture) {
	for i := 1; i < len(caps); i++ {
		for j := i; j > 0 && caps[j-1].Name > caps[j].Name; j-- {
			caps[j-1], caps[j] = caps[j], caps[j-1]
		}
	}
}

// freshLambdaName returns a synthesized name for a lifted closure that no user
// function already uses, so the generated C symbol never collides.
func (c *checker) freshLambdaName() string {
	for {
		name := "lambda_" + strconv.Itoa(c.lambdaSeq)
		c.lambdaSeq++
		if _, taken := c.info.Funcs[name]; !taken {
			return name
		}
	}
}

// retOrNil normalizes a function-type result for a synthesized signature: a closure
// with no `-> type` has a Go-nil Ret, which a declaration's signature spells as the
// `nil` primitive.
func retOrNil(ret Type) Type {
	if ret == nil {
		return Nil
	}
	return ret
}
