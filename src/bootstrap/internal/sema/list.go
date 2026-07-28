package sema

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// Built-in list[T] methods (docs/code/collections.md, GRAMMAR group 4). A list is not a
// user struct, so its methods are compiler intrinsics dispatched on the receiver's
// static `*types.List` type (like the raw-pointer methods), typed here and lowered
// in emit_list.go. Three are modelled this iteration:
//   - `.len() -> int`        the element count
//   - `.get(i: int) -> T?`   the checked access (None on a bad index)
//   - `.append(v: T)`        grow by one; REQUIRES a `mut` receiver, like a struct
//     mutator, and yields nil
//
// The forcing access `xs[i]`, the index-assign `xs[i] = v`, and `for x in xs` are
// handled on their own paths (inferBracket / checkAssign / forInElem).
func (c *checker) listMethodCall(n *ast.Call, fld *ast.Field, lt *types.List) Type {
	switch fld.Name {
	case "len":
		if len(n.Args) != 0 {
			c.errorf(n.Span(), "len() takes no arguments, got %d", len(n.Args))
			c.synthArgs(n)
		}
		return Int
	case "get":
		if len(n.Args) != 1 {
			c.errorf(n.Span(), "get(i) takes exactly one argument, got %d", len(n.Args))
			c.synthArgs(n)
			return &types.Opt{Elem: lt.Elem}
		}
		// The index argument must be ASSIGNABLE to int, exactly like the `xs[i]` index
		// path (inferBracket). A bare `check()` only context-types a literal and never
		// reports a mismatch, so `xs.get(true)` would silently coerce bool -> int and
		// `xs.get("x")` would emit `zrt_list_get(_, "x")` (bad C to cc); checkElem
		// enforces assignability and reports a clean "list index" diagnostic.
		c.checkElem(n.Args[0].Value, Int, "list index")
		return &types.Opt{Elem: lt.Elem}
	case "append":
		if _, mutable, name, ok := c.lvalue(fld.X); ok && !mutable {
			c.errorf(n.Span(), "cannot append to immutable list %q; declare it `mut`", name)
		}
		if c.listIsFrozen(fld.X) {
			c.errorf(n.Span(), "cannot append to a list while iterating it; "+
				"the list is frozen against structural change inside its own `for` loop")
		}
		if len(n.Args) != 1 {
			c.errorf(n.Span(), "append(v) takes exactly one argument, got %d", len(n.Args))
			c.synthArgs(n)
			return Nil
		}
		at := c.check(n.Args[0].Value, lt.Elem)
		if !bad(at) && !bad(lt.Elem) && !c.assignable(lt.Elem, n.Args[0].Value, at) {
			c.errorf(n.Args[0].Value.Span(), "cannot append %s to a list[%s]", at, lt.Elem)
		}
		return Nil
	}
	c.errorf(n.Span(), "a list has no method %q", fld.Name)
	c.synthArgs(n)
	return Invalid
}

// listIsFrozen reports whether an expression names a list binding currently frozen by
// an enclosing `for x in xs` loop (see checker.frozenLists). Only a bare name can be
// frozen — the loop knows the exact binding it walks.
func (c *checker) listIsFrozen(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	sym := c.lookup(id.Name)
	return sym != nil && c.frozenLists[sym] > 0
}
