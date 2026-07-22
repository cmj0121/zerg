package emit

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// Raw-pointer operation lowering (GRAMMAR group 12, Phase 1h U1). Each ptr op is a
// compiler-recognized intrinsic (sema gates it to `unsafe`), lowered here to pure C
// — an address-of, a `*(T*)` deref, pointer arithmetic, or a cast — so the runtime
// gains no new code. A program that names no ptr op never enters ptrCallEmit (every
// branch is guarded by a ptr static type or a recognized intrinsic spelling), so the
// existing examples stay byte-identical.

// ptrCallEmit lowers a raw-pointer intrinsic call, returning ("", false) for any
// other call. It covers the free-function/construction intrinsics `addr(x)`,
// `ptr(p)`, `ptr[T](p)`, and `uint(p)`, and the pointer methods `p.load()`,
// `p.store(v)`, and `p.offset(n)`.
func (e *emitter) ptrCallEmit(n *ast.Call) (string, bool) {
	switch callee := n.Callee.(type) {
	case *ast.Field:
		return e.ptrMethodEmit(n, callee)
	case *ast.Ident:
		return e.ptrIntrinsicEmit(n, callee)
	case *ast.Bracket:
		return e.ptrCastBracketEmit(n, callee)
	}
	return "", false
}

// ptrMethodEmit lowers `p.load()` / `p.store(v)` / `p.offset(n)` when the receiver's
// static type is a raw pointer. load derefs (`*(p)`), store assigns through the
// deref (`*(p) = v`), and offset is C pointer arithmetic (`p + n`, whose stride is
// the pointee `sizeof` for a `ptr[T]`).
func (e *emitter) ptrMethodEmit(n *ast.Call, fld *ast.Field) (string, bool) {
	if _, ok := e.cur.ExprType(e.info, fld.X).(*types.Ptr); !ok {
		return "", false
	}
	recv := e.expr(fld.X)
	switch fld.Name {
	case "load":
		return "(*(" + recv + "))", true
	case "store":
		if len(n.Args) != 1 {
			return "", false
		}
		return "((*(" + recv + ")) = (" + e.expr(n.Args[0].Value) + "))", true
	case "offset":
		if len(n.Args) != 1 {
			return "", false
		}
		return "((" + recv + ") + (" + e.expr(n.Args[0].Value) + "))", true
	}
	return "", false
}

// ptrIntrinsicEmit lowers the Ident-callee ptr intrinsics `addr(x)`, `ptr(p)`, and
// `uint(p)`. Each is recognized only when its result (or, for `uint`, its argument)
// has a raw-pointer static type and the spelling is not shadowed by a user binding,
// so an ordinary call is never intercepted.
func (e *emitter) ptrIntrinsicEmit(n *ast.Call, id *ast.Ident) (string, bool) {
	if _, shadowed := e.info.Refs[id]; shadowed {
		return "", false
	}
	if len(n.Args) != 1 {
		return "", false
	}
	arg := n.Args[0].Value
	switch id.Name {
	case "addr":
		if !e.isPtrType(e.cur.ExprType(e.info, n)) {
			return "", false
		}
		return "(&(" + e.expr(arg) + "))", true
	case "ptr":
		if !e.isPtrType(e.cur.ExprType(e.info, n)) {
			return "", false
		}
		// a ptr-to-`ptr` cast is a plain reinterpret; a uint/int-to-`ptr` cast routes
		// through uintptr_t so the integer's bits become an address.
		if e.isPtrType(e.cur.ExprType(e.info, arg)) {
			return "((void*)(" + e.expr(arg) + "))", true
		}
		return "((void*)(uintptr_t)(" + e.expr(arg) + "))", true
	case "uint":
		if !e.isPtrType(e.cur.ExprType(e.info, arg)) {
			return "", false
		}
		return "((uint64_t)(uintptr_t)(" + e.expr(arg) + "))", true
	}
	return "", false
}

// ptrCastBracketEmit lowers the typed-pointer cast `ptr[T](p)` to a C cast to `T*`.
func (e *emitter) ptrCastBracketEmit(n *ast.Call, br *ast.Bracket) (string, bool) {
	id, ok := br.Base.(*ast.Ident)
	if !ok || id.Name != "ptr" {
		return "", false
	}
	if _, shadowed := e.info.Refs[id]; shadowed {
		return "", false
	}
	pt, ok := e.cur.ExprType(e.info, n).(*types.Ptr)
	if !ok || len(n.Args) != 1 {
		return "", false
	}
	return "((" + e.ctype(pt) + ")(" + e.expr(n.Args[0].Value) + "))", true
}

// isPtrType reports whether a static type is a raw pointer.
func (e *emitter) isPtrType(t types.Type) bool {
	_, ok := t.(*types.Ptr)
	return ok
}
