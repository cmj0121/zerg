package emit

import (
	"fmt"
	"strings"

	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/mono"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// This file is the Phase 1c '#[dyn]' backend (DESIGN-1c §6, U7): the witness-table
// struct types, the concrete witness tables, the erased shared body, the impl
// methods a witness points at, and the call-site dispatch. A '#[dyn]' generic's
// type parameter is erased to 'const void*' and its bound methods are called
// through a witness pointer, so one body serves every type argument.

// witnessStructs emits, once per spec that any witness uses, the witness struct
// type: one function-pointer field per method the spec (and its super-specs)
// provides, each taking an opaque 'const void*' receiver.
func (e *emitter) witnessStructs() {
	seen := map[string]bool{}
	for _, wit := range e.prog.Witnesses {
		if seen[wit.Struct] {
			continue
		}
		seen[wit.Struct] = true
		e.line("typedef struct {")
		e.indent++
		for _, m := range e.specMethods(wit) {
			e.line(fmt.Sprintf("%s (*%s)(const void*);", e.ctype(orNilRet(m.Sig)), m.Name))
		}
		e.indent--
		e.line("} " + wit.Struct + ";")
		e.blank()
	}
}

// witnessGlobals emits each concrete witness table as a static const global whose
// slots hold the impl methods' addresses, in the spec's method order.
func (e *emitter) witnessGlobals() {
	if len(e.prog.Witnesses) == 0 {
		return
	}
	for _, wit := range e.prog.Witnesses {
		fns := make([]string, len(wit.Slots))
		for i, s := range wit.Slots {
			fns[i] = s.Fn
		}
		e.line(fmt.Sprintf("static const %s %s = { %s };", wit.Struct, wit.Global, strings.Join(fns, ", ")))
	}
	e.blank()
}

// specMethods returns a witness's spec methods in layout order (the spec's own
// methods, then each super-spec's), matching the slot order the worker built.
func (e *emitter) specMethods(wit *mono.Witness) []*types.SpecMethod {
	var out []*types.SpecMethod
	if e.info.Specs == nil {
		return out
	}
	for _, sp := range e.info.Specs.SpecClosure(wit.Spec) {
		out = append(out, sp.Methods...)
	}
	return out
}

// erasedSignature renders the C signature of an erased instance — a '#[dyn]' body
// or a witness impl method. A dyn body erases its type-parameter parameters to
// 'const void*' and appends a witness pointer; a witness method takes an opaque
// receiver first. With defn true the parameters are named (for a definition),
// otherwise types only (for a prototype), matching the ordinary path's spelling.
func (e *emitter) erasedSignature(inst *mono.Instance, defn bool) string {
	var params []string
	name := func(fallback string) string {
		if defn {
			return " " + fallback
		}
		return ""
	}
	if inst.RecvErased {
		params = append(params, "const void*"+name(e.selfPtrName()))
	}
	for i, pt := range inst.Params {
		decl := e.paramType(pt)
		if inst.Dyn && i < len(inst.Erased) && inst.Erased[i] {
			decl = "const void*"
		}
		if defn {
			decl += " " + e.declareName(inst.ParamNames[i])
		}
		params = append(params, decl)
	}
	if inst.Dyn && inst.DynSpec != nil {
		params = append(params, "const zgs_"+inst.DynSpec.Name+"*"+name(inst.DynParam))
	}
	return fmt.Sprintf("%s %s(%s)", e.ctype(inst.Ret), inst.Mangled, strings.Join(params, ", "))
}

// selfPtrName is the C name of a witness method's opaque receiver pointer.
func (e *emitter) selfPtrName() string { return "zg_self" }

// dynFunction emits a '#[dyn]' shared body: an erased signature (type parameters as
// 'const void*', a trailing witness pointer) whose body dispatches bound-method
// calls through the witness (see dynDispatch).
func (e *emitter) dynFunction(inst *mono.Instance) {
	e.cur = inst
	e.used = map[string]bool{}
	e.counter = 0
	e.pushScope()
	// pre-reserve the witness pointer name so it cannot clash with a parameter
	e.used["zg_"+inst.DynParam] = true
	sig := e.erasedSignature(inst, true)
	e.line(sig + " {")
	e.indent++
	e.pushScope()
	for _, s := range inst.Origin.Body.Stmts {
		e.stmt(s)
	}
	e.popScope()
	if inst.Ret != types.Nil && !endsWithReturn(inst.Origin.Body) {
		e.line("return " + e.zeroValueC(inst.Ret) + ";")
	}
	e.indent--
	e.line("}")
	e.popScope()
}

// methodFunction emits a witness impl method: an opaque 'const void*' receiver cast
// to the concrete type in a prologue, then the ordinary body — so a field access
// 'this.f' renders against a by-value receiver exactly as a normal method would.
func (e *emitter) methodFunction(inst *mono.Instance) {
	e.cur = inst
	e.used = map[string]bool{}
	e.counter = 0
	e.pushScope()
	this := e.declareName("this")
	sig := e.erasedSignature(inst, true)
	e.line(sig + " {")
	e.indent++
	rc := e.ctype(inst.Recv)
	e.line(fmt.Sprintf("%s %s = *(const %s*)%s;", rc, this, rc, e.selfPtrName()))
	e.pushScope()
	if inst.Origin != nil && inst.Origin.Body != nil {
		for _, s := range inst.Origin.Body.Stmts {
			e.stmt(s)
		}
	}
	e.popScope()
	if inst.Ret != types.Nil && !endsWithReturn(inst.Origin.Body) {
		e.line("return " + e.zeroValueC(inst.Ret) + ";")
	}
	e.indent--
	e.line("}")
	e.popScope()
}

// dynDispatch lowers a bound-method call inside a '#[dyn]' body — 'x.method(args)'
// on an erased type parameter — to a witness call 'w->method(x, args)'. It returns
// "" when the call is not such a dispatch (a normal call falls through).
func (e *emitter) dynDispatch(n *ast.Call) string {
	field, ok := n.Callee.(*ast.Field)
	if !ok {
		return ""
	}
	recv, ok := field.X.(*ast.Ident)
	if !ok || !e.isErasedParam(recv.Name) {
		return ""
	}
	args := []string{"(const void*)" + e.resolve(recv.Name)}
	for _, a := range n.Args {
		args = append(args, e.expr(a.Value))
	}
	return fmt.Sprintf("%s->%s(%s)", e.cur.DynParam, field.Name, strings.Join(args, ", "))
}

// isErasedParam reports whether a name is one of the current dyn body's erased
// (opaque) type-parameter parameters.
func (e *emitter) isErasedParam(name string) bool {
	for i, pn := range e.cur.ParamNames {
		if pn == name {
			return i < len(e.cur.Erased) && e.cur.Erased[i]
		}
	}
	return false
}

// dynCallSite lowers a call to a '#[dyn]' function: each erased argument is
// address-taken to a 'const void*', and the concrete witness is passed last.
func (e *emitter) dynCallSite(n *ast.Call, site *mono.DynSite) string {
	var args []string
	for i, a := range n.Args {
		if i < len(site.Erased) && site.Erased[i] {
			args = append(args, "(const void*)&("+e.expr(a.Value)+")")
		} else {
			args = append(args, e.expr(a.Value))
		}
	}
	if site.Witness != nil {
		args = append(args, "&"+site.Witness.Global)
	}
	return fmt.Sprintf("%s(%s)", site.Callee, strings.Join(args, ", "))
}

// orNilRet returns a spec method's return type, defaulting to the nil type when the
// signature has none.
func orNilRet(sig *types.Fn) types.Type {
	if sig == nil || sig.Ret == nil {
		return types.Nil
	}
	return sig.Ret
}
