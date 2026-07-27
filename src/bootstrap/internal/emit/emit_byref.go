package emit

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
)

// The backend half of the `mut &` mutable-reference parameter (GRAMMAR group 5).
//
// A `mut &x` parameter binds the CALLER's variable, so it lowers to a pointer: the
// callee takes `T*`, every mention of the name inside the body reads or writes
// through it as `(*p)`, and the call site passes the argument's address. That is the
// whole writeback — C's own by-reference idiom, with the language rules (a `mut`
// lvalue argument, no aliasing) already enforced by the front end.
//
// The callee does NOT own a by-ref parameter: docs/memory.md's lifetime table ends
// this call's borrow without freeing, since the caller keeps the variable. So a by-ref
// parameter is skipped when the function registers its parameter drops — releasing it
// would free storage the caller still holds.

// byRefParams reports, per parameter of the instance being emitted, whether it is a
// `mut &` mutable reference. A nil result means the instance declares none, which is
// every function that existed before this lowering — so their emitted C is unchanged.
func byRefParams(inst *mono.Instance) []bool {
	if inst == nil || inst.Origin == nil {
		return nil
	}
	var out []bool
	for i, p := range inst.Origin.Params {
		if !p.Ref {
			continue
		}
		if out == nil {
			out = make([]bool, len(inst.Origin.Params))
		}
		out[i] = true
	}
	return out
}

// isByRefParam reports whether parameter i of the instance is a `mut &` reference.
func isByRefParam(inst *mono.Instance, i int) bool {
	refs := byRefParams(inst)
	return refs != nil && i < len(refs) && refs[i]
}

// declParamType renders parameter i's C type in a signature or prototype. A `mut &`
// parameter binds the caller's variable, so it takes a pointer to the value type. The
// prototype and the definition share this so the two can never disagree.
func (e *emitter) declParamType(inst *mono.Instance, i int) string {
	t := e.paramType(inst.Params[i])
	if isByRefParam(inst, i) {
		return t + "*"
	}
	return t
}

// identIsByRef reports whether an identifier names a `mut &` parameter, whose storage
// is a pointer that every read and write must go through.
func (e *emitter) identIsByRef(n *ast.Ident) bool {
	sym, ok := e.info.Refs[n]
	return ok && sym != nil && sym.ByRef
}

// calleeByRefArgs reports, per argument position of a direct call, whether it binds a
// `mut &` parameter and so must be passed by address. It returns nil for an indirect
// call or a callee with no such parameter, leaving those calls byte-identical.
func (e *emitter) calleeByRefArgs(id *ast.Ident) []bool {
	if id == nil {
		return nil
	}
	sym, ok := e.info.Refs[id]
	if !ok || sym == nil {
		return nil
	}
	fd, ok := sym.Decl.(*ast.FuncDecl)
	if !ok {
		return nil
	}
	return byRefOf(fd)
}

// byRefOf reports, per declared parameter, whether it is `mut &`. It answers nil when
// none is, so a call with no by-ref parameter allocates nothing and stays byte-identical.
// A direct call, a namespace call and a method call all ask it of the same node — the
// declaration — which is what keeps one answer from being three.
func byRefOf(fd *ast.FuncDecl) []bool {
	var out []bool
	for i, p := range fd.Params {
		if !p.Ref {
			continue
		}
		if out == nil {
			out = make([]bool, len(fd.Params))
		}
		out[i] = true
	}
	return out
}

// addressOf renders the argument for a `mut &` parameter. The argument is a `mut`
// lvalue (the front end checked), so its emitted C is addressable. An argument that is
// ITSELF a by-ref parameter already denotes `(*p)`, whose address is just `p` — the
// pass-onward case, which C's `&*p` folds away, but spelling it directly keeps the
// emitted C readable.
func (e *emitter) addressOf(x ast.Expr) string {
	if id, ok := x.(*ast.Ident); ok && e.identIsByRef(id) {
		return e.resolve(id.Name)
	}
	return "&" + e.expr(x)
}
