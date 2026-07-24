package sema

import (
	"strconv"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// Closure literals (docs/functions.md). A closure `fn(...) { }` is a first-class value
// whose captures are its free variables. "A closure is a scope-owned struct whose
// fields are its captures"; capture is by copy, of immutable values and channels only.
//
// This is iteration 2: a NON-CAPTURING closure — one whose body references no enclosing
// local — is lifted to an ordinary top-level function, so it needs no environment and
// reuses the whole function machinery. A closure that DOES capture is gated pending the
// captured-environment representation (iteration 3), except a `mut` capture, which is
// never legal (the value cannot be mutated through the capture, so it must be
// snapshotted first).

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
	if len(captured) > 0 {
		// A `mut` capture is never legal; an immutable capture is legal but its
		// environment representation is a later iteration.
		for name, sym := range captured {
			if sym.mutable {
				c.errorf(fe.Span(), "cannot capture the mutable variable %q in a closure; snapshot it into an immutable binding first", name)
				return
			}
		}
		c.errorf(fe.Span(), "a closure that captures a variable is not yet supported; refer only to parameters and top-level names, or pass state as a parameter")
		return
	}
	c.liftClosure(fe, fn)
}

// liftClosure synthesizes a top-level function from a non-capturing closure: a
// FuncDecl whose body is the closure's, and a matching FuncSig registered under a fresh
// name, so mono enqueues it and emit lowers it like any function. The value of the
// closure expression is then that function (emit spells its name).
func (c *checker) liftClosure(fe *ast.FnExpr, fn *types.Fn) {
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
	c.info.Lambdas[fe] = &Lambda{Name: name, Decl: decl}
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
