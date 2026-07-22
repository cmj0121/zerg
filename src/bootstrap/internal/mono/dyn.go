package mono

import (
	"github.com/cmj0121/zerg/src/bootstrap/internal/ast"
	"github.com/cmj0121/zerg/src/bootstrap/internal/sema"
	"github.com/cmj0121/zerg/src/bootstrap/internal/types"
)

// This file is the Phase 1c '#[dyn]' lowering (DESIGN-1c §6, U6). A '#[dyn]'
// generic is NOT monomorphized per type argument; it compiles to one shared,
// type-erased body that dispatches its bound spec's methods through a witness
// table (a struct of function pointers). At each concrete call site the worker
// records the witness to pass — building, once per (spec, type) pair, a witness
// whose slots are the impl methods for that type — and the call site erases its
// type-parameter argument to an opaque pointer.

// dynCall lowers a call to a '#[dyn]' function: it ensures the single erased body
// exists, resolves the concrete witness for the call's type argument, and records
// the dispatch on the caller instance (DESIGN-1c §6.2). Unlike an ordinary generic
// call it enqueues no per-type specialization of the callee.
func (w *worker) dynCall(in *Instance, sig *sema.FuncSig, n *ast.Call) {
	dyn := w.enqueueDyn(sig)
	spec := dynBoundSpec(sig)
	exprs := pairArgs(sig, n)

	erased := make([]bool, len(sig.Params))
	var concrete types.Type
	for i, p := range sig.Params {
		if _, ok := p.(*types.Param); ok {
			erased[i] = true
			if concrete == nil && exprs[i] != nil {
				concrete = in.ExprType(w.info, exprs[i])
			}
		}
	}

	var wit *Witness
	if spec != nil && concrete != nil {
		wit = w.buildWitness(spec, concrete)
	}
	in.DynSites[n] = &DynSite{Callee: dyn.Mangled, Witness: wit, Erased: erased}
}

// enqueueDyn creates (once) the single erased body of a '#[dyn]' function: its
// type-parameter parameters are marked erased (rendered 'const void*') and it
// carries the bound spec so the emitter appends a witness pointer (DESIGN-1c §6.1).
func (w *worker) enqueueDyn(sig *sema.FuncSig) *Instance {
	mangled := "zgd_" + sig.Name
	if in, ok := w.prog.byMangled[mangled]; ok {
		return in
	}
	in := &Instance{
		Origin:      sig.Decl,
		Mangled:     mangled,
		ParamNames:  sig.ParamNames,
		Params:      make([]types.Type, len(sig.Params)),
		Ret:         orNil(sig.Ret),
		Dyn:         true,
		DynSpec:     dynBoundSpec(sig),
		Erased:      make([]bool, len(sig.Params)),
		DynParam:    "w",
		Calls:       map[*ast.Call]string{},
		DynSites:    map[*ast.Call]*DynSite{},
		MethodCalls: map[*ast.Call]*MethodDispatch{},
		OpCalls:     map[*ast.Binary]*MethodDispatch{},
	}
	for i, p := range sig.Params {
		in.Params[i] = p
		if _, ok := p.(*types.Param); ok {
			in.Erased[i] = true
		}
	}
	w.prog.byMangled[mangled] = in
	w.prog.Funcs = append(w.prog.Funcs, in)
	w.queue = append(w.queue, in)
	return in
}

// buildWitness builds (once) the concrete witness table for a (spec, type) pair:
// it resolves the impl, enqueues each spec method's impl body as an erased-receiver
// instance, and fills one slot per method with that body's mangled name (DESIGN-1c
// §6.2). Every witness of the same spec shares a witness struct type.
func (w *worker) buildWitness(spec *types.SpecDef, concrete types.Type) *Witness {
	global := witnessGlobalName(spec.Name, concrete)
	if wit, ok := w.prog.witByGlobal[global]; ok {
		return wit
	}
	wit := &Witness{Global: global, Struct: witnessStructName(spec.Name), Spec: spec}
	w.prog.witByGlobal[global] = wit

	reg := w.info.Specs
	if reg == nil {
		w.prog.Witnesses = append(w.prog.Witnesses, wit)
		return wit
	}
	for _, sp := range reg.SpecClosure(spec) {
		impl := reg.ResolveImpl(sp, concrete)
		if impl == nil {
			continue
		}
		for _, m := range sp.Methods {
			im := impl.Methods[m.Name]
			if im == nil {
				continue
			}
			inst := w.enqueueMethod(concrete, im)
			wit.Slots = append(wit.Slots, WitnessSlot{Method: m.Name, Fn: inst.Mangled})
		}
	}
	w.prog.Witnesses = append(w.prog.Witnesses, wit)
	return wit
}

// enqueueMethod creates (once) the C body of an impl method used by a witness: an
// erased-receiver instance whose receiver is passed as 'const void*' and cast to
// the concrete type in a prologue (DESIGN-1c §6.1). Its own parameters keep their
// concrete types.
func (w *worker) enqueueMethod(concrete types.Type, m *types.ImplMethod) *Instance {
	fn, _ := m.Decl.(*ast.FuncDecl)
	mangled := "zge_" + typeCode(concrete) + "_" + m.Name
	if in, ok := w.prog.byMangled[mangled]; ok {
		return in
	}
	in := &Instance{
		Origin:      fn,
		Mangled:     mangled,
		Recv:        concrete,
		RecvErased:  true,
		Ret:         orNil(m.Sig.Ret),
		Calls:       map[*ast.Call]string{},
		DynSites:    map[*ast.Call]*DynSite{},
		MethodCalls: map[*ast.Call]*MethodDispatch{},
		OpCalls:     map[*ast.Binary]*MethodDispatch{},
	}
	if fn != nil {
		for _, p := range fn.Params {
			in.ParamNames = append(in.ParamNames, p.Name)
		}
	}
	for _, p := range m.Sig.Params {
		in.Params = append(in.Params, p.Type)
	}
	w.prog.byMangled[mangled] = in
	w.prog.Funcs = append(w.prog.Funcs, in)
	w.queue = append(w.queue, in)
	return in
}

// dynBoundSpec returns the spec a dyn function's single type parameter is bound by
// — the spec whose methods the witness table dispatches. It is the primary
// (non-super) bound: '[T: Ord]' yields Ord (super Eq is reached through the
// closure at witness-build time).
func dynBoundSpec(sig *sema.FuncSig) *types.SpecDef {
	if sig.Generic == nil {
		return nil
	}
	for _, name := range sig.Generic.Names {
		if p, ok := sig.Generic.Params[name].(*types.Param); ok && len(p.Bounds) > 0 {
			return p.Bounds[0]
		}
	}
	return nil
}

// orNil returns t, or the nil type when t is nil, so an instance's return type is
// always set.
func orNil(t types.Type) types.Type {
	if t == nil {
		return types.Nil
	}
	return t
}
