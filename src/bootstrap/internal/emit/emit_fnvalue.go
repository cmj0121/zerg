package emit

import (
	"strings"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// First-class function VALUES in the backend (docs/code/functions.md): a named top-level
// function bound to a name, stored in a field, passed as an argument, and called back
// through. The front end has always typed these — sema records each one in FuncValues /
// NsFuncValues — and only the lowering was missing, so using one was a clean diagnostic
// rather than a wrong program. This file is that lowering.
//
// A held function is stored as ONE C type, `zg_fnptr` — a generic function pointer — and
// cast back to its real signature at the call. C promises exactly that round trip between
// function pointer types and nothing more, which is why the storage type is a function
// pointer rather than `void*`: converting a function pointer to an object pointer is not
// something the standard guarantees.
//
// One storage type is also what keeps the C declarator problem away. A function pointer
// spells its name INSIDE the type — `int64_t (*f)(int64_t)` — while every type here is a
// string that gets a name appended to it. A typedef settles that for every function type
// at once, with no table of them to build and keep in order.
//
// Only NAMED functions. A closure literal captures its scope, and capture is a separate
// surface the seed still rejects.

// prepareFnValues sets needsFnPtr when the program spells a function type anywhere that
// is DECLARED — a parameter, a result, a binding, a struct field. It runs in the prepare
// pass because declarations are written out before any body is: a struct typedef naming
// `zg_fnptr` would otherwise reach the C compiler ahead of the typedef defining it.
//
// A program that holds no function leaves the flag false and stays byte-identical.
func (e *emitter) prepareFnValues() {
	consider := func(t sema.Type) {
		if _, ok := t.(*types.Fn); ok {
			e.needsFnPtr = true
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
	for _, ti := range e.prog.Types {
		for _, f := range ti.Fields {
			consider(f.Type)
		}
	}
}

// emitFnPtrTypedef writes the shared generic function pointer typedef, when the program
// holds a function. Emits nothing otherwise.
func (e *emitter) emitFnPtrTypedef() {
	if !e.needsFnPtr {
		return
	}
	e.line("typedef void (*zg_fnptr)(void);")
	e.blank()
}

// fnPtrCast is the cast from the generic stored pointer back to a real signature,
// `(R (*)(P, ...))`. A `mut &` parameter is a pointer in the C signature, exactly as it is
// where the function is defined — the calling convention is part of the function's type,
// so a cast that ignored it would call through the wrong one.
func (e *emitter) fnPtrCast(ft *types.Fn) string {
	var b strings.Builder
	b.WriteString("(")
	b.WriteString(e.ctype(ft.Ret))
	b.WriteString(" (*)(")
	if len(ft.Params) == 0 {
		b.WriteString("void")
	}
	for i, p := range ft.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(e.ctype(p.Type))
		if p.ByRef {
			b.WriteString("*")
		}
	}
	b.WriteString("))")
	return b.String()
}

// fnValue lowers a function NAME used as a value to the mangled symbol, stored as the
// generic pointer.
func (e *emitter) fnValue(name string) string {
	e.needsFnPtr = true
	return "((zg_fnptr)" + e.prog.CallTarget(name) + ")"
}

// fnValueCall lowers a call THROUGH a value that holds a function: cast the stored
// generic pointer back to the signature its type carries, then call through it.
//
// It must not capture an ordinary direct call. A bare name that resolves to a top-level
// function is typed `fn(P...) -> R` just as a binding holding one is, so the types alone
// cannot tell them apart; what separates them is that a direct call's callee resolves to
// the FUNCTION, while this one resolves to a binding or reads a field.
func (e *emitter) fnValueCall(n *ast.Call) (string, bool) {
	ft, ok := e.cur.ExprType(e.info, n.Callee).(*types.Fn)
	if !ok {
		return "", false
	}
	if id, isIdent := n.Callee.(*ast.Ident); isIdent {
		sym, found := e.info.Refs[id]
		if !found || (sym.Kind != sema.SymVar && sym.Kind != sema.SymConst) {
			return "", false
		}
	}
	// THE ARGUMENTS ARE A RUN, exactly as a direct call's are. This was the last combining
	// form in either compiler still handing its operands to one C construct and inheriting
	// C's unspecified order (docs/core/memory.md), and the two move together: `make oracle`
	// compares them on a program that can tell.
	//
	// A `mut &` argument is skipped, for the same reason a direct call skips it — an address
	// is not an operand the order can be told apart by.
	byref := make([]bool, len(n.Args))
	for i := range n.Args {
		byref[i] = i < len(ft.Params) && ft.Params[i].ByRef
	}
	pre, undo := e.orderOperands(argExprs(nil, n.Args), byref)
	defer undo()
	var args strings.Builder
	for i, a := range n.Args {
		if i > 0 {
			args.WriteString(", ")
		}
		argT := e.cur.ExprType(e.info, a.Value)
		if byref[i] {
			args.WriteString(e.addressOf(a.Value))
			continue
		}
		args.WriteString(e.copyValue(argT, a.Value))
	}
	return orderedForm(pre, "("+e.fnPtrCast(ft)+e.expr(n.Callee)+")("+args.String()+")"), true
}
