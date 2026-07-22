package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/token"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// Raw-pointer operations (GRAMMAR group 12, Phase 1h U1). A `ptr`/`ptr[T]` type is
// safe to name in a signature or field — it is only a description of a pointer
// shape — but every OPERATION on one (taking an address, loading, storing,
// offsetting, or casting) is UNSAFE and legal only inside an `unsafe { }` context.
// The operations are compiler-recognized intrinsics, modelled exactly like the 1f
// `__zrt_*` leaves (addr/casts through builtinCall; load/store/offset through the
// inferCall Field-callee branch), so the runtime gains no new C.

// unsafeOp reports a raw operation used outside an unsafe context, phrasing the
// diagnostic like docs/ffi.md's foreign-call rule. `what` names the operation, e.g.
// "taking a raw address"; a nil-op inside `unsafe { }` is silent.
func (c *checker) unsafeOp(span token.Span, what string) {
	if !c.inUnsafe {
		c.errorf(span, "%s is unsafe; do it inside an `unsafe { }` block", what)
	}
}

// ptrAddr checks `addr(x) -> ptr[T]`: it takes the address of an addressable value
// x (a variable, field, or element) and yields a typed pointer to x's type T. The
// operation is unsafe.
func (c *checker) ptrAddr(n *ast.Call) Type {
	c.unsafeOp(n.Span(), "taking a raw address")
	if len(n.Args) != 1 {
		c.errorf(n.Span(), "addr(x) takes exactly one argument, got %d", len(n.Args))
		c.synthArgs(n)
		return Invalid
	}
	arg := n.Args[0].Value
	t := c.synth(arg)
	if !isAddressable(arg) {
		c.errorf(arg.Span(), "addr expects an addressable value (a variable, field, or element)")
		return Invalid
	}
	if bad(t) {
		return Invalid
	}
	return &types.Ptr{Elem: t}
}

// ptrCast checks a raw-pointer cast — `ptr(p)`, `ptr[T](p)`, or `uint(p)` — yielding
// the target type. The one argument is synthesized (any ptr or integer round-trips)
// and the cast is unsafe.
func (c *checker) ptrCast(n *ast.Call, target Type) Type {
	c.unsafeOp(n.Span(), "a pointer cast")
	if len(n.Args) != 1 {
		c.errorf(n.Span(), "a pointer cast takes exactly one argument, got %d", len(n.Args))
		c.synthArgs(n)
		return Invalid
	}
	c.synth(n.Args[0].Value)
	return target
}

// ptrMethodCall checks the raw-pointer methods `p.load()`, `p.store(v)`, and
// `p.offset(n)` when the receiver's static type is a `*types.Ptr` (DISPATCH). It
// returns (type, true) once it takes responsibility for a ptr receiver, so the
// caller does not fall through to the struct-method or namespace paths; a receiver
// of any other type yields (nil, false). Every method is unsafe.
func (c *checker) ptrMethodCall(n *ast.Call, fld *ast.Field, pt *types.Ptr) (Type, bool) {
	switch fld.Name {
	case "load":
		c.unsafeOp(n.Span(), "dereferencing a raw pointer")
		if len(n.Args) != 0 {
			c.errorf(n.Span(), "load() takes no arguments, got %d", len(n.Args))
			c.synthArgs(n)
		}
		if pt.Elem == nil {
			c.errorf(n.Span(), "cannot load through a bare `ptr`; cast to `ptr[T]` first")
			return Invalid, true
		}
		return pt.Elem, true
	case "store":
		c.unsafeOp(n.Span(), "storing through a raw pointer")
		if pt.Elem == nil {
			c.errorf(n.Span(), "cannot store through a bare `ptr`; cast to `ptr[T]` first")
			c.synthArgs(n)
			return Nil, true
		}
		if len(n.Args) != 1 {
			c.errorf(n.Span(), "store(v) takes exactly one argument, got %d", len(n.Args))
			c.synthArgs(n)
			return Nil, true
		}
		c.check(n.Args[0].Value, pt.Elem)
		return Nil, true
	case "offset":
		c.unsafeOp(n.Span(), "offsetting a raw pointer")
		if len(n.Args) != 1 {
			c.errorf(n.Span(), "offset(n) takes exactly one argument, got %d", len(n.Args))
			c.synthArgs(n)
		} else {
			c.check(n.Args[0].Value, Int)
		}
		return pt, true
	}
	c.errorf(n.Span(), "a raw pointer has no method %q", fld.Name)
	c.synthArgs(n)
	return Invalid, true
}

// isAddressable reports whether an expression names storage whose address can be
// taken — a variable, a struct field, or a container element — as opposed to a
// temporary value.
func isAddressable(e ast.Expr) bool {
	switch e.(type) {
	case *ast.Ident, *ast.Field, *ast.Bracket, *ast.TupleIndex:
		return true
	}
	return false
}
